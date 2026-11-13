package control

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lamp.eilmer.si/internal/surface"
	"lamp.eilmer.si/pkg/api"
	pb "lamp.eilmer.si/pkg/workerpb"
)

// Use the repository's worker protocol boundary; no VM or owner state is used.
func t34ReviewWorker(t *testing.T, config *Config, beforeReply func(*pb.Request)) {
	t.Helper()
	dir, err := os.MkdirTemp("", "t34-worker-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	listener, err := net.Listen("unix", filepath.Join(dir, "worker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	config.Worker.Socket = listener.Addr().String()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				frame, err := pb.ReadFrame(conn)
				if err != nil {
					return
				}
				if err := pb.WriteFrame(conn, &pb.Frame{Payload: &pb.Frame_Hello{Hello: frame.GetHello()}}); err != nil {
					return
				}
				for {
					frame, err := pb.ReadFrame(conn)
					if err != nil {
						return
					}
					request := frame.GetRequest()
					if request == nil {
						return
					}
					if beforeReply != nil {
						beforeReply(request)
					}
					receipt := &pb.Receipt{Detail: map[string]string{"capture_trust": "clean", "custody_host_ids": "[\"hs_local\"]"}}
					switch request.Command.(type) {
					case *pb.Request_Allocate:
						receipt.State = "allocated"
					case *pb.Request_Restore:
						receipt.State = "restored"
					case *pb.Request_Start:
						receipt.State = "command_started"
					case *pb.Request_Capture:
						receipt.State = "captured"
					case *pb.Request_Stop:
						receipt.State = "stopped"
					case *pb.Request_Release:
						receipt.State = "released"
					case *pb.Request_Inspect:
						receipt.State = "command_started"
						receipt.Detail["generation"] = fmt.Sprint(request.Context.Generation)
						receipt.Command = &pb.CommandResult{StartedAtUnixNs: 1, FinishedAtUnixNs: 2}
					}
					if err := pb.WriteFrame(conn, &pb.Frame{Payload: &pb.Frame_Response{Response: &pb.Response{Context: request.Context, Result: &pb.Response_Receipt{Receipt: receipt}}}}); err != nil {
						return
					}
				}
			}()
		}
	}()
}

func t34ReviewCall(t *testing.T, s *Service, name string, input map[string]any) map[string]any {
	t.Helper()
	server := httptest.NewServer(s)
	defer server.Close()
	client := surface.Client{URL: server.URL}
	defer client.Close()
	raw, status, err := client.Call(context.Background(), surface.ByName[name], input)
	if err != nil || status != 200 && status != 202 {
		t.Fatalf("%s: status=%d err=%v body=%s", name, status, err, raw)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestT34ReviewRecoveredSandboxSurvivesDaemonRestart(t *testing.T) {
	config := storeConfig(t)
	t34ReviewWorker(t, &config, nil)
	// Engine import is the external privileged boundary. The fixture returns a
	// valid handle; encryption, restore admission, resume and replay are real.
	admin := filepath.Join(t.TempDir(), "state-admin")
	if err := os.WriteFile(admin, []byte("#!/bin/sh\nprintf '%s\\n' '{\"ok\":true,\"capture_id\":\"cp_recovered\"}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	config.StateAdmin = []string{admin}
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()

	var bundle bytes.Buffer
	gz := gzip.NewWriter(&bundle)
	tw := tar.NewWriter(gz)
	for _, name := range []string{"manifest.json", "mem", "vmstate", "disk"} {
		content := []byte("fixture")
		if name == "manifest.json" {
			content = []byte(`{"capture_id":"cp_original"}`)
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	sum, err := s.putObject(bundle.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	original := newSandbox()
	original.State = "suspended"
	original.Resources = &api.Resources{Vcpus: 1, MemoryMib: 128, DiskGib: 1, PidsMax: 128}
	header := backupHeader{Format: "lamp.backup/v1", BackupID: "bk_fixture", Kind: "sandbox", Resource: object(original), SavedSandbox: &lifeSandbox{Capture: "cp_original"}, EngineBundle: sum, Objects: []string{sum}, Epoch: 1}
	file := filepath.Join(config.StateDir, "backups", "bk_fixture.lbk")
	digest, _, err := s.sealBackup(header, file)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	resource := s.lifeResource("bk", "backup", "pr_local")
	resource["id"] = "bk_fixture"
	resource["state"] = "ready"
	resource["manifest_digest"] = digest
	resource["authority_epoch"] = jsonNumber(1)
	s.life.Backups["bk_fixture"] = &lifeBackup{R: resource, File: file}
	if err := s.saveLife(); err != nil {
		t.Fatal(err)
	}
	s.mu.Unlock()
	restored := t34ReviewCall(t, s, "backup_restore", map[string]any{"backup_id": "bk_fixture", "expected_revision": 1, "idempotency_key": "recover", "wait_ms": 1000})
	refs := restored["resource_ids"].([]any)
	id := refs[len(refs)-1].(map[string]any)["id"].(string)
	sb := t34ReviewCall(t, s, "sandbox_get", map[string]any{"sandbox_id": id})["data"].(map[string]any)
	t34ReviewCall(t, s, "sandbox_resume", map[string]any{"sandbox_id": id, "expected_revision": sb["revision"], "idempotency_key": "resume", "wait_ms": 1000})
	sb = t34ReviewCall(t, s, "sandbox_get", map[string]any{"sandbox_id": id})["data"].(map[string]any)
	if sb["state"] != "ready" {
		t.Fatalf("resume: %v", sb)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	// A lifecycle pass must discover recovery work even when this prefix has
	// only backup_restore and sandbox_resume operations, and no create record.
	s.lifeTickOnce()
	s.mu.Lock()
	current := s.journal.Sandboxes[id]
	scheduled := s.life.Sandboxes[id].reconciling
	s.mu.Unlock()
	if current.State != "ready" && !scheduled {
		t.Fatalf("recovered sandbox after restart: state=%s; no reconciliation scheduled (only backup_restore/sandbox_resume history)", current.State)
	}
	// If recovery was scheduled, require the public exec entrypoint to work.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for current.State != "ready" {
		select {
		case <-ctx.Done():
			t.Fatal("recovered sandbox never became ready")
		default:
		}
		s.mu.Lock()
		current = s.journal.Sandboxes[id]
		s.mu.Unlock()
	}
	t34ReviewCall(t, s, "exec_create", map[string]any{"sandbox_id": id, "argv": []string{"/bin/true"}, "idempotency_key": "after-restart", "wait_ms": 1000})
}

func TestT34ReviewSnapshotDeleteDuringCapture(t *testing.T) {
	config := storeConfig(t)
	capturing, release := make(chan struct{}), make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	t34ReviewWorker(t, &config, func(request *pb.Request) {
		if request.GetCapture() != nil {
			close(capturing)
			<-release
		}
	})
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	// Release the worker before closing the service if an assertion fails.
	defer func() {
		if !released {
			close(release)
			released = true
		}
		s.Close()
	}()
	source := newSandbox()
	source.State = "ready"
	source.Resources = &api.Resources{Vcpus: 1, MemoryMib: 128, DiskGib: 1, PidsMax: 128}
	s.mu.Lock()
	s.bindDefaultTask(caller{Principal: "pr_local"}, &source)
	s.journal.Sandboxes[source.Id] = source
	if err := s.save(source.Id, "$accountability"); err != nil {
		t.Fatal(err)
	}
	s.mu.Unlock()
	accepted := t34ReviewCall(t, s, "snapshot_create", map[string]any{"sandbox_id": source.Id, "idempotency_key": "capture"})
	id := accepted["resource_ids"].([]any)[0].(map[string]any)["id"].(string)
	<-capturing
	server := httptest.NewServer(s)
	defer server.Close()
	client := surface.Client{URL: server.URL}
	defer client.Close()
	raw, status, err := client.Call(context.Background(), surface.ByName["snapshot_delete"], map[string]any{"snapshot_id": id, "expected_revision": 1, "idempotency_key": "delete-during-capture"})
	if err != nil {
		t.Fatal(err)
	}
	if status == 409 && bytes.Contains(raw, []byte("RESOURCE_IN_USE")) {
		return
	}
	if status != 200 {
		t.Fatalf("delete: status=%d body=%s", status, raw)
	}
	close(release)
	released = true
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.lifeWait(ctx, accepted["operation_id"].(string)); err != nil {
		t.Fatal(err)
	}
	listed := t34ReviewCall(t, s, "snapshot_list", map[string]any{})
	for _, item := range listed["items"].([]any) {
		snapshot := item.(map[string]any)
		if snapshot["id"] == id {
			t.Fatalf("snapshot_delete succeeded, but capture completion resurrected snapshot: state=%v revision=%v", snapshot["state"], snapshot["revision"])
		}
	}
}
