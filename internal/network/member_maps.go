package network

import (
	"context"
	"io"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"
)

// GuestEndpoint is supplied by an engine's private packet transport, not a host
// IP dialer and not guest request fields. Implementations must bind Subject to
// the engine handle. No default/ambient host implementation exists.
type GuestEndpoint interface {
	Subject() Subject
	DialGuest(context.Context, string, uint16) (net.Conn, error)
}
type PortMap struct {
	Protocol   string `json:"protocol"`
	MemberPort uint16 `json:"member_port"`
	GuestPort  uint16 `json:"guest_port"`
}
type MappedPorts struct {
	member         *Member
	subject        Subject
	guest          GuestEndpoint
	audit          Audit
	revision       uint64
	ctx            context.Context
	cancel         context.CancelFunc
	once           sync.Once
	life           providerLife
	closers        []io.Closer
	running        sync.WaitGroup
	tcpSem, udpSem chan struct{}
	reserved       int
}

func (m *Member) MapPorts(ctx context.Context, maps []PortMap, guest GuestEndpoint, revision uint64, audit Audit) (*MappedPorts, error) {
	if !m.available() || guest == nil || guest.Subject() != m.subject || revision == 0 || audit == nil || len(maps) == 0 || len(maps) > 64 {
		return nil, ErrDenied
	}
	seen := map[string]bool{}
	for _, mapping := range maps {
		key := mapping.Protocol + ":" + strconv.Itoa(int(mapping.MemberPort))
		if (mapping.Protocol != "tcp" && mapping.Protocol != "udp") || mapping.MemberPort == 0 || mapping.GuestPort == 0 || seen[key] {
			return nil, ErrDenied
		}
		seen[key] = true
	}
	child, cancel := context.WithCancel(ctx)
	p := &MappedPorts{member: m, subject: m.subject, guest: guest, audit: audit, revision: revision, ctx: child, cancel: cancel, tcpSem: m.tcpSem, udpSem: m.udpSem, reserved: len(maps)}
	m.mu.Lock()
	if !m.ready || m.mapCount+len(maps) > 64 {
		m.mu.Unlock()
		cancel()
		return nil, ErrDenied
	}
	m.mapCount += len(maps)
	m.mu.Unlock()
	var start []func()
	for _, mapping := range maps {
		switch mapping.Protocol {
		case "tcp":
			listener, err := m.server.Listen("tcp", ":"+strconv.Itoa(int(mapping.MemberPort)))
			if err != nil {
				p.Close()
				return nil, ErrUnavailable
			}
			p.closers = append(p.closers, listener)
			start = append(start, func() { p.tcp(listener, mapping) })
		case "udp":
			for _, address := range m.Addresses() {
				packet, err := m.server.ListenPacket("udp", netip.AddrPortFrom(address, mapping.MemberPort).String())
				if err != nil {
					p.Close()
					return nil, ErrUnavailable
				}
				p.closers = append(p.closers, packet)
				mapper := &udpMapper{ports: p, packet: packet, mapping: mapping, associations: map[string]*udpAssociation{}}
				start = append(start, mapper.run)
			}
		}
	}
	m.mu.Lock()
	if !m.ready {
		m.mu.Unlock()
		p.Close()
		return nil, ErrUnavailable
	}
	p.running.Add(len(start))
	m.maps = append(m.maps, p)
	m.mu.Unlock()
	for _, fn := range start {
		go func() { defer p.running.Done(); fn() }()
	}
	// Register after goroutine accounting. An already-cancelled context closes
	// every listener and pending/active stream without a start/Wait race.
	context.AfterFunc(child, func() { p.Close() })
	return p, nil
}
func (p *MappedPorts) Close() error {
	p.once.Do(func() {
		p.cancel()
		for _, closer := range p.closers {
			_ = closer.Close()
		}
		_ = p.life.close()
		p.running.Wait()
		p.member.mu.Lock()
		p.member.mapCount -= p.reserved
		for i, ports := range p.member.maps {
			if ports == p {
				p.member.maps = append(p.member.maps[:i], p.member.maps[i+1:]...)
				break
			}
		}
		p.member.mu.Unlock()
	})
	return nil
}
func (p *MappedPorts) admit(mapping PortMap, remote net.Addr) error {
	ctx, cancel := context.WithTimeout(p.ctx, DialTimeout)
	defer cancel()
	peer, err := p.member.WhoIs(ctx, mapping.Protocol, remote.String())
	if err != nil {
		return err
	}
	return p.audit.Record(Event{Kind: "member_ingress", Subject: p.subject, Revision: p.revision, Decision: "allow", Reason: "explicit_port_map", Protocol: mapping.Protocol, MemberPort: mapping.MemberPort, GuestPort: mapping.GuestPort, Peer: &peer})
}
func (p *MappedPorts) tcp(listener net.Listener, mapping PortMap) {
	for {
		raw, err := listener.Accept()
		if err != nil {
			go p.Close()
			return
		}
		select {
		case p.tcpSem <- struct{}{}:
		default:
			raw.Close()
			continue
		}
		client, err := p.life.own(raw)
		if err != nil {
			<-p.tcpSem
			continue
		}
		p.running.Add(1)
		go func() {
			defer p.running.Done()
			defer func() { <-p.tcpSem }()
			defer client.Close()
			if p.admit(mapping, client.RemoteAddr()) != nil {
				go p.Close()
				return
			}
			ctx, cancel := context.WithTimeout(p.ctx, DialTimeout)
			defer cancel()
			raw, err := p.guest.DialGuest(ctx, "tcp", mapping.GuestPort)
			if err != nil {
				return
			}
			upstream, err := p.life.own(raw)
			if err != nil {
				return
			}
			defer upstream.Close()
			deadline := time.Now().Add(FlowLease)
			if until, ok := p.ctx.Deadline(); ok && until.Before(deadline) {
				deadline = until
			}
			if client.SetDeadline(deadline) != nil || upstream.SetDeadline(deadline) != nil {
				return
			}
			relay(p.ctx, client, upstream, "tcp")
		}()
	}
}

type udpMapper struct {
	ports        *MappedPorts
	packet       net.PacketConn
	mapping      PortMap
	mu           sync.Mutex
	associations map[string]*udpAssociation
}
type udpAssociation struct {
	conn   net.Conn
	remote net.Addr
	until  time.Time
	once   sync.Once
}

func (u *udpMapper) remove(key string, a *udpAssociation) {
	a.once.Do(func() {
		u.mu.Lock()
		if u.associations[key] == a {
			delete(u.associations, key)
		}
		u.mu.Unlock()
		_ = a.conn.Close()
		<-u.ports.udpSem
	})
}
func (u *udpMapper) association(key string, remote net.Addr) (*udpAssociation, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if current := u.associations[key]; current != nil {
		return current, nil
	}
	select {
	case u.ports.udpSem <- struct{}{}:
	default:
		return nil, ErrUnavailable
	}
	fail := func(err error) (*udpAssociation, error) { <-u.ports.udpSem; return nil, err }
	if u.ports.admit(u.mapping, remote) != nil {
		go u.ports.Close()
		return fail(ErrUnavailable)
	}
	ctx, cancel := context.WithTimeout(u.ports.ctx, DialTimeout)
	defer cancel()
	raw, err := u.ports.guest.DialGuest(ctx, "udp", u.mapping.GuestPort)
	if err != nil {
		return fail(ErrUnavailable)
	}
	conn, err := u.ports.life.own(raw)
	if err != nil {
		return fail(ErrUnavailable)
	}
	until := time.Now().Add(FlowLease)
	if limit, ok := u.ports.ctx.Deadline(); ok && limit.Before(until) {
		until = limit
	}
	if conn.SetDeadline(until) != nil {
		conn.Close()
		return fail(ErrUnavailable)
	}
	a := &udpAssociation{conn: conn, remote: remote, until: until}
	u.associations[key] = a
	u.ports.running.Add(1)
	go func() {
		defer u.ports.running.Done()
		defer u.remove(key, a)
		buffer := make([]byte, maxDatagram)
		for {
			n, err := a.conn.Read(buffer)
			if err != nil {
				return
			}
			if !time.Now().Before(a.until) {
				return
			}
			_ = u.packet.SetWriteDeadline(time.Now().Add(DialTimeout))
			written, err := u.packet.WriteTo(buffer[:n], a.remote)
			if err != nil || written != n {
				return
			}
		}
	}()
	return a, nil
}
func (u *udpMapper) run() {
	buffer := make([]byte, maxDatagram+1)
	for {
		n, remote, err := u.packet.ReadFrom(buffer)
		if err != nil {
			go u.ports.Close()
			return
		}
		if n > maxDatagram {
			continue
		}
		ap, err := netip.ParseAddrPort(remote.String())
		if err != nil || ap.Port() == 0 || !contains(prefixes("100.64.0.0/10", "fd7a:115c:a1e0::/48"), ap.Addr()) {
			continue
		}
		key := ap.String()
		a, err := u.association(key, net.UDPAddrFromAddrPort(ap))
		if err != nil {
			continue
		}
		until := time.Now().Add(DialTimeout)
		if a.until.Before(until) {
			until = a.until
		}
		if a.conn.SetWriteDeadline(until) != nil {
			u.remove(key, a)
			continue
		}
		if written, err := a.conn.Write(buffer[:n]); err != nil || written != n {
			u.remove(key, a)
		}
	}
}
