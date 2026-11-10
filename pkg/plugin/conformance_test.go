package plugin_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/JohanSardar/lamp/pkg/plugin"
)

// External-service fixtures, not substitutes for the real process/sandbox/grant
// boundary. Vendor, engine and guest qualification belong to their own tickets.
type services struct {
	mu              sync.Mutex
	authorized      bool
	kind, dir, echo string
	ram             []byte
	delivered       string
	stopped         bool
}

func (s *services) Effect(ctx context.Context, e plugin.Effect) (json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.authorized || s.stopped || e.Binding.OwnerID != "owner" || e.Binding.TaskID != "task" || e.Binding.ParentGrant != "parent" || e.Binding.Generation != 1 || e.ResourceID != "assigned" {
		return nil, errors.New("current core policy denied")
	}
	var input struct {
		Payload string `json:"payload"`
	}
	if json.Unmarshal(e.Input, &input) != nil {
		return nil, errors.New("invalid input")
	}
	switch s.kind {
	case "engine":
		if e.Kind != "ram" {
			return nil, errors.New("RAM grant required")
		}
		copy(s.ram, []byte(input.Payload))
		s.delivered = string(s.ram)
	case "network":
		c, err := (&net.Dialer{}).DialContext(ctx, "tcp", s.echo)
		if err != nil {
			return nil, err
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(time.Second))
		if _, err = c.Write([]byte(input.Payload)); err != nil {
			return nil, err
		}
		b := make([]byte, len(input.Payload))
		if _, err = io.ReadFull(c, b); err != nil {
			return nil, err
		}
		s.delivered = string(b)
	case "credential_source":
		// Host-held signing key never enters the worker or its result; fixture
		// broker forwards the signature to its own verifier/sink.
		key := []byte("host-held-fixture-key")
		mac := hmac.New(sha256.New, key)
		mac.Write([]byte(input.Payload))
		s.delivered = hex.EncodeToString(mac.Sum(nil))
	case "harness", "kit":
		// Installation/continuation plans, not execution on an unqualified guest.
		if err := os.WriteFile(filepath.Join(s.dir, s.kind+"-plan"), []byte(input.Payload), 0600); err != nil {
			return nil, err
		}
		b, err := os.ReadFile(filepath.Join(s.dir, s.kind+"-plan"))
		if err != nil {
			return nil, err
		}
		s.delivered = string(b)
	case "ci", "trigger":
		if err := os.Mkdir(filepath.Join(s.dir, "scoped-job"), 0700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(s.dir, "scoped-job", "input"), []byte(input.Payload), 0600); err != nil {
			return nil, err
		}
		s.delivered = input.Payload
	case "console", "access":
		reader, writer := io.Pipe()
		go func() { _, _ = io.WriteString(writer, input.Payload); writer.Close() }()
		b, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			return nil, err
		}
		s.delivered = string(b)
	case "notification_sink":
		f, err := os.OpenFile(filepath.Join(s.dir, "notification"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return nil, err
		}
		_, err = f.WriteString(input.Payload + "\n")
		f.Close()
		if err != nil {
			return nil, err
		}
		s.delivered = input.Payload
	default:
		return nil, errors.New("unknown fixture")
	}
	return json.RawMessage(`{"receipt":"delivered"}`), nil
}
func (s *services) Revoke(string)                { s.mu.Lock(); defer s.mu.Unlock(); s.stopped = true }
func (s *services) FailDependent(string, string) {}
func TestAllTenReferenceTypes(t *testing.T) {
	path := reference(t)
	bin, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	l, ld := launcher(t)
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); b := make([]byte, 256); n, _ := c.Read(b); _, _ = c.Write(b[:n]) }()
		}
	}()
	for _, kind := range []string{"engine", "network", "credential_source", "harness", "kit", "ci", "trigger", "console", "access", "notification_sink"} {
		t.Run(kind, func(t *testing.T) {
			m := fixtureManifest(kind)
			m.Digest = plugin.Digest(bin)
			m.Methods["run"] = plugin.Method{Input: plugin.Schema{Type: "object", Properties: map[string]plugin.Schema{"payload": {Type: "string", MaxLength: 256}}, Required: []string{"payload"}}, Result: plugin.Schema{Type: "object", Properties: map[string]plugin.Schema{"receipt": {Type: "string"}}, Required: []string{"receipt"}}}
			if kind == "engine" {
				m.TrustClass = "engine_trusted"
			}
			if kind == "credential_source" {
				m.TrustClass = "credential"
			}
			if kind == "network" || kind == "access" {
				m.TrustClass = "platform"
			}
			raw, _ := json.Marshal(m)
			pkg, err := plugin.Admit(raw, bin, plugin.Approval{OwnerID: "owner", ManifestDigest: plugin.Digest(raw), EngineWarning: plugin.EngineWarning})
			if err != nil {
				t.Fatal(err)
			}
			core := &services{authorized: true, kind: kind, dir: t.TempDir(), ram: make([]byte, 6), echo: echo.Addr().String()}
			w, err := plugin.Start(context.Background(), plugin.StartOptions{ID: "pg_" + kind, Package: pkg, Executable: path, StateDir: t.TempDir(), Launcher: l, LauncherDigest: ld, DiagnosticSoftLimits: true, Authority: core})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := w.Close(); err != nil {
					t.Error(err)
				}
			}()
			b := plugin.Binding{OwnerID: "owner", TaskID: "task", OperationID: "operation", ParentGrant: "parent", Generation: 1, Deadline: time.Now().Add(5 * time.Second)}
			grantKind := kind
			if kind == "engine" {
				grantKind = "ram"
			}
			grant, err := w.Grant(plugin.GrantRequest{Kind: grantKind, ResourceID: "assigned", Binding: b})
			if err != nil {
				t.Fatal(err)
			}
			result, err := w.Call(context.Background(), "run", b, grant, json.RawMessage(`{"payload":"useful"}`))
			if err != nil || !bytes.Equal(result, []byte(`{"receipt":"delivered"}`)) {
				t.Fatalf("authorized effect: %s %v", result, err)
			}
			want := "useful"
			if kind == "credential_source" {
				mac := hmac.New(sha256.New, []byte("host-held-fixture-key"))
				mac.Write([]byte("useful"))
				want = hex.EncodeToString(mac.Sum(nil))
			}
			core.mu.Lock()
			observed := core.delivered
			core.authorized = false
			core.mu.Unlock()
			if observed != want {
				t.Fatalf("useful effect missing: %q", observed)
			}
			if _, err := w.Call(context.Background(), "run", b, grant, json.RawMessage(`{"payload":"extra"}`)); err == nil {
				t.Fatal("core policy narrowing ignored")
			}
			w.RevokeGrant(grant.ID)
			if _, err := w.Call(context.Background(), "run", b, grant, json.RawMessage(`{"payload":"extra"}`)); err == nil {
				t.Fatal("revoked grant reused")
			}
			t.Log("separate confined worker: useful scoped effect, extra effect denied, grant revoked")
		})
	}
}
