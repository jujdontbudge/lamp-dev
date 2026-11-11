// SPDX-License-Identifier: AGPL-3.0-only
package relay

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// helloConn lets Go's maintained TLS parser inspect a bounded ClientHello.
// It cannot emit a ServerHello, complete TLS or decrypt application traffic.
type helloConn struct {
	net.Conn
	hello bytes.Buffer
}

func (c *helloConn) Read(p []byte) (int, error) {
	remaining := (64 << 10) - c.hello.Len()
	if remaining <= 0 {
		return 0, errors.New("ClientHello too large")
	}
	if len(p) > remaining {
		p = p[:remaining]
	}
	n, e := c.Conn.Read(p)
	_, _ = c.hello.Write(p[:n])
	return n, e
}
func (c *helloConn) Write(p []byte) (int, error) { return len(p), nil }
func (s *Server) raw(ctx context.Context, l net.Listener) error {
	slots := make(chan struct{}, 64)
	for {
		conn, e := l.Accept()
		if e != nil {
			return e
		}
		select {
		case slots <- struct{}{}:
		default:
			_ = conn.Close()
			continue
		}
		releaseHello := sync.OnceFunc(func() { <-slots })
		go func() {
			defer releaseHello()
			defer conn.Close()
			s.rawConnection(ctx, conn, releaseHello)
		}()
	}
}
func (s *Server) rawConnection(parent context.Context, conn net.Conn, releaseHello func()) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	probe := &helloConn{Conn: conn}
	name := ""
	parser := tls.Server(probe, &tls.Config{MinVersion: tls.VersionTLS12, GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) {
		name = h.ServerName
		return nil, errors.New("SNI routing only")
	}})
	_ = parser.HandshakeContext(ctx)
	// The shared pool bounds only ClientHello parsing. A routed stream holds
	// per-fleet capacity instead, including while waiting for its attachment.
	releaseHello()
	b := s.config.Browser
	var target net.Conn
	var e error
	switch name {
	case s.config.Hostname, "account.eilmer.si":
		release, ok := s.acquireStream("ingress:"+name, 64)
		if !ok {
			return
		}
		defer release()
		address := s.config.Listen
		if name == "account.eilmer.si" {
			address = b.AccountTarget
		}
		target, e = (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", address)
	default:
		fleet := b.Routes[name]
		if fleet == "" {
			return
		}
		target, e = s.open(ctx, fleet, "https")
	}
	if e != nil {
		return
	}
	defer target.Close()
	splice(ctx, conn, target, probe.hello.Bytes(), 5*time.Minute)
}

// Successful traffic in either direction refreshes both endpoints. In
// particular, an active one-way download must not look like an idle connection.
// Established ingress gets a generous inactivity budget; authenticated SSH
// instead relies on account expiry/revocation. Setup has its own short bounds.
type activeConn struct {
	net.Conn
	touch func()
}

func (c activeConn) Read(p []byte) (int, error) {
	n, e := c.Conn.Read(p)
	if n > 0 {
		c.touch()
	}
	return n, e
}
func (c activeConn) Write(p []byte) (int, error) {
	n, e := c.Conn.Write(p)
	if n > 0 {
		c.touch()
	}
	return n, e
}
func splice(ctx context.Context, left, right net.Conn, prefix []byte, idle time.Duration) {
	var mu sync.Mutex
	touch := func() {
		mu.Lock()
		defer mu.Unlock()
		var deadline time.Time
		if idle > 0 {
			deadline = time.Now().Add(idle)
		}
		_ = left.SetDeadline(deadline)
		_ = right.SetDeadline(deadline)
	}
	stop := context.AfterFunc(ctx, func() { _ = left.Close(); _ = right.Close() })
	defer stop()
	// Bound initial forwarding without imposing that setup deadline on the
	// established stream (in particular, never on an idle SSH session).
	if len(prefix) != 0 {
		_ = right.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if _, e := io.Copy(right, bytes.NewReader(prefix)); e != nil {
			return
		}
	}
	touch()
	a, b := left, right
	if idle > 0 {
		a, b = activeConn{left, touch}, activeConn{right, touch}
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(b, a); done <- struct{}{} }()
	go func() { _, _ = io.Copy(a, b); done <- struct{}{} }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}
func (s *Server) open(parent context.Context, fleet, service string) (net.Conn, error) {
	release, ok := s.acquireStream("route:"+fleet, 32)
	if !ok {
		return nil, errors.New("fleet routing capacity unavailable")
	}
	handedOff := false
	defer func() {
		if !handedOff {
			release()
		}
	}()
	ctx, cancel := context.WithCancel(parent)
	ticket := secret()
	p := &pending{fleet: fleet, ctx: ctx, ready: make(chan net.Conn)}
	s.mu.Lock()
	p.host = s.hosts[fleet]
	if p.host != nil {
		s.pending[ticket] = p
	}
	s.mu.Unlock()
	if p.host == nil {
		cancel()
		return nil, errors.New("fleet offline")
	}
	defer func() { s.mu.Lock(); delete(s.pending, ticket); s.mu.Unlock() }()
	stop := context.AfterFunc(p.host.ctx, cancel)
	cleanup := func() { cancel(); stop(); release() }
	setup, setupCancel := context.WithTimeout(ctx, 10*time.Second)
	defer setupCancel()
	raw, _ := json.Marshal(map[string]string{"ticket": ticket, "service": service})
	p.host.write.Lock()
	e := p.host.conn.Write(setup, websocket.MessageText, raw)
	p.host.write.Unlock()
	if e != nil {
		cleanup()
		return nil, errors.New("carrier unavailable")
	}
	select {
	case host := <-p.ready:
		handedOff = true
		return closingConn{host, cleanup}, nil
	case <-setup.Done():
		cleanup()
		return nil, errors.New("routing attachment unavailable")
	}
}
