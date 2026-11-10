package plugin_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/JohanSardar/lamp/pkg/plugin"
)

func fixtureManifest(kind string) plugin.Manifest {
	return plugin.Manifest{Type: kind, Publisher: "reference.lamp", Version: "1.0.0", Digest: plugin.Digest([]byte("executable")), ProtocolVersion: plugin.Protocol, TrustClass: "confined", Permissions: []string{kind + ".use"}, Limits: plugin.DefaultLimits(), Compatibility: plugin.Compatibility{APIMajor: 1, MinMinor: 0, MaxMinor: 1}, ConfigSchema: plugin.Schema{Type: "object"}, Methods: map[string]plugin.Method{"run": {Input: plugin.Schema{Type: "object"}, Result: plugin.Schema{Type: "object"}}}}
}
func TestManifestAdmissionPinsBytesAndTrust(t *testing.T) {
	for _, kind := range []string{"engine", "network", "credential_source", "harness", "kit", "ci", "trigger", "console", "access", "notification_sink"} {
		m := fixtureManifest(kind)
		if kind == "engine" {
			m.TrustClass = "engine_trusted"
		}
		raw, _ := json.Marshal(m)
		a := plugin.Approval{OwnerID: "owner", ManifestDigest: plugin.Digest(raw)}
		if kind == "engine" {
			if _, err := plugin.Admit(raw, []byte("executable"), a); err == nil {
				t.Fatal("engine admitted without warning")
			}
			a.EngineWarning = plugin.EngineWarning
		}
		if _, err := plugin.Admit(raw, []byte("executable"), a); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if _, err := plugin.Admit(raw, []byte("substitute"), a); err == nil {
			t.Fatal("digest mismatch accepted")
		}
		if _, err := plugin.Admit(append(raw, ' '), []byte("executable"), a); err == nil {
			t.Fatal("manifest changed after approval")
		}
	}
	m := fixtureManifest("console")
	raw, _ := json.Marshal(m)
	for _, a := range []plugin.Approval{{}, {OwnerID: "owner", ManifestDigest: strings.Repeat("0", 64)}} {
		if _, err := plugin.Admit(raw, []byte("executable"), a); err == nil {
			t.Fatal("unapproved admission")
		}
	}
	m.ProtocolVersion = "lamp.plugin/2"
	raw, _ = json.Marshal(m)
	if _, err := plugin.Admit(raw, []byte("executable"), plugin.Approval{OwnerID: "owner", ManifestDigest: plugin.Digest(raw)}); err == nil {
		t.Fatal("major mismatch")
	}
	m = fixtureManifest("console")
	m.Permissions = append(m.Permissions, "ram.read")
	raw, _ = json.Marshal(m)
	if _, err := plugin.Admit(raw, []byte("executable"), plugin.Approval{OwnerID: "owner", ManifestDigest: plugin.Digest(raw)}); err == nil {
		t.Fatal("ordinary RAM permission")
	}
}
func TestManifestRequiredFields(t *testing.T) {
	raw, _ := json.Marshal(fixtureManifest("console"))
	var original map[string]json.RawMessage
	_ = json.Unmarshal(raw, &original)
	for _, field := range []string{"permissions", "limits", "compatibility", "config_schema"} {
		var doc map[string]json.RawMessage
		_ = json.Unmarshal(raw, &doc)
		delete(doc, field)
		bad, _ := json.Marshal(doc)
		if _, err := plugin.Admit(bad, []byte("executable"), plugin.Approval{OwnerID: "owner", ManifestDigest: plugin.Digest(bad)}); err == nil {
			t.Fatalf("missing %s admitted", field)
		}
	}
	original["permissions"] = json.RawMessage(`null`)
	bad, _ := json.Marshal(original)
	if _, err := plugin.Admit(bad, []byte("executable"), plugin.Approval{OwnerID: "owner", ManifestDigest: plugin.Digest(bad)}); err == nil {
		t.Fatal("null permissions admitted")
	}
}

func TestEmbeddedSchemaRejectsNullAndEmptyEnum(t *testing.T) {
	raw, _ := json.Marshal(fixtureManifest("console"))
	schemas := []string{`{"type":"object","enum":[]}`}
	for _, keyword := range []string{"type", "properties", "required", "items", "enum", "maxLength", "maxItems", "minimum", "maximum"} {
		schema := map[string]any{"type": "object", keyword: nil}
		encoded, _ := json.Marshal(schema)
		schemas = append(schemas, string(encoded))
	}
	schemas = append(schemas, `{"type":"object","properties":{"nested":{"type":"object","enum":[]}}}`)
	for _, schema := range schemas {
		t.Run(schema, func(t *testing.T) {
			var doc map[string]json.RawMessage
			_ = json.Unmarshal(raw, &doc)
			doc["config_schema"] = json.RawMessage(schema)
			bad, _ := json.Marshal(doc)
			if _, err := plugin.Admit(bad, []byte("executable"), plugin.Approval{OwnerID: "owner", ManifestDigest: plugin.Digest(bad)}); err == nil {
				t.Fatal("invalid embedded schema admitted:", schema)
			}
		})
	}
}

func TestNullEnumValueRemainsValid(t *testing.T) {
	schema := plugin.Schema{Type: "null", Enum: []json.RawMessage{json.RawMessage(`null`)}}
	if err := schema.Validate(json.RawMessage(`null`)); err != nil {
		t.Fatal(err)
	}
	m := fixtureManifest("console")
	m.ConfigSchema = schema
	raw, _ := json.Marshal(m)
	if _, err := plugin.Admit(raw, []byte("executable"), plugin.Approval{OwnerID: "owner", ManifestDigest: plugin.Digest(raw)}); err != nil {
		t.Fatal(err)
	}
}

func TestSchemaIsStrictAndBounded(t *testing.T) {
	s := plugin.Schema{Type: "object", Required: []string{"name"}, Properties: map[string]plugin.Schema{"name": {Type: "string", MaxLength: 4}}}
	for _, raw := range []string{`{}`, `{"name":"abcde"}`, `{"name":"ok","secret":"no"}`, `{"name":1}`} {
		if s.Validate(json.RawMessage(raw)) == nil {
			t.Fatal("accepted", raw)
		}
	}
	if err := s.Validate(json.RawMessage(`{"name":"ok"}`)); err != nil {
		t.Fatal(err)
	}
}
