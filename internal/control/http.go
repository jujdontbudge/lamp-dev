package control

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"lamp.eilmer.si/pkg/api"
	pb "lamp.eilmer.si/pkg/workerpb"
)

const maxBody = 64 << 10

// Canonical x-lamp-errors statuses; all demo mutation routes use ErrorEnvelope.
var errorStatuses = map[string]int{
	"INVALID_ARGUMENT": 400, "UNAUTHENTICATED": 401, "NOT_FOUND": 404, "SCOPE_DENIED": 403,
	"REVISION_CONFLICT": 409, "RESULT_EXPIRED": 410, "ENGINE_UNQUALIFIED": 422,
	"NO_CAPACITY": 429, "HOST_UNAVAILABLE": 503, "VM_REQUIRED": 403, "HOST_NOT_TRUSTED": 403,
	"APPROVAL_REQUIRED": 403, "IDEMPOTENCY_CONFLICT": 409, "RESOURCE_IN_USE": 409,
	"OUTCOME_UNKNOWN": 409, "TASK_AUTHORITY_EXPIRED": 409, "KEY_EPOCH_EXPIRED": 410,
	"CURSOR_EXPIRED": 410, "DEVICE_UNAVAILABLE": 422, "DEVICE_NOT_FORKABLE": 422,
	"AUTH_TRANSPORT_UNSUPPORTED": 422, "CONTINUATION_UNSUPPORTED": 422,
	"CLEAN_REBUILD_REQUIRED": 422, "TOKEN_CAP_UNENFORCEABLE": 422,
	"CONTROL_QUOTA_EXCEEDED": 429, "RATE_LIMITED": 429, "DATA_UNAVAILABLE": 503,
	"QUORUM_REQUIRED": 503, "IDEMPOTENCY_HOME_UNAVAILABLE": 503,
	"TOKEN_HOME_UNAVAILABLE": 503, "AUDIT_UNAVAILABLE": 503,
}

// prepareJSON snapshots nested journal values while the caller holds mu. The
// returned writer closes over only immutable bytes and runs after unlocking.
func prepareJSON(w http.ResponseWriter, status int, value any) func() {
	data, err := json.Marshal(value)
	if err != nil {
		status = 503
		data, _ = json.Marshal(api.ErrorEnvelope{RequestId: w.Header().Get("X-Request-ID"), Error: api.ErrorDetail{Code: "DATA_UNAVAILABLE", Message: "cannot encode response"}})
	}
	data = append(data, '\n')
	return func() {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		_, _ = w.Write(data)
	}
}
func writeError(w http.ResponseWriter, requestID, code, message string, operationID *string) {
	prepareError(w, requestID, api.ErrorDetail{Code: api.ErrorCode(code), Message: message, OperationId: operationID})()
}
func prepareError(w http.ResponseWriter, requestID string, detail api.ErrorDetail) func() {
	status, ok := errorStatuses[string(detail.Code)]
	if !ok {
		detail.Code = "DATA_UNAVAILABLE"
		status = 503
	}
	return prepareJSON(w, status, api.ErrorEnvelope{RequestId: requestID, Error: detail})
}

// checkJSON rejects duplicate fields and nulls before typed decoding. In this
// closed demo subset, absence is distinct from an explicitly supplied selector.
func checkJSON(d *json.Decoder) error {
	tok, err := d.Token()
	if err != nil {
		return err
	}
	if tok == nil {
		return errors.New("null is not an admitted value")
	}
	if delim, ok := tok.(json.Delim); ok {
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return errors.New("duplicate JSON field")
				}
				seen[name] = true
				if err := checkJSON(d); err != nil {
					return err
				}
			}
			_, err = d.Token()
			return err
		case '[':
			for d.More() {
				if err := checkJSON(d); err != nil {
					return err
				}
			}
			_, err = d.Token()
			return err
		default:
			return errors.New("invalid JSON delimiter")
		}
	}
	return nil
}
func decodeInput(w http.ResponseWriter, r *http.Request, out any, allowed string) (map[string]json.RawMessage, error) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	if err := checkJSON(d); err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errors.New("expected one JSON object")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return nil, errors.New("expected a JSON object")
	}
	for key := range fields {
		if !strings.Contains(" "+allowed+" ", " "+key+" ") {
			return nil, fmt.Errorf("%s is not supported by the engine demo", key)
		}
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return nil, err
	}
	return fields, nil
}
func (s *Service) engineHTTP(w http.ResponseWriter, r *http.Request) {
	requestID := newID("req")
	w.Header().Set("X-Request-ID", requestID)
	if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1/execs/") && strings.HasSuffix(r.URL.Path, "/output") {
		s.watchOutput(w, r, requestID)
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, requestID, "INVALID_ARGUMENT", "query parameters are not supported", nil)
		return
	}
	path := r.URL.Path
	if !s.authorizePublicRequest(r.Context(), r.Method, path) {
		writeError(w, requestID, "SCOPE_DENIED", "peer has no access to this resource", nil)
		return
	}
	if s.serveCustody(w, r, requestID) {
		return
	}
	if r.Method == "GET" {
		s.readLock()
		var send func()
		defer func() { s.mu.Unlock(); send() }()
		if s.broken {
			send = prepareError(w, requestID, api.ErrorDetail{Code: "DATA_UNAVAILABLE", Message: "journal unavailable"})
			return
		}
		switch {
		case strings.HasPrefix(path, "/v1/operations/"):
			if rec, ok := s.durableOps[strings.TrimPrefix(path, "/v1/operations/")]; ok && s.operationAllowed(requestCaller(r), rec) {
				send = prepareJSON(w, 200, api.OperationGetResult{Data: s.revocationOperation(rec.Operation), RequestId: requestID})
				return
			}
		case strings.HasPrefix(path, "/v1/sandboxes/"):
			if sb, ok := s.journal.Sandboxes[strings.TrimPrefix(path, "/v1/sandboxes/")]; ok && s.sandboxAllowed(requestCaller(r), sb) {
				send = prepareJSON(w, 200, api.SandboxGetResult{Data: sb, RequestId: requestID})
				return
			}
		case strings.HasPrefix(path, "/v1/execs/"):
			if ex, ok := s.journal.Execs[strings.TrimPrefix(path, "/v1/execs/")]; ok && s.execAllowed(requestCaller(r), ex) {
				send = prepareJSON(w, 200, api.ExecGetResult{Data: ex, RequestId: requestID})
				return
			}
		}
		send = prepareError(w, requestID, api.ErrorDetail{Code: "NOT_FOUND", Message: "resource not found"})
		return
	}
	if r.Method != "POST" && !(r.Method == "DELETE" && strings.HasPrefix(path, "/v1/sandboxes/")) {
		writeError(w, requestID, "NOT_FOUND", "unsupported demo route", nil)
		return
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || strings.TrimSpace(keys[0]) == "" || !utf8.ValidString(keys[0]) {
		writeError(w, requestID, "INVALID_ARGUMENT", "one nonempty UTF-8 Idempotency-Key header is required", nil)
		return
	}
	key := keys[0]
	var name, source string
	var wait *uint64
	var input any
	var sb api.Sandbox
	var ex api.Exec
	switch {
	case r.Method == "DELETE":
		var in api.SandboxDeleteInput
		revision, err := expectedRevision(r)
		if err == nil && r.Body != nil && r.Body != http.NoBody && r.ContentLength != 0 {
			_, err = decodeInput(w, r, &in, "sandbox_id wait_ms")
		}
		source = strings.TrimPrefix(path, "/v1/sandboxes/")
		if err != nil || source == "" || strings.Contains(source, "/") || (in.SandboxId != "" && in.SandboxId != source) {
			writeError(w, requestID, "INVALID_ARGUMENT", "delete requires matching sandbox_id, If-Match and Idempotency-Key", nil)
			return
		}
		in.SandboxId, in.ExpectedRevision, in.IdempotencyKey = source, revision, key
		name, wait = "sandbox_delete", in.WaitMs
		in.WaitMs = nil
		input = in
	case path == "/v1/sandboxes":
		var in api.SandboxCreateInput
		fields, err := decodeInput(w, r, &in, "template_id host_id engine_id resources devices durability task_id labels wait_ms")
		in.IdempotencyKey = key
		if err != nil {
			writeError(w, requestID, "INVALID_ARGUMENT", err.Error(), nil)
			return
		}
		if in.TemplateId == nil || *in.TemplateId == "" || in.IdempotencyKey == "" {
			writeError(w, requestID, "INVALID_ARGUMENT", "create requires template_id and Idempotency-Key; only demo body fields are admitted", nil)
			return
		}
		if in.Resources != nil && in.Resources.PidsMax == 0 {
			var limits map[string]json.RawMessage
			json.Unmarshal(fields["resources"], &limits)
			if _, supplied := limits["pids_max"]; supplied {
				writeError(w, requestID, "INVALID_ARGUMENT", "resources.pids_max must be positive", nil)
				return
			}
			in.Resources.PidsMax = 4096
		}
		if in.Resources == nil {
			in.Resources = &api.Resources{Vcpus: 2, MemoryMib: 4096, DiskGib: 20, PidsMax: 4096}
		}
		res := in.Resources
		if res.Vcpus == 0 || res.Vcpus > math.MaxUint32 || res.MemoryMib == 0 || res.DiskGib == 0 || res.PidsMax == 0 {
			writeError(w, requestID, "INVALID_ARGUMENT", "all resource limits must be positive; vcpus must fit uint32", nil)
			return
		}
		if in.EngineId != nil && *in.EngineId == "" {
			writeError(w, requestID, "INVALID_ARGUMENT", "engine_id cannot be empty", nil)
			return
		}
		if _, err := workerDevices(in.Devices); err != nil {
			writeError(w, requestID, "INVALID_ARGUMENT", err.Error(), nil)
			return
		}
		if in.Durability == nil {
			in.Durability = pointer(api.SandboxCreateInputDurabilityVolatile)
		}
		if code := admittedDurability(string(*in.Durability)); code != "" {
			writeError(w, requestID, code, "durability is not supported by the local worker", nil)
			return
		}
		name, key, wait = "sandbox_create", in.IdempotencyKey, in.WaitMs
		in.WaitMs = nil
		input = in
		sb = newSandbox()
		if in.Labels != nil {
			if (*in.Labels)["lamp.browser-custody"] != "" && !s.kitOwner(requestCaller(r)) {
				writeError(w, requestID, "SCOPE_DENIED", "browser custody label requires owner authority", nil)
				return
			}
			sb.Labels = *in.Labels
		}
		sb.TaskId = in.TaskId
		sb.Id = s.resourceID("sb")
		sb.HostId = pointer(s.hostID())
		sb.CustodyHostIds = pointer([]string{s.hostID()})
		if in.HostId != nil && *in.HostId != s.hostID() {
			writeError(w, requestID, "HOST_UNAVAILABLE", "strict host pin does not name this worker", nil)
			return
		}
		sb.TemplateId = in.TemplateId
		sb.EngineId = in.EngineId
		sb.Resources = in.Resources
		sb.Devices = in.Devices
		sb.Durability = pointer(api.SandboxDurability(*in.Durability))
	case strings.HasPrefix(path, "/v1/sandboxes/") && strings.HasSuffix(path, ":fork"):
		var in api.SandboxForkInput
		revisions := r.Header.Values("If-Match")
		if len(revisions) != 1 {
			writeError(w, requestID, "INVALID_ARGUMENT", "one uint64 If-Match header is required", nil)
			return
		}
		revision, err := strconv.ParseUint(revisions[0], 10, 64)
		if err != nil {
			writeError(w, requestID, "INVALID_ARGUMENT", "If-Match must be a uint64 revision", nil)
			return
		}
		_, err = decodeInput(w, r, &in, "sandbox_id fork_mode durability wait_ms")
		in.IdempotencyKey = key
		in.ExpectedRevision = revision
		source = strings.TrimSuffix(strings.TrimPrefix(path, "/v1/sandboxes/"), ":fork")
		if err != nil || source == "" || strings.Contains(source, "/") || in.IdempotencyKey == "" || (in.SandboxId != "" && in.SandboxId != source) || (in.ForkMode != nil && *in.ForkMode != "memory") {
			writeError(w, requestID, "INVALID_ARGUMENT", "fork requires matching sandbox_id, If-Match and Idempotency-Key headers; only memory fork is admitted", nil)
			return
		}
		if in.Durability == nil {
			in.Durability = pointer(api.SandboxForkInputDurabilityVolatile)
		}
		if code := admittedDurability(string(*in.Durability)); code != "" {
			writeError(w, requestID, code, "durability is not supported by the local worker", nil)
			return
		}
		in.SandboxId = source
		name, key, wait = "sandbox_fork", in.IdempotencyKey, in.WaitMs
		in.WaitMs = nil
		input = in
		sb = newSandbox()
		sb.Id = s.resourceID("sb")
		sb.HostId = pointer(s.hostID())
		sb.CustodyHostIds = pointer([]string{s.hostID()})
		sb.ParentSandboxId = pointer(source)
		sb.Durability = pointer(api.SandboxDurability(*in.Durability))
	case path == "/v1/execs":
		var in api.ExecCreateInput
		_, err := decodeInput(w, r, &in, "sandbox_id argv cwd env wait_ms")
		in.IdempotencyKey = key
		if err != nil || in.SandboxId == "" || len(in.Argv) == 0 || in.Argv[0] == "" || in.IdempotencyKey == "" {
			writeError(w, requestID, "INVALID_ARGUMENT", "exec requires sandbox_id, nonempty argv and Idempotency-Key", nil)
			return
		}
		for _, arg := range in.Argv {
			if strings.ContainsRune(arg, 0) {
				writeError(w, requestID, "INVALID_ARGUMENT", "argv must not contain NUL", nil)
				return
			}
		}
		if in.Cwd != nil && (!strings.HasPrefix(*in.Cwd, "/") || strings.ContainsRune(*in.Cwd, 0)) {
			writeError(w, requestID, "INVALID_ARGUMENT", "cwd must be an absolute guest path without NUL", nil)
			return
		}
		if in.Env != nil {
			for k, v := range *in.Env {
				if k == "" || strings.ContainsAny(k, "=\x00") || strings.ContainsRune(v, 0) {
					writeError(w, requestID, "INVALID_ARGUMENT", "invalid command environment", nil)
					return
				}
			}
		}
		source = in.SandboxId
		name, key, wait = "exec_create", in.IdempotencyKey, in.WaitMs
		in.WaitMs = nil
		input = in
		now := timestamp()
		ex = api.Exec{Id: s.resourceID("ex"), Kind: "exec", FleetId: "fl_local", OwnerId: "pr_local", Revision: 1, CreatedAt: now, UpdatedAt: now, State: "accepted", Labels: api.StringMap{}, Visibility: "private", SandboxId: pointer(source), Argv: &in.Argv, Cwd: in.Cwd, Env: in.Env}
	default:
		writeError(w, requestID, "NOT_FOUND", "unsupported demo route", nil)
		return
	}
	if len(key) > 256 || (wait != nil && *wait > 60000) {
		writeError(w, requestID, "INVALID_ARGUMENT", "idempotency_key limit is 256 bytes; wait_ms limit is 60000", nil)
		return
	}
	encoded, _ := json.Marshal(input)
	sum := sha256.Sum256(append([]byte(name+"\n"), encoded...))
	hash := hex.EncodeToString(sum[:])
	a := requestCaller(r)
	journalKey := a.Principal + "\n" + name + "\n" + key
	s.mu.Lock()
	if s.broken {
		s.mu.Unlock()
		writeError(w, requestID, "DATA_UNAVAILABLE", "journal unavailable", nil)
		return
	}
	if !s.can(a, strings.Replace(name, "_", ".", 1)) {
		s.mu.Unlock()
		writeError(w, requestID, "SCOPE_DENIED", "authority changed", nil)
		return
	}
	if id, ok := s.keys[journalKey]; ok {
		rec := s.journal.Operations[id]
		if !s.operationAllowed(a, rec) {
			s.mu.Unlock()
			writeError(w, requestID, "NOT_FOUND", "operation not found", nil)
			return
		}
		if rec.Hash != hash {
			s.mu.Unlock()
			writeError(w, requestID, "IDEMPOTENCY_CONFLICT", "key was already used with different inputs", pointer(id))
			return
		}
		s.mu.Unlock()
		s.respond(w, r, requestID, id, true, wait)
		return
	}
	if len(s.journal.Operations) >= maxOperations && name != "sandbox_delete" {
		s.mu.Unlock()
		writeError(w, requestID, "CONTROL_QUOTA_EXCEEDED", "demo journal operation limit reached", nil)
		return
	}
	queuedDelete := false
	var updatedParent *api.Sandbox
	if source != "" {
		parent, ok := s.journal.Sandboxes[source]
		if !ok || !s.sandboxAllowed(a, parent) {
			s.mu.Unlock()
			writeError(w, requestID, "NOT_FOUND", "sandbox not found", nil)
			return
		}
		if name == "sandbox_fork" && parent.Revision != input.(api.SandboxForkInput).ExpectedRevision {
			s.mu.Unlock()
			writeError(w, requestID, "REVISION_CONFLICT", "sandbox revision changed", nil)
			return
		}
		if name == "sandbox_delete" && parent.Revision != input.(api.SandboxDeleteInput).ExpectedRevision {
			s.mu.Unlock()
			writeError(w, requestID, "REVISION_CONFLICT", "sandbox revision changed", nil)
			return
		}
		if name != "sandbox_delete" && (parent.State != "ready" || s.busy[source] || (name != "exec_create" && s.starting[source] > 0)) {
			s.mu.Unlock()
			writeError(w, requestID, "RESOURCE_IN_USE", "sandbox is not ready or has an in-flight operation", nil)
			return
		}
		if name == "sandbox_delete" {
			queuedDelete = s.busy[source] || s.starting[source] > 0
			parent.State = "deleting"
			parent.Revision++
			parent.UpdatedAt = timestamp()
			sb = parent
		}
		if name == "exec_create" && value(parent.CaptureTrust) == "owner_private" && !admittedSubscriptionExec(ex) {
			s.mu.Unlock()
			writeError(w, requestID, "SCOPE_DENIED", "subscription guest accepts only installed bounded operations; no command data or environment is persisted", nil)
			return
		}
		if name == "exec_create" {
			// Admission needs the actual host and resource limits of the
			// authorized source, rather than an empty hs_local sandbox.
			sb = parent
		}
		if name == "sandbox_fork" {
			sb.TaskId = parent.TaskId
			sb.TemplateId = parent.TemplateId
			sb.EngineId = parent.EngineId
			sb.Resources = parent.Resources
			parent.Revision++
			parent.UpdatedAt = timestamp()
			updatedParent = &parent
		}
	}
	if err := s.accountAdmission(a, name, sb); err != nil {
		s.mu.Unlock()
		e := err.(*coreError)
		writeError(w, requestID, e.code, e.message, nil)
		return
	}
	if s.fleet != nil {
		if err := s.fleet.admit(a, name, sb); err != nil {
			s.mu.Unlock()
			e := err.(*coreError)
			writeError(w, requestID, e.code, e.message, nil)
			return
		}
	}
	if !s.admissionAllowed(a, name, sb) {
		s.mu.Unlock()
		writeError(w, requestID, "SCOPE_DENIED", "admission exceeds caller authority", nil)
		return
	}
	if name == "sandbox_create" || name == "sandbox_fork" {
		if err := s.preflight(r.Context(), sb, name == "sandbox_fork"); err != nil {
			s.mu.Unlock()
			failure := &workerFailure{code: "HOST_UNAVAILABLE", message: err.Error()}
			errors.As(err, &failure)
			writeError(w, requestID, failure.code, failure.message, nil)
			return
		}
	}
	if updatedParent != nil {
		s.journal.Sandboxes[source] = *updatedParent
	}
	sb.OwnerId = a.Principal
	if name == "sandbox_create" || name == "sandbox_fork" {
		s.bindDefaultTask(a, &sb)
		if name == "sandbox_fork" {
			s.chargeFork(sb)
		}
	}
	visibility := "control"
	if name == "exec_create" {
		visibility = "opaque"
	}
	if name != "sandbox_delete" && !s.appendAudit(sb, visibility, name, "allow") {
		s.mu.Unlock()
		writeError(w, requestID, "CONTROL_QUOTA_EXCEEDED", "audit retention quota reached", nil)
		return
	}
	ex.OwnerId = a.Principal
	ref := api.ResourceRef{Kind: "sandbox", Id: sb.Id}
	if name == "exec_create" {
		ref = api.ResourceRef{Kind: "exec", Id: ex.Id}
		s.journal.Execs[ex.Id] = ex
	} else {
		s.journal.Sandboxes[sb.Id] = sb
	}
	now := timestamp()
	id := s.resourceID("op")
	refs := []api.ResourceRef{ref}
	op := api.Operation{Id: id, Kind: "operation", FleetId: "fl_local", OwnerId: a.Principal, Revision: 1, CreatedAt: now, UpdatedAt: now, State: "accepted", Labels: api.StringMap{}, Visibility: "private", OperationName: pointer(name), ResourceIds: &refs, RequestHash: pointer(hash), AcceptedAt: &now, CleanupState: pointer(api.OperationCleanupStateNotNeeded), Durability: pointer(api.OperationDurabilityVolatile)}
	s.journal.Operations[id] = &record{Operation: op, Hash: hash, Key: journalKey, Resource: ref, Caller: a}
	s.keys[journalKey] = id
	s.done[id] = make(chan struct{})
	if name == "sandbox_create" || name == "sandbox_fork" {
		s.busy[sb.Id] = true
	}
	if source != "" {
		if name == "exec_create" {
			s.starting[source]++
		} else if !queuedDelete {
			s.busy[source] = true
		}
	}
	if err := s.saveConcurrent(id, ref.Id, source, "$accountability"); err != nil {
		s.broken = true
		s.mu.Unlock()
		writeError(w, requestID, "DATA_UNAVAILABLE", "cannot persist operation admission", nil)
		return
	}
	if !queuedDelete {
		s.wg.Add(1)
		go s.execute(id, source)
	}
	s.mu.Unlock()
	s.respond(w, r, requestID, id, false, wait)
}
func newSandbox() api.Sandbox {
	now := timestamp()
	return api.Sandbox{Id: newID("sb"), Kind: "sandbox", FleetId: "fl_local", OwnerId: "pr_local", Revision: 1, CreatedAt: now, UpdatedAt: now, State: "accepted", Labels: api.StringMap{}, Visibility: "private", Generation: pointer(uint64(1)), EffectivePolicyRevision: pointer(uint64(1)), Durability: pointer(api.SandboxDurabilityVolatile), Boundary: pointer(api.SandboxBoundaryVm), Readiness: coreReadiness(false, nil)}
}
func (s *Service) respond(w http.ResponseWriter, r *http.Request, requestID, id string, replayed bool, wait *uint64) {
	s.mu.Lock()
	done := s.done[id]
	s.mu.Unlock()
	if wait != nil && *wait > 0 {
		timer := time.NewTimer(time.Duration(*wait) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
		case <-r.Context().Done():
			return
		}
	}
	s.mu.Lock()
	for !s.broken && s.durableOps[id] == nil && s.committing {
		changed := s.changed
		s.mu.Unlock()
		select {
		case <-changed:
		case <-r.Context().Done():
			return
		}
		s.mu.Lock()
	}
	var send func()
	defer func() { s.mu.Unlock(); send() }()
	if s.broken || s.durableOps[id] == nil {
		send = prepareError(w, requestID, api.ErrorDetail{Code: "DATA_UNAVAILABLE", Message: "journal unavailable", OperationId: pointer(id)})
		return
	}
	rec := s.durableOps[id]
	if a := requestCaller(r); a.Principal != "" && (!s.can(a, strings.Replace(value(rec.Operation.OperationName), "_", ".", 1)) || !s.operationAllowed(a, rec)) {
		send = prepareError(w, requestID, api.ErrorDetail{Code: "SCOPE_DENIED", Message: "authority changed while waiting", OperationId: &id})
		return
	}
	op := rec.Operation
	result := api.MutationEnvelope{OperationId: id, ResourceIds: []api.ResourceRef{rec.Resource}, RequestId: requestID, Replayed: replayed, Operation: &op}
	status := 202
	if op.State == "succeeded" {
		status = 200
		result.Result = op.Outcome.Result
	}
	send = prepareJSON(w, status, result)
}
func (s *Service) execute(id, source string) {
	defer s.wg.Done()
	ctx, cancel := context.WithTimeout(s.context(), s.worker.config.Timeout)
	defer cancel()
	s.mu.Lock()
	rec := s.journal.Operations[id]
	if s.broken {
		close(s.done[id])
		s.mu.Unlock()
		return
	}
	if rec.Operation.State == "canceled" {
		s.releaseAdmission(rec, source)
		close(s.done[id])
		s.mu.Unlock()
		return
	}
	if rec.Caller.Principal != "" && !s.can(rec.Caller, strings.Replace(*rec.Operation.OperationName, "_", ".", 1)) {
		rec.Operation.State = "failed"
		rec.Operation.Error = &api.ErrorDetail{Code: "SCOPE_DENIED", Message: "authority changed before dispatch", OperationId: &id}
		rec.Operation.Revision++
		rec.Operation.CompletedAt = pointer(timestamp())
		rec.Operation.UpdatedAt = timestamp()
		rec.Operation.CleanupState = pointer(api.OperationCleanupStateComplete)
		ref := rec.Resource
		if ref.Kind == "sandbox" {
			sb := s.journal.Sandboxes[ref.Id]
			sb.State = "failed"
			sb.Revision++
			s.journal.Sandboxes[ref.Id] = sb
		} else if ref.Kind == "exec" {
			ex := s.journal.Execs[ref.Id]
			ex.State = "failed"
			ex.Revision++
			ex.UpdatedAt = timestamp()
			s.journal.Execs[ref.Id] = ex
		}
		if err := s.saveConcurrent(id, ref.Id); err != nil {
			s.broken = true
		}
		s.releaseAdmission(rec, source)
		close(s.done[id])
		s.mu.Unlock()
		return
	}
	if source != "" {
		sb := s.journal.Sandboxes[source]
		if sb.TaskId != nil {
			t := s.journal.Accountability.Tasks[*sb.TaskId]
			if t.DeadlineAt != nil {
				if deadline, e := time.Parse(time.RFC3339Nano, *t.DeadlineAt); e == nil {
					var stop context.CancelFunc
					ctx, stop = context.WithDeadline(ctx, deadline)
					defer stop()
				}
			}
		}
	}
	rec.Operation.Phase = pointer("worker")
	rec.Operation.State = "running"
	rec.Operation.Error = nil
	rec.Operation.CompletedAt = nil
	rec.Operation.Revision++
	rec.Operation.UpdatedAt = timestamp()
	ref := rec.Resource
	name := *rec.Operation.OperationName
	sb := s.journal.Sandboxes[ref.Id]
	ex := s.journal.Execs[ref.Id]
	if ref.Kind == "sandbox" {
		sb.State = "preparing"
		if name == "sandbox_delete" {
			sb.State = "deleting"
		}
		sb.Revision++
		sb.UpdatedAt = timestamp()
		s.journal.Sandboxes[sb.Id] = sb
	} else {
		sb = s.journal.Sandboxes[source]
	}
	// Accepted is the durable no-replay boundary. For exec, running is an
	// internal dispatch fence; the next durable public transition is the worker
	// command-start result. Readers use durableOps and never see this unsaved
	// revision. A crash still recovers accepted work as outcome_unknown.
	running := make(chan error, 1)
	if name == "exec_create" {
		s.mu.Unlock()
		running <- nil
	} else {
		go func() {
			err := s.saveConcurrent(id, ref.Id, source, "$accountability")
			if err != nil {
				s.broken = true
			}
			s.mu.Unlock()
			running <- err
		}()
	}
	generation := *sb.Generation
	request := func(step, sandbox string, generation uint64) *pb.Request {
		deadline, _ := ctx.Deadline()
		return &pb.Request{Context: &pb.Context{RequestId: newID("req"), OperationId: id + "." + step, DeadlineMs: uint64(deadline.UnixMilli()), SandboxId: sandbox, Generation: generation, GrantId: s.worker.config.GrantID, PolicyRevision: 1}}
	}
	allocate := func() error {
		r := request("allocate", sb.Id, generation)
		resources := sb.Resources
		devices, _ := workerDevices(sb.Devices)
		r.Command = &pb.Request_Allocate{Allocate: &pb.Allocate{Resources: &pb.Resources{Vcpus: uint32(resources.Vcpus), MemoryMib: resources.MemoryMib, DiskGib: resources.DiskGib, PidsMax: resources.PidsMax}, EngineId: value(sb.EngineId), Devices: devices, SharedDisk: name == "sandbox_fork"}}
		return s.worker.perform(ctx, r, "allocated")
	}
	restore := func(capture string) error {
		r := request("restore", sb.Id, generation)
		r.Command = &pb.Request_Restore{Restore: &pb.Restore{CaptureId: capture}}
		receipt, err := s.worker.call(ctx, r)
		if err != nil {
			return err
		}
		if receipt.State != "restored" {
			return errors.New("restore receipt missing")
		}
		if trust := receipt.Detail["capture_trust"]; trust != "" {
			if trust != "clean" && trust != "owner_private" {
				return errors.New("invalid worker capture trust")
			}
			sb.CaptureTrust = pointer(api.SandboxCaptureTrust(trust))
			var hosts []string
			if json.Unmarshal([]byte(receipt.Detail["custody_host_ids"]), &hosts) != nil {
				return errors.New("worker custody hosts missing")
			}
			sb.CustodyHostIds = &hosts
		}
		return nil
	}
	start := func(argv []string, cwd string, env map[string]string) error {
		r := request("start", sb.Id, generation)
		r.Command = &pb.Request_Start{Start: &pb.Start{Argv: argv, Cwd: cwd, Env: env}}
		return s.worker.perform(ctx, r, "command_started")
	}
	var err error
	phase := "engine"
	stage := func(next string) { phase = next }
	// sourceIntact: a fork failed only after the source's capture
	// completed, so the source itself was left running and unchanged.
	sourceIntact := false
	switch name {
	case "sandbox_create":
		err = allocate()
		if err == nil {
			stage("template")
			err = restore(value(sb.TemplateId))
		}
		if err == nil {
			stage("command")
			err = start([]string{"/bin/true"}, "/", nil)
		}
	case "sandbox_fork":
		capture := newID("capture")
		r := request("capture", source, generation)
		consistency := "crash"
		if value(sb.Durability) == "local" {
			consistency = "local"
		}
		r.Command = &pb.Request_Capture{Capture: &pb.Capture{CaptureId: capture, PreservesMemory: true, Consistency: consistency}}
		// The child is allocated first: the engine builds its guard network
		// while the source is being captured.
		err = allocate()
		if err == nil {
			err = s.worker.perform(ctx, r, "captured")
			// Only a completed capture proves the source resumed unchanged.
			sourceIntact = err == nil
		}
		if err == nil {
			err = restore(capture)
		}
		if err == nil {
			stage("command")
			err = start([]string{"/bin/true"}, "/", nil)
		}
	case "sandbox_delete":
		stage("cleanup")
		err = s.releaseSandbox(ctx, sb)
	case "exec_create":
		var env map[string]string
		if ex.Env != nil {
			env = map[string]string(*ex.Env)
		}
		stage("command")
		err = start(*ex.Argv, value(ex.Cwd), env)
	}
	if err == nil && (name == "sandbox_create" || name == "sandbox_fork") {
		// In an audited guest, command_started can witness the guard wrapper.
		// Ready additionally requires the real /bin/true probe to finish with
		// exit zero. Inspect is read-only; an uncertain probe is never rerun.
		for {
			r := request("readiness", sb.Id, generation)
			r.Command = &pb.Request_Inspect{Inspect: &pb.Inspect{CommandOperationId: id + ".start"}}
			receipt, inspectErr := s.worker.call(ctx, r)
			if inspectErr != nil {
				err = inspectErr
				break
			}
			if receipt.Command != nil && receipt.Command.FinishedAtUnixNs != 0 {
				if receipt.Command.ExitCode != 0 || receipt.Command.Signal != 0 {
					err = &workerFailure{code: "AUDIT_UNAVAILABLE", message: "readiness command did not finish successfully"}
				}
				break
			}
			if receipt.Detail["command_state"] != "running" {
				err = &workerFailure{code: "OUTCOME_UNKNOWN", message: "readiness command completion unavailable", unknown: true}
				break
			}
			timer := time.NewTimer(time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				err = &workerFailure{code: "OUTCOME_UNKNOWN", message: "readiness deadline", unknown: true}
			case <-timer.C:
			}
			if err != nil {
				break
			}
		}
	}
	cleaned := false
	if err != nil && (name == "sandbox_create" || name == "sandbox_fork") {
		cleanupCtx, cancelCleanup := context.WithTimeout(s.context(), s.worker.config.Timeout)
		cleaned = s.releaseSandbox(cleanupCtx, sb) == nil
		cancelCleanup()
	}
	if <-running != nil {
		s.mu.Lock()
		s.releaseAdmission(rec, source)
		close(s.done[id])
		s.mu.Unlock()
		return
	}
	now := timestamp()
	s.mu.Lock()
	defer s.mu.Unlock()
	rec = s.journal.Operations[id]
	op := &rec.Operation
	op.Phase = &phase
	op.CompletedAt = &now
	op.UpdatedAt = now
	op.Revision++
	if err != nil {
		failure := &workerFailure{code: "DATA_UNAVAILABLE", message: err.Error()}
		errors.As(err, &failure)
		code := failure.code
		if _, ok := errorStatuses[code]; !ok {
			code = "DATA_UNAVAILABLE"
		}
		op.State = "failed"
		if failure.unknown {
			op.State = "outcome_unknown"
			code = "OUTCOME_UNKNOWN"
		}
		op.Error = &api.ErrorDetail{Code: api.ErrorCode(code), Message: failure.message, Retryable: failure.retryable, OperationId: pointer(id)}
		op.CleanupState = pointer(api.OperationCleanupStatePending)
		if cleaned {
			op.CleanupState = pointer(api.OperationCleanupStateComplete)
		}
		if source != "" && (name == "sandbox_fork" || failure.unknown) && !sourceIntact {
			parent := s.journal.Sandboxes[source]
			parent.State = "unavailable"
			parent.Revision++
			parent.UpdatedAt = now
			s.journal.Sandboxes[source] = parent
		}
		if ref.Kind == "sandbox" {
			sb.Readiness = coreReadiness(false, nil)
			for i, check := range *sb.Readiness.Checks {
				if string(check.Name) == phase {
					(*sb.Readiness.Checks)[i].State = "failed"
					(*sb.Readiness.Checks)[i].Error = op.Error
				}
			}
			sb.State = "failed"
			if name == "sandbox_delete" {
				sb.State = "deleting"
				op.State = "running"
				op.CompletedAt = nil
			}
		} else {
			ex.State = "failed"
			if failure.unknown {
				ex.State = "outcome_unknown"
			}
		}
	} else {
		op.State = "succeeded"
		op.Error = nil
		if name == "sandbox_delete" {
			op.CleanupState = pointer(api.OperationCleanupStateComplete)
		}
		var result api.AdapterDocument
		if ref.Kind == "sandbox" {
			sb.State = "ready"
			sb.Readiness = coreReadiness(true, &now)
			result, _ = json.Marshal(api.SandboxCreateCompletion{SandboxId: sb.Id, SessionIds: []string{}})
			if name == "sandbox_delete" {
				sb.State = "deleted"
				sb.Readiness = nil
				result = api.AdapterDocument(`{}`)
			}
		} else {
			ex.State = "running"
			ex.StartedAt = &now
			result, _ = json.Marshal(api.ExecCreateCompletion{ExecId: ex.Id})
		}
		op.Outcome = &api.Outcome{Result: &result}
	}
	if ref.Kind == "sandbox" {
		sb.Revision++
		sb.UpdatedAt = now
		s.journal.Sandboxes[sb.Id] = sb
	} else {
		ex.Revision++
		ex.UpdatedAt = now
		s.journal.Execs[ex.Id] = ex
	}
	if err := s.saveConcurrent(id, ref.Id, source, "$accountability"); err != nil {
		s.broken = true
	}
	s.releaseAdmission(rec, source)
	if ref.Kind == "sandbox" && (sb.State == "deleted" || cleaned) && !s.broken {
		s.mu.Unlock()
		if releaseErr := s.releaseGuardAudit(s.context(), sb.Id); releaseErr != nil {
			log.Print(releaseErr)
		}
		s.mu.Lock()
	}
	if terminal(op.State) {
		close(s.done[id])
	}
	if name == "exec_create" && err == nil && !s.broken {
		s.wg.Add(1)
		go s.observeExec(ex.Id, request("observe", sb.Id, generation).Context, id+".start")
	}
}
func value[T any](v *T) (zero T) {
	if v != nil {
		return *v
	}
	return zero
}

func admittedDurability(d string) string {
	switch d {
	case "volatile", "local":
		return ""
	case "replicated", "backup":
		return "QUORUM_REQUIRED"
	default:
		return "INVALID_ARGUMENT"
	}
}
func expectedRevision(r *http.Request) (uint64, error) {
	revisions := r.Header.Values("If-Match")
	if len(revisions) != 1 {
		return 0, errors.New("one If-Match required")
	}
	return strconv.ParseUint(revisions[0], 10, 64)
}
func workerNotFound(err error) bool {
	var failure *workerFailure
	return errors.As(err, &failure) && failure.code == "NOT_FOUND" && !failure.unknown
}

// Completion observation is separate from Start admission and never holds mu
// while waiting on the supervisor. A lost command cannot fabricate exit zero.
func (s *Service) observeExec(id string, c *pb.Context, commandID string) {
	defer s.wg.Done()
	timeout := s.worker.config.Timeout
	s.mu.Lock()
	exec := s.journal.Execs[id]
	if parent, ok := s.journal.Sandboxes[value(exec.SandboxId)]; ok && value(parent.CaptureTrust) == "owner_private" && admittedSubscriptionExec(exec) {
		timeout = custodyOperationTimeout
	}
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(s.context(), timeout)
	defer cancel()
	deadline, _ := ctx.Deadline()
	c.DeadlineMs = uint64(deadline.UnixMilli())
	var command *pb.CommandResult
	polls := 0
	for {
		c.RequestId, c.OperationId = newID("req"), newID("op")
		receipt, err := s.worker.call(ctx, &pb.Request{Context: c, Command: &pb.Request_Inspect{Inspect: &pb.Inspect{CommandOperationId: commandID}}})
		if err != nil || receipt.Detail["command_state"] == "unknown" {
			break
		}
		if receipt.Command != nil && receipt.Command.FinishedAtUnixNs != 0 {
			command = receipt.Command
			break
		}
		if receipt.Detail["command_state"] != "running" {
			break
		}
		// 1 ms while the command is young, then 4 ms: short commands are
		// seen promptly without a 1 kHz RPC stream for long-running ones.
		polls++
		delay := time.Millisecond
		if polls > 50 {
			delay = 4 * time.Millisecond
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
		if ctx.Err() != nil {
			break
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ex := s.journal.Execs[id]
	ex.State = "outcome_unknown"
	if command != nil {
		ex.State = "exited"
		ex.ExitCode, ex.Signal = pointer(int64(command.ExitCode)), pointer(int64(command.Signal))
		if command.StartedAtUnixNs != 0 {
			ex.StartedAt = pointer(time.Unix(0, int64(command.StartedAtUnixNs)).UTC().Format(time.RFC3339Nano))
		}
		frames := []api.Frame{}
		for i, tail := range [][]byte{command.StdoutTail, command.StderrTail} {
			if len(tail) == 0 {
				continue
			}
			channel := api.FrameChannelStdout
			if i == 1 {
				channel = api.FrameChannelStderr
			}
			frames = append(frames, api.Frame{StreamId: id + "." + string(channel), Sequence: 0, Channel: channel, DataBase64: base64.StdEncoding.EncodeToString(tail), ByteOffset: "0", LocationEpoch: 1})
		}
		s.journal.Outputs[id] = frames
	}
	ex.Revision++
	ex.UpdatedAt = timestamp()
	s.journal.Execs[id] = ex
	if err := s.saveConcurrent(id); err != nil {
		s.broken = true
	}
}

func (s *Service) watchOutput(w http.ResponseWriter, r *http.Request, requestID string) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/execs/"), "/output")
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, requestID, "INVALID_ARGUMENT", "invalid query encoding", nil)
		return
	}
	var offset uint64
	limit, wait := uint64(100), uint64(0)
	for key, values := range q {
		if len(values) != 1 {
			writeError(w, requestID, "INVALID_ARGUMENT", "one value per output query parameter required", nil)
			return
		}
		var err error
		switch key {
		case "cursor":
			prefix := id + ":"
			if !strings.HasPrefix(values[0], prefix) {
				err = errors.New("cursor belongs to another stream")
			} else {
				offset, err = strconv.ParseUint(strings.TrimPrefix(values[0], prefix), 10, 64)
			}
		case "limit":
			limit, err = strconv.ParseUint(values[0], 10, 64)
		case "wait_ms":
			wait, err = strconv.ParseUint(values[0], 10, 64)
		default:
			err = errors.New("unsupported query parameter")
		}
		if err != nil {
			writeError(w, requestID, "INVALID_ARGUMENT", "invalid output query parameter", nil)
			return
		}
	}
	if wait > 60000 || limit > 1000 {
		writeError(w, requestID, "INVALID_ARGUMENT", "output wait limit is 60000 ms; frame limit is 1000", nil)
		return
	}
	until := time.Now().Add(time.Duration(wait) * time.Millisecond)
	for {
		s.readLock()
		if s.broken {
			s.mu.Unlock()
			writeError(w, requestID, "DATA_UNAVAILABLE", "journal unavailable", nil)
			return
		}
		ex, ok := s.journal.Execs[id]
		if !ok || !s.can(requestCaller(r), "exec.watch") || !s.execAllowed(requestCaller(r), ex) {
			s.mu.Unlock()
			writeError(w, requestID, "NOT_FOUND", "exec not found", nil)
			return
		}
		frames := s.journal.Outputs[id]
		if offset > uint64(len(frames)) {
			s.mu.Unlock()
			writeError(w, requestID, "INVALID_ARGUMENT", "cursor exceeds available output", nil)
			return
		}
		more := ex.State == "accepted" || ex.State == "running"
		if more && offset == uint64(len(frames)) && time.Now().Before(until) {
			// Sleep until the next durable journal change or the deadline.
			changed := s.changed
			s.mu.Unlock()
			delay := time.Until(until)
			if delay > time.Second {
				delay = time.Second
			}
			timer := time.NewTimer(delay)
			select {
			case <-r.Context().Done():
				timer.Stop()
				return
			case <-changed:
				timer.Stop()
			case <-timer.C:
			}
			continue
		}
		end := offset + limit
		if end > uint64(len(frames)) {
			end = uint64(len(frames))
		}
		result := api.ExecWatchResult{Items: append([]api.Frame{}, frames[offset:end]...), RequestId: requestID}
		if more || end < uint64(len(frames)) {
			result.NextCursor = pointer(id + ":" + strconv.FormatUint(end, 10))
		}
		send := prepareJSON(w, 200, result)
		s.mu.Unlock()
		send()
		return
	}
}

// workerDevices maps requested devices to the worker protocol. Only the
// shape is checked here; the engine refuses devices it cannot attach.
func workerDevices(in *[]api.Device) ([]*pb.DeviceRequest, error) {
	if in == nil {
		return nil, nil
	}
	var out []*pb.DeviceRequest
	for _, d := range *in {
		if d.Id == "" || d.Type == "" {
			return nil, errors.New("each device needs id and type")
		}
		var opts struct {
			SizeMiB uint64 `json:"size_mib"`
		}
		if d.Options != nil {
			if err := json.Unmarshal(*d.Options, &opts); err != nil {
				return nil, errors.New("device options: " + err.Error())
			}
		}
		if d.Type == api.DeviceTypeDisk && opts.SizeMiB == 0 {
			return nil, errors.New("disk device " + d.Id + " needs options.size_mib")
		}
		out = append(out, &pb.DeviceRequest{Id: d.Id, Type: string(d.Type), SizeMib: opts.SizeMiB})
	}
	return out, nil
}
