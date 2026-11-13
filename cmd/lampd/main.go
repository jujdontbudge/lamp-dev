// lampd serves the local control API and optional mutually authenticated fleet.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"syscall"
	"time"

	"lamp.eilmer.si/internal/buildversion"
	"lamp.eilmer.si/internal/ci"
	"lamp.eilmer.si/internal/control"
	"lamp.eilmer.si/internal/localconfig"
	"lamp.eilmer.si/internal/relayclient"
	kitruntime "lamp.eilmer.si/kits"
)

func main() {
	if err := run(); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, "lampd:", err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) == 2 && (os.Args[1] == "version" || os.Args[1] == "--version") {
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"version": buildversion.Current(), "api_version": "v1"})
	}
	flags := flag.NewFlagSet("lampd", flag.ContinueOnError)
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Usage: lampd [flags]\nServe the credentialed Lamp HTTP API on a private Unix socket.\nEnroll a supervisor for VM execution; native sessions require --sessions-config.\nExample: lampd --socket /tmp/lamp/api.sock --state-dir /tmp/lamp/control\nUse lamp --help to create and manage workspaces.\nOptions:")
		flags.PrintDefaults()
	}
	auditReservation := flags.Uint64("audit-reservation-bytes", 24<<20, "trusted per-sandbox retained audit reservation, matching the supervisor policy")
	approvalFile := flags.String("approval-actions-config", "", "private owner-enrolled exact webhook actions for one-use approvals")
	ciFile := flags.String("ci-config", "", "private host-enrolled CI policy JSON (GitLab and other shipped systems)")
	auditReader := flags.String("audit-reader", "", "trusted absolute executable reading guard audit for sandbox ID and generation")
	quotaFile := flags.String("quota-file", "", "host admission quota JSON")
	relayFile := flags.String("relay-config", "", "private outbound relay carrier configuration JSON")
	fleetFile := flags.String("fleet-config", "", "private enrolled fleet configuration JSON")
	identityFile := flags.String("identity-file", "", "owner enrollment JSON (owner_id, bootstrap_sha256); bearer is never stored")
	sessionsFile := flags.String("sessions-config", "", "owner-enrolled harnesses and native session runner locations")
	nativeFile := flags.String("native-fallback-file", "", "trusted local native runtime and immutable job credential bindings")
	dashboardListen := flags.String("dashboard-listen", "", "also serve the dashboard and bearer-authenticated API on this TCP address (for example 127.0.0.1:7480)")
	notifyFile := flags.String("notifications-config", "", "private notification destinations and dashboard public URL (OPS-003)")
	kitsConfig := flags.String("kits-config", "", "private kits/vault configuration; start the bundled headless backend")
	kitsSocket := flags.String("kits-socket", "", "enrolled owner-only kits Unix socket (instead of a managed backend)")
	recoveryPublic := flags.String("recovery-public-key", "", "owner's offline data-recovery public key; every backup is also wrapped to it")
	recoveryKey := flags.String("recovery-key-file", "", "replacement host only: owner's recovery key file, read once to import sealed archives")
	recoveryImport := flags.String("recovery-import-dir", "", "directory of sealed .lbk archives to import with --recovery-key-file")
	installDir := flags.String("install-dir", "", "lamp-installer prefix: release mirror, pinned release key and updater agent files")
	profileDir := flags.String("profile-dir", "", "private CPU/mutex/block profiles for a diagnostic run")
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	base := filepath.Join(home, ".local", "lamp", "state")
	socket := flags.String("socket", localconfig.Socket(), "private local HTTP Unix socket")
	state := flags.String("state-dir", filepath.Join(base, "control"), "private operation journal directory")
	exports := flags.String("exports-dir", "", "private broker-published clean reconstruction bundles")
	readers := map[uint32]string{}
	flags.Func("share-reader", "read-only local UID:principal_id (repeatable)", func(value string) error {
		parts := strings.Split(value, ":")
		if len(parts) != 2 {
			return fmt.Errorf("share reader must be UID:principal_id")
		}
		uid, err := strconv.ParseUint(parts[0], 10, 31)
		if err != nil {
			return fmt.Errorf("share reader UID must be numeric")
		}
		if _, exists := readers[uint32(uid)]; exists {
			return fmt.Errorf("duplicate share reader UID")
		}
		readers[uint32(uid)] = parts[1]
		return nil
	})
	workerSocket := flags.String("worker-socket", "", "enrolled supervisor Unix socket")
	workerID := flags.String("worker-id", "", "assigned supervisor identity")
	bootID := flags.String("worker-boot-id", "", "enrolled supervisor boot identity")
	uid := flags.Int("worker-uid", -1, "assigned supervisor OS UID (required)")
	pid := flags.Int("worker-pid", 0, "legacy supervisor PID hint (reconnection authenticates UID, worker identity and host boot)")
	grant := flags.String("grant-id", "", "supervisor-installed control grant ID")
	timeout := flags.Duration("worker-timeout", 30*time.Second, "absolute operation deadline (max 1m)")
	auditRelease := flags.String("audit-release", "", "JSON argv of the trusted guard-spool release command (given capture|remove and a sandbox ID)")
	stateAdmin := flags.String("state-admin", "", "JSON argv of the supervisor's root-only state administration, used to discard saved state")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	if (*recoveryKey == "") != (*recoveryImport == "") {
		return fmt.Errorf("--recovery-key-file and --recovery-import-dir go together")
	}
	var admin []string
	if *stateAdmin != "" {
		if err := json.Unmarshal([]byte(*stateAdmin), &admin); err != nil || len(admin) == 0 {
			return fmt.Errorf("--state-admin must be a JSON argv")
		}
	}
	var release []string
	if *auditRelease != "" {
		if err := json.Unmarshal([]byte(*auditRelease), &release); err != nil || len(release) == 0 {
			return fmt.Errorf("--audit-release must be a JSON argv")
		}
	}
	if len(release) == 0 && *workerSocket != "" {
		if runtime.GOOS == "darwin" {
			release = []string{"/usr/bin/true"}
		} else {
			return fmt.Errorf("--audit-release is required with --worker-socket: configure tools/guard-audit-release.py for this supervisor's audit directory so deleted sandboxes release capacity")
		}
	}
	if *profileDir != "" {
		if err := control.PrivateDir(*profileDir); err != nil {
			return err
		}
		cpu, err := os.OpenFile(filepath.Join(*profileDir, "cpu.pprof"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		if err := pprof.StartCPUProfile(cpu); err != nil {
			cpu.Close()
			return err
		}
		runtime.SetMutexProfileFraction(1)
		runtime.SetBlockProfileRate(1)
		defer func() {
			pprof.StopCPUProfile()
			cpu.Close()
			for _, name := range []string{"mutex", "block"} {
				file, err := os.OpenFile(filepath.Join(*profileDir, name+".pprof"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
				if err == nil {
					pprof.Lookup(name).WriteTo(file, 0)
					file.Close()
				}
			}
		}()
	}
	prepareSocketDir := control.PrivateDir
	if len(readers) > 0 {
		prepareSocketDir = control.SharedAPIDir
	}
	if err := prepareSocketDir(filepath.Dir(*socket)); err != nil {
		return err
	}
	var identity *control.IdentityConfig
	if *identityFile != "" {
		data, e := os.ReadFile(*identityFile)
		if e != nil {
			return e
		}
		identity = &control.IdentityConfig{}
		if e = json.Unmarshal(data, identity); e != nil {
			return e
		}
	}
	var fleetConfig *control.FleetConfig
	var nativeConfig *control.NativeFallbackConfig
	if *nativeFile != "" {
		nativeConfig, err = control.ReadNativeFallback(*nativeFile)
		if err != nil {
			return err
		}
	}
	if *fleetFile != "" {
		data, err := os.ReadFile(*fleetFile)
		if err != nil {
			return err
		}
		fleetConfig = &control.FleetConfig{}
		if err := json.Unmarshal(data, fleetConfig); err != nil {
			return err
		}
	}
	var sessions control.SessionConfig
	if *sessionsFile != "" {
		info, err := os.Lstat(*sessionsFile)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 1<<20 {
			return fmt.Errorf("session enrollment requires a private regular configuration file of at most 1 MiB")
		}
		data, err := os.ReadFile(*sessionsFile)
		if err != nil {
			return err
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&sessions); err != nil {
			return err
		}
		if decoder.Decode(new(any)) != io.EOF {
			return fmt.Errorf("expected one session enrollment configuration")
		}
	}
	var quotas control.Quotas
	if *quotaFile != "" {
		raw, e := os.ReadFile(*quotaFile)
		if e != nil {
			return e
		}
		if e = json.Unmarshal(raw, &quotas); e != nil {
			return e
		}
	}
	var ciConfig *ci.Config
	if *ciFile != "" {
		raw, e := os.ReadFile(*ciFile)
		if e != nil {
			return e
		}
		ciConfig = &ci.Config{}
		if e = json.Unmarshal(raw, ciConfig); e != nil {
			return e
		}
	}
	if *kitsConfig != "" {
		if *kitsSocket != "" {
			return fmt.Errorf("choose --kits-config or --kits-socket")
		}
		if err := control.PrivateDir(*state); err != nil {
			return err
		}
		backend, closeBackend, err := kitruntime.Start(context.Background(), *kitsConfig, filepath.Join(*state, "kits"))
		if err != nil {
			return err
		}
		defer closeBackend()
		*kitsSocket = backend
	}
	var notifications *control.NotificationConfig
	if *notifyFile != "" {
		if notifications, err = control.LoadNotificationConfig(*notifyFile); err != nil {
			return err
		}
	}
	var relayConfig *relayclient.Config
	if *relayFile != "" {
		relayConfig, err = relayclient.LoadConfig(*relayFile)
		if err != nil {
			return err
		}
	}
	var protected map[string]control.ProtectedAction
	if *approvalFile != "" {
		protected, err = control.LoadApprovalWebhooks(*approvalFile)
		if err != nil {
			return err
		}
	}
	service, err := control.Open(control.Config{ProtectedActions: protected, Relay: relayConfig, Notifications: notifications, KitsSocket: *kitsSocket, AuditReservationBytes: *auditReservation, CI: ciConfig, Quotas: quotas, AuditReader: *auditReader, Fleet: fleetConfig, Identity: identity, NativeFallback: nativeConfig, StateDir: *state, ExportsDir: *exports, SharedReaders: readers, Sessions: sessions, Worker: control.WorkerConfig{Socket: *workerSocket, WorkerID: *workerID, BootID: *bootID, UID: *uid, PID: *pid, GrantID: *grant, Timeout: *timeout}, StateAdmin: admin, AuditRelease: release, RecoveryPublicKey: *recoveryPublic, RecoveryKeyFile: *recoveryKey, RecoveryImportDir: *recoveryImport, InstallDir: *installDir})
	if err != nil {
		return err
	}
	defer service.Close()
	var handler http.Handler = service
	if fleetConfig != nil {
		fleet, err := control.OpenFleet(service, *fleetConfig)
		if err != nil {
			return err
		}
		defer fleet.Close()
		handler = fleet
	}
	// Open holds the state lock. Only ECONNREFUSED proves an old socket stale;
	// permissions, timeouts and non-socket paths must never cause an unlink.
	if info, e := os.Lstat(*socket); e == nil && info.Mode()&os.ModeSocket != 0 {
		c, dialErr := net.DialTimeout("unix", *socket, time.Second)
		if dialErr == nil {
			c.Close()
			return fmt.Errorf("API socket is already served: %s", *socket)
		}
		if errors.Is(dialErr, syscall.ECONNREFUSED) {
			if e := os.Remove(*socket); e != nil {
				return e
			}
		}
	}
	listener, err := net.Listen("unix", *socket)
	if err != nil {
		return err
	}
	defer listener.Close()
	mode := os.FileMode(0600)
	if len(readers) > 0 {
		mode = 0666
	}
	if err := os.Chmod(*socket, mode); err != nil {
		return err
	}
	server := &http.Server{Handler: handler, ConnContext: service.PeerContext, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 65 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	// Socket permissions are not the sole check: authenticate each HTTP peer UID
	// before handing its stream to net/http. Only this one local caller is admitted.
	var authenticated net.Listener = &localListener{Listener: listener}
	if len(readers) > 0 {
		authenticated = &sharedListener{Listener: listener, service: service}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	relayDone := make(chan struct{})
	go func() { defer close(relayDone); service.RunRelay(ctx) }()
	defer func() { stop(); <-relayDone }()
	done := make(chan error, 1)
	go func() { done <- server.Serve(authenticated) }()
	if *dashboardListen != "" {
		tcp, err := net.Listen("tcp", *dashboardListen)
		if err != nil {
			return err
		}
		defer tcp.Close()
		web := &http.Server{Handler: control.RequireBearer(handler), ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 65 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
		go func() { done <- web.Serve(tcp) }()
		fmt.Fprintln(os.Stderr, "lampd: dashboard listening on", tcp.Addr())
	}
	fmt.Fprintln(os.Stderr, "lampd: control API listening on", *socket)
	select {
	case err := <-done:
		if err != http.ErrServerClosed {
			return err
		}
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		server.Shutdown(shutdown)
		server.Close()
		<-done
	}
	return nil
}

type localListener struct{ net.Listener }

type sharedListener struct {
	net.Listener
	service *control.Service
}

func (l *sharedListener) Accept() (net.Conn, error) {
	for {
		connection, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if l.service.AuthenticatePublicPeer(connection.(*net.UnixConn)) != nil {
			connection.Close()
			continue
		}
		return connection, nil
	}
}

func (l *localListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if control.AuthenticateLocal(c.(*net.UnixConn)) != nil {
			c.Close()
			continue
		}
		return c, nil
	}
}
