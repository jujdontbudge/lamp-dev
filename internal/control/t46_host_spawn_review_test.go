package control

import (
	"net/http/httptest"
	"strings"
	"testing"

	"lamp.eilmer.si/pkg/api"
)

func TestT46ReviewSideAfterTeleportToHost(t *testing.T) {
	s := t32InstalledService(t)
	s.mu.Lock()
	s.sessions.Hosts = map[string]SessionHost{"hs_local": {OwnerID: "pr_owner", Socket: "/unused", Root: t.TempDir()}}
	s.sessions.Harnesses = map[string]SessionHarness{"review": {OwnerID: "pr_owner", Argv: []string{"/usr/bin/false"}, Cwd: "{session_dir}/workspace", AuthModes: []string{"none"}}}
	s.journal.Sandboxes["sb_review"] = api.Sandbox{Id: "sb_review", OwnerId: "pr_owner", State: "ready"}
	// Exact relevant fields left by VM -> host restore. Observed prevents a
	// background runner from executing; this checks only the public mutation.
	s.journal.Control.Sessions["se_review"] = &managedSession{Observed: true, Resource: map[string]any{
		"id": "se_review", "owner_id": "pr_owner", "revision": uint64(1),
		"state": "active", "sandbox_id": "sb_review", "agent_mode": "inside",
		"harness_id": "review", "auth_mode": "none", "location_epoch": uint64(2),
		"location": map[string]any{"kind": "host", "host_id": "hs_local"},
	}}
	s.mu.Unlock()
	r := httptest.NewRequest("POST", "/v1/sessions/se_review:spawn", strings.NewReader(`{"side":true,"prompt":"Explain the shared files"}`))
	r.Header.Set("Authorization", "Bearer review-disposable-owner")
	r.Header.Set("Idempotency-Key", "review-host-side")
	r.Header.Set("If-Match", "1")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code >= 300 {
		t.Fatalf("side chat on enrolled host should be accepted; HTTP %d: %s", w.Code, w.Body.String())
	}
}
