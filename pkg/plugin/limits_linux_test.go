package plugin

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type limitAuthority struct{}

func (limitAuthority) Effect(context.Context, Effect) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}
func (limitAuthority) Revoke(string)                {}
func (limitAuthority) FailDependent(string, string) {}

func TestLinuxRuntimeRefusesWeakenedLimits(t *testing.T) {
	// This changes only the uniquely owned disposable plugin unit, never the
	// user's manager/slices or other services. The kernel/controller is real.
	dir := t.TempDir()
	program := filepath.Join(dir, "reference")
	helper := filepath.Join(dir, "launcher")
	for _, cmd := range []*exec.Cmd{exec.Command("go", "build", "-o", program, "../../cmd/lamp-plugin-reference"), exec.Command("cc", "-O2", "-Wall", "-Wextra", "-Werror", "-o", helper, "../../tools/plugin-launcher.c")} {
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture build: %s %v", out, err)
		}
	}
	binary, _ := os.ReadFile(program)
	bootstrap, _ := os.ReadFile(helper)
	m := Manifest{Type: "console", Publisher: "reference.lamp", Version: "1.0.0", Digest: Digest(binary), ProtocolVersion: Protocol, TrustClass: "confined", Permissions: []string{"console.use"}, Limits: DefaultLimits(), ConfigSchema: Schema{Type: "object"}, Compatibility: Compatibility{APIMajor: 1, MinMinor: 0, MaxMinor: 1}, Methods: map[string]Method{"run": {Input: Schema{Type: "object"}, Result: Schema{Type: "object"}}}}
	raw, _ := json.Marshal(m)
	p, err := Admit(raw, binary, Approval{OwnerID: "owner", ManifestDigest: Digest(raw)})
	if err != nil {
		t.Fatal(err)
	}
	w, err := Start(context.Background(), StartOptions{ID: "quota-proof", Package: p, Executable: program, StateDir: dir, Launcher: helper, LauncherDigest: Digest(bootstrap), Authority: limitAuthority{}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := w.Close(); err != nil {
			t.Error(err)
		}
	}()
	var unit string
	for _, arg := range w.cmd.Args {
		if strings.HasPrefix(arg, "--unit=") {
			unit = strings.TrimPrefix(arg, "--unit=") + ".service"
		}
	}
	if unit == "" {
		t.Fatal("missing owned service identity")
	}
	if out, err := exec.Command("/usr/bin/systemctl", "--user", "set-property", "--runtime", unit, "CPUQuota=100%").CombinedOutput(); err != nil {
		t.Fatalf("weaken owned CPU limit: %s %v", out, err)
	}
	b := Binding{OwnerID: "owner", TaskID: "task", OperationID: "op", ParentGrant: "parent", Generation: 1, Deadline: time.Now().Add(5 * time.Second)}
	if _, err := w.Grant(GrantRequest{Kind: "console", ResourceID: "assigned", Binding: b}); err == nil {
		t.Error("grant issued despite unenforced requested CPU ceiling")
	}
	select {
	case <-w.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("lost kernel limit did not open the circuit")
	}
}
