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
	"syscall"
	"time"

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
	fleetFile := flags.String("fleet-config", "", "private enrolled fleet configuration JSON")
	identityFile := flags.String("identity-file", "", "owner enrollment JSON (owner_id, bootstrap_sha256); bearer is never stored")
	profileDir := flags.String("profile-dir", "", "private CPU/mutex/block profiles for a diagnostic run")
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	base := filepath.Join(home, ".local", "state", "lamp-demo")
	socket := flags.String("socket", filepath.Join(base, "api.sock"), "private local HTTP Unix socket")
	state := flags.String("state-dir", filepath.Join(base, "control"), "private operation journal directory")
	workerSocket := flags.String("worker-socket", "", "enrolled supervisor Unix socket")
	workerID := flags.String("worker-id", "", "assigned supervisor identity")
	bootID := flags.String("worker-boot-id", "", "enrolled supervisor boot identity")
	uid := flags.Int("worker-uid", -1, "assigned supervisor OS UID (required)")
	pid := flags.Int("worker-pid", 0, "assigned supervisor OS PID (required)")
	grant := flags.String("grant-id", "", "supervisor-installed control grant ID")
	timeout := flags.Duration("worker-timeout", 30*time.Second, "absolute operation deadline (max 1m)")
	stateAdmin := flags.String("state-admin", "", "JSON argv of the supervisor's root-only state administration, used to discard saved state")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	var admin []string
	if *stateAdmin != "" {
		if err := json.Unmarshal([]byte(*stateAdmin), &admin); err != nil || len(admin) == 0 {
			return fmt.Errorf("--state-admin must be a JSON argv")
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
	if err := control.PrivateDir(filepath.Dir(*socket)); err != nil {
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
	service, err := control.Open(control.Config{Fleet: fleetConfig, Identity: identity, StateDir: *state, Worker: control.WorkerConfig{Socket: *workerSocket, WorkerID: *workerID, BootID: *bootID, UID: *uid, PID: *pid, GrantID: *grant, Timeout: *timeout}, StateAdmin: admin})
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
	if err := os.Chmod(*socket, 0600); err != nil {
		return err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 65 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	// Socket permissions are not the sole check: authenticate each HTTP peer UID
	// before handing its stream to net/http. Only this one local caller is admitted.
	authenticated := &localListener{Listener: listener}
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
