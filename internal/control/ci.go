package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/JohanSardar/lamp/internal/ci"
	pb "github.com/JohanSardar/lamp/pkg/workerpb"
)

type ciJob struct {
	R           map[string]any `json:"r"`
	Repository  string         `json:"repository"`
	External    int64          `json:"external"`
	Run         int64          `json:"run"`
	Runner      int64          `json:"runner"`
	Command     string         `json:"runner_command,omitempty"`
	Registering bool           `json:"registration_attempted,omitempty"`
	Cancel      bool           `json:"cancel"`
	Retain      bool           `json:"retain"`
	Workflow    string         `json:"workflow,omitempty"`
	BrokerGrant string         `json:"broker_grant,omitempty"`
}

func init() {
	lifeKinds["job"] = true
	lifeHandlers["job_create"] = (*Service).planJobCreate
	lifeHandlers["job_cancel"] = (*Service).planJobCancel
	lifeHandlers["job_reconcile"] = (*Service).planJobReconcile
}
func (s *Service) initCI(config *ci.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.life.Jobs == nil {
		s.life.Jobs = map[string]*ciJob{}
	}
	if s.life.AuthFlows == nil {
		s.life.AuthFlows = map[string]*ciAuthFlow{}
	}
	if config == nil {
		for _, j := range s.life.Jobs {
			if !ciTerminal(textField(j.R, "state")) {
				return errors.New("active CI jobs require their enrolled CI configuration")
			}
		}
		return nil
	}
	g, err := ci.New(*config)
	if err != nil {
		return err
	}
	s.github = g
	return nil
}
func ciTerminal(state string) bool {
	return state == "completed" || state == "failed" || state == "canceled"
}
func (s *Service) ciRepo(name string) (ci.Repository, bool) {
	if s.github != nil {
		for _, r := range s.github.Config.Repositories {
			if r.Name == name {
				return r, true
			}
		}
	}
	return ci.Repository{}, false
}
func (s *Service) planJobCreate(a caller, in map[string]any) (lifePlan, error) {
	if s.github == nil {
		return lifePlan{}, reject("AUTH_TRANSPORT_UNSUPPORTED", "GitHub CI is not enrolled")
	}
	if a.Principal != s.journal.Control.OwnerID || a.KeyID != "" {
		return lifePlan{}, reject("SCOPE_DENIED", "job intake is host-controller authority only; workflow identities cannot assert trust")
	}
	if err := onlyFields(in, "plugin_id external_id labels"); err != nil {
		return lifePlan{}, err
	}
	if textField(in, "plugin_id") != "pg_github" {
		return lifePlan{}, reject("AUTH_TRANSPORT_UNSUPPORTED", "CI adapter is not installed")
	}
	external := textField(in, "external_id")
	parts := strings.Split(external, "/")
	if len(parts) != 3 {
		return lifePlan{}, reject("INVALID_ARGUMENT", "external_id must be owner/repository/numeric-job-id")
	}
	name := parts[0] + "/" + parts[1]
	id, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || id <= 0 {
		return lifePlan{}, reject("INVALID_ARGUMENT", "invalid forge job id")
	}
	if _, ok := s.ciRepo(name); !ok {
		return lifePlan{}, reject("SCOPE_DENIED", "repository is not enrolled")
	}
	for _, j := range s.life.Jobs {
		if j.Repository == name && j.External == id {
			return lifePlan{ref: lifeRef("job", textField(j.R, "id")), result: map[string]any{"job_id": textField(j.R, "id")}}, nil
		}
		if !ciTerminal(textField(j.R, "state")) {
			return lifePlan{}, reject("NO_CAPACITY", "one CI job is already active on this controller")
		}
	}
	if uint64(len(s.life.Jobs)) >= s.quotas.Records {
		return lifePlan{}, reject("CONTROL_QUOTA_EXCEEDED", "retained CI job quota reached")
	}
	m := s.lifeResource("jb", "job", a.Principal)
	applyLabels(m, in)
	m["state"] = "queued"
	m["plugin_id"] = "pg_github"
	m["external_id"] = external
	m["artifact_ids"] = []string{}
	j := &ciJob{R: m, Repository: name, External: id}
	jid := textField(m, "id")
	s.life.Jobs[jid] = j
	return lifePlan{ref: lifeRef("job", jid), timeout: 6 * time.Hour, run: func(ctx context.Context) (map[string]any, error) {
		err := s.runCIJob(ctx, jid)
		return map[string]any{"job_id": jid}, err
	}}, nil
}
func (s *Service) planJobCancel(a caller, in map[string]any) (lifePlan, error) {
	m, err := s.ownedOrMissing(a, "job", textField(in, "job_id"))
	if err != nil {
		return lifePlan{}, err
	}
	if err = lifeRevision(m, in); err != nil {
		return lifePlan{}, err
	}
	j := s.life.Jobs[textField(m, "id")]
	if !ciTerminal(textField(m, "state")) {
		j.Cancel = true
		editResource(m)
	}
	return lifePlan{ref: lifeRef("job", textField(m, "id")), result: map[string]any{}}, nil
}
func (s *Service) planJobReconcile(a caller, in map[string]any) (lifePlan, error) {
	m, err := s.ownedOrMissing(a, "job", textField(in, "job_id"))
	if err != nil {
		return lifePlan{}, err
	}
	if err = lifeRevision(m, in); err != nil {
		return lifePlan{}, err
	}
	if textField(m, "state") != "cleanup_failed" {
		return lifePlan{}, reject("RESOURCE_IN_USE", "only orphan cleanup needs reconciliation")
	}
	id := textField(m, "id")
	return lifePlan{ref: lifeRef("job", id), run: func(ctx context.Context) (map[string]any, error) {
		return map[string]any{}, s.cleanupCI(ctx, id, false)
	}}, nil
}
func (s *Service) ciUpdate(id string, fn func(*ciJob)) error {
	s.readLock()
	defer s.mu.Unlock()
	if s.broken {
		return errors.New("CI journal unavailable")
	}
	j := s.life.Jobs[id]
	fn(j)
	editResource(j.R)
	if err := s.saveLife(); err != nil {
		s.broken = true
		return errors.New("CI journal unavailable")
	}
	return nil
}
func (s *Service) ciSnapshot(id string) ciJob {
	s.readLock()
	defer s.mu.Unlock()
	b, _ := json.Marshal(s.life.Jobs[id])
	var j ciJob
	json.Unmarshal(b, &j)
	return j
}
func (s *Service) ciState(id, state string) error {
	return s.ciUpdate(id, func(j *ciJob) { j.R["state"] = state })
}
func (s *Service) ciController(pending []string) {
	defer s.wg.Done()
	// Never rerun a runner after controller loss. Its one-job authority may have
	// been consumed; recover outputs, revoke authority and release its VM.
	for _, id := range pending {
		ctx, cancel := context.WithTimeout(s.context(), 2*time.Minute)
		s.cleanupCI(ctx, id, false)
		cancel()
	}
	seconds := s.github.Config.PollSeconds
	if seconds == 0 {
		<-s.context().Done()
		return
	}
	timer := time.NewTicker(time.Duration(seconds) * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-s.context().Done():
			return
		case <-timer.C:
			for _, r := range s.github.Config.Repositories {
				ctx, cancel := context.WithTimeout(s.context(), time.Minute)
				jobs, e := s.github.Queued(ctx, r)
				more, e2 := s.github.QueuedInProgress(ctx, r)
				cancel()
				if e != nil || e2 != nil {
					continue
				}
				jobs = append(jobs, more...)
				for _, j := range jobs {
					s.readLock()
					owner := s.journal.Control.OwnerID
					s.mu.Unlock()
					s.lifeStart(caller{Principal: owner}, "job_create", map[string]any{"plugin_id": "pg_github", "external_id": fmt.Sprintf("%s/%d", r.Name, j.ID), "idempotency_key": fmt.Sprintf("github:%d:%d", r.ID, j.ID)})
				}
			}
		}
	}
}

// ownerMachineAdmission applies the owner's shared LX ceiling across private
// supervisors, not merely the allocations known to this lampd.
func ownerMachineAdmission() error {
	if runtime.GOOS != "linux" {
		return reject("VM_REQUIRED", "GitHub CI currently requires the enrolled Linux Firecracker worker")
	}
	out, err := exec.Command("pgrep", "-c", "firecracker").Output()
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return reject("NO_CAPACITY", "cannot observe host VM count")
		}
	}
	count, e := strconv.Atoi(strings.TrimSpace(string(out)))
	if e != nil || count >= 2 {
		return reject("NO_CAPACITY", "owner host VM ceiling reached")
	}
	mem, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return err
	}
	available := int64(0)
	for _, line := range strings.Split(string(mem), "\n") {
		if strings.HasPrefix(line, "MemAvailable:") {
			f := strings.Fields(line)
			if len(f) > 1 {
				available, _ = strconv.ParseInt(f[1], 10, 64)
			}
		}
	}
	if available < 12*1024*1024 {
		return reject("NO_CAPACITY", "owner host needs 12 GiB available memory")
	}
	return nil
}
func (s *Service) ciCreateSandbox(ctx context.Context, id string, r ci.Repository) (string, error) {
	for {
		if err := ownerMachineAdmission(); err == nil {
			break
		}
		if runtime.GOOS != "linux" {
			return "", reject("VM_REQUIRED", "CI requires the enrolled Linux VM worker")
		}
		if s.ciSnapshot(id).Cancel {
			return "", context.Canceled
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	j := s.ciSnapshot(id)
	a := caller{Principal: textField(j.R, "owner_id")}
	code, out := s.internalHTTP(a, "POST", "/v1/sandboxes", map[string]string{"Idempotency-Key": "ci-create:" + id}, map[string]any{"template_id": r.TemplateID, "resources": map[string]any{"vcpus": 2, "memory_mib": 4096, "disk_gib": 20, "pids_max": 4096}})
	if code >= 400 {
		return "", errors.New("fresh CI sandbox admission failed")
	}
	opID, _ := out["operation_id"].(string)
	s.readLock()
	rec := s.journal.Operations[opID]
	if rec == nil {
		s.mu.Unlock()
		return "", errors.New("fresh CI sandbox identity unavailable")
	}
	sandbox := rec.Resource.Id
	done := s.done[opID]
	s.mu.Unlock()
	// Persist the identity before waiting: recovery also resolves the create
	// operation's durable idempotency key if this write is interrupted.
	if err := s.ciUpdate(id, func(j *ciJob) { j.R["sandbox_id"] = sandbox }); err != nil {
		return sandbox, err
	}
	select {
	case <-ctx.Done():
		return sandbox, ctx.Err()
	case <-done:
	}
	s.readLock()
	sb := s.journal.Sandboxes[sandbox]
	s.mu.Unlock()
	if sb.State != "ready" {
		s.readLock()
		failure := s.journal.Operations[opID].Operation.Error
		s.mu.Unlock()
		if failure != nil {
			return sandbox, fmt.Errorf("fresh CI VM did not become ready (%s: %s)", failure.Code, failure.Message)
		}
		return sandbox, errors.New("fresh CI VM did not become ready")
	}
	if value(sb.CaptureTrust) != "clean" {
		return sandbox, reject("VM_REQUIRED", "CI needs a clean, signed-out base template")
	}
	if err := s.ciUpdate(id, func(j *ciJob) { j.R["task_id"] = value(sb.TaskId) }); err != nil {
		return sandbox, err
	}
	// Inspect the actual root-owned per-generation network attachment emitted
	// by the enrolled supervisor, not a caller's boolean policy assertion.
	var raw []byte
	var err error
	reader := s.github.Config.NetworkReader
	if len(reader) > 0 {
		raw, err = exec.CommandContext(ctx, reader[0], append(append([]string{}, reader[1:]...), sandbox, strconv.FormatUint(value(sb.Generation), 10))...).Output()
	} else {
		matches, e := filepath.Glob(filepath.Join(s.github.Config.AuditDirectory, fmt.Sprintf("%s-g%d-*", sandbox, value(sb.Generation)), "network.json"))
		if e != nil || len(matches) != 1 {
			return sandbox, reject("SCOPE_DENIED", "CI network attachment cannot be verified")
		}
		raw, err = os.ReadFile(matches[0])
	}
	if err != nil {
		return sandbox, reject("SCOPE_DENIED", "CI network attachment cannot be read")
	}
	var policy struct {
		AuditReservation uint64 `json:"audit_reservation_bytes"`
		Require          bool   `json:"require_allowlist"`
		Exceptions       []any  `json:"exceptions"`
		Domains          []any  `json:"subscription_domains"`
		Ceiling          []struct {
			Default string `json:"default_egress"`
			Allow   []struct {
				CIDR string `json:"cidr"`
			} `json:"allow"`
		} `json:"policy_ceiling"`
	}
	if json.Unmarshal(raw, &policy) != nil || !policy.Require || !s.ciBrokerNetwork(id, r, policy.Exceptions) || len(policy.Domains) > 0 {
		return sandbox, reject("SCOPE_DENIED", "CI requires an immutable allowlist; only an exact enrolled trusted model-broker exception is permitted")
	}
	if policy.AuditReservation > s.auditReservationBytes {
		return sandbox, reject("CONTROL_QUOTA_EXCEEDED", "installed audit allowance exceeds the enrolled per-sandbox reservation")
	}
	deny := false
	for _, l := range policy.Ceiling {
		if l.Default == "deny" && len(l.Allow) > 0 {
			deny = true
		}
		for _, r := range l.Allow {
			if r.CIDR == "0.0.0.0/0" || r.CIDR == "::/0" {
				return sandbox, reject("SCOPE_DENIED", "CI cannot admit an unrestricted transport allowlist")
			}
		}
	}
	if !deny {
		return sandbox, reject("SCOPE_DENIED", "CI requires an immutable deny-by-default network ceiling")
	}
	return sandbox, nil
}
func (s *Service) runCIJob(ctx context.Context, id string) (err error) {
	// Cleanup has independent authority and a bounded deadline even when a
	// workflow, caller or controller cancels the execution context.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
		e := s.cleanupCI(cleanup, id, err == nil)
		if e != nil {
			err = errors.Join(err, e)
		}
	}()
	if err = s.ciState(id, "preparing"); err != nil {
		return err
	}
	j := s.ciSnapshot(id)
	r, _ := s.ciRepo(j.Repository)
	demand, err := s.github.Verify(ctx, r, j.External)
	if err != nil {
		return err
	}
	if err = s.ciUpdate(id, func(j *ciJob) {
		j.Run = demand.Run.ID
		j.Workflow = demand.Run.Path
		labels := object(j.R["labels"])
		labels["route"] = demand.Route
		j.R["labels"] = labels
		j.R["repository_visibility"] = map[bool]string{true: "public", false: "private"}[!demand.Run.Repository.Private]
		j.R["trust_class"] = map[bool]string{true: "public_pr", false: "trusted"}[demand.Public]
		j.Retain = r.RetainStopped && !demand.Public
		j.R["source_identity"] = map[string]any{"issuer": ci.GitHubIssuer, "repository_id": strconv.FormatInt(r.ID, 10), "repository_owner_id": strconv.FormatInt(r.OwnerID, 10), "sha": demand.Run.HeadSHA, "event": demand.Run.Event}
	}); err != nil {
		return err
	}
	sandbox, err := s.ciCreateSandbox(ctx, id, r)
	if err != nil {
		return err
	}
	// Fresh local copy of a clean runner distribution; never configure or reuse
	// the enrolled image's runner. No host credentials are copied into the guest.
	_, err = s.guestPython(ctx, sandbox, `import os,shutil,sys
src=sys.argv[1];dst='/var/tmp/lamp-ci-runner'
if any(os.path.lexists(os.path.join(src,p)) for p in ('.runner','.credentials','.credentials_rsaparams','_work')): raise SystemExit(1)
if os.path.exists(dst): raise SystemExit(1)
shutil.copytree(src,dst,symlinks=True)
os.chmod(dst,0o700)
`, r.RunnerDirectory)
	if err != nil {
		return fmt.Errorf("clean runner distribution unavailable in template: %w", err)
	}
	// Recheck the forge after VM preparation; do not issue authority for a job
	// canceled while allocating. JIT config is only ever in this stack and the
	// private worker envelope, never public exec argv/env or operation metadata.
	demand, err = s.github.Verify(ctx, r, j.External)
	if err != nil {
		return err
	}
	if s.ciSnapshot(id).Cancel {
		return context.Canceled
	}
	if err = s.ciUpdate(id, func(j *ciJob) { j.Registering = true }); err != nil {
		return err
	}
	runner, jit, err := s.github.JIT(ctx, r, demand.RunnerLabel, "lamp-"+id)
	if err != nil {
		return err
	}
	if err = s.ciUpdate(id, func(j *ciJob) {
		j.Runner = runner
		j.R["runner_ref"] = strconv.FormatInt(runner, 10)
		actions := []string{"one-job-runner"}
		if demand.Route == "copilot_cloud_agent" {
			actions = append(actions, "coding-agent-session")
		}
		j.R["job_token"] = map[string]any{"issuer": "https://github.com", "audience": r.Name, "actions": actions, "resource_ids": []string{fmt.Sprint(j.External)}}
	}); err != nil {
		s.github.Deregister(ctx, r, runner)
		return err
	}
	modelEnv, err := s.ciModelEnvironment(ctx, id, r)
	if err != nil {
		return err
	}
	req := s.workerRequest(ctx, "ci-runner", sandbox, s.sandboxGeneration(sandbox))
	command := req.Context.OperationId
	if err = s.ciUpdate(id, func(j *ciJob) { j.Command = command }); err != nil {
		return err
	}
	req.Command = &pb.Request_Start{Start: &pb.Start{Cwd: "/var/tmp/lamp-ci-runner", Argv: []string{"python3", "-c", `import os
jit=os.environ.pop('LAMP_JIT_CONFIG')
os.environ['HOME']='/tmp'
os.execv('./run.sh',['./run.sh','--jitconfig',jit])`}, Env: map[string]string{"LAMP_JIT_CONFIG": jit, "PATH": "/usr/local/bin:/usr/bin:/bin", "HOME": "/tmp", "RUNNER_ALLOW_RUNASROOT": "1", "TAR_OPTIONS": "--no-same-owner"}}}
	for k, v := range modelEnv {
		req.GetStart().Env[k] = v
	}
	// The protected collector presents UID 0 inside its task user namespace,
	// mapped to an unprivileged guest UID. This skips only the runner's cosmetic
	// UID check; it grants no host, supervisor or outer-guest authority. GNU
	// tar must likewise keep extracted files owned by this one mapped task UID,
	// rather than trying to chown archives to unmapped build-machine accounts.
	err = s.worker.perform(ctx, req, "command_started")
	jit = ""
	req = nil
	if err != nil {
		return errors.New("one-job runner launch failed")
	}
	if err = s.ciState(id, "running"); err != nil {
		return err
	}
	timer := time.NewTicker(3 * time.Second)
	defer timer.Stop()
	var exitedAt time.Time
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			current := s.ciSnapshot(id)
			if current.Cancel {
				if e := s.github.Cancel(ctx, r, current.Run); e != nil {
					return e
				}
				s.ciUpdate(id, func(j *ciJob) { j.R["result"] = "cancelled" })
				return nil
			}
			observed, e := s.github.GetJob(ctx, r, j.External)
			if e != nil {
				continue
			}
			if observed.RunnerID != 0 && observed.RunnerID != runner {
				return errors.New("forge assigned demand to a different runner")
			}
			if observed.Status == "completed" {
				s.ciUpdate(id, func(j *ciJob) { j.R["result"] = observed.Conclusion })
				return nil
			}
			q := s.workerRequest(ctx, "ci-observe", sandbox, s.sandboxGeneration(sandbox))
			q.Command = &pb.Request_Inspect{Inspect: &pb.Inspect{CommandOperationId: command}}
			receipt, e := s.worker.call(ctx, q)
			if e != nil {
				return errors.New("one-job runner observation lost")
			}
			if receipt.Command != nil && receipt.Command.FinishedAtUnixNs != 0 {
				if receipt.Command.ExitCode != 0 || receipt.Command.Signal != 0 {
					return fmt.Errorf("runner exited before native job completion (exit %d, signal %d)", receipt.Command.ExitCode, receipt.Command.Signal)
				}
				// GitHub's job observation can lag the runner's successful exit. Keep
				// polling the native result briefly, without restarting the runner.
				if exitedAt.IsZero() {
					exitedAt = time.Now()
				} else if time.Since(exitedAt) > time.Minute {
					return errors.New("native job result unavailable after runner exit")
				}
			}
		}
	}
}
func (s *Service) cleanupCI(ctx context.Context, id string, allowRetain bool) error {
	s.ciState(id, "collecting")
	j := s.ciSnapshot(id)
	r, ok := s.ciRepo(j.Repository)
	if !ok {
		// Removing intake policy must not strand execution. Cleanup may still
		// revoke the previously enrolled repository's authority, but never retain
		// its VM or admit more work. Forge failure is reported after VM release.
		r = ci.Repository{Name: j.Repository}
		allowRetain = false
	}
	// Resolve a create whose response was lost. Idempotency is permanent; never
	// allocate again or turn uncertainty into a second runner.
	if textField(j.R, "sandbox_id") == "" {
		s.readLock()
		key := textField(j.R, "owner_id") + "\nsandbox_create\nci-create:" + id
		if op := s.keys[key]; op != "" {
			j.R["sandbox_id"] = s.journal.Operations[op].Resource.Id
		}
		s.mu.Unlock()
		s.ciUpdate(id, func(n *ciJob) {
			if v := textField(j.R, "sandbox_id"); v != "" {
				n.R["sandbox_id"] = v
			}
		})
	}
	// Native cancellation first asks GitHub to revoke the running job token.
	// An interrupted controller never leaves a job with live forge authority.
	result := textField(j.R, "result")
	if j.Run != 0 && result == "" {
		if observed, e := s.github.GetJob(ctx, r, j.External); e == nil && observed.Status == "completed" {
			result = observed.Conclusion
			s.ciUpdate(id, func(j *ciJob) { j.R["result"] = result })
		}
	}
	if j.Run != 0 && result == "" {
		s.github.Cancel(ctx, r, j.Run)
	}
	brokerErr := s.ciRevokeModel(ctx, id, r)
	var deregErr error
	if j.Registering || j.Runner != 0 {
		deregErr = s.github.DeregisterJob(ctx, r, "lamp-"+id, j.Runner)
	}
	sandboxID := textField(j.R, "sandbox_id")
	keep := allowRetain && j.Retain && deregErr == nil && result != "" && result != "cancelled"
	if keep && j.Command != "" {
		for keep {
			q := s.workerRequest(ctx, "ci-retain-observe", sandboxID, s.sandboxGeneration(sandboxID))
			q.Command = &pb.Request_Inspect{Inspect: &pb.Inspect{CommandOperationId: j.Command}}
			receipt, e := s.worker.call(ctx, q)
			if e != nil {
				keep = false
				break
			}
			if receipt.Command != nil && receipt.Command.FinishedAtUnixNs != 0 {
				break
			}
			if receipt.Detail["command_state"] != "running" {
				keep = false
				break
			}
			select {
			case <-ctx.Done():
				keep = false
			case <-time.After(200 * time.Millisecond):
			}
		}
	} else {
		keep = false
	}
	if !keep && sandboxID != "" {
		// Freeze execution before collecting any bytes. Delete below also releases
		// the namespace and all job-owned state, even when this stop was uncertain.
		q := s.workerRequest(ctx, "ci-end-stop", sandboxID, s.sandboxGeneration(sandboxID))
		q.Command = &pb.Request_Stop{Stop: &pb.Stop{Force: true}}
		s.worker.perform(ctx, q, "stopped")
	}
	// Native outputs already survive VM removal on GitHub. Also capture them in
	// Lamp's durable object store; unavailable bytes remain explicitly incomplete.
	s.collectCIOutputs(ctx, id, r, j)
	sandbox := textField(j.R, "sandbox_id")
	var releaseErr error
	if sandbox != "" {
		s.readLock()
		sb := s.journal.Sandboxes[sandbox]
		s.mu.Unlock()
		if sb.State != "deleted" {
			if keep {
				// Strip all runner registration and transport files before retaining inert
				// encrypted state. The stopped task cannot resume under its old authority.
				_, e := s.guestPython(ctx, sandbox, `import os,shutil
root='/var/tmp/lamp-ci-runner'
work=root+'/_work'
if os.path.isdir(work):
 shutil.rmtree(work+'/_temp',ignore_errors=True)
 shutil.move(work,'/var/tmp/lamp-ci-inspection')
shutil.rmtree(root)`)
				if e == nil {
					op, _, e2 := s.lifeStart(caller{Principal: sb.OwnerId}, "sandbox_suspend", map[string]any{"sandbox_id": sandbox, "expected_revision": jsonNumber(sb.Revision), "durability": "local", "idempotency_key": "ci-retain:" + id})
					if e2 == nil {
						s.readLock()
						done := s.done[op]
						s.mu.Unlock()
						select {
						case <-done:
						case <-ctx.Done():
						}
						s.readLock()
						sb = s.journal.Sandboxes[sandbox]
						s.mu.Unlock()
						if sb.State != "suspended" {
							e = errors.New("CI retention did not stop the VM")
						}
					} else {
						e = e2
					}
				}
				if e != nil {
					keep = false
				}
			}
			if !keep {
				why := "ci-job-end-" + strconv.FormatUint(sb.Revision, 10)
				s.internalDelete(sb, why)
				s.readLock()
				key := sb.OwnerId + "\nsandbox_delete\nlamp-" + why + "-delete:" + sb.Id
				op := s.keys[key]
				var done chan struct{}
				if op != "" {
					done = s.done[op]
				}
				s.mu.Unlock()
				if done != nil {
					select {
					case <-done:
					case <-ctx.Done():
					}
				}
				s.readLock()
				sb = s.journal.Sandboxes[sandbox]
				s.mu.Unlock()
				if sb.State != "deleted" {
					releaseErr = errors.New("CI sandbox cleanup is not complete")
				}
			}
		}
		s.readLock()
		if t, exists := s.journal.Accountability.Tasks[value(sb.TaskId)]; exists {
			t.State = "stopped"
			t.Revision++
			t.UpdatedAt = timestamp()
			s.journal.Accountability.Tasks[t.Id] = t
			if s.save("$accountability") != nil {
				s.broken = true
			}
		}
		s.mu.Unlock()
	}
	if brokerErr != nil || deregErr != nil || releaseErr != nil {
		s.ciState(id, "cleanup_failed")
		return errors.New("CI authority or VM cleanup needs reconciliation")
	}
	state := "failed"
	if result == "success" {
		state = "completed"
	}
	if result == "cancelled" || j.Cancel {
		state = "canceled"
	}
	return s.ciState(id, state)
}
func (s *Service) collectCIOutputs(ctx context.Context, id string, r ci.Repository, j ciJob) {
	if j.Run == 0 {
		return
	}
	outputs, err := s.github.Outputs(ctx, r, j.Run, j.External)
	if err != nil && len(outputs) == 0 {
		return
	}
	remaining := int64(32 << 20)
	s.readLock()
	for _, a := range s.life.Artifacts {
		if textField(a.R, "job_id") == id && textField(a.R, "state") == "complete" {
			remaining -= int64(uintField(a.R, "present_bytes"))
		}
	}
	s.mu.Unlock()
	if remaining < 0 {
		remaining = 0
	}
	for _, output := range outputs {
		// Stable label prevents duplicate publications during recovery.
		label := output.Name
		s.readLock()
		found := false
		for _, a := range s.life.Artifacts {
			if textField(a.R, "job_id") == id {
				labels, _ := a.R["labels"].(map[string]any)
				if textField(labels, "native_output") == label && textField(a.R, "state") == "complete" {
					found = true
				}
			}
		}
		s.mu.Unlock()
		if found {
			continue
		}
		data, e := s.github.Download(ctx, output.Path, remaining)
		if e == nil {
			remaining -= int64(len(data))
		}
		s.readLock()
		owner := textField(j.R, "owner_id")
		if uint64(len(s.life.Artifacts)+len(s.life.Blobs)) >= s.quotas.Records {
			labels := object(s.life.Jobs[id].R["labels"])
			labels["output_capture"] = "control_quota_exceeded"
			s.life.Jobs[id].R["labels"] = labels
			if s.saveLife() != nil {
				s.broken = true
			}
			s.mu.Unlock()
			return
		}
		var plan lifePlan
		if e == nil {
			sum, putErr := s.putObject(data)
			if putErr == nil {
				b := s.newReadyBlob(owner, sum, int64(len(data)), "application/octet-stream", "")
				plan, putErr = s.planArtifactCreate(caller{Principal: owner}, map[string]any{"job_id": id, "download_blob_id": textField(b, "id")})
				if putErr == nil {
					a := s.life.Artifacts[plan.ref.Id]
					a.R["labels"] = map[string]any{"native_output": label}
				}
			}
			e = putErr
		}
		if e != nil {
			m := s.lifeResource("ar", "artifact", owner)
			m["job_id"] = id
			m["state"] = "incomplete"
			m["expected_bytes"] = jsonNumber(output.Size)
			m["present_bytes"] = jsonNumber(0)
			m["labels"] = map[string]any{"native_output": label}
			aid := textField(m, "id")
			s.life.Artifacts[aid] = &lifeArtifact{R: m}
			plan.ref = lifeRef("artifact", aid)
		}
		if plan.ref.Id != "" {
			current := s.life.Jobs[id]
			ids := stringsField(current.R, "artifact_ids")
			current.R["artifact_ids"] = appendOnce(ids, plan.ref.Id)
			editResource(current.R)
		}
		if s.saveLife() != nil {
			s.broken = true
		}
		s.mu.Unlock()
	}
}

// ciPluginHTTP is intentionally a host adapter descriptor, not a simulated
// installed worker or a claim about vendor-cloud placement.
func (s *Service) ciPluginHTTP(w http.ResponseWriter, r *http.Request, id string) {
	s.readLock()
	defer s.mu.Unlock()
	if !s.can(requestCaller(r), "plugin.get") || !s.resourceAllowed(requestCaller(r), lifeRef("plugin", "pg_github")) {
		writeError(w, id, "NOT_FOUND", "plugin not found", nil)
		return
	}
	m := newResource("plugin", s.journal.Control.OwnerID)
	m["id"] = "pg_github"
	m["created_at"] = s.journal.Control.Fleet.CreatedAt
	m["updated_at"] = s.journal.Control.Fleet.CreatedAt
	m["type"] = "ci"
	m["state"] = "disabled"
	if s.github != nil {
		m["state"] = "enabled"
	}
	m["version"] = "1"
	m["adapter_ids"] = []string{"github_actions"}
	m["labels"] = map[string]string{"runner_mode": "jit-one-job"}
	prepareJSON(w, 200, map[string]any{"data": m, "request_id": id})()
}
