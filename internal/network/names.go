package network

import (
	"fmt"
	"net/netip"
	"strings"
)

func hostSelector(name string) bool {
	if len(name) == 0 || len(name) > 253 || name != strings.ToLower(name) {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	_, err := netip.ParseAddr(name)
	return err != nil
}

// Bind a selected provider's DNS answers to an exact transport host rule.
// This grants numeric transport, not HTTP/TLS site identity. Private addresses
// still pass the immutable inventory and private-range checks on every dial.
func (d *Director) bindDNSLocked(name string, ips []netip.Addr) error {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	if _, err := netip.ParseAddr(name); err == nil {
		return nil
	}
	if !hostSelector(name) || len(ips) > 64 {
		return ErrDenied
	}
	for _, layers := range [][]Layer{d.policy.Ceiling, d.policy.Layers} {
		for i := range layers {
			for _, rules := range [][]Rule{layers[i].Allow, layers[i].Deny} {
				for j := range rules {
					r := &rules[j]
					if r.HostPattern != nil && *r.HostPattern == name {
						if r.addresses == nil {
							r.addresses = map[netip.Addr]bool{}
						}
						for _, ip := range ips {
							if len(r.addresses) >= 256 && !r.addresses[ip.Unmap()] {
								return fmt.Errorf("DNS rule address limit: %w", ErrDenied)
							}
							r.addresses[ip.Unmap()] = true
						}
					}
				}
			}
		}
	}
	return nil
}
