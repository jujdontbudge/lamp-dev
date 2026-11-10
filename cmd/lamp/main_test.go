package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestCLIUsesCanonicalMutationHeaders(t *testing.T) {
	for _, action := range []string{"create", "exec", "fork"} {
		t.Run(action, func(t *testing.T) {
			dir, err := os.MkdirTemp("", "lamp-cli-headers-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			socket := filepath.Join(dir, "api.sock")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			failures := make(chan string, 1)
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, err := io.ReadAll(r.Body)
				if err != nil {
					failures <- err.Error()
					return
				}
				var body map[string]json.RawMessage
				if err := json.Unmarshal(data, &body); err != nil {
					failures <- err.Error()
					return
				}
				failure := ""
				if r.Header.Get("Idempotency-Key") != "cli-key" {
					failure = "CLI omitted Idempotency-Key header"
				}
				if _, ok := body["idempotency_key"]; ok {
					failure = "CLI sent idempotency_key in the JSON body"
				}
				if action == "fork" {
					if r.Header.Get("If-Match") != "7" {
						failure = "CLI omitted If-Match header"
					}
					if _, ok := body["expected_revision"]; ok {
						failure = "CLI sent expected_revision in JSON body"
					}
					if _, ok := body["sandbox_id"]; ok {
						failure = "CLI sent fork path parameter in JSON body"
					}
				}
				failures <- failure
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintln(w, `{"operation_id":"op_test","resource_ids":[],"request_id":"req_test","replayed":false}`)
			})}
			go server.Serve(listener)
			defer server.Close()
			args := []string{"sandbox", action, "--socket", socket, "--idempotency-key", "cli-key", "--json"}
			switch action {
			case "create":
				args = append(args, "--template-id", "tpl_demo")
			case "exec":
				args[0], args[1] = "exec", "create"
				args = append(args, "--sandbox-id", "sb_test", "--", "/bin/true")
			case "fork":
				args = append(args, "--sandbox-id", "sb_test", "--expected-revision", "7")
			}
			var out, stderr bytes.Buffer
			if code := run(args, &out, &stderr); code != 0 {
				t.Fatalf("CLI exit %d: %s", code, stderr.String())
			}
			if failure := <-failures; failure != "" {
				t.Fatal(failure)
			}
		})
	}
}
