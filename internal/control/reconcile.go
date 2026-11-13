package control

import (
	"context"
	"strconv"
	"time"

	"lamp.eilmer.si/pkg/api"
	pb "lamp.eilmer.si/pkg/workerpb"
)

// reconcileSandbox inspects the original command and reconnects storage.
// It may restore readiness only for an
// originally successful create/fork, with the same generation and independently
// witnessed original command. Missing proof leaves the resource unavailable.
func (s *Service) reconcileSandbox(id, operationID string) {
	defer s.wg.Done()
	ctx, cancel := context.WithTimeout(s.context(), s.worker.config.Timeout)
	defer cancel()
	s.readLock()
	sb, ok := s.journal.Sandboxes[id]
	if !ok || sb.State != "unavailable" || s.busy[id] {
		s.mu.Unlock()
		return
	}
	// Startup and worker reconnect recovery can inspect the same guest.
	// Only one may reconnect its mounts before publishing readiness.
	s.busy[id] = true
	defer func() { s.mu.Lock(); delete(s.busy, id); s.mu.Unlock() }()
	if ok && sb.State == "unavailable" && s.resumedGeneration(id, value(sb.Generation)) {
		revision := sb.Revision
		s.mu.Unlock()
		s.reconcileResumed(ctx, id, revision)
		return
	}
	if !ok || sb.State != "unavailable" || sb.Readiness == nil || sb.Readiness.CommandStartedAt == nil {
		s.mu.Unlock()
		return
	}
	generation := value(sb.Generation)
	revision := sb.Revision
	s.mu.Unlock()
	deadline, _ := ctx.Deadline()
	receipt, err := s.worker.call(ctx, &pb.Request{Context: &pb.Context{RequestId: newID("req"), OperationId: newID("op"), DeadlineMs: uint64(deadline.UnixMilli()), SandboxId: id, Generation: generation, GrantId: s.worker.config.GrantID, PolicyRevision: 1}, Command: &pb.Request_Inspect{Inspect: &pb.Inspect{CommandOperationId: operationID + ".start"}}})
	if err != nil || receipt.Command == nil || receipt.Command.StartedAtUnixNs == 0 || receipt.Command.FinishedAtUnixNs == 0 || receipt.Command.ExitCode != 0 || receipt.Command.Signal != 0 || receipt.Detail["generation"] != strconv.FormatUint(generation, 10) || receipt.State != "running" && receipt.State != "command_started" {
		return
	}
	if err := s.remountStorage(ctx, id); err != nil {
		return
	}
	s.readLock()
	defer s.mu.Unlock()
	current := s.journal.Sandboxes[id]
	if s.broken || current.Revision != revision || current.State != "unavailable" {
		return
	}
	current.State = "ready"
	current.Revision++
	current.UpdatedAt = timestamp()
	at := time.Unix(0, int64(receipt.Command.StartedAtUnixNs)).UTC().Format(time.RFC3339Nano)
	current.Readiness = coreReadiness(true, &at)
	s.journal.Sandboxes[id] = current
	if err := s.save(id); err != nil {
		s.broken = true
	}
}

// Observe VM liveness independently of idle eligibility, including active jobs
// and sandboxes whose idle action is keep. A lost control channel is uncertain;
// only the engine's witnessed failure changes ready to failed.
func (s *Service) observeVMFailures() {
	s.readLock()
	candidates := []api.Sandbox{}
	for _, sb := range s.journal.Sandboxes {
		if sb.State == "ready" || sb.State == "failed" && sb.Labels["lamp.failure"] == "" {
			candidates = append(candidates, sb)
		}
	}
	s.mu.Unlock()
	for _, sb := range candidates {
		ctx, cancel := context.WithTimeout(s.context(), 2*time.Second)
		deadline, _ := ctx.Deadline()
		receipt, err := s.worker.call(ctx, &pb.Request{Context: &pb.Context{RequestId: newID("req"), OperationId: newID("op"), DeadlineMs: uint64(deadline.UnixMilli()), SandboxId: sb.Id, Generation: value(sb.Generation), GrantId: s.worker.config.GrantID, PolicyRevision: 1}, Command: &pb.Request_Inspect{Inspect: &pb.Inspect{}}})
		cancel()
		if err != nil || receipt.State != "failed" {
			continue
		}
		s.readLock()
		current := s.journal.Sandboxes[sb.Id]
		if !s.broken && current.State == sb.State && current.Revision == sb.Revision {
			current.State = "failed"
			current.Revision++
			current.UpdatedAt = timestamp()
			if current.Labels == nil {
				current.Labels = api.StringMap{}
			}
			current.Labels["lamp.failure"] = receipt.Detail["failure"]
			current.Readiness = coreReadiness(false, nil)
			s.journal.Sandboxes[sb.Id] = current
			if s.save(sb.Id) != nil {
				s.broken = true
			}
		}
		s.mu.Unlock()
	}
}
