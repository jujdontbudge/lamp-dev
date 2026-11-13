package control_test

import (
	"net/http/httptest"
	"testing"
	"time"

	"lamp.eilmer.si/internal/control"
)

func TestT33ReviewRestartReleasesMissingGuest(t *testing.T) {
	f := newCore(t, nil)
	created := f.call(t, f.owner, "sandbox_create", map[string]any{
		"template_id": "tpl", "idempotency_key": "restart-missing", "wait_ms": 1000,
	}, 200)
	sid := field(result(created), "sandbox_id")
	f.server.Close()
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	// Model the documented supervisor restart: the volatile guest is gone.
	f.worker.mu.Lock()
	f.worker.inspectMissing = true
	f.worker.mu.Unlock()
	s, err := control.Open(f.config)
	if err != nil {
		t.Fatal(err)
	}
	f.service = s
	f.server = httptest.NewServer(s)
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(50 * time.Millisecond)
	defer poll.Stop()
	for {
		sb := data(f.call(t, f.owner, "sandbox_get", map[string]any{"sandbox_id": sid}, 200))
		quota := f.call(t, f.owner, "metric_list", map[string]any{}, 200)
		usage := quota["usage"].(map[string]any)
		if sb["state"] == "failed" && field(usage, "sandboxes") == "0" {
			return
		}
		select {
		case <-poll.C:
		case <-deadline.C:
			t.Fatalf("missing guest after daemon restart: state=%v charged_sandboxes=%v; want failed and 0", sb["state"], usage["sandboxes"])
		}
	}
}
