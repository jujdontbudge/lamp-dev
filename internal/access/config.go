// Package access exposes the public Lamp API and console without exposing a host shell.
package access

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	"lamp.eilmer.si/internal/accountidentity"
)

type User struct {
	Name           string                    `json:"name"`
	AuthorizedKeys []string                  `json:"authorized_keys,omitempty"`
	TokenFile      string                    `json:"token_file"`
	Identity       *accountidentity.Identity `json:"identity,omitempty"`
	// Tailnet identities are matched to WhoIs, never HTTP headers or SSH usernames.
	TailnetLogin string `json:"tailnet_login,omitempty"`
	TailnetTag   string `json:"tailnet_tag,omitempty"`
}
type Tailscale struct {
	Hostname    string `json:"hostname"`
	ControlURL  string `json:"control_url,omitempty"`
	AuthKeyFile string `json:"auth_key_file,omitempty"`
	AuthURLFile string `json:"auth_url_file"`
	SSHPort     uint16 `json:"ssh_port"`
	HTTPPort    uint16 `json:"http_port"`
}
type Domain struct {
	Name   string `json:"name"`
	Email  string `json:"email"`
	Listen string `json:"listen"`
	ACMECA string `json:"acme_ca,omitempty"`
}
type Cloudflare struct {
	Executable           string `json:"executable"`
	ExecutableSHA256     string `json:"executable_sha256"`
	TunnelID             string `json:"tunnel_id"`
	CredentialsFile      string `json:"credentials_file"`
	Hostname             string `json:"hostname"`
	Plugin               string `json:"plugin"`
	Manifest             string `json:"manifest"`
	ManifestSHA256       string `json:"manifest_sha256"`
	Launcher             string `json:"launcher"`
	LauncherSHA256       string `json:"launcher_sha256"`
	DiagnosticSoftLimits bool   `json:"diagnostic_soft_limits,omitempty"`
}

// OIDC is optional and owner-operated. It is never constructed for local-only
// fleets and is not accepted as a substitute for the shared Eilmer relay account.
type OIDC struct {
	Issuer   string   `json:"issuer"`
	Audience string   `json:"audience"`
	JWKSURL  string   `json:"jwks_url"`
	Parties  []string `json:"authorized_parties"`
}
type Config struct {
	StateDir   string      `json:"state_dir"`
	APISocket  string      `json:"api_socket"`
	SSHListen  string      `json:"ssh_listen,omitempty"`
	HTTPListen string      `json:"http_listen,omitempty"`
	HostCAFile string      `json:"host_ca_file"`
	UserCAFile string      `json:"user_ca_file"`
	Hostnames  []string    `json:"hostnames"`
	Users      []User      `json:"users"`
	Tailscale  *Tailscale  `json:"tailscale,omitempty"`
	Domain     *Domain     `json:"domain,omitempty"`
	Cloudflare *Cloudflare `json:"cloudflare,omitempty"`
	OIDC       *OIDC       `json:"oidc,omitempty"`
}

func Load(path string) (Config, error) {
	var c Config
	b, e := PrivateRead(path)
	if e != nil {
		return c, e
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if e = d.Decode(&c); e != nil {
		return c, e
	}
	if d.Decode(new(any)) != io.EOF {
		return c, errors.New("expected one configuration")
	}
	return c, c.Validate()
}
func (c Config) Validate() error {
	for _, p := range []string{c.StateDir, c.APISocket, c.HostCAFile, c.UserCAFile} {
		if !filepath.IsAbs(p) {
			return errors.New("absolute state, socket and CA paths required")
		}
	}
	if len(c.Hostnames) == 0 || len(c.Users) == 0 {
		return errors.New("hostnames and users required")
	}
	if c.SSHListen == "" && c.HTTPListen == "" && c.Tailscale == nil && c.Domain == nil {
		return errors.New("at least one access listener required")
	}
	if c.SSHListen != "" {
		host, _, e := net.SplitHostPort(c.SSHListen)
		if e != nil || net.ParseIP(host) == nil {
			return errors.New("SSH listener requires a numeric address")
		}
	}
	if c.HTTPListen != "" && !loopback(c.HTTPListen) {
		return errors.New("plain HTTP origin must use numeric loopback; use domain HTTPS for remote API access")
	}
	if c.OIDC != nil {
		if e := validateOIDC(*c.OIDC); e != nil {
			return e
		}
	}
	seen := map[string]bool{}
	identities := map[accountidentity.Identity]bool{}
	for _, u := range c.Users {
		if u.Name == "" || strings.ContainsAny(u.Name, " \t\n/:") || seen[u.Name] || !filepath.IsAbs(u.TokenFile) {
			return errors.New("unique SSH names and absolute principal token files required")
		}
		seen[u.Name] = true
		if u.Identity != nil {
			if c.OIDC == nil || u.Identity.Issuer != c.OIDC.Issuer || u.Identity.Subject == "" || identities[*u.Identity] {
				return errors.New("unique immutable OIDC issuer/subject mappings required")
			}
			identities[*u.Identity] = true
		}
		if u.TailnetLogin != "" && u.TailnetTag != "" {
			return errors.New("choose a tailnet login or tag per user")
		}
	}
	if t := c.Tailscale; t != nil {
		if t.Hostname == "" || !filepath.IsAbs(t.AuthURLFile) || t.SSHPort == 0 || t.HTTPPort == 0 || t.SSHPort == t.HTTPPort {
			return errors.New("tailscale hostname, private auth URL file and distinct nonzero ports required")
		}
	}
	if d := c.Domain; d != nil {
		if !validHostname(d.Name) || d.Email == "" || d.Listen == "" {
			return errors.New("domain hostname, ACME email and listener required")
		}
	}
	if f := c.Cloudflare; f != nil {
		if c.HTTPListen == "" || !validHostname(f.Hostname) || f.TunnelID == "" || !filepath.IsAbs(f.CredentialsFile) || !filepath.IsAbs(f.Executable) {
			return errors.New("cloudflare requires a local HTTP origin, hostname, tunnel ID and absolute paths")
		}
	}
	return nil
}
func validHostname(s string) bool {
	return strings.Contains(s, ".") && !strings.ContainsAny(s, " /\\:*?@\r\n") && s == strings.ToLower(s)
}
func loopback(addr string) bool {
	h, _, e := net.SplitHostPort(addr)
	ip := net.ParseIP(h)
	return e == nil && ip != nil && ip.IsLoopback()
}

// PrivateRead deliberately refuses symlinks and group/world readable credentials.
func PrivateRead(path string) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	i, e := f.Stat()
	if e != nil {
		return nil, e
	}
	l, e := os.Lstat(path)
	if e != nil || !i.Mode().IsRegular() || l.Mode()&os.ModeSymlink != 0 || i.Mode().Perm()&0077 != 0 || i.Size() > 1<<20 {
		return nil, fmt.Errorf("private regular file required: %s", path)
	}
	return io.ReadAll(io.LimitReader(f, 1<<20))
}
func token(path string) (string, error) {
	b, e := PrivateRead(path)
	s := strings.TrimSpace(string(b))
	if e == nil && (s == "" || strings.ContainsAny(s, " \r\n\t")) {
		e = errors.New("invalid principal token file")
	}
	return s, e
}
