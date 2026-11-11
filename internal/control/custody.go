package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/JohanSardar/lamp/pkg/api"
	pb "github.com/JohanSardar/lamp/pkg/workerpb"
)

// Native device authorization and offline clean builds outlive an RPC admission.
// This is a bounded operation lifetime, not an E01/E02 performance threshold.
const custodyOperationTimeout = 10 * time.Minute

type savedSnapshot struct {
	Snapshot     api.Snapshot `json:"snapshot"`
	CaptureID    string       `json:"capture_id,omitempty"`
	TemplateID   string       `json:"template_id,omitempty"`
	ExportBlobID string       `json:"export_blob_id,omitempty"`
}

type cleanExport struct {
	TemplateID     string `json:"template_id"`
	BlobID         string `json:"blob_id"`
	ManifestDigest string `json:"manifest_digest"`
	OwnerID        string `json:"owner_id"`
	CreatedAt      string `json:"created_at"`
}

type custodyPlan struct {
	Name       string `json:"name"`
	Source     string `json:"source"`
	SnapshotID string `json:"snapshot_id,omitempty"`
	Clean      bool   `json:"clean"`
	Subject    string `json:"subject,omitempty"`
	Durability string `json:"durability"`
}

func admittedSubscriptionExec(ex api.Exec) bool {
	if ex.Argv == nil || (ex.Env != nil && len(*ex.Env) != 0) || (ex.Cwd != nil && *ex.Cwd != "/" && *ex.Cwd != "") {
		return false
	}
	argv := *ex.Argv
	if len(argv) == 1 && argv[0] == "/bin/true" {
		return true
	}
	if len(argv) < 2 || argv[0] != "/usr/local/bin/lamp-subscription" {
		return false
	}
	if len(argv) == 3 && argv[1] == "init" {
		if len(argv[2]) != 32 {
			return false
		}
		for _, value := range argv[2] {
			if !(value >= '0' && value <= '9') && !(value >= 'a' && value <= 'f') {
				return false
			}
		}
		return true
	}
	if len(argv) != 2 {
		return false
	}
	switch argv[1] {
	case "status", "sign-in", "start", "turn", "refresh", "proof", "stop":
		return true
	default:
		return false
	}
}

func (s *Service) initCustody() {
	if s.journal.Snapshots == nil {
		s.journal.Snapshots = map[string]savedSnapshot{}
	}
	if s.journal.Shares == nil {
		s.journal.Shares = map[string]api.Share{}
	}
	if s.journal.Templates == nil {
		s.journal.Templates = map[string]cleanExport{}
	}
}

func (s *Service) serveCustody(w http.ResponseWriter, r *http.Request, requestID string) bool {
	path := r.URL.Path
	if r.Method == "GET" && (strings.HasPrefix(path, "/v1/blobs/") || strings.HasPrefix(path, "/v1/templates/")) {
		s.serveExport(w, r, requestID)
		return true
	}
	if r.Method == "GET" && (strings.HasPrefix(path, "/v1/snapshots/") || strings.HasPrefix(path, "/v1/shares/")) {
		s.readLock()
		var send func()
		if s.broken {
			send = prepareError(w, requestID, api.ErrorDetail{Code: "DATA_UNAVAILABLE", Message: "journal unavailable"})
		} else if strings.HasPrefix(path, "/v1/snapshots/") {
			if snapshot, ok := s.journal.Snapshots[strings.TrimPrefix(path, "/v1/snapshots/")]; ok && s.sandboxAllowed(requestCaller(r), s.journal.Sandboxes[value(snapshot.Snapshot.SandboxId)]) && s.resourceAllowed(requestCaller(r), api.ResourceRef{Kind: "snapshot", Id: snapshot.Snapshot.Id}) {
				send = prepareJSON(w, 200, api.SnapshotGetResult{RequestId: requestID, Data: snapshot.Snapshot})
			}
		} else if share, ok := s.journal.Shares[strings.TrimPrefix(path, "/v1/shares/")]; ok {
			a := requestCaller(r)
			if a.Principal == "" || (s.own(a, share.OwnerId) && s.resourceAllowed(a, api.ResourceRef{Kind: "share", Id: share.Id})) {
				send = prepareJSON(w, 200, api.ShareGetResult{RequestId: requestID, Data: share})
			}
		}
		if send == nil {
			send = prepareError(w, requestID, api.ErrorDetail{Code: "NOT_FOUND", Message: "resource not found"})
		}
		s.mu.Unlock()
		send()
		return true
	}
	suspend := strings.HasPrefix(path, "/v1/sandboxes/") && strings.HasSuffix(path, ":suspend")
	resume := strings.HasPrefix(path, "/v1/sandboxes/") && strings.HasSuffix(path, ":resume")
	if r.Method != "POST" || (path != "/v1/snapshots" && path != "/v1/shares" && !suspend && !resume) {
		return false
	}
	fail := func(code, message string) bool { writeError(w, requestID, code, message, nil); return true }
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || strings.TrimSpace(keys[0]) == "" || len(keys[0]) > 256 || !utf8.ValidString(keys[0]) {
		return fail("INVALID_ARGUMENT", "one bounded Idempotency-Key is required")
	}
	key := keys[0]
	plan := custodyPlan{Durability: "volatile"}
	var input any
	var wait *uint64
	var revision uint64
	now := timestamp()
	var snapshot savedSnapshot
	var share api.Share
	switch {
	case path == "/v1/snapshots":
		var in api.SnapshotCreateInput
		_, err := decodeInput(w, r, &in, "sandbox_id preserves_memory capture_trust durability wait_ms")
		if err != nil || in.SandboxId == nil || *in.SandboxId == "" {
			return fail("INVALID_ARGUMENT", "snapshot requires sandbox_id and no caller-supplied exposure attestation")
		}
		if in.CaptureTrust != nil && *in.CaptureTrust != "clean" && *in.CaptureTrust != "owner_private" {
			return fail("INVALID_ARGUMENT", "invalid capture trust")
		}
		plan.Name, plan.Source = "snapshot_create", *in.SandboxId
		plan.Clean = in.CaptureTrust != nil && *in.CaptureTrust == "clean"
		if in.PreservesMemory != nil && *in.PreservesMemory == plan.Clean {
			return fail("CLEAN_REBUILD_REQUIRED", "a clean export cannot preserve private memory; private captures must preserve memory")
		}
		if in.Durability != nil {
			plan.Durability = *in.Durability
		}
		wait, in.WaitMs, in.IdempotencyKey = in.WaitMs, nil, key
		input = in
	case path == "/v1/shares":
		var in api.ShareCreateInput
		_, err := decodeInput(w, r, &in, "resource_kind resource_id subject_id actions wait_ms")
		if err != nil || in.ResourceKind == nil || in.ResourceId == nil || in.SubjectId == nil || *in.SubjectId == "" || *in.ResourceKind != "sandbox" {
			return fail("INVALID_ARGUMENT", "share requires a source sandbox, subject_id and bounded actions")
		}
		if in.Actions != nil && (len(*in.Actions) != 1 || (*in.Actions)[0] != "template.read") {
			return fail("SCOPE_DENIED", "a rebuilt handoff grants only template.read, never the private source")
		}
		if !s.enrolledShareReader(*in.SubjectId) {
			return fail("SCOPE_DENIED", "share recipient must be an enrolled read-only principal")
		}
		plan.Name, plan.Source, plan.Subject, plan.Clean = "share_create", *in.ResourceId, *in.SubjectId, true
		share = api.Share{Id: newID("sh"), Kind: "share", FleetId: "fl_local", OwnerId: "pr_local", Revision: 1, CreatedAt: now, UpdatedAt: now, State: "pending_clean_handoff", Labels: api.StringMap{}, Visibility: "private", SubjectId: in.SubjectId, Actions: &[]string{"template.read"}}
		wait, in.WaitMs, in.IdempotencyKey = in.WaitMs, nil, key
		input = in
	case suspend:
		var in api.SandboxSuspendInput
		var err error
		revision, err = expectedRevision(r)
		if err == nil {
			_, err = decodeInput(w, r, &in, "sandbox_id durability wait_ms")
		}
		source := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/sandboxes/"), ":suspend")
		if err != nil || (in.SandboxId != "" && in.SandboxId != source) {
			return fail("INVALID_ARGUMENT", "suspend requires matching sandbox_id and If-Match")
		}
		in.SandboxId, in.ExpectedRevision, in.IdempotencyKey = source, revision, key
		plan.Name, plan.Source = "sandbox_suspend", source
		if in.Durability != nil {
			plan.Durability = string(*in.Durability)
		}
		wait, in.WaitMs = in.WaitMs, nil
		input = in
	case resume:
		var in api.SandboxResumeInput
		var err error
		revision, err = expectedRevision(r)
		if err == nil {
			_, err = decodeInput(w, r, &in, "sandbox_id wait_ms")
		}
		source := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/sandboxes/"), ":resume")
		if err != nil || (in.SandboxId != "" && in.SandboxId != source) {
			return fail("INVALID_ARGUMENT", "resume requires matching sandbox_id and If-Match")
		}
		in.SandboxId, in.ExpectedRevision, in.IdempotencyKey = source, revision, key
		plan.Name, plan.Source = "sandbox_resume", source
		wait, in.WaitMs = in.WaitMs, nil
		input = in
	}
	if plan.Source == "" || strings.Contains(plan.Source, "/") || (wait != nil && *wait > 60000) {
		return fail("INVALID_ARGUMENT", "invalid source or wait_ms")
	}
	if code := admittedDurability(plan.Durability); code != "" {
		return fail(code, "unsupported durability")
	}
	encoded, _ := json.Marshal(input)
	sum := sha256.Sum256(append([]byte(plan.Name+"\n"), encoded...))
	hash := hex.EncodeToString(sum[:])
	a := requestCaller(r)
	journalKey := a.Principal + "\n" + plan.Name + "\n" + key
	s.readLock()
	if s.broken {
		s.mu.Unlock()
		return fail("DATA_UNAVAILABLE", "journal unavailable")
	}
	if !s.can(a, strings.Replace(plan.Name, "_", ".", 1)) {
		s.mu.Unlock()
		return fail("SCOPE_DENIED", "authority changed")
	}
	if id, ok := s.keys[journalKey]; ok {
		if !s.operationAllowed(a, s.journal.Operations[id]) {
			s.mu.Unlock()
			return fail("NOT_FOUND", "operation not found")
		}
		if s.journal.Operations[id].Hash != hash {
			s.mu.Unlock()
			return fail("IDEMPOTENCY_CONFLICT", "idempotency key was already used")
		}
		s.mu.Unlock()
		s.respond(w, r, requestID, id, true, wait)
		return true
	}
	if len(s.journal.Operations) >= maxOperations {
		s.mu.Unlock()
		return fail("CONTROL_QUOTA_EXCEEDED", "operation quota exceeded")
	}
	sb, ok := s.journal.Sandboxes[plan.Source]
	if !ok || !s.sandboxAllowed(a, sb) {
		s.mu.Unlock()
		return fail("NOT_FOUND", "sandbox not found")
	}
	if (suspend || resume) && sb.Revision != revision {
		s.mu.Unlock()
		return fail("REVISION_CONFLICT", "sandbox revision changed")
	}
	expected := api.SandboxState("ready")
	if resume {
		expected = "suspended"
	}
	if (sb.State != expected && !(plan.Clean && (sb.State == "suspended" || sb.State == "unavailable"))) || s.busy[plan.Source] || s.starting[plan.Source] > 0 {
		s.mu.Unlock()
		return fail("RESOURCE_IN_USE", "sandbox is not in the required state")
	}
	if resume && sb.SnapshotId == nil {
		s.mu.Unlock()
		return fail("CONTINUATION_UNSUPPORTED", "suspend capture missing")
	}
	if err := s.accountAdmission(a, plan.Name, sb); err != nil {
		s.mu.Unlock()
		e := err.(*coreError)
		return fail(e.code, e.message)
	}
	if !s.appendAudit(sb, "control", plan.Name, "allow") {
		s.mu.Unlock()
		return fail("CONTROL_QUOTA_EXCEEDED", "audit retention quota reached")
	}
	if !resume {
		s.chargeFork(sb)
	}
	ref := api.ResourceRef{Kind: "sandbox", Id: sb.Id}
	if plan.Name == "snapshot_create" || suspend {
		plan.SnapshotID = newID("ss")
		snapshot.Snapshot = api.Snapshot{Id: plan.SnapshotID, Kind: "snapshot", FleetId: "fl_local", OwnerId: sb.OwnerId, Revision: 1, CreatedAt: now, UpdatedAt: now, State: "capturing", Labels: api.StringMap{}, Visibility: "private", SandboxId: pointer(sb.Id), Generation: sb.Generation, Durability: pointer(plan.Durability)}
		s.journal.Snapshots[plan.SnapshotID] = snapshot
		if !suspend {
			ref = api.ResourceRef{Kind: "snapshot", Id: plan.SnapshotID}
		}
	}
	if resume {
		if sb.SnapshotId == nil {
			s.mu.Unlock()
			return fail("CONTINUATION_UNSUPPORTED", "suspend capture missing")
		}
		plan.SnapshotID = *sb.SnapshotId
	}
	if plan.Name == "share_create" {
		share.OwnerId = sb.OwnerId
		s.journal.Shares[share.Id] = share
		ref = api.ResourceRef{Kind: "share", Id: share.Id}
	}
	id := newID("op")
	op := api.Operation{Id: id, Kind: "operation", FleetId: "fl_local", OwnerId: sb.OwnerId, Revision: 1, CreatedAt: now, UpdatedAt: now, State: "accepted", Labels: api.StringMap{}, Visibility: "private", OperationName: &plan.Name, ResourceIds: &[]api.ResourceRef{ref}, RequestHash: &hash, AcceptedAt: &now, CleanupState: pointer(api.OperationCleanupStateNotNeeded), Durability: pointer(api.OperationDurabilityVolatile)}
	s.journal.Operations[id] = &record{Caller: a, Operation: op, Hash: hash, Key: journalKey, Resource: ref, Custody: &plan}
	s.keys[journalKey], s.done[id], s.busy[plan.Source] = id, make(chan struct{}), true
	if err := s.save(id, ref.Id, plan.SnapshotID, "$accountability"); err != nil {
		s.broken = true
		s.mu.Unlock()
		return fail("DATA_UNAVAILABLE", "custody admission was not committed")
	}
	s.wg.Add(1)
	go s.executeCustody(id)
	s.mu.Unlock()
	s.respond(w, r, requestID, id, false, wait)
	return true
}

func (s *Service) executeCustody(id string) {
	defer s.wg.Done()
	s.readLock()
	rec := s.journal.Operations[id]
	plan, ref := *rec.Custody, rec.Resource
	if s.broken || rec.Operation.State != "accepted" {
		delete(s.busy, plan.Source)
		close(s.done[id])
		s.mu.Unlock()
		return
	}
	if rec.Caller.Principal != "" && !s.can(rec.Caller, strings.Replace(plan.Name, "_", ".", 1)) {
		rec.Operation.State = "failed"
		rec.Operation.Error = &api.ErrorDetail{Code: "SCOPE_DENIED", Message: "authority changed before custody dispatch"}
		rec.Operation.Revision++
		rec.Operation.CompletedAt = pointer(timestamp())
		rec.Operation.UpdatedAt = timestamp()
		rec.Operation.CleanupState = pointer(api.OperationCleanupStateComplete)
		if err := s.save(id); err != nil {
			s.broken = true
		}
		delete(s.busy, plan.Source)
		close(s.done[id])
		s.mu.Unlock()
		return
	}
	sb := s.journal.Sandboxes[plan.Source]
	snapshot := s.journal.Snapshots[plan.SnapshotID]
	rec.Operation.State = "running"
	rec.Operation.Revision++
	if err := s.save(id); err != nil {
		s.broken = true
		delete(s.busy, plan.Source)
		close(s.done[id])
		s.mu.Unlock()
		return
	}
	taskDeadline := s.journal.Accountability.Tasks[value(sb.TaskId)].DeadlineAt
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(s.context(), custodyOperationTimeout)
	if taskDeadline != nil {
		if deadline, e := time.Parse(time.RFC3339Nano, *taskDeadline); e == nil {
			var stop context.CancelFunc
			ctx, stop = context.WithDeadline(ctx, deadline)
			defer stop()
		}
	}
	defer cancel()
	call := func(step string, command any) (*pb.Receipt, error) {
		deadline, _ := ctx.Deadline()
		r := &pb.Request{Context: &pb.Context{RequestId: newID("req"), OperationId: id + "." + step, DeadlineMs: uint64(deadline.UnixMilli()), SandboxId: sb.Id, Generation: value(sb.Generation), PolicyRevision: value(sb.EffectivePolicyRevision), GrantId: s.worker.config.GrantID}}
		switch command := command.(type) {
		case *pb.Inspect:
			r.Command = &pb.Request_Inspect{Inspect: command}
		case *pb.Capture:
			r.Command = &pb.Request_Capture{Capture: command}
		case *pb.Stop:
			r.Command = &pb.Request_Stop{Stop: command}
		case *pb.Restore:
			r.Command = &pb.Request_Restore{Restore: command}
		case *pb.Rebuild:
			r.Command = &pb.Request_Rebuild{Rebuild: command}
		default:
			return nil, errors.New("invalid custody worker step")
		}
		return s.worker.call(ctx, r)
	}
	var receipt *pb.Receipt
	var err error
	var observed *pb.Receipt
	if plan.Name == "sandbox_resume" {
		if snapshot.CaptureID == "" {
			err = &workerFailure{code: "CONTINUATION_UNSUPPORTED", message: "suspend capture missing"}
		} else {
			receipt, err = call("resume", &pb.Restore{CaptureId: snapshot.CaptureID})
		}
	} else if plan.Clean {
		purpose := "export"
		if plan.Name == "share_create" {
			purpose = "share"
		}
		receipt, err = call("rebuild", &pb.Rebuild{SourceId: sb.Id, DestinationPrincipal: plan.Subject, Purpose: purpose, CleanRebuild: true})
		if err == nil && (receipt.State != "rebuilt" || len(receipt.CommittedHandleIds) != 1 || receipt.Detail["capture_trust"] != "clean" || !exportBlobID.MatchString(receipt.Detail["export_blob_id"])) {
			err = errors.New("clean reconstruction receipt missing")
		}
	} else {
		observed, err = call("inspect", &pb.Inspect{})
		if err == nil && observed.Detail["capture_trust"] != "clean" && observed.Detail["capture_trust"] != "owner_private" {
			err = errors.New("worker did not attest capture trust")
		}
		if err == nil {
			consistency := "crash"
			if plan.Durability == "local" {
				consistency = "local"
			}
			receipt, err = call("capture", &pb.Capture{CaptureId: "capture_" + strings.TrimPrefix(plan.SnapshotID, "ss_"), PreservesMemory: true, Consistency: consistency})
		}
		if err == nil && receipt.State != "captured" {
			err = errors.New("memory capture receipt missing")
		}
		if err == nil && plan.Name == "sandbox_suspend" {
			_, err = call("stop", &pb.Stop{Force: true})
		}
	}
	s.readLock()
	defer s.mu.Unlock()
	rec = s.journal.Operations[id]
	now := timestamp()
	builtTemplate := ""
	rec.Operation.CompletedAt, rec.Operation.UpdatedAt = &now, now
	rec.Operation.Revision++
	if err != nil {
		failure := &workerFailure{code: "DATA_UNAVAILABLE", message: "custody worker step failed"}
		errors.As(err, &failure)
		code := failure.code
		if _, ok := errorStatuses[code]; !ok {
			code = "DATA_UNAVAILABLE"
		}
		rec.Operation.State = "failed"
		if failure.unknown {
			rec.Operation.State, code = "outcome_unknown", "OUTCOME_UNKNOWN"
			sb.State = "unavailable"
		}
		rec.Operation.Error = &api.ErrorDetail{Code: api.ErrorCode(code), Message: failure.message, OperationId: &id}
		if plan.SnapshotID != "" && plan.Name != "sandbox_resume" {
			snapshot.Snapshot.State = "failed"
		}
	} else {
		rec.Operation.State = "succeeded"
		if plan.Clean {
			builtTemplate = receipt.CommittedHandleIds[0]
			s.journal.Templates[builtTemplate] = cleanExport{TemplateID: builtTemplate, BlobID: receipt.Detail["export_blob_id"], ManifestDigest: receipt.Detail["manifest_digest"], OwnerID: sb.OwnerId, CreatedAt: now}
		}
		var result any
		switch plan.Name {
		case "sandbox_resume":
			sb.State = "ready"
			result = map[string]string{"sandbox_id": sb.Id}
		case "share_create":
			share := s.journal.Shares[ref.Id]
			share.State, share.UpdatedAt, share.ResourceKind, share.ResourceId = "active", now, pointer("template"), pointer(receipt.CommittedHandleIds[0])
			share.Revision++
			s.journal.Shares[ref.Id] = share
			result = map[string]string{"share_id": ref.Id}
		default:
			snapshot.Snapshot.State, snapshot.Snapshot.UpdatedAt = "ready", now
			snapshot.Snapshot.Revision++
			if plan.Clean {
				snapshot.TemplateID, snapshot.ExportBlobID = receipt.CommittedHandleIds[0], receipt.Detail["export_blob_id"]
				snapshot.Snapshot.CaptureTrust, snapshot.Snapshot.PreservesMemory = pointer(api.SnapshotCaptureTrustClean), pointer(false)
				snapshot.Snapshot.ManifestDigest = pointer(receipt.Detail["manifest_digest"])
			} else {
				snapshot.CaptureID = "capture_" + strings.TrimPrefix(plan.SnapshotID, "ss_")
				snapshot.Snapshot.CaptureTrust, snapshot.Snapshot.PreservesMemory = pointer(api.SnapshotCaptureTrust(observed.Detail["capture_trust"])), pointer(true)
				var hosts []string
				if json.Unmarshal([]byte(observed.Detail["custody_host_ids"]), &hosts) == nil {
					snapshot.Snapshot.CustodyHostIds = &hosts
				}
			}
			result = map[string]string{"snapshot_id": plan.SnapshotID}
			if plan.Name == "sandbox_suspend" {
				sb.State, sb.SnapshotId = "suspended", &plan.SnapshotID
				result = map[string]string{"sandbox_id": sb.Id}
			}
		}
		encoded, _ := json.Marshal(result)
		document := api.AdapterDocument(encoded)
		rec.Operation.Outcome = &api.Outcome{Result: &document}
		if plan.Clean {
			rec.Operation.Outcome.CommittedEffects = &[]api.Effect{
				{ResourceRef: &api.ResourceRef{Kind: "template", Id: builtTemplate}, Action: pointer("clean_rebuild"), State: pointer("committed")},
				{ResourceRef: &api.ResourceRef{Kind: "blob", Id: receipt.Detail["export_blob_id"]}, Action: pointer("clean_export"), State: pointer("committed")},
			}
		}
	}
	sb.Revision++
	sb.UpdatedAt = now
	s.journal.Sandboxes[sb.Id] = sb
	if plan.SnapshotID != "" && plan.Name != "sandbox_resume" {
		s.journal.Snapshots[plan.SnapshotID] = snapshot
	}
	if s.save(id, sb.Id, ref.Id, plan.SnapshotID, builtTemplate) != nil {
		s.broken = true
	}
	delete(s.busy, plan.Source)
	close(s.done[id])
}
