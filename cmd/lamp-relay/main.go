// SPDX-License-Identifier: AGPL-3.0-only
// lamp-relay is optional; local and owner-OIDC fleets do not run it.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"lamp.eilmer.si/internal/relay"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "lamp-relay:", e)
		os.Exit(1)
	}
}
func run() error {
	config := flag.String("config", "", "private relay configuration JSON")
	flag.Parse()
	c, e := relay.Load(*config)
	if e != nil {
		return e
	}
	s, e := relay.New(c)
	if e != nil {
		return e
	}
	defer s.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return s.Run(ctx)
}
