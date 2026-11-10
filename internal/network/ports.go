package network

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// IngressAuth authenticates the actual transport, never guest headers. For a
// private member listener use Member.WhoIs("tcp", request.RemoteAddr).
type IngressAuth func(context.Context, *http.Request) (PeerIdentity, error)
type PublishedPort struct {
	ID      string  `json:"id"`
	Subject Subject `json:"subject"`
	Port    uint16  `json:"port"`
	Origin  string  `json:"origin"`
}
type ShareLink struct {
	GrantID         string    `json:"grant_id"`
	URL             string    `json:"url,omitempty"`
	ExpiresAt       time.Time `json:"expires_at"`
	SecretAvailable bool      `json:"secret_available"`
}
type portEntry struct {
	PublishedPort
	endpoint    GuestEndpoint
	ctx         context.Context
	cancel      context.CancelFunc
	certificate *tls.Certificate
	active      bool
	connections map[*portConn]struct{}
}
type shareEntry struct {
	id, port, recipient string
	expires             time.Time
	hash                [32]byte
	used, revoked       bool
	ctx                 context.Context
	cancel              context.CancelFunc
}
type portSession struct {
	port, grant string
	expires     time.Time
}
type shareReplay struct {
	port, recipient, grant string
	expires                time.Time
}
type PortManager struct {
	mu         sync.Mutex
	authority  *Authority
	domain     string
	listenPort uint16
	auth       IngressAuth
	audit      Audit
	viewers    map[string]bool
	ports      map[string]*portEntry
	ids        map[string]*portEntry
	grants     map[string]*shareEntry
	tokens     map[[32]byte]*shareEntry
	sessions   map[[32]byte]portSession
	replays    map[string]shareReplay
	used       map[string]bool
	ledger     *os.File
}

func NewPortManager(directory, domain string, listenPort uint16, authority *Authority, auth IngressAuth, audit Audit, privateViewers []string) (*PortManager, error) {
	if authority == nil || auth == nil || audit == nil || listenPort == 0 || len(domain) == 0 || len(domain) > 200 || strings.ContainsAny(domain, "/:@?#\\ \t\r\n") || domain != strings.ToLower(domain) {
		return nil, ErrDenied
	}
	for _, label := range strings.Split(domain, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return nil, ErrDenied
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return nil, ErrDenied
			}
		}
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, ErrDenied
	}
	path := filepath.Join(directory, "used-origins.ndjson")
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 16<<20 {
			return nil, ErrDenied
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	ledger, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	dir, err := os.Open(directory)
	if err != nil {
		ledger.Close()
		return nil, err
	}
	syncErr := dir.Sync()
	dir.Close()
	if syncErr != nil {
		ledger.Close()
		return nil, syncErr
	}
	manager := &PortManager{authority: authority, domain: domain, listenPort: listenPort, auth: auth, audit: audit, viewers: map[string]bool{}, ports: map[string]*portEntry{}, ids: map[string]*portEntry{}, grants: map[string]*shareEntry{}, tokens: map[[32]byte]*shareEntry{}, sessions: map[[32]byte]portSession{}, replays: map[string]shareReplay{}, used: map[string]bool{}, ledger: ledger}
	decoder := json.NewDecoder(ledger)
	for {
		var origin string
		err := decoder.Decode(&origin)
		if err == io.EOF {
			break
		}
		if err != nil {
			ledger.Close()
			return nil, ErrDenied
		}
		manager.used[origin] = true
	}
	for _, viewer := range privateViewers {
		if viewer == "" {
			ledger.Close()
			return nil, ErrDenied
		}
		manager.viewers[viewer] = true
	}
	return manager, nil
}
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func (m *PortManager) Publish(endpoint GuestEndpoint, port uint16) (PublishedPort, error) {
	if endpoint == nil || !validSubject(endpoint.Subject()) || port == 0 {
		return PublishedPort{}, ErrDenied
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.ids) >= 256 {
		return PublishedPort{}, ErrUnavailable
	}
	token, err := randomToken()
	if err != nil {
		return PublishedPort{}, err
	}
	sum := sha256.Sum256([]byte(token))
	suffix := hex.EncodeToString(sum[:16])
	id := "po_" + suffix
	host := "p-" + suffix + "." + m.domain
	authority := host
	if m.listenPort != 443 {
		authority = net.JoinHostPort(host, fmt.Sprint(m.listenPort))
	}
	origin := "https://" + authority
	if m.used[origin] {
		return PublishedPort{}, ErrUnavailable
	}
	cert, err := m.authority.certificateFor(host)
	if err != nil {
		return PublishedPort{}, err
	}
	if err := json.NewEncoder(m.ledger).Encode(origin); err != nil {
		return PublishedPort{}, err
	}
	if m.ledger.Sync() != nil {
		return PublishedPort{}, ErrUnavailable
	}
	m.used[origin] = true
	ctx, cancel := context.WithCancel(context.Background())
	p := &portEntry{PublishedPort: PublishedPort{ID: id, Subject: endpoint.Subject(), Port: port, Origin: origin}, endpoint: endpoint, ctx: ctx, cancel: cancel, certificate: cert, active: true, connections: map[*portConn]struct{}{}}
	m.ports[authority] = p
	m.ids[id] = p
	return p.PublishedPort, nil
}
func (m *PortManager) TLSConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"h2", "http/1.1"}, GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		m.mu.Lock()
		defer m.mu.Unlock()
		for _, p := range m.ids {
			u, _ := url.Parse(p.Origin)
			if p.active && u.Hostname() == hello.ServerName {
				// Refresh a live publication before its leaf expires, without
				// changing the origin, CA trust, grants or active connections.
				leaf := p.certificate.Leaf
				now := time.Now()
				if leaf == nil || !now.Before(leaf.NotAfter) ||
					(!now.Before(leaf.NotAfter.Add(-time.Hour)) && leaf.NotAfter.Before(m.authority.certificate.NotAfter)) {
					certificate, err := m.authority.certificateFor(u.Hostname())
					if err != nil {
						return nil, err
					}
					p.certificate = certificate
				}
				return p.certificate, nil
			}
		}
		return nil, ErrDenied
	}}
}

// Share returns a bearer URL exactly once. Only its hash and nonsecret grant
// metadata remain in memory. Replaying a key returns the same grant, no URL.
func (m *PortManager) Share(portID, idempotencyKey, recipient string, expires time.Time) (ShareLink, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.ids[portID]
	if p == nil || !p.active || idempotencyKey == "" || len(idempotencyKey) > 128 {
		return ShareLink{}, ErrDenied
	}
	defaultExpiry := expires.IsZero()
	if old, ok := m.replays[idempotencyKey]; ok {
		if defaultExpiry {
			expires = old.expires
		}
		if old.port != portID || old.recipient != recipient || !old.expires.Equal(expires) {
			return ShareLink{}, ErrRevision
		}
		return ShareLink{GrantID: old.grant, ExpiresAt: old.expires, SecretAvailable: false}, nil
	}
	if defaultExpiry {
		expires = time.Now().Add(time.Hour)
	}
	if len(m.grants) >= 4096 || len(m.replays) >= 4096 {
		return ShareLink{}, ErrUnavailable
	}
	if !expires.After(time.Now()) {
		return ShareLink{}, ErrDenied
	}
	token, err := randomToken()
	if err != nil {
		return ShareLink{}, err
	}
	id, err := randomToken()
	if err != nil {
		return ShareLink{}, err
	}
	grantID := "pg_" + id[:22]
	ctx, cancel := context.WithDeadline(p.ctx, expires)
	g := &shareEntry{id: grantID, port: portID, recipient: recipient, expires: expires, hash: sha256.Sum256([]byte(token)), ctx: ctx, cancel: cancel}
	m.grants[grantID] = g
	m.tokens[g.hash] = g
	m.replays[idempotencyKey] = shareReplay{portID, recipient, grantID, expires}
	context.AfterFunc(ctx, func() { _ = m.RevokeShare(grantID) })
	return ShareLink{GrantID: grantID, URL: p.Origin + "/_lamp/#" + token, ExpiresAt: expires, SecretAvailable: true}, nil
}
func (m *PortManager) RevokeShare(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	g := m.grants[id]
	if g == nil {
		return ErrDenied
	}
	if g.revoked {
		return nil
	}
	g.revoked = true
	g.cancel()
	delete(m.tokens, g.hash)
	if p := m.ids[g.port]; p != nil {
		for c := range p.connections {
			if c.grant == id {
				_ = c.Conn.Close()
				delete(p.connections, c)
			}
		}
	}
	return nil
}
func (m *PortManager) Unpublish(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.ids[id]
	if p == nil {
		return ErrDenied
	}
	p.active = false
	p.cancel()
	for c := range p.connections {
		_ = c.Conn.Close()
		delete(p.connections, c)
	}
	return nil
}
func (m *PortManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.ids {
		p.active = false
		p.cancel()
		for c := range p.connections {
			_ = c.Conn.Close()
		}
	}
	return m.ledger.Close()
}
func (m *PortManager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Origin-Agent-Cluster", "?1")
	m.mu.Lock()
	p := m.ports[r.Host]
	valid := p != nil && p.active && p.endpoint.Subject() == p.Subject
	if valid {
		u, _ := url.Parse(p.Origin)
		valid = r.TLS != nil && r.TLS.ServerName == u.Hostname()
	}
	m.mu.Unlock()
	if !valid {
		http.NotFound(w, r)
		return
	}
	if r.URL.Path == "/_lamp/" {
		nonce, err := randomToken()
		if err != nil {
			http.Error(w, "unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+nonce+"'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'")
		_, _ = io.WriteString(w, `<!doctype html><meta name="referrer" content="no-referrer"><script nonce="`+nonce+`">const token=location.hash.slice(1);history.replaceState(null,'','/_lamp/');fetch('/_lamp/exchange',{method:'POST',credentials:'same-origin',referrerPolicy:'no-referrer',headers:{'Content-Type':'application/json'},body:JSON.stringify({token})}).then(r=>{if(r.ok)location.replace('/');else document.body.textContent='Link unavailable'});</script>`)
		return
	}
	if r.URL.Path == "/_lamp/exchange" {
		m.exchange(w, r, p)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/_lamp") {
		http.NotFound(w, r)
		return
	}
	peer, authErr := m.auth(r.Context(), r)
	grant := ""
	requestCtx := p.ctx
	m.mu.Lock()
	allowed := authErr == nil && m.viewers[peer.NodeID]
	if !allowed {
		if cookie, err := r.Cookie("__Host-lamp"); err == nil {
			if session, ok := m.sessions[sha256.Sum256([]byte(cookie.Value))]; ok && session.port == p.ID && time.Now().Before(session.expires) {
				g := m.grants[session.grant]
				if g != nil && !g.revoked && g.ctx.Err() == nil && (g.recipient == "" || (authErr == nil && g.recipient == peer.NodeID)) {
					allowed = true
					grant = g.id
					requestCtx = g.ctx
				}
			}
		}
	}
	allowed = allowed && p.active
	m.mu.Unlock()
	if !allowed {
		http.Error(w, "sign-in or a current share is required", http.StatusForbidden)
		return
	}
	reason := "signed_in"
	if grant != "" {
		reason = "share"
	}
	if m.audit.Record(Event{Kind: "port_ingress", Subject: p.Subject, Revision: 1, Decision: "allow", Reason: reason, PortID: p.ID, GrantID: grant}) != nil {
		_ = m.Unpublish(p.ID)
		http.Error(w, "audit unavailable", 503)
		return
	}
	ctx, cancel := context.WithCancel(requestCtx)
	defer cancel()
	stop := context.AfterFunc(r.Context(), cancel)
	defer stop()
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, MaxResponseHeaderBytes: 16 << 10, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		connect, done := context.WithTimeout(ctx, DialTimeout)
		defer done()
		raw, err := p.endpoint.DialGuest(connect, "tcp", p.Port)
		if err != nil {
			return nil, ErrUnavailable
		}
		c := &portConn{Conn: raw, manager: m, port: p, grant: grant}
		m.mu.Lock()
		valid := p.active && p.endpoint.Subject() == p.Subject
		if grant != "" {
			g := m.grants[grant]
			valid = valid && g != nil && !g.revoked && g.ctx.Err() == nil
		}
		if valid {
			p.connections[c] = struct{}{}
		}
		m.mu.Unlock()
		if !valid {
			raw.Close()
			return nil, ErrDenied
		}
		return c, nil
	}}
	defer transport.CloseIdleConnections()
	proxy := &httputil.ReverseProxy{Transport: transport, Rewrite: func(request *httputil.ProxyRequest) {
		request.SetURL(&url.URL{Scheme: "http", Host: "app.lamp.invalid"})
		request.Out.Host = "app.lamp.invalid"
		for name := range request.Out.Header {
			lower := strings.ToLower(name)
			if lower == "cookie" || lower == "authorization" || lower == "proxy-authorization" || lower == "referer" || lower == "forwarded" || strings.HasPrefix(lower, "x-forwarded-") || strings.HasPrefix(lower, "x-lamp-") || strings.HasPrefix(lower, "x-authenticated-") {
				request.Out.Header.Del(name)
			}
		}
	}, ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "app unavailable", http.StatusBadGateway)
	}, ModifyResponse: func(response *http.Response) error {
		// An app cannot replace the host's authority cookie or set parent-domain
		// cookies. App authentication is intentionally not forwarded by this route.
		response.Header.Del("Set-Cookie")
		response.Header.Del("Server")
		return nil
	}}
	proxy.ServeHTTP(w, r.WithContext(ctx))
}
func (m *PortManager) exchange(w http.ResponseWriter, r *http.Request, p *portEntry) {
	if r.Method != "POST" || r.URL.RawQuery != "" || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != p.Origin) {
		http.Error(w, "invalid exchange", 400)
		return
	}
	var input struct {
		Token string `json:"token"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || len(input.Token) > 128 {
		http.Error(w, "invalid exchange", 400)
		return
	}
	peer, authErr := m.auth(r.Context(), r)
	m.mu.Lock()
	defer m.mu.Unlock()
	for hash, session := range m.sessions {
		if !time.Now().Before(session.expires) {
			delete(m.sessions, hash)
		}
	}
	if len(m.sessions) >= 4096 {
		http.Error(w, "unavailable", 503)
		return
	}
	g := m.tokens[sha256.Sum256([]byte(input.Token))]
	if g == nil || g.port != p.ID || g.used || g.revoked || !p.active || g.ctx.Err() != nil || (g.recipient != "" && (authErr != nil || peer.NodeID != g.recipient)) {
		http.Error(w, "link unavailable", 403)
		return
	}
	session, err := randomToken()
	if err != nil {
		http.Error(w, "unavailable", 503)
		return
	}
	g.used = true
	delete(m.tokens, g.hash)
	m.sessions[sha256.Sum256([]byte(session))] = portSession{p.ID, g.id, g.expires}
	http.SetCookie(w, &http.Cookie{Name: "__Host-lamp", Value: session, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, Expires: g.expires})
	w.WriteHeader(http.StatusNoContent)
}

type portConn struct {
	net.Conn
	manager *PortManager
	port    *portEntry
	grant   string
	once    sync.Once
	err     error
}

func (c *portConn) Close() error {
	c.once.Do(func() {
		c.err = c.Conn.Close()
		c.manager.mu.Lock()
		delete(c.port.connections, c)
		c.manager.mu.Unlock()
	})
	return c.err
}
