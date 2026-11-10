package control_test

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/JohanSardar/lamp/internal/control"
)

// This drives the independent Python E03 observer against real Go control and
// Rust RPC/command processes. It explicitly cannot certify the engine boundary.
func TestControlBoundaryDiagnostic(t *testing.T) {
	output := os.Getenv("LAMP_E03_DIAGNOSTIC_OUTPUT")
	if output == "" {
		t.Skip("set LAMP_E03_DIAGNOSTIC_OUTPUT to a new evidence directory; diagnostic only")
	}
	build := exec.Command("cargo", "build", "--manifest-path", "testdata/worker/Cargo.toml", "--target-dir", "../../target/control-test-worker", "--locked")
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatalf("Rust fixture: %v %s", e, out)
	}
	f := newCore(t, nil)
	f.server.Close()
	f.service.Close()
	socket := filepath.Join(filepath.Dir(f.config.StateDir), "rust.sock")
	worker := exec.Command("../../target/control-test-worker/debug/lamp-control-worker-fixture", socket, filepath.Dir(socket), strconv.Itoa(os.Getuid()))
	var log bytes.Buffer
	worker.Stderr = &log
	if e := worker.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() {
		worker.Process.Kill()
		worker.Wait()
		if t.Failed() {
			t.Log(log.String())
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, e := os.Stat(socket); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Rust socket not bound")
		}
		time.Sleep(time.Millisecond)
	}
	f.config.Worker.Socket = socket
	f.config.Worker.PID = worker.Process.Pid
	f.config.Worker.Timeout = 60 * time.Second
	var e error
	f.service, e = control.Open(f.config)
	if e != nil {
		t.Fatal(e)
	}
	f.server = httptest.NewServer(f.service)
	f.mutation(t, f.owner, "fleet_update", map[string]any{"fleet_id": "fl_local", "mode": "team", "expected_revision": 1})
	manifest := []map[string]string{}
	environment := os.Environ()
	for i := 0; i < 32; i++ {
		p, k := f.member(t, "member")
		created := f.call(t, k, "sandbox_create", map[string]any{"template_id": "tpl_demo", "idempotency_key": "prepare", "wait_ms": 1000}, 200)
		variable := "LAMP_E03_CALLER_" + strconv.Itoa(i)
		environment = append(environment, variable+"="+k)
		manifest = append(manifest, map[string]string{"principal_id": p, "sandbox_id": field(result(created), "sandbox_id"), "token_env": variable})
	}
	raw, _ := json.Marshal(manifest)
	manifestPath := filepath.Join(filepath.Dir(f.config.StateDir), "callers.json")
	os.WriteFile(manifestPath, raw, 0600)
	observer := exec.Command("python3", "-c", `
import json,sys
sys.path.insert(0,"../..")
from benchmarks.control import control_suite
from benchmarks.evidence import Evidence
output,url,manifest=sys.argv[1:]
e=Evidence(output,{}, {"suite":"E03","qualification":"diagnostic Rust protocol fixture","owner_services":"untouched"})
r=control_suite(url,None,json.load(open(manifest)),e,smoke=True)
s=e.finish({"E03":r},diagnostic=True)
assert r["failures"]==0, r
assert s["status"]!="pass", s
print(json.dumps(s["results"]["E03"],sort_keys=True))
`, output, f.server.URL, manifestPath)
	observer.Env = environment
	out, e := observer.CombinedOutput()
	if e != nil {
		t.Fatalf("independent observer: %v %s", e, out)
	}
	t.Logf("diagnostic only: %s", out)
}
