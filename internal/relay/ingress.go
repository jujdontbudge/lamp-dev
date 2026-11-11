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
		go func() { defer func() { <-slots }(); defer conn.Close(); s.rawConnection(ctx, conn) }()
	}
}
func (s *Server) rawConnection(parent context.Context, conn net.Conn) {
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
	_ = conn.SetReadDeadline(time.Time{})
	b := s.config.Browser
	var target net.Conn
	var e error
	switch name {
	case s.config.Hostname:
		target, e = (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", s.config.Listen)
	case "account.eilmer.si":
		target, e = (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", b.AccountTarget)
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
	if _, e = target.Write(probe.hello.Bytes()); e != nil {
		return
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(target, conn); done <- struct{}{} }()
	go func() { _, _ = io.Copy(conn, target); done <- struct{}{} }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}
func (s *Server) open(parent context.Context, fleet, service string) (net.Conn, error) {
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
	cleanup := func() { cancel(); stop() }
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
		return closingConn{host, cleanup}, nil
	case <-setup.Done():
		cleanup()
		return nil, errors.New("routing attachment unavailable")
	}
}
