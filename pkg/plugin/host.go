package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strconv"
	"sync"
	"time"
)

type StartOptions struct {
	ID                   string
	Package              *Package
	Executable, StateDir string
	// Launcher is the installed host-built tools/plugin-launcher.c, not plugin input.
	Launcher, LauncherDigest string
	// DiagnosticSoftLimits permits macOS's sampled, non-qualifying resource watcher.
	// Production callers must leave this false until hard quotas are qualified.
	DiagnosticSoftLimits bool
	Authority            Authority
}
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("plugin RPC %d: %s", e.Code, e.Message) }

type Hello struct {
	ProtocolVersion string   `json:"protocol_version"`
	Minor           int      `json:"minor"`
	APIMajor        int      `json:"api_major"`
	Type            string   `json:"type"`
	ManifestDigest  string   `json:"manifest_digest"`
	Features        []string `json:"features"`
	Methods         []string `json:"methods"`
}
type Invocation struct {
	Binding Binding         `json:"binding"`
	Grant   Grant           `json:"grant"`
	Input   json.RawMessage `json:"input"`
}
type reply struct {
	result json.RawMessage
	err    error
}
type pending struct {
	reply   chan reply
	binding Binding
	grant   string
	ctx     context.Context
	cancel  context.CancelFunc
	effects int
}
type Worker struct {
	mu           sync.Mutex
	pkg          *Package
	id           string
	authority    Authority
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	stdout       io.ReadCloser
	stderr       io.ReadCloser
	cleanup      func() error
	cleanupErr   error
	pending      map[string]*pending
	grants       map[string]Grant
	next         uint64
	reason       error
	writes       chan Message
	stopped      chan struct{}
	done         chan struct{}
	exited       chan struct{}
	breakOnce    sync.Once
	effects      chan struct{}
	seenRequests map[string]bool // read-loop owned; bounded worker lifetime
}

func Start(ctx context.Context, o StartOptions) (*Worker, error) {
	if o.Package == nil || o.Authority == nil || !boundedID(o.ID) {
		return nil, errors.New("admitted package, plugin identity and core authority required")
	}
	cmd, cleanup, err := prepare(o)
	if err != nil {
		return nil, err
	}
	w := &Worker{pkg: o.Package, id: o.ID, authority: o.Authority, cmd: cmd, cleanup: cleanup, pending: map[string]*pending{}, grants: map[string]Grant{}, writes: make(chan Message, o.Package.manifest.Limits.Concurrent), stopped: make(chan struct{}), done: make(chan struct{}), exited: make(chan struct{}), effects: make(chan struct{}, o.Package.manifest.Limits.Concurrent)}
	if w.stdin, err = cmd.StdinPipe(); err != nil {
		_ = cleanup()
		return nil, err
	}
	if w.stdout, err = cmd.StdoutPipe(); err != nil {
		w.stdin.Close()
		_ = cleanup()
		return nil, err
	}
	if w.stderr, err = cmd.StderrPipe(); err != nil {
		w.stdin.Close()
		w.stdout.Close()
		_ = cleanup()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		w.stdin.Close()
		w.stdout.Close()
		w.stderr.Close()
		_ = cleanup()
		return nil, err
	}
	go func() {
		err := cmd.Wait()
		close(w.exited)
		if err == nil {
			err = errors.New("plugin exited")
		}
		w.trip(err)
	}()
	go w.writeLoop()
	go w.readLoop()
	go w.stderrLoop()
	go w.monitor()
	h := Hello{ProtocolVersion: Protocol, Minor: min(Minor, o.Package.manifest.Compatibility.MaxMinor), APIMajor: 1, Type: o.Package.manifest.Type, ManifestDigest: o.Package.digest, Features: []string{"typed-grants", "cancel"}}
	for method := range o.Package.manifest.Methods {
		h.Methods = append(h.Methods, method)
	}
	slices.Sort(h.Methods)
	params, _ := json.Marshal(h)
	raw, err := w.request(ctx, "initialize", params, Binding{}, "")
	var got Hello
	if err == nil {
		err = decode(raw, &got)
	}
	if err == nil && (got.ProtocolVersion != Protocol || got.Minor != h.Minor || got.APIMajor != 1 || got.Type != h.Type || got.ManifestDigest != h.ManifestDigest || !sameStrings(got.Methods, h.Methods)) {
		err = errors.New("plugin handshake identity/API mismatch")
	}
	if err == nil {
		for _, feature := range append([]string{"typed-grants", "cancel"}, o.Package.manifest.RequiredFeatures...) {
			if !slices.Contains(got.Features, feature) {
				err = errors.New("plugin missing required feature")
			}
		}
	}
	if err != nil {
		w.trip(err)
		<-w.done
		return nil, err
	}
	return w, nil
}
func sameStrings(a, b []string) bool {
	a = slices.Clone(a)
	b = slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}
func (w *Worker) Done() <-chan struct{} { return w.done }
func (w *Worker) Err() error            { w.mu.Lock(); defer w.mu.Unlock(); return w.reason }
func (w *Worker) Close() error          { w.trip(errors.New("plugin disabled")); <-w.done; return w.cleanupErr }
func (w *Worker) trip(err error) {
	w.breakOnce.Do(func() {
		w.mu.Lock()
		w.reason = err
		clear(w.grants)
		for id, p := range w.pending {
			p.cancel()
			p.reply <- reply{err: err}
			delete(w.pending, id)
		}
		close(w.stopped)
		w.mu.Unlock()
		// Never block a transport reader or the process waiter on teardown itself.
		go func() {
			w.stdin.Close()
			w.stdout.Close()
			w.stderr.Close()
			w.cleanupErr = w.cleanup()
			<-w.exited
			w.authority.Revoke(w.id)
			w.authority.FailDependent(w.id, "plugin circuit open")
			close(w.done)
		}()
	})
}
func (w *Worker) send(ctx context.Context, m Message) error {
	select {
	case <-w.stopped:
		return w.Err()
	default:
	}
	select {
	case w.writes <- m:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-w.stopped:
		return w.Err()
	}
}
func (w *Worker) writeLoop() {
	for {
		select {
		case <-w.stopped:
			return
		case m := <-w.writes:
			b, err := json.Marshal(m)
			if err == nil {
				err = WriteFrame(w.stdin, b)
			}
			if err != nil {
				w.trip(err)
				return
			}
		}
	}
}
func (w *Worker) request(ctx context.Context, method string, params json.RawMessage, b Binding, grant string) (json.RawMessage, error) {
	deadline := time.Now().Add(time.Duration(w.pkg.manifest.Limits.DeadlineMS) * time.Millisecond)
	if !b.Deadline.IsZero() && b.Deadline.Before(deadline) {
		deadline = b.Deadline
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	w.mu.Lock()
	if w.reason != nil {
		err := w.reason
		w.mu.Unlock()
		return nil, err
	}
	if len(w.pending) >= w.pkg.manifest.Limits.Concurrent {
		w.mu.Unlock()
		return nil, errors.New("plugin concurrency limit")
	}
	w.next++
	id := "h-" + strconv.FormatUint(w.next, 10)
	p := &pending{reply: make(chan reply, 1), binding: b, grant: grant, ctx: ctx, cancel: cancel}
	w.pending[id] = p
	w.mu.Unlock()
	if err := w.send(ctx, Message{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		w.trip(err)
		return nil, err
	}
	select {
	case r := <-p.reply:
		return r.result, r.err
	case <-ctx.Done():
		if err := w.Err(); err != nil {
			return nil, err
		}
		// Cancellation is sent best-effort; kill the worker rather than trusting it
		// to stop. A timed-out effect has unknown outcome, never automatic retry.
		raw, _ := json.Marshal(map[string]string{"id": id})
		select {
		case w.writes <- Message{JSONRPC: "2.0", Method: "cancel", Params: raw}:
		default:
		}
		w.trip(ctx.Err())
		return nil, ctx.Err()
	}
}
func (w *Worker) Call(ctx context.Context, method string, b Binding, g Grant, input json.RawMessage) (json.RawMessage, error) {
	spec, ok := w.pkg.manifest.Methods[method]
	if !ok {
		return nil, errors.New("undeclared plugin method")
	}
	if err := spec.Input.Validate(input); err != nil {
		return nil, err
	}
	w.mu.Lock()
	installed, ok := w.grants[g.ID]
	allowed := ok && b.valid(w.pkg.owner) && b.within(installed.Binding) && g.Kind == installed.Kind && g.ResourceID == installed.ResourceID
	w.mu.Unlock()
	if !allowed {
		return nil, errors.New("grant binding denied")
	}
	// Copy caller bytes so later mutation cannot change an authorized request.
	raw, err := json.Marshal(Invocation{Binding: b, Grant: installed, Input: input})
	if err != nil {
		return nil, err
	}
	result, err := w.request(ctx, method, raw, b, g.ID)
	if err != nil {
		return nil, err
	}
	if err := spec.Result.Validate(result); err != nil {
		w.trip(err)
		return nil, err
	}
	return result, nil
}
func (w *Worker) readLoop() {
	w.seenRequests = make(map[string]bool)
	window := time.Now()
	count := 0
	for {
		raw, err := ReadFrame(w.stdout)
		if err != nil {
			w.trip(err)
			return
		}
		if time.Since(window) >= time.Second {
			window = time.Now()
			count = 0
		}
		count++
		if count > w.pkg.manifest.Limits.FramesPerSecond {
			w.trip(errors.New("plugin output flood"))
			return
		}
		var m Message
		if err := decode(raw, &m); err != nil || m.JSONRPC != "2.0" || !boundedID(m.ID) {
			w.trip(errors.New("malformed plugin RPC"))
			return
		}
		if m.Method != "" {
			if m.Method != "host.effect" || m.Result != nil || m.Error != nil || m.Params == nil {
				w.trip(errors.New("unsupported plugin request"))
				return
			}
			if w.seenRequests[m.ID] || len(w.seenRequests) >= 4096 {
				w.trip(errors.New("plugin request replay or lifetime limit"))
				return
			}
			w.seenRequests[m.ID] = true
			select {
			case w.effects <- struct{}{}:
				go w.effect(m)
			default:
				w.trip(errors.New("plugin effect queue flood"))
				return
			}
			continue
		}
		if m.Params != nil || (m.Result == nil) == (m.Error == nil) {
			w.trip(errors.New("invalid plugin response"))
			return
		}
		w.mu.Lock()
		p, ok := w.pending[m.ID]
		if ok {
			delete(w.pending, m.ID)
		}
		w.mu.Unlock()
		if !ok {
			w.trip(errors.New("unsolicited or duplicate plugin response"))
			return
		}
		if m.Error != nil {
			p.reply <- reply{err: m.Error}
		} else {
			p.reply <- reply{result: m.Result}
		}
	}
}
func (w *Worker) effect(m Message) {
	defer func() { <-w.effects }()
	deny := func() {
		_ = w.send(context.Background(), Message{JSONRPC: "2.0", ID: m.ID, Error: &RPCError{Code: -32001, Message: "effect denied"}})
	}
	var e Effect
	if err := decode(m.Params, &e); err != nil {
		deny()
		return
	}
	w.mu.Lock()
	g, gok := w.grants[e.GrantID]
	p, pok := w.pending[e.CallID]
	allowed := gok && pok && w.reason == nil && p.grant == g.ID && e.Kind == g.Kind && e.ResourceID == g.ResourceID && e.Binding.valid(w.pkg.owner) && e.Binding.within(g.Binding) && e.Binding.within(p.binding) && e.Binding.Deadline.Equal(p.binding.Deadline) && p.effects < 16
	if allowed {
		p.effects++
	}
	w.mu.Unlock()
	if !allowed {
		deny()
		return
	}
	result, err := w.authority.Effect(p.ctx, e)
	if err != nil || p.ctx.Err() != nil {
		deny()
		return
	}
	if err := StrictJSON(result); err != nil {
		w.trip(errors.New("invalid core effect result"))
		return
	}
	_ = w.send(p.ctx, Message{JSONRPC: "2.0", ID: m.ID, Result: result})
}
func (w *Worker) stderrLoop() {
	// stderr is diagnostics, not an unbounded retained log or secret-bearing error.
	_, err := io.CopyN(io.Discard, w.stderr, int64(w.pkg.manifest.Limits.StderrBytes)+1)
	if err == nil {
		w.trip(errors.New("plugin stderr flood"))
	} else if err != io.EOF {
		w.trip(err)
	}
}
