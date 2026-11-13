package control

import (
	"context"
	"runtime"

	"lamp.eilmer.si/internal/relayclient"
	"lamp.eilmer.si/pkg/api"
)

// RunRelay runs only the explicitly enrolled outbound carrier. The relay never
// receives the control bearer or the fleet SSH/TLS private keys.
func (s *Service) RunRelay(ctx context.Context) {
	if c := s.relayConfig; c != nil {
		_ = relayclient.HostHTTPSStatus(ctx, c.Origin, c.CredentialFile, c.SSHTarget, c.HTTPSTarget, "", s.relayConnected.Store)
	}
}

// Caller holds s.mu, just like resources.
func (s *Service) relays(a caller) []map[string]any {
	if s.relayConfig == nil || s.role(a) != "owner" {
		return nil
	}
	f := s.journal.Control.Fleet
	m := map[string]any{"id": "rl_local", "kind": "relay", "host_id": s.hostID(), "origin": s.relayConfig.Origin, "fleet_id": f.Id, "owner_id": f.OwnerId, "revision": 1, "created_at": f.CreatedAt, "updated_at": f.UpdatedAt, "visibility": "private", "labels": map[string]string{}}
	if !s.hostAllowed(a, s.hostID()) || !s.resourceAllowed(a, api.ResourceRef{Kind: "relay", Id: "rl_local"}) {
		return nil
	}
	m["state"] = "disconnected"
	if s.relayConnected.Load() {
		m["state"] = "connected"
	}
	return []map[string]any{m}
}
func (s *Service) localHosts(a caller) []map[string]any {
	if !s.hostAllowed(a, s.hostID()) {
		return nil
	}
	f := s.journal.Control.Fleet
	m := map[string]any{"id": s.hostID(), "kind": "host", "fleet_id": f.Id, "owner_id": f.OwnerId, "revision": 1, "created_at": f.CreatedAt, "updated_at": f.UpdatedAt, "state": "ready", "labels": map[string]string{}, "visibility": "private", "os": runtime.GOOS, "arch": runtime.GOARCH, "engine_ids": []string{}, "roles": []string{"control"}, "version": s.installedVersion()}
	return []map[string]any{m}
}
func (s *Service) resourceAllowedForEngine(a caller, engine string) bool {
	s.readLock()
	defer s.mu.Unlock()
	return s.resourceAllowed(a, api.ResourceRef{Kind: "engine", Id: engine})
}

// Caller holds s.mu.
func (s *Service) hostAllowed(a caller, id string) bool {
	if !s.resourceAllowed(a, api.ResourceRef{Kind: "host", Id: id}) {
		return false
	}
	for _, scope := range s.scopes(a) {
		if !has(scope.HostIds, id) {
			return false
		}
	}
	return true
}
