package plugin_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/JohanSardar/lamp/pkg/plugin"
)

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

func reference(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "reference")
	cmd := exec.Command("go", "build", "-o", p, "../../cmd/lamp-plugin-reference")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build reference: %s %v", b, err)
	}
	return p
}
func startReference(t *testing.T, path, kind string, a *authority) (*plugin.Worker, plugin.Binding) {
	t.Helper()
	bin, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m := fixtureManifest(kind)
	m.Digest = plugin.Digest(bin)
	m.Methods = map[string]plugin.Method{"run": {Input: plugin.Schema{Type: "object"}, Result: plugin.Schema{Type: "object", Properties: map[string]plugin.Schema{"receipt": {Type: "string"}}, Required: []string{"receipt"}}}}
	raw, _ := json.Marshal(m)
	p, err := plugin.Admit(raw, bin, plugin.Approval{OwnerID: "owner", ManifestDigest: plugin.Digest(raw)})
	if err != nil {
		t.Fatal(err)
	}
	l, ld := launcher(t)
	w, err := plugin.Start(context.Background(), plugin.StartOptions{ID: "test-" + kind, Package: p, Executable: path, StateDir: t.TempDir(), Launcher: l, LauncherDigest: ld, DiagnosticSoftLimits: true, Authority: a})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := w.Close(); err != nil {
			t.Error(err)
		}
	})
	b := plugin.Binding{OwnerID: "owner", TaskID: "task", OperationID: "operation", Generation: 1, ParentGrant: "parent", Deadline: time.Now().Add(5 * time.Second)}
	return w, b
}
func TestSixteenConcurrentRPCs(t *testing.T) {
	path := reference(t)
	w, b := startReference(t, path, "console", new(authority))
	g, err := w.Grant(plugin.GrantRequest{Kind: "console", ResourceID: "session", Binding: b})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 16)
	for i := 0; i < 16; i++ {
		go func() {
			<-start
			_, err := w.Call(context.Background(), "run", b, g, json.RawMessage(`{}`))
			results <- err
		}()
	}
	close(start)
	for i := 0; i < 16; i++ {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
}

func TestRealConfinedPluginEffect(t *testing.T) {
	path := reference(t)
	a := new(authority)
	w, b := startReference(t, path, "console", a)
	grant, err := w.Grant(plugin.GrantRequest{Kind: "console", ResourceID: "session", Binding: b})
	if err != nil {
		t.Fatal(err)
	}
	got, err := w.Call(context.Background(), "run", b, grant, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"receipt":"delivered"}` {
		t.Fatalf("wrong effect %s", got)
	}
	if _, err := w.Grant(plugin.GrantRequest{Kind: "ram", ResourceID: "neighbor", Binding: b}); err == nil {
		t.Fatal("ordinary plugin received RAM")
	}
	if _, err := w.Call(context.Background(), "undeclared", b, grant, json.RawMessage(`{}`)); err == nil {
		t.Fatal("undeclared method")
	}
	wrong := b
	wrong.Generation++
	if _, err := w.Call(context.Background(), "run", wrong, grant, json.RawMessage(`{}`)); err == nil {
		t.Fatal("stale generation")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.effects != 1 {
		t.Fatalf("unauthorized effects: %d", a.effects)
	}
}
