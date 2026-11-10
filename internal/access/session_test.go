package access

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// The real core has no session backend until T21. This one adapter test checks
// the public lease/input/resize protocol without claiming a live guest session.
func TestPublicSessionAdapter(t *testing.T) {
	dir, err := os.MkdirTemp("", "lamp-attach-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	for _, name := range []string{"host-ca", "user-ca", "identity"} {
		if err = InitCA(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	credential := filepath.Join(dir, "token")
	if err = os.WriteFile(credential, []byte("adapter-principal"), 0600); err != nil {
		t.Fatal(err)
	}
	signer, err := loadSigner(filepath.Join(dir, "identity"))
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	seen := map[string]bool{}
	input := make(chan struct{})
	released := make(chan struct{})
	l, err := net.Listen("unix", filepath.Join(dir, "api.sock"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer adapter-principal" {
			t.Error("principal token was not preserved")
			w.WriteHeader(403)
			return
		}
		if r.Method == "POST" && r.Header.Get("Idempotency-Key") == "" {
			t.Error("missing idempotency key")
		}
		reply := map[string]any{"request_id": "req_adapter"}
		var body map[string]any
		if r.Method == "POST" {
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("invalid mutation body")
			}
			reply["result"] = map[string]any{}
		}
		mu.Lock()
		seen[r.Method+" "+r.URL.Path] = true
		mu.Unlock()
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/sessions":
			reply["items"] = []any{map[string]any{"id": "ss_one"}}
		case "GET /v1/sessions/ss_one":
			reply["data"] = map[string]any{"revision": 7, "location_epoch": 2}
		case "POST /v1/leases":
			if body["resource_id"] != "ss_one" || body["type"] != "input" {
				t.Error("lease not bound to session")
			}
			reply["result"] = map[string]any{"lease_id": "ls_input"}
		case "POST /v1/sessions/ss_one:attach", "POST /v1/sessions/ss_one:resize", "POST /v1/sessions/ss_one:input":
			if body["lease_id"] != "ls_input" || r.Header.Get("If-Match") != "7" {
				t.Error("missing lease or revision")
			}
			if strings.HasSuffix(r.URL.Path, ":input") {
				if body["location_epoch"] != float64(2) || body["sequence"] != float64(0) || body["data_base64"] != base64.StdEncoding.EncodeToString([]byte("hello")) {
					t.Error("input was not sequenced and epoch bound")
				}
				close(input)
			}
		case "GET /v1/sessions/ss_one/output":
			select {
			case <-input:
			case <-ctx.Done():
				return
			}
			reply["items"] = []any{map[string]any{"channel": "stdout", "data_base64": base64.StdEncoding.EncodeToString([]byte("attached\n"))}}
		case "GET /v1/leases/ls_input":
			reply["data"] = map[string]any{"revision": 1}
		case "POST /v1/leases/ls_input:release":
			close(released)
		default:
			t.Errorf("unexpected public call %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(reply)
	})}
	go server.Serve(l)
	defer server.Close()
	s, err := New(Config{StateDir: filepath.Join(dir, "access"), APISocket: filepath.Join(dir, "api.sock"), SSHListen: "127.0.0.1:0", HostCAFile: filepath.Join(dir, "host-ca"), UserCAFile: filepath.Join(dir, "user-ca"), Hostnames: []string{"console.eilmer.si"}, Users: []User{{Name: "owner", TokenFile: credential, AuthorizedKeys: []string{string(ssh.MarshalAuthorizedKey(signer.PublicKey()))}}}})
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan string, 1)
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx, func(_, address string) { ready <- address }) }()
	defer func() { cancel(); <-done }()
	var address string
	select {
	case address = <-ready:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	ca, err := loadSigner(filepath.Join(dir, "host-ca"))
	if err != nil {
		t.Fatal(err)
	}
	checker := &ssh.CertChecker{IsHostAuthority: func(k ssh.PublicKey, _ string) bool { return string(k.Marshal()) == string(ca.PublicKey().Marshal()) }}
	client, err := ssh.Dial("tcp", address, &ssh.ClientConfig{User: "owner", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: func(_ string, remote net.Addr, k ssh.PublicKey) error {
		return checker.CheckHostKey("console.eilmer.si:22", remote, k)
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	list, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	out, err := list.Output("sessions")
	list.Close()
	if err != nil || !strings.Contains(string(out), "ss_one") {
		t.Fatalf("sessions: %s, %v", out, err)
	}
	attach, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer attach.Close()
	if err = attach.RequestPty("xterm", 24, 80, ssh.TerminalModes{}); err != nil {
		t.Fatal(err)
	}
	stdin, err := attach.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := attach.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = attach.Start("attach ss_one"); err != nil {
		t.Fatal(err)
	}
	if _, err = io.WriteString(stdin, "hello"); err != nil {
		t.Fatal(err)
	}
	out, err = io.ReadAll(stdout)
	if err != nil {
		t.Fatal(err)
	}
	if err = attach.Wait(); err != nil {
		t.Fatal(err)
	}
	if string(out) != "attached\r\n" {
		t.Fatalf("output %q", out)
	}
	select {
	case <-released:
	case <-ctx.Done():
		t.Fatal("input lease not released")
	}
	mu.Lock()
	defer mu.Unlock()
	if !seen["POST /v1/sessions/ss_one:resize"] {
		t.Error("PTY resize was not forwarded")
	}
}
