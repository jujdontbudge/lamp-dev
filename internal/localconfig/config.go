// Package localconfig discovers the installation owning the running client.
package localconfig

import (
	"os"
	"path/filepath"
	"strings"
)

func Prefix() string {
	if p := os.Getenv("LAMP_PREFIX"); p != "" {
		return p
	}
	// Resolve current/ and user-created bin symlinks, then releases/<version>/bin.
	if exe, err := os.Executable(); err == nil {
		if exe, err = filepath.EvalSymlinks(exe); err == nil {
			p := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(exe))))
			if _, err := os.Stat(filepath.Join(p, "config", "install.json")); err == nil {
				return p
			}
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "lamp")
}

func Socket() string {
	if s := os.Getenv("LAMP_SOCKET"); s != "" {
		return s
	}
	return filepath.Join(Prefix(), "run", "api.sock")
}

// Credentials never send the local owner's bearer to an explicitly selected
// remote endpoint. Explicit token files and LAMP_TOKEN still work remotely.
func Token(file, socket, url string) (string, error) {
	if file == "" {
		file = os.Getenv("LAMP_TOKEN_FILE")
	}
	if file == "" {
		if t := os.Getenv("LAMP_TOKEN"); t != "" {
			return t, nil
		}
		if url != "" || socket != filepath.Join(Prefix(), "run", "api.sock") {
			return "", nil
		}
		file = filepath.Join(Prefix(), "config", "owner.token")
		if _, err := os.Stat(file); os.IsNotExist(err) {
			return "", nil
		}
	}
	b, err := os.ReadFile(file)
	return strings.TrimSpace(string(b)), err
}
