package control

import (
	"context"
	"strconv"
	"time"

	pb "lamp.eilmer.si/pkg/workerpb"
)

// reconcileSandbox performs only Inspect. It may restore readiness only for an
// originally successful create/fork, with the same generation and independently
// witnessed original command. Missing proof leaves the resource unavailable.
func (s *Service) reconcileSandbox(id, operationID string) {
	defer s.wg.Done()
	ctx, cancel := context.WithTimeout(s.context(), s.worker.config.Timeout)
	defer cancel()
	s.readLock()
	sb, ok := s.journal.Sandboxes[id]
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
