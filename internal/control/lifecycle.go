package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"lamp.eilmer.si/internal/network"
	"lamp.eilmer.si/pkg/api"
	pb "lamp.eilmer.si/pkg/workerpb"
)

// Lifecycle defaults (LIF-001, LIF-003).
const (
	defaultIdleGrace          = 300
	defaultSuspendedRetention = 604800
	maxIdleGrace              = 86400
)

// lifeTick is how often the idle monitor observes sandboxes.
var lifeTick = 5 * time.Second

func (s *Service) initLifecycle(admin []string) error {
	if err := s.loadLife(); err != nil {
		return err
	}
	s.stateAdmin = admin
	s.wg.Add(1)
	go s.lifeMonitor()
	return nil
}

func lifecycleOf(sb api.Sandbox) (action string, grace, retention uint64, expires time.Time) {
	action, grace, retention = "suspend", defaultIdleGrace, defaultSuspendedRetention
	if l := sb.Lifecycle; l != nil {
		if l.IdleAction != nil {
			action = string(*l.IdleAction)
		}
		if l.IdleGraceSeconds != nil {
			grace = *l.IdleGraceSeconds
		}
		if l.SuspendedRetentionSeconds != nil {
			retention = *l.SuspendedRetentionSeconds
		}
		if l.ExpiresAt != nil {
			expires, _ = time.Parse(time.RFC3339Nano, *l.ExpiresAt)
		}
	}
	return
}

// taskExpired reports a passed hard deadline (host-authoritative time).
func (s *Service) taskExpired(sb api.Sandbox) bool {
	_, _, _, expires := lifecycleOf(sb)
	return !expires.IsZero() && !s.lifeNow().Before(expires)
}

func init() {
	lifeHandlers["sandbox_suspend"] = (*Service).planSuspend
	lifeHandlers["sandbox_resume"] = (*Service).planResume
	lifeHandlers["sandbox_update"] = (*Service).planSandboxUpdate
}

// --- suspend / resume ------------------------------------------------------

func (s *Service) planSuspend(a caller, in map[string]any) (lifePlan, error) {
	return s.planSuspendWith(a, in, false)
}

// planDeadlineSuspend is the hard-expiry suspension: a running task command
// does not hold the sandbox past its deadline. The command is captured with
// the guest and its outcome becomes unknown; resuming needs new authority.
func (s *Service) planDeadlineSuspend(a caller, in map[string]any) (lifePlan, error) {
	return s.planSuspendWith(a, in, true)
}

func (s *Service) planSuspendWith(a caller, in map[string]any, deadline bool) (lifePlan, error) {
	id := textField(in, "sandbox_id")
	sb, err := s.lifeSandboxFor(a, id)
	if err != nil {
		return lifePlan{}, err
	}
	if sb.Revision != uintField(in, "expected_revision") {
		return lifePlan{}, reject("REVISION_CONFLICT", "sandbox revision is "+strconv.FormatUint(sb.Revision, 10))
	}
	if d := textField(in, "durability"); d != "" && d != "local" {
		if d == "volatile" {
			return lifePlan{}, reject("INVALID_ARGUMENT", "a suspended sandbox is always saved durably")
		}
		return lifePlan{}, reject("QUORUM_REQUIRED", "replicated suspension needs a second storage host")
	}
	if sb.State != "ready" || s.busy[id] || s.starting[id] > 0 {
		return lifePlan{}, reject("RESOURCE_IN_USE", "sandbox is not ready or has an in-flight operation")
	}
	for _, ex := range s.journal.Execs {
		if !deadline && value(ex.SandboxId) == id && (ex.State == "accepted" || ex.State == "running") {
			return lifePlan{}, reject("RESOURCE_IN_USE", "a task command is still running")
		}
	}
	if l := s.life.Sandboxes[id]; l != nil && l.Pool != "" {
		return lifePlan{}, reject("RESOURCE_IN_USE", "unclaimed warm-pool sandbox")
	}
	sb.State = "suspending"
	sb.Revision++
	sb.UpdatedAt = timestamp()
	s.journal.Sandboxes[id] = sb
	s.busy[id] = true
	suffix := strings.TrimPrefix(newID("x"), "x_")
	capture := "cp_" + suffix
	captured := false
	released := false
	snapID := "sn_" + suffix
	return lifePlan{ref: lifeRef("sandbox", id), ids: []string{id, snapID}, timeout: 5 * time.Minute,
		run: func(ctx context.Context) (map[string]any, error) {
			gen := s.sandboxGeneration(id)
			r := s.workerRequest(ctx, "capture", id, gen)
			r.Command = &pb.Request_Capture{Capture: &pb.Capture{CaptureId: capture, PreservesMemory: true, Consistency: "local"}}
			if err := s.worker.perform(ctx, r, "captured"); err != nil {
				return nil, err
			}
			captured = true
			r = s.workerRequest(ctx, "stop", id, gen)
			r.Command = &pb.Request_Stop{Stop: &pb.Stop{Force: true}}
			if err := s.worker.perform(ctx, r, "stopped"); err != nil {
				return nil, err
			}
			r = s.workerRequest(ctx, "release", id, gen)
			r.Command = &pb.Request_Release{Release: &pb.Release{}}
			if err := s.worker.perform(ctx, r, "released"); err != nil {
				return nil, err
			}
			released = true
			return map[string]any{}, nil
		},
		finish: func(_ map[string]any, err error) error {
			delete(s.busy, id)
			sb := s.journal.Sandboxes[id]
			sb.Revision++
			sb.UpdatedAt = timestamp()
			l := s.lifeSB(id)
			switch {
			case err == nil:
				sb.State = "suspended"
				sb.SnapshotId = pointer(snapID)
				s.recordSuspendSnapshot(sb, snapID, capture)
				sb.Durability = pointer(api.SandboxDurabilityLocal)
				sb.SuspendEligibility = nil
				l.Capture = capture
				l.SuspendedAt = s.lifeNow().Format(time.RFC3339Nano)
			case !captured:
				// The source keeps running unchanged when its capture fails.
				sb.State = "ready"
			case released:
				sb.State = "unavailable"
			default:
				// Captured but not released: the saved state is intact and
				// the guest may still hold memory. Report it, never hide it.
				sb.State = "unavailable"
				l.Capture = capture
			}
			s.journal.Sandboxes[id] = sb
			return err
		}}, nil
}

func (s *Service) planResume(a caller, in map[string]any) (lifePlan, error) {
	id := textField(in, "sandbox_id")
	sb, err := s.lifeSandboxFor(a, id)
	if err != nil {
		return lifePlan{}, err
	}
	if sb.Revision != uintField(in, "expected_revision") {
		return lifePlan{}, reject("REVISION_CONFLICT", "sandbox revision is "+strconv.FormatUint(sb.Revision, 10))
	}
	if len(stringsField(in, "exposure_ids")) > 0 {
		return lifePlan{}, reject("CONTINUATION_UNSUPPORTED", "old exposures must not be revived with a saved CI job")
	}
	continuation := textField(in, "continuation_task_id")
	if continuation != "" {
		if textField(in, "expected_task_id") != value(sb.TaskId) || continuation == value(sb.TaskId) {
			return lifePlan{}, reject("REVISION_CONFLICT", "inspection needs the expected old Task and a distinct new Task")
		}
		task, ok := s.journal.Accountability.Tasks[continuation]
		if !ok || task.OwnerId != a.Principal || s.taskStopped(task) || !s.resourceAllowed(a, lifeRef("task", continuation)) {
			return lifePlan{}, reject("SCOPE_DENIED", "new Task authority is unavailable")
		}
		sb.TaskId = pointer(continuation)
		// Admission rechecks the new Task's budget and the host's quotas rather
		// than replenishing the completed job's authority.
		if err := s.accountAdmission(a, "sandbox_resume", sb); err != nil {
			return lifePlan{}, err
		}
	} else {
		if textField(in, "expected_task_id") != "" && textField(in, "expected_task_id") != value(sb.TaskId) {
			return lifePlan{}, reject("REVISION_CONFLICT", "Task changed")
		}
		if task, ok := s.journal.Accountability.Tasks[value(sb.TaskId)]; ok && s.taskStopped(task) {
			return lifePlan{}, reject("TASK_AUTHORITY_EXPIRED", "saved job inspection requires a new Task")
		}
	}
	if sb.State != "suspended" || s.busy[id] {
		return lifePlan{}, reject("RESOURCE_IN_USE", "sandbox is not suspended")
	}
	for _, job := range s.life.Jobs {
		if textField(job.R, "sandbox_id") == id && job.Retain {
			if err := ownerMachineAdmission(); err != nil {
				return lifePlan{}, err
			}
			break
		}
	}
	if s.taskExpired(sb) {
		return lifePlan{}, reject("TASK_AUTHORITY_EXPIRED", "the sandbox deadline has passed; saved work needs an authorized continuation")
	}
	l := s.lifeSB(id)
	if l.Capture == "" {
		return lifePlan{}, reject("DATA_UNAVAILABLE", "saved state is missing")
	}
	if err := s.accountAdmission(a, "sandbox_resume", sb); err != nil {
		return lifePlan{}, err
	}
	capture := l.Capture
	allocated := false
	sb.State = "resuming"
	sb.Revision++
	// A resumed sandbox runs as a new generation: its own guard audit spool
	// and identity barrier, so nothing bound to the old run carries over.
	// The bump is kept even if the restore fails, because the attempt may
	// already have used that generation.
	sb.Generation = pointer(value(sb.Generation) + 1)
	sb.UpdatedAt = timestamp()
	s.journal.Sandboxes[id] = sb
	s.busy[id] = true
	resources := sb.Resources
	engine := value(sb.EngineId)
	ids := []string{id}
	if sb.SnapshotId != nil {
		ids = append(ids, *sb.SnapshotId)
	}
	return lifePlan{ref: lifeRef("sandbox", id), ids: ids, timeout: 5 * time.Minute,
		run: func(ctx context.Context) (map[string]any, error) {
			gen := s.sandboxGeneration(id)
			r := s.workerRequest(ctx, "allocate", id, gen)
			r.Command = &pb.Request_Allocate{Allocate: &pb.Allocate{Resources: &pb.Resources{Vcpus: uint32(resources.Vcpus), MemoryMib: resources.MemoryMib, DiskGib: resources.DiskGib, PidsMax: resources.PidsMax}, EngineId: engine}}
			if err := s.worker.perform(ctx, r, "allocated"); err != nil {
				return nil, err
			}
			allocated = true
			if sb.NetworkPolicyId != nil {
				s.readLock()
				n := s.life.Networks[*sb.NetworkPolicyId]
				var layers []network.Layer
				if n != nil {
					layer, e := policyLayer(n.R)
					if e == nil {
						layers = []network.Layer{layer}
					}
				}
				s.mu.Unlock()
				if len(layers) == 0 {
					return nil, reject("DATA_UNAVAILABLE", "saved network policy unavailable")
				}
				if _, e := s.networkCall(ctx, id, gen, map[string]any{"operation": "policy_update", "expected_revision": 1, "revision": 2, "policy_layers": layers}); e != nil {
					return nil, e
				}
			}
			r = s.workerRequest(ctx, "restore", id, gen)
			r.Command = &pb.Request_Restore{Restore: &pb.Restore{CaptureId: capture}}
			err := s.worker.perform(ctx, r, "restored")
			if err == nil {
				err = s.remountStorage(ctx, id)
			}
			if err == nil {
				var res guestResult
				res, err = s.guestRun(ctx, id, "/bin/true")
				if err == nil && res.Exit != 0 {
					err = reject("DATA_UNAVAILABLE", "restored guest failed its readiness probe")
				}
			}
			if err != nil {
				// Never present a failed restore as a cold start: release the
				// partial allocation and keep the intact saved state.
				cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
				defer cancel()
				stop := s.workerRequest(cleanup, "stop", id, gen)
				stop.Command = &pb.Request_Stop{Stop: &pb.Stop{Force: true}}
				s.worker.perform(cleanup, stop, "stopped")
				rel := s.workerRequest(cleanup, "release", id, gen)
				rel.Command = &pb.Request_Release{Release: &pb.Release{}}
				s.worker.perform(cleanup, rel, "released")
				return nil, err
			}
			return map[string]any{}, nil
		},
		finish: func(_ map[string]any, err error) error {
			delete(s.busy, id)
			sb := s.journal.Sandboxes[id]
			sb.Revision++
			sb.UpdatedAt = timestamp()
			l := s.lifeSB(id)
			if err == nil {
				sb.State = "ready"
				if sb.NetworkPolicyId != nil {
					sb.EffectivePolicyRevision = pointer(uint64(2))
				} else {
					sb.EffectivePolicyRevision = pointer(uint64(1))
				}
				s.retireSnapshot(sb.SnapshotId)
				sb.SnapshotId = nil
				s.life.Discards[capture] = "resumed"
				l.Resumed = value(sb.Generation)
				l.Capture = ""
				l.SuspendedAt = ""
				l.IdleFrom = time.Time{}
				l.Activity = s.lifeNow()
			} else {
				sb.State = "suspended"
				var failure *workerFailure
				if !allocated && errors.As(err, &failure) && failure.code == "SCOPE_DENIED" {
					sb.Generation = pointer(value(sb.Generation) - 1)
				}
			}
			s.journal.Sandboxes[id] = sb
			if l.waking != nil {
				close(l.waking)
				l.waking = nil
			}
			return err
		}}, nil
}

// resumedGeneration reports whether generation was brought up by a resume
// (so the create command reconciliation inspects does not exist). Under mu.
func (s *Service) resumedGeneration(id string, generation uint64) bool {
	l := s.life.Sandboxes[id]
	return l != nil && generation != 0 && l.Resumed == generation
}

// reconcileResumed marks a resumed sandbox ready again after a daemon restart
// once its guest, at the same generation, answers a readiness probe.
func (s *Service) reconcileResumed(ctx context.Context, id string, revision uint64) {
	res, err := s.guestRun(ctx, id, "/bin/true")
	if err != nil || res.Exit != 0 {
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
	s.journal.Sandboxes[id] = current
	if err := s.save(id); err != nil {
		s.broken = true
	}
}

// Wake resumes a suspended sandbox for authorized activity (exec, message,
// SSH attach or an allowed dev request) and waits until it is ready.
// Concurrent wakes coalesce on one resume.
func (s *Service) Wake(ctx context.Context, a caller, id string) error {
	s.readLock()
	sb, ok := s.journal.Sandboxes[id]
	if !ok || !s.sandboxAllowed(a, sb) {
		s.mu.Unlock()
		return nil // the original request reports NOT_FOUND
	}
	l := s.lifeSB(id)
	l.Activity = s.lifeNow()
	l.IdleFrom = time.Time{}
	if l.Pool != "" {
		s.mu.Unlock()
		return reject("RESOURCE_IN_USE", "unclaimed warm-pool sandbox")
	}
	if sb.State == "suspended" && s.taskExpired(sb) {
		s.mu.Unlock()
		return reject("TASK_AUTHORITY_EXPIRED", "the sandbox deadline has passed; automatic wake is refused")
	}
	waiting := l.waking
	start := sb.State == "suspended" && waiting == nil
	if start {
		waiting = make(chan struct{})
		l.waking = waiting
	}
	revision := sb.Revision
	s.mu.Unlock()
	if start {
		in := map[string]any{"sandbox_id": id, "expected_revision": jsonNumber(revision), "idempotency_key": "lamp-wake:" + id + ":" + strconv.FormatUint(revision, 10)}
		opID, _, err := s.lifeStart(caller{Principal: sb.OwnerId}, "sandbox_resume", in)
		if err != nil {
			s.mu.Lock()
			if l.waking == waiting {
				close(waiting)
				l.waking = nil
			}
			s.mu.Unlock()
			return err
		}
		return s.lifeWait(ctx, opID)
	}
	if waiting != nil {
		select {
		case <-waiting:
		case <-ctx.Done():
			return reject("OUTCOME_UNKNOWN", "sandbox still resuming")
		}
		s.mu.Lock()
		state := s.journal.Sandboxes[id].State
		s.mu.Unlock()
		if state != "ready" {
			return reject("DATA_UNAVAILABLE", "sandbox did not resume")
		}
	}
	return nil
}

// wakeForExec is the exec admission hook: it resumes a suspended target
// and records task session activity before the ordinary exec path runs.
func (s *Service) wakeForExec(w http.ResponseWriter, r *http.Request, requestID string) bool {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		writeError(w, requestID, "INVALID_ARGUMENT", "body exceeds limit", nil)
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	var body struct {
		SandboxID string            `json:"sandbox_id"`
		Env       map[string]string `json:"env"`
	}
	if json.Unmarshal(data, &body) != nil || body.SandboxID == "" {
		return true // the exec path reports the malformed request
	}
	a := requestCaller(r)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	if err := s.Wake(ctx, a, body.SandboxID); err != nil {
		code := "DATA_UNAVAILABLE"
		var e *coreError
		if errors.As(err, &e) {
			code = e.code
		}
		writeError(w, requestID, code, err.Error(), nil)
		return false
	}
	// A command environment can carry a credential. Taint only rises.
	for k := range body.Env {
		up := strings.ToUpper(k)
		for _, word := range []string{"TOKEN", "SECRET", "PASSWORD", "API_KEY", "CREDENTIAL", "COOKIE"} {
			if strings.Contains(up, word) {
				s.readLock()
				if sb, ok := s.journal.Sandboxes[body.SandboxID]; ok && s.sandboxAllowed(a, sb) {
					s.addTaint(body.SandboxID, "exec_environment")
					if s.saveLife() != nil {
						s.broken = true
					}
				}
				s.mu.Unlock()
				return true
			}
		}
	}
	return true
}

func (s *Service) addTaint(id, reason string) {
	l := s.lifeSB(id)
	for _, t := range l.Taint {
		if t == reason {
			return
		}
	}
	l.Taint = append(l.Taint, reason)
}

// taintOf is monotonic: a sandbox's own taint, its ancestors' (forked RAM and
// disk carry everything the parent held) and custody-bearing state.
func (s *Service) taintOf(id string) []string {
	seen := map[string]bool{}
	var out []string
	for depth := 0; id != "" && depth < 64; depth++ {
		if l := s.life.Sandboxes[id]; l != nil {
			for _, t := range l.Taint {
				if !seen[t] {
					seen[t] = true
					out = append(out, t)
				}
			}
		}
		sb := s.journal.Sandboxes[id]
		if trust := value(sb.CaptureTrust); trust == "owner_private" || trust == "rebuild_required" {
			if !seen["capture_trust:"+string(trust)] {
				seen["capture_trust:"+string(trust)] = true
				out = append(out, "capture_trust:"+string(trust))
			}
		}
		if sb.Exposures != nil && len(*sb.Exposures) > 0 && !seen["exposures"] {
			seen["exposures"] = true
			out = append(out, "exposures")
		}
		id = value(sb.ParentSandboxId)
	}
	return out
}

// --- per-sandbox overrides (LIF-003) ---------------------------------------

func (s *Service) planSandboxUpdate(a caller, in map[string]any) (lifePlan, error) {
	if _, ok := in["network_policy_id"]; ok {
		return s.planSandboxPolicy(a, in)
	}
	id := textField(in, "sandbox_id")
	sb, err := s.lifeSandboxFor(a, id)
	if err != nil {
		return lifePlan{}, err
	}
	for k := range in {
		switch k {
		case "sandbox_id", "idempotency_key", "expected_revision", "lifecycle", "capture_trust":
		default:
			return lifePlan{}, reject("INVALID_ARGUMENT", k+" is not updatable on a sandbox")
		}
	}
	if sb.Revision != uintField(in, "expected_revision") {
		return lifePlan{}, reject("REVISION_CONFLICT", "sandbox revision is "+strconv.FormatUint(sb.Revision, 10))
	}
	if sb.State == "deleted" || sb.State == "deleting" {
		return lifePlan{}, reject("NOT_FOUND", "sandbox not found")
	}
	if raw, ok := in["lifecycle"]; ok {
		var next api.Lifecycle
		b, _ := json.Marshal(raw)
		if err := json.Unmarshal(b, &next); err != nil {
			return lifePlan{}, reject("INVALID_ARGUMENT", "invalid lifecycle")
		}
		if s.taskExpired(sb) {
			return lifePlan{}, reject("TASK_AUTHORITY_EXPIRED", "an expired deadline is never reactivated")
		}
		if err := s.validateLifecycle(a, &next); err != nil {
			return lifePlan{}, err
		}
		owner := s.role(a) == "owner"
		if next.IdleGraceSeconds != nil && *next.IdleGraceSeconds > maxIdleGrace && !owner {
			return lifePlan{}, reject("SCOPE_DENIED", "idle grace above 86400 s needs owner authority")
		}
		if next.SuspendedRetentionSeconds != nil && *next.SuspendedRetentionSeconds > defaultSuspendedRetention && !owner {
			return lifePlan{}, reject("SCOPE_DENIED", "suspended retention above 604800 s needs owner authority")
		}
		if next.ExpiresAt != nil {
			if _, err := time.Parse(time.RFC3339Nano, *next.ExpiresAt); err != nil {
				return lifePlan{}, reject("INVALID_ARGUMENT", "expires_at must be RFC 3339 UTC")
			}
		}
		merged := api.Lifecycle{}
		if sb.Lifecycle != nil {
			merged = *sb.Lifecycle
		}
		if next.IdleAction != nil {
			merged.IdleAction = next.IdleAction
		}
		if next.IdleGraceSeconds != nil {
			merged.IdleGraceSeconds = next.IdleGraceSeconds
		}
		if next.SuspendedRetentionSeconds != nil {
			merged.SuspendedRetentionSeconds = next.SuspendedRetentionSeconds
		}
		if next.ExpiresAt != nil {
			merged.ExpiresAt = next.ExpiresAt
		}
		if next.OnJobEnd != nil {
			merged.OnJobEnd = next.OnJobEnd
		}
		sb.Lifecycle = &merged
	}
	if trust := textField(in, "capture_trust"); trust != "" {
		current := value(sb.CaptureTrust)
		// Trust only moves toward rebuild_required.
		rank := map[string]int{"": 0, "clean": 0, "owner_private": 1, "rebuild_required": 2}
		if rank[trust] < rank[string(current)] {
			return lifePlan{}, reject("SCOPE_DENIED", "capture trust is monotonic")
		}
		sb.CaptureTrust = pointer(api.SandboxCaptureTrust(trust))
		if trust != "clean" {
			s.addTaint(id, "capture_trust:"+trust)
		}
	}
	sb.Revision++
	sb.UpdatedAt = timestamp()
	s.journal.Sandboxes[id] = sb
	return lifePlan{ref: lifeRef("sandbox", id), ids: []string{id}, result: map[string]any{}}, nil
}

// --- idle observation (LIF-002) --------------------------------------------

// The observer runs as an ordinary task exec, and every task exec has its own
// PID namespace. It therefore sees other execs' processes only as anonymous
// entries ("0") in the guest-wide task cgroup, and sees the shared network
// namespace in full. Measured on the guard's collector: the observer's own
// exec contributes observerInfra hidden processes, and every other exec that
// is still alive contributes execInfra (collector, status relay, namespace
// reaper) plus its task processes.
const (
	observerInfra = 2
	execInfra     = 3
)

// pyIdle reports the task cgroup, the run-queue count (three samples), every
// TCP/UDP socket not in TIME_WAIT/CLOSE, and the parked-harness markers. A
// marker is /tmp/lamp/harness-wait/<name> (or /run/lamp/harness-wait when an
// image provides it) holding {"waiting_for":"person_message"}; it is live only
// while its harness holds an exclusive flock on it, so a stale marker from an
// exited process means nothing.
const pyIdle = `import os,json,fcntl,time
out=dict(cgroup=None,running=[],socks=[],waits={})
def unreadable(error): raise error
try:
  out['cgroup']=[]
  for root,dirs,files in os.walk('/sys/fs/cgroup/tasks',onerror=unreadable):
    out['cgroup'] += open(root+'/cgroup.procs').read().split()
  if not os.path.isdir('/sys/fs/cgroup/tasks'): out['cgroup']=None
except OSError: out['cgroup']=None
for i in range(3):
  out['running'].append(int(open('/proc/loadavg').read().split()[3].split('/')[0]))
  time.sleep(0.1)
for proto in ('tcp','tcp6','udp','udp6'):
  try: lines=open('/proc/net/'+proto).read().splitlines()[1:]
  except OSError: continue
  for line in lines:
    f=line.split()
    if len(f)>9 and not (proto.startswith('tcp') and f[3] in ('06','07')): out['socks'].append(dict(proto=proto,state=f[3],local=f[1]))
for d in ('/run/lamp/harness-wait','/tmp/lamp/harness-wait'):
  try: names=os.listdir(d)
  except OSError: continue
  for n in names:
    try:
      fd=os.open(d+'/'+n,os.O_RDONLY)
      try:
        try:
          fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB); live=False; fcntl.flock(fd,fcntl.LOCK_UN)
        except BlockingIOError: live=True
        doc=json.loads(os.read(fd,4096) or b'{}')
      finally: os.close(fd)
      out['waits'][n]=dict(live=live,doc=doc if isinstance(doc,dict) else {})
    except (OSError,ValueError): out['waits'][n]=dict(live=True,doc={})
print(json.dumps(out))`

type idleObservation struct {
	Cgroup  []string `json:"cgroup"`
	Running []int    `json:"running"`
	Socks   []struct {
		Proto string `json:"proto"`
		State string `json:"state"`
		Local string `json:"local"`
	} `json:"socks"`
	Waits map[string]struct {
		Live bool           `json:"live"`
		Doc  map[string]any `json:"doc"`
	} `json:"waits"`
}

// classifyIdle applies LIF-002 to one observation. Only a fresh observation
// with no reasons is eligible: no task process beyond parked harness waits,
// nothing on the run queue but the observer, and no task connection or
// listener. Detail lists what was found.
func classifyIdle(raw []byte) (reasons []string, detail []string) {
	var o idleObservation
	if err := json.Unmarshal(raw, &o); err != nil || o.Cgroup == nil || len(o.Running) == 0 {
		return []string{"accounting_unknown"}, nil
	}
	reason := map[string]bool{}
	hidden := 0
	for _, pid := range o.Cgroup {
		if pid == "0" {
			hidden++
		}
	}
	hidden = max(hidden-observerInfra, 0)
	parked := 0
	for name, w := range o.Waits {
		if !w.Live {
			continue
		}
		if w.Doc["waiting_for"] != "person_message" {
			reason["harness_wait_unqualified"] = true
			detail = append(detail, "wait:"+name)
			continue
		}
		parked++
	}
	// A parked harness exec is exactly its infrastructure and one process.
	if hidden > parked*(execInfra+1) {
		reason["task_process"] = true
		detail = append(detail, "processes:"+strconv.Itoa(hidden))
	}
	running := o.Running[0]
	for _, r := range o.Running {
		running = min(running, r)
	}
	if running > 1 {
		reason["task_process"] = true
		detail = append(detail, "running:"+strconv.Itoa(running-1))
	}
	if len(o.Socks) > 0 {
		reason["task_connection"] = true
		for _, sk := range o.Socks {
			detail = append(detail, sk.Proto+":"+sk.State)
		}
		if parked > 0 {
			// A parked wait holds no task connection.
			reason["harness_wait_unqualified"] = true
		}
	}
	for _, r := range []string{"session_activity", "task_process", "task_connection", "harness_wait_unqualified", "accounting_unknown"} {
		if reason[r] {
			reasons = append(reasons, r)
		}
	}
	sort.Strings(detail)
	return reasons, detail
}

func (s *Service) observeIdle(ctx context.Context, id string) (reasons []string, detail []string) {
	out, err := s.guestPython(ctx, id, pyIdle)
	if err != nil {
		return []string{"accounting_unknown"}, nil
	}
	return classifyIdle(out)
}

// --- monitor ---------------------------------------------------------------

func (s *Service) lifeMonitor() {
	defer s.wg.Done()
	ticker := time.NewTicker(lifeTick)
	defer ticker.Stop()
	for {
		select {
		case <-s.context().Done():
			s.finalDiscard()
			return
		case <-ticker.C:
		}
		s.lifeTickOnce()
	}
}

// finalDiscard gives saved state that is already unreferenced (resumed,
// deleted or expired) one last discard attempt as the service closes, so it
// is not left behind until the next start.
func (s *Service) finalDiscard() {
	s.readLock()
	var retired []string
	for id, l := range s.life.Sandboxes {
		if sb := s.journal.Sandboxes[id]; l.Capture != "" && sb.State == "deleted" {
			s.life.Discards[l.Capture] = "sandbox deleted"
			l.Capture = ""
			if snap := s.retireSnapshot(sb.SnapshotId); snap != "" {
				retired = append(retired, snap)
			}
		}
	}
	if len(retired) > 0 && !s.broken {
		s.save(retired...)
	}
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s.discardCaptures(ctx)
}

type idleCandidate struct {
	id, owner string
	revision  uint64
	since     time.Time
}

func (s *Service) lifeTickOnce() {
	s.observeVMFailures()
	ctx, cancel := context.WithTimeout(s.context(), 2*time.Minute)
	defer cancel()
	var candidates []idleCandidate
	var recoveries []struct{ id, operation string }
	var expiredDelete []api.Sandbox
	var hardStop []idleCandidate
	var retired []string
	s.readLock()
	if s.broken {
		s.mu.Unlock()
		return
	}
	now := s.lifeNow()
	for id, sb := range s.journal.Sandboxes {
		l := s.life.Sandboxes[id]
		action, _, retention, _ := lifecycleOf(sb)
		switch sb.State {
		case "unavailable":
			l = s.lifeSB(id)
			if s.busy[id] || l.reconciling {
				continue
			}
			if s.resumedGeneration(id, value(sb.Generation)) {
				l.reconciling = true
				recoveries = append(recoveries, struct{ id, operation string }{id, ""})
				continue
			}
			for _, op := range s.journal.Operations {
				name := value(op.Operation.OperationName)
				if op.Resource.Id == id && op.Operation.State == "succeeded" && (name == "sandbox_create" || name == "sandbox_fork") {
					l.reconciling = true
					recoveries = append(recoveries, struct{ id, operation string }{id, op.Operation.Id})
					break
				}
			}
		case "ready":
			if s.busy[id] || s.starting[id] > 0 || (l != nil && l.Pool != "") {
				continue
			}
			if s.taskExpired(sb) {
				expiredDelete = append(expiredDelete, sb)
				continue
			}
			if s.sessionSandboxBusy(id) {
				l = s.lifeSB(id)
				l.IdleFrom = time.Time{}
				l.Activity = now
				sb.SuspendEligibility = &api.SuspendEligibility{Eligible: false, Reasons: []api.SuspendEligibilityReasonsItem{"session_activity"}, ObservedAt: timestamp(), ObservationState: api.SuspendEligibilityObservationStateFresh}
				s.journal.Sandboxes[id] = sb
				continue
			}
			if action == "keep" {
				continue
			}
			var since time.Time
			if l != nil {
				since = l.Activity
			}
			candidates = append(candidates, idleCandidate{id: id, owner: sb.OwnerId, revision: sb.Revision, since: since})
		case "suspended":
			if s.taskExpired(sb) {
				expiredDelete = append(expiredDelete, sb)
				continue
			}
			if l == nil || l.SuspendedAt == "" || s.busy[id] {
				continue
			}
			at, err := time.Parse(time.RFC3339Nano, l.SuspendedAt)
			if err == nil && !now.Before(at.Add(time.Duration(retention)*time.Second)) {
				expiredDelete = append(expiredDelete, sb)
			}
		case "deleted":
			if l != nil && l.Capture != "" {
				s.life.Discards[l.Capture] = "sandbox deleted"
				l.Capture = ""
				if snap := s.retireSnapshot(sb.SnapshotId); snap != "" {
					retired = append(retired, snap)
				}
			}
		}
	}
	s.sweepStorage(now)
	if s.saveLife() != nil {
		s.broken = true
	}
	if len(retired) > 0 && s.save(retired...) != nil {
		s.broken = true
	}
	s.mu.Unlock()
	for _, c := range recoveries {
		s.wg.Add(1)
		go func(id, operation string) {
			defer func() {
				s.mu.Lock()
				if l := s.life.Sandboxes[id]; l != nil {
					l.reconciling = false
				}
				s.mu.Unlock()
			}()
			s.reconcileSandbox(id, operation)
		}(c.id, c.operation)
	}
	s.discardCaptures(ctx)
	for _, sb := range expiredDelete {
		s.internalDelete(sb, "retention")
	}
	for _, c := range hardStop {
		s.internalSuspend(c, "deadline")
	}
	for _, c := range candidates {
		// The observation is dated when it starts reading the guest.
		observedAt := timestamp()
		reasons, _ := s.observeIdle(ctx, c.id)
		s.readLock()
		sb, ok := s.journal.Sandboxes[c.id]
		if !ok || sb.State != "ready" {
			s.mu.Unlock()
			continue
		}
		l := s.lifeSB(c.id)
		if !l.Activity.Equal(c.since) || time.Since(l.Activity) < lifeTick {
			reasons = append([]string{"session_activity"}, reasons...)
		}
		for _, ex := range s.journal.Execs {
			if value(ex.SandboxId) == c.id && (ex.State == "accepted" || ex.State == "running" || ex.State == "outcome_unknown") {
				reasons = appendOnce(reasons, "task_process")
			}
		}
		state := api.SuspendEligibilityObservationStateFresh
		if len(reasons) == 1 && reasons[0] == "accounting_unknown" {
			state = api.SuspendEligibilityObservationStateUnknown
		}
		items := make([]api.SuspendEligibilityReasonsItem, 0, len(reasons))
		for _, r := range reasons {
			items = append(items, api.SuspendEligibilityReasonsItem(r))
		}
		eligible := len(reasons) == 0
		// An observation, not a configuration change: no revision bump.
		sb.SuspendEligibility = &api.SuspendEligibility{Eligible: eligible, Reasons: items, ObservedAt: observedAt, ObservationState: state}
		s.journal.Sandboxes[c.id] = sb
		due := false
		if !eligible {
			l.IdleFrom = time.Time{}
		} else {
			if l.IdleFrom.IsZero() {
				l.IdleFrom = s.lifeNow()
			}
			_, grace, _, _ := lifecycleOf(sb)
			due = s.lifeNow().Sub(l.IdleFrom) >= time.Duration(grace)*time.Second
		}
		action, _, _, _ := lifecycleOf(sb)
		c.revision = sb.Revision
		if s.saveLife() != nil {
			s.broken = true
			due = false
		}
		s.mu.Unlock()
		if due && action == "delete" {
			s.internalDelete(sb, "idle")
		} else if due {
			s.internalSuspend(c, "idle")
		}
	}
	s.refillPools(ctx)
}

func appendOnce(xs []string, v string) []string {
	for _, x := range xs {
		if x == v {
			return xs
		}
	}
	return append(xs, v)
}

func (s *Service) internalSuspend(c idleCandidate, why string) {
	in := map[string]any{"sandbox_id": c.id, "expected_revision": jsonNumber(c.revision), "durability": "local", "idempotency_key": "lamp-" + why + "-suspend:" + c.id + ":" + strconv.FormatUint(c.revision, 10)}
	handler := (*Service).planSuspend
	if why == "deadline" {
		handler = (*Service).planDeadlineSuspend
	}
	s.lifeStartWith(caller{Principal: c.owner}, "sandbox_suspend", in, handler)
}

// internalHTTP drives an existing public handler on behalf of a principal.
func (s *Service) internalHTTP(a caller, method, path string, headers map[string]string, body any) (int, map[string]any) {
	var reader io.Reader = http.NoBody
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	r := httptest.NewRequest(method, path, reader)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	r = r.WithContext(context.WithValue(s.context(), callerContext{}, a))
	w := httptest.NewRecorder()
	s.engineHTTP(w, r)
	out := map[string]any{}
	json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func (s *Service) internalDelete(sb api.Sandbox, why string) {
	ctx, cancel := context.WithTimeout(s.context(), 15*time.Minute)
	defer cancel()
	if !s.detachForDelete(ctx, sb.OwnerId, sb.Id) {
		return // kept until its files are written back
	}
	s.mu.Lock()
	sb = s.journal.Sandboxes[sb.Id]
	s.mu.Unlock()
	s.internalHTTP(caller{Principal: sb.OwnerId}, "DELETE", "/v1/sandboxes/"+sb.Id, map[string]string{
		"Idempotency-Key": "lamp-" + why + "-delete:" + sb.Id, "If-Match": strconv.FormatUint(sb.Revision, 10)}, nil)
}

// discardCaptures deletes engine captures nothing references any more,
// through the supervisor's root-only state administration when configured.
func (s *Service) discardCaptures(ctx context.Context) {
	s.mu.Lock()
	admin := s.stateAdmin
	var pending []string
	for c := range s.life.Discards {
		pending = append(pending, c)
	}
	s.mu.Unlock()
	if len(admin) == 0 {
		return
	}
	for _, c := range pending {
		cmd := exec.CommandContext(ctx, admin[0], append(append([]string{}, admin[1:]...), "delete", c)...)
		out, err := cmd.CombinedOutput()
		gone := err == nil || strings.Contains(string(out), "NOT_FOUND")
		if !gone {
			continue
		}
		s.readLock()
		delete(s.life.Discards, c)
		if s.saveLife() != nil {
			s.broken = true
		}
		s.mu.Unlock()
	}
}

// recordSuspendSnapshot publishes the suspend capture as a Snapshot, readable
// with snapshot_get, until the capture is discarded. Under mu.
func (s *Service) recordSuspendSnapshot(sb api.Sandbox, id, capture string) {
	now := timestamp()
	trust := api.SnapshotCaptureTrustOwnerPrivate
	if t := value(sb.CaptureTrust); t != "" {
		trust = api.SnapshotCaptureTrust(t)
	}
	s.journal.Snapshots[id] = savedSnapshot{CaptureID: capture, Snapshot: api.Snapshot{Id: id, Kind: "snapshot", FleetId: "fl_local", OwnerId: sb.OwnerId, Revision: 1, CreatedAt: now, UpdatedAt: now, State: "ready", Labels: api.StringMap{}, Visibility: "private", SandboxId: pointer(sb.Id), Generation: sb.Generation, PreservesMemory: pointer(true), CaptureTrust: &trust, Durability: pointer("local"), CustodyHostIds: &[]string{s.hostID()}, Consistency: pointer(api.SnapshotConsistencyApplication)}}
}

// retireSnapshot marks a suspend snapshot deleted once its capture is no
// longer held, and returns its id for saving ("" when there is none). Under mu.
func (s *Service) retireSnapshot(id *string) string {
	if id == nil {
		return ""
	}
	snap, ok := s.journal.Snapshots[*id]
	if !ok || snap.Snapshot.State == "deleted" {
		return ""
	}
	snap.Snapshot.State = "deleted"
	snap.Snapshot.Revision++
	snap.Snapshot.UpdatedAt = timestamp()
	s.journal.Snapshots[*id] = snap
	return *id
}
