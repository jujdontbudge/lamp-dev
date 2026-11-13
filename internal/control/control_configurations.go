package control

import (
	"encoding/json"
	"fmt"
	"time"

	"lamp.eilmer.si/pkg/api"
)

// CI configurations select an already owner-enrolled repository. Credentials,
// executable paths and network authority remain in the private host enrollment.
func ciConfigurationSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"repository": map[string]any{"type": "string"}}, "required": []string{"repository"}}
}
func (s *Service) validCIConfiguration(plugin string, ref api.ConfigRef) bool {
	c, ok := s.journal.Control.Configurations[ref.ConfigurationId]
	return ok && c.State == "ready" && value(c.PluginId) == plugin && value(c.Digest) == ref.Digest
}
func (s *Service) ciConfigurationValid(plugin string) bool {
	p := s.journal.Control.Plugins[plugin]
	if p == nil || p["config_ref"] == nil {
		return true
	}
	var ref api.ConfigRef
	assign(object(p["config_ref"]), &ref)
	return s.validCIConfiguration(plugin, ref)
}
func (s *Service) ciConfiguredRepository(plugin, name string) bool {
	p := s.journal.Control.Plugins[plugin]
	if p == nil || p["config_ref"] == nil {
		return true
	}
	var ref api.ConfigRef
	assign(object(p["config_ref"]), &ref)
	if !s.validCIConfiguration(plugin, ref) {
		return false
	}
	c := s.journal.Control.Configurations[ref.ConfigurationId]
	var doc map[string]any
	if c.Document == nil || json.Unmarshal(*c.Document, &doc) != nil {
		return false
	}
	return textField(doc, "repository") == name
}
func (s *Service) configurationMutation(a caller, op api.OperationDescriptor, in map[string]any) (api.ResourceRef, map[string]any, *string, []string, error) {
	ref := api.ResourceRef{Kind: "configuration", Id: textField(in, "configuration_id")}
	fail := func(code, msg string) (api.ResourceRef, map[string]any, *string, []string, error) {
		return ref, nil, nil, nil, reject(code, msg)
	}
	if s.role(a) != "owner" {
		return fail("SCOPE_DENIED", "configuration management requires the owner")
	}
	c := s.journal.Control
	if op.Name == "configuration_delete" {
		if err := acceptFields(in, "configuration_id"); err != nil {
			return ref, nil, nil, nil, err
		}
		m, ok := c.Configurations[ref.Id]
		if !ok || m.State == "deleted" || !s.resourceAllowed(a, ref) {
			return fail("NOT_FOUND", "configuration not found")
		}
		if m.Revision != uintField(in, "expected_revision") {
			return fail("REVISION_CONFLICT", fmt.Sprintf("current revision is %d", m.Revision))
		}
		for _, p := range c.Plugins {
			if textField(object(p["config_ref"]), "configuration_id") == ref.Id {
				return fail("RESOURCE_IN_USE", "configuration is pinned by a plugin")
			}
		}
		m.State = "deleted"
		m.Revision++
		m.UpdatedAt = timestamp()
		c.Configurations[ref.Id] = m
	} else {
		if err := acceptFields(in, "name plugin_id adapter_id schema_digest document credential_source_refs labels"); err != nil {
			return ref, nil, nil, nil, err
		}
		for _, scope := range s.scopes(a) {
			if scope.ResourceIds != nil {
				return fail("SCOPE_DENIED", "new configuration outside resource selector")
			}
		}
		if uint64(len(c.Configurations)) >= s.quotas.Records {
			return fail("CONTROL_QUOTA_EXCEEDED", "configuration retention limit reached")
		}
		var adapter map[string]any
		for _, candidate := range s.pluginResources("adapter") {
			if candidate["id"] == in["adapter_id"] {
				adapter = candidate
				break
			}
		}
		if adapter == nil {
			return fail("NOT_FOUND", "shipped CI adapter not found")
		}
		plugin := textField(adapter, "plugin_id")
		if id := textField(in, "plugin_id"); id != "" && id != plugin {
			return fail("INVALID_ARGUMENT", "plugin does not own adapter")
		}
		if textField(in, "schema_digest") != textField(adapter, "schema_digest") {
			return fail("INVALID_ARGUMENT", "immutable adapter schema digest mismatch")
		}
		if len(stringsField(in, "credential_source_refs")) != 0 {
			return fail("INVALID_ARGUMENT", "CI credentials must be enrolled privately by the host owner")
		}
		doc, ok := in["document"].(map[string]any)
		if !ok || len(doc) != 1 || textField(doc, "repository") == "" {
			return fail("INVALID_ARGUMENT", "configuration document requires only repository")
		}
		enrolled := false
		if f := s.forges[plugin]; f != nil {
			for _, r := range f.Repos() {
				enrolled = enrolled || r.Name == textField(doc, "repository")
			}
		}
		if !enrolled {
			return fail("NOT_FOUND", "repository is not host-enrolled for this adapter")
		}
		m := newResource("configuration", c.OwnerID)
		m["state"] = "ready"
		m["plugin_id"] = plugin
		m["adapter_id"] = adapter["id"]
		m["schema_digest"] = adapter["schema_digest"]
		m["document"] = doc
		m["digest"] = digest(map[string]any{"plugin_id": plugin, "adapter_id": adapter["id"], "schema_digest": adapter["schema_digest"], "document": doc})
		if name, ok := in["name"]; ok {
			m["name"] = name
		}
		applyLabels(m, in)
		var config api.Configuration
		assign(m, &config)
		if c.Configurations == nil {
			c.Configurations = map[string]api.Configuration{}
		}
		c.Configurations[config.Id] = config
		ref.Id = config.Id
	}
	return ref, map[string]any{"configuration_id": ref.Id}, nil, nil, nil
}

// Updating a share changes metadata and may shorten its lifetime. Changing the
// recipient, resource or permissions requires explicit revocation and reissue.
func (s *Service) updateControlShare(a caller, in map[string]any) (api.ResourceRef, map[string]any, *string, []string, error) {
	ref := api.ResourceRef{Kind: "share", Id: textField(in, "share_id")}
	if err := acceptFields(in, "share_id expires_at labels"); err != nil {
		return ref, nil, nil, nil, err
	}
	m := s.journal.Control.SessionShares[ref.Id]
	session := m != nil
	if !session {
		if share, ok := s.journal.Shares[ref.Id]; ok {
			m = object(share)
		}
	}
	if m == nil || !s.own(a, textField(m, "owner_id")) || !s.resourceAllowed(a, ref) {
		return ref, nil, nil, nil, reject("NOT_FOUND", "share not found")
	}
	if uintField(m, "revision") != uintField(in, "expected_revision") {
		return ref, nil, nil, nil, reject("REVISION_CONFLICT", "share revision changed")
	}
	if textField(m, "state") != "active" || sessionExpired(m) {
		return ref, nil, nil, nil, reject("RESOURCE_IN_USE", "share is no longer active")
	}
	if expiry, ok := in["expires_at"]; ok {
		when, err := time.Parse(time.RFC3339Nano, fmt.Sprint(expiry))
		old := textField(m, "expires_at")
		if err != nil || !time.Now().Before(when) || old != "" && earlier(old, fmt.Sprint(expiry)) {
			return ref, nil, nil, nil, reject("INVALID_ARGUMENT", "share expiry may only be shortened into the future")
		}
		m["expires_at"] = expiry
	}
	applyLabels(m, in)
	editResource(m)
	if !session {
		var share api.Share
		assign(m, &share)
		s.journal.Shares[ref.Id] = share
		return ref, map[string]any{}, nil, []string{ref.Id}, nil
	}
	return ref, map[string]any{}, nil, nil, nil
}
