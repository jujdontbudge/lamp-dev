package network

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// VerifyExit observes the selected provider through a TLS-verified independent
// reflector and compares an address preverified outside Lamp. It never invents
// distinctness from labels, follows a redirect, or uses an ambient HTTP proxy.
func (d *Director) VerifyExit(ctx context.Context, reflector string, expected netip.Addr) (netip.Addr, error) {
	u, err := url.Parse(reflector)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !public(expected.Unmap()) {
		return netip.Addr{}, ErrDenied
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	authority := net.JoinHostPort(u.Hostname(), port)
	transport := &http.Transport{
		Proxy: nil, DisableKeepAlives: true, TLSHandshakeTimeout: DialTimeout, ResponseHeaderTimeout: DialTimeout, MaxResponseHeaderBytes: maxHead,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if network != "tcp" || !strings.EqualFold(address, authority) {
				return nil, ErrDenied
			}
			ap, err := netip.ParseAddrPort(net.JoinHostPort("0.0.0.0", port))
			if err != nil {
				return nil, ErrDenied
			}
			permits, err := d.Resolve(ctx, d.subject, u.Hostname(), "tcp", ap.Port())
			if err != nil {
				return nil, err
			}
			for _, permit := range permits {
				if permit.Destination().Address.Is4() == expected.Unmap().Is4() {
					return d.Dial(ctx, permit)
				}
			}
			return nil, ErrUnavailable
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return netip.Addr{}, ErrDenied
	}
	response, err := client.Do(request)
	if err != nil {
		return netip.Addr{}, ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return netip.Addr{}, ErrUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 129))
	if err != nil || len(body) > 128 {
		return netip.Addr{}, ErrUnavailable
	}
	observed, err := netip.ParseAddr(strings.TrimSpace(string(body)))
	if err != nil || !public(observed.Unmap()) {
		return netip.Addr{}, ErrUnavailable
	}
	observed = observed.Unmap()
	if observed != expected.Unmap() {
		return observed, ErrUnavailable
	}
	return observed, nil
}
