package access

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/crypto/ssh"
	_ "tailscale.com/feature/identityfederation" // enable tsnet's OIDC federation hook
	"tailscale.com/ipn/store/mem"
	"tailscale.com/tsnet"
)

func noRedirect(*http.Request, []*http.Request) error { return errors.New("redirect refused") }
func Proxy(ctx context.Context, address string, in io.Reader, out io.Writer) error {
	u, e := url.Parse(address)
	if e != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("expected SSH websocket URL without credentials")
	}
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else if u.Scheme == "http" && loopback(u.Host) {
		u.Scheme = "ws"
	} else {
		return errors.New("SSH websocket requires HTTPS (HTTP is permitted on numeric loopback)")
	}
	c, _, e := websocket.Dial(ctx, u.String(), &websocket.DialOptions{HTTPClient: &http.Client{CheckRedirect: noRedirect, Timeout: 15 * time.Second}})
	if e != nil {
		return errors.New("SSH websocket connection failed")
	}
	defer c.CloseNow()
	c.SetReadLimit(1 << 20)
	nc := websocket.NetConn(ctx, c, websocket.MessageBinary)
	defer nc.Close()
	return copyStream(nc, in, out)
}
func copyStream(c net.Conn, in io.Reader, out io.Writer) error {
	go func() {
		_, _ = io.Copy(c, in)
		if v, ok := c.(interface{ CloseWrite() error }); ok {
			_ = v.CloseWrite()
		}
	}()
	_, e := io.Copy(out, c)
	return e
}

// TailnetConnect is a ProxyCommand for clients without a system Tailscale daemon.
// Its state belongs solely to Lamp, including when using an owner's Headscale.
func TailnetConnect(ctx context.Context, dir string, t Tailscale, target string, in io.Reader, out io.Writer) error {
	node, e := NewTailnet(dir, t)
	if e != nil {
		return e
	}
	defer node.Close()
	if _, e = node.Up(ctx); e != nil {
		return errors.New("client tailnet enrollment failed")
	}
	c, e := node.Dial(ctx, "tcp", target)
	if e != nil {
		return e
	}
	defer c.Close()
	return copyStream(c, in, out)
}

type CIConfig struct {
	ClientID, Audience, Hostname, Target, HTTPAddress, User, HostCAFile string
	Tags                                                                []string
}

// RunCI obtains the runner's workload identity, joins an ephemeral userspace node,
// and mints a five-minute SSH certificate. No bearer, node state or SSH key is saved.
func RunCI(ctx context.Context, c CIConfig, command []string, in io.Reader, out, stderr io.Writer) error {
	if os.Getenv("TS_AUTHKEY") != "" || os.Getenv("TS_CLIENT_SECRET") != "" {
		return errors.New("CI federation requires TS_AUTHKEY and TS_CLIENT_SECRET to be unset")
	}
	if c.ClientID == "" || c.Audience == "" || len(c.Tags) == 0 {
		return errors.New("CI client ID, audience and tags required")
	}
	requestURL := os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL")
	requestToken := os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN")
	u, e := url.Parse(requestURL)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || requestToken == "" {
		return errors.New("GitHub Actions id-token: write permission required")
	}
	q := u.Query()
	q.Set("audience", c.Audience)
	u.RawQuery = q.Encode()
	r, e := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if e != nil {
		return e
	}
	r.Header.Set("Authorization", "Bearer "+requestToken)
	httpClient := &http.Client{Timeout: 15 * time.Second, CheckRedirect: noRedirect}
	resp, e := httpClient.Do(r)
	if e != nil {
		return errors.New("runner identity request failed")
	}
	var identity struct {
		Value string `json:"value"`
	}
	e = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&identity)
	resp.Body.Close()
	if e != nil || resp.StatusCode != 200 || identity.Value == "" {
		return errors.New("runner identity unavailable")
	}
	node := &tsnet.Server{Hostname: c.Hostname, Ephemeral: true, Store: new(mem.Store), ClientID: c.ClientID + "?ephemeral=true&preauthorized=true", IDToken: identity.Value, AdvertiseTags: c.Tags, UserLogf: func(string, ...any) {}, Logf: func(string, ...any) {}}
	defer node.Close()
	if e = node.Start(); e != nil {
		return errors.New("CI tailnet startup failed")
	}
	if _, e = node.Up(ctx); e != nil {
		return errors.New("CI federation enrollment failed")
	}
	return runTailnetSSH(ctx, node, c, command, in, out, stderr)
}

func runTailnetSSH(ctx context.Context, node *tsnet.Server, c CIConfig, command []string, in io.Reader, out, stderr io.Writer) error {
	_, private, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		return e
	}
	signer, e := ssh.NewSignerFromKey(private)
	if e != nil {
		return e
	}
	request, _ := json.Marshal(map[string]string{"user": c.User, "public_key": string(ssh.MarshalAuthorizedKey(signer.PublicKey()))})
	transport := &http.Transport{DialContext: node.Dial}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: noRedirect}
	r, e := http.NewRequestWithContext(ctx, "POST", "http://"+c.HTTPAddress+"/ssh/certificate", bytes.NewReader(request))
	if e != nil {
		return e
	}
	r.Header.Set("Content-Type", "application/json")
	resp, e := client.Do(r)
	if e != nil {
		return errors.New("tailnet certificate request failed")
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 8192))
	resp.Body.Close()
	if e != nil || resp.StatusCode != 200 {
		return errors.New("tailnet certificate denied")
	}
	key, _, _, _, e := ssh.ParseAuthorizedKey(raw)
	if e != nil {
		return e
	}
	cert, ok := key.(*ssh.Certificate)
	if !ok {
		return errors.New("invalid user certificate")
	}
	certSigner, e := ssh.NewCertSigner(cert, signer)
	if e != nil {
		return e
	}
	caRaw, e := os.ReadFile(c.HostCAFile)
	if e != nil {
		return e
	}
	ca, _, _, _, e := ssh.ParseAuthorizedKey(caRaw)
	if e != nil {
		return e
	}
	checker := &ssh.CertChecker{IsHostAuthority: func(k ssh.PublicKey, _ string) bool { return bytes.Equal(k.Marshal(), ca.Marshal()) }}
	nc, e := node.Dial(ctx, "tcp", c.Target)
	if e != nil {
		return e
	}
	defer nc.Close()
	conn, chans, reqs, e := ssh.NewClientConn(nc, c.Target, &ssh.ClientConfig{User: c.User, Auth: []ssh.AuthMethod{ssh.PublicKeys(certSigner)}, HostKeyCallback: checker.CheckHostKey, Timeout: 15 * time.Second})
	if e != nil {
		return e
	}
	sshClient := ssh.NewClient(conn, chans, reqs)
	defer sshClient.Close()
	session, e := sshClient.NewSession()
	if e != nil {
		return e
	}
	defer session.Close()
	session.Stdin = in
	session.Stdout = out
	session.Stderr = stderr
	if len(command) == 0 {
		command = []string{"sessions"}
	}
	for _, a := range command {
		if strings.ContainsAny(a, " \r\n\t\"'\\") {
			return errors.New("invalid console argument")
		}
	}
	done := make(chan error, 1)
	go func() { done <- session.Run(strings.Join(command, " ")) }()
	select {
	case e = <-done:
		return e
	case <-ctx.Done():
		return fmt.Errorf("CI console cancelled: %w", ctx.Err())
	}
}
