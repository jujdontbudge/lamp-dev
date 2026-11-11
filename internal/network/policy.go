// Package network owns numeric dial authority on the host. A provider receives
// only an already checked IP/transport/port, never a name to resolve again.
package network

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"slices"
)

var ErrDenied = errors.New("network destination denied")
var ErrRevision = errors.New("network revision conflict")
var ErrUnavailable = errors.New("selected network route unavailable")

// Subject is assigned by the engine handle, not a guest packet or request.
type Subject struct {
	SandboxID  string `json:"sandbox_id"`
	Generation uint64 `json:"generation"`
}
type Destination struct {
	Address  netip.Addr `json:"address"`
	Protocol string     `json:"protocol"`
	Port     uint16     `json:"port"`
}

func (d Destination) canonical() Destination { d.Address = d.Address.Unmap(); return d }
func (d Destination) valid() bool {
	return d.Address.IsValid() && d.Address.Zone() == "" && d.Port != 0 && (d.Protocol == "tcp" || d.Protocol == "udp")
}
func (d Destination) AddrPort() netip.AddrPort { return netip.AddrPortFrom(d.Address, d.Port) }

// Rule uses the public NetworkRule vocabulary. This transport compiler refuses
// host/site selectors instead of pretending that IP rules inspect TLS sites.
type Rule struct {
	CIDR        string   `json:"cidr"`
	HostPattern *string  `json:"host_pattern,omitempty"`
	Protocol    string   `json:"protocol"`
	Ports       []uint16 `json:"ports"`
	prefix      netip.Prefix
}
type Layer struct {
	FilterMode    string `json:"filter_mode"`
	DefaultEgress string `json:"default_egress"`
	Allow         []Rule `json:"allow"`
	Deny          []Rule `json:"deny"`
}

// Policy is copied and compiled at the host boundary. Ceilings, host identities
// and public-PR private exclusions cannot be changed by a sandbox revision.
type Policy struct {
	Ceiling          []Layer        `json:"policy_ceiling"`
	Layers           []Layer        `json:"policy_layers"`
	HostAddresses    []netip.Addr   `json:"host_addresses"`
	LANPrefixes      []netip.Prefix `json:"lan_prefixes"`
	SafetyDenies     []netip.Prefix `json:"safety_denies"`
	PrivateServices  []Destination  `json:"private_services"`
	RequireAllowlist bool           `json:"require_allowlist"`
	PublicPR         bool           `json:"public_pr"`
}

var specialV4 = prefixes("0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4", "168.63.129.16/32")
var specialV6 = prefixes("2001::/23", "2001:db8::/32", "2002::/16", "3fff::/20")

func prefixes(values ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(values))
	for i, s := range values {
		out[i] = netip.MustParsePrefix(s)
	}
	return out
}
func contains(ps []netip.Prefix, a netip.Addr) bool {
	for _, p := range ps {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
func public(a netip.Addr) bool {
	a = a.Unmap()
	if a.Is4() {
		return !contains(specialV4, a)
	}
	return netip.MustParsePrefix("2000::/3").Contains(a) && !contains(specialV6, a)
}
func compileLayers(in []Layer) ([]Layer, error) {
	if len(in) > 8 {
		return nil, ErrDenied
	}
	out := make([]Layer, len(in))
	for i, l := range in {
		if l.FilterMode != "transport" || (l.DefaultEgress != "public" && l.DefaultEgress != "deny") || len(l.Allow)+len(l.Deny) > 256 {
			return nil, ErrDenied
		}
		compile := func(rs []Rule) ([]Rule, error) {
			result := make([]Rule, len(rs))
			for j, r := range rs {
				p, err := netip.ParsePrefix(r.CIDR)
				if err != nil || p.Addr().Is4In6() || p.Addr().Zone() != "" || r.HostPattern != nil || (r.Protocol != "tcp" && r.Protocol != "udp") || len(r.Ports) == 0 || len(r.Ports) > 64 || slices.Contains(r.Ports, uint16(0)) {
					return nil, ErrDenied
				}
				r.prefix = p.Masked()
				r.CIDR = r.prefix.String()
				r.Ports = slices.Clone(r.Ports)
				result[j] = r
			}
			return result, nil
		}
		var err error
		l.Allow, err = compile(l.Allow)
		if err != nil {
			return nil, err
		}
		l.Deny, err = compile(l.Deny)
		if err != nil {
			return nil, err
		}
		out[i] = l
	}
	return out, nil
}
func (p Policy) Validate() error { _, err := p.compile(); return err }
func (p Policy) compile() (Policy, error) {
	var err error
	p.Ceiling, err = compileLayers(p.Ceiling)
	if err != nil {
		return Policy{}, err
	}
	p.Layers, err = compileLayers(p.Layers)
	if err != nil {
		return Policy{}, err
	}
	if p.RequireAllowlist || p.PublicPR {
		deny := false
		for _, l := range append(slices.Clone(p.Ceiling), p.Layers...) {
			deny = deny || l.DefaultEgress == "deny"
		}
		if !deny {
			return Policy{}, ErrDenied
		}
	}
	p.HostAddresses = slices.Clone(p.HostAddresses)
	p.LANPrefixes = slices.Clone(p.LANPrefixes)
	p.SafetyDenies = slices.Clone(p.SafetyDenies)
	p.PrivateServices = slices.Clone(p.PrivateServices)
	if len(p.HostAddresses) == 0 || len(p.HostAddresses) > 4096 || len(p.LANPrefixes) > 4096 || len(p.SafetyDenies) > 4096 || len(p.PrivateServices) > 64 {
		return Policy{}, ErrDenied
	}
	for i, a := range p.HostAddresses {
		if !a.IsValid() || a.Zone() != "" {
			return Policy{}, ErrDenied
		}
		p.HostAddresses[i] = a.Unmap()
	}
	for _, ps := range [][]netip.Prefix{p.LANPrefixes, p.SafetyDenies} {
		for _, n := range ps {
			if !n.IsValid() || n.Addr().Is4In6() {
				return Policy{}, ErrDenied
			}
		}
	}
	for i, d := range p.PrivateServices {
		d = d.canonical()
		if !d.valid() {
			return Policy{}, ErrDenied
		}
		p.PrivateServices[i] = d
	}
	return p, nil
}
func (r Rule) matches(d Destination) bool {
	return r.prefix.Contains(d.Address) && r.Protocol == d.Protocol && slices.Contains(r.Ports, d.Port)
}
func (p Policy) digest() string {
	// Only compiled concrete numeric selectors are present; no provider keys or
	// bearer data are part of this digest or its persisted inputs.
	data, _ := json.Marshal(struct {
		Policy Policy
		V4, V6 []netip.Prefix
	}{p, specialV4, specialV6})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func (p Policy) allows(d Destination) bool {
	d = d.canonical()
	if !d.valid() || slices.Contains(p.HostAddresses, d.Address) || contains(p.SafetyDenies, d.Address) {
		return false
	}
	// No private-service grant can expose loopback, link-local/metadata,
	// multicast, unspecified addresses or the provider metadata VIP.
	if !d.Address.IsGlobalUnicast() || d.Address.IsLoopback() || d.Address.IsLinkLocalUnicast() || d.Address == netip.MustParseAddr("168.63.129.16") {
		return false
	}
	isPublic := public(d.Address) && !contains(p.LANPrefixes, d.Address)
	if !isPublic && (p.PublicPR || !slices.Contains(p.PrivateServices, d)) {
		return false
	}
	for _, layers := range [][]Layer{p.Ceiling, p.Layers} {
		for _, l := range layers {
			for _, r := range l.Deny {
				if r.matches(d) {
					return false
				}
			}
			if l.DefaultEgress == "deny" {
				match := false
				for _, r := range l.Allow {
					match = match || r.matches(d)
				}
				if !match {
					return false
				}
			}
		}
	}
	return true
}

// Inventory includes every local address and connected prefix, including
// publicly numbered LANs and IPv6. Rebuild/fence on host topology changes.
func Inventory() (addresses []netip.Addr, prefixes []netip.Prefix, err error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, nil, err
	}
	for _, iface := range interfaces {
		values, e := iface.Addrs()
		if e != nil {
			return nil, nil, e
		}
		for _, value := range values {
			p, e := netip.ParsePrefix(value.String())
			if e != nil {
				return nil, nil, e
			}
			addresses = append(addresses, p.Addr().Unmap())
			prefixes = append(prefixes, p.Masked())
		}
	}
	if len(addresses) == 0 {
		return nil, nil, ErrUnavailable
	}
	return
}
