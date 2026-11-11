package plugin_test

import (
	"context"
	"encoding/json"
	"github.com/JohanSardar/lamp/pkg/plugin"
	"golang.org/x/sys/unix"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

func hostile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "hostile")
	cmd := exec.Command("go", "build", "-o", p, "./testdata/hostile")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hostile build: %s %v", b, err)
	}
	return p
}

func startHostile(t *testing.T, path string, a *authority) (*plugin.Worker, plugin.Binding, plugin.Grant) {
	t.Helper()
	bin, _ := os.ReadFile(path)
	m := fixtureManifest("console")
	m.Digest = plugin.Digest(bin)
	props := map[string]plugin.Schema{}
	for _, key := range []string{"mode", "path", "tcp", "unix", "fd"} {
		props[key] = plugin.Schema{Type: "string"}
	}
	results := map[string]plugin.Schema{}
	for _, key := range []string{"file", "tcp", "unix", "env", "fd", "subprocess"} {
		results[key] = plugin.Schema{Type: "boolean"}
	}
	m.Methods["run"] = plugin.Method{Input: plugin.Schema{Type: "object", Properties: props}, Result: plugin.Schema{Type: "object", Properties: results}}
	raw, _ := json.Marshal(m)
	pkg, err := plugin.Admit(raw, bin, plugin.Approval{OwnerID: "owner", ManifestDigest: plugin.Digest(raw)})
	if err != nil {
		t.Fatal(err)
	}
	l, ld := launcher(t)
	w, err := plugin.Start(context.Background(), plugin.StartOptions{ID: "hostile", Package: pkg, Executable: path, StateDir: t.TempDir(), Launcher: l, LauncherDigest: ld, DiagnosticSoftLimits: true, Authority: a})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := w.Close(); err != nil {
			t.Error(err)
		}
	})
	b := plugin.Binding{OwnerID: "owner", TaskID: "task", OperationID: "op", Generation: 1, ParentGrant: "parent", Deadline: time.Now().Add(5 * time.Second)}
	g, err := w.Grant(plugin.GrantRequest{Kind: "console", ResourceID: "session", Binding: b})
	if err != nil {
		t.Fatal(err)
	}
	return w, b, g
}

func TestNativeSandboxDeniesAmbientAuthority(t *testing.T) {
	path := hostile(t)
	sentinel := filepath.Join(t.TempDir(), "owner-secret")
	if err := os.WriteFile(sentinel, []byte("inherited-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(sentinel)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// Deliberately remove CLOEXEC on an unrelated host FD. exec must close it.
	if _, err := unix.FcntlInt(f.Fd(), unix.F_SETFD, 0); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAMP_TEST_OWNER_SECRET", "not-for-plugin")
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	socketDir, err := os.MkdirTemp("/tmp", "lamp-plg-sock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	socket := filepath.Join(socketDir, "owner.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	w, b, g := startHostile(t, path, new(authority))
	// FD numbers are merely probe inputs, never granted handles.
	input, _ := json.Marshal(map[string]string{"mode": "probe", "path": sentinel, "tcp": tcp.Addr().String(), "unix": socket, "fd": strconv.Itoa(int(f.Fd()))})
	result, err := w.Call(context.Background(), "run", b, g, input)
	if err != nil {
		t.Fatal(err)
	}
	var observed map[string]bool
	if err := json.Unmarshal(result, &observed); err != nil {
		t.Fatal(err)
	}
	if len(observed) != 6 {
		t.Fatalf("missing probes: %s", result)
	}
	for probe, escaped := range observed {
		if escaped {
			t.Errorf("ambient %s succeeded", probe)
		}
	}
	t.Logf("real OS probes: %s", result)
}

type authority struct {
	mu      sync.Mutex
	effects int
	revoked bool
	failed  []string
}

func (a *authority) Effect(ctx context.Context, e plugin.Effect) (json.RawMessage, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.effects++
	return json.RawMessage(`{"receipt":"delivered"}`), nil
}

func (a *authority) Revoke(pluginID string) { a.mu.Lock(); defer a.mu.Unlock(); a.revoked = true }

func (a *authority) FailDependent(pluginID, reason string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failed = append(a.failed, pluginID)
}

func launcher(t *testing.T) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "launcher")
	if out, err := exec.Command("cc", "-O2", "-Wall", "-Wextra", "-Werror", "-o", path, "../../tools/plugin-launcher.c").CombinedOutput(); err != nil {
		t.Fatalf("build launcher: %s %v", out, err)
	}
	bin, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, plugin.Digest(bin)
}

func fixtureManifest(kind string) plugin.Manifest {
	return plugin.Manifest{Type: kind, Publisher: "reference.lamp", Version: "1.0.0", Digest: plugin.Digest([]byte("executable")), ProtocolVersion: plugin.Protocol, TrustClass: "confined", Permissions: []string{kind + ".use"}, Limits: plugin.DefaultLimits(), Compatibility: plugin.Compatibility{APIMajor: 1, MinMinor: 0, MaxMinor: 1}, ConfigSchema: plugin.Schema{Type: "object"}, Methods: map[string]plugin.Method{"run": {Input: plugin.Schema{Type: "object"}, Result: plugin.Schema{Type: "object"}}}}
}
