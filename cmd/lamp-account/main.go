// SPDX-License-Identifier: Apache-2.0
// lamp-account holds identity-provider sessions, not work or model credentials.
// Run it separately from the untrusted relay with its own TLS key and OS identity.
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"lamp.eilmer.si/internal/hostedissuer"
	"lamp.eilmer.si/internal/hostedlogin"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "lamp-account:", e)
		os.Exit(1)
	}
}
func run() error {
	path := flag.String("config", "", "private account configuration")
	flag.Parse()
	raw, e := hostedissuer.ReadPrivate(*path)
	if e != nil {
		return e
	}
	var c hostedlogin.AccountConfig
	if json.Unmarshal(raw, &c) != nil {
		return errors.New("invalid account configuration")
	}
	if _, e = hostedissuer.ReadPrivate(c.Key); e != nil {
		return e
	}
	service, e := hostedlogin.NewAccount(c)
	if e != nil {
		return e
	}
	server := &http.Server{Addr: c.Listen, Handler: service, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13}, TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){}, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, MaxHeaderBytes: 16384, ErrorLog: log.New(io.Discard, "", 0)}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	e = server.ListenAndServeTLS(c.Certificate, c.Key)
	if e == http.ErrServerClosed {
		return nil
	}
	return e
}
