package network

import (
	"context"
	"crypto/rand"
	"io"
	"net"
	"net/netip"
	"slices"
	"sync"
	"time"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/link/ethernet"
	"gvisor.dev/gvisor/pkg/tcpip/network/arp"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"
)

// BridgeConfig describes a private engine transport. Addresses do not choose
// identity: Subject is bound by the supervisor's assigned descriptor/handle.
type BridgeConfig struct {
	Subject          Subject      `json:"subject"`
	GuestAddresses   []netip.Addr `json:"guest_addresses"`
	GatewayAddresses []netip.Addr `json:"gateway_addresses"`
	// Optional trusted namespace proxy sources, never a prefix or guest header.
	ServiceSources []netip.Addr `json:"service_sources"`
	NextHop4       netip.Addr   `json:"next_hop4"`
	NextHop6       netip.Addr   `json:"next_hop6"`
	MTU            uint32       `json:"mtu"`
}

// FrameBridge owns exactly one private datagram/Ethernet descriptor. There is
// no host bridge, kernel default-route fallback, namespace or VPN key in the
// guest. Closing it disconnects the only packet path. The same API accepts the
// Mac adapter's private Ethernet handle once T8 supplies it.
type FrameBridge struct {
	config   BridgeConfig
	frames   io.ReadWriteCloser
	director *Director
	stack    *stack.Stack
	link     *channel.Endpoint
	ctx      context.Context
	cancel   context.CancelFunc
	life     providerLife
	flows    chan struct{}
	once     sync.Once
	running  sync.WaitGroup
}

func NewFrameBridge(parent context.Context, c BridgeConfig, frames io.ReadWriteCloser, director *Director) (*FrameBridge, error) {
	if frames == nil || director == nil || c.Subject != director.subject || len(c.GuestAddresses) == 0 || len(c.GuestAddresses) > 2 || len(c.GatewayAddresses) == 0 || len(c.GatewayAddresses) > 2 || len(c.ServiceSources) > 2 || c.MTU < 1280 || c.MTU > 9000 {
		return nil, ErrDenied
	}
	c.GuestAddresses = slices.Clone(c.GuestAddresses)
	c.GatewayAddresses = slices.Clone(c.GatewayAddresses)
	c.ServiceSources = slices.Clone(c.ServiceSources)
	for _, addresses := range [][]netip.Addr{c.GuestAddresses, c.GatewayAddresses, c.ServiceSources} {
		for _, a := range addresses {
			if !a.IsValid() || a.Is4In6() || a.Zone() != "" || a.IsUnspecified() || a.IsMulticast() {
				return nil, ErrDenied
			}
		}
	}
	for _, a := range c.GatewayAddresses {
		if slices.Contains(c.GuestAddresses, a) || slices.Contains(c.ServiceSources, a) {
			return nil, ErrDenied
		}
	}
	if c.NextHop4.IsValid() && (!c.NextHop4.Is4() || c.NextHop4.IsUnspecified()) {
		return nil, ErrDenied
	}
	if c.NextHop6.IsValid() && (!c.NextHop6.Is6() || c.NextHop6.Is4In6() || c.NextHop6.IsUnspecified()) {
		return nil, ErrDenied
	}
	mac := make([]byte, 6)
	if _, err := rand.Read(mac); err != nil {
		return nil, ErrUnavailable
	}
	mac[0] = (mac[0] | 2) & 0xfe
	link := channel.New(256, c.MTU+header.EthernetMinimumSize, tcpip.LinkAddress(mac))
	s := stack.New(stack.Options{NetworkProtocols: []stack.NetworkProtocolFactory{ipv4.NewProtocol, ipv6.NewProtocol, arp.NewProtocol}, TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol}})
	fail := func() (*FrameBridge, error) { s.Close(); s.Wait(); link.Close(); return nil, ErrUnavailable }
	if s.CreateNIC(1, ethernet.New(link)) != nil || s.SetPromiscuousMode(1, true) != nil || s.SetSpoofing(1, true) != nil {
		return fail()
	}
	for _, a := range c.GatewayAddresses {
		protocol := ipv4.ProtocolNumber
		bits := 32
		if a.Is6() {
			protocol = ipv6.ProtocolNumber
			bits = 128
		}
		if s.AddProtocolAddress(1, tcpip.ProtocolAddress{Protocol: protocol, AddressWithPrefix: tcpip.AddressWithPrefix{Address: tcpip.AddrFromSlice(a.AsSlice()), PrefixLen: bits}}, stack.AddressProperties{}) != nil {
			return fail()
		}
	}
	routes := []tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: 1}, {Destination: header.IPv6EmptySubnet, NIC: 1}}
	if c.NextHop4.IsValid() {
		routes[0].Gateway = tcpip.AddrFromSlice(c.NextHop4.AsSlice())
	}
	if c.NextHop6.IsValid() {
		routes[1].Gateway = tcpip.AddrFromSlice(c.NextHop6.AsSlice())
	}
	s.SetRouteTable(routes)
	sendBuffer := tcpip.TCPSendBufferSizeRangeOption{Min: 4096, Default: 32 << 10, Max: 128 << 10}
	receiveBuffer := tcpip.TCPReceiveBufferSizeRangeOption{Min: 4096, Default: 32 << 10, Max: 128 << 10}
	if s.SetTransportProtocolOption(tcp.ProtocolNumber, &sendBuffer) != nil || s.SetTransportProtocolOption(tcp.ProtocolNumber, &receiveBuffer) != nil {
		return fail()
	}
	ctx, cancel := context.WithCancel(parent)
	b := &FrameBridge{config: c, frames: frames, director: director, stack: s, link: link, ctx: ctx, cancel: cancel, flows: make(chan struct{}, 128)}
	tcpForwarder := tcp.NewForwarder(s, 32<<10, 64, b.tcp)
	s.SetTransportProtocolHandler(tcp.ProtocolNumber, tcpForwarder.HandlePacket)
	// Synchronous endpoint creation retains only gVisor's receive queue, not an
	// orphaned clone of every denied UDP packet in Forwarder.HandlePacket.
	s.SetTransportProtocolHandler(udp.ProtocolNumber, b.udp)
	b.running.Add(2)
	go func() { defer b.running.Done(); b.input() }()
	go func() { defer b.running.Done(); b.output() }()
	context.AfterFunc(ctx, func() { b.Close() })
	go func() {
		select {
		case <-ctx.Done():
		case <-director.Done():
			b.Close()
		}
	}()
	return b, nil
}
func (b *FrameBridge) Subject() Subject      { return b.config.Subject }
func (b *FrameBridge) Done() <-chan struct{} { return b.ctx.Done() }
func (b *FrameBridge) Close() error {
	b.once.Do(func() {
		b.cancel()
		_ = b.frames.Close()
		_ = b.life.close()
		_ = b.director.Close()
		b.stack.Close()
		b.link.Close()
		b.running.Wait()
		b.stack.Wait()
	})
	return nil
}
func (b *FrameBridge) input() {
	sources := append(slices.Clone(b.config.GuestAddresses), b.config.ServiceSources...)
	frame := make([]byte, b.config.MTU+header.EthernetMinimumSize+1)
	for {
		n, err := b.frames.Read(frame)
		if err != nil {
			go b.Close()
			return
		}
		if n > int(b.config.MTU)+header.EthernetMinimumSize {
			b.director.RejectFrame()
			continue
		}
		_, ok := frameClass(frame[:n], sources, b.config.GatewayAddresses)
		if !ok {
			b.director.RejectFrame()
			continue
		}
		packet := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(append([]byte(nil), frame[:n]...))})
		b.link.InjectInbound(0, packet)
		packet.DecRef()
	}
}
func (b *FrameBridge) output() {
	for {
		packet := b.link.ReadContext(b.ctx)
		if packet == nil {
			return
		}
		view := packet.ToView()
		data := view.AsSlice()
		n, err := b.frames.Write(data)
		view.Release()
		packet.DecRef()
		if err != nil || n != len(data) {
			go b.Close()
			return
		}
	}
}
func (b *FrameBridge) target(id stack.TransportEndpointID, protocol string) (Permit, error) {
	destination, ok := netip.AddrFromSlice(id.LocalAddress.AsSlice())
	if !ok {
		return Permit{}, ErrDenied
	}
	return b.director.Authorize(b.config.Subject, Destination{Address: destination.Unmap(), Protocol: protocol, Port: id.LocalPort})
}
func (b *FrameBridge) tcp(request *tcp.ForwarderRequest) {
	select {
	case b.flows <- struct{}{}:
	default:
		request.Complete(true)
		return
	}
	defer func() { <-b.flows }()
	id := request.ID()
	address, ok := netip.AddrFromSlice(id.LocalAddress.AsSlice())
	if ok && b.isDNS(address, request.ID().LocalPort) {
		var queue waiter.Queue
		endpoint, stackErr := request.CreateEndpoint(&queue)
		request.Complete(stackErr != nil)
		if stackErr != nil {
			return
		}
		client, err := b.life.own(gonet.NewTCPConn(&queue, endpoint))
		if err != nil {
			return
		}
		b.serveDNS(client, "tcp")
		return
	}
	permit, err := b.target(request.ID(), "tcp")
	if err != nil {
		request.Complete(true)
		return
	}
	upstream, err := b.director.Dial(b.ctx, permit)
	if err != nil {
		request.Complete(true)
		return
	}
	defer upstream.Close()
	var queue waiter.Queue
	endpoint, stackErr := request.CreateEndpoint(&queue)
	request.Complete(stackErr != nil)
	if stackErr != nil {
		return
	}
	raw := gonet.NewTCPConn(&queue, endpoint)
	client, err := b.life.own(raw)
	if err != nil {
		return
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(FlowLease))
	relay(b.ctx, client, upstream, "tcp")
}
func (b *FrameBridge) udp(id stack.TransportEndpointID, packet *stack.PacketBuffer) bool {
	select {
	case b.flows <- struct{}{}:
	default:
		return true
	}
	address, ok := netip.AddrFromSlice(id.LocalAddress.AsSlice())
	dns := ok && b.isDNS(address, id.LocalPort)
	var permit Permit
	var err error
	if !dns {
		permit, err = b.target(id, "udp")
	}
	if err != nil {
		<-b.flows
		return true
	}
	var queue waiter.Queue
	endpoint, stackErr := udp.NewForwarderRequest(b.stack, id, packet).CreateEndpoint(&queue)
	if stackErr != nil {
		<-b.flows
		return true
	}
	raw := gonet.NewUDPConn(&queue, endpoint)
	client, err := b.life.own(raw)
	if err != nil {
		<-b.flows
		return true
	}
	go func() {
		defer func() { <-b.flows }()
		defer client.Close()
		if dns {
			b.serveDNS(client, "udp")
			return
		}
		upstream, err := b.director.Dial(b.ctx, permit)
		if err != nil {
			return
		}
		defer upstream.Close()
		_ = client.SetDeadline(time.Now().Add(FlowLease))
		relay(b.ctx, client, upstream, "udp")
	}()
	return true
}

// DialGuest is bound to the assigned private descriptor and immutable guest
// addresses. It never dials a host IP, management endpoint or arbitrary sandbox.
func (b *FrameBridge) DialGuest(ctx context.Context, protocol string, port uint16) (net.Conn, error) {
	if port == 0 || (protocol != "tcp" && protocol != "udp") {
		return nil, ErrDenied
	}
	select {
	case <-b.ctx.Done():
		return nil, ErrUnavailable
	default:
	}
	address := b.config.GuestAddresses[0]
	network := ipv4.ProtocolNumber
	if address.Is6() {
		network = ipv6.ProtocolNumber
	}
	target := tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFromSlice(address.AsSlice()), Port: port}
	// Promiscuous forwarding temporarily learns remote destination addresses.
	// Never let an inbound map auto-select one of those as its local source.
	var local tcpip.FullAddress
	for _, gateway := range b.config.GatewayAddresses {
		if gateway.Is4() == address.Is4() {
			local = tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFromSlice(gateway.AsSlice())}
			break
		}
	}
	if local.Addr.Len() == 0 {
		return nil, ErrDenied
	}
	var raw net.Conn
	var err error
	if protocol == "tcp" {
		raw, err = gonet.DialTCPWithBind(ctx, b.stack, local, target, network)
	} else {
		raw, err = gonet.DialUDP(b.stack, &local, &target, network)
	}
	if err != nil {
		return nil, ErrUnavailable
	}
	return b.life.own(raw)
}
