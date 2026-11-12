package access

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JohanSardar/lamp/internal/surface"
	"github.com/JohanSardar/lamp/pkg/api"
	ssh "github.com/gliderlabs/ssh"
)

func randomID() string { var b [16]byte; _, _ = rand.Read(b[:]); return hex.EncodeToString(b[:]) }
func descriptor(name string) api.OperationDescriptor {
	for _, op := range api.Operations {
		if op.Name == name {
			return op
		}
	}
	panic("unknown operation: " + name)
}

type backend struct{ client *surface.Client }

type guestExit struct{ code int }

func (e *guestExit) Error() string { return fmt.Sprintf("guest shell exited with status %d", e.code) }

func (b backend) call(ctx context.Context, name string, in map[string]any) (map[string]any, error) {
	op := descriptor(name)
	if op.Mutation {
		in["idempotency_key"] = randomID()
		in["wait_ms"] = uint64(60000)
	}
	data, status, e := b.client.Call(ctx, op, in)
	if e != nil {
		return nil, e
	}
	m, e := surface.Decode(data)
	if e != nil {
		return nil, errors.New("invalid control response")
	}
	if status < 200 || status >= 300 {
		code := "DATA_UNAVAILABLE"
		if detail, ok := m["error"].(map[string]any); ok {
			if s, ok := detail["code"].(string); ok {
				code = s
			}
			if strings.HasPrefix(name, "session_") && code == "NOT_FOUND" && text(detail, "message") == "operation not installed" {
				return nil, errors.New("sessions are not available yet on this daemon")
			}
		}
		return nil, fmt.Errorf("control %s: %s", name, code)
	}
	if op.Mutation && m["result"] == nil {
		return nil, fmt.Errorf("control %s has not completed; inspect operation %v", name, m["operation_id"])
	}
	return m, nil
}
func field(m map[string]any, k string) map[string]any { v, _ := m[k].(map[string]any); return v }
func text(m map[string]any, k string) string          { v, _ := m[k].(string); return v }

// The console uses only public operations. It has no worker, filesystem or host-exec authority.
func (s *Service) console(session ssh.Session) {
	u, ok := s.user(session.User())
	if !ok {
		session.Exit(1)
		return
	}
	t, e := token(u.TokenFile)
	if e != nil {
		fmt.Fprintln(session.Stderr(), "principal credential unavailable")
		session.Exit(1)
		return
	}
	client := &surface.Client{Socket: s.config.APISocket, Token: t}
	defer client.Close()
	b := backend{client}
	args := session.Command()
	if len(args) == 0 {
		args = []string{"sessions"}
	}
	ctx := session.Context()
	switch {
	case len(args) == 1 && args[0] == "sessions":
		var m map[string]any
		m, e = b.call(ctx, "session_list", map[string]any{})
		if e == nil {
			e = json.NewEncoder(session).Encode(m)
		}
	case len(args) == 2 && args[0] == "attach" && safeID(args[1]):
		e = b.attach(session, args[1])
	case len(args) == 2 && args[0] == "watch" && safeID(args[1]):
		e = b.output(ctx, session, session.Stderr(), "session_watch", "session_id", args[1])
	case len(args) == 2 && args[0] == "shell" && safeID(args[1]):
		e = b.shell(session, args[1])
	default:
		e = errors.New("use sessions, watch <session_id>, attach <session_id>, or shell <sandbox_id>")
	}
	if e != nil {
		var status *guestExit
		if errors.As(e, &status) {
			session.Exit(status.code)
			return
		}
		fmt.Fprintln(session.Stderr(), e)
		session.Exit(1)
		return
	}
	session.Exit(0)
}
func safeID(s string) bool { return s != "" && len(s) <= 256 && !strings.ContainsAny(s, "/\\ \r\n\t") }

// shell is the bounded line console supported by today's guest exec API. Every
// line runs in a fresh guest /bin/sh, never the access host. T21 provides PTYs.
func (b backend) shell(s ssh.Session, id string) error {
	// Attaching is activity: a suspended sandbox resumes before the prompt.
	wake, cancel := context.WithTimeout(s.Context(), 5*time.Minute)
	e := surface.WakeSandbox(wake, b.client, id)
	cancel()
	if e != nil {
		return e
	}
	_, _, interactive := s.Pty()
	if interactive {
		fmt.Fprintln(s, "Lamp guest command console; each line starts a new shell. Ctrl-D exits.")
	}
	scan := bufio.NewScanner(s)
	scan.Buffer(make([]byte, 4096), 48<<10)
	status := 0
	finish := func() error {
		if status != 0 {
			return &guestExit{status}
		}
		return nil
	}
	for {
		if interactive {
			fmt.Fprint(s, "$ ")
		}
		if !scan.Scan() {
			if e := scan.Err(); e != nil {
				return e
			}
			return finish()
		}
		line := scan.Text()
		if line == "exit" {
			return finish()
		}
		m, e := b.call(s.Context(), "exec_create", map[string]any{"sandbox_id": id, "argv": []string{"/bin/sh", "-c", line}})
		if e != nil {
			return e
		}
		execID := text(field(m, "result"), "exec_id")
		if execID == "" {
			return errors.New("missing guest exec identity")
		}
		if e = b.output(s.Context(), s, s.Stderr(), "exec_watch", "exec_id", execID); e != nil {
			return e
		}
		m, e = b.call(s.Context(), "exec_get", map[string]any{"exec_id": execID})
		if e != nil {
			return e
		}
		command := field(m, "data")
		if text(command, "state") != "exited" {
			return errors.New("guest command completion unknown")
		}
		status, e = strconv.Atoi(fmt.Sprint(command["exit_code"]))
		if e != nil || status < 0 || status > 255 {
			return errors.New("invalid guest exit status")
		}
	}
}
func (b backend) output(ctx context.Context, stdout, stderr io.Writer, op, key, id string) error {
	cursor := ""
	for {
		in := map[string]any{key: id, "wait_ms": uint64(1000)}
		if cursor != "" {
			in["cursor"] = cursor
		}
		m, e := b.call(ctx, op, in)
		if e != nil {
			return e
		}
		items, _ := m["items"].([]any)
		for _, raw := range items {
			f, _ := raw.(map[string]any)
			channel := text(f, "channel")
			if channel != "stdout" && channel != "stderr" {
				continue
			}
			data, e := base64.StdEncoding.DecodeString(text(f, "data_base64"))
			if e != nil {
				return errors.New("invalid output frame")
			}
			writer := stdout
			if channel == "stderr" {
				writer = stderr
			}
			if _, e = writer.Write(data); e != nil {
				return e
			}
		}
		next := text(m, "next_cursor")
		if next == "" {
			return nil
		}
		cursor = next
		if len(items) == 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
}
func (b backend) attach(s ssh.Session, id string) error {
	ctx, cancel := context.WithCancel(s.Context())
	defer cancel()
	m, e := b.call(ctx, "session_get", map[string]any{"session_id": id})
	if e != nil {
		return e
	}
	resource := field(m, "data")
	epoch := resource["location_epoch"]
	l, e := b.call(ctx, "lease_create", map[string]any{"type": "input", "resource_id": id, "expires_at": time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339Nano)})
	if e != nil {
		return e
	}
	lease := text(field(l, "result"), "lease_id")
	if lease == "" {
		return errors.New("missing input lease")
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		v, e := b.call(cleanup, "lease_get", map[string]any{"lease_id": lease})
		if e == nil {
			_, _ = b.call(cleanup, "lease_release", map[string]any{"lease_id": lease, "expected_revision": field(v, "data")["revision"]})
		}
	}()
	_, e = b.call(ctx, "session_attach", map[string]any{"session_id": id, "lease_id": lease, "expected_revision": resource["revision"]})
	if e != nil {
		return e
	}
	ctx, stop := context.WithTimeout(ctx, 4*time.Minute)
	defer stop() // before lease expiration
	var mutation sync.Mutex
	mutate := func(op string, in map[string]any) error {
		mutation.Lock()
		defer mutation.Unlock()
		v, e := b.call(ctx, "session_get", map[string]any{"session_id": id})
		if e != nil {
			return e
		}
		current := field(v, "data")
		if fmt.Sprint(current["location_epoch"]) != fmt.Sprint(epoch) {
			return errors.New("session moved; reconnect")
		}
		in["session_id"] = id
		in["lease_id"] = lease
		in["expected_revision"] = current["revision"]
		_, e = b.call(ctx, op, in)
		return e
	}
	done := make(chan error, 2)
	go func() {
		buf := make([]byte, 8192)
		var sequence uint64
		for {
			n, e := s.Read(buf)
			if n > 0 {
				if e = mutate("session_input", map[string]any{"location_epoch": epoch, "sequence": sequence, "data_base64": base64.StdEncoding.EncodeToString(buf[:n])}); e != nil {
					done <- e
					return
				}
				sequence++
			}
			if e != nil {
				done <- e
				return
			}
		}
	}()
	if pty, windows, ok := s.Pty(); ok {
		if e = mutate("session_resize", map[string]any{"rows": pty.Window.Height, "columns": pty.Window.Width}); e != nil {
			return e
		}
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case w, ok := <-windows:
					if !ok {
						return
					}
					if e := mutate("session_resize", map[string]any{"rows": w.Height, "columns": w.Width}); e != nil {
						done <- e
						return
					}
				}
			}
		}()
	}
	go func() { done <- b.output(ctx, s, s.Stderr(), "session_watch", "session_id", id) }()
	select {
	case e = <-done:
		if e == io.EOF {
			return nil
		}
		return e
	case <-ctx.Done():
		return ctx.Err()
	}
}
