// lamp-broker runs one owner's host-only credential and approval boundary.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"lamp.eilmer.si/internal/broker"
	"lamp.eilmer.si/internal/control"
)

func main() {
	if run() != nil {
		fmt.Fprintln(os.Stderr, "lamp-broker: startup or listener failed (check private configuration and listener paths)")
		os.Exit(1)
	}
}
func run() error {
	flags := flag.NewFlagSet("lamp-broker", flag.ContinueOnError)
	config := flags.String("config", "", "private host configuration JSON")
	socket := flags.String("owner-socket", "", "owner-UID authenticated Unix socket, in a private directory")
	listen := flags.String("task-listen", "127.0.0.1:7778", "scoped task HTTP address; non-loopback requires TLS")
	cert := flags.String("tls-cert", "", "task listener TLS certificate (public part may be installed in guests)")
	key := flags.String("tls-key", "", "host-only task listener TLS private key")
	audit := flags.String("audit", "", "new private append-only redacted audit file (required)")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || *config == "" || *socket == "" || *audit == "" {
		return errors.New("config, owner-socket and audit required")
	}
	if err := unix.Setrlimit(unix.RLIMIT_CORE, &unix.Rlimit{Cur: 0, Max: 0}); err != nil {
		return err
	}
	if err := disableDumps(); err != nil {
		return err
	}
	cfg, err := broker.Load(*config)
	if err != nil {
		return err
	}
	parent := filepath.Dir(*socket)
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("owner socket requires private directory")
	}
	file, err := os.OpenFile(*audit, os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	service, err := broker.New(cfg, file)
	if err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(*listen)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || (!ip.IsLoopback() && (*cert == "" || *key == "")) {
		return errors.New("non-loopback task listener requires TLS")
	}
	owner, err := net.Listen("unix", *socket)
	if err != nil {
		return err
	}
	defer owner.Close()
	if err := os.Chmod(*socket, 0600); err != nil {
		return err
	}
	task, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	defer task.Close()
	server := func(h http.Handler) *http.Server {
		return &http.Server{Handler: h, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 50 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 8192}
	}
	ownerServer := server(service.OwnerHandler())
	taskServer := server(service.TaskHandler())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 2)
	go func() { done <- ownerServer.Serve(&ownerListener{owner}) }()
	go func() {
		if *cert != "" && *key != "" {
			done <- taskServer.ServeTLS(task, *cert, *key)
		} else {
			done <- taskServer.Serve(task)
		}
	}()
	fmt.Fprintln(os.Stderr, "lamp-broker: owner socket and scoped task listener ready")
	select {
	case err = <-done:
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	_ = taskServer.Shutdown(shutdown)
	_ = ownerServer.Shutdown(shutdown)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

type ownerListener struct{ net.Listener }

func (l *ownerListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if control.AuthenticateLocal(c.(*net.UnixConn)) != nil {
			_ = c.Close()
			continue
		}
		return c, nil
	}
}
