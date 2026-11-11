// Package kits embeds the owner-operated Python browser/vault boundary in lampd.
package kits

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

//go:embed *.py
var modules embed.FS

// Start installs only private application files and starts a headless CLI service.
// Configuration contains paths, not credentials on argv. The caller owns Close.
func Start(ctx context.Context, config, root string) (string, func(), error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", nil, err
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return "", nil, fmt.Errorf("private kits directory required")
	}
	runtimeDir := filepath.Join(root, "runtime")
	packageDir := filepath.Join(runtimeDir, "kits")
	if err := os.MkdirAll(packageDir, 0700); err != nil {
		return "", nil, err
	}
	entries, _ := modules.ReadDir(".")
	for _, entry := range entries {
		data, err := modules.ReadFile(entry.Name())
		if err != nil {
			return "", nil, err
		}
		if err = os.WriteFile(filepath.Join(packageDir, entry.Name()), data, 0600); err != nil {
			return "", nil, err
		}
	}
	socket := filepath.Join(root, "owner.sock")
	if info, err := os.Lstat(socket); err == nil {
		// A backend killed with SIGKILL (or by OOM) leaves its socket behind.
		// Remove only a real socket (Lstat: never a symlink's target) that no
		// backend answers; a live backend or any other file is never replaced.
		if info.Mode()&os.ModeSocket == 0 {
			return "", nil, fmt.Errorf("kits socket path is not a socket; refuse to replace it")
		}
		conn, dialErr := net.DialTimeout("unix", socket, time.Second)
		if dialErr == nil {
			conn.Close()
			return "", nil, fmt.Errorf("a kits backend is already serving %s", socket)
		}
		if !errors.Is(dialErr, syscall.ECONNREFUSED) {
			return "", nil, fmt.Errorf("kits socket state unknown; refuse to replace it: %w", dialErr)
		}
		if err := os.Remove(socket); err != nil {
			return "", nil, err
		}
	}
	command := exec.CommandContext(ctx, "python3", "-m", "kits", "serve", "--config", config, "--root", root, "--owner-socket", socket)
	command.Dir = runtimeDir
	// Native diagnostics may contain private data; never forward them to lampd logs.
	if err := command.Start(); err != nil {
		return "", nil, err
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	closeService := func() {
		// SIGTERM, not SIGINT: a process started from a background shell
		// inherits SIGINT as ignored, and the service would never exit,
		// blocking lampd's SIGTERM drain (installer restarts and updates).
		if command.Process != nil {
			_ = command.Process.Signal(syscall.SIGTERM)
		}
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = command.Process.Kill()
			<-done
		}
	}
	for {
		select {
		case <-ctx.Done():
			_ = command.Process.Kill()
			<-done
			return "", nil, ctx.Err()
		case <-done:
			return "", nil, fmt.Errorf("kits service exited before readiness")
		case <-time.After(25 * time.Millisecond):
			if info, err := os.Lstat(socket); err == nil && info.Mode()&os.ModeSocket != 0 {
				return socket, closeService, nil
			}
		}
	}
}
