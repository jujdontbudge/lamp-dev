// lamp-access is the owner-run access edge. It does not require a hosted Lamp account.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"lamp.eilmer.si/internal/access"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "lamp-access:", e)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) < 2 {
		return errors.New("use serve, init-ca, connect, tailnet-connect, ci, or cloudflare-manifest")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	flags := flag.NewFlagSet("lamp-access "+os.Args[1], flag.ContinueOnError)
	switch os.Args[1] {
	case "serve":
		path := flags.String("config", "", "private access configuration JSON")
		if e := flags.Parse(os.Args[2:]); e != nil {
			return e
		}
		if flags.NArg() != 0 {
			return errors.New("unexpected arguments")
		}
		c, e := access.Load(*path)
		if e != nil {
			return e
		}
		s, e := access.New(c)
		if e != nil {
			return e
		}
		return s.Run(ctx, func(route, address string) {
			state := "listening"
			if route == "cloudflare-connector-started" {
				state = "started"
			}
			_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"route": route, "address": address, "state": state})
		})
	case "init-ca":
		path := flags.String("out", "", "new private fleet CA key path (public key is .pub)")
		if e := flags.Parse(os.Args[2:]); e != nil {
			return e
		}
		if *path == "" {
			return errors.New("--out required")
		}
		return access.InitCA(*path)
	case "connect":
		address := flags.String("url", "", "https://owner-domain/ssh (OpenSSH ProxyCommand)")
		if e := flags.Parse(os.Args[2:]); e != nil {
			return e
		}
		return access.Proxy(ctx, *address, os.Stdin, os.Stdout)
	case "ci":
		var c access.CIConfig
		flags.StringVar(&c.ClientID, "client-id", "", "Tailscale federated identity client ID")
		flags.StringVar(&c.Audience, "audience", "", "federated workload audience")
		flags.StringVar(&c.Hostname, "hostname", "lamp-ci", "ephemeral node name")
		flags.StringVar(&c.Target, "target", "", "fleet SSH host:port")
		flags.StringVar(&c.HTTPAddress, "certificate-address", "", "fleet Tailscale HTTP host:port")
		flags.StringVar(&c.User, "user", "", "configured fleet principal")
		flags.StringVar(&c.HostCAFile, "host-ca", "", "fleet host CA public key")
		tags := flags.String("tags", "", "comma-separated CI tags")
		if e := flags.Parse(os.Args[2:]); e != nil {
			return e
		}
		if *tags != "" {
			c.Tags = strings.Split(*tags, ",")
		}
		ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		return access.RunCI(ctx, c, flags.Args(), os.Stdin, os.Stdout, os.Stderr)
	case "tailnet-connect":
		var t access.Tailscale
		dir := flags.String("state-dir", "", "private client state directory")
		flags.StringVar(&t.Hostname, "hostname", "lamp-client", "client node name")
		flags.StringVar(&t.ControlURL, "control-url", "", "optional owner Headscale URL")
		flags.StringVar(&t.AuthKeyFile, "auth-key-file", "", "optional private enrollment key file")
		flags.StringVar(&t.AuthURLFile, "auth-url-file", "", "private interactive enrollment URL file")
		target := flags.String("target", "", "tailnet host:port")
		if e := flags.Parse(os.Args[2:]); e != nil {
			return e
		}
		return access.TailnetConnect(ctx, *dir, t, *target, os.Stdin, os.Stdout)
	case "cloudflare-manifest":
		path := flags.String("plugin", "", "built lamp-access-cloudflare executable")
		out := flags.String("out", "", "new manifest file")
		if e := flags.Parse(os.Args[2:]); e != nil {
			return e
		}
		bin, e := os.ReadFile(*path)
		if e != nil {
			return e
		}
		raw, e := access.CloudflareManifest(bin)
		if e != nil {
			return e
		}
		f, e := os.OpenFile(*out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		defer f.Close()
		_, e = f.Write(append(raw, '\n'))
		return e
	default:
		return errors.New("unknown command")
	}
}
