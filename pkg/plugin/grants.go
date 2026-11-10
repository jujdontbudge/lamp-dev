package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

type Binding struct {
	OwnerID     string    `json:"owner_id"`
	TaskID      string    `json:"task_id"`
	OperationID string    `json:"operation_id"`
	Generation  uint64    `json:"generation"`
	ParentGrant string    `json:"parent_grant"`
	Deadline    time.Time `json:"deadline"`
}

func (b Binding) valid(owner string) bool {
	return b.OwnerID == owner && boundedID(b.OwnerID) && boundedID(b.TaskID) && boundedID(b.OperationID) && boundedID(b.ParentGrant) && b.Generation > 0 && b.Deadline.After(time.Now())
}
func boundedID(s string) bool { return len(s) > 0 && len(s) <= 128 }
func (b Binding) within(p Binding) bool {
	return b.OwnerID == p.OwnerID && b.TaskID == p.TaskID && b.OperationID == p.OperationID && b.Generation == p.Generation && b.ParentGrant == p.ParentGrant && !b.Deadline.After(p.Deadline)
}

type GrantRequest struct {
	Kind       string  `json:"kind"`
	ResourceID string  `json:"resource_id"`
	Binding    Binding `json:"binding"`
}
type Grant struct {
	ID string `json:"id"`
	GrantRequest
}
type Effect struct {
	CallID     string          `json:"call_id"`
	GrantID    string          `json:"grant_id"`
	Kind       string          `json:"kind"`
	ResourceID string          `json:"resource_id"`
	Binding    Binding         `json:"binding"`
	Input      json.RawMessage `json:"input"`
}

// Authority is implemented by core/broker. Effect MUST reauthorize against
// current owner/task/policy/parent-grant state on EVERY invocation and honor ctx.
// Revoke fences external handles; FailDependent affects only this plugin's work.
// Neither hook may wait on the worker that called it. No permissive default exists.
type Authority interface {
	Effect(context.Context, Effect) (json.RawMessage, error)
	Revoke(pluginID string)
	FailDependent(pluginID, reason string)
}

func (w *Worker) Grant(r GrantRequest) (Grant, error) {
	if err := w.checkRuntime(); err != nil {
		return Grant{}, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.reason != nil {
		return Grant{}, w.reason
	}
	if !r.Binding.valid(w.pkg.owner) || !boundedID(r.ResourceID) {
		return Grant{}, errors.New("invalid grant binding")
	}
	m := w.pkg.manifest
	if !hasPermission(m, m.Type+".use") || !(r.Kind == m.Type || (r.Kind == "ram" && m.TrustClass == "engine_trusted")) {
		return Grant{}, errors.New("grant kind denied")
	}
	if len(w.grants) >= 64 {
		return Grant{}, errors.New("grant capacity reached")
	}
	g := Grant{ID: randomID(), GrantRequest: r}
	w.grants[g.ID] = g
	return g, nil
}
func hasPermission(m Manifest, p string) bool {
	for _, x := range m.Permissions {
		if x == p {
			return true
		}
	}
	return false
}

// RevokeGrant invalidates the local token immediately. External authority must
// also fence active effects (the core remains their final policy enforcement).
func (w *Worker) RevokeGrant(id string) {
	w.mu.Lock()
	delete(w.grants, id)
	for _, p := range w.pending {
		if p.grant == id {
			p.cancel()
		}
	}
	w.mu.Unlock()
}
