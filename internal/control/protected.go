package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/JohanSardar/lamp/pkg/api"
)

// invokeProtected is the connection adapter integration seam for T17. An absent
// adapter fails closed; neither CLI nor MCP can supply executable adapter code.
func (s *Service) invokeProtected(w http.ResponseWriter, r *http.Request, requestID string, in map[string]any) {
	a := requestCaller(r)
	var input api.ConnectionInvokeInput
	assign(in, &input)
	canonical := input
	canonical.WaitMs = nil
	raw, _ := json.Marshal(canonical)
	hash := bearerHash("connection_invoke\n" + string(raw))
	key := a.Principal + "\nconnection_invoke\n" + input.IdempotencyKey
	s.readLock()
	if s.broken || !s.can(a, "connection.invoke") {
		s.mu.Unlock()
		writeError(w, requestID, "SCOPE_DENIED", "protected authority unavailable", nil)
		return
	}
	if id, ok := s.keys[key]; ok {
		rec := s.journal.Operations[id]
		if rec.Hash != hash {
			s.mu.Unlock()
			writeError(w, requestID, "IDEMPOTENCY_CONFLICT", "key already used with different inputs", pointer(id))
			return
		}
		s.mu.Unlock()
		s.respond(w, r, requestID, id, true, input.WaitMs)
		return
	}
	if len(s.journal.Operations) >= maxOperations {
		s.mu.Unlock()
		writeError(w, requestID, "CONTROL_QUOTA_EXCEEDED", "operation retention limit reached", nil)
		return
	}
	adapter, ok := s.protected[input.ConnectionId]
	if !ok || adapter.ValidateInvocation == nil || adapter.Execute == nil {
		s.mu.Unlock()
		writeError(w, requestID, "NOT_FOUND", "protected adapter not installed", nil)
		return
	}
	bound, err := adapter.ValidateInvocation(input)
	approvalBefore := s.journal.Control.Approvals[value(input.ApprovalId)]
	if err == nil {
		bound = cloneBoundAction(bound)
	}
	if err == nil {
		if bound.Plan.ResourceRef == nil || bound.Plan.ResourceRef.Id != input.ConnectionId || bound.ResourceRevision != input.ExpectedRevision {
			err = reject("REVISION_CONFLICT", "protected resource revision changed")
		} else if !s.protectedScope(a, bound) {
			err = reject("SCOPE_DENIED", "protected resource outside scope")
		} else {
			err = s.consumeApproval(a, value(input.ApprovalId), bound)
		}
	}
	if err != nil {
		s.mu.Unlock()
		code := "INVALID_ARGUMENT"
		var ce *coreError
		if errors.As(err, &ce) {
			code = ce.code
		}
		writeError(w, requestID, code, err.Error(), nil)
		return
	}
	id := s.resourceID("op")
	now := timestamp()
	ref := *bound.Plan.ResourceRef
	refs := []api.ResourceRef{ref}
	operation := api.Operation{Id: id, Kind: "operation", FleetId: "fl_local", OwnerId: a.Principal, Revision: 1, CreatedAt: now, UpdatedAt: now, State: "accepted", Labels: api.StringMap{}, Visibility: "private", OperationName: pointer("connection_invoke"), ResourceIds: &refs, RequestHash: &hash, AcceptedAt: &now, CleanupState: pointer(api.OperationCleanupStateNotNeeded)}
	rec := &record{Operation: operation, Key: key, Hash: hash, Resource: ref, Caller: a}
	s.journal.Operations[id] = rec
	s.keys[key] = id
	s.done[id] = make(chan struct{})
	if err := s.save("$control", id); err != nil {
		if errors.Is(err, errAuthorityConflict) {
			// No local write or protected dispatch occurred on a definite CAS
			// rejection. Restore consumption and discard the staged admission.
			s.journal.Control.Approvals[value(input.ApprovalId)] = approvalBefore
			delete(s.journal.Operations, id)
			delete(s.keys, key)
			delete(s.done, id)
			s.mu.Unlock()
			writeError(w, requestID, "REVISION_CONFLICT", "fleet authority changed; retry the same request", nil)
			return
		}
		s.broken = true
		s.mu.Unlock()
		writeError(w, requestID, "DATA_UNAVAILABLE", "cannot commit approval consumption and admission", pointer(id))
		return
	}
	s.wg.Add(1)
	go s.executeProtected(id, input, bound, adapter)
	s.mu.Unlock()
	s.respond(w, r, requestID, id, false, input.WaitMs)
}
func (s *Service) protectedScope(a caller, b BoundAction) bool {
	sb, ok := s.journal.Sandboxes[b.SandboxID]
	if !ok || b.PrincipalID != a.Principal || !s.own(a, sb.OwnerId) || value(sb.Generation) != b.Generation {
		return false
	}
	for _, sc := range s.scopes(a) {
		if !has(sc.SandboxIds, sb.Id) || !has(sc.RecipeIds, value(sb.RecipeId)) || !has(sc.HostIds, sandboxHost(sb)) {
			return false
		}
		if sc.ResourceIds != nil {
			found := false
			for _, r := range *sc.ResourceIds {
				if b.Plan.ResourceRef != nil && r == *b.Plan.ResourceRef {
					found = true
				}
			}
			if !found {
				return false
			}
		}
	}
	return true
}
func (s *Service) executeProtected(id string, in api.ConnectionInvokeInput, bound BoundAction, adapter ProtectedAction) {
	defer s.wg.Done()
	ctx, cancel := context.WithTimeout(s.context(), s.worker.config.Timeout)
	defer cancel()
	s.readLock()
	rec := s.journal.Operations[id]
	if s.broken || rec.Operation.State == "canceled" {
		close(s.done[id])
		s.mu.Unlock()
		return
	}
	checked, err := adapter.ValidateInvocation(in)
	if err == nil {
		checked = cloneBoundAction(checked)
	}
	if err == nil && (!s.can(rec.Caller, "connection.invoke") || !s.protectedScope(rec.Caller, checked) || digest(checked) != digest(bound)) {
		err = reject("SCOPE_DENIED", "protected authority or action changed before dispatch")
	}
	if approval, ok := s.journal.Control.Approvals[value(in.ApprovalId)]; err == nil && (!ok || approval.State != "consumed" || s.expired(approval.ExpiresAt)) {
		err = reject("APPROVAL_REQUIRED", "approval expired before dispatch")
	}
	if err == nil {
		rec.Operation.State = "running"
		rec.Operation.Revision++
		rec.Operation.Phase = pointer("protected_effect")
		rec.Operation.UpdatedAt = timestamp()
		if e := s.save(id); e != nil {
			s.broken = true
			close(s.done[id])
			s.mu.Unlock()
			return
		}
	}
	s.mu.Unlock()
	var result api.AdapterDocument
	dispatched := err == nil
	if dispatched {
		result, err = runProtected(ctx, adapter, bound)
	}
	s.readLock()
	defer s.mu.Unlock()
	rec = s.journal.Operations[id]
	rec.Operation.State = "succeeded"
	rec.Operation.CompletedAt = pointer(timestamp())
	rec.Operation.UpdatedAt = timestamp()
	rec.Operation.Revision++
	if err != nil {
		code := api.ErrorCode("SCOPE_DENIED")
		rec.Operation.State = "failed"
		var ce *coreError
		if errors.As(err, &ce) {
			code = api.ErrorCode(ce.code)
		}
		if dispatched {
			rec.Operation.State = "outcome_unknown"
			code = "OUTCOME_UNKNOWN"
		}
		rec.Operation.Error = &api.ErrorDetail{Code: code, Message: err.Error(), OperationId: &id}
	} else {
		rec.Operation.Outcome = &api.Outcome{Result: &result}
	}
	if e := s.save(id); e != nil {
		s.broken = true
	}
	close(s.done[id])
}
func runProtected(ctx context.Context, adapter ProtectedAction, bound BoundAction) (result api.AdapterDocument, err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("protected adapter panicked; outcome unknown")
		}
	}()
	result, err = adapter.Execute(ctx, bound)
	if err == nil && !json.Valid(result) {
		err = errors.New("protected adapter returned invalid outcome")
	}
	return
}

func cloneBoundAction(bound BoundAction) BoundAction {
	raw, _ := json.Marshal(bound)
	var clone BoundAction
	json.Unmarshal(raw, &clone)
	return clone
}
