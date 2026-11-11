// SPDX-License-Identifier: AGPL-3.0-only
// Package relay is the optional Eilmer ciphertext forwarding service.
package relay

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/JohanSardar/lamp/internal/hostedlogin"
)

type Fleet struct {
	ID      string     `json:"id"`
	Owner   Identity   `json:"owner"`
	Members []Identity `json:"members"`
}
type Browser struct {
	Origin        string            `json:"origin"`
	AccountOrigin string            `json:"account_origin"`
	RawListen     string            `json:"raw_listen"`
	PrivateListen string            `json:"private_listen,omitempty"`
	AccountTarget string            `json:"account_target"`
	Routes        map[string]string `json:"routes"`
}
type Config struct {
	Listen      string   `json:"listen"`
	Hostname    string   `json:"hostname"`
	State       string   `json:"state"`
	Certificate string   `json:"certificate,omitempty"`
	Key         string   `json:"key,omitempty"`
	Issuer      string   `json:"issuer"`
	Audience    string   `json:"audience,omitempty"`
	Parties     []string `json:"authorized_parties"`
	Fleets      []Fleet  `json:"fleets"`
	Browser     *Browser `json:"browser,omitempty"`
	// PrivateDevelopment permits only numeric loopback listeners and a local test issuer.
	// It cannot be used to publish a fixture identity provider or an insecure listener.
	PrivateDevelopment bool `json:"private_development,omitempty"`
}

var fleetID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)

func Load(path string) (Config, error) {
	var c Config
	raw, e := PrivateRead(path)
	if e != nil {
		return c, e
	}
	if e = decode(raw, &c); e != nil {
		return c, e
	}
	return c, c.Validate()
}
func (c Config) Validate() error {
	if !filepath.IsAbs(c.State) || c.Hostname != "relay.eilmer.si" || len(c.Fleets) == 0 || len(c.Parties) == 0 {
		return errors.New("private absolute state path, relay.eilmer.si and enrolled fleets/parties required")
	}
	host, _, e := net.SplitHostPort(c.Listen)
	if e != nil {
		return e
	}
	issuer, e := url.Parse(c.Issuer)
	if e != nil || issuer.RawQuery != "" || issuer.Fragment != "" || issuer.User != nil || issuer.Path != "" {
		return errors.New("invalid issuer")
	}
	if c.PrivateDevelopment {
		if !loopback(host) || issuer.Scheme != "http" || !loopback(issuer.Hostname()) {
			return errors.New("private development requires loopback relay and issuer")
		}
	} else if c.Issuer != "https://clerk.eilmer.si" || c.Audience != "lamp:relay" || !filepath.IsAbs(c.Certificate) || !filepath.IsAbs(c.Key) {
		return errors.New("shared production Eilmer issuer, lamp:relay audience and separate TLS certificate/key required")
	}
	seen := map[string]bool{}
	for _, f := range c.Fleets {
		if !fleetID.MatchString(f.ID) || seen[f.ID] || f.Owner.Issuer != c.Issuer || f.Owner.Subject == "" {
			return errors.New("unique fleet IDs and immutable Eilmer owner identity required")
		}
		seen[f.ID] = true
		for _, m := range f.Members {
			if m.Issuer != c.Issuer || m.Subject == "" {
				return errors.New("members require Eilmer issuer and subject")
			}
		}
	}
	if b := c.Browser; b != nil {
		if c.PrivateDevelopment || !hostedlogin.ValidOrigin(b.Origin, c.Hostname) || !hostedlogin.ValidOrigin(b.AccountOrigin, "account.eilmer.si") {
			return errors.New("production account and relay browser origins required")
		}
		for _, addr := range []string{b.RawListen, b.AccountTarget, c.Listen} {
			h, _, e := net.SplitHostPort(addr)
			if e != nil || !loopback(h) {
				return errors.New("raw ingress and TLS service targets must be numeric loopback")
			}
		}
		if b.PrivateListen != "" {
			h, _, e := net.SplitHostPort(b.PrivateListen)
			ip := net.ParseIP(h)
			if e != nil || ip == nil || ip.To4() == nil || !ip.IsPrivate() {
				return errors.New("optional direct ingress must bind an explicit RFC1918 IPv4 address")
			}
		}
		for name, fleet := range b.Routes {
			prefix := strings.TrimSuffix(name, "."+fleet+".fleet.eilmer.si")
			if !seen[fleet] || prefix == name || !fleetID.MatchString(prefix) || strings.Contains(prefix, ".") {
				return errors.New("allocated one-label fleet ingress names required")
			}
		}
	}
	for _, p := range c.Parties {
		u, e := url.Parse(p)
		if e != nil || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("authorized parties must be exact origins")
		}
		if !c.PrivateDevelopment && (u.Scheme != "https" || u.Host != "accounts.eilmer.si") {
			return errors.New("only the existing shared account origin may mint relay sessions")
		}
	}
	return nil
}
func loopback(s string) bool { ip := net.ParseIP(s); return ip != nil && ip.IsLoopback() }
func PrivateRead(path string) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	a, e := f.Stat()
	if e != nil {
		return nil, e
	}
	b, e := os.Lstat(path)
	if e != nil || !a.Mode().IsRegular() || b.Mode()&os.ModeSymlink != 0 || a.Mode().Perm()&0077 != 0 || a.Size() > 1<<20 {
		return nil, errors.New("private regular file required")
	}
	return io.ReadAll(io.LimitReader(f, (1<<20)+1))
}
func decode(raw []byte, v any) error {
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return errors.New("expected one JSON value")
	}
	return nil
}
