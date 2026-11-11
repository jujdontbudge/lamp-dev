// lampd serves the local control API and optional mutually authenticated fleet.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
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

	"github.com/JohanSardar/lamp/internal/ci"
	"github.com/JohanSardar/lamp/internal/control"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "lampd:", err)
		os.Exit(1)
	}
}
func run() error {
	flags := flag.NewFlagSet("lampd", flag.ContinueOnError)
	auditReservation := flags.Uint64("audit-reservation-bytes", 24<<20, "trusted per-sandbox retained audit reservation, matching the supervisor policy")
	ciFile := flags.String("ci-config", "", "private host-enrolled GitHub CI policy JSON")
	auditReader := flags.String("audit-reader", "", "trusted absolute executable reading guard audit for sandbox ID and generation")
	quotaFile := flags.String("quota-file", "", "host admission quota JSON")
	fleetFile := flags.String("fleet-config", "", "private enrolled fleet configuration JSON")
	identityFile := flags.String("identity-file", "", "owner enrollment JSON (owner_id, bootstrap_sha256); bearer is never stored")
	nativeFile := flags.String("native-fallback-file", "", "trusted local native runtime and immutable job credential bindings")
	recoveryPublic := flags.String("recovery-public-key", "", "owner's offline data-recovery public key; every backup is also wrapped to it")
	recoveryKey := flags.String("recovery-key-file", "", "replacement host only: owner's recovery key file, read once to import sealed archives")
	recoveryImport := flags.String("recovery-import-dir", "", "directory of sealed .lbk archives to import with --recovery-key-file")
	installDir := flags.String("install-dir", "", "lamp-installer prefix: release mirror, pinned release key and updater agent files")
	profileDir := flags.String("profile-dir", "", "private CPU/mutex/block profiles for a diagnostic run")
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	base := filepath.Join(home, ".local", "state", "lamp-demo")
	socket := flags.String("socket", filepath.Join(base, "api.sock"), "private local HTTP Unix socket")
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
	pid := flags.Int("worker-pid", 0, "assigned supervisor OS PID (required)")
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
	service, err := control.Open(control.Config{AuditReservationBytes: *auditReservation, CI: ciConfig, Quotas: quotas, AuditReader: *auditReader, Fleet: fleetConfig, Identity: identity, NativeFallback: nativeConfig, StateDir: *state, ExportsDir: *exports, SharedReaders: readers, Worker: control.WorkerConfig{Socket: *workerSocket, WorkerID: *workerID, BootID: *bootID, UID: *uid, PID: *pid, GrantID: *grant, Timeout: *timeout}, StateAdmin: admin, AuditRelease: release, RecoveryPublicKey: *recoveryPublic, RecoveryKeyFile: *recoveryKey, RecoveryImportDir: *recoveryImport, InstallDir: *installDir})
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
	// Never unlink an existing socket: a second daemon must not steal a live path.
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
	done := make(chan error, 1)
	go func() { done <- server.Serve(authenticated) }()
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
