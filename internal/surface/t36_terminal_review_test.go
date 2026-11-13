package surface_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"lamp.eilmer.si/internal/surface"
)

func TestT36TerminalSuppliesTerminalType(t *testing.T) {
	// Both lamp sandbox shell and the SSH shell use this public API client.
	// Guest launchers clear their environment; fullscreen programs need TERM.
	created := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/execs" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		created <- input
		// Stop after observing the launch, without creating a VM or host shell.
		w.WriteHeader(http.StatusServiceUnavailable)
		io.WriteString(w, `{"error":{"code":"HOST_UNAVAILABLE","message":"review capture complete"}}`)
	}))
	defer server.Close()
	client := &surface.Client{URL: server.URL}
	defer client.Close()
	_, _, _ = surface.Terminal(context.Background(), client, "sb_review", "", nil, io.Discard, io.Discard, nil)
	input := <-created
	env, _ := input["env"].(map[string]any)
	if term, _ := env["TERM"].(string); term == "" {
		t.Fatalf("PTY launch omits TERM; vim/top receive a cleared environment: %v", input)
	}
}
