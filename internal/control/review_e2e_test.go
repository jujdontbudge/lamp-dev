package control_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/JohanSardar/lamp/pkg/api"
)

func readySandboxFromCLI(t *testing.T, d *demo) api.SandboxGetResult {
	t.Helper()
	out, err := exec.Command(lampBinary, "sandbox", "create", "--socket", d.socket, "--template-id", "tpl_demo", "--idempotency-key", "seed", "--wait-ms", "1000", "--json").CombinedOutput()
	if err != nil {
		t.Fatalf("seed CLI: %v %s", err, out)
	}
	var created api.SandboxCreateResult
	if err := json.Unmarshal(out, &created); err != nil || created.Result == nil {
		t.Fatalf("seed completion: %v %s", err, out)
	}
	var sb api.SandboxGetResult
	d.request(t, "GET", "/v1/sandboxes/"+created.Result.SandboxId, "", 200, &sb)
	return sb
}

func TestInvalidUTF8IdempotencyKeyRejectedAcrossRestart(t *testing.T) {
	d := startDemo(t)
	close(d.worker.ready)
	for attempt := 0; attempt < 2; attempt++ {
		var response api.ErrorEnvelope
		d.request(t, "POST", "/v1/sandboxes", `{"template_id":"tpl_demo","wait_ms":1000}`, 400, &response, "Idempotency-Key", "caf\xe9")
		if response.Error.Code != "INVALID_ARGUMENT" {
			t.Fatalf("attempt %d: expected INVALID_ARGUMENT, got %+v", attempt, response.Error)
		}
		if len(d.worker.seen()) != 0 {
			t.Fatalf("attempt %d: invalid UTF-8 key reached the worker", attempt)
		}
		if attempt == 0 {
			d.stop()
			d = startDaemon(t, filepath.Dir(d.socket), d.worker)
		}
	}
}

func TestMutationIdempotencyKeyHeader(t *testing.T) {
	d := startDemo(t)
	close(d.worker.ready)
	sb := readySandboxFromCLI(t, d)
	routes := []struct{ name, path, body string }{
		{"create", "/v1/sandboxes", `{"template_id":"tpl_demo","wait_ms":1000}`},
		{"exec", "/v1/execs", fmt.Sprintf(`{"sandbox_id":%q,"argv":["/bin/true"],"wait_ms":1000}`, sb.Data.Id)},
		{"fork", "/v1/sandboxes/" + sb.Data.Id + ":fork", `{"wait_ms":1000}`},
	}
	for _, route := range routes {
		t.Run(route.name, func(t *testing.T) {
			headers := []string{"If-Match", strconv.FormatUint(sb.Data.Revision, 10)}
			before := len(d.worker.seen())
			d.request(t, "POST", route.path, route.body, 400, nil, headers...)
			legacyBody := route.body[:len(route.body)-1] + `,"idempotency_key":"legacy"}`
			d.request(t, "POST", route.path, legacyBody, 400, nil, headers...)
			d.request(t, "POST", route.path, legacyBody, 400, nil, append(headers, "Idempotency-Key", "legacy")...)
			d.request(t, "POST", route.path, route.body, 400, nil, append(headers, "Idempotency-Key", "a", "Idempotency-Key", "b")...)
			if len(d.worker.seen()) != before {
				t.Fatal("invalid key transport dispatched a worker effect")
			}
			headers = append(headers, "Idempotency-Key", "header-"+route.name)
			var first, replay api.MutationEnvelope
			d.request(t, "POST", route.path, route.body, 200, &first, headers...)
			n := len(d.worker.seen())
			d.request(t, "POST", route.path, route.body, 200, &replay, headers...)
			if !replay.Replayed || replay.OperationId != first.OperationId || len(d.worker.seen()) != n {
				t.Fatal("header-key replay did not deduplicate")
			}
		})
	}
}

func TestForkIfMatchHeader(t *testing.T) {
	d := startDemo(t)
	close(d.worker.ready)
	sb := readySandboxFromCLI(t, d)
	path := "/v1/sandboxes/" + sb.Data.Id + ":fork"
	revision := strconv.FormatUint(sb.Data.Revision, 10)
	before := len(d.worker.seen())
	d.request(t, "POST", path, `{}`, 400, nil, "Idempotency-Key", "missing")
	for i, value := range []string{"", "-1", "not-a-revision", "18446744073709551616", `"3"`, "*"} {
		d.request(t, "POST", path, `{}`, 400, nil, "Idempotency-Key", fmt.Sprintf("bad-%d", i), "If-Match", value)
	}
	d.request(t, "POST", path, `{}`, 400, nil, "Idempotency-Key", "duplicate", "If-Match", revision, "If-Match", revision)
	d.request(t, "POST", path, `{}`, 409, nil, "Idempotency-Key", "stale", "If-Match", "0")
	d.request(t, "POST", path, fmt.Sprintf(`{"expected_revision":%d}`, sb.Data.Revision), 400, nil, "Idempotency-Key", "disagree", "If-Match", "0")
	if len(d.worker.seen()) != before {
		t.Fatal("invalid or stale precondition dispatched a capture")
	}
	var forked api.SandboxForkResult
	d.request(t, "POST", path, `{"wait_ms":1000}`, 200, &forked, "Idempotency-Key", "canonical-fork", "If-Match", revision)
	if forked.Result == nil {
		t.Fatal("header-only canonical fork omitted completion")
	}
	d.request(t, "POST", path, `{}`, 409, nil, "Idempotency-Key", "stale-after-fork", "If-Match", revision)
}

func TestWorkerErrorMetadataSurvivesJournalAndHTTP(t *testing.T) {
	d := startDemo(t)
	data, err := os.ReadFile("../../contracts/public/schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Errors map[string]int `json:"x-lamp-errors"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	// Exercise all canonical codes through real worker -> journal -> HTTP paths.
	for code, status := range schema.Errors {
		for _, retryable := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/retryable=%t", code, retryable), func(t *testing.T) {
				d.worker.mu.Lock()
				d.worker.fail = code
				d.worker.retryable = retryable
				d.worker.mu.Unlock()
				key := fmt.Sprintf("%s-%t", code, retryable)
				var response api.ErrorEnvelope
				d.request(t, "POST", "/v1/sandboxes", `{"template_id":"t","wait_ms":1000}`, status, &response, "Idempotency-Key", key)
				if string(response.Error.Code) != code || response.Error.Retryable != retryable || response.Error.OperationId == nil {
					t.Fatalf("lost worker metadata: %+v", response.Error)
				}
				op := d.operation(t, *response.Error.OperationId)
				if op.Error == nil || string(op.Error.Code) != code || op.Error.Retryable != retryable {
					t.Fatalf("journal projection lost metadata: %+v", op.Error)
				}
			})
		}
	}
	d.worker.mu.Lock()
	d.worker.fail = "AUDIT_UNAVAILABLE"
	d.worker.retryable = true
	d.worker.mu.Unlock()
	body := `{"template_id":"t","wait_ms":1000}`
	var first api.ErrorEnvelope
	d.request(t, "POST", "/v1/sandboxes", body, 503, &first, "Idempotency-Key", "audit-restart")
	d.stop()
	before := len(d.worker.seen())
	restarted := startDaemon(t, filepath.Dir(d.socket), d.worker)
	var replay api.ErrorEnvelope
	restarted.request(t, "POST", "/v1/sandboxes", body, 503, &replay, "Idempotency-Key", "audit-restart")
	if replay.Error.Code != "AUDIT_UNAVAILABLE" || !replay.Error.Retryable || replay.Error.OperationId == nil || first.Error.OperationId == nil || *replay.Error.OperationId != *first.Error.OperationId || len(d.worker.seen()) != before {
		t.Fatalf("restart/replay lost worker metadata: %+v", replay.Error)
	}
}
