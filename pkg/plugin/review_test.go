package plugin_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"testing"

	"github.com/JohanSardar/lamp/pkg/plugin"
)

func TestZeroConfigurationIsNotReady(t *testing.T) {
	var cfg plugin.Configuration
	if cfg.ValidateRef(plugin.ConfigRef{}) == nil {
		t.Fatal("zero unvalidated configuration accepted")
	}
}
func TestPublisherSignatureIsOnlyProvenance(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(fixtureManifest("console"))
	signature := ed25519.Sign(private, raw)
	if err := plugin.VerifySignature(raw, public, signature); err != nil {
		t.Fatal(err)
	}
	if plugin.VerifySignature(append(raw, ' '), public, signature) == nil {
		t.Fatal("changed manifest signature accepted")
	}
	if _, err := plugin.Admit(raw, []byte("executable"), plugin.Approval{}); err == nil {
		t.Fatal("publisher signature substituted for owner permission review")
	}
}
func TestStartupRehashesExecutable(t *testing.T) {
	path := reference(t)
	bin, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m := fixtureManifest("console")
	m.Digest = plugin.Digest(bin)
	raw, _ := json.Marshal(m)
	p, err := plugin.Admit(raw, bin, plugin.Approval{OwnerID: "owner", ManifestDigest: plugin.Digest(raw)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("substituted executable"), 0700); err != nil {
		t.Fatal(err)
	}
	l, ld := launcher(t)
	if w, err := plugin.Start(context.Background(), plugin.StartOptions{ID: "substitute", Package: p, Executable: path, StateDir: t.TempDir(), Launcher: l, LauncherDigest: ld, DiagnosticSoftLimits: true, Authority: new(authority)}); err == nil {
		_ = w.Close()
		t.Fatal("startup digest substitution accepted")
	}
}
