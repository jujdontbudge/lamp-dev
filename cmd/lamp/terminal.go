package main

import (
	"context"
	"flag"
	"fmt"
	"golang.org/x/term"
	"io"
	"lamp.eilmer.si/internal/surface"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

func shell(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("lamp sandbox shell", flag.ContinueOnError)
	flags.SetOutput(stderr)
	socket := surface.DefaultSocket()
	flags.StringVar(&socket, "socket", socket, "lampd socket")
	origin := flags.String("api-url", os.Getenv("LAMP_API_URL"), "API origin")
	tokenFile := flags.String("token-file", "", "private token file")
	sandbox := flags.String("sandbox-id", "", "sandbox ID")
	execID := flags.String("exec-id", "", "reconnect to an existing PTY exec")
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		*sandbox = args[0]
		args = args[1:]
	}
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *sandbox == "" {
		fmt.Fprintln(stderr, "sandbox ID required")
		return 2
	}
	token, err := surface.Credential(*tokenFile)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 3
	}
	client := &surface.Client{Socket: socket, URL: *origin, Token: token}
	defer client.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer cancel()
	if err := surface.WakeSandbox(ctx, client, *sandbox); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	sizes := make(chan [2]uint64, 1)
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		state, err := term.MakeRaw(fd)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		defer term.Restore(fd, state)
		resize := make(chan os.Signal, 1)
		signal.Notify(resize, syscall.SIGWINCH)
		defer signal.Stop(resize)
		send := func() {
			cols, rows, err := term.GetSize(fd)
			if err == nil {
				select {
				case sizes <- [2]uint64{uint64(rows), uint64(cols)}:
				default:
				}
			}
		}
		send()
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-resize:
					send()
				}
			}
		}()
	}
	id, code, err := surface.Terminal(ctx, client, *sandbox, *execID, os.Stdin, stdout, stderr, sizes, os.Getenv("TERM"))
	if err != nil {
		fmt.Fprintf(stderr, "terminal %s: %v\n", id, err)
		return 1
	}
	if code < 0 {
		return 1
	}
	return code
}
