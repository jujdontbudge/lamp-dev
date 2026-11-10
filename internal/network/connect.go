package network

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/idna"
)

const maxHead = 16 << 10
const maxDatagram = 65507

// ConnectProvider terminates at a host-owned loopback SSH tunnel. The SSH
// process owns authentication; no key, URL credential or auth header is ever
// passed to a guest. Numeric TCP CONNECT and bounded, framed UDP are supported.
// This UDP extension requires the owned ServeConnect endpoint; it is not
// advertised as arbitrary third-party HTTP CONNECT-UDP interoperability.
type ConnectProvider struct {
	life     providerLife
	endpoint netip.AddrPort
	dialer   net.Dialer
}

func NewConnectProvider(endpoint netip.AddrPort) (*ConnectProvider, error) {
	if !endpoint.IsValid() || endpoint.Port() == 0 || !endpoint.Addr().IsLoopback() || endpoint.Addr().Zone() != "" {
		return nil, ErrDenied
	}
	return &ConnectProvider{endpoint: endpoint}, nil
}
func (p *ConnectProvider) Resolve(ctx context.Context, name string) ([]netip.Addr, error) {
	if !p.life.alive() {
		return nil, ErrUnavailable
	}
	if ip, err := netip.ParseAddr(name); err == nil {
		return []netip.Addr{ip.Unmap()}, nil
	}
	name, err := exitDNSName(name)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, DialTimeout)
	defer cancel()
	raw, err := p.dialer.DialContext(ctx, "tcp", p.endpoint.String())
	if err != nil {
		return nil, ErrUnavailable
	}
	conn, err := p.life.own(raw)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	if conn.SetDeadline(deadline) != nil {
		return nil, ErrUnavailable
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	// The owned endpoint resolves in the remote exit's DNS context. No local
	// resolver (including a caller-injected resolver) is involved or retried.
	target := net.JoinHostPort(name, "53")
	if writeAll(conn, []byte("CONNECT "+target+" HTTP/1.1\r\nHost: "+target+"\r\nX-Lamp-Transport: dns\r\n\r\n")) != nil {
		return nil, ErrUnavailable
	}
	reader := bufio.NewReader(conn)
	head, err := readHead(reader)
	if err != nil {
		return nil, ErrUnavailable
	}
	response, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(head)), nil)
	if err != nil || response.StatusCode != 200 || len(response.Header.Values("X-Lamp-Transport")) != 1 || response.Header.Get("X-Lamp-Transport") != "dns" || len(response.TransferEncoding) != 0 || response.ContentLength <= 0 || response.ContentLength > 4096 {
		return nil, ErrUnavailable
	}
	body := make([]byte, int(response.ContentLength))
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, ErrUnavailable
	}
	var addresses []netip.Addr
	if json.Unmarshal(body, &addresses) != nil || len(addresses) == 0 || len(addresses) > 64 || ctx.Err() != nil || !p.life.alive() {
		return nil, ErrUnavailable
	}
	for i, address := range addresses {
		if !address.IsValid() || address.Zone() != "" {
			return nil, ErrUnavailable
		}
		addresses[i] = address.Unmap()
	}
	return addresses, nil
}

// Restrict the extension to a DNS hostname, not arbitrary CONNECT authorities
// or header bytes. Literal addresses take the numeric path instead.
func exitDNSName(name string) (string, error) {
	name, err := idna.Lookup.ToASCII(strings.TrimSuffix(name, "."))
	if err != nil || name == "" || len(name) > 253 {
		return "", ErrDenied
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", ErrDenied
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return "", ErrDenied
			}
		}
	}
	return strings.ToLower(name) + ".", nil
}
func (p *ConnectProvider) NumericDial(ctx context.Context, d Destination) (net.Conn, error) {
	d = d.canonical()
	if !d.valid() {
		return nil, ErrDenied
	}
	if !p.life.alive() {
		return nil, ErrUnavailable
	}
	raw, err := p.dialer.DialContext(ctx, "tcp", p.endpoint.String())
	if err != nil {
		return nil, ErrUnavailable
	}
	success := false
	defer func() {
		if !success {
			raw.Close()
		}
	}()
	deadline := time.Now().Add(DialTimeout)
	if until, ok := ctx.Deadline(); ok && until.Before(deadline) {
		deadline = until
	}
	if err := raw.SetDeadline(deadline); err != nil {
		return nil, ErrUnavailable
	}
	stop := context.AfterFunc(ctx, func() { _ = raw.Close() })
	defer stop()
	target := d.AddrPort().String()
	if err := writeAll(raw, []byte("CONNECT "+target+" HTTP/1.1\r\nHost: "+target+"\r\nX-Lamp-Transport: "+d.Protocol+"\r\n\r\n")); err != nil {
		return nil, ErrUnavailable
	}
	reader := bufio.NewReader(raw)
	head, err := readHead(reader)
	if err != nil {
		return nil, ErrUnavailable
	}
	response, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(head)), nil)
	if err != nil {
		return nil, ErrUnavailable
	}
	// A generic HTTP CONNECT server must not accidentally treat UDP as TCP.
	if response.StatusCode != 200 || len(response.Header.Values("X-Lamp-Transport")) != 1 || response.Header.Get("X-Lamp-Transport") != d.Protocol || len(response.TransferEncoding) != 0 || response.ContentLength > 0 {
		return nil, ErrUnavailable
	}
	if ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	if err := raw.SetDeadline(time.Time{}); err != nil {
		return nil, ErrUnavailable
	}
	var conn net.Conn = &bufferedConn{Conn: raw, reader: reader}
	if d.Protocol == "udp" {
		conn = &datagramConn{Conn: conn}
	}
	conn, err = p.life.own(conn)
	if err != nil {
		return nil, err
	}
	success = true
	return conn, nil
}
func (p *ConnectProvider) Close() error { return p.life.close() }

func readHead(reader *bufio.Reader) ([]byte, error) {
	var head []byte
	for len(head) <= maxHead {
		line, err := reader.ReadSlice('\n')
		if err != nil {
			return nil, ErrDenied
		}
		head = append(head, line...)
		if len(head) > maxHead {
			return nil, ErrDenied
		}
		if len(line) == 2 && line[0] == '\r' {
			return head, nil
		}
	}
	return nil, ErrDenied
}
func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(b []byte) (int, error) { return c.reader.Read(b) }
func (c *bufferedConn) CloseWrite() error {
	if conn, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return conn.CloseWrite()
	}
	return nil
}

type datagramConn struct {
	net.Conn
	readMu, writeMu sync.Mutex
}

func (c *datagramConn) Read(b []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	var size [4]byte
	if _, err := io.ReadFull(c.Conn, size[:]); err != nil {
		return 0, err
	}
	n := binary.BigEndian.Uint32(size[:])
	if n > maxDatagram {
		c.Conn.Close()
		return 0, ErrDenied
	}
	if int(n) > len(b) {
		_, err := io.CopyN(io.Discard, c.Conn, int64(n))
		if err != nil {
			return 0, err
		}
		return 0, io.ErrShortBuffer
	}
	return io.ReadFull(c.Conn, b[:int(n)])
}
func (c *datagramConn) Write(b []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if len(b) > maxDatagram {
		return 0, ErrDenied
	}
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(b)))
	if err := writeAll(c.Conn, size[:]); err != nil {
		return 0, err
	}
	if err := writeAll(c.Conn, b); err != nil {
		c.Conn.Close()
		return 0, err
	}
	return len(b), nil
}

// ServeConnect is an owned exit fixture/service, reachable only through local
// loopback (normally an authenticated SSH tunnel). Its own Director enforces
// the remote host's address/LAN/metadata ceiling before a numeric final dial.
// Cancellation closes the listener, pending clients and every active relay.
func ServeConnect(ctx context.Context, listener net.Listener, director *Director) error {
	address, err := netip.ParseAddrPort(listener.Addr().String())
	if err != nil || !address.Addr().IsLoopback() {
		return ErrDenied
	}
	clients := &providerLife{}
	stop := context.AfterFunc(ctx, func() { listener.Close(); _ = clients.close() })
	defer stop()
	defer listener.Close()
	sem := make(chan struct{}, 64)
	var running sync.WaitGroup
	defer func() { _ = clients.close(); running.Wait() }()
	for {
		raw, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return ErrUnavailable
		}
		select {
		case sem <- struct{}{}:
		default:
			raw.Close()
			continue
		}
		client, err := clients.own(raw)
		if err != nil {
			<-sem
			continue
		}
		running.Add(1)
		go func() {
			defer running.Done()
			defer func() { <-sem }()
			defer client.Close()
			serveConnectOne(ctx, client, director)
		}()
	}
}
func serveConnectOne(ctx context.Context, client net.Conn, director *Director) {
	if client.SetDeadline(time.Now().Add(DialTimeout)) != nil {
		return
	}
	reader := bufio.NewReader(client)
	head, err := readHead(reader)
	refuse := func(status int) {
		_ = writeAll(client, []byte(fmt.Sprintf("HTTP/1.1 %d Rejected\r\nContent-Length: 0\r\nConnection: close\r\n\r\n", status)))
	}
	if err != nil {
		refuse(400)
		return
	}
	request, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(head)))
	if err != nil {
		refuse(400)
		return
	}
	transport := request.Header.Get("X-Lamp-Transport")
	if request.Method != "CONNECT" || request.Proto != "HTTP/1.1" || request.Host != request.RequestURI || len(request.Header.Values("X-Lamp-Transport")) != 1 || (transport != "tcp" && transport != "udp" && transport != "dns") || len(request.TransferEncoding) != 0 || request.ContentLength > 0 {
		refuse(400)
		return
	}
	for header := range request.Header {
		if !strings.EqualFold(header, "X-Lamp-Transport") {
			refuse(400)
			return
		}
	}
	if transport == "dns" {
		host, port, err := net.SplitHostPort(request.RequestURI)
		name, nameErr := exitDNSName(host)
		if err != nil || port != "53" || nameErr != nil {
			refuse(400)
			return
		}
		resolve, cancel := context.WithTimeout(ctx, DialTimeout)
		defer cancel()
		addresses, err := director.dnsIPs(resolve, name, dnsmessage.TypeALL)
		if err != nil || len(addresses) == 0 {
			refuse(502)
			return
		}
		body, err := json.Marshal(addresses)
		if err != nil || len(body) > 4096 {
			refuse(502)
			return
		}
		_ = writeAll(client, []byte(fmt.Sprintf("HTTP/1.1 200 OK\r\nX-Lamp-Transport: dns\r\nContent-Length: %d\r\n\r\n", len(body))))
		_ = writeAll(client, body)
		return
	}
	ap, err := netip.ParseAddrPort(request.RequestURI)
	if err != nil || ap.Port() == 0 {
		refuse(400)
		return
	}
	target := Destination{Address: ap.Addr(), Protocol: transport, Port: ap.Port()}
	permit, err := director.Authorize(director.subject, target)
	if err != nil {
		refuse(403)
		return
	}
	upstream, err := director.Dial(ctx, permit)
	if err != nil {
		refuse(502)
		return
	}
	defer upstream.Close()
	if err := writeAll(client, []byte("HTTP/1.1 200 Connected\r\nX-Lamp-Transport: "+transport+"\r\n\r\n")); err != nil {
		return
	}
	// Handshake timeout is not a session lifetime. Route/policy/context closure
	// still fences the tracked connection without a periodic renewal protocol.
	if client.SetDeadline(time.Time{}) != nil {
		return
	}
	var inbound net.Conn = &bufferedConn{Conn: client, reader: reader}
	if transport == "udp" {
		inbound = &datagramConn{Conn: inbound}
	}
	relay(ctx, inbound, upstream, transport)
}
func relay(ctx context.Context, a, b net.Conn, protocol string) {
	refreshUDP := func() error {
		until := time.Now().Add(UDPIdleTimeout)
		if err := a.SetReadDeadline(until); err != nil {
			return err
		}
		return b.SetReadDeadline(until)
	}
	if protocol == "udp" && refreshUDP() != nil {
		a.Close()
		b.Close()
		return
	}
	pump := func(dst, src net.Conn) error {
		if protocol == "udp" {
			buf := make([]byte, maxDatagram)
			for {
				n, err := src.Read(buf)
				if err != nil {
					return err
				}
				if err := refreshUDP(); err != nil {
					return err
				}
				if err := dst.SetWriteDeadline(time.Now().Add(DialTimeout)); err != nil {
					return err
				}
				written, err := dst.Write(buf[:n])
				if err != nil {
					return err
				}
				if written != n {
					return io.ErrShortWrite
				}
			}
		}
		_, err := io.Copy(dst, src)
		if closeWriter, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = closeWriter.CloseWrite()
		}
		return err
	}
	done := make(chan error, 2)
	go func() { done <- pump(a, b) }()
	go func() { done <- pump(b, a) }()
	first := false
	select {
	case err := <-done:
		first = true
		if err != nil || protocol == "udp" {
			a.Close()
			b.Close()
		}
	case <-ctx.Done():
		a.Close()
		b.Close()
	}
	if first {
		select {
		case <-done:
		case <-ctx.Done():
			a.Close()
			b.Close()
			<-done
		}
	} else {
		<-done
		<-done
	}
}
