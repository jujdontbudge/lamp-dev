package control_test

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/JohanSardar/lamp/pkg/api"
	pb "github.com/JohanSardar/lamp/pkg/workerpb"
)

func TestDurabilityRejectionHasNoEffect(t *testing.T) {
	d := startDemo(t)
	close(d.worker.ready)
	for _, durability := range []string{"replicated", "backup", "invalid"} {
		status, code := 503, api.ErrorCodeQUORUMREQUIRED
		if durability == "invalid" {
			status, code = 400, api.ErrorCodeINVALIDARGUMENT
		}
		var response api.ErrorEnvelope
		d.request(t, "POST", "/v1/sandboxes", fmt.Sprintf(`{"template_id":"t","durability":%q}`, durability), status, &response, "Idempotency-Key", durability)
		if response.Error.Code != code || len(d.worker.seen()) != 0 {
			t.Fatalf("rejected durability had an effect: %+v", response)
		}
	}
	out, err := exec.Command(lampBinary, "sandbox", "create", "--socket", d.socket, "--template-id", "t", "--durability", "replicated", "--json").CombinedOutput()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 1 {
		t.Fatalf("HTTP failure did not exit 1: %v %s", err, out)
	}
	var response api.ErrorEnvelope
	if json.Unmarshal(out, &response) != nil || response.Error.Code != "QUORUM_REQUIRED" || len(d.worker.seen()) != 0 {
		t.Fatalf("CLI durability refusal: %s", out)
	}

}

func TestDeleteFailedAndUnavailableSandboxes(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		t.Run(fmt.Sprint(unavailable), func(t *testing.T) {
			d := startDemo(t)
			close(d.worker.ready)
			if !unavailable {
				d.worker.mu.Lock()
				d.worker.fail = "NO_CAPACITY"
				d.worker.mu.Unlock()
			}
			var accepted api.MutationEnvelope
			d.request(t, "POST", "/v1/sandboxes", `{"template_id":"t"}`, 202, &accepted, "Idempotency-Key", "seed-delete")
			if unavailable {
				d.await(t, accepted.OperationId, "succeeded")
			} else {
				until := time.Now().Add(time.Second)
				for d.operation(t, accepted.OperationId).State != "failed" {
					if time.Now().After(until) {
						t.Fatal("create never failed")
					}
					time.Sleep(time.Millisecond)
				}
			}
			d.worker.mu.Lock()
			d.worker.fail = "NOT_FOUND"
			d.worker.mu.Unlock()
			if unavailable {
				d.stop()
				d = startDaemon(t, filepath.Dir(d.socket), d.worker)
			}
			id := accepted.ResourceIds[0].Id
			var sb api.SandboxGetResult
			d.request(t, "GET", "/v1/sandboxes/"+id, "", 200, &sb)
			if unavailable && sb.Data.State != "unavailable" {
				t.Fatal(sb.Data.State)
			}
			path := "/v1/sandboxes/" + id
			before := len(d.worker.seen())
			d.request(t, "DELETE", path, `{}`, 400, nil, "Idempotency-Key", "missing-revision")
			d.request(t, "DELETE", path, `{}`, 409, nil, "Idempotency-Key", "stale", "If-Match", "0")
			if len(d.worker.seen()) != before {
				t.Fatal("invalid delete reached worker")
			}
			headers := []string{"Idempotency-Key", "delete", "If-Match", strconv.FormatUint(sb.Data.Revision, 10)}
			var deleted, replay api.MutationEnvelope
			d.request(t, "DELETE", path, `{"wait_ms":1000}`, 200, &deleted, headers...)
			d.request(t, "DELETE", path, ``, 200, &replay, headers...)
			if deleted.Result == nil || replay.OperationId != deleted.OperationId || !replay.Replayed || len(d.worker.seen()) != before+2 {
				t.Fatal("delete lost completion, cleanup steps, or replay")
			}
			d.request(t, "GET", path, "", 200, &sb)
			if sb.Data.State != "deleted" {
				t.Fatal(sb.Data.State)
			}
			requests := d.worker.seen()[before:]
			if requests[0].GetStop() == nil || requests[1].GetRelease() == nil {
				t.Fatal("delete did not Stop then Release")
			}
		})
	}
}

func TestExecCompletionAndOutputQueries(t *testing.T) {
	d := startDemo(t)
	close(d.worker.ready)
	sb := readySandboxFromCLI(t, d)
	var created api.ExecCreateResult
	d.request(t, "POST", "/v1/execs", fmt.Sprintf(`{"sandbox_id":%q,"argv":["/bin/true"],"wait_ms":1000}`, sb.Data.Id), 200, &created, "Idempotency-Key", "output")
	id := created.Result.ExecId
	var page api.ExecWatchResult
	d.request(t, "GET", "/v1/execs/"+id+"/output?wait_ms=1000&limit=1", "", 200, &page)
	if page.Items == nil || page.NextCursor != nil {
		t.Fatal("completed empty output must terminate cursor")
	}
	for _, query := range []string{"wait_ms=60001", "limit=1001", "cursor=other:0", "limit=-1", "limit=1&limit=2", "surprise=1"} {
		d.request(t, "GET", "/v1/execs/"+id+"/output?"+query, "", 400, nil)
	}
	out, err := exec.Command(lampBinary, "exec", "get", id, "--socket", d.socket, "--json").CombinedOutput()
	if err != nil {
		t.Fatalf("positional exec get: %v %s", err, out)
	}
	var ex api.ExecGetResult
	if json.Unmarshal(out, &ex) != nil || ex.Data.State != "exited" || ex.Data.ExitCode == nil || *ex.Data.ExitCode != 0 {
		t.Fatalf("missing exit evidence: %s", out)
	}

}

func TestExecLostAfterStart(t *testing.T) {
	d := startDemo(t)
	close(d.worker.ready)
	sb := readySandboxFromCLI(t, d)
	d.worker.mu.Lock()
	d.worker.commandState = "unknown"
	d.worker.mu.Unlock()
	var created api.ExecCreateResult
	d.request(t, "POST", "/v1/execs", fmt.Sprintf(`{"sandbox_id":%q,"argv":["/bin/true"],"wait_ms":1000}`, sb.Data.Id), 200, &created, "Idempotency-Key", "lost-command")
	var ex api.ExecGetResult
	until := time.Now().Add(time.Second)
	for {
		d.request(t, "GET", "/v1/execs/"+created.Result.ExecId, "", 200, &ex)
		if ex.Data.State == "outcome_unknown" {
			break
		}
		if time.Now().After(until) {
			t.Fatal("lost command never became unknown")
		}
		time.Sleep(time.Millisecond)
	}
	if ex.Data.ExitCode != nil {
		t.Fatal("lost command fabricated exit")
	}
	for _, r := range d.worker.seen() {
		if inspect := r.GetInspect(); inspect != nil && inspect.CommandOperationId != "" && inspect.CommandOperationId != created.OperationId+".start" {
			t.Fatal("Inspect did not address original Start")
		}
	}
}

func TestExecPollsRunningCommandAndPaginatesOutput(t *testing.T) {
	d := startDemo(t)
	close(d.worker.ready)
	sb := readySandboxFromCLI(t, d)
	d.worker.mu.Lock()
	d.worker.commandState = "running"
	d.worker.command = &pb.CommandResult{}
	d.worker.mu.Unlock()
	var created api.ExecCreateResult
	d.request(t, "POST", "/v1/execs", fmt.Sprintf(`{"sandbox_id":%q,"argv":["/bin/true"],"wait_ms":1000}`, sb.Data.Id), 200, &created, "Idempotency-Key", "pages")
	id := created.Result.ExecId
	path := "/v1/execs/" + id + "/output"
	var initial api.ExecWatchResult
	d.request(t, "GET", path, "", 200, &initial)
	if initial.NextCursor == nil || len(initial.Items) != 0 {
		t.Fatal("running output needs continuation")
	}
	d.worker.mu.Lock()
	d.worker.commandState = "exited"
	d.worker.command = &pb.CommandResult{FinishedAtUnixNs: 2, ExitCode: 7, Signal: 9, StdoutTail: []byte("hello\n"), StderrTail: []byte("error\n")}
	d.worker.mu.Unlock()
	var first api.ExecWatchResult
	d.request(t, "GET", path+"?cursor="+*initial.NextCursor+"&limit=1&wait_ms=1000", "", 200, &first)
	if len(first.Items) != 1 || first.NextCursor == nil {
		t.Fatalf("first page: %+v", first)
	}
	frame := first.Items[0]
	if frame.StreamId != id+".stdout" || frame.Sequence != 0 || frame.Channel != "stdout" || frame.DataBase64 != "aGVsbG8K" || frame.ByteOffset != "0" || frame.LocationEpoch != 1 {
		t.Fatalf("noncanonical frame: %+v", frame)
	}
	out, err := exec.Command(lampBinary, "exec", "watch", id, "--socket", d.socket, "--cursor", *first.NextCursor, "--limit", "1", "--wait-ms", "1000", "--json").CombinedOutput()
	if err != nil {
		t.Fatalf("CLI watch: %v: %s", err, out)
	}
	var second api.ExecWatchResult
	if json.Unmarshal(out, &second) != nil || len(second.Items) != 1 || second.NextCursor != nil || second.Items[0].Channel != "stderr" || second.Items[0].DataBase64 != "ZXJyb3IK" {
		t.Fatalf("second page: %s", out)
	}
	var replay api.ExecWatchResult
	d.request(t, "GET", path+"?cursor="+*first.NextCursor, "", 200, &replay)
	if len(replay.Items) != 1 || replay.Items[0] != second.Items[0] {
		t.Fatal("cursor replay changed frames")
	}
	var ex api.ExecGetResult
	d.request(t, "GET", "/v1/execs/"+id, "", 200, &ex)
	if ex.Data.State != "exited" || ex.Data.ExitCode == nil || *ex.Data.ExitCode != 7 || ex.Data.Signal == nil || *ex.Data.Signal != 9 {
		t.Fatalf("lost exit status: %+v", ex.Data)
	}
	d.stop()
	d = startDaemon(t, filepath.Dir(d.socket), d.worker)
	d.request(t, "GET", path+"?cursor="+*first.NextCursor, "", 200, &replay)
	if len(replay.Items) != 1 || replay.Items[0] != second.Items[0] {
		t.Fatal("restart lost retained output")
	}
	d.request(t, "GET", "/v1/execs/"+id, "", 200, &ex)
	if ex.Data.State != "exited" || ex.Data.ExitCode == nil || *ex.Data.ExitCode != 7 {
		t.Fatal("restart lost terminal command evidence")
	}

}
