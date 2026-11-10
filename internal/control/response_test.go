package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JohanSardar/lamp/pkg/api"
)

type blockedResponseWriter struct {
	header           http.Header
	entered, release chan struct{}
}

func (w *blockedResponseWriter) Header() http.Header { return w.header }
func (w *blockedResponseWriter) WriteHeader(int)     {}
func (w *blockedResponseWriter) Write(p []byte) (int, error) {
	close(w.entered)
	<-w.release
	return len(p), nil
}

func TestSlowResponseDoesNotHoldServiceMutex(t *testing.T) {
	for _, route := range []string{"get-exec", "get-operation", "get-sandbox", "get-missing", "get-broken", "respond", "respond-error", "respond-broken"} {
		t.Run(route, func(t *testing.T) {
			sb := newSandbox()
			ex := api.Exec{Id: "ex_test", Argv: pointer([]string{strings.Repeat("x", 60000)})}
			op := api.Operation{Id: "op_test", State: "succeeded", Outcome: &api.Outcome{Result: pointer(api.AdapterDocument(json.RawMessage(`{"exec_id":"ex_test"}`)))}}
			s := &Service{journal: journal{Operations: map[string]*record{op.Id: {Operation: op, Resource: api.ResourceRef{Kind: "exec", Id: ex.Id}}}, Sandboxes: map[string]api.Sandbox{sb.Id: sb}, Execs: map[string]api.Exec{ex.Id: ex}}, done: map[string]chan struct{}{op.Id: make(chan struct{})}}
			if err := s.initAuthority(Config{}); err != nil {
				t.Fatal(err)
			}
			if route == "respond-error" {
				s.journal.Operations[op.Id].Operation.Error = &api.ErrorDetail{Code: "AUDIT_UNAVAILABLE", Message: "audit is down", Retryable: true}
			}
			if strings.HasSuffix(route, "broken") {
				s.broken = true
			}
			writer := &blockedResponseWriter{header: http.Header{}, entered: make(chan struct{}), release: make(chan struct{})}
			firstDone := make(chan struct{})
			go func() {
				defer close(firstDone)
				if strings.HasPrefix(route, "respond") {
					s.respond(writer, httptest.NewRequest("POST", "/v1/execs", nil), "req", op.Id, true, nil)
					return
				}
				path := "/v1/execs/" + ex.Id
				switch route {
				case "get-operation":
					path = "/v1/operations/" + op.Id
				case "get-sandbox", "get-broken":
					path = "/v1/sandboxes/" + sb.Id
				case "get-missing":
					path = "/v1/sandboxes/missing"
				}
				s.ServeHTTP(writer, httptest.NewRequest("GET", path, nil))
			}()
			<-writer.entered
			secondDone := make(chan struct{})
			go func() {
				defer close(secondDone)
				s.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/sandboxes/"+sb.Id, nil))
			}()
			select {
			case <-secondDone:
			case <-time.After(200 * time.Millisecond):
				t.Error("a blocked client write held the global mutex and stalled an unrelated read")
			}
			close(writer.release)
			<-firstDone
			<-secondDone
		})
	}
}
