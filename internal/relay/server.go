// SPDX-License-Identifier: AGPL-3.0-only
package relay

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/JohanSardar/lamp/internal/hostedlogin"
	"github.com/coder/websocket"
)

type carrier struct {
	conn   *websocket.Conn
	write  sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
}
type pending struct {
	fleet string
	host  *carrier
	ctx   context.Context
	ready chan net.Conn
}
type closingConn struct {
	net.Conn
	cancel context.CancelFunc
}

func (c closingConn) Close() error { c.cancel(); return c.Conn.Close() }

type Server struct {
	config       Config
	verifier     *Verifier
	state        *store
	mu           sync.Mutex
	hosts        map[string]*carrier
	pending      map[string]*pending
	slots        chan struct{}
	streamCounts map[string]int
	browserLogin *hostedlogin.Client
}

func New(c Config) (*Server, error) {
	if e := c.Validate(); e != nil {
		return nil, e
	}
	state, e := openStore(c.State)
	if e != nil {
		return nil, e
	}
	s := &Server{config: c, verifier: &Verifier{Issuer: c.Issuer, Audience: c.Audience, Parties: c.Parties, JWKS: c.Issuer + "/.well-known/jwks.json"}, state: state, hosts: map[string]*carrier{}, pending: map[string]*pending{}, slots: make(chan struct{}, 64), streamCounts: map[string]int{}}
	if b := c.Browser; b != nil {
		s.browserLogin, e = hostedlogin.New(b.Origin, b.AccountOrigin)
		if e != nil {
			state.close()
			return nil, e
		}
	}
	return s, nil
}

// Routing and attachment capacity is isolated by configured fleet, not shared
// with short-lived API requests. Control-origin ingress uses separate keys.
func (s *Server) acquireStream(key string, limit int) (func(), bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.streamCounts[key] >= limit {
		return nil, false
	}
	s.streamCounts[key]++
	return sync.OnceFunc(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.streamCounts[key] == 1 {
			delete(s.streamCounts, key)
		} else {
			s.streamCounts[key]--
		}
	}), true
}

func (s *Server) Close() {
	s.mu.Lock()
	for _, h := range s.hosts {
		h.cancel()
		_ = h.conn.CloseNow()
	}
	s.mu.Unlock()
	s.state.close()
}
func (s *Server) fleet(id string) (Fleet, bool) {
	for _, f := range s.config.Fleets {
		if f.ID == id {
			return f, true
		}
	}
	return Fleet{}, false
}
func bearer(r *http.Request) string {
	values := r.Header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
		return ""
	}
	t := strings.TrimPrefix(values[0], "Bearer ")
	if strings.ContainsAny(t, " \r\n\t") {
		return ""
	}
	return t
}
func (s *Server) account(w http.ResponseWriter, r *http.Request, f Fleet, owner bool) (time.Time, bool) {
	var id Identity
	var expiry time.Time
	var e error
	if grant, ok := s.browserAccount(r); ok {
		id = grant.Identity
		expiry = grant.Expires
	} else {
		id, expiry, e = s.verifier.Verify(r.Context(), bearer(r))
	}
	if e != nil {
		http.Error(w, "Eilmer account required", 401)
		return time.Time{}, false
	}
	allowed := id == f.Owner
	if !owner {
		for _, m := range f.Members {
			if id == m {
				allowed = true
			}
		}
	}
	if !allowed {
		http.Error(w, "fleet access denied", 403)
	}
	return expiry, allowed
}
func respond(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func body(w http.ResponseWriter, r *http.Request, v any) bool {
	b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 8192))
	if e != nil || decode(b, v) != nil {
		http.Error(w, "invalid request", 400)
		return false
	}
	return true
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.config.PrivateDevelopment {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
	}
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("Origin-Agent-Cluster", "?1")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	// This service has no browser session, CORS or cookie authentication. Even
	// same-site sibling origins must not exercise account or websocket authority.
	expectedHost := s.config.Hostname
	if s.config.Browser != nil {
		u, _ := url.Parse(s.config.Browser.Origin)
		expectedHost = u.Host
	}
	if (!s.config.PrivateDevelopment && (r.TLS == nil || r.TLS.ServerName != s.config.Hostname)) || (r.Host != expectedHost && !(s.config.PrivateDevelopment && r.Host == s.config.Listen)) {
		http.Error(w, "wrong host", 421)
		return
	}
	if s.browserLogin != nil && r.URL.RawQuery == "" {
		if s.browserLogin.Handle(w, r) {
			return
		}
		if r.Method == "GET" && r.URL.Path == "/" {
			s.browserLogin.Status(w, r)
			return
		}
	}
	if r.Header.Get("Origin") != "" || r.URL.RawQuery != "" || r.Header.Get("Cookie") != "" {
		http.Error(w, "origin, cookie and query credentials are not accepted", 403)
		return
	}
	path := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	releaseAPI := func() {}
	// Attach has its own per-fleet budget; an established stream must never
	// retain the API capacity needed to enroll, connect or replace a carrier.
	if !(len(path) == 3 && path[0] == "v1" && path[1] == "attach" && r.Method == "GET") {
		select {
		case s.slots <- struct{}{}:
			releaseAPI = sync.OnceFunc(func() { <-s.slots })
			defer releaseAPI()
		default:
			http.Error(w, "relay capacity unavailable", 503)
			return
		}
	}
	if len(path) == 2 && path[0] == "v1" && path[1] == "enroll" && r.Method == "POST" {
		s.enroll(w, r)
		return
	}
	if len(path) != 3 || path[0] != "v1" {
		http.NotFound(w, r)
		return
	}
	f, ok := s.fleet(path[2])
	if !ok {
		http.NotFound(w, r)
		return
	}
	switch path[1] {
	case "enrollments":
		if r.Method != "POST" {
			http.NotFound(w, r)
			return
		}
		if _, ok := s.account(w, r, f, true); !ok {
			return
		}
		token := secret()
		expiry := time.Now().Add(60 * time.Second)
		e := s.state.update(func(db *database) error {
			if db.Hosts[f.ID] != "" {
				return errors.New("host already enrolled")
			}
			db.Enrollments[f.ID] = enrollment{hash(token), expiry}
			return nil
		})
		if e != nil {
			http.Error(w, "enrollment unavailable; revoke an existing host before replacement", 409)
			return
		}
		respond(w, map[string]any{"token": token, "expires_at": expiry, "fleet": f.ID})
	case "hosts":
		if r.Method != "DELETE" {
			http.NotFound(w, r)
			return
		}
		if _, ok := s.account(w, r, f, true); !ok {
			return
		}
		if e := s.state.update(func(db *database) error { delete(db.Hosts, f.ID); delete(db.Enrollments, f.ID); return nil }); e != nil {
			http.Error(w, "revocation unavailable", 503)
			return
		}
		s.mu.Lock()
		if h := s.hosts[f.ID]; h != nil {
			h.cancel()
			_ = h.conn.CloseNow()
		}
		s.mu.Unlock()
		w.WriteHeader(204)
	case "carrier":
		if r.Method != "GET" {
			http.NotFound(w, r)
			return
		}
		s.carrier(w, r, f.ID, releaseAPI)
	case "attach":
		if r.Method != "GET" {
			http.NotFound(w, r)
			return
		}
		s.attach(w, r, f.ID)
	case "connect":
		if r.Method != "GET" {
			http.NotFound(w, r)
			return
		}
		expiry, ok := s.account(w, r, f, false)
		if !ok {
			return
		}
		s.connect(w, r, f.ID, expiry, releaseAPI)
	default:
		http.NotFound(w, r)
	}
}
func (s *Server) enroll(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Fleet          string `json:"fleet"`
		Token          string `json:"token"`
		CredentialHash string `json:"credential_hash"`
	}
	if !body(w, r, &in) {
		return
	}
	if _, ok := s.fleet(in.Fleet); !ok || !validHash(in.Token) || !validHash(in.CredentialHash) {
		http.Error(w, "invalid enrollment", 403)
		return
	}
	e := s.state.update(func(db *database) error {
		p, ok := db.Enrollments[in.Fleet]
		if !ok || !time.Now().Before(p.Expires) || p.Hash != hash(in.Token) || db.Hosts[in.Fleet] != "" {
			return errors.New("expired or consumed")
		}
		delete(db.Enrollments, in.Fleet)
		db.Hosts[in.Fleet] = in.CredentialHash
		return nil
	})
	if e != nil {
		http.Error(w, "invalid or unavailable enrollment", 403)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) carrier(w http.ResponseWriter, r *http.Request, fleet string, releaseAPI func()) {
	credential := bearer(r)
	if !s.state.host(fleet, credential) {
		http.Error(w, "host credential required", 401)
		return
	}
	s.mu.Lock()
	if s.hosts[fleet] != nil {
		s.mu.Unlock()
		http.Error(w, "host carrier already connected", 409)
		return
	}
	// Serialize registration with revocation, then recheck the durable credential.
	if !s.state.host(fleet, credential) {
		s.mu.Unlock()
		http.Error(w, "host credential revoked", 401)
		return
	}
	c, e := websocket.Accept(w, r, nil)
	if e != nil {
		s.mu.Unlock()
		return
	}
	releaseAPI()
	c.SetReadLimit(1024)
	ctx, cancel := context.WithCancel(r.Context())
	h := &carrier{conn: c, ctx: ctx, cancel: cancel}
	s.hosts[fleet] = h
	s.mu.Unlock()
	defer func() {
		cancel()
		_ = c.CloseNow()
		s.mu.Lock()
		if s.hosts[fleet] == h {
			delete(s.hosts, fleet)
		}
		s.mu.Unlock()
	}()
	// Keep a read active for websocket pongs; host sends no application messages.
	done := c.CloseRead(ctx)
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-done.Done():
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			ping, cancel := context.WithTimeout(ctx, 5*time.Second)
			e = c.Ping(ping)
			cancel()
			if e != nil {
				return
			}
		}
	}
}
func (s *Server) attach(w http.ResponseWriter, r *http.Request, fleet string) {
	if !s.state.host(fleet, bearer(r)) {
		http.Error(w, "host credential required", 401)
		return
	}
	release, ok := s.acquireStream("attach:"+fleet, 32)
	if !ok {
		http.Error(w, "fleet attachment capacity unavailable", 503)
		return
	}
	defer release()
	ticket := r.Header.Get("X-Lamp-Ticket")
	s.mu.Lock()
	p := s.pending[ticket]
	if p == nil || p.fleet != fleet || s.hosts[fleet] != p.host {
		s.mu.Unlock()
		http.Error(w, "routing grant expired or consumed", 403)
		return
	}
	delete(s.pending, ticket)
	s.mu.Unlock()
	c, e := websocket.Accept(w, r, nil)
	if e != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(1 << 20)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(p.ctx, cancel)
	defer stop()
	conn := closingConn{websocket.NetConn(ctx, c, websocket.MessageBinary), cancel}
	select {
	case p.ready <- conn:
	case <-ctx.Done():
		return
	}
	<-ctx.Done()
}
func (s *Server) connect(w http.ResponseWriter, r *http.Request, fleet string, expiry time.Time, releaseAPI func()) {
	ctx, cancel := context.WithDeadline(r.Context(), expiry)
	defer cancel()
	host, e := s.open(ctx, fleet, "ssh")
	if e != nil {
		http.Error(w, "fleet connection unavailable", 503)
		return
	}
	defer host.Close()
	c, e := websocket.Accept(w, r, nil)
	if e != nil {
		return
	}
	defer c.CloseNow()
	releaseAPI()
	c.SetReadLimit(1 << 20)
	client := websocket.NetConn(ctx, c, websocket.MessageBinary)
	defer client.Close()
	splice(ctx, client, host, nil, 0)
}
func (s *Server) browserAccount(r *http.Request) (hostedlogin.Grant, bool) {
	if s.browserLogin == nil {
		return hostedlogin.Grant{}, false
	}
	return s.browserLogin.Authenticate(r)
}
func (s *Server) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if b := s.config.Browser; b != nil {
		for _, address := range []string{b.RawListen, b.PrivateListen} {
			if address == "" {
				continue
			}
			l, e := net.Listen("tcp", address)
			if e != nil {
				return e
			}
			defer l.Close()
			stop := context.AfterFunc(ctx, func() { _ = l.Close() })
			defer stop()
			go func() { _ = s.raw(ctx, l); cancel() }()
		}
	}
	server := &http.Server{TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13}, Addr: s.config.Listen, Handler: s, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16384, ErrorLog: log.New(io.Discard, "", 0)}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	var e error
	if s.config.PrivateDevelopment {
		e = server.ListenAndServe()
	} else {
		e = server.ListenAndServeTLS(s.config.Certificate, s.config.Key)
	}
	if e == http.ErrServerClosed {
		return nil
	}
	return e
}
