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
	"strconv"
	"strings"
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
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	prepareSocketDir := control.PrivateDir
	if len(readers) > 0 {
		prepareSocketDir = control.SharedAPIDir
	}
	if err := prepareSocketDir(filepath.Dir(*socket)); err != nil {
		return err
	}
	service, err := control.Open(control.Config{StateDir: *state, ExportsDir: *exports, SharedReaders: readers, Worker: control.WorkerConfig{Socket: *workerSocket, WorkerID: *workerID, BootID: *bootID, UID: *uid, PID: *pid, GrantID: *grant, Timeout: *timeout}})
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
	mode := os.FileMode(0600)
	if len(readers) > 0 {
		mode = 0666
	}
	if err := os.Chmod(*socket, mode); err != nil {
		return err
	}
	server := &http.Server{Handler: service, ConnContext: service.PeerContext, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 65 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
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
