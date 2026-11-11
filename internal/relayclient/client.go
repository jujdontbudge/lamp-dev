// SPDX-License-Identifier: Apache-2.0
// Package relayclient carries host-authenticated SSH through an untrusted relay.
// It deliberately does not import the AGPL relay server.
package relayclient

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

type Credential struct {
	Fleet  string `json:"fleet"`
	Secret string `json:"secret"`
}

func privateRead(path string) ([]byte, error) {
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
	if e != nil || !a.Mode().IsRegular() || b.Mode()&os.ModeSymlink != 0 || a.Mode().Perm()&0077 != 0 || a.Size() > 16384 {
		return nil, errors.New("private regular credential file required")
	}
	return io.ReadAll(io.LimitReader(f, 16384))
}
func endpoint(base, path string, ws bool) (string, error) {
	u, e := url.Parse(base)
	if e != nil {
		return "", e
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Host == "" {
		return "", errors.New("bare relay origin required; no URL credentials")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && ip != nil && ip.IsLoopback()) {
		return "", errors.New("HTTPS relay required except numeric loopback development")
	}
	u.Path = path
	if ws {
		if u.Scheme == "https" {
			u.Scheme = "wss"
		} else {
			u.Scheme = "ws"
		}
	}
	return u.String(), nil
}
func request(ctx context.Context, base, path, token string, in any) (*http.Response, error) {
	address, e := endpoint(base, path, false)
	if e != nil {
		return nil, e
	}
	raw, e := json.Marshal(in)
	if e != nil {
		return nil, e
	}
	req, e := http.NewRequestWithContext(ctx, "POST", address, bytes.NewReader(raw))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return client.Do(req)
}
func accountToken(path string) (string, error) {
	b, e := privateRead(path)
	if e != nil {
		return "", e
	}
	s := strings.TrimSpace(string(b))
	if s == "" || strings.ContainsAny(s, " \r\n\t") {
		return "", errors.New("invalid account token file")
	}
	return s, nil
}
func validFleet(f string) bool {
	if len(f) == 0 || len(f) > 48 {
		return false
	}
	for i, c := range f {
		if (c < 'a' || c > 'z') && (i == 0 || c < '0' || c > '9') && (i == 0 || c != '-') {
			return false
		}
	}
	return true
}

// IssueEnrollment writes the one-time token to a private file, never stdout or argv.
func IssueEnrollment(ctx context.Context, base, fleet, accountFile, out string) error {
	if !validFleet(fleet) {
		return errors.New("invalid fleet")
	}
	token, e := accountToken(accountFile)
	if e != nil {
		return e
	}
	resp, e := request(ctx, base, "/v1/enrollments/"+fleet, token, nil)
	if e != nil {
		return errors.New("enrollment request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.New("enrollment denied or unavailable")
	}
	var result struct {
		Token string `json:"token"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&result) != nil || len(result.Token) != 64 {
		return errors.New("invalid enrollment response")
	}
	f, e := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	_, e = f.WriteString(result.Token + "\n")
	if e == nil {
		e = f.Sync()
	}
	return e
}

// Enroll creates the host reconnect secret locally and sends only its hash.
// The output is retained even after an ambiguous network failure: the token
// may already have been consumed, and this is then the only reconnect secret.
func Enroll(ctx context.Context, base, fleet, enrollmentFile, out string) error {
	if !validFleet(fleet) {
		return errors.New("invalid fleet")
	}
	token, e := accountToken(enrollmentFile)
	if e != nil {
		return e
	}
	var key [32]byte
	if _, e = rand.Read(key[:]); e != nil {
		return e
	}
	credential := hex.EncodeToString(key[:])
	raw, _ := json.Marshal(Credential{fleet, credential})
	f, e := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	if _, e = f.Write(append(raw, '\n')); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	sum := sha256.Sum256([]byte(credential))
	resp, e := request(ctx, base, "/v1/enroll", "", map[string]string{"fleet": fleet, "token": token, "credential_hash": hex.EncodeToString(sum[:])})
	if e != nil {
		return errors.New("enrollment outcome unknown; retain host credential and try hosting")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 204 {
		return errors.New("enrollment denied or unavailable; credential retained")
	}
	return nil
}
func dial(ctx context.Context, base, path, token, ticket string) (*websocket.Conn, error) {
	return dialAt(ctx, base, path, token, ticket, "")
}
func dialAt(ctx context.Context, base, path, token, ticket, dialAddress string) (*websocket.Conn, error) {
	address, e := endpoint(base, path, true)
	if e != nil {
		return nil, e
	}
	h := http.Header{"Authorization": []string{"Bearer " + token}}
	if ticket != "" {
		h.Set("X-Lamp-Ticket", ticket)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if dialAddress != "" {
		client.Transport = &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, dialAddress)
		}}
	}
	c, resp, e := websocket.Dial(ctx, address, &websocket.DialOptions{HTTPHeader: h, HTTPClient: client})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if e != nil {
		return nil, errors.New("relay connection denied or unavailable")
	}
	c.SetReadLimit(1 << 20)
	return c, nil
}

// Proxy is an OpenSSH ProxyCommand. SSH still verifies the independently pinned
// fleet host key/CA and authenticates the caller inside this ciphertext stream.
func Proxy(ctx context.Context, base, fleet, tokenFile string, in io.Reader, out io.Writer) error {
	if !validFleet(fleet) {
		return errors.New("invalid fleet")
	}
	token, e := accountToken(tokenFile)
	if e != nil {
		return e
	}
	setup, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	c, e := dial(setup, base, "/v1/connect/"+fleet, token, "")
	if e != nil {
		return e
	}
	defer c.CloseNow()
	conn := websocket.NetConn(ctx, c, websocket.MessageBinary)
	defer conn.Close()
	done := make(chan error, 2)
	go func() { _, e := io.Copy(conn, in); done <- e }()
	go func() { _, e := io.Copy(out, conn); done <- e }()
	select {
	case e = <-done:
		return e
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Host maintains one outbound carrier; it accepts no inbound listener. Only a
// configured loopback SSH service can be reached, never arbitrary relay targets.
func Host(ctx context.Context, base, credentialFile, target string) error {
	return HostHTTPS(ctx, base, credentialFile, target, "", "")
}
func HostHTTPS(ctx context.Context, base, credentialFile, target, httpsTarget, dialAddress string) error {
	raw, e := privateRead(credentialFile)
	if e != nil {
		return e
	}
	var credential Credential
	if json.Unmarshal(raw, &credential) != nil || !validFleet(credential.Fleet) || len(credential.Secret) != 64 {
		return errors.New("invalid host credential")
	}
	for _, address := range []string{target, httpsTarget, dialAddress} {
		if address == "" {
			continue
		}
		host, _, e := net.SplitHostPort(address)
		ip := net.ParseIP(host)
		if e != nil || ip == nil || !ip.IsLoopback() {
			return errors.New("host service and optional relay dial targets must be numeric loopback")
		}
	}
	if target == "" {
		return errors.New("SSH target required")
	}
	for {
		if ctx.Err() != nil {
			return nil
		}
		_ = hostSession(ctx, base, credential, target, httpsTarget, dialAddress)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(2 * time.Second):
		}
	}
}
func hostSession(ctx context.Context, base string, credential Credential, target, httpsTarget, dialAddress string) error {
	setup, cancel := context.WithTimeout(ctx, 15*time.Second)
	c, e := dialAt(setup, base, "/v1/carrier/"+credential.Fleet, credential.Secret, "", dialAddress)
	cancel()
	if e != nil {
		return e
	}
	defer c.CloseNow()
	session, stop := context.WithCancel(ctx)
	defer stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	defer stop()
	slots := make(chan struct{}, 16)
	for {
		_, raw, e := c.Read(session)
		if e != nil {
			return e
		}
		var open struct {
			Ticket  string `json:"ticket"`
			Service string `json:"service"`
		}
		if json.Unmarshal(raw, &open) != nil || len(open.Ticket) != 64 {
			return errors.New("invalid carrier message")
		}
		selected := target
		if open.Service == "https" {
			selected = httpsTarget
		} else if open.Service != "" && open.Service != "ssh" {
			return errors.New("unknown carrier service")
		}
		if selected == "" {
			continue
		}
		select {
		case slots <- struct{}{}:
		default:
			continue
		}
		wg.Add(1)
		go func(ticket, target string) {
			defer wg.Done()
			defer func() { <-slots }()
			_ = attach(session, base, credential, target, ticket, dialAddress)
		}(open.Ticket, selected)
	}
}
func attach(ctx context.Context, base string, credential Credential, target, ticket, dialAddress string) error {
	setup, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	local, e := (&net.Dialer{}).DialContext(setup, "tcp", target)
	if e != nil {
		return e
	}
	defer local.Close()
	c, e := dialAt(setup, base, "/v1/attach/"+credential.Fleet, credential.Secret, ticket, dialAddress)
	if e != nil {
		return e
	}
	defer c.CloseNow()
	remote := websocket.NetConn(ctx, c, websocket.MessageBinary)
	defer remote.Close()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(remote, local); done <- struct{}{} }()
	go func() { _, _ = io.Copy(local, remote); done <- struct{}{} }()
	select {
	case <-done:
	case <-ctx.Done():
	}
	return nil
}
