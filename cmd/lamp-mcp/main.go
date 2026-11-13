// lamp-mcp is a stdio MCP adapter. Every tool calls the same credentialed HTTP
// API as lamp, including denials, idempotency, revisions and bounded event waits.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"lamp.eilmer.si/internal/localconfig"
	"lamp.eilmer.si/internal/surface"
)

func main() {
	flags := flag.NewFlagSet("lamp-mcp", flag.ContinueOnError)
	flags.SetOutput(os.Stdout)
	socket := flags.String("socket", localconfig.Socket(), "lampd Unix socket (LAMP_SOCKET)")
	url := flags.String("api-url", os.Getenv("LAMP_API_URL"), "API origin (LAMP_API_URL)")
	tokenFile := flags.String("token-file", "", "private bearer file (LAMP_TOKEN_FILE; otherwise LAMP_TOKEN)")
	flags.Usage = func() {
		fmt.Fprintln(os.Stdout, `Usage: lamp-mcp [--socket PATH] [--token-file PATH]
Serve Lamp tools over newline-delimited JSON-RPC on stdin/stdout.
Environment: LAMP_SOCKET, LAMP_API_URL, LAMP_TOKEN_FILE, LAMP_TOKEN.
MCP client config example:
{"mcpServers":{"lamp":{"command":"lamp-mcp","env":{"LAMP_TOKEN_FILE":"/path/to/config/owner.token"}}}}`)
		flags.PrintDefaults()
	}
	if err := flags.Parse(os.Args[1:]); err != nil {
		if err == flag.ErrHelp {
			return
		}
		os.Exit(2)
	}
	if flags.NArg() != 0 {
		flags.Usage()
		os.Exit(2)
	}
	token, err := localconfig.Token(*tokenFile, *socket, *url)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lamp-mcp: cannot read credential file:", err)
		os.Exit(2)
	}
	client := &surface.Client{Socket: *socket, URL: *url, Token: token}
	defer client.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := surface.ServeMCP(ctx, os.Stdin, os.Stdout, client); err != nil {
		fmt.Fprintln(os.Stderr, "lamp-mcp:", err)
		os.Exit(1)
	}
}
