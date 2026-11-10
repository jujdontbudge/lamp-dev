package control

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JohanSardar/lamp/pkg/api"
)

func TestConsumedApprovalExpiryStillPreventsDispatch(t *testing.T) {
	s, err := Open(storeConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sb := newSandbox()
	s.journal.Sandboxes[sb.Id] = sb
	ref := api.ResourceRef{Kind: "connection", Id: "cn_fixture"}
	bound := BoundAction{PrincipalID: "pr_local", SandboxID: sb.Id, Generation: 1, ResourceRevision: 1, Plan: api.ActionPlan{ResourceRef: &ref}}
	id := "op_expired_consumed"
	s.journal.Operations[id] = &record{Caller: caller{Principal: "pr_local"}, Operation: api.Operation{Id: id, State: "accepted"}}
	s.journal.Control.Approvals["ap_expired"] = api.Approval{Id: "ap_expired", State: "consumed", ExpiresAt: pointer(time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano))}
	s.done[id] = make(chan struct{})
	called := false
	adapter := ProtectedAction{ValidateInvocation: func(api.ConnectionInvokeInput) (BoundAction, error) { return bound, nil }, Execute: func(context.Context, BoundAction) (api.AdapterDocument, error) {
		called = true
		return api.AdapterDocument(`{}`), nil
	}}
	s.wg.Add(1)
	s.executeProtected(id, api.ConnectionInvokeInput{ApprovalId: pointer("ap_expired")}, bound, adapter)
	if called || s.journal.Operations[id].Operation.State != "failed" {
		t.Fatal("expired consumed approval dispatched an effect")
	}
}

func TestCancelBeforeDispatchAndTerminalReplay(t *testing.T) {
	config := storeConfig(t)
	s, e := Open(config)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	sb := newSandbox()
	now := timestamp()
	s.journal.Sandboxes[sb.Id] = sb
	op := api.Operation{Id: "op_pending", Kind: "operation", FleetId: "fl_local", OwnerId: "pr_local", Revision: 1, State: "accepted", CreatedAt: now, UpdatedAt: now, Labels: api.StringMap{}, Visibility: "private", OperationName: pointer("sandbox_create"), ResourceIds: &[]api.ResourceRef{{Kind: "sandbox", Id: sb.Id}}}
	s.journal.Operations[op.Id] = &record{Operation: op, Resource: api.ResourceRef{Kind: "sandbox", Id: sb.Id}, Key: "seed", Hash: "seed"}
	if e := s.save(op.Id, sb.Id); e != nil {
		t.Fatal(e)
	}
	invoke := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/operations/"+op.Id+":cancel", strings.NewReader(`{}`))
		r.Header.Set("Idempotency-Key", "cancel-once")
		r.Header.Set("If-Match", "1")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	first := invoke()
	if first.Code != 200 {
		t.Fatal(first.Body.String())
	}
	var accepted api.MutationEnvelope
	json.Unmarshal(first.Body.Bytes(), &accepted)
	if s.journal.Operations[op.Id].Operation.State != "canceled" || value(s.journal.Operations[op.Id].Operation.CancelEffect) != "prevented" || s.journal.Sandboxes[sb.Id].State != "failed" {
		t.Fatal("cancellation did not prevent uncommitted effects")
	}
	second := invoke()
	var replay api.MutationEnvelope
	json.Unmarshal(second.Body.Bytes(), &replay)
	if second.Code != 200 || !replay.Replayed || replay.OperationId != accepted.OperationId {
		t.Fatal("cancel replay changed operation")
	}
}
func TestExpiryAndCursorRetentionBoundaries(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Second).UTC().Format(time.RFC3339Nano)
	future := now.Add(time.Hour).UTC().Format(time.RFC3339Nano)
	at := now.UTC().Format(time.RFC3339Nano)
	if !expired(&past) || !expired(&at) || expired(&future) {
		t.Fatal("expiry boundary")
	}
	if !earlier("2026-11-10T00:00:00Z", "2026-11-10T00:00:00.1Z") {
		t.Fatal("fractional expiry comparison widened authority")
	}
	config := storeConfig(t)
	s, e := Open(config)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	s.journal.Events = []storedEvent{{Owner: "pr_local", Event: api.Event{Sequence: 1, OccurredAt: now.Add(-eventRetention - time.Second).UTC().Format(time.RFC3339Nano)}}, {Owner: "pr_local", Event: api.Event{Sequence: 2, OccurredAt: future}}}
	s.pruneEvents()
	if len(s.journal.Events) != 1 || s.journal.EventFloor != 1 {
		t.Fatal("retention floor loses replay position")
	}
	a := caller{Principal: "pr_local"}
	cursor := s.cursor(a, "events", "2")
	if pos, e := s.readCursor(a, "events", cursor); e != nil || pos != "2" {
		t.Fatal("signed cursor")
	}
	if _, e := s.readCursor(caller{Principal: "foreign"}, "events", cursor); e == nil {
		t.Fatal("cross-owner cursor accepted")
	}
}

func TestActionBindingSnapshotsAndCanonicalObjectOrder(t *testing.T) {
	value := "original"
	operation := "write"
	raw := api.AdapterDocument(`{"b":2,"a":1}`)
	bound := BoundAction{PrincipalID: "p", Plan: api.ActionPlan{Operation: &operation, Arguments: &raw, AdapterDigest: &value}}
	clone := cloneBoundAction(bound)
	value = "changed"
	raw[2] = 'x'
	if valueOf := valueString(clone.Plan.AdapterDigest); valueOf != "original" {
		t.Fatal("bound action aliases mutable adapter data")
	}
	a := map[string]any{"plan": json.RawMessage(`{"a":1,"b":2}`)}
	b := map[string]any{"plan": json.RawMessage(`{"b":2,"a":1}`)}
	if digest(a) != digest(b) {
		t.Fatal("plan object order changes canonical action digest")
	}
}
func valueString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
