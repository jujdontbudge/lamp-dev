package plugin_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/JohanSardar/lamp/pkg/plugin"
)

func TestMacCannotClaimHardQuotaQualification(t *testing.T) {
	path := reference(t)
	bin, _ := os.ReadFile(path)
	m := fixtureManifest("console")
	m.Digest = plugin.Digest(bin)
	raw, _ := json.Marshal(m)
	p, err := plugin.Admit(raw, bin, plugin.Approval{OwnerID: "owner", ManifestDigest: plugin.Digest(raw)})
	if err != nil {
		t.Fatal(err)
	}
	l, ld := launcher(t)
	w, err := plugin.Start(context.Background(), plugin.StartOptions{ID: "production", Package: p, Executable: path, StateDir: t.TempDir(), Launcher: l, LauncherDigest: ld, Authority: new(authority)})
	if err == nil {
		_ = w.Close()
		t.Fatal("Mac production start silently accepted sampled resource quotas")
	}
	if !strings.Contains(err.Error(), "hard resource quotas") {
		t.Fatal(err)
	}
}
