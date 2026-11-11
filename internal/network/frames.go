package network

import (
	"encoding/binary"
	"net/netip"
	"slices"
)

// frameClass bounds parsing before gVisor and rejects fragments, options,
// encapsulation, VLANs and IPv6 extension headers rather than bypassing policy.
// ARP/NDP are confined to the private engine segment's assigned neighbours.
func frameClass(frame []byte, sources, gateways []netip.Addr) (data bool, ok bool) {
	if len(frame) < 14 {
		return false, false
	}
	ether := binary.BigEndian.Uint16(frame[12:14])
	packet := frame[14:]
	isSource := func(a netip.Addr) bool { return slices.Contains(sources, a) }
	assigned := func(a netip.Addr) bool { return slices.Contains(sources, a) || slices.Contains(gateways, a) }
	if ether == 0x0806 {
		if len(packet) < 28 || binary.BigEndian.Uint16(packet[:2]) != 1 || binary.BigEndian.Uint16(packet[2:4]) != 0x0800 || packet[4] != 6 || packet[5] != 4 {
			return false, false
		}
		op := binary.BigEndian.Uint16(packet[6:8])
		if op != 1 && op != 2 {
			return false, false
		}
		sender := netip.AddrFrom4([4]byte(packet[14:18]))
		target := netip.AddrFrom4([4]byte(packet[24:28]))
		return false, (sender.IsUnspecified() || isSource(sender)) && assigned(target)
	}
	var source netip.Addr
	var offset, total, protocol int
	switch ether {
	case 0x0800:
		if len(packet) < 20 || packet[0] != 0x45 || binary.BigEndian.Uint16(packet[6:8])&0x3fff != 0 {
			return false, false
		}
		total = int(binary.BigEndian.Uint16(packet[2:4]))
		offset = 20
		protocol = int(packet[9])
		source = netip.AddrFrom4([4]byte(packet[12:16]))
	case 0x86dd:
		if len(packet) < 40 || packet[0]>>4 != 6 {
			return false, false
		}
		total = 40 + int(binary.BigEndian.Uint16(packet[4:6]))
		offset = 40
		protocol = int(packet[6])
		source = netip.AddrFrom16([16]byte(packet[8:24]))
		destination := netip.AddrFrom16([16]byte(packet[24:40]))
		if source.Is4In6() || destination.Is4In6() {
			return false, false
		}
		if protocol == 58 {
			if total < 64 || total > len(packet) || packet[7] != 255 || packet[41] != 0 || (packet[40] != 135 && packet[40] != 136) {
				return false, false
			}
			target := netip.AddrFrom16([16]byte(packet[48:64]))
			return false, (isSource(source) || source.IsLinkLocalUnicast() || source.IsUnspecified()) && assigned(target)
		}
	default:
		return false, false
	}
	if !isSource(source) || total > len(packet) || total < offset {
		return false, false
	}
	// Ethernet minimum-frame padding is not an IP payload or covert extra packet.
	if total < len(packet) {
		if len(frame) > 60 {
			return false, false
		}
		for _, b := range packet[total:] {
			if b != 0 {
				return false, false
			}
		}
	}
	switch protocol {
	case 6:
		if total < offset+20 {
			return false, false
		}
		end := offset + int(packet[offset+12]>>4)*4
		if end < offset+20 || end > total {
			return false, false
		}
	case 17:
		if total < offset+8 || int(binary.BigEndian.Uint16(packet[offset+4:offset+6])) != total-offset {
			return false, false
		}
	default:
		return false, false
	}
	return true, true
}
