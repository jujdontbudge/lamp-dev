package plugin_test

import (
	"encoding/json"
	"testing"

	"github.com/JohanSardar/lamp/pkg/plugin"
)

func TestConfigurationDoesNotRoundLargeIntegers(t *testing.T) {
	m := fixtureManifest("network")
	m.ConfigSchema = plugin.Schema{Type: "object", Properties: map[string]plugin.Schema{"number": {Type: "integer"}}}
	raw, _ := json.Marshal(m)
	p, err := plugin.Admit(raw, []byte("executable"), plugin.Approval{OwnerID: "owner", ManifestDigest: plugin.Digest(raw)})
	if err != nil {
		t.Fatal(err)
	}
	a := p.Adapter("pg_integer")
	one, err := a.Configure("cfg_same", a.SchemaDigest, json.RawMessage(`{"number":9007199254740992}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	two, err := a.Configure("cfg_same", a.SchemaDigest, json.RawMessage(`{"number":9007199254740993}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if one.Ref().Digest == two.Ref().Digest {
		t.Fatal("distinct config integers alias the same digest")
	}
}

func TestImmutableAdapterAndConfiguration(t *testing.T) {
	m := fixtureManifest("network")
	m.ConfigSchema = plugin.Schema{Type: "object", Properties: map[string]plugin.Schema{"endpoint": {Type: "string", MaxLength: 128}}, Required: []string{"endpoint"}}
	raw, _ := json.Marshal(m)
	p, err := plugin.Admit(raw, []byte("executable"), plugin.Approval{OwnerID: "owner", ManifestDigest: plugin.Digest(raw)})
	if err != nil {
		t.Fatal(err)
	}
	adapter := p.Adapter("pg_test")
	cfg, err := adapter.Configure("cfg_new", adapter.SchemaDigest, json.RawMessage(`{"endpoint":"fixture.invalid"}`), []string{"cr_vault"})
	if err != nil {
		t.Fatal(err)
	}
	ref := cfg.Ref()
	if ref.ConfigurationID != "cfg_new" || !plugin.ValidDigest(ref.Digest) {
		t.Fatal("missing immutable identity")
	}
	if err := cfg.ValidateRef(ref); err != nil {
		t.Fatal(err)
	}
	wrong := ref
	wrong.Digest = plugin.Digest([]byte("other"))
	if cfg.ValidateRef(wrong) == nil {
		t.Fatal("stale config digest")
	}
	if _, err := adapter.Configure("cfg_bad", "unknown", json.RawMessage(`{}`), nil); err == nil {
		t.Fatal("unknown schema")
	}
	if _, err := adapter.Configure("cfg_secret", adapter.SchemaDigest, json.RawMessage(`{"endpoint":"x","token":"secret"}`), nil); err == nil {
		t.Fatal("out-of-schema secret")
	}
	adapter.ConfigSchema.Properties["token"] = plugin.Schema{Type: "string"}
	// Caller mutation must not alter the immutable published schema.
	if _, err := adapter.Configure("cfg_mutated", adapter.SchemaDigest, json.RawMessage(`{"endpoint":"x","token":"secret"}`), nil); err == nil {
		t.Fatal("schema mutation")
	}
	cfg.Delete()
	if cfg.ValidateRef(ref) == nil {
		t.Fatal("deleted configuration reference")
	}
}
