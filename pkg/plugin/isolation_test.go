package plugin_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/JohanSardar/lamp/pkg/plugin"
	"golang.org/x/sys/unix"
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
func TestPluginCannotForgeOrReplayEffect(t *testing.T) {
	path := hostile(t)
	for _, mode := range []string{"forge", "replay"} {
		t.Run(mode, func(t *testing.T) {
			a := new(authority)
			w, b, g := startHostile(t, path, a)
			raw, _ := json.Marshal(map[string]string{"mode": mode})
			_, _ = w.Call(context.Background(), "run", b, g, raw)
			a.mu.Lock()
			count := a.effects
			a.mu.Unlock()
			want := 0
			if mode == "replay" {
				want = 1
			}
			if count != want {
				t.Fatalf("host authorized %d effects, want %d", count, want)
			}
		})
	}
}

func TestWorkerFailureRevokesOnlyDependentWork(t *testing.T) {
	path := hostile(t)
	normal := reference(t)
	a := new(authority)
	healthy, b := startReference(t, normal, "console", a)
	g, err := healthy.Grant(plugin.GrantRequest{Kind: "console", ResourceID: "unrelated", Binding: b})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"crash", "hang", "oversize", "stderr", "malformed", "flood", "memory"} {
		t.Run(mode, func(t *testing.T) {
			dependent := new(authority)
			w, b, g := startHostile(t, path, dependent)
			input, _ := json.Marshal(map[string]string{"mode": mode})
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			if mode == "hang" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), time.Second)
			}
			defer cancel()
			if _, err := w.Call(ctx, "run", b, g, input); err == nil {
				t.Fatal("fault accepted")
			}
			select {
			case <-w.Done():
			case <-time.After(8 * time.Second):
				t.Fatal("worker cleanup hung")
			}
			if _, err := w.Grant(plugin.GrantRequest{Kind: "console", ResourceID: "session", Binding: b}); err == nil {
				t.Fatal("grants survived circuit break")
			}
			dependent.mu.Lock()
			revoked := dependent.revoked
			failures := len(dependent.failed)
			dependent.mu.Unlock()
			if !revoked || failures != 1 {
				t.Fatalf("cleanup missing: %v %d", revoked, failures)
			}
			if mode == "memory" && errors.Is(w.Err(), context.DeadlineExceeded) {
				t.Fatal("memory quota did not terminate the worker before the call deadline")
			}
			t.Logf("circuit opened, grants revoked: %v", w.Err())
		})
	}
	// A new context/grant for the unrelated worker after all failures.
	b.Deadline = time.Now().Add(5 * time.Second)
	g, err = healthy.Grant(plugin.GrantRequest{Kind: "console", ResourceID: "unrelated", Binding: b})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := healthy.Call(context.Background(), "run", b, g, json.RawMessage(`{}`)); err != nil {
		t.Fatalf("unrelated worker failed: %v", err)
	}
}
