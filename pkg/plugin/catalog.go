package plugin

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
)

// Adapter is a publication snapshot. Its unexported admitted bytes, not mutable
// exported discovery fields, drive configuration validation. The control core
// persists/publicizes these records and owns their authorization/lifecycle.
type Adapter struct {
	ID               string            `json:"id"`
	PluginID         string            `json:"plugin_id"`
	Type             string            `json:"type"`
	Digest           string            `json:"digest"`
	ManifestDigest   string            `json:"manifest_digest"`
	SchemaDigest     string            `json:"schema_digest"`
	ConfigSchema     Schema            `json:"config_schema"`
	OperationSchemas map[string]Schema `json:"operation_schemas"`
	ResultSchemas    map[string]Schema `json:"result_schemas"`
	Compatibility    Compatibility     `json:"compatibility"`
	Certification    string            `json:"certification"`
	EvidenceRef      string            `json:"evidence_ref"`
	admitted         []byte
}

func (p *Package) Adapter(pluginID string) Adapter {
	m := p.Manifest()
	config, _ := json.Marshal(m.ConfigSchema)
	a := Adapter{PluginID: pluginID, Type: m.Type, Digest: m.Digest, ManifestDigest: p.digest, SchemaDigest: Digest(config), ConfigSchema: m.ConfigSchema, OperationSchemas: map[string]Schema{}, ResultSchemas: map[string]Schema{}, Compatibility: m.Compatibility, Certification: "unqualified"}
	for n, s := range m.Methods {
		a.OperationSchemas[n] = s.Input
		a.ResultSchemas[n] = s.Result
	}
	identity, _ := json.Marshal(a)
	a.ID = "ad_" + Digest(identity)
	a.admitted, _ = json.Marshal(a)
	return a
}

type ConfigRef struct {
	ConfigurationID string `json:"configuration_id"`
	Digest          string `json:"digest"`
}
type configDocument struct {
	ID                   string          `json:"id"`
	PluginID             string          `json:"plugin_id"`
	AdapterID            string          `json:"adapter_id"`
	SchemaDigest         string          `json:"schema_digest"`
	Document             json.RawMessage `json:"document"`
	CredentialSourceRefs []string        `json:"credential_source_refs"`
}
type Configuration struct {
	mu       sync.Mutex
	document configDocument
	digest   string
	deleted  bool
}

func (a Adapter) Configure(id, schemaDigest string, document json.RawMessage, refs []string) (*Configuration, error) {
	var admitted Adapter
	if len(a.admitted) == 0 || decode(a.admitted, &admitted) != nil {
		return nil, errors.New("unadmitted adapter")
	}
	if !boundedID(id) || schemaDigest != admitted.SchemaDigest || len(document) > 64<<10 {
		return nil, errors.New("invalid configuration identity, schema or size")
	}
	if err := admitted.ConfigSchema.Validate(document); err != nil {
		return nil, err
	}
	if len(refs) > 64 {
		return nil, errors.New("too many credential sources")
	}
	refs = slices.Clone(refs)
	slices.Sort(refs)
	for i, r := range refs {
		if !boundedID(r) || !strings.HasPrefix(r, "cr_") || (i > 0 && refs[i-1] == r) {
			return nil, errors.New("invalid credential-source reference")
		}
	}
	// Canonicalize objects before deriving an immutable document digest. No
	// plaintext secret retrieval occurs here. Core must resolve/authorize refs.
	var v any
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	if err := decoder.Decode(&v); err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	d := configDocument{ID: id, PluginID: admitted.PluginID, AdapterID: admitted.ID, SchemaDigest: schemaDigest, Document: canonical, CredentialSourceRefs: refs}
	raw, _ := json.Marshal(d)
	return &Configuration{document: d, digest: Digest(raw)}, nil
}
func (c *Configuration) Ref() ConfigRef {
	c.mu.Lock()
	defer c.mu.Unlock()
	return ConfigRef{ConfigurationID: c.document.ID, Digest: c.digest}
}
func (c *Configuration) ValidateRef(ref ConfigRef) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.deleted || !boundedID(c.document.ID) || !ValidDigest(c.digest) || ref.ConfigurationID != c.document.ID || ref.Digest != c.digest {
		return errors.New("configuration not ready or digest mismatch")
	}
	return nil
}
func (c *Configuration) Delete() { c.mu.Lock(); defer c.mu.Unlock(); c.deleted = true }
