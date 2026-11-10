package network

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"
)

// Provider is host-side only. NumericDial cannot resolve a name or retry an
// alternate IP. A remote operator is trusted to honor the checked numeric dial.
// Provider death must return an error, never switch to an ambient direct route.
type Provider interface {
	Resolve(context.Context, string) ([]netip.Addr, error)
	NumericDial(context.Context, Destination) (net.Conn, error)
	Close() error
}

const MaxFlows = 4096
const DialTimeout = 2 * time.Second
const FlowLease = 30 * time.Second

// Permit cannot be constructed by guest/plugin callers. It is bound to one
// host-assigned subject, director, revision, numeric destination and deadline.
type Permit struct {
	owner       *Director
	revision    uint64
	destination Destination
	until       time.Time
	link        *SandboxLink
}

func (p Permit) Destination() Destination { return p.destination }

type Director struct {
	mu            sync.Mutex
	subject       Subject
	revision      uint64
	policy        Policy
	provider      Provider
	audit         Audit
	closed        bool
	done          chan struct{}
	root          context.Context
	cancel        context.CancelFunc
	auditGate     sync.Mutex
	auditRevision atomic.Uint64
	lastAudit     atomic.Pointer[time.Time]
	unsupported   atomic.Uint64
	active        map[*flow]struct{}
	links         map[Destination]*SandboxLink
}

func NewDirector(subject Subject, revision uint64, policy Policy, provider Provider, audit Audit) (*Director, error) {
	if !validSubject(subject) || revision == 0 || provider == nil || audit == nil {
		return nil, ErrDenied
	}
	if scoped, ok := provider.(interface{ Subject() Subject }); ok && scoped.Subject() != subject {
		return nil, ErrDenied
	}
	compiled, err := policy.compile()
	if err != nil {
		return nil, err
	}
	root, cancel := context.WithCancel(context.Background())
	d := &Director{subject: subject, revision: revision, policy: compiled, provider: provider, audit: audit, active: make(map[*flow]struct{}), links: make(map[Destination]*SandboxLink), done: make(chan struct{}), root: root, cancel: cancel}
	d.auditRevision.Store(revision)
	if err := d.record(Event{Kind: "route_ready", Decision: "allow", PolicySHA256: compiled.digest()}); err != nil {
		return nil, err
	}
	go d.auditLoop()
	go d.auditWatch()
	return d, nil
}
func (d *Director) Authorize(subject Subject, target Destination) (Permit, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	p, err := d.authorize(subject, target)
	if err != nil {
		target = target.canonical()
		var dest *Destination
		if target.valid() {
			dest = &target
		}
		if e := d.record(Event{Kind: "destination", Destination: dest, Decision: "deny", Reason: "policy_or_subject"}); e != nil {
			return Permit{}, e
		}
	}
	return p, err
}
func (d *Director) authorize(subject Subject, target Destination) (Permit, error) {
	target = target.canonical()
	if d.closed || d.root.Err() != nil {
		return Permit{}, ErrUnavailable
	}
	if subject != d.subject || !d.policy.allows(target) {
		return Permit{}, ErrDenied
	}
	until := time.Now().Add(FlowLease)
	link := d.links[target]
	if link != nil {
		if link.ctx.Err() != nil || !link.life.alive() {
			return Permit{}, ErrDenied
		}
		if link.until.Before(until) {
			until = link.until
		}
	}
	return Permit{owner: d, revision: d.revision, destination: target, until: until, link: link}, nil
}

// Resolve rejects the entire answer set if any candidate is disallowed. All
// returned permits name immutable numeric targets; providers never see name.
func (d *Director) Resolve(ctx context.Context, subject Subject, name, protocol string, port uint16) ([]Permit, error) {
	if name == "" || len(name) > 253 || port == 0 || (protocol != "tcp" && protocol != "udp") {
		return nil, ErrDenied
	}
	d.mu.Lock()
	if d.closed || subject != d.subject {
		d.mu.Unlock()
		return nil, ErrDenied
	}
	d.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, DialTimeout)
	defer cancel()
	stop := context.AfterFunc(d.root, cancel)
	defer stop()
	ips, err := d.provider.Resolve(ctx, name)
	if err != nil {
		return nil, ErrUnavailable
	}
	if len(ips) == 0 || len(ips) > 64 {
		return nil, ErrDenied
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	permits := make([]Permit, 0, len(ips))
	for _, ip := range ips {
		target := Destination{Address: ip, Protocol: protocol, Port: port}.canonical()
		permit, err := d.authorize(subject, target)
		var dest *Destination
		if target.valid() {
			dest = &target
		}
		decision := "allow"
		if err != nil {
			decision = "deny"
		}
		if e := d.record(Event{Kind: "dns_answer", Destination: dest, Decision: decision, Reason: "numeric_answer_check"}); e != nil {
			return nil, e
		}
		if err != nil {
			return nil, err
		}
		permits = append(permits, permit)
	}
	return permits, nil
}

// Dial serializes admission with revision changes. Bounded provider dials finish
// (or cancel) and register before a new revision can ACK; no in-flight old dial
// may start emitting after that ACK. Narrowing closes *all* old admissions.
func (d *Director) Dial(ctx context.Context, p Permit) (net.Conn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, ErrUnavailable
	}
	if p.owner != d || p.revision != d.revision || p.link != d.links[p.destination] || !time.Now().Before(p.until) || !d.policy.allows(p.destination) {
		return nil, ErrDenied
	}
	if len(d.active) >= MaxFlows {
		return nil, ErrUnavailable
	}
	if err := d.record(Event{Kind: "dial", Destination: &p.destination, Decision: "allow", Reason: "numeric_grant"}); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, DialTimeout)
	defer cancel()
	stop := context.AfterFunc(d.root, cancel)
	defer stop()
	var raw net.Conn
	var err error
	if p.link != nil {
		raw, err = p.link.dial(ctx, p.destination)
	} else {
		raw, err = d.provider.NumericDial(ctx, p.destination)
	}
	if err != nil || raw == nil {
		return nil, ErrUnavailable
	}
	if ctx.Err() != nil || !time.Now().Before(p.until) {
		raw.Close()
		return nil, ErrDenied
	}
	if err := raw.SetDeadline(p.until); err != nil {
		raw.Close()
		return nil, ErrUnavailable
	}
	f := &flow{Conn: raw, owner: d, destination: p.destination}
	d.active[f] = struct{}{}
	f.timer = time.AfterFunc(time.Until(p.until), func() {
		_ = f.Conn.Close()
		d.mu.Lock()
		delete(d.active, f)
		d.mu.Unlock()
	})
	return f, nil
}

// Update may change sandbox layers only; parent/CI/host ceilings, identity,
// selected provider and exact private-service scope remain immutable.
func (d *Director) Update(expected, revision uint64, layers []Layer) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return ErrUnavailable
	}
	if expected != d.revision || expected == ^uint64(0) || revision != expected+1 {
		return ErrRevision
	}
	next := d.policy
	next.Layers = layers
	compiled, err := next.compile()
	if err != nil {
		return err
	}
	for f := range d.active {
		_ = f.Conn.Close()
		f.timer.Stop()
		delete(d.active, f)
	}
	if err := d.record(Event{Kind: "policy_fence", Revision: revision, Decision: "allow", Reason: "retire_all", PolicySHA256: compiled.digest()}); err != nil {
		return err
	}
	d.policy = compiled
	d.revision = revision
	return nil
}

// Caller holds mu. A failed audit stops every existing and future path.
func (d *Director) record(event Event) error {
	event.Subject = d.subject
	if event.Revision == 0 {
		event.Revision = d.revision
	}
	d.auditGate.Lock()
	previous := d.lastAudit.Load()
	started := time.Now()
	err := d.audit.Record(event)
	if time.Since(started) > time.Second || (previous != nil && time.Since(*previous) > time.Second) {
		err = ErrUnavailable
	}
	if err == nil {
		now := time.Now()
		d.lastAudit.Store(&now)
		if event.Kind == "policy_fence" {
			d.auditRevision.Store(event.Revision)
		}
	}
	d.auditGate.Unlock()
	if err != nil {
		d.cancel()
		if !d.closed {
			d.closed = true
			close(d.done)
		}
		for f := range d.active {
			_ = f.Conn.Close()
			f.timer.Stop()
			delete(d.active, f)
		}
		_ = d.provider.Close()
		return ErrUnavailable
	}
	return nil
}
func (d *Director) Done() <-chan struct{} { return d.root.Done() }
func (d *Director) AuditAge() time.Duration {
	last := d.lastAudit.Load()
	if last == nil {
		return time.Hour
	}
	return time.Since(*last)
}

// Heartbeat does not wait behind a pending numeric dial. The independent
// bridge watchdog can disconnect its frame FD while a storage barrier blocks.
func (d *Director) Heartbeat(unsupported uint64) error {
	select {
	case <-d.done:
		return ErrUnavailable
	default:
	}
	d.auditGate.Lock()
	previous := d.lastAudit.Load()
	started := time.Now()
	err := d.audit.Record(Event{Kind: "heartbeat", Subject: d.subject, Revision: d.auditRevision.Load(), Decision: "observed", Reason: "bounded_liveness", Packets: unsupported})
	if time.Since(started) > time.Second || (previous != nil && time.Since(*previous) > time.Second) {
		err = ErrUnavailable
	}
	if err == nil {
		now := time.Now()
		d.lastAudit.Store(&now)
	}
	d.auditGate.Unlock()
	if err != nil {
		_ = d.Close()
		return ErrUnavailable
	}
	return nil
}
func (d *Director) auditLoop() {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-d.root.Done():
			return
		case <-ticker.C:
			if d.Heartbeat(d.unsupported.Swap(0)) != nil {
				return
			}
		}
	}
}
func (d *Director) auditWatch() {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-d.root.Done():
			return
		case <-ticker.C:
			if d.AuditAge() > time.Second {
				_ = d.Close()
				return
			}
		}
	}
}
func (d *Director) RejectFrame() { d.unsupported.Add(1) }
func (d *Director) Close() error {
	// Cancel pending dials and close provider sockets before waiting on the
	// admission mutex; failure can never leave a late dial alive after fencing.
	d.cancel()
	providerErr := d.provider.Close()
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	close(d.done)
	var failures []error
	for f := range d.active {
		failures = append(failures, f.Conn.Close())
		f.timer.Stop()
		delete(d.active, f)
	}
	failures = append(failures, providerErr)
	return errors.Join(failures...)
}

type flow struct {
	net.Conn
	destination Destination
	owner       *Director
	timer       *time.Timer
	once        sync.Once
	err         error
}

func (f *flow) CloseWrite() error {
	if conn, ok := f.Conn.(interface{ CloseWrite() error }); ok {
		return conn.CloseWrite()
	}
	return nil
}
func (f *flow) Close() error {
	f.once.Do(func() {
		f.err = f.Conn.Close()
		f.timer.Stop()
		f.owner.mu.Lock()
		delete(f.owner.active, f)
		f.owner.mu.Unlock()
	})
	return f.err
}
