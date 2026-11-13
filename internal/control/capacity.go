package control

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"lamp.eilmer.si/pkg/api"
	pb "lamp.eilmer.si/pkg/workerpb"
)

// Preflight uses the engine's actual admission path, without allocating a jail,
// a disk or a sandbox record. Allocate repeats admission when it takes ownership.
func (s *Service) preflight(parent context.Context, sb api.Sandbox, fork bool) error {
	ctx, cancel := context.WithTimeout(parent, s.worker.config.Timeout)
	defer cancel()
	deadline, _ := ctx.Deadline()
	devices, err := workerDevices(sb.Devices)
	if err != nil {
		return err
	}
	r := sb.Resources
	template := value(sb.TemplateId)
	if fork {
		template = ""
	}
	receipt, err := s.worker.call(ctx, &pb.Request{
		Context: &pb.Context{RequestId: newID("req"), OperationId: newID("op"), DeadlineMs: uint64(deadline.UnixMilli()), SandboxId: sb.Id, Generation: value(sb.Generation), GrantId: s.worker.config.GrantID, PolicyRevision: 1},
		Command: &pb.Request_Allocate{Allocate: &pb.Allocate{Resources: &pb.Resources{Vcpus: uint32(r.Vcpus), MemoryMib: r.MemoryMib, DiskGib: r.DiskGib, PidsMax: r.PidsMax}, EngineId: value(sb.EngineId), Devices: devices, ValidateOnly: true, TemplateId: template, SharedDisk: fork}},
	})
	if err == nil && receipt.State != "validated" {
		return &workerFailure{code: "ENGINE_UNQUALIFIED", message: "supervisor does not support admission preflight; update the supervisor"}
	}
	return err
}

// Release is an idempotent owner cleanup operation. It tears down even a failed
// boot; it does not depend on Stop or a guest's generation-bound execution grant.
func (s *Service) releaseSandbox(ctx context.Context, sb api.Sandbox) error {
	deadline, _ := ctx.Deadline()
	err := s.worker.perform(ctx, &pb.Request{
		Context: &pb.Context{RequestId: newID("req"), OperationId: newID("op"), DeadlineMs: uint64(deadline.UnixMilli()), SandboxId: sb.Id, Generation: value(sb.Generation), GrantId: s.worker.config.GrantID, PolicyRevision: 1},
		Command: &pb.Request_Release{Release: &pb.Release{}},
	}, "released")
	if workerNotFound(err) {
		return nil
	}
	return err
}

// A crash never replays create/start. It reaps incomplete allocations instead.
// Pending deletes wait for an engine to return, retaining capacity until Release
// witnesses cleanup. The original operation and idempotency key remain intact.
func (s *Service) recoverCapacity() {
	defer s.wg.Done()
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	// The first connection after lampd restarts also needs observation: the
	// supervisor may have restarted and reaped its volatile guests meanwhile.
	observedEpoch := uint64(0)
	for {
		epoch, connected := s.worker.connectionState()
		if epoch > 0 && !connected && s.context().Err() == nil {
			ctx, cancel := context.WithTimeout(s.context(), s.worker.config.Timeout)
			deadline, _ := ctx.Deadline()
			s.worker.call(ctx, &pb.Request{Context: &pb.Context{RequestId: newID("req"), OperationId: newID("op"), DeadlineMs: uint64(deadline.UnixMilli())}, Command: &pb.Request_Inspect{Inspect: &pb.Inspect{}}})
			cancel()
			epoch, connected = s.worker.connectionState()
		}
		reconnected := connected && epoch > observedEpoch
		if reconnected {
			observedEpoch = epoch
		}
		s.readLock()
		if s.broken {
			s.mu.Unlock()
			return
		}
		var purged []string
		for sid, sb := range s.journal.Sandboxes {
			at, err := time.Parse(time.RFC3339Nano, sb.UpdatedAt)
			if sb.State == "deleted" && s.auditReleased[sid] && !s.busy[sid] && err == nil && time.Since(at) >= 24*time.Hour {
				delete(s.journal.Sandboxes, sid)
				purged = append(purged, "$purge:"+sid)
			}
		}
		if len(purged) > 0 && s.save(purged...) != nil {
			s.broken = true
		}
		var cleanup []*record
		var reconcile [][2]string
		var observe []api.Sandbox
		for id, rec := range s.journal.Operations {
			sb, ok := s.journal.Sandboxes[rec.Resource.Id]
			if !ok || sb.State == "deleted" || s.busy[sb.Id] || s.starting[sb.Id] > 0 {
				continue
			}
			name := value(rec.Operation.OperationName)
			if reconnected && (name == "sandbox_create" || name == "sandbox_fork") && rec.Operation.State == "succeeded" && (sb.State == "ready" || sb.State == "unavailable") {
				observe = append(observe, sb)
			}
			if name == "sandbox_delete" && !terminal(rec.Operation.State) {
				s.busy[sb.Id] = true
				s.wg.Add(1)
				go s.execute(id, sb.Id)
			} else if (name == "sandbox_create" || name == "sandbox_fork") && (rec.Operation.State == "failed" || rec.Operation.State == "outcome_unknown") && value(rec.Operation.CleanupState) != "complete" {
				s.busy[sb.Id] = true
				cleanup = append(cleanup, rec)
			} else if sb.State == "unavailable" && rec.Operation.State == "succeeded" && (name == "sandbox_create" || name == "sandbox_fork") {
				reconcile = append(reconcile, [2]string{sb.Id, id})
			}
		}
		s.mu.Unlock()
		for _, rec := range cleanup {
			s.readLock()
			sb := s.journal.Sandboxes[rec.Resource.Id]
			s.mu.Unlock()
			ctx, cancel := context.WithTimeout(s.context(), s.worker.config.Timeout)
			err := s.releaseSandbox(ctx, sb)
			cancel()
			s.readLock()
			if err == nil && !s.broken {
				rec.Operation.CleanupState = pointer(api.OperationCleanupStateComplete)
				rec.Operation.Revision++
				rec.Operation.UpdatedAt = timestamp()
				sb.State = "failed"
				sb.Readiness = coreReadiness(false, nil)
				sb.Revision++
				sb.UpdatedAt = timestamp()
				s.journal.Sandboxes[sb.Id] = sb
				if s.save(rec.Operation.Id, sb.Id) != nil {
					s.broken = true
				}
			}
			delete(s.busy, sb.Id)
			s.mu.Unlock()
			if err == nil {
				s.releaseGuardAudit(s.context(), sb.Id)
			}
		}
		for _, sb := range observe {
			s.observeSandbox(sb)
		}
		for _, pair := range reconcile {
			s.wg.Add(1)
			s.reconcileSandbox(pair[0], pair[1])
		}
		select {
		case <-s.context().Done():
			return
		case <-tick.C:
		}
	}
}

func (s *Service) guardReleasable(sb api.Sandbox) bool {
	return sb.State == "deleted" || (sb.State == "failed" && !s.sandboxCharged(sb))
}

func guardReleaseError(command, sandbox, action string, err error) error {
	return fmt.Errorf("audit release %s: sandbox %s: %s failed: %w; fix --audit-release and restart lampd to retry", command, sandbox, action, err)
}

// Metrics expose the effective local control quota and charged resources for the
// authenticated principal. Host admission remains authoritative in the engine.
func (s *Service) capacityMetrics(w http.ResponseWriter, id string, a caller) {
	used := map[string]uint64{"sandboxes": 0, "vcpus": 0, "memory_mib": 0, "disk_gib": 0, "audit_bytes": 0, "retained_bytes": 0, "execs": 0, "records": 0}
	pending := []string{}
	for budget, bytes := range s.journal.Accountability.RetainedBytes {
		if s.journal.Accountability.Budgets[budget].OwnerId == a.Principal {
			used["retained_bytes"] += bytes
			used["disk_gib"] += (bytes + (1 << 30) - 1) >> 30
		}
	}
	for _, ex := range s.journal.Execs {
		if ex.OwnerId == a.Principal && s.execCharged(ex) {
			used["execs"]++
		}
	}
	for _, rec := range s.journal.Operations {
		if rec.Operation.OwnerId == a.Principal {
			used["records"]++
		}
	}
	for sid, sb := range s.journal.Sandboxes {
		if sb.OwnerId != a.Principal {
			continue
		}
		if s.sandboxCharged(sb) {
			used["sandboxes"]++
			if sb.Resources != nil {
				used["vcpus"] += sb.Resources.Vcpus
				used["memory_mib"] += sb.Resources.MemoryMib
				used["disk_gib"] += sb.Resources.DiskGib
			}
		}
		if !s.auditReleased[sid] {
			used["audit_bytes"] += s.auditReservationBytes
			if s.guardReleasable(sb) {
				pending = append(pending, sid)
			}
		}
	}
	prepareJSON(w, 200, map[string]any{"limits": s.quotas, "usage": used, "pending_audit_release": pending, "recovery": "delete unused sandboxes; fix --audit-release and restart lampd to retry pending audit capture; raise --quota-file for control limits", "request_id": id})()
}

// The engine reaps volatile guests after its own crash. Observe that fact rather
// than leaving ghost "ready" records or charging resources the engine released.
func (s *Service) observeSandbox(observed api.Sandbox) {
	ctx, cancel := context.WithTimeout(s.context(), s.worker.config.Timeout)
	defer cancel()
	deadline, _ := ctx.Deadline()
	_, err := s.worker.call(ctx, &pb.Request{Context: &pb.Context{RequestId: newID("req"), OperationId: newID("op"), DeadlineMs: uint64(deadline.UnixMilli()), SandboxId: observed.Id, Generation: value(observed.Generation), GrantId: s.worker.config.GrantID, PolicyRevision: 1}, Command: &pb.Request_Inspect{Inspect: &pb.Inspect{}}})
	if err == nil {
		return
	}
	s.readLock()
	current, ok := s.journal.Sandboxes[observed.Id]
	if !ok || s.broken || current.Revision != observed.Revision || s.busy[current.Id] {
		s.mu.Unlock()
		return
	}
	changed := []string{current.Id}
	missing := workerNotFound(err)
	if missing {
		current.State = "failed"
		for id, rec := range s.journal.Operations {
			name := value(rec.Operation.OperationName)
			if rec.Resource.Id == current.Id && (name == "sandbox_create" || name == "sandbox_fork") {
				rec.Operation.CleanupState = pointer(api.OperationCleanupStateComplete)
				rec.Operation.Revision++
				rec.Operation.UpdatedAt = timestamp()
				changed = append(changed, id)
			}
		}
		current.Readiness = coreReadiness(false, nil)
	} else if current.State == "ready" {
		current.State = "unavailable"
	} else {
		s.mu.Unlock()
		return
	}
	current.Revision++
	current.UpdatedAt = timestamp()
	s.journal.Sandboxes[current.Id] = current
	if s.save(changed...) != nil {
		s.broken = true
	}
	s.mu.Unlock()
	if missing {
		s.releaseGuardAudit(s.context(), current.Id)
	}
}
