package control_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JohanSardar/lamp/internal/control"
	"github.com/JohanSardar/lamp/internal/surface"
	"github.com/JohanSardar/lamp/pkg/api"
	pb "github.com/JohanSardar/lamp/pkg/workerpb"
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
func result(m map[string]any) map[string]any  { return m["result"].(map[string]any) }
func data(m map[string]any) map[string]any    { return m["data"].(map[string]any) }
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
func TestScopedKeysSecretOnceAndRestart(t *testing.T) {
	f := newCore(t, nil)
	expiry := time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	first := f.call(t, f.owner, "key_create", map[string]any{"scope": map[string]any{"actions": []string{"key.create", "key.get", "key.list", "sandbox.get", "event.list"}, "sandbox_ids": []string{}}, "expires_at": expiry, "idempotency_key": "secret-once"}, 200)
	key := result(first)["secret"].(string)
	id := field(result(first), "key_id")
	replay := f.call(t, f.owner, "key_create", map[string]any{"scope": map[string]any{"actions": []string{"key.create", "key.get", "key.list", "sandbox.get", "event.list"}, "sandbox_ids": []string{}}, "expires_at": expiry, "idempotency_key": "secret-once"}, 200)
	if result(replay)["secret"] != nil || result(replay)["secret_available"] != false {
		t.Fatal("secret returned on replay")
	}
	child := result(f.mutation(t, key, "key_create", map[string]any{"scope": map[string]any{"actions": []string{"sandbox.create", "sandbox.get", "key.get"}}, "expires_at": time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339Nano)}))
	childKey := child["secret"].(string)
	childID := child["key_id"].(string)
	f.call(t, childKey, "sandbox_create", map[string]any{"template_id": "tpl", "idempotency_key": "cannot-widen"}, 403)
	effective := data(f.call(t, childKey, "key_get", map[string]any{"key_id": childID}, 200))
	if field(effective, "expires_at") != expiry || len(effective["scope"].(map[string]any)["sandbox_ids"].([]any)) != 0 {
		t.Fatal("child broadened expiry or empty selector")
	}
	f.server.Close()
	if e := f.service.Close(); e != nil {
		t.Fatal(e)
	}
	for _, file := range []string{"journal.json", "journal.log"} {
		bytes, e := os.ReadFile(filepath.Join(f.config.StateDir, file))
		if e != nil {
			t.Fatal(e)
		}
		if strings.Contains(string(bytes), key) || strings.Contains(string(bytes), childKey) {
			t.Fatal("bearer persisted")
		}
	}
	var e error
	f.service, e = control.Open(f.config)
	if e != nil {
		t.Fatal(e)
	}
	f.server = httptest.NewServer(f.service)
	f.call(t, key, "key_get", map[string]any{"key_id": id}, 200)
	replay = f.call(t, f.owner, "key_create", map[string]any{"scope": map[string]any{"actions": []string{"key.create", "key.get", "key.list", "sandbox.get", "event.list"}, "sandbox_ids": []string{}}, "expires_at": expiry, "idempotency_key": "secret-once"}, 200)
	if result(replay)["secret"] != nil {
		t.Fatal("restart reissued secret")
	}
	f.mutation(t, f.owner, "key_delete", map[string]any{"key_id": id, "expected_revision": 1})
	f.call(t, childKey, "key_get", map[string]any{"key_id": childID}, 401)
	t.Log("hashed 256-bit bearers, secret-once replay/restart, delegation intersection, empty selectors and descendant rejection passed")
}
func TestEventsReplayIsolationAndLiveRevoke(t *testing.T) {
	f := newCore(t, nil)
	f.mutation(t, f.owner, "fleet_update", map[string]any{"fleet_id": "fl_local", "mode": "team", "expected_revision": 1})
	_, key := f.member(t, "member")
	_, other := f.member(t, "member")
	f.call(t, key, "sandbox_create", map[string]any{"template_id": "tpl", "idempotency_key": "event-create", "wait_ms": 1000}, 200)
	events := f.call(t, key, "event_list", map[string]any{}, 200)
	items := events["items"].([]any)
	if len(items) < 4 {
		t.Fatal("missing lifecycle events")
	}
	last := uint64(0)
	for _, v := range items {
		e := v.(map[string]any)
		n, _ := strconv.ParseUint(field(e, "sequence"), 10, 64)
		if n <= last {
			t.Fatal("events out of order")
		}
		last = n
	}
	cursor := events["next_cursor"].(string)
	if len(f.call(t, key, "event_list", map[string]any{"cursor": cursor}, 200)["items"].([]any)) != 0 {
		t.Fatal("cursor replay repeated acknowledged events")
	}
	f.call(t, other, "event_list", map[string]any{"cursor": cursor}, 410)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", f.server.URL+"/v1/events", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "text/event-stream")
	response, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatal("not SSE")
	}
	scanner := bufio.NewScanner(response.Body)
	if !scanner.Scan() {
		t.Fatal("no live event")
	}

	reader := result(f.mutation(t, f.owner, "key_create", map[string]any{"scope": map[string]any{"actions": []string{"event.list"}}, "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)}))
	readerKey := reader["secret"].(string)
	req2, _ := http.NewRequest("GET", f.server.URL+"/v1/events", nil)
	req2.Header.Set("Authorization", "Bearer "+readerKey)
	req2.Header.Set("Accept", "text/event-stream")
	r2, e := http.DefaultClient.Do(req2)
	if e != nil {
		t.Fatal(e)
	}
	defer r2.Body.Close()
	closed := make(chan struct{})
	go func() { io.Copy(io.Discard, r2.Body); close(closed) }()
	f.mutation(t, f.owner, "key_delete", map[string]any{"key_id": reader["key_id"], "expected_revision": 1})
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("revoked event stream stayed open")
	}
	t.Log("durable ordered events, owner-bound signed replay cursor, live SSE and active reader revocation passed")
}

func TestOneUseApprovalExactPlanAndRealEffect(t *testing.T) {
	var effects atomic.Int64
	var expected control.BoundAction
	var mu sync.Mutex
	dir := t.TempDir()
	marker := filepath.Join(dir, "effect")
	validator := func() control.BoundAction { mu.Lock(); defer mu.Unlock(); return expected }
	adapter := control.ProtectedAction{ValidateApproval: func(api.ApprovalCreateInput) (control.BoundAction, error) { return validator(), nil }, ValidateInvocation: func(api.ConnectionInvokeInput) (control.BoundAction, error) { return validator(), nil }, Execute: func(ctx context.Context, b control.BoundAction) (api.AdapterDocument, error) {
		effects.Add(1)
		return api.AdapterDocument(`{"written":true}`), os.WriteFile(marker, []byte("exact-approved-effect"), 0600)
	}}
	f := newCore(t, map[string]control.ProtectedAction{"cn_fixture": adapter})
	created := f.call(t, f.owner, "sandbox_create", map[string]any{"template_id": "tpl", "idempotency_key": "approval-sandbox", "wait_ms": 1000}, 200)
	sandbox := field(result(created), "sandbox_id")
	operation := "write_marker"
	adapterDigest := "sha256:fixture-adapter"
	ref := api.ResourceRef{Kind: "connection", Id: "cn_fixture"}
	destination := api.Destination{Hostname: pointerString("fixture.example"), Protocol: pointerString("https")}
	arguments := api.AdapterDocument(`{"value":"exact-approved-effect"}`)
	empty := []string{}
	expected = control.BoundAction{PrincipalID: "pr_owner", TaskID: "tk_fixture", SandboxID: sandbox, Generation: 1, ResourceRevision: 1, Plan: api.ActionPlan{AdapterDigest: &adapterDigest, Operation: &operation, ResourceRef: &ref, Arguments: &arguments, Destination: &destination, SecretRefs: &empty, RedactedFields: &empty}}
	makeApproval := func() map[string]any {
		r := f.mutation(t, f.owner, "approval_create", map[string]any{"principal_id": "pr_owner", "task_id": "tk_fixture", "sandbox_id": sandbox, "generation": 1, "resource_ref": ref, "action_plan": expected.Plan, "destination": destination, "expires_at": time.Now().Add(55 * time.Second).UTC().Format(time.RFC3339Nano)})
		return data(f.call(t, f.owner, "approval_get", map[string]any{"approval_id": result(r)["approval_id"]}, 200))
	}
	approval := makeApproval()
	decisionKey := result(f.mutation(t, f.owner, "key_create", map[string]any{"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), "scope": map[string]any{"actions": []string{"approval.approve"}, "resource_ids": []api.ResourceRef{{Kind: "approval", Id: "ap_other"}}}}))["secret"].(string)
	f.call(t, decisionKey, "approval_approve", map[string]any{"approval_id": approval["id"], "action_digest": approval["action_digest"], "expected_revision": 1, "idempotency_key": "outside-decision-selector"}, 404)
	if current := data(f.call(t, f.owner, "approval_get", map[string]any{"approval_id": approval["id"]}, 200)); current["state"] != "pending" || field(current, "revision") != "1" {
		t.Fatal("out-of-scope decision changed approval")
	}
	f.mutation(t, f.owner, "approval_approve", map[string]any{"approval_id": approval["id"], "action_digest": approval["action_digest"], "expected_revision": 1})
	var wg sync.WaitGroup
	successes := atomic.Int64{}
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := surface.Client{URL: f.server.URL, Token: f.owner}
			defer c.Close()
			_, status, e := c.Call(context.Background(), surface.ByName["connection_invoke"], map[string]any{"connection_id": "cn_fixture", "attachment_id": "at_fixture", "operation": "write_marker", "arguments": map[string]any{"value": "exact-approved-effect"}, "approval_id": approval["id"], "expected_revision": 1, "idempotency_key": fmt.Sprintf("consume-%d", i), "wait_ms": 1000})
			if e != nil {
				t.Error(e)
			}
			if status == 200 {
				successes.Add(1)
			} else if status != 403 {
				t.Errorf("consume status %d", status)
			}
		}(i)
	}
	wg.Wait()
	if effects.Load() != 1 || successes.Load() != 1 {
		t.Fatalf("effects %d successes %d", effects.Load(), successes.Load())
	}
	bytes, e := os.ReadFile(marker)
	if e != nil || string(bytes) != "exact-approved-effect" {
		t.Fatal("missing independent effect")
	}
	second := makeApproval()
	f.mutation(t, f.owner, "approval_approve", map[string]any{"approval_id": second["id"], "action_digest": second["action_digest"], "expected_revision": 1})
	mu.Lock()
	changed := api.Destination{Hostname: pointerString("evil.example"), Protocol: pointerString("https")}
	expected.Plan.Destination = &changed
	mu.Unlock()
	f.call(t, f.owner, "connection_invoke", map[string]any{"connection_id": "cn_fixture", "attachment_id": "at_fixture", "operation": "write_marker", "arguments": map[string]any{}, "approval_id": second["id"], "expected_revision": 1, "idempotency_key": "changed-destination", "wait_ms": 1000}, 403)
	if effects.Load() != 1 {
		t.Fatal("changed destination performed effect")
	}
	t.Log("20 simultaneous distinct-key consumers => one durable approval consumption and one independently read file effect; changed destination rejected")
}
func pointerString(s string) *string { return &s }

func TestSharingModesAndRoleBoundaries(t *testing.T) {
	f := newCore(t, nil)
	p := result(f.mutation(t, f.owner, "principal_create", map[string]any{"issuer": "fixture", "subject": "same-email-different-subject"}))["principal_id"]
	f.mutation(t, f.owner, "membership_create", map[string]any{"principal_id": p, "role": "member"})
	f.call(t, f.owner, "key_create", map[string]any{"principal_id": p, "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), "idempotency_key": "single-refusal"}, 403)
	f.mutation(t, f.owner, "fleet_update", map[string]any{"fleet_id": "fl_local", "mode": "team", "expected_revision": 1})
	_, admin := f.member(t, "admin")
	_, member := f.member(t, "member")
	_, viewer := f.member(t, "viewer")
	for _, token := range []string{admin, member, viewer} {
		f.call(t, token, "fleet_get", map[string]any{"fleet_id": "fl_local"}, 200)
		f.call(t, token, "fleet_update", map[string]any{"fleet_id": "fl_local", "mode": "public_service", "expected_revision": 2, "idempotency_key": "forbidden-mode"}, 403)
	}
	adminP := result(f.mutation(t, admin, "principal_create", map[string]any{"issuer": "fixture", "subject": "admin-enrolled"}))["principal_id"]
	f.call(t, admin, "membership_create", map[string]any{"principal_id": adminP, "role": "admin", "idempotency_key": "self-promote"}, 403)
	f.call(t, member, "principal_create", map[string]any{"issuer": "fixture", "subject": "forbidden", "idempotency_key": "member-create"}, 403)
	// Even the fleet administrator cannot read another principal's private content.
	sb := result(f.call(t, member, "sandbox_create", map[string]any{"template_id": "tpl", "idempotency_key": "member-private", "wait_ms": 1000}, 200))["sandbox_id"]
	for _, token := range []string{admin, viewer, f.owner} {
		f.call(t, token, "sandbox_get", map[string]any{"sandbox_id": sb}, 404)
	}
	f.mutation(t, f.owner, "fleet_update", map[string]any{"fleet_id": "fl_local", "mode": "public_service", "expected_revision": 2})
	f.call(t, member, "sandbox_get", map[string]any{"sandbox_id": sb}, 200)
	f.mutation(t, f.owner, "fleet_update", map[string]any{"fleet_id": "fl_local", "mode": "single_owner", "expected_revision": 3})
	f.call(t, member, "sandbox_get", map[string]any{"sandbox_id": sb}, 401)
	t.Log("single-owner/team/public-service admission, owner/admin/member/viewer role separation and private content defaults passed")
}
func TestThirtyTwoCredentialedCallers(t *testing.T) {
	f := newCore(t, nil)
	f.mutation(t, f.owner, "fleet_update", map[string]any{"fleet_id": "fl_local", "mode": "team", "expected_revision": 1})
	type identity struct{ principal, token string }
	callers := make([]identity, 32)
	for i := range callers {
		p, k := f.member(t, "member")
		callers[i] = identity{p, k}
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	ids := make(chan string, 32)
	for i, c := range callers {
		wg.Add(1)
		go func(i int, c identity) {
			defer wg.Done()
			<-start
			r := f.call(t, c.token, "sandbox_create", map[string]any{"template_id": "tpl", "idempotency_key": "identical-key", "wait_ms": 30000}, 200)
			id := field(result(r), "sandbox_id")
			sb := data(f.call(t, c.token, "sandbox_get", map[string]any{"sandbox_id": id}, 200))
			if sb["owner_id"] != c.principal {
				t.Error("cross-owner effect")
			}
			f.call(t, callers[(i+1)%32].token, "sandbox_get", map[string]any{"sandbox_id": id}, 404)
			ids <- id
		}(i, c)
	}
	close(start)
	wg.Wait()
	close(ids)
	unique := map[string]bool{}
	for id := range ids {
		if unique[id] {
			t.Error("lost effect")
		}
		unique[id] = true
	}
	if len(unique) != 32 {
		t.Fatalf("resources %d", len(unique))
	}
	t.Log("32 simultaneous credentialed callers: 32 distinct durable resources, zero lost or cross-owner effects (protocol fixture; no speed qualification)")
}

func TestDeniedAdmissionDoesNotAdvanceSourceRevision(t *testing.T) {
	f := newCore(t, nil)
	created := f.call(t, f.owner, "sandbox_create", map[string]any{"template_id": "tpl", "idempotency_key": "scoped-source", "wait_ms": 1000}, 200)
	id := field(result(created), "sandbox_id")
	before := data(f.call(t, f.owner, "sandbox_get", map[string]any{"sandbox_id": id}, 200))
	requests := len(f.worker.seen())
	for _, scope := range []map[string]any{{"actions": []string{"sandbox.fork", "sandbox.get"}, "limits": map[string]any{"concurrency": 0}}, {"actions": []string{"sandbox.fork", "sandbox.get"}, "sandbox_ids": []string{id}}} {
		key := result(f.mutation(t, f.owner, "key_create", map[string]any{"scope": scope, "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)}))["secret"].(string)
		f.call(t, key, "sandbox_fork", map[string]any{"sandbox_id": id, "expected_revision": before["revision"], "idempotency_key": fmt.Sprintf("denied-%d", time.Now().UnixNano()), "wait_ms": 1000}, 403)
	}
	after := data(f.call(t, f.owner, "sandbox_get", map[string]any{"sandbox_id": id}, 200))
	if before["revision"] != after["revision"] || len(f.worker.seen()) != requests {
		t.Fatal("denied admission changed source or dispatched effect")
	}
}

func TestRotationKeepsIssuingKeyAuthority(t *testing.T) {
	f := newCore(t, nil)
	longExpiry := time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	shortExpiry := time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339Nano)
	target := result(f.mutation(t, f.owner, "key_create", map[string]any{"expires_at": longExpiry}))
	issuer := result(f.mutation(t, f.owner, "key_create", map[string]any{"expires_at": shortExpiry, "scope": map[string]any{"actions": []string{"key.rotate", "key.get"}}}))
	rotated := result(f.mutation(t, issuer["secret"].(string), "key_rotate", map[string]any{"key_id": target["key_id"], "expected_revision": 1, "expires_at": longExpiry}))
	key := rotated["secret"].(string)
	resource := data(f.call(t, key, "key_get", map[string]any{"key_id": rotated["key_id"]}, 200))
	if resource["expires_at"] != shortExpiry || resource["parent_key_id"] != issuer["key_id"] {
		t.Fatal("rotation detached issuer expiry or ancestry")
	}
	f.call(t, key, "sandbox_list", map[string]any{}, 403)
	f.mutation(t, f.owner, "key_delete", map[string]any{"key_id": issuer["key_id"], "expected_revision": 1})
	f.call(t, key, "key_get", map[string]any{"key_id": rotated["key_id"]}, 401)

	self := result(f.mutation(t, f.owner, "key_create", map[string]any{"expires_at": longExpiry}))
	replacement := result(f.mutation(t, self["secret"].(string), "key_rotate", map[string]any{"key_id": self["key_id"], "expected_revision": 1}))
	f.call(t, replacement["secret"].(string), "key_get", map[string]any{"key_id": replacement["key_id"]}, 200)
	f.call(t, self["secret"].(string), "key_get", map[string]any{"key_id": self["key_id"]}, 401)
	t.Log("rotation preserves issuer action ceiling, expiry and live ancestry; self-rotation replaces the revoked key")
}

func TestOwnerCannotMintOrRotateAnotherPrincipalCredential(t *testing.T) {
	f := newCore(t, nil)
	f.mutation(t, f.owner, "fleet_update", map[string]any{"fleet_id": "fl_local", "mode": "team", "expected_revision": 1})
	alice, token := f.member(t, "member")
	created := f.call(t, token, "sandbox_create", map[string]any{"template_id": "tpl", "idempotency_key": "private", "wait_ms": 30000}, 200)
	f.call(t, f.owner, "sandbox_get", map[string]any{"sandbox_id": result(created)["sandbox_id"]}, 404)
	f.call(t, f.owner, "key_create", map[string]any{"principal_id": alice, "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), "idempotency_key": "impersonate"}, 403)
	keys := f.call(t, token, "key_list", map[string]any{}, 200)["items"].([]any)
	key := keys[0].(map[string]any)
	f.call(t, f.owner, "key_rotate", map[string]any{"key_id": key["id"], "expected_revision": key["revision"], "idempotency_key": "foreign-rotate"}, 403)
	f.call(t, token, "sandbox_get", map[string]any{"sandbox_id": result(created)["sandbox_id"]}, 200)
}

func TestRotationCannotDropIndependentInheritedAncestry(t *testing.T) {
	f := newCore(t, nil)
	expiry := time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	a := result(f.mutation(t, f.owner, "key_create", map[string]any{"expires_at": expiry}))
	b := result(f.mutation(t, a["secret"].(string), "key_create", map[string]any{"expires_at": expiry}))
	c := result(f.mutation(t, f.owner, "key_create", map[string]any{"expires_at": expiry}))
	f.call(t, c["secret"].(string), "key_rotate", map[string]any{"key_id": b["key_id"], "expected_revision": 1, "idempotency_key": "drop-A"}, 403)
	f.mutation(t, f.owner, "key_delete", map[string]any{"key_id": a["key_id"], "expected_revision": 1})
	f.call(t, b["secret"].(string), "sandbox_list", map[string]any{}, 401)
}

func TestPendingCleanupKeepsCapacityUntilRelease(t *testing.T) {
	f := newCore(t, nil)
	key := result(f.mutation(t, f.owner, "key_create", map[string]any{"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), "scope": map[string]any{"limits": map[string]any{"concurrency": 1}}}))["secret"].(string)
	sb := result(f.call(t, key, "sandbox_create", map[string]any{"template_id": "tpl", "idempotency_key": "first", "wait_ms": 30000}, 200))["sandbox_id"]
	before := data(f.call(t, key, "sandbox_get", map[string]any{"sandbox_id": sb}, 200))
	f.worker.mu.Lock()
	f.worker.fail = "HOST_UNAVAILABLE"
	f.worker.mu.Unlock()
	f.call(t, key, "sandbox_delete", map[string]any{"sandbox_id": sb, "expected_revision": before["revision"], "idempotency_key": "failed-release", "wait_ms": 30000}, 503)
	f.worker.mu.Lock()
	f.worker.fail = ""
	f.worker.mu.Unlock()
	for i := 0; i < 3; i++ {
		f.call(t, key, "sandbox_create", map[string]any{"template_id": "tpl", "idempotency_key": fmt.Sprintf("overspend-%d", i), "wait_ms": 30000}, 403)
	}
	after := data(f.call(t, key, "sandbox_get", map[string]any{"sandbox_id": sb}, 200))
	f.call(t, key, "sandbox_delete", map[string]any{"sandbox_id": sb, "expected_revision": after["revision"], "idempotency_key": "confirmed-release", "wait_ms": 30000}, 200)
	f.call(t, key, "sandbox_create", map[string]any{"template_id": "tpl", "idempotency_key": "after-release", "wait_ms": 30000}, 200)
}

func TestReadinessRejectsFailedGuardProbe(t *testing.T) {
	f := newCore(t, nil)
	f.worker.mu.Lock()
	f.worker.command = &pb.CommandResult{FinishedAtUnixNs: 1, ExitCode: 70}
	f.worker.mu.Unlock()
	result := f.call(t, f.owner, "sandbox_create", map[string]any{"template_id": "template", "idempotency_key": "failed-probe", "wait_ms": 60000}, 503)
	op := result["error"].(map[string]any)["operation_id"].(string)
	failed := f.call(t, f.owner, "operation_get", map[string]any{"operation_id": op}, 200)["data"].(map[string]any)
	if failed["state"] != "failed" || failed["cleanup_state"] != "pending" {
		t.Fatal(failed)
	}
	items := f.call(t, f.owner, "sandbox_list", map[string]any{}, 200)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["state"] == "ready" {
		t.Fatal("failed guard probe advertised ready", items)
	}
}

func TestLiveExecKeepsConcurrencyChargeAfterCommandStart(t *testing.T) {
	f := newCore(t, nil)
	sb := field(result(f.call(t, f.owner, "sandbox_create", map[string]any{"template_id": "tpl", "idempotency_key": "live-limit-sb", "wait_ms": 60000}, 200)), "sandbox_id")
	key := f.call(t, f.owner, "key_create", map[string]any{"scope": map[string]any{"limits": map[string]any{"concurrency": 1}}, "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), "idempotency_key": "live-limit-key"}, 200)["result"].(map[string]any)["secret"].(string)
	f.worker.mu.Lock()
	f.worker.commandState = "running"
	f.worker.command = &pb.CommandResult{}
	f.worker.mu.Unlock()
	f.call(t, key, "exec_create", map[string]any{"sandbox_id": sb, "argv": []string{"/bin/true"}, "idempotency_key": "live-one", "wait_ms": 60000}, 200)
	f.call(t, key, "exec_create", map[string]any{"sandbox_id": sb, "argv": []string{"/bin/true"}, "idempotency_key": "live-two", "wait_ms": 60000}, 403)
	f.worker.mu.Lock()
	f.worker.commandState = "exited"
	f.worker.command = &pb.CommandResult{FinishedAtUnixNs: 1}
	f.worker.mu.Unlock()
}

func TestConcurrentExecStartsShareSandboxWithoutDroppingAdmissions(t *testing.T) {
	f := newCore(t, nil)
	sb := field(result(f.call(t, f.owner, "sandbox_create", map[string]any{"template_id": "tpl", "idempotency_key": "shared-exec-sb", "wait_ms": 60000}, 200)), "sandbox_id")
	var wg sync.WaitGroup
	ids := make(chan string, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			result := f.call(t, f.owner, "exec_create", map[string]any{"sandbox_id": sb, "argv": []string{"/bin/true"}, "idempotency_key": fmt.Sprint("shared-", i), "wait_ms": 60000}, 200)
			ids <- result["result"].(map[string]any)["exec_id"].(string)
		}(i)
	}
	wg.Wait()
	close(ids)
	distinct := map[string]bool{}
	for id := range ids {
		distinct[id] = true
	}
	if len(distinct) != 32 {
		t.Fatal("lost or repeated concurrent start", len(distinct))
	}
}
