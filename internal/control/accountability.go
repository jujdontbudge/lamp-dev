package control

// Accountability is local to an immutable resource home. Token reservations never
// move between hosts or get refunded after an uncertain upstream outcome.
import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"lamp.eilmer.si/pkg/api"
	pb "lamp.eilmer.si/pkg/workerpb"
)

type Quotas struct {
	Sandboxes  uint64 `json:"sandboxes"`
	MemoryMiB  uint64 `json:"memory_mib"`
	DiskGiB    uint64 `json:"disk_gib"`
	Execs      uint64 `json:"execs"`
	Records    uint64 `json:"records"`
	AuditBytes uint64 `json:"audit_bytes"`
}

func defaultQuotas(q Quotas) Quotas {
	if q.Sandboxes == 0 {
		q.Sandboxes = 16
	}
	if q.MemoryMiB == 0 {
		q.MemoryMiB = 65536
	}
	if q.DiskGiB == 0 {
		q.DiskGiB = 512
	}
	if q.Execs == 0 {
		q.Execs = 64
	}
	if q.Records == 0 {
		q.Records = 8192
	}
	if q.AuditBytes == 0 {
		q.AuditBytes = 512 << 20
	}
	return q
}

type reservation struct {
	Budget, Sandbox string
	Tokens          uint64
	Settled         bool
}
type usageSample struct {
	At         int64  `json:"at"`
	Count      uint64 `json:"count,omitempty"`
	DurationMS uint64 `json:"duration_ms,omitempty"`
}
type accountability struct {
	RetainedBytes       map[string]uint64        `json:"retained_bytes"`
	ForkChargesReleased bool                     `json:"fork_charges_released,omitempty"`
	Budgets             map[string]api.Budget    `json:"budgets"`
	Tasks               map[string]api.Task      `json:"tasks"`
	TaskCallers         map[string]caller        `json:"task_callers"`
	Reservations        map[string]reservation   `json:"reservations"`
	Audit               []api.AuditRecord        `json:"audit"`
	Exports             []api.AuditRecord        `json:"exports,omitempty"`
	Telemetry           map[string]api.Telemetry `json:"telemetry"`
	Usage               map[string][]usageSample `json:"usage"`
	// Notification sinks and deliveries (OPS-003, notifications.go).
	Sinks          map[string]api.NotificationSink `json:"notification_sinks,omitempty"`
	Notifications  map[string]*notificationRecord  `json:"notifications,omitempty"`
	NotifySequence uint64                          `json:"notify_sequence,omitempty"`
	NotifyStarted  bool                            `json:"notify_started,omitempty"`
}

func (s *Service) initAccountability() {
	if s.journal.Accountability != nil {
		if s.journal.Accountability.RetainedBytes == nil {
			s.journal.Accountability.RetainedBytes = map[string]uint64{}
		}
		// Older journals permanently charged each temporary fork capture, even
		// after the child was deleted. The consumed capture is engine GC's
		// responsibility; only the child's live resources need a reservation.
		if !s.journal.Accountability.ForkChargesReleased {
			for _, rec := range s.journal.Operations {
				if value(rec.Operation.OperationName) != "sandbox_fork" {
					continue
				}
				sb := s.journal.Sandboxes[rec.Resource.Id]
				if sb.Resources == nil {
					continue
				}
				t := s.journal.Accountability.Tasks[value(sb.TaskId)]
				budget := value(t.BudgetId)
				charge := (sb.Resources.DiskGib << 30) + (sb.Resources.MemoryMib << 20)
				held := s.journal.Accountability.RetainedBytes[budget]
				if held >= charge {
					s.journal.Accountability.RetainedBytes[budget] = held - charge
				}
			}
			s.journal.Accountability.ForkChargesReleased = true
		}
		return
	}
	s.journal.Accountability = &accountability{ForkChargesReleased: true, RetainedBytes: map[string]uint64{}, Budgets: map[string]api.Budget{}, Tasks: map[string]api.Task{}, TaskCallers: map[string]caller{}, Reservations: map[string]reservation{}, Audit: []api.AuditRecord{}, Telemetry: map[string]api.Telemetry{}, Usage: map[string][]usageSample{}}
}
func (s *Service) telemetry(a caller) api.Telemetry {
	if t, ok := s.journal.Accountability.Telemetry[a.Principal]; ok {
		return t
	}
	m := newResource("telemetry", a.Principal)
	m["id"] = "tm_local"
	m["enabled"] = false
	m["counts_enabled"] = false
	m["timings_enabled"] = false
	m["retention_days"] = 90
	m["state"] = "disabled"
	var t api.Telemetry
	assign(m, &t)
	return t
}
func (s *Service) accountResources(a caller, kind string) []map[string]any {
	items := []map[string]any{}
	add := func(v any) {
		m := object(v)
		if textField(m, "owner_id") == a.Principal {
			items = append(items, m)
		}
	}
	c := s.journal.Accountability
	switch kind {
	case "budget":
		for _, v := range c.Budgets {
			add(v)
		}
	case "task":
		for _, v := range c.Tasks {
			add(v)
		}
	case "audit_record":
		for _, v := range c.Audit {
			add(v)
		}
		for _, v := range c.Exports {
			add(v)
		}
	}
	return items
}
func (s *Service) accountMutation(a caller, op api.OperationDescriptor, in map[string]any) (api.ResourceRef, map[string]any, *string, []string, error) {
	c := s.journal.Accountability
	kind, verb, _ := strings.Cut(op.Scope, ".")
	ref := api.ResourceRef{Kind: api.ResourceKind(kind)}
	result := map[string]any{}
	fail := func(code, msg string) (api.ResourceRef, map[string]any, *string, []string, error) {
		return ref, nil, nil, nil, reject(code, msg)
	}
	if kind == "audit_record" {
		return s.exportAudit(a, in)
	}
	if kind == "telemetry" {
		t := s.telemetry(a)
		if t.Revision != uintField(in, "expected_revision") {
			return fail("REVISION_CONFLICT", "telemetry revision changed")
		}
		if verb == "purge" {
			delete(c.Usage, a.Principal)
		} else {
			if v, ok := in["retention_days"]; ok && fmt.Sprint(v) != "90" {
				return fail("INVALID_ARGUMENT", "usage retention is 90 days")
			}
			m := object(t)
			for _, k := range []string{"enabled", "counts_enabled", "timings_enabled", "consent_revision"} {
				if v, ok := in[k]; ok {
					m[k] = v
				}
			}
			editResource(m)
			m["retention_days"] = 90
			m["state"] = "disabled"
			if m["enabled"] == true {
				m["state"] = "active"
			}
			assign(m, &t)
			c.Telemetry[a.Principal] = t
			if !value(t.Enabled) {
				delete(c.Usage, a.Principal)
			}
		}
		ref.Id = t.Id
		return ref, result, nil, []string{"$accountability"}, nil
	}
	var m map[string]any
	if verb == "create" {
		if uint64(len(c.Budgets)+len(c.Tasks)) >= s.quotas.Records {
			return fail("CONTROL_QUOTA_EXCEEDED", "retained resource quota reached")
		}
		for _, sc := range s.scopes(a) {
			if sc.ResourceIds != nil {
				return fail("SCOPE_DENIED", "new resource outside selector")
			}
		}
		m = newResource(kind, a.Principal)
		m["id"] = s.resourceID(map[string]string{"budget": "bg", "task": "tk"}[kind])
	} else {
		for _, v := range s.accountResources(a, kind) {
			if textField(v, "id") == textField(in, kind+"_id") {
				m = v
				break
			}
		}
		if m == nil {
			return fail("NOT_FOUND", "resource not found")
		}
		if !s.resourceAllowed(a, api.ResourceRef{Kind: ref.Kind, Id: textField(m, "id")}) {
			return fail("SCOPE_DENIED", "resource outside selector")
		}
		if uintField(m, "revision") != uintField(in, "expected_revision") {
			return fail("REVISION_CONFLICT", "resource revision changed")
		}
		editResource(m)
	}
	ref.Id = textField(m, "id")
	if kind == "budget" {
		if verb == "delete" {
			m["state"] = "deleted"
		} else {
			// Changing an in-use cap or deadline could replenish a running task's authority.
			if verb != "create" {
				for _, t := range c.Tasks {
					if value(t.BudgetId) == ref.Id {
						return fail("RESOURCE_IN_USE", "budget attached to a task")
					}
				}
			}
			for _, k := range []string{"wall_time_ms", "token_limit", "token_mode", "resource_limits", "watchdog", "deadline_at", "labels", "visibility"} {
				if v, ok := in[k]; ok {
					m[k] = v
				}
			}
			if uintField(m, "wall_time_ms") == 0 && textField(m, "deadline_at") == "" {
				return fail("INVALID_ARGUMENT", "budget requires a hard time limit")
			}
			if uintField(m, "wall_time_ms") > uint64((365*24*time.Hour)/time.Millisecond) {
				return fail("INVALID_ARGUMENT", "wall time exceeds supported duration")
			}
			if d := textField(m, "deadline_at"); d != "" {
				if _, e := time.Parse(time.RFC3339Nano, d); e != nil {
					return fail("INVALID_ARGUMENT", "invalid deadline")
				}
			}
			if w, ok := m["watchdog"].(map[string]any); ok {
				if uintField(w, "warn_after_ms") != 0 || uintField(w, "repeated_action_limit") != 0 || textField(w, "on_stuck") == "ask" {
					return fail("INVALID_ARGUMENT", "this watchdog supports hard block/stop only")
				}
			}
			m["token_home_host_id"] = s.hostID()
			m["reserved_tokens"] = 0
			m["used_tokens"] = 0
			if m["token_mode"] == nil {
				m["token_mode"] = "best_effort"
			}
		}
		var b api.Budget
		assign(m, &b)
		c.Budgets[ref.Id] = b
	} else if kind == "task" {
		if verb == "create" {
			for _, k := range []string{"parent_task_id", "budget_id", "deadline_at", "labels", "visibility"} {
				if v, ok := in[k]; ok {
					m[k] = v
				}
			}
			if p := textField(m, "parent_task_id"); p != "" {
				parent, ok := c.Tasks[p]
				if !ok || parent.OwnerId != a.Principal || s.taskStopped(parent) {
					return fail("SCOPE_DENIED", "parent unavailable")
				}
				if b := textField(m, "budget_id"); b != "" && b != value(parent.BudgetId) {
					return fail("SCOPE_DENIED", "descendants share the parent budget")
				}
				m["budget_id"] = value(parent.BudgetId)
				if parent.DeadlineAt != nil {
					if d := textField(m, "deadline_at"); d == "" || earlier(*parent.DeadlineAt, d) {
						m["deadline_at"] = *parent.DeadlineAt
					}
				}
			}
			b, ok := c.Budgets[textField(m, "budget_id")]
			if !ok || b.OwnerId != a.Principal || b.State != "active" {
				return fail("INVALID_ARGUMENT", "active local budget required")
			}
			deadline := value(b.DeadlineAt)
			if value(b.WallTimeMs) > 0 {
				d := s.enforcementNow().Add(time.Duration(*b.WallTimeMs) * time.Millisecond).UTC().Format(time.RFC3339Nano)
				if deadline == "" || earlier(d, deadline) {
					deadline = d
				}
			}
			// One shared budget starts once, including independently created tasks using it.
			if b.DeadlineAt == nil || earlier(deadline, *b.DeadlineAt) {
				b.DeadlineAt = &deadline
				b.Revision++
				c.Budgets[b.Id] = b
			}
			if d := textField(m, "deadline_at"); d == "" || earlier(deadline, d) {
				m["deadline_at"] = deadline
			}
			if _, err := time.Parse(time.RFC3339Nano, textField(m, "deadline_at")); err != nil {
				return fail("INVALID_ARGUMENT", "invalid task deadline")
			}
			m["state"] = "running"
			c.TaskCallers[ref.Id] = a
		} else {
			m["state"] = "stopped"
			if verb == "delete" {
				m["state"] = "deleted"
			}
		}
		var t api.Task
		assign(m, &t)
		c.Tasks[t.Id] = t
	} else {
		return fail("NOT_FOUND", "operation not installed")
	}
	if verb == "create" {
		result[kind+"_id"] = ref.Id
	}
	return ref, result, nil, []string{"$accountability"}, nil
}
func (s *Service) taskStopped(t api.Task) bool {
	return s.taskStopRequired(t, true)
}
func (s *Service) taskStopRequired(t api.Task, checkAuthority bool) bool {
	c := s.journal.Accountability
	for n := 0; n <= len(c.Tasks); n++ {
		if t.State != "running" || s.expired(t.DeadlineAt) || checkAuthority && !s.current(c.TaskCallers[t.Id]) {
			return true
		}
		b, ok := c.Budgets[value(t.BudgetId)]
		if !ok || b.State != "active" || s.expired(b.DeadlineAt) {
			return true
		}
		if t.ParentTaskId == nil {
			return false
		}
		var exists bool
		t, exists = c.Tasks[*t.ParentTaskId]
		if !exists {
			return true
		}
	}
	return true
}
func (s *Service) accountAdmission(a caller, name string, sb api.Sandbox) error {
	retains := name == "snapshot_create" || name == "sandbox_suspend" || name == "share_create"
	if name == "sandbox_delete" {
		return nil
	}
	q := s.quotas
	var n, mem, disk, commands, records uint64
	for id, bytes := range s.journal.Accountability.RetainedBytes {
		if s.journal.Accountability.Budgets[id].OwnerId == a.Principal {
			disk += (bytes + (1 << 30) - 1) >> 30
		}
	}
	for _, b := range s.journal.Sandboxes {
		if b.OwnerId == a.Principal && s.sandboxCharged(b) {
			n++
			if b.Resources != nil {
				mem += b.Resources.MemoryMib
				disk += b.Resources.DiskGib
			}
		}
	}
	for _, e := range s.journal.Execs {
		if e.OwnerId == a.Principal && s.execCharged(e) {
			commands++
		}
	}
	for _, r := range s.journal.Operations {
		if r.Operation.OwnerId == a.Principal {
			records++
		}
	}
	if uint64(len(s.journal.Operations)) >= q.Records*16 {
		return reject("CONTROL_QUOTA_EXCEEDED", "host retained operation quota reached")
	}
	if uint64(len(s.journal.Accountability.Budgets)+len(s.journal.Accountability.Tasks)) >= q.Records {
		return reject("CONTROL_QUOTA_EXCEEDED", "retained budget/task quota reached")
	}
	if records >= q.Records {
		return reject("CONTROL_QUOTA_EXCEEDED", "retained operation quota reached")
	}
	if name == "exec_create" && commands >= q.Execs {
		return reject("RESOURCE_IN_USE", "command queue quota reached")
	}
	if name == "sandbox_create" || name == "sandbox_fork" {
		// Deleted sandboxes keep their slot until their spools are captured and
		// removed; live and unreleased sandboxes each hold one reservation.
		held := 0
		var heldIDs []string
		for id, x := range s.journal.Sandboxes {
			if !s.guardReleasable(x) || !s.auditReleased[id] {
				held++
				heldIDs = append(heldIDs, id)
			}
		}
		if uint64(held+1) > q.AuditBytes/s.auditReservationBytes {
			return reject("CONTROL_QUOTA_EXCEEDED", fmt.Sprintf("audit storage (guard spool) limit=%d bytes, used=%d bytes, request=%d bytes; held sandboxes=%v; delete sandboxes or fix --audit-release and restart lampd to retry cleanup", q.AuditBytes, uint64(held)*s.auditReservationBytes, s.auditReservationBytes, heldIDs))
		}
		if n >= q.Sandboxes || sb.Resources == nil || sb.Resources.MemoryMib > q.MemoryMiB || mem > q.MemoryMiB-sb.Resources.MemoryMib || sb.Resources.DiskGib > q.DiskGiB || disk > q.DiskGiB-sb.Resources.DiskGib {
			return reject("CONTROL_QUOTA_EXCEEDED", fmt.Sprintf("sandbox quota: count=%d/%d, memory=%d/%d MiB, disk=%d/%d GiB; request=%d MiB/%d GiB; delete sandboxes or raise --quota-file limits", n, q.Sandboxes, mem, q.MemoryMiB, disk, q.DiskGiB, sb.Resources.MemoryMib, sb.Resources.DiskGib))
		}
	}
	if retains && sb.Resources != nil && (disk+sb.Resources.DiskGib+(sb.Resources.MemoryMib+1023)/1024 > q.DiskGiB) {
		return reject("CONTROL_QUOTA_EXCEEDED", "retained capture disk quota reached")
	}
	if sb.TaskId != nil {
		t, ok := s.journal.Accountability.Tasks[*sb.TaskId]
		if !ok || t.OwnerId != a.Principal || s.taskStopped(t) {
			return reject("SCOPE_DENIED", "task budget stopped or expired")
		}
		b := s.journal.Accountability.Budgets[value(t.BudgetId)]
		if b.ResourceLimits != nil {
			l := b.ResourceLimits
			var count, m, d, v uint64
			for _, x := range s.journal.Sandboxes {
				xt := s.journal.Accountability.Tasks[value(x.TaskId)]
				if value(xt.BudgetId) == b.Id && s.sandboxCharged(x) {
					count++
					if x.Resources != nil {
						m += x.Resources.MemoryMib
						d += x.Resources.DiskGib
						v += x.Resources.Vcpus
					}
				}
			}
			oldTask := s.journal.Accountability.Tasks[value(s.journal.Sandboxes[sb.Id].TaskId)]
			rebind := name == "sandbox_resume" && value(oldTask.BudgetId) != b.Id
			if name == "sandbox_create" || name == "sandbox_fork" || rebind {
				count++
				m += sb.Resources.MemoryMib
				d += sb.Resources.DiskGib
				v += sb.Resources.Vcpus
			}
			retained := s.journal.Accountability.RetainedBytes[b.Id]
			if retains {
				retained += (sb.Resources.DiskGib << 30) + (sb.Resources.MemoryMib << 20)
			}
			if l.RetainedBytes != nil && retained > *l.RetainedBytes {
				return reject("CONTROL_QUOTA_EXCEEDED", "retained capture quota reached")
			}
			if l.ControlRecords != nil {
				var charged uint64
				for _, r := range s.journal.Operations {
					if s.operationBudget(r) == b.Id {
						charged++
					}
				}
				if charged >= *l.ControlRecords {
					return reject("CONTROL_QUOTA_EXCEEDED", "control retention quota reached")
				}
			}
			if l.Concurrency != nil && count > *l.Concurrency || l.MemoryMib != nil && m > *l.MemoryMib || l.DiskGib != nil && d > *l.DiskGib || l.Vcpus != nil && v > *l.Vcpus {
				return reject("CONTROL_QUOTA_EXCEEDED", "shared task resource quota reached")
			}
		}
	}
	return nil
}
func (s *Service) sandboxMustStop(sb api.Sandbox) bool {
	return s.sandboxStopRequired(sb, true)
}

// Homes retain sandbox/task associations even after release. Resolve older
// receipts through those immutable associations rather than charging every
// operation by the same principal to every one of that principal's budgets.
func (s *Service) operationBudget(r *record) string {
	c := s.journal.Accountability
	ref := r.Resource
	if r.Custody != nil {
		ref = api.ResourceRef{Kind: "sandbox", Id: r.Custody.Source}
	}
	switch ref.Kind {
	case "budget":
		return ref.Id
	case "task":
		return value(c.Tasks[ref.Id].BudgetId)
	case "exec":
		ref = api.ResourceRef{Kind: "sandbox", Id: value(s.journal.Execs[ref.Id].SandboxId)}
	case "audit_record":
		for _, group := range [][]api.AuditRecord{c.Audit, c.Exports} {
			for _, rec := range group {
				if rec.Id == ref.Id {
					return value(c.Tasks[value(rec.TaskId)].BudgetId)
				}
			}
		}
	}
	if ref.Kind == "sandbox" {
		return value(c.Tasks[value(s.journal.Sandboxes[ref.Id].TaskId)].BudgetId)
	}
	return ""
}
func (s *Service) sandboxStopRequired(sb api.Sandbox, checkAuthority bool) bool {
	// Suspended captures are inert encrypted state, not task execution. Ending
	// the old Task must not erase an explicitly retained CI inspection capture.
	// Resume separately requires current (and, for ended jobs, new) Task authority.
	if sb.State == "suspended" {
		return false
	}
	if sb.TaskId != nil {
		t, ok := s.journal.Accountability.Tasks[*sb.TaskId]
		if !ok || s.taskStopRequired(t, checkAuthority) {
			return true
		}
	}
	if !checkAuthority {
		return false
	}
	for _, r := range s.journal.Operations {
		if r.Resource.Id == sb.Id && (value(r.Operation.OperationName) == "sandbox_create" || value(r.Operation.OperationName) == "sandbox_fork") && r.Caller.Principal != "" && !s.can(r.Caller, strings.Replace(value(r.Operation.OperationName), "_", ".", 1)) {
			return true
		}
	}
	for _, r := range s.journal.Operations {
		if r.Custody != nil && r.Custody.Source == sb.Id && r.Caller.Principal != "" && !s.current(r.Caller) {
			return true
		}
	}
	// A command started through derived authority is stopped together with its sandbox.
	for _, r := range s.journal.Operations {
		if r.Resource.Kind == "exec" && value(s.journal.Execs[r.Resource.Id].SandboxId) == sb.Id && r.Caller.Principal != "" && !s.can(r.Caller, strings.Replace(value(r.Operation.OperationName), "_", ".", 1)) {
			return true
		}
	}
	return false
}
func (s *Service) watchAccountability() {
	defer s.wg.Done()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	nextPrune := time.Now().Add(time.Hour)
	for {
		select {
		case <-s.context().Done():
			return
		case <-tick.C:
		}
		s.readLock()
		// Journal failure closes admission, but must never disable stopping.
		// Before the first quorum activation, retained authority is unknown;
		// enforce real deadlines without treating startup as revocation.
		checkAuthority := !s.fleetEnabled || s.fleet != nil && s.fleet.authorityActivated()
		if !s.broken && time.Now().After(nextPrune) {
			nextPrune = time.Now().Add(time.Hour)
			if s.pruneUsage(time.Now()) {
				if err := s.save("$accountability"); err != nil {
					s.broken = true
				} else if err = s.compact(); err != nil {
					s.broken = true
				}
			}
		}
		changed := false
		for id, t := range s.journal.Accountability.Tasks {
			stalled := t.State == "running" && s.taskStalled(t)
			if stalled || t.State == "running" && s.taskStopRequired(t, checkAuthority) {
				// A watchdog block (no progress for block_after_ms before the
				// deadline) is stuck (OPS-003 task.stuck); every other stop is
				// stopped. Either way taskStopRequired then stops its sandboxes.
				t.State = "stopped"
				if stalled {
					t.State = "blocked"
				}
				t.Revision++
				t.UpdatedAt = timestamp()
				s.journal.Accountability.Tasks[id] = t
				changed = true
			}
		}
		for id, b := range s.journal.Accountability.Budgets {
			if b.State == "active" && s.expired(b.DeadlineAt) {
				b.State = "disabled"
				b.Revision++
				b.UpdatedAt = timestamp()
				s.journal.Accountability.Budgets[id] = b
				changed = true
			}
		}
		if changed && !s.broken {
			if err := s.save("$accountability"); err != nil {
				s.broken = true
			}
		}
		var ids []string
		for id, b := range s.journal.Sandboxes {
			if s.sandboxCharged(b) && !s.busy[id] && s.starting[id] == 0 && s.sandboxStopRequired(b, checkAuthority) {
				s.busy[id] = true
				s.wg.Add(1)
				ids = append(ids, id)
			}
		}
		s.mu.Unlock()
		for _, id := range ids {
			go func() { defer s.wg.Done(); s.stopAccountableSandbox(id) }()
		}
	}
}
func (s *Service) stopAccountableSandbox(id string) {
	s.readLock()
	sb := s.journal.Sandboxes[id]
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(s.context(), s.worker.config.Timeout)
	defer cancel()
	deadline, _ := ctx.Deadline()
	request := func(step string) *pb.Request {
		return &pb.Request{Context: &pb.Context{RequestId: newID("req"), OperationId: newID("op") + "." + step, DeadlineMs: uint64(deadline.UnixMilli()), SandboxId: id, Generation: value(sb.Generation), GrantId: s.worker.config.GrantID, PolicyRevision: 1}}
	}
	r := request("watchdog-stop")
	r.Command = &pb.Request_Stop{Stop: &pb.Stop{Force: true}}
	err := s.worker.perform(ctx, r, "stopped")
	if workerNotFound(err) {
		err = nil
	}
	if err == nil {
		r = request("watchdog-release")
		r.Command = &pb.Request_Release{Release: &pb.Release{}}
		err = s.worker.perform(ctx, r, "released")
		if workerNotFound(err) {
			err = nil
		}
	}
	s.readLock()
	defer s.mu.Unlock()
	delete(s.busy, id)
	sb = s.journal.Sandboxes[id]
	sb.Revision++
	sb.UpdatedAt = timestamp()
	if err == nil {
		sb.State = "deleted"
	} else {
		sb.State = "unavailable"
	}
	s.journal.Sandboxes[id] = sb
	if e := s.save(id); e != nil {
		s.broken = true
	}
}

// Bridge is installed only behind lampd's owner-UID Unix listener. Fleet TLS
// ingress rejects these non-public routes, including forwarded requests.
func (s *Service) accountabilityBridge(w http.ResponseWriter, r *http.Request) {
	peer, present := r.Context().Value(peerContextKey{}).(publicPeer)
	if !present || !peer.owner {
		http.Error(w, "accounting requires the owner Unix peer", http.StatusForbidden)
		return
	}
	if s.fleet != nil {
		if err := s.fleet.project(); err != nil {
			http.Error(w, "fleet authority unavailable", http.StatusServiceUnavailable)
			return
		}
	}
	var in struct {
		Sandbox     string  `json:"sandbox_id"`
		Reservation string  `json:"reservation_id"`
		Tokens      uint64  `json:"tokens"`
		Actual      *uint64 `json:"actual_tokens"`
		Duration    uint64  `json:"duration_ms"`
		Type        string  `json:"type"`
		Exact       bool    `json:"exact"`
	}
	if r.Method != "POST" || json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		http.Error(w, "invalid", 400)
		return
	}
	if in.Tokens > 1<<40 || in.Actual != nil && *in.Actual > 1<<40 {
		http.Error(w, "token count outside supported range", 400)
		return
	}
	s.readLock()
	defer s.mu.Unlock()
	if s.broken {
		http.Error(w, "journal unavailable", 503)
		return
	}
	sb, ok := s.journal.Sandboxes[in.Sandbox]
	if !ok {
		http.Error(w, "sandbox unavailable", 403)
		return
	}
	c := s.journal.Accountability
	t := c.Tasks[value(sb.TaskId)]
	b := c.Budgets[value(t.BudgetId)]
	if strings.HasSuffix(r.URL.Path, "/usage") {
		prepareJSON(w, 200, map[string]any{"samples": pruneUsageSamples(c.Usage[sb.OwnerId], time.Now()), "retention_days": 90})()
		return
	}
	if strings.HasSuffix(r.URL.Path, "/settle") {
		res, ok := c.Reservations[in.Reservation]
		if !ok || res.Sandbox != sb.Id {
			http.Error(w, "reservation unavailable", 403)
			return
		}
		if !res.Settled {
			b = c.Budgets[res.Budget]
			reserved := value(b.ReservedTokens)
			if reserved < res.Tokens {
				http.Error(w, "ledger invalid", 503)
				return
			}
			b.ReservedTokens = pointer(reserved - res.Tokens)
			used := res.Tokens
			if in.Actual != nil && *in.Actual > res.Tokens {
				b.State = "disabled"
			}
			if in.Actual != nil {
				used = *in.Actual
			}
			b.UsedTokens = pointer(value(b.UsedTokens) + used)
			b.Revision++
			c.Budgets[b.Id] = b
			res.Settled = true
			c.Reservations[in.Reservation] = res
			s.recordUsage(sb.OwnerId, in.Duration)
			if err := s.save("$accountability"); err != nil {
				s.broken = true
				http.Error(w, "journal unavailable", 503)
				return
			}
		}
		prepareJSON(w, 200, map[string]any{"settled": true})()
		return
	}
	if sb.State == "deleted" || s.sandboxMustStop(sb) {
		http.Error(w, "sandbox stopped", 403)
		return
	}
	if t.Id == "" || b.Id == "" {
		http.Error(w, "task budget required", 403)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/check") {
		prepareJSON(w, 200, map[string]any{"active": true})()
		return
	}
	if strings.HasSuffix(r.URL.Path, "/reserve") {
		if value(b.TokenMode) == "exact" && !in.Exact {
			http.Error(w, "exact vendor tokenizer required", 403)
			return
		}
		var requests uint64
		for _, res := range c.Reservations {
			if res.Sandbox == sb.Id {
				requests++
			}
		}
		if requests >= s.quotas.Records {
			http.Error(w, "retained reservation quota", 429)
			return
		}
		if b.TokenLimit != nil && (in.Tokens > *b.TokenLimit || value(b.UsedTokens) > *b.TokenLimit-in.Tokens || value(b.ReservedTokens) > *b.TokenLimit-in.Tokens-value(b.UsedTokens)) {
			http.Error(w, "token budget exhausted", 429)
			return
		}
		if !s.appendAudit(sb, "request", in.Type, "allow") {
			http.Error(w, "audit quota exhausted", 429)
			return
		}
		rid := newID("reservation")
		c.Reservations[rid] = reservation{Budget: b.Id, Sandbox: sb.Id, Tokens: in.Tokens}
		b.ReservedTokens = pointer(value(b.ReservedTokens) + in.Tokens)
		b.Revision++
		c.Budgets[b.Id] = b
		if err := s.save("$accountability"); err != nil {
			s.broken = true
			http.Error(w, "journal unavailable", 503)
			return
		}
		prepareJSON(w, 200, map[string]any{"reservation_id": rid, "deadline_at": t.DeadlineAt, "token_mode": b.TokenMode})()
		return
	}
	http.Error(w, "unknown bridge operation", 404)
}
func (s *Service) appendAudit(sb api.Sandbox, visibility, typ, decision string) bool {
	c := s.journal.Accountability
	retained := &c.Audit
	if visibility == "control" && typ == "audit.export" {
		// Export receipts have their own bounded allowance so a full request
		// log can still be retrieved without dropping any retained records.
		retained = &c.Exports
	}
	var count uint64
	var bytes uint64
	for _, rec := range *retained {
		if value(rec.SandboxId) == sb.Id {
			count++
			raw, _ := json.Marshal(rec)
			bytes += uint64(len(raw))
		}
	}
	if count >= s.quotas.Records {
		return false
	}
	m := newResource("audit_record", sb.OwnerId)
	m["id"] = s.resourceID("au")
	m["visibility"] = visibility
	m["state"] = "ready"
	m["sandbox_id"] = sb.Id
	m["task_id"] = sb.TaskId
	m["host_id"] = s.hostID()
	m["generation"] = sb.Generation
	m["type"] = typ
	m["decision"] = decision
	m["sequence"] = len(*retained) + 1
	if len(*retained) > 0 {
		m["previous_hash"] = digest((*retained)[len(*retained)-1])
	}
	var a api.AuditRecord
	assign(m, &a)
	if bytes+2048 > 16<<20 {
		return false
	}
	*retained = append(*retained, a)
	return true
}
func (s *Service) recordUsage(owner string, duration uint64) {
	t := s.telemetry(caller{Principal: owner})
	if !value(t.Enabled) {
		return
	}
	u := usageSample{At: time.Now().Unix()}
	if value(t.CountsEnabled) {
		u.Count = 1
	}
	if value(t.TimingsEnabled) {
		u.DurationMS = duration
	}
	samples := s.journal.Accountability.Usage[owner]
	cut := time.Now().Add(-90 * 24 * time.Hour).Unix()
	kept := samples[:0]
	for _, v := range samples {
		if v.At >= cut {
			kept = append(kept, v)
		}
	}
	if uint64(len(kept)) < s.quotas.Records {
		s.journal.Accountability.Usage[owner] = append(kept, u)
	}
}
func (s *Service) exportAudit(a caller, in map[string]any) (api.ResourceRef, map[string]any, *string, []string, error) {
	ref := api.ResourceRef{Kind: "audit_record"}
	filter, _ := in["filter"].(map[string]any)
	id := textField(filter, "sandbox_id")
	sb, ok := s.journal.Sandboxes[id]
	if !ok || !s.sandboxAllowed(a, sb) {
		return ref, nil, nil, nil, reject("NOT_FOUND", "export requires an authorized sandbox_id filter")
	}
	records := []any{}
	for _, r := range s.journal.Accountability.Audit {
		if value(r.SandboxId) == id {
			records = append(records, r)
		}
	}
	for _, r := range s.journal.Accountability.Exports {
		if value(r.SandboxId) == id {
			records = append(records, r)
		}
	}
	if guardRecords, ok := in["$guard_records"].([]any); ok {
		records = append(records, guardRecords...)
	}
	raw, err := json.Marshal(records)
	if err != nil || uint64(len(raw)) > s.quotas.AuditBytes {
		return ref, nil, nil, nil, reject("CONTROL_QUOTA_EXCEEDED", "audit export quota exceeded")
	}
	dir := filepath.Join(s.dir, "audit-exports")
	var charged uint64
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return ref, nil, nil, nil, err
	}
	for _, entry := range entries {
		info, e := entry.Info()
		if e != nil {
			return ref, nil, nil, nil, e
		}
		charged += uint64(info.Size())
	}
	if charged+uint64(len(raw)) > s.quotas.AuditBytes {
		return ref, nil, nil, nil, reject("CONTROL_QUOTA_EXCEEDED", "retained audit exports quota exhausted")
	}
	if err = PrivateDir(dir); err != nil {
		return ref, nil, nil, nil, err
	}
	if !s.appendAudit(sb, "control", "audit.export", "allow") {
		return ref, nil, nil, nil, reject("CONTROL_QUOTA_EXCEEDED", "audit export receipt quota reached")
	}
	path := filepath.Join(dir, id+"-"+newID("export")+".json")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return ref, nil, nil, nil, err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.Remove(path)
			_ = syncDirectory(dir)
		}
	}()
	_, err = file.Write(raw)
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return ref, nil, nil, nil, err
	}
	if err = syncDirectory(dir); err != nil {
		return ref, nil, nil, nil, err
	}
	last := &s.journal.Accountability.Exports[len(s.journal.Accountability.Exports)-1]
	details, _ := json.Marshal(map[string]any{"path": path, "sha256": bearerHash(string(raw)), "records": len(records), "guard_reader_configured": s.auditReader != ""})
	d := api.AuditDetails(details)
	last.Details = &d
	ref.Id = last.Id
	complete = true
	return ref, map[string]any{}, nil, []string{"$accountability"}, nil
}

type limitedAuditBuffer struct{ strings.Builder }

func (b *limitedAuditBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 16<<20 {
		return 0, fmt.Errorf("audit reader exceeded quota")
	}
	return b.Builder.Write(p)
}

// Legacy callers get a bounded task automatically; descendants inherit it.
func (s *Service) bindDefaultTask(a caller, sb *api.Sandbox) {
	if sb.TaskId != nil {
		return
	}
	c := s.journal.Accountability
	ms := uint64(time.Hour / time.Millisecond)
	for _, sc := range s.scopes(a) {
		if sc.Limits != nil && sc.Limits.WallTimeMs != nil && *sc.Limits.WallTimeMs < ms {
			ms = *sc.Limits.WallTimeMs
		}
	}
	deadline := s.enforcementNow().Add(time.Duration(ms) * time.Millisecond).UTC().Format(time.RFC3339Nano)
	bm := newResource("budget", a.Principal)
	bm["id"] = s.resourceID("bg")
	bm["wall_time_ms"] = ms
	bm["deadline_at"] = deadline
	bm["token_mode"] = "best_effort"
	bm["token_home_host_id"] = s.hostID()
	var b api.Budget
	assign(bm, &b)
	c.Budgets[b.Id] = b
	tm := newResource("task", a.Principal)
	tm["id"] = s.resourceID("tk")
	tm["budget_id"] = b.Id
	tm["deadline_at"] = deadline
	tm["state"] = "running"
	var t api.Task
	assign(tm, &t)
	c.Tasks[t.Id] = t
	c.TaskCallers[t.Id] = a
	sb.TaskId = &t.Id
}

// Retention applies to numeric opt-in samples, not the mandatory security audit.
func pruneUsageSamples(samples []usageSample, now time.Time) []usageSample {
	cut := now.Add(-90 * 24 * time.Hour).Unix()
	kept := make([]usageSample, 0, len(samples))
	for _, v := range samples {
		if v.At >= cut {
			kept = append(kept, v)
		}
	}
	return kept
}
func (s *Service) pruneUsage(now time.Time) bool {
	changed := false
	for owner, samples := range s.journal.Accountability.Usage {
		kept := pruneUsageSamples(samples, now)
		if len(kept) != len(samples) {
			s.journal.Accountability.Usage[owner] = kept
			changed = true
		}
	}
	return changed
}

func (s *Service) readGuardAudit(a caller, in map[string]any) ([]any, error) {
	if s.auditReader == "" {
		return nil, nil
	}
	filter, _ := in["filter"].(map[string]any)
	id := textField(filter, "sandbox_id")
	s.readLock()
	sb, ok := s.journal.Sandboxes[id]
	allowed := ok && s.sandboxAllowed(a, sb)
	s.mu.Unlock()
	if !allowed {
		return nil, reject("NOT_FOUND", "sandbox not found")
	}
	records := []any{}
	if s.auditReader != "" {
		var text string
		captured, fromCapture := s.capturedGuardAudit(id)
		if fromCapture {
			// Spools were removed after capture; serve the captured copy.
			text = string(captured)
		} else {
			ctx, cancel := context.WithTimeout(s.context(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, s.auditReader, id, strconv.FormatUint(value(sb.Generation), 10))
			var out limitedAuditBuffer
			cmd.Stdout = &out
			if err := cmd.Run(); err != nil {
				return nil, reject("AUDIT_UNAVAILABLE", "guard audit reader failed")
			}
			text = out.String()
		}
		for _, line := range strings.Split(text, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var v map[string]any
			if json.Unmarshal([]byte(line), &v) != nil {
				return nil, reject("AUDIT_UNAVAILABLE", "invalid guard record")
			}
			attr, _ := v["attribution"].(map[string]any)
			if textField(attr, "sandbox_id") != id {
				return nil, reject("AUDIT_UNAVAILABLE", "guard attribution mismatch")
			}
			if fmt.Sprint(attr["generation"]) != strconv.FormatUint(value(sb.Generation), 10) {
				if fromCapture {
					continue // the capture spans every generation
				}
				return nil, reject("AUDIT_UNAVAILABLE", "guard attribution mismatch")
			}
			records = append(records, v)
		}
	}
	return records, nil
}

func (s *Service) chargeFork(sb api.Sandbox) {
	t := s.journal.Accountability.Tasks[value(sb.TaskId)]
	s.journal.Accountability.RetainedBytes[value(t.BudgetId)] += (sb.Resources.DiskGib << 30) + (sb.Resources.MemoryMib << 20)
}

// taskStalled reports a running task whose budget watchdog saw no progress for
// block_after_ms before its deadline. Progress is the task's own updates and
// its sandboxes' activity (a ready sandbox not yet observed idle counts as
// progressing now).
func (s *Service) taskStalled(t api.Task) bool {
	b, ok := s.journal.Accountability.Budgets[value(t.BudgetId)]
	if !ok || b.Watchdog == nil || b.Watchdog.BlockAfterMs == nil || *b.Watchdog.BlockAfterMs == 0 || s.expired(t.DeadlineAt) || s.expired(b.DeadlineAt) {
		return false
	}
	last, _ := time.Parse(time.RFC3339Nano, t.UpdatedAt)
	for id, sb := range s.journal.Sandboxes {
		if sb.TaskId == nil || *sb.TaskId != t.Id || sb.State == "deleted" {
			continue
		}
		if at, err := time.Parse(time.RFC3339Nano, sb.UpdatedAt); err == nil && at.After(last) {
			last = at
		}
		if s.life != nil {
			if l := s.life.Sandboxes[id]; l != nil {
				if sb.State == "ready" && l.idleFrom.IsZero() {
					return false
				}
				for _, at := range []time.Time{l.activity, l.idleFrom} {
					if at.After(last) {
						last = at
					}
				}
			}
		}
	}
	return time.Since(last) >= time.Duration(*b.Watchdog.BlockAfterMs)*time.Millisecond
}
