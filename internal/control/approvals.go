package control

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"lamp.eilmer.si/pkg/api"
)

// BoundAction is independently constructed by an installed enforcing adapter.
// ResourceRevision fences settings changes even if the sandbox generation stays.
// No secret values may appear in Plan; broker-owned keyed hashes belong there.
type BoundAction struct {
	PrincipalID      string         `json:"principal_id"`
	TaskID           string         `json:"task_id"`
	SandboxID        string         `json:"sandbox_id"`
	Generation       uint64         `json:"generation"`
	ResourceRevision uint64         `json:"resource_revision"`
	Plan             api.ActionPlan `json:"action_plan"`
}

// ProtectedAction is a trusted adapter registration, never supplied via HTTP.
// Validators must derive exact effects and destinations from immutable adapter
// state. Execute must perform that bound effect without changing the plan.
type ProtectedAction struct {
	Describe           func() BoundAction
	ValidateApproval   func(api.ApprovalCreateInput) (BoundAction, error)
	ValidateInvocation func(api.ConnectionInvokeInput) (BoundAction, error)
	Execute            func(context.Context, BoundAction) (api.AdapterDocument, error)
}

func (s *Service) approvalMutation(a caller, op api.OperationDescriptor, in map[string]any) (api.ResourceRef, map[string]any, *string, []string, error) {
	c := s.journal.Control
	ref := api.ResourceRef{Kind: "approval"}
	fail := func(code, message string) (api.ResourceRef, map[string]any, *string, []string, error) {
		return ref, nil, nil, nil, reject(code, message)
	}
	var approval api.Approval
	if op.Name == "approval_delete" {
		if err := acceptFields(in, "approval_id"); err != nil {
			return ref, nil, nil, nil, err
		}
		ref.Id = textField(in, "approval_id")
		approval, ok := c.Approvals[ref.Id]
		if !ok || s.role(a) != "owner" || !s.resourceAllowed(a, ref) || !s.approvalSelectors(a, approval) {
			return fail("NOT_FOUND", "approval not found")
		}
		if approval.Revision != uintField(in, "expected_revision") {
			return fail("REVISION_CONFLICT", "approval revision changed")
		}
		delete(c.Approvals, ref.Id)
		return ref, map[string]any{"approval_id": ref.Id}, nil, nil, nil
	}
	switch op.Name {
	case "approval_create":
		if err := acceptFields(in, "principal_id task_id sandbox_id generation resource_ref action_plan destination expires_at"); err != nil {
			return ref, nil, nil, nil, err
		}
		var input api.ApprovalCreateInput
		assign(in, &input)
		if input.PrincipalId == nil {
			input.PrincipalId = pointer(a.Principal)
		}
		if *input.PrincipalId != a.Principal {
			return fail("SCOPE_DENIED", "approval principal mismatch")
		}
		sb, ok := s.journal.Sandboxes[input.SandboxId]
		if !ok || sb.OwnerId != *input.PrincipalId || value(sb.Generation) != input.Generation {
			return fail("SCOPE_DENIED", "approval sandbox generation mismatch")
		}
		adapter, ok := s.protected[input.ResourceRef.Id]
		if !ok || adapter.ValidateApproval == nil {
			return fail("INVALID_ARGUMENT", "no enforcing adapter registered for resource")
		}
		bound, err := adapter.ValidateApproval(input)
		if err == nil {
			bound = cloneBoundAction(bound)
		}
		if err != nil {
			return fail("INVALID_ARGUMENT", err.Error())
		}
		if !s.protectedScope(a, bound) {
			return fail("SCOPE_DENIED", "protected action outside caller selectors")
		}
		if bound.PrincipalID != *input.PrincipalId || bound.TaskID != input.TaskId || bound.SandboxID != input.SandboxId || bound.Generation != input.Generation || bound.Plan.ResourceRef == nil || *bound.Plan.ResourceRef != input.ResourceRef || bound.Plan.AdapterDigest == nil || bound.Plan.Operation == nil || bound.Plan.Destination == nil {
			return fail("INVALID_ARGUMENT", "adapter context mismatch")
		}
		if digest(input.ActionPlan) != digest(bound.Plan) || input.Destination == nil || digest(input.Destination) != digest(bound.Plan.Destination) {
			return fail("INVALID_ARGUMENT", "proposed effect differs from adapter plan")
		}
		raw, _ := json.Marshal(bound.Plan)
		if len(raw) > 64<<10 {
			return fail("INVALID_ARGUMENT", "action plan exceeds 64 KiB")
		}
		expires, err := time.Parse(time.RFC3339Nano, input.ExpiresAt)
		if err != nil || !time.Now().Before(expires) || expires.After(time.Now().Add(time.Minute)) {
			return fail("INVALID_ARGUMENT", "approval TTL must be positive and at most 60 seconds")
		}
		m := newResource("approval", c.OwnerID)
		m["id"] = s.resourceID("approval")
		m["state"] = "pending"
		m["principal_id"] = bound.PrincipalID
		m["task_id"] = bound.TaskID
		m["sandbox_id"] = bound.SandboxID
		m["generation"] = bound.Generation
		m["resource_ref"] = input.ResourceRef
		m["action_plan"] = bound.Plan
		m["destination"] = bound.Plan.Destination
		m["expires_at"] = input.ExpiresAt
		m["action_digest"] = digest(struct {
			Bound  BoundAction
			Expiry string
		}{bound, input.ExpiresAt})
		assign(m, &approval)
	case "approval_approve", "approval_deny", "approval_revoke":
		if err := acceptFields(in, "approval_id action_digest reason"); err != nil {
			return ref, nil, nil, nil, err
		}
		if s.role(a) != "owner" {
			return fail("SCOPE_DENIED", "fleet owner approval required")
		}
		var ok bool
		approval, ok = c.Approvals[textField(in, "approval_id")]
		if !ok {
			return fail("NOT_FOUND", "approval not found")
		}
		if !s.resourceAllowed(a, api.ResourceRef{Kind: "approval", Id: approval.Id}) || !s.approvalSelectors(a, approval) {
			return fail("NOT_FOUND", "approval not found")
		}
		if approval.Revision != uintField(in, "expected_revision") {
			return fail("REVISION_CONFLICT", fmt.Sprintf("current revision is %d", approval.Revision))
		}
		if s.expired(approval.ExpiresAt) {
			return fail("APPROVAL_REQUIRED", "approval expired")
		}
		switch op.Name {
		case "approval_approve":
			if approval.State != "pending" || textField(in, "action_digest") != value(approval.ActionDigest) {
				return fail("APPROVAL_REQUIRED", "approval plan or state mismatch")
			}
			approval.State = "approved"
		case "approval_deny":
			if approval.State != "pending" {
				return fail("APPROVAL_REQUIRED", "approval no longer pending")
			}
			approval.State = "denied"
		case "approval_revoke":
			if approval.State != "pending" && approval.State != "approved" {
				return fail("APPROVAL_REQUIRED", "approval already terminal")
			}
			approval.State = "revoked"
		}
		approval.Revision++
		approval.UpdatedAt = timestamp()
	default:
		return fail("NOT_FOUND", "operation not installed")
	}
	c.Approvals[approval.Id] = approval
	ref.Id = approval.Id
	return ref, map[string]any{"approval_id": approval.Id}, nil, nil, nil
}

func (s *Service) approvalSelectors(a caller, approval api.Approval) bool {
	sb, ok := s.journal.Sandboxes[value(approval.SandboxId)]
	if !ok {
		return false
	}
	for _, scope := range s.scopes(a) {
		if !has(scope.SandboxIds, sb.Id) || !has(scope.RecipeIds, value(sb.RecipeId)) || !has(scope.HostIds, sandboxHost(sb)) {
			return false
		}
	}
	return true
}

// consumeApproval runs under the same journal mutex as protected admission.
// The caller must include $control in that admission's durable write set before
// dispatch; a crash then leaves consumption and the effect outcome unknown.
func (s *Service) consumeApproval(a caller, id string, bound BoundAction) error {
	approval, ok := s.journal.Control.Approvals[id]
	if !ok || approval.State != "approved" || s.expired(approval.ExpiresAt) || value(approval.PrincipalId) != a.Principal || bound.PrincipalID != a.Principal {
		return reject("APPROVAL_REQUIRED", "approved exact action required")
	}
	sb, ok := s.journal.Sandboxes[bound.SandboxID]
	if !ok || !s.current(a) || !s.own(a, sb.OwnerId) || value(sb.Generation) != bound.Generation {
		return reject("APPROVAL_REQUIRED", "sandbox generation changed")
	}
	expected := digest(struct {
		Bound  BoundAction
		Expiry string
	}{bound, value(approval.ExpiresAt)})
	if expected != value(approval.ActionDigest) {
		return reject("APPROVAL_REQUIRED", "action, destination, task or revision changed")
	}
	approval.State = "consumed"
	approval.ConsumedAt = pointer(timestamp())
	approval.Revision++
	approval.UpdatedAt = timestamp()
	s.journal.Control.Approvals[id] = approval
	return nil
}
