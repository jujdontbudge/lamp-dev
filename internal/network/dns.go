package network

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// DNS uses the selected exit/member context. Only checked numeric answers are
// returned; an answer is not a reusable dial authority and every dial rechecks.
func (d *Director) dnsIPs(ctx context.Context, name string, kind dnsmessage.Type) ([]netip.Addr, error) {
	ctx, cancel := context.WithTimeout(ctx, DialTimeout)
	defer cancel()
	stop := context.AfterFunc(d.root, cancel)
	defer stop()
	ips, err := d.provider.Resolve(ctx, strings.TrimSuffix(name, "."))
	if err != nil || len(ips) > 64 {
		return nil, ErrUnavailable
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, ErrUnavailable
	}
	ports := map[uint16]bool{1: true, 53: true, 80: true, 443: true}
	for _, layers := range [][]Layer{d.policy.Ceiling, d.policy.Layers} {
		for _, layer := range layers {
			for _, rules := range [][]Rule{layer.Allow, layer.Deny} {
				for _, rule := range rules {
					for _, port := range rule.Ports {
						ports[port] = true
					}
				}
			}
		}
	}
	for _, service := range d.policy.PrivateServices {
		ports[service.Port] = true
	}
	for port := uint16(1); port != 0; port++ {
		if !ports[port] {
			ports[port] = true
			break
		}
	}
	var result []netip.Addr
	for _, ip := range ips {
		ip = ip.Unmap()
		if (kind == dnsmessage.TypeA && !ip.Is4()) || (kind == dnsmessage.TypeAAAA && !ip.Is6()) {
			continue
		}
		allowed := false
		for port := range ports {
			for _, protocol := range []string{"tcp", "udp"} {
				target := Destination{ip, protocol, port}
				if d.policy.allows(target) {
					allowed = true
					break
				}
			}
			if allowed {
				break
			}
		}
		decision := "allow"
		if !allowed {
			decision = "deny"
		}
		if d.record(Event{Kind: "dns_answer", DNSAddress: &ip, Decision: decision, Reason: "selected_context_answer"}) != nil {
			return nil, ErrUnavailable
		}
		if !allowed {
			return nil, ErrDenied
		}
		result = append(result, ip)
	}
	return result, nil
}
func (b *FrameBridge) isDNS(idAddress netip.Addr, port uint16) bool {
	if port != 53 {
		return false
	}
	for _, a := range b.config.GatewayAddresses {
		if a == idAddress {
			return true
		}
	}
	return false
}
func (b *FrameBridge) dnsAnswer(query []byte) []byte {
	var message dnsmessage.Message
	if len(query) > 4096 || message.Unpack(query) != nil || message.Header.Response || message.Header.OpCode != 0 || len(message.Questions) != 1 {
		return nil
	}
	question := message.Questions[0]
	response := dnsmessage.Message{Header: dnsmessage.Header{ID: message.Header.ID, Response: true, RecursionDesired: message.Header.RecursionDesired, RecursionAvailable: true}, Questions: message.Questions}
	if question.Class != dnsmessage.ClassINET || (question.Type != dnsmessage.TypeA && question.Type != dnsmessage.TypeAAAA) {
		response.Header.RCode = dnsmessage.RCodeRefused
	} else {
		ips, err := b.director.dnsIPs(b.ctx, question.Name.String(), question.Type)
		if err != nil {
			response.Header.RCode = dnsmessage.RCodeRefused
		} else {
			for _, ip := range ips {
				resource := dnsmessage.Resource{Header: dnsmessage.ResourceHeader{Name: question.Name, Type: question.Type, Class: dnsmessage.ClassINET, TTL: 30}}
				if ip.Is4() {
					resource.Body = &dnsmessage.AResource{A: ip.As4()}
				} else {
					resource.Body = &dnsmessage.AAAAResource{AAAA: ip.As16()}
				}
				response.Answers = append(response.Answers, resource)
			}
		}
	}
	bytes, err := response.Pack()
	if err != nil {
		return nil
	}
	if len(bytes) > 4096 {
		response.Answers = nil
		response.Header.Truncated = true
		bytes, _ = response.Pack()
	}
	return bytes
}
func (b *FrameBridge) serveDNS(conn net.Conn, protocol string) {
	defer conn.Close()
	buffer := make([]byte, 4096)
	for {
		// Bound an idle DNS conversation, not its total active lifetime.
		if conn.SetReadDeadline(time.Now().Add(UDPIdleTimeout)) != nil {
			return
		}
		var data []byte
		if protocol == "tcp" {
			var size [2]byte
			if _, err := io.ReadFull(conn, size[:]); err != nil {
				return
			}
			n := int(binary.BigEndian.Uint16(size[:]))
			if n > len(buffer) {
				return
			}
			if _, err := io.ReadFull(conn, buffer[:n]); err != nil {
				return
			}
			data = buffer[:n]
		} else {
			n, err := conn.Read(buffer)
			if err != nil {
				return
			}
			data = buffer[:n]
		}
		answer := b.dnsAnswer(data)
		if len(answer) == 0 {
			return
		}
		if conn.SetWriteDeadline(time.Now().Add(DialTimeout)) != nil {
			return
		}
		if protocol == "tcp" {
			var size [2]byte
			binary.BigEndian.PutUint16(size[:], uint16(len(answer)))
			if writeAll(conn, size[:]) != nil || writeAll(conn, answer) != nil {
				return
			}
		} else {
			if _, err := conn.Write(answer); err != nil {
				return
			}
		}
	}
}
