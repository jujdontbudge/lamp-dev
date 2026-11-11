package control_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/JohanSardar/lamp/internal/control"
	"github.com/JohanSardar/lamp/internal/surface"
	pb "github.com/JohanSardar/lamp/pkg/workerpb"
	"google.golang.org/protobuf/proto"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

type coreFixture struct {
	service       *control.Service
	server        *httptest.Server
	config        control.Config
	worker        *workerFixture
	owner         string
	subjectTokens []string
	nextSubject   int
}

func newCore(t *testing.T, protected map[string]control.ProtectedAction) *coreFixture {
	t.Helper()
	dir, e := os.MkdirTemp("", "lamp-core-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	w := newWorker(t, dir)
	close(w.ready)
	token := make([]byte, 32)
	rand.Read(token)
	owner := hex.EncodeToString(token)
	sum := sha256.Sum256([]byte(owner))
	// Correctness fixtures tolerate race instrumentation and contended disks.
	// Performance thresholds belong to the independent E03 runner.
	config := control.Config{StateDir: filepath.Join(dir, "state"), Identity: &control.IdentityConfig{OwnerID: "pr_owner", BootstrapSHA256: hex.EncodeToString(sum[:])}, ProtectedActions: protected, Worker: control.WorkerConfig{Socket: w.listener.Addr().String(), WorkerID: "worker-test", BootID: "boot-test", UID: os.Getuid(), PID: os.Getpid(), GrantID: "grant-test", Timeout: 30 * time.Second}}
	subjectTokens := make([]string, 128)
	for i := range subjectTokens {
		token := make([]byte, 32)
		rand.Read(token)
		subjectTokens[i] = hex.EncodeToString(token)
		sum := sha256.Sum256([]byte(subjectTokens[i]))
		config.Identity.Subjects = append(config.Identity.Subjects, control.SubjectEnrollment{Issuer: "fixture", Subject: fmt.Sprint(i), SHA256: hex.EncodeToString(sum[:])})
	}
	s, e := control.Open(config)
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(s)
	f := &coreFixture{service: s, server: server, config: config, worker: w, owner: owner, subjectTokens: subjectTokens}
	t.Cleanup(func() { f.server.Close(); f.service.Close() })
	return f
}

func (f *coreFixture) call(t *testing.T, token, name string, in map[string]any, want int) map[string]any {
	t.Helper()
	c := surface.Client{URL: f.server.URL, Token: token}
	defer c.Close()
	data, status, err := c.Call(context.Background(), surface.ByName[name], in)
	if err != nil {
		t.Fatal(err)
	}
	if status != want {
		t.Fatalf("%s: status %d want %d: %s", name, status, want, data)
	}
	m, e := surface.Decode(data)
	if e != nil {
		t.Fatal(e)
	}
	return m
}

func (f *coreFixture) mutation(t *testing.T, token, name string, in map[string]any) map[string]any {
	t.Helper()
	in["idempotency_key"] = fmt.Sprintf("%s-%d", name, time.Now().UnixNano())
	return f.call(t, token, name, in, 200)
}

func field(m map[string]any, k string) string { return fmt.Sprint(m[k]) }

func result(m map[string]any) map[string]any { return m["result"].(map[string]any) }

func data(m map[string]any) map[string]any { return m["data"].(map[string]any) }

func (f *coreFixture) member(t *testing.T, role string) (string, string) {
	t.Helper()
	index := f.nextSubject
	f.nextSubject++
	p := result(f.mutation(t, f.owner, "principal_create", map[string]any{"issuer": "fixture", "subject": fmt.Sprint(index)}))["principal_id"].(string)
	f.mutation(t, f.owner, "membership_create", map[string]any{"principal_id": p, "role": role})
	if role == "viewer" {
		return p, f.subjectTokens[index]
	}
	k := result(f.mutation(t, f.subjectTokens[index], "key_create", map[string]any{"principal_id": p, "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)}))
	return p, k["secret"].(string)
}

func TestControlCoreInterfacesAndAuthority(t *testing.T) {
	f := newCore(t, nil)
	f.call(t, "", "sandbox_list", map[string]any{}, 401)
	f.mutation(t, f.owner, "fleet_update", map[string]any{"fleet_id": "fl_local", "mode": "team", "expected_revision": 1})
	alice, aliceKey := f.member(t, "member")
	bob, bobKey := f.member(t, "member")
	_, viewer := f.member(t, "viewer")
	input := map[string]any{"template_id": "tpl", "idempotency_key": "same-owner-key", "wait_ms": 1000}
	a := f.call(t, aliceKey, "sandbox_create", input, 200)
	b := f.call(t, bobKey, "sandbox_create", input, 200)
	aid := field(result(a), "sandbox_id")
	bid := field(result(b), "sandbox_id")
	if aid == bid || field(data(f.call(t, aliceKey, "sandbox_get", map[string]any{"sandbox_id": aid}, 200)), "owner_id") != alice || field(data(f.call(t, bobKey, "sandbox_get", map[string]any{"sandbox_id": bid}, 200)), "owner_id") != bob {
		t.Fatal("cross-owner idempotency or owner identity")
	}
	f.call(t, bobKey, "sandbox_get", map[string]any{"sandbox_id": aid}, 404)
	f.call(t, f.owner, "sandbox_get", map[string]any{"sandbox_id": aid}, 404)
	args := []string{"sandbox", "create", "--api-url", f.server.URL, "--template-id", "tpl", "--idempotency-key", "same-owner-key", "--wait-ms", "1000", "--json"}
	cmd := exec.Command(lampBinary, args...)
	cmd.Env = append(os.Environ(), "LAMP_TOKEN="+aliceKey)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("CLI: %v %s", e, out)
	}
	m, _ := surface.Decode(out)
	if field(m, "operation_id") != field(a, "operation_id") || m["replayed"] != true {
		t.Fatal("CLI did not replay API acceptance")
	}
	mcp := mcpCall(t, f.server.URL, aliceKey, "sandbox_create", input)
	structured := mcp["result"].(map[string]any)["structuredContent"].(map[string]any)
	if structured["operation_id"] != a["operation_id"] || structured["replayed"] != true {
		t.Fatal("MCP did not replay API acceptance")
	}
	before := len(f.worker.seen())
	f.call(t, viewer, "sandbox_create", input, 403)
	cmd = exec.Command(lampBinary, args...)
	cmd.Env = append(os.Environ(), "LAMP_TOKEN="+viewer)
	out, e = cmd.CombinedOutput()
	if exit, ok := e.(*exec.ExitError); !ok || exit.ExitCode() != 4 || !json.Valid(out) {
		t.Fatalf("CLI denial: %v %s", e, out)
	}
	if mcpCall(t, f.server.URL, viewer, "sandbox_create", input)["result"].(map[string]any)["isError"] != true || len(f.worker.seen()) != before {
		t.Fatal("denied transport dispatched work")
	}
	malformed := map[string]any{"template_id": "tpl", "unknown": true, "idempotency_key": "bad"}
	if mcpCall(t, f.server.URL, aliceKey, "sandbox_create", malformed)["result"].(map[string]any)["isError"] != true {
		t.Fatal("malformed MCP tool succeeded")
	}
	var wg sync.WaitGroup
	ids := make(chan string, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := f.call(t, aliceKey, "sandbox_create", map[string]any{"template_id": "tpl", "idempotency_key": "twenty-retries", "wait_ms": 1000}, 200)
			ids <- field(result(r), "sandbox_id")
		}()
	}
	wg.Wait()
	close(ids)
	one := ""
	for id := range ids {
		if one != "" && one != id {
			t.Fatal("same-key duplicate")
		}
		one = id
	}
	sb := data(f.call(t, aliceKey, "sandbox_get", map[string]any{"sandbox_id": aid}, 200))
	revision := sb["revision"]
	statuses := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := surface.Client{URL: f.server.URL, Token: aliceKey}
			defer c.Close()
			_, status, e := c.Call(context.Background(), surface.ByName["sandbox_fork"], map[string]any{"sandbox_id": aid, "expected_revision": revision, "idempotency_key": fmt.Sprintf("fork-%d", i), "wait_ms": 1000})
			if e != nil {
				t.Error(e)
			}
			statuses <- status
		}(i)
	}
	wg.Wait()
	close(statuses)
	success, conflicts := 0, 0
	for status := range statuses {
		if status == 200 {
			success++
		}
		if status == 409 {
			conflicts++
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("stale edit race successes=%d conflicts=%d", success, conflicts)
	}
	checks := sb["readiness"].(map[string]any)["checks"].([]any)
	if len(checks) != 10 || sb["readiness"].(map[string]any)["command_started_at"] == nil {
		t.Fatal("readiness missing checks or real command marker")
	}
	t.Log("HTTP, real CLI, stdio MCP: same acceptance and denials; 20 retries => one resource; stale revision race => one effect; private owner isolation")
}

func mcpCall(t *testing.T, url, token, name string, args map[string]any) map[string]any {
	t.Helper()
	request, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
	cmd := exec.Command(mcpBinary)
	cmd.Env = append(os.Environ(), "LAMP_API_URL="+url, "LAMP_TOKEN="+token)
	cmd.Stdin = bytes.NewReader(append(request, '\n'))
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("MCP binary: %v %s", e, out)
	}
	m, e := surface.Decode(out)
	if e != nil {
		t.Fatalf("MCP: %s %v", out, e)
	}
	return m
}

var lampBinary, mcpBinary string

func TestMain(m *testing.M) {
	if testCleanupChild() {
		return
	}
	dir, err := os.MkdirTemp("", "lamp-control-test-")
	if err != nil {
		panic(err)
	}
	cleanup, err := guardTestDirectory(dir)
	if err != nil {
		os.RemoveAll(dir)
		panic(err)
	}
	lampBinary = filepath.Join(dir, "lamp")
	mcpBinary = filepath.Join(dir, "lamp-mcp")
	for _, b := range []struct{ path, pkg string }{{lampBinary, "../../cmd/lamp"}, {mcpBinary, "../../cmd/lamp-mcp"}} {
		args := []string{"build", "-o", b.path, b.pkg}
		if os.Getenv("LAMP_CONTROL_RACE") == "1" {
			args = append([]string{"build", "-race"}, args[1:]...)
		}
		c := exec.Command("go", args...)
		if out, err := c.CombinedOutput(); err != nil {
			fmt.Fprintln(os.Stderr, string(out), err)
			cleanup()
			os.Exit(1)
		}
	}
	code := m.Run()
	cleanup()
	os.Exit(code)
}

type workerFixture struct {
	listener net.Listener
	mu       sync.Mutex
	requests []*pb.Request
	states   map[string]string
	ready    chan struct{}
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
		response := &pb.Response{Context: r.Context, Result: &pb.Response_Receipt{Receipt: &pb.Receipt{State: state}}}
		if inspect := r.GetInspect(); inspect != nil && inspect.CommandOperationId != "" {
			response.Result = &pb.Response_Receipt{Receipt: &pb.Receipt{State: "inspected", Detail: map[string]string{"command_state": "exited"}, Command: &pb.CommandResult{FinishedAtUnixNs: 1}}}
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

// A separate session watches a pipe held only by the test parent. Even SIGKILL
// or Go's timeout panic closes that pipe; the watcher then removes the build
// directory. It owns only the uniquely created directory passed by its parent.
func guardTestDirectory(dir string) (func(), error) {
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		reader.Close()
		writer.Close()
		return nil, err
	}
	child := exec.Command(executable)
	child.Env = append(os.Environ(), "LAMP_TEST_CLEANUP_DIR="+dir)
	child.Stdin = reader
	child.Stderr = os.Stderr
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	err = child.Start()
	reader.Close()
	if err != nil {
		writer.Close()
		return nil, err
	}
	return func() { writer.Close(); child.Wait() }, nil
}

func testCleanupChild() bool {
	if dir := os.Getenv("LAMP_TEST_CLEANUP_DIR"); dir != "" {
		io.Copy(io.Discard, os.Stdin)
		if err := os.RemoveAll(dir); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	return false
}
