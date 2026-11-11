package broker

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type Grant struct {
	Sandbox     string              `json:"sandbox_id"`
	Generation  uint64              `json:"generation"`
	Peer        string              `json:"peer_ip"`
	Connections map[string][]string `json:"connections"`
	Settings    []string            `json:"settings"`
	TTL         int                 `json:"ttl_seconds"`
	expires     time.Time
	id          string
}
type Invocation struct {
	Connection string         `json:"connection_id"`
	Operation  string         `json:"operation"`
	Arguments  map[string]any `json:"arguments"`
	Approval   string         `json:"approval_id,omitempty"`
}
type proposal struct {
	ID         string     `json:"id"`
	Digest     string     `json:"digest"`
	GrantID    string     `json:"-"`
	Sandbox    string     `json:"sandbox_id"`
	Generation uint64     `json:"generation"`
	Invocation Invocation `json:"invocation"`
	Expires    time.Time  `json:"expires_at"`
	Approved   bool       `json:"approved"`
}
type flight struct {
	Grant, Connection string
	Cancel            context.CancelFunc
}
type Broker struct {
	flights   map[string]flight
	config    Config
	slots     chan struct{}
	mu        sync.Mutex
	grants    map[string]Grant // keyed by SHA-256, never raw task bearer
	proposals map[string]*proposal
	changes   map[string]*settingsChange
	client    *http.Client
	audit     *os.File
}

func New(c Config, audit *os.File) (*Broker, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	// No environment proxy, redirects, cookies, automatic retries or shared owner
	// connection pools. Each process belongs to one OS owner.
	roots, err := x509.SystemCertPool()
	if err != nil {
		return nil, errors.New("system trust unavailable")
	}
	for _, path := range c.UpstreamCAFiles {
		pem, err := os.ReadFile(path)
		if err != nil || !roots.AppendCertsFromPEM(pem) {
			return nil, errors.New("invalid upstream CA")
		}
	}
	tr := &http.Transport{Proxy: nil, DisableKeepAlives: true, MaxResponseHeaderBytes: 16384, TLSHandshakeTimeout: 10 * time.Second, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	return &Broker{flights: map[string]flight{}, slots: make(chan struct{}, 32), config: c, grants: map[string]Grant{}, proposals: map[string]*proposal{}, changes: map[string]*settingsChange{}, audit: audit, client: &http.Client{Transport: tr, Timeout: 40 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect denied") }}}, nil
}
func randomID() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("system entropy unavailable")
	}
	return hex.EncodeToString(b[:])
}
func digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func contains(a []string, s string) bool {
	for _, v := range a {
		if v == s {
			return true
		}
	}
	return false
}
func decode(r *http.Request, v any) error {
	d := json.NewDecoder(io.LimitReader(r.Body, limit+1))
	d.UseNumber()
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return errors.New("INVALID_ARGUMENT")
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("INVALID_ARGUMENT")
	}
	return nil
}
func send(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, code string) {
	status := http.StatusForbidden
	switch code {
	case "INVALID_ARGUMENT":
		status = 400
	case "UPSTREAM_UNAVAILABLE", "CREDENTIAL_UNAVAILABLE", "OUTPUT_BLOCKED", "OUTCOME_UNKNOWN":
		status = 502
	case "REVISION_CONFLICT":
		status = 409
	}
	send(w, status, map[string]any{"error": map[string]string{"code": code}})
}

// OwnerHandler MUST only be served on an authenticated owner-UID Unix socket.
// Never expose it to a sandbox or on the guest HTTP listener.
func (b *Broker) OwnerHandler() http.Handler { return http.HandlerFunc(b.owner) }
func (b *Broker) TaskHandler() http.Handler  { return http.HandlerFunc(b.task) }
func (b *Broker) owner(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.expire()
	switch {
	case r.Method == "POST" && r.URL.Path == "/owner/grants":
		var g Grant
		if decode(r, &g) != nil || g.Sandbox == "" || g.Generation == 0 || net.ParseIP(g.Peer) == nil || g.TTL < 1 || g.TTL > 300 {
			fail(w, "INVALID_ARGUMENT")
			return
		}
		if len(b.grants) >= 256 {
			fail(w, "SCOPE_DENIED")
			return
		}
		for id, ops := range g.Connections {
			c, ok := b.config.Connections[id]
			if !ok {
				fail(w, "SCOPE_DENIED")
				return
			}
			for _, op := range ops {
				if _, ok := c.Operations[op]; !ok {
					fail(w, "SCOPE_DENIED")
					return
				}
			}
		}
		for _, id := range g.Settings {
			if _, ok := b.config.Settings[id]; !ok {
				fail(w, "SCOPE_DENIED")
				return
			}
		}
		token := randomID()
		g.id = randomID()
		g.expires = time.Now().Add(time.Duration(g.TTL) * time.Second)
		b.grants[digest(token)] = g
		b.log("grant", g.Sandbox, "issued")
		send(w, 200, map[string]any{"token": token, "grant_id": g.id, "expires_at": g.expires})
	case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/owner/access/"):
		id := strings.TrimPrefix(r.URL.Path, "/owner/access/")
		c, ok := b.config.Connections[id]
		var in struct {
			Access string `json:"access"`
		}
		if !ok || decode(r, &in) != nil || !contains([]string{"off", "read_only", "ask_first", "full"}, in.Access) || (c.Kind == "git_sign" && in.Access != "ask_first" && in.Access != "off") {
			fail(w, "INVALID_ARGUMENT")
			return
		}
		c.Access = in.Access
		b.config.Connections[id] = c
		for _, f := range b.flights {
			if f.Connection == id {
				f.Cancel()
			}
		}
		for key, p := range b.proposals {
			if p.Invocation.Connection == id {
				delete(b.proposals, key)
			}
		}
		b.log(id, "owner", "access_changed")
		send(w, 200, map[string]string{"access": in.Access})
	case r.Method == "POST" && r.URL.Path == "/owner/revoke":
		var in struct {
			GrantID string `json:"grant_id"`
		}
		if decode(r, &in) != nil {
			fail(w, "INVALID_ARGUMENT")
			return
		}
		for k, g := range b.grants {
			if g.id == in.GrantID {
				b.log("revoke", g.Sandbox, "revoked")
				delete(b.grants, k)
				for _, f := range b.flights {
					if f.Grant == g.id {
						f.Cancel()
					}
				}
			}
		}
		send(w, 200, map[string]bool{"revoked": true})
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/owner/approvals/"):
		p, ok := b.proposals[strings.TrimPrefix(r.URL.Path, "/owner/approvals/")]
		if !ok {
			fail(w, "SCOPE_DENIED")
			return
		}
		send(w, 200, p)
	case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/owner/approvals/"):
		p, ok := b.proposals[strings.TrimPrefix(r.URL.Path, "/owner/approvals/")]
		var in struct {
			Digest string `json:"digest"`
		}
		if !ok || decode(r, &in) != nil || in.Digest != p.Digest || p.Approved || !time.Now().Before(p.Expires) {
			fail(w, "SCOPE_DENIED")
			return
		}
		p.Approved = true
		b.log("approve", p.Sandbox, "approved")
		send(w, 200, p)
	case strings.HasPrefix(r.URL.Path, "/owner/settings/"):
		b.ownerSettings(w, r)
	default:
		fail(w, "SCOPE_DENIED")
	}
}
func (b *Broker) expire() {
	now := time.Now()
	for k, g := range b.grants {
		if !now.Before(g.expires) {
			delete(b.grants, k)
		}
	}
	for k, p := range b.proposals {
		if !now.Before(p.Expires) {
			delete(b.proposals, k)
		}
	}
	for k, c := range b.changes {
		if !now.Before(c.Expires) {
			delete(b.changes, k)
		}
	}
}
func (b *Broker) task(w http.ResponseWriter, r *http.Request) {
	select {
	case b.slots <- struct{}{}:
		defer func() { <-b.slots }()
	default:
		fail(w, "QUEUE_FULL")
		return
	}
	// Holding the lock through dispatch makes revocation a fence: after its owner
	// response, no old invocation remains running. Upstream effects aren't undone.
	b.mu.Lock()
	defer b.mu.Unlock()
	b.expire()
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	g, ok := b.grants[digest(token)]
	if err != nil || !ok || host != g.Peer || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		fail(w, "SCOPE_DENIED")
		return
	}
	if r.Method != "POST" {
		fail(w, "SCOPE_DENIED")
		return
	}
	if strings.HasPrefix(r.URL.Path, "/v1/settings/") {
		b.taskSettings(w, r, g)
		return
	}
	var in Invocation
	modelRoute := false
	nativeRoute := false
	if r.URL.Path == "/v1/invoke" {
		if decode(r, &in) != nil {
			fail(w, "INVALID_ARGUMENT")
			return
		}
	} else if strings.HasPrefix(r.URL.Path, "/v1/models/") && strings.HasSuffix(r.URL.Path, "/native/chat/completions") {
		// Native OpenAI-compatible harnesses (opencode, pi, goose, droid BYOK)
		// keep their own streaming and tool shapes; the credential stays here.
		in.Connection = strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/models/"), "/native/chat/completions")
		in.Operation = "chat_completions"
		if decode(r, &in.Arguments) != nil {
			fail(w, "INVALID_ARGUMENT")
			return
		}
		modelRoute, nativeRoute = true, true
	} else if strings.HasPrefix(r.URL.Path, "/v1/models/") && strings.HasSuffix(r.URL.Path, "/chat/completions") {
		in.Connection = strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/models/"), "/chat/completions")
		in.Operation = "chat"
		if decode(r, &in.Arguments) != nil {
			fail(w, "INVALID_ARGUMENT")
			return
		}
		// Approval is broker transport metadata, not a model parameter. Remove
		// it before schema checks, action hashing and upstream serialization.
		approval, hasApproval := in.Arguments["approval_id"]
		if hasApproval {
			var ok bool
			in.Approval, ok = approval.(string)
			if !ok {
				fail(w, "INVALID_ARGUMENT")
				return
			}
			delete(in.Arguments, "approval_id")
		}
		header := r.Header.Values("X-Lamp-Approval-Id")
		if len(header) > 1 || len(header) == 1 && hasApproval && header[0] != in.Approval {
			fail(w, "INVALID_ARGUMENT")
			return
		}
		if len(header) == 1 {
			in.Approval = header[0]
		}
		modelRoute = true
	} else if strings.HasPrefix(r.URL.Path, "/v1/models/") && (strings.HasSuffix(r.URL.Path, "/v1/messages") || strings.HasSuffix(r.URL.Path, "/responses")) {
		tail := strings.TrimPrefix(r.URL.Path, "/v1/models/")
		suffix := "/responses"
		in.Operation = "responses"
		if strings.HasSuffix(tail, "/v1/messages") {
			suffix, in.Operation = "/v1/messages", "messages"
		}
		in.Connection = strings.TrimSuffix(tail, suffix)
		if decode(r, &in.Arguments) != nil {
			fail(w, "INVALID_ARGUMENT")
			return
		}
		modelRoute, nativeRoute = true, true
	} else {
		fail(w, "SCOPE_DENIED")
		return
	}
	c, ok := b.config.Connections[in.Connection]
	if !ok || (modelRoute && c.Kind != "model") || !contains(g.Connections[in.Connection], in.Operation) {
		fail(w, "SCOPE_DENIED")
		return
	}
	op, ok := c.Operations[in.Operation]
	if !ok || c.Access == "off" || (c.Access == "read_only" && op.Write) {
		fail(w, "SCOPE_DENIED")
		return
	}
	if err := validateFields(in.Arguments, op.Fields); err != nil {
		fail(w, err.Error())
		return
	}
	if nativeRoute {
		if err := normalizeNative(in); err != nil {
			fail(w, err.Error())
			return
		}
	}
	if err := validateInvocation(c, in); err != nil {
		fail(w, err.Error())
		return
	}
	canonical := in
	canonical.Approval = ""
	hash := digest(struct {
		Grant  string
		Action Invocation
	}{g.id, canonical})
	if c.Access == "ask_first" {
		p, ok := b.proposals[in.Approval]
		if !ok || !p.Approved || p.GrantID != g.id || p.Digest != hash || !time.Now().Before(p.Expires) {
			if len(b.proposals) >= 256 {
				fail(w, "SCOPE_DENIED")
				return
			}
			id := randomID()
			p = &proposal{ID: id, Digest: hash, GrantID: g.id, Sandbox: g.Sandbox, Generation: g.Generation, Invocation: canonical, Expires: g.expires}
			b.proposals[id] = p
			send(w, 403, map[string]any{"error": map[string]string{"code": "APPROVAL_REQUIRED"}, "proposal_id": id, "digest": hash})
			return
		}
		delete(b.proposals, in.Approval) // consumed BEFORE effect; errors never replay
	}
	var reservationID string
	expires := g.expires
	if b.config.AccountabilitySocket != "" {
		tokens, e := reserveTokens(r.Context(), c, in)
		if e != nil {
			fail(w, e.Error())
			return
		}
		reply, e := b.accounting(r.Context(), "reserve", map[string]any{"sandbox_id": g.Sandbox, "tokens": tokens, "type": in.Connection + "." + in.Operation, "exact": c.Kind != "model" || len(c.TokenizerCommand) > 0})
		if e != nil {
			fail(w, e.Error())
			return
		}
		reservationID, _ = reply["reservation_id"].(string)
		if d, ok := reply["deadline_at"].(string); ok {
			if at, e := time.Parse(time.RFC3339Nano, d); e == nil && at.Before(expires) {
				expires = at
			}
		}
	}
	started := time.Now()
	ctx, cancel := context.WithDeadline(r.Context(), expires)
	defer cancel()
	ctx, stop := context.WithTimeout(ctx, 40*time.Second)
	defer stop()
	flightID := randomID()
	b.flights[flightID] = flight{g.id, in.Connection, stop}
	b.mu.Unlock()
	finished := make(chan struct{})
	if reservationID != "" {
		go func() {
			ticker := time.NewTicker(250 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-finished:
					return
				case <-ctx.Done():
					return
				case <-ticker.C:
					if _, e := b.accounting(ctx, "check", map[string]any{"sandbox_id": g.Sandbox}); e != nil {
						stop()
						return
					}
				}
			}
		}()
	}
	var result json.RawMessage
	var nativeContent string
	if nativeRoute {
		result, nativeContent, err = b.nativeModel(ctx, c, in)
	} else {
		result, err = b.execute(ctx, c, in)
	}
	close(finished)
	b.mu.Lock()
	delete(b.flights, flightID)
	if reservationID != "" {
		var actual *uint64
		if err == nil && c.Kind == "model" {
			actual = modelUsage(result)
			if nativeRoute {
				actual = nativeModelUsage(result)
			}
		}
		settleCtx, settleCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, settleErr := b.accounting(settleCtx, "settle", map[string]any{"sandbox_id": g.Sandbox, "reservation_id": reservationID, "actual_tokens": actual, "duration_ms": time.Since(started).Milliseconds()})
		settleCancel()
		if settleErr != nil {
			fail(w, "ACCOUNTING_UNAVAILABLE")
			return
		}
	}
	if err != nil {
		b.log(in.Connection, g.Sandbox, err.Error())
		fail(w, err.Error())
		return
	}
	b.log(in.Connection, g.Sandbox, "completed")
	if nativeRoute {
		w.Header().Set("Content-Type", nativeContent)
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(200)
		_, _ = w.Write(result)
		return
	}
	if modelRoute {
		send(w, 200, result)
		return
	}
	send(w, 200, map[string]any{"data": result})
}
func (b *Broker) log(connection, sandbox, outcome string) {
	// Never log headers, URLs, bodies, command stderr, tokens or settings bytes.
	if b.audit != nil {
		_ = json.NewEncoder(b.audit).Encode(map[string]any{"time": time.Now().UTC(), "connection": connection, "sandbox": sandbox, "outcome": outcome})
		_ = b.audit.Sync()
	}
}
