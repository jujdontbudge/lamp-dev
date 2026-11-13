package network

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"sync"
)

// HostService scopes an owner-enrolled loopback service to one exact TCP port.
// A read-only HTTP route rejects writes, CONNECT and upgrades before dialing.
// ask_first intentionally refuses autonomous access; the owner must change the
// connection to full or read_only before a guest request can reach the service.
type HostService struct {
	ID       Subject
	Endpoint netip.AddrPort
	Access   string
}

func (s *HostService) Subject() Subject { return s.ID }
func (s *HostService) DialGuest(ctx context.Context, protocol string, port uint16) (net.Conn, error) {
	if protocol != "tcp" || port != s.Endpoint.Port() || !s.Endpoint.Addr().IsLoopback() {
		return nil, ErrDenied
	}
	if s.Access == "off" || s.Access == "ask_first" {
		return nil, ErrDenied
	}
	if s.Access == "full" {
		return (&net.Dialer{}).DialContext(ctx, "tcp", s.Endpoint.String())
	}
	if s.Access != "read_only" {
		return nil, ErrDenied
	}
	client, server := net.Pipe()
	listener := &serviceListener{conn: server, done: make(chan struct{})}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, DialContext: func(c context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(c, "tcp", s.Endpoint.String())
	}}
	proxy := &httputil.ReverseProxy{Transport: transport, Rewrite: func(r *httputil.ProxyRequest) {
		r.SetURL(&url.URL{Scheme: "http", Host: s.Endpoint.String()})
		r.Out.Header.Del("Authorization")
		r.Out.Header.Del("Cookie")
	}}
	httpServer := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS") || r.Header.Get("Upgrade") != "" {
			http.Error(w, "read_only connection: writes and upgrades require owner permission", http.StatusForbidden)
			return
		}
		proxy.ServeHTTP(w, r)
	}), ReadHeaderTimeout: DialTimeout, MaxHeaderBytes: 16 << 10}
	go func() { defer server.Close(); defer transport.CloseIdleConnections(); _ = httpServer.Serve(listener) }()
	return client, nil
}

type serviceListener struct {
	conn net.Conn
	done chan struct{}
	once sync.Once
}
type serviceConn struct {
	net.Conn
	listener *serviceListener
}

func (c *serviceConn) Close() error { err := c.Conn.Close(); c.listener.Close(); return err }

func (l *serviceListener) Accept() (net.Conn, error) {
	if l.conn != nil {
		c := l.conn
		l.conn = nil
		return &serviceConn{c, l}, nil
	}
	<-l.done
	return nil, io.EOF
}
func (l *serviceListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *serviceListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }

// SelectProvider fences old connections before selecting the enrolled exit.
// Provider death never chooses direct networking as a fallback.
func (d *Director) SelectProvider(provider Provider) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		provider.Close()
		return ErrUnavailable
	}
	for f := range d.active {
		_ = f.Conn.Close()
		delete(d.active, f)
	}
	if err := d.record(Event{Kind: "exit_change", Decision: "allow", Reason: "owner_enrolled_exit"}); err != nil {
		provider.Close()
		return err
	}
	_ = d.provider.Close()
	d.provider = provider
	// Forget numeric DNS grants from the previous resolver context.
	for _, layers := range [][]Layer{d.policy.Ceiling, d.policy.Layers} {
		for i := range layers {
			for _, rules := range [][]Rule{layers[i].Allow, layers[i].Deny} {
				for j := range rules {
					rules[j].addresses = nil
				}
			}
		}
	}
	return nil
}
