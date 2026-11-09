// lampd serves the bounded, single-local-caller engine demo control path.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
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
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	if err := control.PrivateDir(filepath.Dir(*socket)); err != nil {
		return err
	}
	service, err := control.Open(control.Config{StateDir: *state, Worker: control.WorkerConfig{Socket: *workerSocket, WorkerID: *workerID, BootID: *bootID, UID: *uid, PID: *pid, GrantID: *grant, Timeout: *timeout}})
	if err != nil {
		return err
	}
	defer service.Close()
	// Never unlink an existing socket: a second daemon must not steal a live path.
	listener, err := net.Listen("unix", *socket)
	if err != nil {
		return err
	}
	defer listener.Close()
	if err := os.Chmod(*socket, 0600); err != nil {
		return err
	}
	server := &http.Server{Handler: service, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 65 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	// Socket permissions are not the sole check: authenticate each HTTP peer UID
	// before handing its stream to net/http. Only this one local caller is admitted.
	authenticated := &localListener{Listener: listener}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- server.Serve(authenticated) }()
	fmt.Fprintln(os.Stderr, "lampd: local demo API listening on", *socket)
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
