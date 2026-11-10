package surface

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestConcurrentClientInitialization(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":{}}`))
	}))
	defer server.Close()
	client := &Client{URL: server.URL}
	defer client.Close()
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, status, err := client.Call(context.Background(), ByName["sandbox_list"], map[string]any{})
			if err != nil || status != 200 {
				t.Errorf("concurrent call: status=%d error=%v", status, err)
			}
		}()
	}
	close(start)
	wg.Wait()
}

func TestStrictTransportContracts(t *testing.T) {
	for _, raw := range []string{`{"idempotency_key":"k","template_id":"t"}`, `{"idempotency_key":"k","recipe_id":"r"}`} {
		v, e := Decode([]byte(raw))
		if e != nil || Validate("SandboxCreateInput", v) != nil {
			t.Fatalf("valid create rejected: %s %v", raw, e)
		}
	}
	for _, raw := range []string{`{"idempotency_key":"k"}`, `{"idempotency_key":"k","template_id":"t","recipe_id":"r"}`, `{"idempotency_key":"k","template_id":"t","x":1}`, `{"idempotency_key":"k","template_id":null}`, `{"idempotency_key":"k","template_id":"t","resources":{"vcpus":18446744073709551616,"memory_mib":1,"disk_gib":1,"pids_max":1}}`} {
		v, e := Decode([]byte(raw))
		if e == nil && Validate("SandboxCreateInput", v) == nil {
			t.Fatalf("invalid create admitted: %s", raw)
		}
	}
	for _, raw := range []string{`{"x":1,"x":2}`, `{"nested":{"x":1,"x":2}}`, `{} {}`} {
		if _, e := Decode([]byte(raw)); e == nil {
			t.Fatalf("ambiguous JSON accepted: %s", raw)
		}
	}
	max := map[string]any{"sandbox_id": "sb", "expected_revision": json.Number("18446744073709551615"), "idempotency_key": "key"}
	path, headers, body, e := Request(ByName["sandbox_delete"], max)
	if e != nil || path != "/v1/sandboxes/sb" || headers["If-Match"] != "18446744073709551615" || string(body) != "{}" {
		t.Fatalf("uint64 or selectors changed: %s %+v %s %v", path, headers, body, e)
	}
	for _, id := range []string{"", "a/b", "a\x00b"} {
		if _, _, _, e := Request(ByName["sandbox_get"], map[string]any{"sandbox_id": id}); e == nil {
			t.Fatal("invalid path ID")
		}
	}
}
func TestEveryCanonicalBindingAndMCPSchema(t *testing.T) {
	for _, op := range ByName {
		method := op.Method
		path := op.Path
		in := map[string]any{}
		for i := 0; i < len(path); i++ {
			if path[i] == '{' {
				j := i
				for path[j] != '}' {
					j++
				}
				in[path[i+1:j]] = "fixture"
				path = path[:i] + "fixture" + path[j+1:]
			}
		}
		matched, _, ok := Match(method, path)
		if !ok || matched.Name != op.Name {
			t.Fatalf("unreachable operation %s -> %s", op.Name, matched.Name)
		}
		raw, e := json.Marshal(InputSchema(op.InputSchema))
		if e != nil || !json.Valid(raw) {
			t.Fatalf("invalid MCP schema: %s", op.Name)
		}
	}
}
func TestAuthenticatedClientRejectsRedirectAndUnsafeOrigin(t *testing.T) {
	for _, url := range []string{"http://remote.example", "https://user:secret@example.com", "https://example.com?token=secret", "https://example.com/path"} {
		c := Client{URL: url}
		if _, e := c.httpClient(); e == nil {
			t.Fatalf("unsafe origin accepted: %s", url)
		}
	}
}
