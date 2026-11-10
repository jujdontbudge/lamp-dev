package network

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/netip"
	"sync"
	"time"
)

// SandboxLink is host-created directional authority for one source generation,
// target engine handle and exact service. Neither common ownership nor a shared
// IP/host implies this grant. It is never inherited by a new Director/fork.
type SandboxLink struct {
	ID          string
	source      Subject
	target      GuestEndpoint
	targetID    Subject
	destination Destination
	until       time.Time
	ctx         context.Context
	cancel      context.CancelFunc
	life        providerLife
	once        sync.Once
	director    *Director
}

// AttachLink requires the existing parent/sandbox policy to admit the exact
// service as well. The connection lifts only the default private-address floor.
// Alias is an engine-private benchmark-range address, not a routed host address.
func (d *Director) AttachLink(target GuestEndpoint, alias netip.Addr, protocol string, port uint16, expires time.Time) (*SandboxLink, error) {
	if target == nil || target.Subject() == d.subject || !validSubject(target.Subject()) || !netip.MustParsePrefix("198.18.0.0/15").Contains(alias) || port == 0 || (protocol != "tcp" && protocol != "udp") || !expires.After(time.Now()) {
		return nil, ErrDenied
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.root.Err() != nil || d.policy.PublicPR || len(d.links) >= 64 {
		return nil, ErrDenied
	}
	destination := Destination{Address: alias, Protocol: protocol, Port: port}
	if _, exists := d.links[destination]; exists {
		return nil, ErrDenied
	}
	next := d.policy
	next.PrivateServices = append(append([]Destination(nil), next.PrivateServices...), destination)
	next, err := next.compile()
	if err != nil || !next.allows(destination) {
		return nil, ErrDenied
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return nil, ErrUnavailable
	}
	ctx, cancel := context.WithDeadline(d.root, expires)
	link := &SandboxLink{ID: "cn_" + hex.EncodeToString(random), source: d.subject, target: target, targetID: target.Subject(), destination: destination, until: expires, ctx: ctx, cancel: cancel, director: d}
	if err := d.record(Event{Kind: "sandbox_link_attach", Decision: "allow", Reason: "directional_exact_service", Destination: &destination, LinkID: link.ID, Target: &link.targetID, PolicySHA256: next.digest()}); err != nil {
		cancel()
		return nil, err
	}
	d.policy = next
	d.links[destination] = link
	context.AfterFunc(ctx, func() { _ = link.Close() })
	return link, nil
}
func (l *SandboxLink) dial(ctx context.Context, destination Destination) (net.Conn, error) {
	if destination != l.destination || l.target.Subject() != l.targetID || l.ctx.Err() != nil || !l.life.alive() {
		return nil, ErrDenied
	}
	child, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(l.ctx, cancel)
	defer stop()
	defer cancel()
	raw, err := l.target.DialGuest(child, destination.Protocol, destination.Port)
	if err != nil {
		return nil, ErrUnavailable
	}
	if l.ctx.Err() != nil {
		raw.Close()
		return nil, ErrDenied
	}
	return l.life.own(raw)
}
func (l *SandboxLink) Close() error {
	l.once.Do(func() {
		l.cancel()
		_ = l.life.close()
		d := l.director
		d.mu.Lock()
		defer d.mu.Unlock()
		// Retain a tombstone in d.links: detachment must never fall back to an exit
		// that happens to route this private alias. Existing admissions are fenced.
		for f := range d.active {
			if f.destination == l.destination {
				_ = f.Conn.Close()
				f.timer.Stop()
				delete(d.active, f)
			}
		}
		if !d.closed {
			_ = d.record(Event{Kind: "sandbox_link_revoke", Decision: "deny", Reason: "attachment_removed", Destination: &l.destination, LinkID: l.ID})
		}
	})
	return nil
}
