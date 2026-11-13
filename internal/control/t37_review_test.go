package control_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestT37ScopedKeyMCPDiscovery(t *testing.T) {
	f := newCore(t, nil)
	f.mutation(t, f.owner, "fleet_update", map[string]any{"fleet_id": "fl_local", "mode": "team", "expected_revision": 1})
	principal := result(f.mutation(t, f.owner, "principal_create", map[string]any{"issuer": "local", "subject": "bea"}))["principal_id"].(string)
	f.mutation(t, f.owner, "membership_create", map[string]any{"principal_id": principal, "role": "member"})
	key := result(f.mutation(t, f.owner, "key_create", map[string]any{"principal_id": principal, "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), "scope": map[string]any{"actions": []string{"session.get", "session.watch", "session.input", "lease.create"}}}))["secret"].(string)
	// Establish that the owner-issued scoped credential authenticates successfully.
	f.call(t, key, "session_get", map[string]any{"session_id": "ss_missing"}, 404)
	cmd := exec.Command(mcpBinary)
	cmd.Env = append(os.Environ(), "LAMP_API_URL="+f.server.URL, "LAMP_TOKEN="+key)
	cmd.Stdin = bytes.NewBufferString("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/list\"}\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("MCP process: %v", err)
	}
	var response map[string]any
	if err := json.Unmarshal(out, &response); err != nil {
		t.Fatal(err)
	}
	if response["error"] != nil {
		t.Fatalf("tools/list failed for valid session-scoped key: %s", out)
	}
}
