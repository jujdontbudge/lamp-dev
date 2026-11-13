package control

import (
	"lamp.eilmer.si/pkg/api"
	"sort"
	"strings"
)

// A committed revocation receipt is distinct from host enforcement. Only an
// observation of the current authority and released resources acknowledges it.
func (s *Service) authorityEnforced() bool {
	for _, sb := range s.journal.Sandboxes {
		if s.sandboxCharged(sb) && s.sandboxMustStop(sb) {
			return false
		}
	}
	return true
}
func (s *Service) revocationOperation(op api.Operation) api.Operation {
	name := value(op.OperationName)
	if !(name == "grant_delete" || name == "grant_revoke" || name == "key_delete" || name == "key_rotate" || strings.HasPrefix(name, "membership_") || name == "principal_delete" || name == "principal_update") {
		return op
	}
	complete := true
	enforcers := []api.EnforcerStatus{}
	if s.fleet == nil {
		ack := api.EnforcerStatusAcknowledgmentPending
		if s.authorityEnforced() {
			ack = api.EnforcerStatusAcknowledgmentAcknowledged
		} else {
			complete = false
		}
		enforcers = append(enforcers, api.EnforcerStatus{HostId: pointer(s.hostID()), Enforcer: pointer(api.EnforcerStatusEnforcerSupervisor), Acknowledgment: &ack})
	} else {
		f := s.fleet
		f.mu.Lock()
		defer f.mu.Unlock()
		ids := []string{}
		for id := range f.peers {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			obs := f.observations[id]
			ack := api.EnforcerStatusAcknowledgmentPending
			if obs.ControlDigest == digest(s.journal.Control) && obs.AuthorityEnforced {
				ack = api.EnforcerStatusAcknowledgmentAcknowledged
			} else {
				complete = false
			}
			e := api.EnforcerStatus{HostId: pointer(id), Enforcer: pointer(api.EnforcerStatusEnforcerSupervisor), Acknowledgment: &ack}
			if obs.AuthorityExpiresAt != "" {
				e.AuthorityExpiresAt = &obs.AuthorityExpiresAt
			}
			if ack == api.EnforcerStatusAcknowledgmentAcknowledged {
				e.AcknowledgedAt = &obs.At
			} else {
				residual := []api.ResidualCapability{{CapabilityKind: pointer("offline_authority"), ExpiresAt: e.AuthorityExpiresAt, RevokeState: pointer(api.ResidualCapabilityRevokeStatePending)}}
				e.ResidualCapabilities = &residual
			}
			enforcers = append(enforcers, e)
		}
	}
	op.RevocationStatus = &api.RevocationStatus{Complete: &complete, Enforcers: &enforcers}
	return op
}
