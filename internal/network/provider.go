package network

import (
	"context"
	"net"
	"net/netip"
	"sync"
)

type Resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

// providerLife makes provider loss close existing sockets as well as refuse
// new dials. There is no fallback provider field or ambient retry path.
type providerLife struct {
	mu     sync.Mutex
	closed bool
	conns  map[*providerConn]struct{}
}

func (l *providerLife) alive() bool { l.mu.Lock(); defer l.mu.Unlock(); return !l.closed }
func (l *providerLife) own(raw net.Conn) (net.Conn, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		raw.Close()
		return nil, ErrUnavailable
	}
	if l.conns == nil {
		l.conns = map[*providerConn]struct{}{}
	}
	c := &providerConn{Conn: raw, life: l}
	l.conns[c] = struct{}{}
	return c, nil
}
func (l *providerLife) close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	for c := range l.conns {
		_ = c.Conn.Close()
		delete(l.conns, c)
	}
	return nil
}

type providerConn struct {
	net.Conn
	life *providerLife
}

func (c *providerConn) Close() error {
	err := c.Conn.Close()
	c.life.mu.Lock()
	delete(c.life.conns, c)
	c.life.mu.Unlock()
	return err
}
func (c *providerConn) CloseWrite() error {
	if conn, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return conn.CloseWrite()
	}
	return nil
}

// DirectProvider uses the host's selected DNS context and always sends a
// numeric address to net.Dialer. The Director supplies policy/audit authority.
type DirectProvider struct {
	life     providerLife
	resolver Resolver
	dialer   net.Dialer
}

func NewDirectProvider(resolver Resolver) *DirectProvider {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	return &DirectProvider{resolver: resolver}
}
func (p *DirectProvider) Resolve(ctx context.Context, name string) ([]netip.Addr, error) {
	if !p.life.alive() {
		return nil, ErrUnavailable
	}
	if ip, err := netip.ParseAddr(name); err == nil {
		return []netip.Addr{ip.Unmap()}, nil
	}
	return p.resolver.LookupNetIP(ctx, "ip", name)
}
func (p *DirectProvider) NumericDial(ctx context.Context, d Destination) (net.Conn, error) {
	d = d.canonical()
	if !d.valid() {
		return nil, ErrDenied
	}
	if !p.life.alive() {
		return nil, ErrUnavailable
	}
	raw, err := p.dialer.DialContext(ctx, d.Protocol, d.AddrPort().String())
	if err != nil {
		return nil, ErrUnavailable
	}
	return p.life.own(raw)
}
func (p *DirectProvider) Close() error { return p.life.close() }
