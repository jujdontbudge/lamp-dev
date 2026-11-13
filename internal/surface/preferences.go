package surface

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lamp.eilmer.si/pkg/api"
)

// DefaultSocket and Credential are shared by the CLI and MCP adapter.
func DefaultSocket() string {
	if value := os.Getenv("LAMP_SOCKET"); value != "" {
		return value
	}
	home, _ := os.UserHomeDir()
	if prefix := installedPrefix(); prefix != "" {
		return filepath.Join(prefix, "run", "api.sock")
	}
	return filepath.Join(home, ".local", "state", "lamp-demo", "api.sock")
}
func Credential(file string) (string, error) {
	if file == "" {
		file = os.Getenv("LAMP_TOKEN_FILE")
	}
	if file == "" {
		if token := os.Getenv("LAMP_TOKEN"); token != "" {
			return token, nil
		}
		if prefix := installedPrefix(); prefix != "" {
			file = filepath.Join(prefix, "config", "owner.token")
		} else {
			return "", nil
		}
	}
	data, err := os.ReadFile(file)
	return strings.TrimSpace(string(data)), err
}
func InstalledOperations(fleet bool) []api.OperationDescriptor {
	out := []api.OperationDescriptor{}
	for _, op := range api.Operations {
		if CoreNames[op.Name] || fleet && (op.Name == "host_get" || op.Name == "host_list") {
			out = append(out, op)
		}
	}
	return out
}

func installedPrefix() string {
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	for dir := filepath.Dir(executable); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if info, err := os.Stat(filepath.Join(dir, "config", "install.json")); err == nil && info.Mode().IsRegular() {
			return dir
		}
	}
	return ""
}

// Operations uses the connected daemon's installed inventory when available.
// Offline help falls back to the handlers shipped in this build.
func (c *Client) Operations(ctx context.Context) []api.OperationDescriptor {
	ops := InstalledOperations(strings.HasPrefix(c.URL, "lamp+fleet:"))
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	raw, status, err := c.Call(ctx, ByName["docs_read"], map[string]any{})
	if err != nil || status != 200 {
		return ops
	}
	var reply struct {
		Installed map[string]bool `json:"installed_operations"`
	}
	if json.Unmarshal(raw, &reply) != nil || reply.Installed == nil {
		return ops
	}
	result := []api.OperationDescriptor{}
	for _, op := range api.Operations {
		if reply.Installed[op.Name] {
			result = append(result, op)
		}
	}
	return result
}
