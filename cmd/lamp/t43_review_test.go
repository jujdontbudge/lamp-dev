package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestT43AttachDrainsOutputWhenSessionEnds(t *testing.T) {
	// A session is active when attached, then ends with 101 retained frames.
	// The real watch API defaults to pages of 100 and supplies next_cursor.
	gets, watches := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reply any
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/sessions/se_review":
			gets++
			state := "active"
			if gets > 1 {
				state = "ended"
			}
			reply = map[string]any{"data": map[string]any{"state": state, "revision": 1, "location_epoch": 1}}
		case r.Method == "POST" && r.URL.Path == "/v1/leases":
			reply = map[string]any{"result": map[string]any{"lease_id": "le_review"}}
		case r.Method == "POST" && r.URL.Path == "/v1/sessions/se_review:attach":
			reply = map[string]any{"result": map[string]any{"lease_id": "le_review"}}
		case r.Method == "GET" && r.URL.Path == "/v1/sessions/se_review/output":
			watches++
			frames := []any{}
			var cursor any
			if r.URL.Query().Get("cursor") == "" {
				for i := 0; i < 100; i++ {
					frames = append(frames, map[string]any{"channel": "stdout", "data_base64": base64.StdEncoding.EncodeToString([]byte("line\n"))})
				}
				cursor = "last-page"
			} else {
				frames = append(frames, map[string]any{"channel": "stdout", "data_base64": base64.StdEncoding.EncodeToString([]byte("final result\n"))})
			}
			reply = map[string]any{"items": frames, "next_cursor": cursor}
		case r.Method == "GET" && r.URL.Path == "/v1/leases/le_review":
			reply = map[string]any{"data": map[string]any{"revision": 1}}
		case r.Method == "POST" && r.URL.Path == "/v1/leases/le_review:release":
			reply = map[string]any{"result": map[string]any{}}
		default:
			t.Errorf("unexpected API call: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected route", 404)
			return
		}
		json.NewEncoder(w).Encode(reply)
	}))
	defer server.Close()
	stdin, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	previous := os.Stdin
	os.Stdin = stdin
	defer func() { os.Stdin = previous }()
	t.Setenv("LAMP_TOKEN", "disposable-review-token")
	t.Setenv("LAMP_TOKEN_FILE", "")
	var out, errs bytes.Buffer
	code := run([]string{"session", "attach", "--session-id", "se_review", "--api-url", server.URL}, &out, &errs)
	if code != 0 {
		t.Fatalf("attach exited %d: %s", code, &errs)
	}
	if !strings.Contains(out.String(), "final result\n") {
		t.Fatalf("attach exited successfully after %d watch page(s), dropping final result; printed %d of 101 frames", watches, strings.Count(out.String(), "\n"))
	}
}
