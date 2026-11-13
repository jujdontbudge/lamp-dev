package control

import (
	"net/http/httptest"
	"strings"
	"testing"

	"lamp.eilmer.si/pkg/api"
)

func TestT39ClosePreviewAfterInterruptedClose(t *testing.T) {
	config := storeConfig(t)
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	// A published preview's supervisor generation survives a lampd crash.
	s.readLock()
	n := s.addNetwork("po", "port", "pr_local")
	id := textField(n.R, "id")
	n.R["state"], n.R["sandbox_id"], n.R["generation"] = "ready", "sb_preview", uint64(1)
	n.Runtime = "po_live_sidecar"
	plan, err := s.planPortClose(caller{Principal: "pr_local"}, map[string]any{
		"port_id": id, "expected_revision": n.R["revision"],
	})
	if err != nil {
		t.Fatal(err)
	}
	// Persist exactly the admission boundary before plan.run sends the close.
	// The effect is deliberately never replayed, just as for a crashed daemon.
	s.journal.Operations["op_interrupted"] = &record{
		Operation: api.Operation{Id: "op_interrupted", OwnerId: "pr_local", State: "accepted", OperationName: pointer("port_unpublish")},
		Resource:  plan.ref, Caller: caller{Principal: "pr_local"}, Key: "pr_local\nport_unpublish\ninterrupted",
	}
	if err := s.saveLife(); err != nil {
		t.Fatal(err)
	}
	if err := s.save("op_interrupted"); err != nil {
		t.Fatal(err)
	}
	s.mu.Unlock()
	s.Close()
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := s.journal.Operations["op_interrupted"].Operation.State; got != "outcome_unknown" {
		t.Fatalf("interrupted operation state = %s", got)
	}
	// The owner issues a fresh explicit close, not an uncertain-effect replay.
	r := httptest.NewRequest("POST", "/v1/ports/"+id+":unpublish", nil)
	r.Header.Set("Idempotency-Key", "owner-retry-close")
	r.Header.Set("If-Match", "1")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code == 409 && strings.Contains(w.Body.String(), "network operation is in flight") {
		t.Fatalf("owner cannot close surviving preview after restart: HTTP %d %s", w.Code, w.Body.String())
	}
	if w.Code != 200 && w.Code != 202 {
		t.Fatalf("fresh close was not admitted: HTTP %d %s", w.Code, w.Body.String())
	}
}
