package control_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/JohanSardar/lamp/pkg/api"
	pb "github.com/JohanSardar/lamp/pkg/workerpb"
	"google.golang.org/protobuf/proto"
)

var lampBinary, daemonBinary string

func TestMain(m *testing.M) {
	// The journal kill tests rerun this binary as a child and kill it, so a
	// child must neither build the CLI binaries nor leave them behind.
	if os.Getenv("LAMP_JOURNAL_WRITER_DIR") != "" || os.Getenv("LAMP_MARKER_CHILD_DIR") != "" {
		os.Exit(m.Run())
	}
	dir, err := os.MkdirTemp("", "lamp-control-test-")
	if err != nil {
		panic(err)
	}
	lampBinary = filepath.Join(dir, "lamp")
	daemonBinary = filepath.Join(dir, "lampd")
	for _, b := range []struct{ path, pkg string }{{lampBinary, "../../cmd/lamp"}, {daemonBinary, "../../cmd/lampd"}} {
		args := []string{"build", "-o", b.path, b.pkg}
		if os.Getenv("LAMP_CONTROL_RACE") == "1" {
			args = append([]string{"build", "-race"}, args[1:]...)
		}
		c := exec.Command("go", args...)
		if out, err := c.CombinedOutput(); err != nil {
			fmt.Fprintln(os.Stderr, string(out), err)
			os.RemoveAll(dir)
			os.Exit(1)
		}
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type workerFixture struct {
	listener     net.Listener
	mu           sync.Mutex
	requests     []*pb.Request
	states       map[string]string
	ready        chan struct{}
	fail         string
	retryable    bool
	drop         bool
	commandState string
	command      *pb.CommandResult
}

func newWorker(t *testing.T, dir string) *workerFixture {
	t.Helper()
	l, err := net.Listen("unix", filepath.Join(dir, "worker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	w := &workerFixture{listener: l, states: map[string]string{}, ready: make(chan struct{})}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go w.serve(c)
		}
	}()
	return w
}
func (w *workerFixture) serve(c net.Conn) {
	defer c.Close()
	f, err := pb.ReadFrame(c)
	if err != nil || f.GetHello() == nil {
		return
	}
	hello := f.GetHello()
	if hello.Protocol != pb.Protocol {
		return
	}
	if pb.WriteFrame(c, &pb.Frame{Payload: &pb.Frame_Hello{Hello: &pb.Hello{Protocol: pb.Protocol, WorkerId: "worker-test", BootId: "boot-test", MaxControlBytes: pb.MaxControlBytes}}}) != nil {
		return
	}
	for {
		f, err := pb.ReadFrame(c)
		if err != nil {
			return
		}
		r := f.GetRequest()
		if r == nil {
			return
		}
		w.mu.Lock()
		w.requests = append(w.requests, proto.Clone(r).(*pb.Request))
		fail, drop, retryable := w.fail, w.drop, w.retryable
		commandState, command := w.commandState, w.command
		if commandState == "" {
			commandState = "exited"
		}
		if command == nil {
			command = &pb.CommandResult{FinishedAtUnixNs: 1}
		}
		state := ""
		switch r.Command.(type) {
		case *pb.Request_Allocate:
			state = "allocated"
		case *pb.Request_Restore:
			state = "restored"
		case *pb.Request_Capture:
			state = "captured"
		case *pb.Request_Start:
			state = "accepted"
		case *pb.Request_Inspect:
			state = w.states[r.Context.SandboxId]
			if state == "accepted" {
				select {
				case <-w.ready:
					state = "command_started"
				default:
				}
			}
		case *pb.Request_Stop:
			state = "stopped"
		case *pb.Request_Release:
			state = "released"
		}
		w.states[r.Context.SandboxId] = state
		w.mu.Unlock()
		if drop {
			return
		}
		response := &pb.Response{Context: r.Context, Result: &pb.Response_Receipt{Receipt: &pb.Receipt{State: state}}}
		if inspect := r.GetInspect(); inspect != nil && inspect.CommandOperationId != "" {
			response.Result = &pb.Response_Receipt{Receipt: &pb.Receipt{State: "inspected", Detail: map[string]string{"command_state": commandState}, Command: command}}
		}
		if fail != "" {
			response.Result = &pb.Response_Error{Error: &pb.WorkerError{Code: fail, Message: "fixture rejection", Retryable: retryable}}
		}
		if pb.WriteFrame(c, &pb.Frame{Payload: &pb.Frame_Response{Response: response}}) != nil {
			return
		}
	}
}
func (w *workerFixture) seen() []*pb.Request {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]*pb.Request{}, w.requests...)
}

type demo struct {
	socket, state string
	client        *http.Client
	worker        *workerFixture
	stop          func()
}

func startDemo(t *testing.T) *demo {
	t.Helper()
	dir, err := os.MkdirTemp("", "lamp-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	w := newWorker(t, dir)
	return startDaemon(t, dir, w)
}
func startDaemon(t *testing.T, dir string, w *workerFixture) *demo {
	t.Helper()
	return startDaemonFor(t, dir, w, w.listener.Addr().String(), os.Getpid())
}
func startDaemonFor(t *testing.T, dir string, w *workerFixture, workerSocket string, workerPID int) *demo {
	t.Helper()
	socket := filepath.Join(dir, "api.sock")
	state := filepath.Join(dir, "state")
	cmd := exec.Command(daemonBinary, "--socket", socket, "--state-dir", state, "--worker-socket", workerSocket, "--worker-id", "worker-test", "--worker-boot-id", "boot-test", "--worker-uid", strconv.Itoa(os.Getuid()), "--worker-pid", strconv.Itoa(workerPID), "--grant-id", "grant-test")
	var log bytes.Buffer
	cmd.Stdout = &log
	cmd.Stderr = &log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cmd.Process.Signal(os.Interrupt)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				cmd.Process.Kill()
				<-done
			}
		})
	}
	t.Cleanup(stop)
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	t.Cleanup(tr.CloseIdleConnections)
	d := &demo{socket, state, &http.Client{Transport: tr, Timeout: 5 * time.Second}, w, stop}
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		select {
		case err := <-done:
			done <- err
			t.Fatalf("lampd did not start: %v: %s", err, log.String())
		default:
		}
		if conn, err := net.DialTimeout("unix", socket, 50*time.Millisecond); err == nil {
			conn.Close()
			return d
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("lampd did not bind its socket")
	return nil
}
func (d *demo) request(t *testing.T, method, path, body string, status int, out any, headers ...string) {
	t.Helper()
	r, err := http.NewRequest(method, "http://lamp"+path, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	for i := 0; i < len(headers); i += 2 {
		r.Header.Add(headers[i], headers[i+1])
	}
	res, err := d.client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != status {
		t.Fatalf("%s %s: got HTTP %d, want %d: %s", method, path, res.StatusCode, status, data)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("decode %s: %v", data, err)
		}
	}
}
func (d *demo) operation(t *testing.T, id string) api.Operation {
	var out api.OperationGetResult
	d.request(t, "GET", "/v1/operations/"+id, "", 200, &out)
	return out.Data
}
func (d *demo) await(t *testing.T, id string, want api.OperationState) api.Operation {
	t.Helper()
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		o := d.operation(t, id)
		if o.State == want {
			return o
		}
		if o.State == "failed" || o.State == "outcome_unknown" {
			t.Fatalf("operation: %+v", o)
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("operation %s never reached %s", id, want)
	return api.Operation{}
}
func TestEndToEndCreateExecFork(t *testing.T) {
	d := startDemo(t)
	var created api.SandboxCreateResult
	d.request(t, "POST", "/v1/sandboxes", `{"template_id":"tpl_demo"}`, 202, &created, "Idempotency-Key", "create-1")
	if created.OperationId == "" || len(created.ResourceIds) != 1 || created.Result != nil {
		t.Fatalf("invalid acceptance: %+v", created)
	}
	id := created.ResourceIds[0].Id
	var sb api.SandboxGetResult
	d.request(t, "GET", "/v1/sandboxes/"+id, "", 200, &sb)
	if sb.Data.State == "ready" || (sb.Data.Readiness != nil && sb.Data.Readiness.CommandStartedAt != nil) {
		t.Fatal("accepted reported ready without command marker")
	}
	close(d.worker.ready)
	d.await(t, created.OperationId, "succeeded")
	d.request(t, "GET", "/v1/sandboxes/"+id, "", 200, &sb)
	if sb.Data.State != "ready" || sb.Data.Readiness == nil || sb.Data.Readiness.CommandStartedAt == nil || sb.Data.Readiness.AgentReadyAt != nil {
		t.Fatalf("readiness: %+v", sb.Data)
	}
	args := []string{"exec", "create", "--socket", d.socket, "--sandbox-id", id, "--idempotency-key", "exec-1", "--wait-ms", "1000", "--json", "--", "/bin/echo", "hello"}
	out, err := exec.Command(lampBinary, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("CLI exec: %v: %s", err, out)
	}
	var executed api.ExecCreateResult
	if err := json.Unmarshal(out, &executed); err != nil {
		t.Fatal(err)
	}
	if executed.Result == nil {
		t.Fatalf("CLI omitted completion: %s", out)
	}
	var ex api.ExecGetResult
	d.request(t, "GET", "/v1/execs/"+executed.Result.ExecId, "", 200, &ex)
	if (ex.Data.State != "running" && ex.Data.State != "exited") || ex.Data.StartedAt == nil {
		t.Fatalf("exec: %+v", ex.Data)
	}
	var forked api.SandboxForkResult
	d.request(t, "POST", "/v1/sandboxes/"+id+":fork", `{"wait_ms":1000}`, 200, &forked, "Idempotency-Key", "fork-1", "If-Match", strconv.FormatUint(sb.Data.Revision, 10))
	if forked.Result == nil || forked.Result.SandboxId == id {
		t.Fatalf("fork: %+v", forked)
	}
	d.request(t, "GET", "/v1/sandboxes/"+forked.Result.SandboxId, "", 200, &sb)
	if sb.Data.ParentSandboxId == nil || *sb.Data.ParentSandboxId != id || sb.Data.State != "ready" {
		t.Fatalf("fork child: %+v", sb.Data)
	}
	requests := d.worker.seen()
	for _, r := range requests {
		if capture := r.GetCapture(); capture != nil && (!capture.PreservesMemory || capture.Consistency != "crash") {
			t.Fatalf("memory fork capture is not crash-consistent: %+v", capture)
		}
	}
	commands := map[string]bool{}
	opids := map[string]bool{}
	for _, r := range requests {
		c := r.Context
		if c == nil || c.RequestId == "" || c.OperationId == "" || c.SandboxId == "" || c.Generation == 0 || c.PolicyRevision == 0 || c.GrantId != "grant-test" || c.DeadlineMs == 0 {
			t.Fatalf("incomplete context: %+v", c)
		}
		if opids[c.OperationId] {
			t.Fatalf("reused effect operation ID: %s", c.OperationId)
		}
		opids[c.OperationId] = true
		commands[fmt.Sprintf("%T", r.Command)] = true
	}
	for _, name := range []string{"*workerpb.Request_Allocate", "*workerpb.Request_Restore", "*workerpb.Request_Start", "*workerpb.Request_Capture"} {
		if !commands[name] {
			t.Errorf("missing worker command %s", name)
		}
	}
	t.Logf("real CLI -> HTTP/Unix -> lampd -> framed worker: create=%s exec=%s fork=%s; acceptance stayed unready until marker", id, executed.Result.ExecId, forked.Result.SandboxId)
}
func TestIdempotencyValidationAndRevision(t *testing.T) {
	d := startDemo(t)
	close(d.worker.ready)
	var first, replay api.SandboxCreateResult
	body := `{"template_id":"tpl_demo","wait_ms":1000}`
	d.request(t, "POST", "/v1/sandboxes", body, 200, &first, "Idempotency-Key", "same")
	n := len(d.worker.seen())
	d.request(t, "POST", "/v1/sandboxes", `{"template_id":"tpl_demo"}`, 200, &replay, "Idempotency-Key", "same")
	if !replay.Replayed || replay.OperationId != first.OperationId || len(d.worker.seen()) != n {
		t.Fatal("replay redelivered an effect")
	}
	d.request(t, "POST", "/v1/sandboxes", `{"template_id":"other"}`, 409, nil, "Idempotency-Key", "same")
	for _, body := range []string{`{}`, `{"template_id":"t","surprise":1}`, `{"template_id":"t","kit_ids":["kit"]}`, `{"template_id":"t","auth_mode":"subscription"}`, `{"template_id":"t","resources":{"vcpus":0}}`, `{"template_id":"t","template_id":"other"}`, `{"template_id":"t"} {}`} {
		d.request(t, "POST", "/v1/sandboxes", body, 400, nil, "Idempotency-Key", "k")
	}
	d.request(t, "POST", "/v1/sandboxes/"+first.Result.SandboxId+":fork", `{}`, 409, nil, "Idempotency-Key", "stale", "If-Match", "0")
	if len(d.worker.seen()) != n {
		t.Fatal("invalid input reached the worker")
	}
}
func TestUnknownWorkerOutcomeIsNotSuccess(t *testing.T) {
	d := startDemo(t)
	d.worker.mu.Lock()
	d.worker.drop = true
	d.worker.mu.Unlock()
	var result api.SandboxCreateResult
	d.request(t, "POST", "/v1/sandboxes", `{"template_id":"t","wait_ms":1000}`, 409, &result, "Idempotency-Key", "drop")
	var errout api.ErrorEnvelope
	d.request(t, "POST", "/v1/sandboxes", `{"template_id":"t","wait_ms":1000}`, 409, &errout, "Idempotency-Key", "drop")
	if errout.Error.Code != "OUTCOME_UNKNOWN" || errout.Error.OperationId == nil {
		t.Fatalf("unknown outcome: %+v", errout)
	}
	o := d.operation(t, *errout.Error.OperationId)
	if o.State != "outcome_unknown" {
		t.Fatal(o.State)
	}
	if len(d.worker.seen()) != 1 {
		t.Fatal("uncertain effect was retried")
	}
}
func TestForkFailureInvalidatesSourceReadiness(t *testing.T) {
	d := startDemo(t)
	close(d.worker.ready)
	var first api.SandboxCreateResult
	d.request(t, "POST", "/v1/sandboxes", `{"template_id":"t","wait_ms":1000}`, 200, &first, "Idempotency-Key", "first")
	var parent api.SandboxGetResult
	d.request(t, "GET", "/v1/sandboxes/"+first.Result.SandboxId, "", 200, &parent)
	d.worker.mu.Lock()
	d.worker.drop = true
	d.worker.mu.Unlock()
	d.request(t, "POST", "/v1/sandboxes/"+parent.Data.Id+":fork", `{"wait_ms":1000}`, 409, nil, "Idempotency-Key", "fork-drop", "If-Match", strconv.FormatUint(parent.Data.Revision, 10))
	d.request(t, "GET", "/v1/sandboxes/"+parent.Data.Id, "", 200, &parent)
	if parent.Data.State != "unavailable" {
		t.Fatalf("uncertain paused source is still %s", parent.Data.State)
	}
}
func TestRestartNeverRedeliversAcceptedEffects(t *testing.T) {
	d := startDemo(t)
	var first api.SandboxCreateResult
	body := `{"template_id":"t"}`
	d.request(t, "POST", "/v1/sandboxes", body, 202, &first, "Idempotency-Key", "restart")
	until := time.Now().Add(time.Second)
	for {
		started := false
		for _, r := range d.worker.seen() {
			if r.GetStart() != nil {
				started = true
				break
			}
		}
		if started {
			break
		}
		if time.Now().After(until) {
			t.Fatal("worker never accepted Start")
		}
		time.Sleep(time.Millisecond)
	}
	d.stop()
	before := len(d.worker.seen())
	restarted := startDaemon(t, filepath.Dir(d.socket), d.worker)
	var result api.ErrorEnvelope
	restarted.request(t, "POST", "/v1/sandboxes", body, 409, &result, "Idempotency-Key", "restart")
	if result.Error.Code != "OUTCOME_UNKNOWN" || len(d.worker.seen()) != before {
		t.Fatal("restart retried an uncertain effect")
	}
	var sb api.SandboxGetResult
	restarted.request(t, "GET", "/v1/sandboxes/"+first.ResourceIds[0].Id, "", 200, &sb)
	if sb.Data.State == "ready" {
		t.Fatal("restart fabricated readiness")
	}
}
func TestWorkerRejectionRetainsFailedOperation(t *testing.T) {
	d := startDemo(t)
	d.worker.mu.Lock()
	d.worker.fail = "NO_CAPACITY"
	d.worker.mu.Unlock()
	var result api.ErrorEnvelope
	d.request(t, "POST", "/v1/sandboxes", `{"template_id":"t","wait_ms":1000}`, 429, &result, "Idempotency-Key", "reject")
	if result.Error.OperationId == nil {
		t.Fatal("missing failed operation ID")
	}
	o := d.operation(t, *result.Error.OperationId)
	if o.State != "failed" || o.CleanupState == nil || *o.CleanupState != "pending" {
		t.Fatalf("failed operation lost diagnostics: %+v", o)
	}
	var sb api.SandboxGetResult
	d.request(t, "GET", "/v1/sandboxes/"+(*o.ResourceIds)[0].Id, "", 200, &sb)
	if sb.Data.State != "failed" {
		t.Fatal(sb.Data.State)
	}
}
func TestCLIReadPreservesUInt64(t *testing.T) {
	d := startDemo(t)
	close(d.worker.ready)
	var created api.SandboxCreateResult
	d.request(t, "POST", "/v1/sandboxes", `{"template_id":"t","wait_ms":1000,"resources":{"vcpus":2,"memory_mib":9007199254740993,"disk_gib":20,"pids_max":1024}}`, 200, &created, "Idempotency-Key", "uint64")
	out, err := exec.Command(lampBinary, "sandbox", "get", "--socket", d.socket, "--sandbox-id", created.Result.SandboxId).CombinedOutput()
	if err != nil || !bytes.Contains(out, []byte(`"memory_mib": 9007199254740993`)) {
		t.Fatalf("CLI rounded a canonical uint64: %v %s", err, out)
	}
}
func TestJournalFailureDoesNotPublishSuccess(t *testing.T) {
	d := startDemo(t)
	var created api.SandboxCreateResult
	d.request(t, "POST", "/v1/sandboxes", `{"template_id":"t"}`, 202, &created, "Idempotency-Key", "disk-failure")
	until := time.Now().Add(time.Second)
	for {
		started := false
		for _, r := range d.worker.seen() {
			if r.GetStart() != nil {
				started = true
				break
			}
		}
		if started {
			break
		}
		if time.Now().After(until) {
			t.Fatal("worker never started")
		}
		time.Sleep(time.Millisecond)
	}
	if err := os.Rename(d.state, d.state+"-moved"); err != nil {
		t.Fatal(err)
	}
	close(d.worker.ready)
	until = time.Now().Add(time.Second)
	for time.Now().Before(until) {
		res, err := d.client.Get("http://lamp/v1/operations/" + created.OperationId)
		if err != nil {
			t.Fatal(err)
		}
		var result api.OperationGetResult
		json.NewDecoder(res.Body).Decode(&result)
		res.Body.Close()
		if res.StatusCode == 503 {
			return
		}
		if result.Data.State == "succeeded" {
			t.Fatal("uncommitted terminal success was published after journal failure")
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("journal failure was not surfaced")
}
func TestCLICreateAndReads(t *testing.T) {
	d := startDemo(t)
	close(d.worker.ready)
	out, err := exec.Command(lampBinary, "sandbox", "create", "--socket", d.socket, "--template-id", "tpl_demo", "--wait-ms", "1000", "--json").CombinedOutput()
	if err != nil {
		t.Fatalf("create: %v %s", err, out)
	}
	var r api.SandboxCreateResult
	if err := json.Unmarshal(out, &r); err != nil || r.Result == nil {
		t.Fatalf("create JSON: %v %s", err, out)
	}
	for _, args := range [][]string{{"operation", "get", "--operation-id", r.OperationId}, {"sandbox", "get", "--sandbox-id", r.Result.SandboxId}} {
		args = append(args, "--socket", d.socket, "--json")
		if out, err := exec.Command(lampBinary, args...).CombinedOutput(); err != nil {
			t.Fatalf("get: %v %s", err, out)
		}
	}
}
