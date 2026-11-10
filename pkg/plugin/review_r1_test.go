package plugin_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/JohanSardar/lamp/pkg/plugin"
)

func TestCaseExactManifestFields(t *testing.T) {
	m := fixtureManifest("console")
	m.Permissions = []string{}
	raw, _ := json.Marshal(m)
	for _, change := range []struct{ old, new string }{
		{`"permissions":[]`, `"permissions":[],"Permissions":["console.use"]`},
		{`"memory_bytes":67108864`, `"memory_bytes":67108864,"Memory_bytes":8388608`},
		{`"config_schema":{"type":"object"}`, `"config_schema":{"type":"object","Properties":{"extra":{"type":"string"}}}`},
		{`"result":{"type":"object"}`, `"result":{"type":"object","Type":"string"}`},
	} {
		t.Run(change.new, func(t *testing.T) {
			bad := []byte(strings.Replace(string(raw), change.old, change.new, 1))
			if string(bad) == string(raw) {
				t.Fatal("bad regression fixture")
			}
			p, err := plugin.Admit(bad, []byte("executable"), plugin.Approval{OwnerID: "owner", ManifestDigest: plugin.Digest(bad)})
			if err == nil {
				t.Fatalf("case alias admitted: %+v", p.Manifest())
			}
		})
	}
	// Schema property names are application data, not struct field aliases.
	m.ConfigSchema = plugin.Schema{Type: "object", Properties: map[string]plugin.Schema{"Permissions": {Type: "string"}}}
	raw, _ = json.Marshal(m)
	if _, err := plugin.Admit(raw, []byte("executable"), plugin.Approval{OwnerID: "owner", ManifestDigest: plugin.Digest(raw)}); err != nil {
		t.Fatal(err)
	}
}

func TestExactNumericSchemaValidation(t *testing.T) {
	for _, tc := range []struct {
		schema, value string
		valid         bool
	}{
		{`{"type":"integer"}`, `1.00000000000000001`, false},
		{`{"type":"integer"}`, `-1.00000000000000001`, false},
		{`{"type":"integer","maximum":9007199254740992}`, `9007199254740993`, false},
		{`{"type":"integer","maximum":9007199254740993}`, `9007199254740993`, true},
		{`{"type":"integer","minimum":-9007199254740992}`, `-9007199254740993`, false},
		{`{"type":"number","maximum":0.1}`, `0.10000000000000001`, false},
		{`{"type":"integer"}`, `1.00000000000000000`, true},
		{`{"type":"integer"}`, `12.5e1`, true},
		{`{"type":"integer"}`, `125e-1`, false},
		{`{"type":"integer"}`, `1e1000000000`, true},
		{`{"type":"number","maximum":1e1000000000}`, `2e1000000000`, false},
		{`{"type":"number","minimum":1e-1000000000}`, `2e-1000000000`, true},
		{`{"type":"integer"}`, `-0.000e-1000000000`, true},
	} {
		t.Run(tc.schema+"/"+tc.value, func(t *testing.T) {
			var s plugin.Schema
			if err := json.Unmarshal([]byte(tc.schema), &s); err != nil {
				t.Fatal(err)
			}
			err := s.Validate(json.RawMessage(tc.value))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, want %v (%v)", err == nil, tc.valid, err)
			}
		})
	}
}
