package control_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/JohanSardar/lamp/pkg/api"
)

// This is a real two-language transport test, NOT a microVM or fork oracle. The
// confined-engine implementation belongs to T2; this fixture executes only the
// test's fixed commands in its temporary directory on the host.
func TestRustWorkerControlPath(t *testing.T) {
	build := exec.Command("cargo", "build", "--manifest-path", "testdata/worker/Cargo.toml", "--target-dir", "../../target/control-test-worker", "--locked")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build Rust protocol fixture: %v\n%s", err, out)
	}
	dir, err := os.MkdirTemp("", "lamp-rust-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "worker.sock")
	fixture := exec.Command("../../target/control-test-worker/debug/lamp-control-worker-fixture", socket, dir, strconv.Itoa(os.Getuid()))
	var log bytes.Buffer
	fixture.Stdout = &log
	fixture.Stderr = &log
	if err := fixture.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		fixture.Process.Kill()
		fixture.Wait()
		if t.Failed() {
			t.Logf("Rust fixture diagnostics: %s", log.String())
		}
	}()
	until := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if time.Now().After(until) {
			t.Fatal("Rust fixture did not bind socket")
		}
		time.Sleep(time.Millisecond)
	}
	d := startDaemonFor(t, dir, nil, socket, fixture.Process.Pid)
	defer d.stop()
	var created api.SandboxCreateResult
	d.request(t, "POST", "/v1/sandboxes", `{"template_id":"tpl_demo","wait_ms":1000}`, 200, &created, "Idempotency-Key", "rust-create")
	if created.Result == nil {
		t.Fatal("missing sandbox completion")
	}
	var sb api.SandboxGetResult
	d.request(t, "GET", "/v1/sandboxes/"+created.Result.SandboxId, "", 200, &sb)
	if sb.Data.Readiness == nil || sb.Data.Readiness.CommandStartedAt == nil {
		t.Fatal("no command readiness")
	}
	marker := filepath.Join(dir, "independent-command-marker")
	// The worker fixture allows only /bin/true and this exact fixed marker command.
	out, err := exec.Command(lampBinary, "exec", "create", "--socket", d.socket, "--sandbox-id", sb.Data.Id, "--idempotency-key", "rust-exec", "--wait-ms", "1000", "--json", "--", "/bin/sh", "-c", "printf control-path-ok > \"$1\"", "fixture", marker).CombinedOutput()
	if err != nil {
		t.Fatalf("CLI to Rust exec: %v %s", err, out)
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "control-path-ok" {
		t.Fatalf("independent marker: %q %v", data, err)
	}
	var executed api.ExecCreateResult
	if err := json.Unmarshal(out, &executed); err != nil {
		t.Fatal(err)
	}
	forkOut, err := exec.Command(lampBinary, "sandbox", "fork", "--socket", d.socket, "--sandbox-id", sb.Data.Id, "--expected-revision", strconv.FormatUint(sb.Data.Revision, 10), "--idempotency-key", "rust-fork", "--wait-ms", "1000", "--json").CombinedOutput()
	if err != nil {
		t.Fatalf("CLI to Rust fork: %v %s", err, forkOut)
	}
	var forked api.SandboxForkResult
	if err := json.Unmarshal(forkOut, &forked); err != nil || forked.Result == nil {
		t.Fatalf("fork completion %s %v", forkOut, err)
	}
	if _, err := os.Stat(filepath.Join(dir, forked.Result.SandboxId+".started")); err != nil {
		t.Fatal("fork child never ran a probe")
	}
	// Exercise the frozen independent caller unchanged, including its exact argv
	// construction, output cursor loop, local fork durability, and cleanup.
	harness := exec.Command("python3", "-c", `
import os, sys
sys.path.insert(0, "../..")
from benchmarks.public import PublicLamp
lamp = PublicLamp(cli=sys.argv[1], timeout=5)
resources = {"vcpus":2,"memory_mib":4096,"disk_gib":20,"pids_max":4096}
created = lamp.mutation("sandbox", "create", {"template_id":"tpl_demo", "resources":resources, "durability":"local"})["sandbox_id"]
observed = lamp.get(created)
assert observed["resources"] == resources and observed["durability"] == "local" and observed["boundary"] == "vm"
assert lamp.execute(created, ["/bin/echo", '{"fixture":"ok"}']) == {"fixture":"ok"}
child = lamp.mutation("sandbox", "fork", {"sandbox_id":created,"expected_revision":lamp.get(created)["revision"],"fork_mode":"memory","durability":"local"})["sandbox_id"]
assert lamp.get(child)["resources"] == resources and lamp.get(child)["durability"] == "local"
lamp.delete(child)
lamp.delete(created)
assert lamp.get(child)["state"] == "deleted" and lamp.get(created)["state"] == "deleted"
print("unchanged PublicLamp CLI caller: create, exec completion/output, local fork, delete passed")
`, lampBinary)
	harness.Env = append(os.Environ(), "LAMP_SOCKET="+d.socket)
	if output, err := harness.CombinedOutput(); err != nil {
		t.Fatalf("independent caller: %v: %s", err, output)
	} else {
		t.Log(string(output))
	}
	t.Logf("CLI -> HTTP/Unix -> lampd -> lamp.worker/1 -> Rust process: create=%s exec=%s fork=%s; independent command marker=%s", created.Result.SandboxId, executed.Result.ExecId, forked.Result.SandboxId, data)
	if evidence := os.Getenv("LAMP_CONTROL_EVIDENCE_DIR"); evidence != "" {
		if err := os.MkdirAll(evidence, 0700); err != nil {
			t.Fatal(err)
		}
		files := map[string]any{"SandboxCreateResult": created, "SandboxGetResult": sb, "ExecCreateResult": executed, "SandboxForkResult": forked, "OperationGetResult": api.OperationGetResult{Data: d.operation(t, created.OperationId), RequestId: "req_evidence"}}
		for name, value := range files {
			data, err := json.MarshalIndent(value, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(evidence, name+".json"), append(data, '\n'), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(evidence, "marker.txt"), []byte(fmt.Sprintf("independent host-fixture command: %s\nNo microVM or memory fork performance claim.\n", data)), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
