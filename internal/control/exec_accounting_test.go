package control

import (
	"testing"
	"time"

	"github.com/JohanSardar/lamp/pkg/api"
)

func TestExecAuthorityLostBeforeDispatchReleasesCharge(t *testing.T) {
	for _, invalidation := range []string{"expired", "revoked"} {
		t.Run(invalidation, func(t *testing.T) {
			s, err := Open(storeConfig(t))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			owner := s.journal.Control.OwnerID
			sb := newSandbox()
			sb.OwnerId, sb.State = owner, "ready"
			s.journal.Sandboxes[sb.Id] = sb
			for _, id := range []string{"admitting", "replacement"} {
				s.journal.Control.Keys[id] = storedKey{Key: api.Key{Id: id, State: "active", PrincipalId: &owner, ExpiresAt: pointer(time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)), Scope: &api.Scope{Limits: &api.Limits{Concurrency: pointer(uint64(1))}}}}
			}
			admitting := caller{Principal: owner, KeyID: "admitting"}
			replacement := caller{Principal: owner, KeyID: "replacement"}
			if !s.admissionAllowed(admitting, "exec_create", sb) {
				t.Fatal("original admission refused")
			}
			ex := api.Exec{Id: "ex_prevented", OwnerId: owner, SandboxId: &sb.Id, State: "accepted", Revision: 1}
			s.journal.Execs[ex.Id] = ex
			rec := &record{Caller: admitting, Resource: api.ResourceRef{Kind: "exec", Id: ex.Id}, Operation: api.Operation{Id: "op_prevented", OwnerId: owner, State: "accepted", OperationName: pointer("exec_create"), Revision: 1}}
			s.journal.Operations[rec.Operation.Id] = rec
			s.done[rec.Operation.Id] = make(chan struct{})
			s.starting[sb.Id] = 1
			if s.admissionAllowed(replacement, "exec_create", sb) {
				t.Fatal("accepted exec was not charged")
			}
			if err := s.save(rec.Operation.Id, ex.Id); err != nil {
				t.Fatal(err)
			}
			// Deterministically place invalidation between durable admission and
			// the real executor's authority check. /unused is never dispatched.
			key := s.journal.Control.Keys[admitting.KeyID]
			if invalidation == "expired" {
				key.Key.ExpiresAt = pointer(time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano))
			} else {
				key.Key.State = "revoked"
			}
			s.journal.Control.Keys[admitting.KeyID] = key
			s.wg.Add(1)
			s.execute(rec.Operation.Id, sb.Id)
			if rec.Operation.State != "failed" || value(rec.Operation.CleanupState) != "complete" || rec.Operation.Error.Code != "SCOPE_DENIED" {
				t.Fatalf("unexpected prevented outcome: %+v", rec.Operation)
			}
			if !s.admissionAllowed(replacement, "exec_create", sb) {
				t.Fatalf("undispatched exec still charges replacement key: %+v", s.journal.Execs[ex.Id])
			}
			if s.journal.Execs[ex.Id].State != "failed" || s.starting[sb.Id] != 0 {
				t.Fatal("prevented exec retains live state or start fence")
			}
		})
	}
}
