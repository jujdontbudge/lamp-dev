package control

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	pb "lamp.eilmer.si/pkg/workerpb"
)

// Transport fixture supplies the exact JSON produced by the guest job wrapper.
func t44ReviewService(t *testing.T, poll func() string, released ...func()) *Service {
	t.Helper()
	cfg := storeConfig(t)
	dir, err := os.MkdirTemp("/tmp", "t44-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	cfg.Worker.Socket = filepath.Join(dir, "w.sock")
	l, err := net.Listen("unix", cfg.Worker.Socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, e := l.Accept()
			if e != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				hello, e := pb.ReadFrame(c)
				if e != nil {
					return
				}
				if pb.WriteFrame(c, hello) != nil {
					return
				}
				for {
					frame, e := pb.ReadFrame(c)
					if e != nil {
						return
					}
					r := frame.GetRequest()
					receipt := &pb.Receipt{State: "inspected"}
					switch {
					case r.GetAllocate() != nil:
						receipt.State = "allocated"
						if r.GetAllocate().ValidateOnly {
							receipt.State = "validated"
						}
					case r.GetRestore() != nil:
						receipt.State = "restored"
					case r.GetStart() != nil:
						receipt.State = "command_started"
					case r.GetInspect() != nil:
						receipt.Command = &pb.CommandResult{FinishedAtUnixNs: 1}
					case r.GetStop() != nil:
						receipt.State = "stopped"
					case r.GetRelease() != nil:
						receipt.State = "released"
						for _, notify := range released {
							notify()
						}
					case r.GetSessionCall() != nil:
						var q struct {
							Argv []string `json:"argv"`
						}
						json.Unmarshal(r.GetSessionCall().RequestJson, &q)
						output := `{"installed":true}`
						if len(q.Argv) > 2 && q.Argv[1] != "-c" {
							output = `{"job":"0123456789abcdef0123456789abcdef"}`
							if q.Argv[2] == "--poll" {
								output = poll()
							}
						}
						raw, _ := json.Marshal(map[string]any{"exit_code": 0, "stdout_base64": base64.StdEncoding.EncodeToString([]byte(output))})
						receipt.Detail = map[string]string{"response_json": string(raw)}
					}
					if pb.WriteFrame(c, &pb.Frame{Payload: &pb.Frame_Response{Response: &pb.Response{Context: r.Context, Result: &pb.Response_Receipt{Receipt: receipt}}}}) != nil {
						return
					}
				}
			}(c)
		}
	}()
	s, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func t44ReviewHTTP(s *Service, method, path, body, key string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if strings.HasSuffix(path, ":stop") {
		r.Header.Set("If-Match", "1")
	}
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestT44ReviewAcknowledgedRelayTimeout(t *testing.T) {
	s := t44ReviewService(t, func() string { return `{"outcome_unknown":true,"error":"TimeoutError","message":"timed out"}` })
	s.readLock()
	sb := newSandbox()
	sb.Id = "sb_review"
	sb.OwnerId = "pr_local"
	sb.State = "ready"
	s.journal.Sandboxes[sb.Id] = sb
	s.mu.Unlock()
	w := t44ReviewHTTP(s, "POST", "/v1/sandboxes/sb_review:run-code", `{"code":"import time; time.sleep(1101); print('effect completed')","wait_ms":1000}`, "timeout")
	t.Logf("POST run-code: HTTP %d %s", w.Code, w.Body.String())
	if !strings.Contains(w.Body.String(), `"state":"outcome_unknown"`) {
		t.Fatal("acknowledged job lost its runtime connection; expected outcome_unknown")
	}
}

func TestT44ReviewStoppedTaskDuringCreateKit(t *testing.T) {
	var s *Service
	var stopped atomic.Bool
	var polls atomic.Int32
	var released atomic.Bool
	secondPoll := make(chan struct{}, 1)
	s = t44ReviewService(t, func() string {
		if stopped.Load() {
			if polls.Add(1) == 2 {
				secondPoll <- struct{}{}
			}
		}
		return `{"pending":true}`
	}, func() { released.Store(true) })
	w := t44ReviewHTTP(s, "POST", "/v1/sandboxes", `{"template_id":"tpl","kit_ids":["python"]}`, "create")
	if w.Code != 202 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		OperationId string `json:"operation_id"`
		ResourceIds []struct {
			Id string `json:"id"`
		} `json:"resource_ids"`
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	id := out.ResourceIds[0].Id
	s.readLock()
	task := s.journal.Sandboxes[id].TaskId
	s.mu.Unlock()
	w = t44ReviewHTTP(s, "POST", "/v1/tasks/"+*task+":stop", `{}`, "stop")
	if w.Code != 200 {
		t.Fatalf("stop: %d %s", w.Code, w.Body.String())
	}
	stopped.Store(true)
	s.readLock()
	done := s.done[out.OperationId]
	s.mu.Unlock()
	select {
	case <-done:
	case <-secondPoll:
		t.Fatal("stopped Task continued kit polling instead of releasing its VM")
	}
	if !released.Load() {
		t.Fatal("stopped Task did not release its VM")
	}
	w = t44ReviewHTTP(s, "GET", "/v1/sandboxes/"+id, "", "")
	t.Logf("after task_stop, kit polls=%d; GET sandbox: HTTP %d %s", polls.Load(), w.Code, w.Body.String())
	if !strings.Contains(w.Body.String(), `"state":"failed"`) && !strings.Contains(w.Body.String(), `"state":"deleted"`) {
		t.Fatal("stopped Task still has a live preparing VM while create --kit polls")
	}
}
