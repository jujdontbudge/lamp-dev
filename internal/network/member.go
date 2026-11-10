package network

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/idna"
	"golang.org/x/sys/unix"
	"tailscale.com/envknob"
	"tailscale.com/tsnet"
)

// Member is a real, isolated tsnet node, never the host daemon masquerading as a
// sandbox. Its state directory and keys are host-only and subject-bound.
// Raw Ethernet, ICMP, subnet advertisements and ambient socket forwarding are
// unsupported. Only explicit TCP/UDP maps register inbound listeners.
type Member struct {
	server         *tsnet.Server
	lifecycle      sync.Mutex
	started        bool
	stateLock      *os.File
	subject        Subject
	life           providerLife
	once           sync.Once
	closeErr       error
	mu             sync.Mutex
	ready          bool
	addresses      []netip.Addr
	done           chan struct{}
	maps           []*MappedPorts
	mapCount       int
	tcpSem, udpSem chan struct{}
}
type memberManifest struct {
	Subject    Subject `json:"subject"`
	Hostname   string  `json:"hostname"`
	ControlURL string  `json:"control_url"`
}

func memberState(dir, hostname, controlURL string, subject Subject) error {
	if !filepath.IsAbs(dir) || !validSubject(subject) || len(hostname) == 0 || len(hostname) > 63 || strings.HasPrefix(hostname, "-") || strings.HasSuffix(hostname, "-") {
		return ErrDenied
	}
	for _, c := range hostname {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return ErrDenied
		}
	}
	parent, err := os.Stat(filepath.Dir(dir))
	if err != nil || !parent.IsDir() || parent.Mode().Perm()&0077 != 0 {
		return ErrDenied
	}
	created := false
	if err := os.Mkdir(dir, 0700); err == nil {
		created = true
	} else if !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return ErrDenied
	}
	marker := filepath.Join(dir, "lamp-member.json")
	expected := memberManifest{subject, hostname, controlURL}
	if created {
		f, err := os.OpenFile(marker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		err = json.NewEncoder(f).Encode(expected)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			return errors.Join(err, closeErr)
		}
		directory, err := os.Open(dir)
		if err != nil {
			return err
		}
		err = directory.Sync()
		closeErr = directory.Close()
		return errors.Join(err, closeErr)
	}
	info, err = os.Lstat(marker)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 4096 {
		return ErrDenied
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		return err
	}
	var actual memberManifest
	if json.Unmarshal(data, &actual) != nil || actual != expected {
		return ErrDenied
	}
	return nil
}
func validSubject(s Subject) bool {
	if s.SandboxID == "" || len(s.SandboxID) > 128 || s.Generation == 0 {
		return false
	}
	for _, c := range s.SandboxID {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// NewMember accepts an optional auth key from host custody, never an argv,
// environment or persisted config. With no key it uses interactive enrollment;
// the authentication URL is delivered only to the supplied private sink.
func NewMember(subject Subject, dir, hostname, authKey string) (*Member, error) {
	return NewMemberWithControl(subject, dir, hostname, authKey, "")
}

// NewMemberWithControl pins the control plane as part of state identity.
// Headscale and SaaS are distinct qualifications. Plain HTTP is accepted only
// on a numeric loopback address for a disposable owned Headscale fixture.
func NewMemberWithControl(subject Subject, dir, hostname, authKey, controlURL string) (*Member, error) {
	if controlURL == "" {
		controlURL = "https://controlplane.tailscale.com"
	}
	control, err := url.Parse(controlURL)
	if err != nil || control.User != nil || control.RawQuery != "" || control.Fragment != "" || control.Hostname() == "" || (control.Path != "" && control.Path != "/") {
		return nil, ErrDenied
	}
	if control.Scheme != "https" {
		address, err := netip.ParseAddr(control.Hostname())
		if control.Scheme != "http" || err != nil || !address.IsLoopback() {
			return nil, ErrDenied
		}
	}
	controlURL = strings.TrimSuffix(control.String(), "/")
	for _, entry := range os.Environ() {
		name, value, _ := strings.Cut(entry, "=")
		if name == "TS_NO_LOGS_NO_SUPPORT" && value == "true" {
			continue
		}
		if strings.HasPrefix(name, "TS_") || strings.HasPrefix(name, "TSNET_") {
			if value != "" {
				return nil, ErrDenied
			}
		}
	}
	if authKey != "" && (len(authKey) < 16 || len(authKey) > 4096 || strings.ContainsAny(authKey, "\r\n\x00")) {
		return nil, ErrDenied
	}
	if err := memberState(dir, hostname, controlURL, subject); err != nil {
		return nil, err
	}
	lockFD, err := unix.Open(filepath.Join(dir, "lamp-member.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, ErrUnavailable
	}
	lock := os.NewFile(uintptr(lockFD), "member-state-lock")
	lockInfo, statErr := lock.Stat()
	if statErr != nil || !lockInfo.Mode().IsRegular() || lockInfo.Mode().Perm()&0077 != 0 {
		lock.Close()
		return nil, ErrDenied
	}
	if unix.Flock(lockFD, unix.LOCK_EX|unix.LOCK_NB) != nil {
		lock.Close()
		return nil, ErrUnavailable
	}
	envknob.SetNoLogsNoSupport()
	discard := func(string, ...any) {}
	s := &tsnet.Server{Dir: dir, Hostname: hostname, AuthKey: authKey, ControlURL: controlURL, Ephemeral: true, RunWebClient: false, Logf: discard, UserLogf: discard}
	return &Member{server: s, subject: subject, done: make(chan struct{}), tcpSem: make(chan struct{}, 64), udpSem: make(chan struct{}, 64), stateLock: lock}, nil
}

// Start waits for real enrollment/approval and reports ready only with actual
// provider-assigned member addresses. authSink must not log the URL. A failed
// approval/timeout never reports a constructed endpoint as working membership.
func (m *Member) Start(ctx context.Context, authSink func(string) error) error {
	m.lifecycle.Lock()
	select {
	case <-m.done:
		m.lifecycle.Unlock()
		return ErrUnavailable
	default:
	}
	if m.started {
		m.lifecycle.Unlock()
		return ErrUnavailable
	}
	m.started = true
	startErr := m.server.Start()
	m.lifecycle.Unlock()
	if startErr != nil {
		return ErrUnavailable
	}
	client, err := m.server.LocalClient()
	if err != nil {
		return ErrUnavailable
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	lastURL := ""
	for {
		status, err := client.StatusWithoutPeers(ctx)
		if err == nil {
			if status.BackendState == "Running" && len(status.TailscaleIPs) > 0 {
				m.mu.Lock()
				select {
				case <-m.done:
					m.mu.Unlock()
					return ErrUnavailable
				default:
				}
				m.addresses = append([]netip.Addr(nil), status.TailscaleIPs...)
				m.ready = true
				m.mu.Unlock()
				go m.watchHealth()
				return nil
			}
			if status.AuthURL != "" && status.AuthURL != lastURL {
				if authSink == nil || authSink(status.AuthURL) != nil {
					return ErrUnavailable
				}
				lastURL = status.AuthURL
			}
		}
		select {
		case <-ctx.Done():
			return ErrUnavailable
		case <-m.done:
			return ErrUnavailable
		case <-ticker.C:
		}
	}
}
func (m *Member) watchHealth() {
	client, err := m.server.LocalClient()
	if err != nil {
		_ = m.Close()
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.done:
			return
		case <-ticker.C:
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		status, err := client.StatusWithoutPeers(ctx)
		cancel()
		if err != nil || status.BackendState != "Running" {
			_ = m.Close()
			return
		}
	}
}

type PeerIdentity struct {
	Address netip.Addr `json:"address"`
	NodeID  string     `json:"node_id"`
	Name    string     `json:"name"`
}

func (m *Member) WhoIs(ctx context.Context, protocol, remote string) (PeerIdentity, error) {
	if !m.available() || (protocol != "tcp" && protocol != "udp") {
		return PeerIdentity{}, ErrUnavailable
	}
	ap, err := netip.ParseAddrPort(remote)
	if err != nil || ap.Port() == 0 {
		return PeerIdentity{}, ErrDenied
	}
	client, err := m.server.LocalClient()
	if err != nil {
		return PeerIdentity{}, ErrUnavailable
	}
	who, err := client.WhoIsProto(ctx, protocol, ap.String())
	if err != nil || who.Node == nil || who.Node.ID == 0 {
		return PeerIdentity{}, ErrDenied
	}
	matches := false
	for _, prefix := range who.Node.Addresses {
		matches = matches || prefix.Contains(ap.Addr().Unmap())
	}
	if !matches {
		return PeerIdentity{}, ErrDenied
	}
	return PeerIdentity{Address: ap.Addr().Unmap(), NodeID: strconv.FormatInt(int64(who.Node.ID), 10), Name: strings.TrimSuffix(who.Node.Name, ".")}, nil
}
func (m *Member) Subject() Subject { return m.subject }
func (m *Member) Addresses() []netip.Addr {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]netip.Addr(nil), m.addresses...)
}
func (m *Member) available() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.ready && m.life.alive() }
func (m *Member) Resolve(ctx context.Context, name string) ([]netip.Addr, error) {
	if !m.available() {
		return nil, ErrUnavailable
	}
	if ip, err := netip.ParseAddr(name); err == nil {
		return []netip.Addr{ip.Unmap()}, nil
	}
	name, err := idna.Lookup.ToASCII(strings.TrimSuffix(name, "."))
	if err != nil || len(name) == 0 || len(name) > 253 {
		return nil, ErrDenied
	}
	client, err := m.server.LocalClient()
	if err != nil {
		return nil, ErrUnavailable
	}
	var ips []netip.Addr
	for _, kind := range []string{"A", "AAAA"} {
		answer, _, err := client.QueryDNS(ctx, name, kind)
		if err != nil {
			return nil, ErrUnavailable
		}
		var parser dnsmessage.Parser
		header, err := parser.Start(answer)
		if err != nil || !header.Response || header.Truncated {
			return nil, ErrUnavailable
		}
		if header.RCode != dnsmessage.RCodeSuccess {
			return nil, ErrUnavailable
		}
		if err := parser.SkipAllQuestions(); err != nil {
			return nil, ErrUnavailable
		}
		records, err := parser.AllAnswers()
		if err != nil || len(records) > 64 {
			return nil, ErrUnavailable
		}
		for _, record := range records {
			switch body := record.Body.(type) {
			case *dnsmessage.AResource:
				ips = append(ips, netip.AddrFrom4(body.A))
			case *dnsmessage.AAAAResource:
				ips = append(ips, netip.AddrFrom16(body.AAAA).Unmap())
			}
		}
	}
	if len(ips) == 0 || len(ips) > 64 {
		return nil, ErrUnavailable
	}
	return ips, nil
}

// ListenTCP exposes only this member's explicit service, not a host socket or
// subnet listener. TLS/authentication remain the ingress handler's responsibility.
func (m *Member) ListenTCP(port uint16) (net.Listener, error) {
	if port == 0 || !m.available() {
		return nil, ErrDenied
	}
	listener, err := m.server.Listen("tcp", ":"+strconv.Itoa(int(port)))
	if err != nil {
		return nil, ErrUnavailable
	}
	return listener, nil
}
func (m *Member) NumericDial(ctx context.Context, d Destination) (net.Conn, error) {
	d = d.canonical()
	// Endpoint membership is not an internet/subnet router. Reject rather than
	// let tsnet's general dialer choose a host socket for a non-tailnet target.
	if !m.available() || !d.valid() || !contains(prefixes("100.64.0.0/10", "fd7a:115c:a1e0::/48"), d.Address) {
		return nil, ErrDenied
	}
	for _, own := range m.Addresses() {
		if own == d.Address {
			return nil, ErrDenied
		}
	}
	raw, err := m.server.Dial(ctx, d.Protocol, d.AddrPort().String())
	if err != nil {
		return nil, ErrUnavailable
	}
	return m.life.own(raw)
}
func (m *Member) Close() error {
	m.once.Do(func() {
		close(m.done)
		m.mu.Lock()
		m.ready = false
		maps := append([]*MappedPorts(nil), m.maps...)
		m.mu.Unlock()
		for _, ports := range maps {
			ports.Close()
		}
		_ = m.life.close()
		m.lifecycle.Lock()
		if m.started {
			m.closeErr = m.server.Close()
		}
		m.lifecycle.Unlock()
		m.closeErr = errors.Join(m.closeErr, m.stateLock.Close())
	})
	return m.closeErr
}
