package network

import (
	"context"
	"net"
	"net/netip"
	"strings"
)

// AttachedProvider keeps the independently selected internet exit separate
// from private membership. Tailnet dials always use the sandbox member, never
// the host's Tailscale daemon. Neither failure falls back to the other route.
type AttachedProvider struct {
	exit    Provider
	member  *Member
	domains []string
}

func AttachMember(exit Provider, member *Member, domains []string) (Provider, error) {
	if exit == nil || member == nil {
		return nil, ErrDenied
	}
	if scoped, ok := exit.(interface{ Subject() Subject }); ok && scoped.Subject() != member.subject {
		return nil, ErrDenied
	}
	copyDomains := make([]string, 0, len(domains))
	for _, domain := range domains {
		domain = strings.TrimSuffix(strings.ToLower(domain), ".")
		if domain == "" || strings.ContainsAny(domain, "/@:#?\\ \t\r\n") {
			return nil, ErrDenied
		}
		copyDomains = append(copyDomains, domain)
	}
	return &AttachedProvider{exit: exit, member: member, domains: copyDomains}, nil
}
func (p *AttachedProvider) Subject() Subject { return p.member.subject }
func (p *AttachedProvider) Resolve(ctx context.Context, name string) ([]netip.Addr, error) {
	if address, err := netip.ParseAddr(name); err == nil {
		return []netip.Addr{address.Unmap()}, nil
	}
	canonical := strings.TrimSuffix(strings.ToLower(name), ".")
	for _, domain := range p.domains {
		if canonical == domain || strings.HasSuffix(canonical, "."+domain) {
			return p.member.Resolve(ctx, name)
		}
	}
	return p.exit.Resolve(ctx, name)
}
func (p *AttachedProvider) NumericDial(ctx context.Context, d Destination) (net.Conn, error) {
	d = d.canonical()
	if contains(prefixes("100.64.0.0/10", "fd7a:115c:a1e0::/48"), d.Address) {
		return p.member.NumericDial(ctx, d)
	}
	return p.exit.NumericDial(ctx, d)
}
func (p *AttachedProvider) Close() error { _ = p.member.Close(); return p.exit.Close() }
