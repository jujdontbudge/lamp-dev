package control

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/JohanSardar/lamp/pkg/api"
	"github.com/hashicorp/raft"
	raftbolt "github.com/hashicorp/raft-boltdb/v2"
)

// FleetConfig is private operator enrollment, never caller-supplied authority.
// Each certificate has its immutable host ID as a DNS SAN. All voters must have
// the same host manifest and owner enrollment. No relay or extra voter exists.
type FleetConfig struct {
	PublicAPIListen string      `json:"public_api_listen,omitempty"`
	APIListen       string      `json:"api_listen,omitempty"`
	RaftListen      string      `json:"raft_listen,omitempty"`
	HostID          string      `json:"host_id"`
	Hosts           []FleetHost `json:"hosts"`
	CAFile          string      `json:"ca_file"`
	CertFile        string      `json:"cert_file"`
	KeyFile         string      `json:"key_file"`
	// GuardDir is a separate non-restored private location for journal watermarks.
	GuardDir  string `json:"guard_dir"`
	Bootstrap bool   `json:"bootstrap,omitempty"`
}
type FleetHost struct {
	ID       string       `json:"id"`
	API      string       `json:"api"`
	Raft     string       `json:"raft"`
	OS       string       `json:"os"`
	Arch     string       `json:"arch"`
	Engines  []string     `json:"engines"`
	Capacity api.Capacity `json:"capacity"`
}
type fleetObservation struct {
	ControlDigest      string                 `json:"control_digest"`
	AuthorityEnforced  bool                   `json:"authority_enforced"`
	AuthorityExpiresAt string                 `json:"authority_expires_at"`
	Host               string                 `json:"host"`
	Sandboxes          map[string]api.Sandbox `json:"sandboxes"`
	Execs              map[string]api.Exec    `json:"execs"`
	Sequence           uint64                 `json:"sequence"`
	At                 string                 `json:"at"`
}
type fleetPeer struct {
	host   FleetHost
	client *http.Client
}
type Fleet struct {
	publicServer      *http.Server
	service           *Service
	config            FleetConfig
	local             FleetHost
	peers             map[string]fleetPeer
	fsm               *fleetFSM
	raft              *raft.Raft
	transport         *raft.NetworkTransport
	store             *raftbolt.BoltStore
	server            *http.Server
	listener          net.Listener
	ctx               context.Context
	cancel            context.CancelFunc
	wg                sync.WaitGroup
	mu                sync.Mutex // observations, lease and online status only; never held over I/O
	observations      map[string]fleetObservation
	online            map[string]bool
	active            bool
	deadline          int64
	lastAuthorityTime time.Time
	leaseExpiry       time.Time
	controlDigest     string // protected by service.mu
	projecting        bool   // protected by service.mu
	collectMu         sync.Mutex
	global            sync.Mutex // serialize global HTTP management mutations
}
type tlsStream struct {
	net.Listener
	config    *tls.Config
	names     map[string]string
	advertise net.Addr
}

func (s *tlsStream) Addr() net.Addr { return s.advertise }
func (s *tlsStream) Dial(address raft.ServerAddress, timeout time.Duration) (net.Conn, error) {
	config := s.config.Clone()
	config.ServerName = s.names[string(address)]
	return tls.DialWithDialer(&net.Dialer{Timeout: timeout}, "tcp", string(address), config)
}

func OpenFleet(s *Service, config FleetConfig) (*Fleet, error) {
	if !s.journal.Control.Credentialed {
		return nil, errors.New("fleet requires credentialed owner enrollment")
	}
	if len(config.Hosts) < 1 {
		return nil, errors.New("fleet needs enrolled hosts")
	}
	sort.Slice(config.Hosts, func(i, j int) bool { return config.Hosts[i].ID < config.Hosts[j].ID })
	f := &Fleet{service: s, config: config, peers: map[string]fleetPeer{}, fsm: &fleetFSM{}, observations: map[string]fleetObservation{}, online: map[string]bool{}}
	seen := map[string]bool{}
	names := map[string]string{}
	for _, host := range config.Hosts {
		if len(host.ID) > 20 || strings.Trim(host.ID, "abcdefghijklmnopqrstuvwxyz0123456789_") != "" || !strings.HasPrefix(host.ID, "hs_") || strings.ContainsAny(host.ID, ".~ /\\\n") || seen[host.ID] {
			return nil, errors.New("unique immutable hs_ host IDs required")
		}
		if _, _, err := net.SplitHostPort(host.Raft); err != nil {
			return nil, err
		}
		u, err := url.Parse(host.API)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
			return nil, errors.New("peer API must be an HTTPS origin")
		}
		seen[host.ID] = true
		names[host.Raft] = host.ID
		if host.ID == config.HostID {
			f.local = host
		}
	}
	if f.local.ID == "" {
		return nil, errors.New("local host must be enrolled")
	}
	if config.GuardDir == "" || filepath.Clean(config.GuardDir) == filepath.Clean(s.dir) || strings.HasPrefix(filepath.Clean(config.GuardDir)+"/", filepath.Clean(s.dir)+"/") {
		return nil, errors.New("fleet needs a separate guard_dir outside state backups")
	}
	if err := PrivateDir(config.GuardDir); err != nil {
		return nil, err
	}
	// A stale home journal must never turn a retained key into a fresh allocation.
	if raw, err := os.ReadFile(filepath.Join(config.GuardDir, "watermark.json")); err == nil {
		var mark struct {
			Host     string
			Sequence uint64
		}
		if json.Unmarshal(raw, &mark) != nil || mark.Host != config.HostID || s.journal.Sequence < mark.Sequence {
			return nil, errors.New("home journal rolled back; reconcile or explicitly fence a new authority epoch")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	} else if s.journal.Sequence != 0 {
		return nil, errors.New("existing fleet journal has no independent recovery watermark")
	}
	if len(f.local.Engines) > 0 && s.worker.config.Socket == "/unused-control-only" {
		return nil, errors.New("compute hosts need an enrolled local supervisor")
	}
	cert, err := tls.LoadX509KeyPair(config.CertFile, config.KeyFile)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(config.CAFile)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(raw) {
		return nil, errors.New("invalid fleet CA")
	}
	baseTLS := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, RootCAs: roots, ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert}
	baseTLS.VerifyConnection = func(connection tls.ConnectionState) error {
		if len(connection.PeerCertificates) == 0 {
			return errors.New("host certificate required")
		}
		for _, name := range connection.PeerCertificates[0].DNSNames {
			if seen[name] {
				return nil
			}
		}
		return errors.New("certificate host is not enrolled")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, err
	}
	if err = leaf.VerifyHostname(config.HostID); err != nil {
		return nil, err
	}
	for _, host := range config.Hosts {
		peerTLS := baseTLS.Clone()
		peerTLS.ServerName = host.ID
		f.peers[host.ID] = fleetPeer{host: host, client: &http.Client{Transport: &http.Transport{TLSClientConfig: peerTLS, DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext, TLSHandshakeTimeout: 2 * time.Second}, Timeout: 65 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("fleet redirects forbidden") }}}
	}
	dir := filepath.Join(s.dir, "fleet")
	if err = PrivateDir(dir); err != nil {
		return nil, err
	}
	f.store, err = raftbolt.NewBoltStore(filepath.Join(dir, "raft.db"))
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			f.Close()
		}
	}()
	snapshots, err := raft.NewFileSnapshotStore(dir, 2, os.Stderr)
	if err != nil {
		return nil, err
	}
	raftListen := config.RaftListen
	if raftListen == "" {
		raftListen = f.local.Raft
	}
	listener, err := tls.Listen("tcp", raftListen, baseTLS)
	if err != nil {
		return nil, err
	}
	advertise, err := net.ResolveTCPAddr("tcp", f.local.Raft)
	if err != nil {
		listener.Close()
		return nil, err
	}
	f.transport = raft.NewNetworkTransport(&tlsStream{Listener: listener, config: baseTLS, names: names, advertise: advertise}, 3, 2*time.Second, os.Stderr)
	options := raft.DefaultConfig()
	options.LocalID = raft.ServerID(config.HostID)
	options.LogOutput = os.Stderr
	options.HeartbeatTimeout = 3 * time.Second
	options.ElectionTimeout = 3 * time.Second
	options.LeaderLeaseTimeout = 3 * time.Second
	options.CommitTimeout = 50 * time.Millisecond
	f.raft, err = raft.NewRaft(options, f.fsm, f.store, f.store, snapshots, f.transport)
	if err != nil {
		return nil, err
	}
	if config.Bootstrap {
		exists, e := raft.HasExistingState(f.store, f.store, snapshots)
		if e != nil {
			return nil, e
		}
		if !exists {
			servers := []raft.Server{}
			for _, host := range config.Hosts {
				servers = append(servers, raft.Server{Suffrage: raft.Voter, ID: raft.ServerID(host.ID), Address: raft.ServerAddress(host.Raft)})
			}
			if e = f.raft.BootstrapCluster(raft.Configuration{Servers: servers}).Error(); e != nil {
				return nil, e
			}
		}
	}
	u, _ := url.Parse(f.local.API)
	apiListen := config.APIListen
	if apiListen == "" {
		apiListen = u.Host
	}
	f.listener, err = tls.Listen("tcp", apiListen, baseTLS)
	if err != nil {
		return nil, err
	}
	f.server = &http.Server{Handler: http.HandlerFunc(f.peerHTTP), ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 65 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	if raw, err := os.ReadFile(filepath.Join(s.dir, "fleet-catalog.json")); err == nil {
		_ = json.Unmarshal(raw, &f.observations)
	}
	if f.observations == nil {
		f.observations = map[string]fleetObservation{}
	}
	f.ctx, f.cancel = context.WithCancel(s.context())
	var publicListener net.Listener
	if config.PublicAPIListen != "" {
		publicTLS := baseTLS.Clone()
		publicTLS.ClientAuth = tls.NoClientCert
		publicTLS.VerifyConnection = nil
		listener, err := tls.Listen("tcp", config.PublicAPIListen, publicTLS)
		if err != nil {
			return nil, err
		}
		f.publicServer = &http.Server{Handler: f, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 65 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
		publicListener = listener
	}
	s.mu.Lock()
	s.fleet = f
	s.mu.Unlock()
	if publicListener != nil {
		f.wg.Add(1)
		go func() { defer f.wg.Done(); _ = f.publicServer.Serve(publicListener) }()
	}
	f.wg.Add(2)
	go func() { defer f.wg.Done(); _ = f.server.Serve(f.listener) }()
	go f.maintain()
	ok = true
	return f, nil
}
func (f *Fleet) Close() {
	if f.cancel != nil {
		f.cancel()
	}
	if f.server != nil {
		_ = f.server.Close()
	}
	if f.publicServer != nil {
		_ = f.publicServer.Close()
	}
	f.wg.Wait()
	if f.raft != nil {
		_ = f.raft.Shutdown().Error()
	}
	if f.transport != nil {
		_ = f.transport.Close()
	}
	if f.store != nil {
		_ = f.store.Close()
	}
	for _, peer := range f.peers {
		peer.client.CloseIdleConnections()
	}
}
func (f *Fleet) apply(command fleetCommand) error {
	raw, err := json.Marshal(command)
	if err != nil {
		return err
	}
	future := f.raft.Apply(raw, 2*time.Second)
	if err = future.Error(); err != nil {
		return err
	}
	if response := future.Response(); response != nil {
		return response.(error)
	}
	return nil
}
func (f *Fleet) leaderID() string { _, id := f.raft.LeaderWithID(); return string(id) }
func (f *Fleet) command(command fleetCommand) error {
	if f.raft.State() == raft.Leader {
		return f.apply(command)
	}
	leader := f.leaderID()
	if leader == "" {
		return errors.New("authority quorum unavailable")
	}
	raw, _ := json.Marshal(command)
	ctx, cancel := context.WithTimeout(f.ctx, 3*time.Second)
	defer cancel()
	reply, err := f.peerRequest(ctx, leader, "POST", "/internal/fleet/commit", raw, nil)
	if err != nil {
		return err
	}
	defer reply.Body.Close()
	if reply.StatusCode == http.StatusConflict {
		return errAuthorityConflict
	}
	if reply.StatusCode != 200 {
		return errors.New("authority quorum unavailable or revision changed")
	}
	return nil
}

// Called with the home service mutex held, before any effect/publication.
func (f *Fleet) commitControl(ids []string) error {
	if f.projecting {
		return nil
	}
	found := false
	for _, id := range ids {
		if id == "$control" {
			found = true
		}
	}
	if !found {
		return nil
	}
	records := map[string]*record{}
	for _, id := range ids {
		if rec := f.service.journal.Operations[id]; rec != nil {
			records[id] = rec
		}
	}
	command := fleetCommand{Expected: f.controlDigest, Control: f.service.journal.Control, Records: records}
	if err := f.command(command); err != nil {
		return err
	}
	f.controlDigest = digest(command.Control)
	return nil
}
func (f *Fleet) watermark(sequence uint64) error {
	path := filepath.Join(f.config.GuardDir, "watermark.json")
	raw, _ := json.Marshal(struct {
		Host     string
		Sequence uint64
	}{f.config.HostID, sequence})
	file, err := os.OpenFile(path+".new", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = file.Write(raw)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(path+".new", path)
	}
	if err == nil {
		err = syncDirectory(f.config.GuardDir)
	}
	return err
}
func (f *Fleet) project() error {
	state := f.fsm.snapshot()
	if state.Control == nil {
		return errors.New("fleet authority not initialized")
	}
	if state.Configuration != digest(f.config.Hosts) {
		return errors.New("enrolled host configuration differs from committed fleet")
	}
	s := f.service
	s.readLock()
	defer s.mu.Unlock()
	state = f.fsm.snapshot()
	if s.broken {
		return errors.New("home journal unavailable")
	}
	hash := digest(state.Control)
	if hash == f.controlDigest {
		return nil
	}
	s.journal.Control = state.Control
	f.controlDigest = hash
	ids := []string{"$control"}
	for id, rec := range state.Records {
		if old := s.journal.Operations[id]; old != nil && old.Operation.Revision > rec.Operation.Revision {
			continue
		}
		s.journal.Operations[id] = rec
		s.keys[rec.Key] = id
		s.durableOps[id] = rec
		if s.done[id] == nil {
			done := make(chan struct{})
			close(done)
			s.done[id] = done
		}
		ids = append(ids, id)
	}
	f.projecting = true
	err := s.save(ids...)
	f.projecting = false
	if err != nil {
		s.broken = true
	}
	return err
}
func (f *Fleet) maintain() {
	defer f.wg.Done()
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for {
		if f.raft.State() == raft.Leader && f.fsm.snapshot().Control == nil {
			s := f.service
			s.mu.Lock()
			raw, _ := json.Marshal(s.journal.Control)
			s.mu.Unlock()
			var control controlState
			_ = json.Unmarshal(raw, &control)
			_ = f.apply(fleetCommand{Genesis: &fleetState{Control: &control, Records: map[string]*record{}, Homes: map[string]string{}, Epoch: 1, ExpiresAt: time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339Nano), Configuration: digest(f.config.Hosts)}})
		}
		if state := f.fsm.snapshot(); state.Control != nil {
			expires, _ := time.Parse(time.RFC3339Nano, state.ExpiresAt)
			f.mu.Lock()
			active := f.active
			oldExpiry := f.leaseExpiry
			f.mu.Unlock()
			if active && expires != oldExpiry {
				f.activate(expires)
			}
			if f.raft.State() == raft.Leader && active && expires.Sub(f.authorityNow()) < 12*time.Hour {
				_ = f.apply(fleetCommand{RenewExpiresAt: f.authorityNow().Add(24 * time.Hour).UTC().Format(time.RFC3339Nano)})
			}
			if err := f.project(); err == nil {
				f.mu.Lock()
				active := f.active
				f.mu.Unlock()
				if !active {
					// Restarted services need a fresh quorum fence before spending any old slice.
					if err := f.command(fleetCommand{}); err == nil {
						expiry, _ := time.Parse(time.RFC3339Nano, f.fsm.snapshot().ExpiresAt)
						f.activate(expiry)
					}
				}
				f.collect()
			}
		}
		select {
		case <-f.ctx.Done():
			return
		case <-timer.C:
		}
	}
}
func (f *Fleet) peerRequest(ctx context.Context, host, method, path string, body []byte, headers http.Header) (*http.Response, error) {
	peer, ok := f.peers[host]
	if !ok {
		return nil, errors.New("host not enrolled")
	}
	request, err := http.NewRequestWithContext(ctx, method, peer.host.API+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if headers != nil {
		request.Header = headers.Clone()
	}
	return peer.client.Do(request)
}
func (f *Fleet) peerHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/internal/fleet/commit" && r.Method == "POST" {
		var command fleetCommand
		err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&command)
		// Genesis is only produced locally by the initial elected coordinator.
		if err == nil && command.Genesis == nil {
			err = f.apply(command)
		} else {
			err = errors.New("invalid authority command")
		}
		if err != nil {
			if errors.Is(err, errAuthorityConflict) {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			http.Error(w, "authority unavailable", 503)
			return
		}
		w.WriteHeader(200)
		return
	}
	if r.URL.Path == "/internal/fleet/observe" && r.Method == "GET" {
		s := f.service
		s.readLock()
		observation := fleetObservation{ControlDigest: digest(s.journal.Control), AuthorityEnforced: s.authorityEnforced(), AuthorityExpiresAt: f.fsm.snapshot().ExpiresAt, Host: f.config.HostID, Sandboxes: s.journal.Sandboxes, Execs: s.journal.Execs, Sequence: s.journal.Sequence, At: timestamp()}
		raw, err := json.Marshal(observation)
		s.mu.Unlock()
		if err != nil {
			http.Error(w, "observation unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
		return
	}
	// Peer forwarding retains the caller's bearer. A host certificate alone never
	// grants a public caller owner authority, and callers cannot request another hop.
	if r.Header.Get("X-Lamp-Fleet-Hop") == "1" {
		f.serve(w, r, true)
		return
	}
	writeError(w, newID("req"), "UNAUTHENTICATED", "peer forwarding required", nil)
}
func (f *Fleet) collect() {
	f.collectMu.Lock()
	defer f.collectMu.Unlock()
	var group sync.WaitGroup
	for id := range f.peers {
		group.Add(1)
		go func(id string) {
			defer group.Done()
			var observation fleetObservation
			var err error
			if id == f.config.HostID {
				s := f.service
				s.readLock()
				raw, _ := json.Marshal(fleetObservation{ControlDigest: digest(s.journal.Control), AuthorityEnforced: s.authorityEnforced(), AuthorityExpiresAt: f.fsm.snapshot().ExpiresAt, Host: id, Sandboxes: s.journal.Sandboxes, Execs: s.journal.Execs, Sequence: s.journal.Sequence, At: timestamp()})
				s.mu.Unlock()
				err = json.Unmarshal(raw, &observation)
			} else {
				var response *http.Response
				ctx, cancel := context.WithTimeout(f.ctx, 2*time.Second)
				defer cancel()
				response, err = f.peerRequest(ctx, id, "GET", "/internal/fleet/observe", nil, nil)
				if err == nil {
					if response.StatusCode != 200 {
						err = errors.New("host unavailable")
					} else {
						err = json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&observation)
					}
					_ = response.Body.Close()
				}
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			f.online[id] = err == nil && observation.Host == id
			if f.online[id] {
				f.observations[id] = observation
			}
		}(id)
	}
	group.Wait()
	// Discovery retains last observations across an ingress restart. This cache
	// has no admission authority and cannot promote a missing host's work.
	f.mu.Lock()
	raw, _ := json.Marshal(f.observations)
	f.mu.Unlock()
	_ = os.WriteFile(filepath.Join(f.service.dir, "fleet-catalog.json"), raw, 0600)
}
func (f *Fleet) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if serveDashboard(w, r) {
		return
	}
	if strings.HasPrefix(r.URL.Path, "/internal/accountability/") {
		// The bridge checks the Unix peer itself. Public TLS and mTLS
		// forwarding cannot supply the owner peer context.
		f.service.accountabilityBridge(w, r)
		return
	}
	f.serve(w, r, false)
}
func (f *Fleet) forward(w http.ResponseWriter, r *http.Request, home, code string) {
	f.mu.Lock()
	_, observed := f.observations[home]
	online := f.online[home]
	f.mu.Unlock()
	if observed && !online {
		writeError(w, newID("req"), code, "immutable home "+home+" is unavailable; work was not reassigned", nil)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 128<<10))
	if err != nil {
		writeError(w, newID("req"), "INVALID_ARGUMENT", "request exceeds limit", nil)
		return
	}
	headers := r.Header.Clone()
	headers.Set("X-Lamp-Fleet-Hop", "1")
	// Public hop headers are ignored at the Unix ingress; only mTLS peers may use one.
	response, err := f.peerRequest(r.Context(), home, r.Method, r.URL.RequestURI(), raw, headers)
	if err != nil {
		writeError(w, newID("req"), code, "immutable home "+home+" is unavailable; work was not reassigned", nil)
		return
	}
	defer response.Body.Close()
	for key, values := range response.Header {
		if key != "Connection" && key != "Transfer-Encoding" {
			w.Header()[key] = values
		}
	}
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, response.Body)
}
