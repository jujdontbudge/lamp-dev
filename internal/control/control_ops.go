package control

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"lamp.eilmer.si/internal/ci"
	"lamp.eilmer.si/pkg/api"
)

// Built-in CI code ships in lampd. Host enrollment supplies credentials and
// immutable project policy; HTTP installation never accepts executable paths.
func (s *Service) ciPlugin(system string) map[string]any {
	id := ciPluginFor(system)
	m := newResource("plugin", s.journal.Control.OwnerID)
	m["id"], m["type"], m["version"] = id, "ci", "1"
	m["created_at"], m["updated_at"] = s.journal.Control.Fleet.CreatedAt, s.journal.Control.Fleet.CreatedAt
	m["state"] = "disabled"
	if s.forges[id] != nil {
		m["state"] = "enabled"
	}
	m["package_blob_id"] = "builtin:" + id
	m["digest"] = bearerHash("lamp-ci/1:" + system)
	m["adapter_ids"] = []string{strings.ReplaceAll(system, "-", "_")}
	m["labels"] = map[string]string{"runner_mode": ciRunnerMode[system], "system": system}
	if saved := s.journal.Control.Plugins[id]; saved != nil {
		for _, key := range []string{"revision", "updated_at", "state", "labels"} {
			m[key] = saved[key]
		}
	}
	return m
}
func (s *Service) ciEnabled(id string) bool {
	saved := s.journal.Control.Plugins[id]
	return s.forges[id] != nil && (saved == nil || textField(saved, "state") == "enabled")
}
func (s *Service) pluginResources(kind string) []map[string]any {
	out := []map[string]any{}
	for _, system := range ci.Systems {
		m := s.ciPlugin(system)
		if kind == "adapter" {
			p := m
			m = newResource("adapter", s.journal.Control.OwnerID)
			m["id"], m["plugin_id"], m["digest"] = strings.ReplaceAll(system, "-", "_"), p["id"], p["digest"]
			m["created_at"], m["updated_at"] = p["created_at"], p["updated_at"]
			m["certification"] = "unqualified"
		}
		out = append(out, m)
	}
	return out
}
func (s *Service) pluginMutation(a caller, op api.OperationDescriptor, in map[string]any) (api.ResourceRef, map[string]any, *string, []string, error) {
	ref := api.ResourceRef{Kind: "plugin", Id: textField(in, "plugin_id")}
	fail := func(code, msg string) (api.ResourceRef, map[string]any, *string, []string, error) {
		return ref, nil, nil, nil, reject(code, msg)
	}
	if s.role(a) != "owner" {
		return fail("SCOPE_DENIED", "plugin management requires the owner")
	}
	_, verb, _ := strings.Cut(op.Scope, ".")
	fields := "plugin_id"
	if verb == "create" {
		fields = "package_blob_id digest type labels"
	} else if verb == "update" {
		fields += " labels"
	} else if verb == "test" {
		fields += " suite_digest"
	}
	if err := acceptFields(in, fields); err != nil {
		return ref, nil, nil, nil, err
	}
	if verb == "create" {
		ref.Id = strings.TrimPrefix(textField(in, "package_blob_id"), "builtin:")
	}
	var m map[string]any
	for _, item := range s.pluginResources("plugin") {
		if textField(item, "id") == ref.Id {
			m = item
			break
		}
	}
	if m == nil {
		return fail("NOT_FOUND", "shipped CI package not found; use package_blob_id and digest from plugin list")
	}
	if !s.resourceAllowed(a, ref) {
		return fail("SCOPE_DENIED", "plugin outside key selectors")
	}
	if verb != "create" && uintField(in, "expected_revision") != uintField(m, "revision") {
		return fail("REVISION_CONFLICT", fmt.Sprintf("current revision is %d", uintField(m, "revision")))
	}
	switch verb {
	case "create":
		if textField(in, "package_blob_id") != textField(m, "package_blob_id") || textField(in, "digest") != textField(m, "digest") || textField(in, "type") != "" && textField(in, "type") != "ci" {
			return fail("INVALID_ARGUMENT", "built-in package identity or digest mismatch")
		}
		if s.forges[ref.Id] == nil {
			return fail("AUTH_TRANSPORT_UNSUPPORTED", "enroll this CI system with lampd --ci-config before enabling its plugin")
		}
		m["state"] = "enabled"
	case "delete":
		m["state"] = "revoked"
	case "update":
	case "test":
		if textField(in, "suite_digest") != "" {
			return fail("INVALID_ARGUMENT", "external test suites are not installed")
		}
		if !s.ciEnabled(ref.Id) {
			return fail("AUTH_TRANSPORT_UNSUPPORTED", "CI plugin is not enabled and enrolled")
		}
		// Check the actual private credential inputs without exposing them. Runner
		// and VM execution is demonstrated by job create, never inferred here.
		for _, repo := range s.forges[ref.Id].Repos() {
			path := repo.TokenFile
			if path == "" {
				path = s.ciConfig.TokenFile
			}
			if err := checkCICredential(path); err != nil {
				return fail("UNAUTHENTICATED", err.Error())
			}
		}
		return ref, map[string]any{}, nil, nil, nil
	default:
		return fail("NOT_FOUND", "operation not installed")
	}
	if labels, ok := in["labels"]; ok {
		m["labels"] = labels
	}
	editResource(m)
	if s.journal.Control.Plugins == nil {
		s.journal.Control.Plugins = map[string]map[string]any{}
	}
	s.journal.Control.Plugins[ref.Id] = m
	return ref, map[string]any{"plugin_id": ref.Id}, nil, nil, nil
}

func checkCICredential(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 1<<20 {
		return errors.New("CI credential must be a private regular file")
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(strings.TrimSpace(string(raw))) == 0 {
		return errors.New("CI credential unavailable or empty")
	}
	return nil
}

// The supervisor's host enrollment mints control authority. These operations
// expose and retire that enrollment; they cannot mint worker/handle grants.
func (s *Service) initControlGrant() {
	id := s.worker.config.GrantID
	if id == "" || id == "none" {
		return
	}
	c := s.journal.Control
	if c.Grants == nil {
		c.Grants = map[string]api.Grant{}
	}
	if _, ok := c.Grants[id]; ok {
		return
	}
	m := newResource("grant", c.OwnerID)
	m["id"], m["principal_id"], m["revocation_state"] = id, c.OwnerID, "active"
	m["labels"] = map[string]string{"authority": "enrolled-host-control", "host_id": s.hostID()}
	var g api.Grant
	assign(m, &g)
	c.Grants[id] = g
}
func (s *Service) controlGrantRevoked() bool {
	g, ok := s.journal.Control.Grants[s.worker.config.GrantID]
	return ok && g.State != "active"
}
func (s *Service) grantMutation(a caller, op api.OperationDescriptor, in map[string]any) (api.ResourceRef, map[string]any, *string, []string, error) {
	ref := api.ResourceRef{Kind: "grant", Id: textField(in, "grant_id")}
	fail := func(code, msg string) (api.ResourceRef, map[string]any, *string, []string, error) {
		return ref, nil, nil, nil, reject(code, msg)
	}
	if err := acceptFields(in, "grant_id reason"); err != nil {
		return ref, nil, nil, nil, err
	}
	if s.role(a) != "owner" || !s.resourceAllowed(a, ref) {
		return fail("SCOPE_DENIED", "host control grant requires owner authority")
	}
	g, ok := s.journal.Control.Grants[ref.Id]
	if !ok {
		return fail("NOT_FOUND", "enrolled host control grant not found")
	}
	if g.Revision != uintField(in, "expected_revision") {
		return fail("REVISION_CONFLICT", fmt.Sprintf("current revision is %d", g.Revision))
	}
	g.State = "disabled"
	if op.Name == "grant_delete" {
		g.State = "deleted"
	}
	g.RevocationState = pointer("revoked")
	g.Revision++
	g.UpdatedAt = timestamp()
	s.journal.Control.Grants[ref.Id] = g
	return ref, map[string]any{}, nil, nil, nil
}

// Provider reads happen outside the journal lock. Mutation admission rechecks
// authority and revision afterwards; no runner is registered by this probe.
func (s *Service) checkCIPlugin(ctx context.Context, a caller, in map[string]any) error {
	id := textField(in, "plugin_id")
	s.readLock()
	if _, replay := s.keys[a.Principal+"\nplugin_test\n"+textField(in, "idempotency_key")]; replay {
		s.mu.Unlock()
		return nil // coreHTTP performs the canonical hash and visibility checks.
	}
	enabled := s.role(a) == "owner" && s.can(a, "plugin.test") && s.ciEnabled(id) && s.resourceAllowed(a, api.ResourceRef{Kind: "plugin", Id: id})
	forge := s.forges[id]
	s.mu.Unlock()
	if !enabled || forge == nil {
		return reject("SCOPE_DENIED", "plugin test requires an enabled, enrolled CI plugin and owner authority")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for _, repo := range forge.Repos() {
		if _, err := forge.Queued(ctx, repo); err != nil {
			return reject("AUTH_TRANSPORT_UNSUPPORTED", "CI provider probe failed; check enrolled endpoint, project and private credential")
		}
	}
	return nil
}
