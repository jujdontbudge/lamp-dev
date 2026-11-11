package access

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/JohanSardar/lamp/internal/accountidentity"
	"github.com/JohanSardar/lamp/internal/surface"
	"github.com/caddyserver/certmagic"
	"github.com/coder/websocket"
	ssh "github.com/gliderlabs/ssh"
	xssh "golang.org/x/crypto/ssh"
	"tailscale.com/client/local"
	"tailscale.com/tsnet"
)

type Service struct {
	config      Config
	ssh         *ssh.Server
	userCA      xssh.Signer
	connections chan struct{}
	oidc        *accountidentity.Verifier
}

func New(c Config) (*Service, error) {
	if e := c.Validate(); e != nil {
		return nil, e
	}
	if e := os.MkdirAll(c.StateDir, 0700); e != nil {
		return nil, e
	}
	i, e := os.Stat(c.StateDir)
	if e != nil || !i.IsDir() || i.Mode().Perm()&0077 != 0 {
		return nil, errors.New("access state directory must be private")
	}
	ca, e := loadSigner(c.UserCAFile)
	if e != nil {
		return nil, e
	}
	for _, u := range c.Users {
		if _, e = token(u.TokenFile); e != nil {
			return nil, e
		}
		for _, k := range u.AuthorizedKeys {
			if _, _, _, _, e = xssh.ParseAuthorizedKey([]byte(k)); e != nil {
				return nil, errors.New("invalid authorized key")
			}
		}
	}
	signer, e := hostSigner(c)
	if e != nil {
		return nil, e
	}
	s := &Service{config: c, userCA: ca, connections: make(chan struct{}, 64)}
	s.initOIDC()
	s.ssh = &ssh.Server{HostSigners: []ssh.Signer{signer}, Handler: s.console, PublicKeyHandler: s.authenticate, IdleTimeout: 10 * time.Minute, MaxTimeout: 4 * time.Hour,
		ChannelHandlers: map[string]ssh.ChannelHandler{"session": ssh.DefaultSessionHandler}, RequestHandlers: map[string]ssh.RequestHandler{},
		LocalPortForwardingCallback: func(ssh.Context, string, uint32) bool { return false }, ReversePortForwardingCallback: func(ssh.Context, string, uint32) bool { return false },
	}
	return s, nil
}
func (s *Service) user(name string) (User, bool) {
	for _, u := range s.config.Users {
		if u.Name == name {
			return u, true
		}
	}
	return User{}, false
}
func (s *Service) authenticate(ctx ssh.Context, key ssh.PublicKey) bool {
	u, ok := s.user(ctx.User())
	if !ok {
		return false
	}
	if cert, ok := key.(*xssh.Certificate); ok {
		if cert.CertType != xssh.UserCert {
			return false
		}
		checker := &xssh.CertChecker{IsUserAuthority: func(k xssh.PublicKey) bool { return ssh.KeysEqual(k, s.userCA.PublicKey()) }}
		return ssh.KeysEqual(cert.SignatureKey, s.userCA.PublicKey()) && checker.CheckCert(ctx.User(), cert) == nil
	}
	for _, raw := range u.AuthorizedKeys {
		k, _, options, _, e := xssh.ParseAuthorizedKey([]byte(raw))
		if e == nil && len(options) == 0 && ssh.KeysEqual(key, k) {
			return true
		}
	}
	return false
}
func (s *Service) handle(ctx context.Context, c net.Conn) {
	select {
	case s.connections <- struct{}{}:
	case <-ctx.Done():
		c.Close()
		return
	default:
		c.Close()
		return
	}
	defer func() { <-s.connections }()
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	s.ssh.HandleConn(c)
}
func (s *Service) serveSSH(ctx context.Context, l net.Listener) error {
	for {
		c, e := l.Accept()
		if e != nil {
			return e
		}
		go s.handle(ctx, c)
	}
}
func (s *Service) handler(lc *local.Client) http.Handler {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", s.config.APISocket)
	}}
	proxy := &httputil.ReverseProxy{Transport: transport, Rewrite: func(p *httputil.ProxyRequest) {
		p.SetURL(&url.URL{Scheme: "http", Host: "lamp"})
		p.Out.Host = "lamp"
		// Rewrite runs after hop-by-hop stripping. Preserve the credential checked
		// at the edge even if Connection nominated Authorization for removal;
		// an absent bearer would otherwise select the Unix socket's owner mode.
		p.Out.Header.Set("Authorization", p.In.Header.Get("Authorization"))
		for k := range p.Out.Header {
			if strings.HasPrefix(strings.ToLower(k), "x-lamp-") || strings.HasPrefix(strings.ToLower(k), "cf-") || strings.EqualFold(k, "Forwarded") {
				p.Out.Header.Del(k)
			}
		}
	}, ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) { http.Error(w, "control unavailable", 503) }}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == "/ssh" && r.Method == "GET" {
			// SSH authenticates inside the encrypted websocket; browser-origin requests are refused.
			if r.Header.Get("Origin") != "" {
				http.Error(w, "browser origins are not accepted", 403)
				return
			}
			c, e := websocket.Accept(w, r, nil)
			if e != nil {
				return
			}
			defer c.CloseNow()
			c.SetReadLimit(1 << 20)
			s.handle(r.Context(), websocket.NetConn(r.Context(), c, websocket.MessageBinary))
			return
		}
		if r.URL.Path == "/ssh/certificate" && r.Method == "POST" {
			if lc == nil {
				http.NotFound(w, r)
				return
			}
			s.certificate(w, r, lc)
			return
		}
		if _, _, ok := surface.Match(strings.ToLower(r.Method), r.URL.Path); !ok {
			http.NotFound(w, r)
			return
		}
		values := r.Header.Values("Authorization")
		if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") || strings.TrimSpace(strings.TrimPrefix(values[0], "Bearer ")) == "" {
			http.Error(w, "bearer required", 401)
			return
		}
		if s.oidc != nil && (r.Header.Get("Origin") != "" || r.Header.Get("Cookie") != "") {
			http.Error(w, "OIDC bearer API does not accept browser origins or cookies", 403)
			return
		}
		r, cancel, ok := s.withOIDC(w, r)
		defer cancel()
		if !ok {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
		proxy.ServeHTTP(w, r)
	})
}
func (s *Service) certificate(w http.ResponseWriter, r *http.Request, lc *local.Client) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	peer, e := lc.WhoIs(ctx, r.RemoteAddr)
	if e != nil || peer.Node == nil || peer.UserProfile == nil {
		http.Error(w, "tailnet identity unavailable", 403)
		return
	}
	var in struct {
		User      string `json:"user"`
		PublicKey string `json:"public_key"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || d.Decode(new(any)) != io.EOF {
		http.Error(w, "invalid certificate request", 400)
		return
	}
	u, ok := s.user(in.User)
	// Tagged nodes cannot inherit their owner's human login privileges.
	matched := ok && ((len(peer.Node.Tags) == 0 && u.TailnetLogin != "" && u.TailnetLogin == peer.UserProfile.LoginName) || (u.TailnetTag != "" && slices.Contains(peer.Node.Tags, u.TailnetTag)))
	if !matched {
		http.Error(w, "tailnet principal denied", 403)
		return
	}
	key, _, opts, rest, e := xssh.ParseAuthorizedKey([]byte(in.PublicKey))
	if e != nil || len(opts) > 0 || len(rest) > 0 {
		http.Error(w, "invalid public key", 400)
		return
	}
	if _, ok := key.(*xssh.Certificate); ok {
		http.Error(w, "expected plain public key", 400)
		return
	}
	cert, e := signedCertificate(key, s.userCA, xssh.UserCert, []string{u.Name}, 5*time.Minute)
	if e != nil {
		http.Error(w, "certificate unavailable", 503)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write(xssh.MarshalAuthorizedKey(cert))
}

// Run starts only explicitly configured routes. A local-only configuration never
// constructs a Tailscale client, ACME client or tunnel process.
func (s *Service) Run(ctx context.Context, ready func(string, string)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		tick := time.NewTicker(12 * time.Hour)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if signer, e := hostSigner(s.config); e == nil {
					s.ssh.AddHostKey(signer)
				} else {
					cancel()
					return
				}
			}
		}
	}()
	var wg sync.WaitGroup
	var closers []io.Closer
	errorsCh := make(chan error, 8)
	defer func() {
		cancel()
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i].Close()
		}
		s.ssh.Close()
		wg.Wait()
	}()
	start := func(name string, l net.Listener, handler http.Handler) {
		closers = append(closers, l)
		wg.Add(1)
		go func() {
			defer wg.Done()
			var e error
			if handler == nil {
				e = s.serveSSH(ctx, l)
			} else {
				server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
				defer server.Close()
				go func() { <-ctx.Done(); server.Close() }()
				e = server.Serve(l)
			}
			select {
			case errorsCh <- e:
			case <-ctx.Done():
			}
		}()
		ready(name, l.Addr().String())
	}
	if s.config.SSHListen != "" {
		l, e := net.Listen("tcp", s.config.SSHListen)
		if e != nil {
			return e
		}
		start("local-ssh", l, nil)
	}
	if s.config.HTTPListen != "" {
		l, e := net.Listen("tcp", s.config.HTTPListen)
		if e != nil {
			return e
		}
		start("local-http", l, s.handler(nil))
	}
	if t := s.config.Tailscale; t != nil {
		node, e := NewTailnet(filepath.Join(s.config.StateDir, "tailscale"), *t)
		if e != nil {
			return e
		}
		closers = append(closers, node)
		if _, e = node.Up(ctx); e != nil {
			return errors.New("tailscale enrollment failed; inspect private auth URL file")
		}
		lc, e := node.LocalClient()
		if e != nil {
			return e
		}
		l, e := node.Listen("tcp", fmt.Sprintf(":%d", t.SSHPort))
		if e != nil {
			return e
		}
		start("tailscale-ssh", l, nil)
		l, e = node.Listen("tcp", fmt.Sprintf(":%d", t.HTTPPort))
		if e != nil {
			return e
		}
		start("tailscale-http", l, s.handler(lc))
	}
	if d := s.config.Domain; d != nil {
		l, cleanup, e := s.domainListener(ctx, *d)
		if e != nil {
			return e
		}
		defer cleanup()
		start("domain-https", l, s.handler(nil))
	}
	var connectorDone <-chan struct{}
	if f := s.config.Cloudflare; f != nil {
		cleanup, done, e := s.startCloudflare(ctx, *f)
		if e != nil {
			return e
		}
		defer cleanup()
		connectorDone = done
		ready("cloudflare-connector-started", "https://"+f.Hostname)
	}
	select {
	case <-ctx.Done():
		return nil
	case <-connectorDone:
		return errors.New("cloudflare connector exited; inspect private connector log")
	case e := <-errorsCh:
		return e
	}
}
func NewTailnet(dir string, t Tailscale) (*tsnet.Server, error) {
	if !filepath.IsAbs(dir) || t.Hostname == "" || t.AuthKeyFile == "" && !filepath.IsAbs(t.AuthURLFile) {
		return nil, errors.New("private absolute client state and an enrollment key or auth URL path required")
	}
	key := ""
	var e error
	if t.AuthKeyFile != "" {
		key, e = token(t.AuthKeyFile)
		if e != nil {
			return nil, e
		}
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	info, e := os.Stat(dir)
	if e != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("tailnet state directory must be private")
	}
	return &tsnet.Server{Dir: dir, Hostname: t.Hostname, ControlURL: t.ControlURL, AuthKey: key, UserLogf: func(format string, args ...any) {
		if strings.Contains(format, "To start this tsnet") && len(args) > 0 {
			_ = writeAuthURL(t.AuthURLFile, []byte(fmt.Sprint(args[len(args)-1])+"\n"))
		}
	}, Logf: func(string, ...any) {}}, nil
}

func writeAuthURL(path string, data []byte) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".lamp-auth-url-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	_, e = f.Write(data)
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return os.Rename(f.Name(), path)
}
func (s *Service) domainListener(ctx context.Context, d Domain) (net.Listener, func(), error) {
	var magic *certmagic.Config
	cache := certmagic.NewCache(certmagic.CacheOptions{GetConfigForCert: func(certmagic.Certificate) (*certmagic.Config, error) { return magic, nil }})
	cleanup := func() { cache.Stop() }
	magic = certmagic.New(cache, certmagic.Config{Storage: &certmagic.FileStorage{Path: filepath.Join(s.config.StateDir, "certificates")}})
	ca := d.ACMECA
	if ca == "" {
		ca = certmagic.LetsEncryptProductionCA
	}
	issuer := certmagic.NewACMEIssuer(magic, certmagic.ACMEIssuer{CA: ca, Email: d.Email, Agreed: true, DisableHTTPChallenge: true})
	magic.Issuers = []certmagic.Issuer{issuer}
	l, e := net.Listen("tcp", d.Listen)
	if e != nil {
		cleanup()
		return nil, func() {}, e
	}
	// Start the TLS listener before issuance so TLS-ALPN challenges are reachable.
	// ManageAsync obtains and renews only the owner's configured name.
	if e = magic.ManageAsync(ctx, []string{d.Name}); e != nil {
		l.Close()
		cleanup()
		return nil, func() {}, e
	}
	return tls.NewListener(l, magic.TLSConfig()), cleanup, nil
}
