// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/JohanSardar/lamp/internal/relayclient"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "lamp-relay-client:", e)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) < 2 {
		return errors.New("use issue-enrollment, enroll, host or connect")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	flags := flag.NewFlagSet("lamp-relay-client", flag.ContinueOnError)
	base := flags.String("relay", "https://relay.eilmer.si", "relay origin")
	fleet := flags.String("fleet", "", "enrolled fleet ID")
	account := flags.String("account-token-file", "", "private short-lived shared Eilmer account token")
	enrollment := flags.String("enrollment-file", "", "private one-time enrollment token file")
	credential := flags.String("credential-file", "", "private host reconnect credential file")
	target := flags.String("target", "", "loopback fleet SSH host:port")
	httpsTarget := flags.String("https-target", "", "optional loopback fleet HTTPS host:port; TLS terminates only there")
	dialAddress := flags.String("relay-dial-address", "", "optional loopback relay dial override; TLS hostname verification is unchanged")
	out := flags.String("out", "", "new private enrollment output file")
	if e := flags.Parse(os.Args[2:]); e != nil {
		return e
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	switch os.Args[1] {
	case "issue-enrollment":
		return relayclient.IssueEnrollment(ctx, *base, *fleet, *account, *out)
	case "enroll":
		return relayclient.Enroll(ctx, *base, *fleet, *enrollment, *credential)
	case "host":
		return relayclient.HostHTTPS(ctx, *base, *credential, *target, *httpsTarget, *dialAddress)
	case "connect":
		return relayclient.Proxy(ctx, *base, *fleet, *account, os.Stdin, os.Stdout)
	default:
		return errors.New("unknown command")
	}
}
