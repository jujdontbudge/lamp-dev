package control

import (
	"strconv"
	"time"

	"lamp.eilmer.si/pkg/api"
)

func (s *Service) validateLifecycle(a caller, l *api.Lifecycle) error {
	if l == nil {
		return nil
	}
	if l.IdleAction != nil && *l.IdleAction != "keep" && *l.IdleAction != "suspend" && *l.IdleAction != "stop" && *l.IdleAction != "delete" {
		return reject("INVALID_ARGUMENT", "invalid idle_action")
	}
	if l.IdleGraceSeconds != nil && *l.IdleGraceSeconds > maxIdleGrace && s.role(a) != "owner" {
		return reject("SCOPE_DENIED", "idle grace above 86400 s needs owner authority")
	}
	if l.SuspendedRetentionSeconds != nil && *l.SuspendedRetentionSeconds > defaultSuspendedRetention && s.role(a) != "owner" {
		return reject("SCOPE_DENIED", "suspended retention above 604800 s needs owner authority")
	}
	for _, n := range []*uint64{l.IdleGraceSeconds, l.SuspendedRetentionSeconds} {
		if n != nil && *n > uint64((365*24*time.Hour)/time.Second) {
			return reject("INVALID_ARGUMENT", "lifecycle duration exceeds one year")
		}
	}
	if l.ExpiresAt != nil {
		at, err := time.Parse(time.RFC3339Nano, *l.ExpiresAt)
		if err != nil || !at.After(s.lifeNow()) || at.Sub(s.lifeNow()) > 365*24*time.Hour {
			return reject("INVALID_ARGUMENT", "expires_at must be in the next year")
		}
	}
	return nil
}

func (s *Service) taskForBudget(a caller, id string) (api.Task, error) {
	b, ok := s.journal.Accountability.Budgets[id]
	if !ok || !s.own(a, b.OwnerId) || !s.resourceAllowed(a, lifeRef("budget", id)) || b.State != "active" || s.expired(b.DeadlineAt) {
		return api.Task{}, reject("SCOPE_DENIED", "active owned budget required")
	}
	deadline := s.enforcementNow().Add(time.Hour)
	if b.WallTimeMs != nil {
		deadline = s.enforcementNow().Add(time.Duration(*b.WallTimeMs) * time.Millisecond)
	}
	if b.DeadlineAt != nil {
		at, _ := time.Parse(time.RFC3339Nano, *b.DeadlineAt)
		if at.Before(deadline) {
			deadline = at
		}
	}
	// Shared wall-time budgets start once, just as task_create does.
	for _, t := range s.journal.Accountability.Tasks {
		if value(t.BudgetId) == id && t.DeadlineAt != nil {
			at, _ := time.Parse(time.RFC3339Nano, *t.DeadlineAt)
			if at.Before(deadline) {
				deadline = at
			}
		}
	}
	if !deadline.After(s.enforcementNow()) {
		return api.Task{}, reject("TASK_AUTHORITY_EXPIRED", "budget has expired")
	}
	m := newResource("task", a.Principal)
	m["id"] = s.resourceID("tk")
	m["budget_id"] = id
	m["deadline_at"] = deadline.UTC().Format(time.RFC3339Nano)
	m["state"] = "running"
	var t api.Task
	assign(m, &t)
	return t, nil
}

// A snapshot owns its capture independently of its source's lifetime.
func (s *Service) snapshotAllowed(a caller, snap savedSnapshot) bool {
	if !s.current(a) || !s.own(a, snap.Snapshot.OwnerId) || !s.resourceAllowed(a, lifeRef("snapshot", snap.Snapshot.Id)) {
		return false
	}
	if source, ok := s.journal.Sandboxes[value(snap.Snapshot.SandboxId)]; ok {
		return s.sandboxSelectors(a, source)
	}
	for _, sc := range s.scopes(a) {
		if !has(sc.SandboxIds, value(snap.Snapshot.SandboxId)) {
			return false
		}
	}
	return true
}

func init() { lifeHandlers["snapshot_delete"] = (*Service).planSnapshotDelete }

func (s *Service) planSnapshotDelete(a caller, in map[string]any) (lifePlan, error) {
	id := textField(in, "snapshot_id")
	snap, ok := s.journal.Snapshots[id]
	if !ok || !s.snapshotAllowed(a, snap) {
		return lifePlan{}, reject("NOT_FOUND", "snapshot not found")
	}
	if snap.Snapshot.Revision != uintField(in, "expected_revision") {
		return lifePlan{}, reject("REVISION_CONFLICT", "snapshot revision is "+strconv.FormatUint(snap.Snapshot.Revision, 10))
	}
	if snap.Snapshot.State == "capturing" {
		return lifePlan{}, reject("RESOURCE_IN_USE", "snapshot capture is in progress")
	}
	for _, sb := range s.journal.Sandboxes {
		if value(sb.SnapshotId) == id && sb.State != "deleted" && sb.State != "ready" && sb.State != "failed" {
			return lifePlan{}, reject("RESOURCE_IN_USE", "snapshot is in use")
		}
	}
	snap.Snapshot.State = "deleted"
	snap.Snapshot.Revision++
	snap.Snapshot.UpdatedAt = timestamp()
	s.journal.Snapshots[id] = snap
	if snap.CaptureID != "" {
		s.life.Discards[snap.CaptureID] = "snapshot deleted"
	}
	return lifePlan{ref: lifeRef("snapshot", id), ids: []string{id}, result: map[string]any{}}, nil
}
