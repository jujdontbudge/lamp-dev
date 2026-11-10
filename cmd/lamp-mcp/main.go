// lamp-mcp is a stdio MCP adapter. Every tool calls the same credentialed HTTP
// API as lamp, including denials, idempotency, revisions and bounded event waits.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/JohanSardar/lamp/internal/surface"
)

func main() {
	home, _ := os.UserHomeDir()
	socket := os.Getenv("LAMP_SOCKET")
	if socket == "" {
		socket = filepath.Join(home, ".local", "state", "lamp-demo", "api.sock")
	}
	client := &surface.Client{Socket: socket, URL: os.Getenv("LAMP_API_URL"), Token: os.Getenv("LAMP_TOKEN")}
	defer client.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := surface.ServeMCP(ctx, os.Stdin, os.Stdout, client); err != nil {
		fmt.Fprintln(os.Stderr, "lamp-mcp:", err)
		os.Exit(1)
	}
}
