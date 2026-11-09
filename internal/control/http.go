package control

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/JohanSardar/lamp/pkg/api"
	pb "github.com/JohanSardar/lamp/pkg/workerpb"
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
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestID := newID("req")
	w.Header().Set("X-Request-ID", requestID)
	if r.URL.RawQuery != "" {
		writeError(w, requestID, "INVALID_ARGUMENT", "query parameters are not supported", nil)
		return
	}
	path := r.URL.Path
	if r.Method == "GET" {
		s.mu.Lock()
		var send func()
		defer func() { s.mu.Unlock(); send() }()
		if s.broken {
			send = prepareError(w, requestID, api.ErrorDetail{Code: "DATA_UNAVAILABLE", Message: "journal unavailable"})
			return
		}
		switch {
		case strings.HasPrefix(path, "/v1/operations/"):
			if rec, ok := s.journal.Operations[strings.TrimPrefix(path, "/v1/operations/")]; ok {
				send = prepareJSON(w, 200, api.OperationGetResult{Data: rec.Operation, RequestId: requestID})
				return
			}
		case strings.HasPrefix(path, "/v1/sandboxes/"):
			if sb, ok := s.journal.Sandboxes[strings.TrimPrefix(path, "/v1/sandboxes/")]; ok {
				send = prepareJSON(w, 200, api.SandboxGetResult{Data: sb, RequestId: requestID})
				return
			}
		case strings.HasPrefix(path, "/v1/execs/"):
			if ex, ok := s.journal.Execs[strings.TrimPrefix(path, "/v1/execs/")]; ok {
				send = prepareJSON(w, 200, api.ExecGetResult{Data: ex, RequestId: requestID})
				return
			}
		}
		send = prepareError(w, requestID, api.ErrorDetail{Code: "NOT_FOUND", Message: "resource not found"})
		return
	}
	if r.Method != "POST" {
		writeError(w, requestID, "NOT_FOUND", "unsupported demo route", nil)
		return
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || strings.TrimSpace(keys[0]) == "" {
		writeError(w, requestID, "INVALID_ARGUMENT", "one nonempty Idempotency-Key header is required", nil)
		return
	}
	key := keys[0]
	var name, source string
	var wait *uint64
	var input any
	var sb api.Sandbox
	var ex api.Exec
	switch {
	case path == "/v1/sandboxes":
		var in api.SandboxCreateInput
		_, err := decodeInput(w, r, &in, "template_id engine_id resources wait_ms")
		in.IdempotencyKey = key
		if err != nil || in.TemplateId == nil || *in.TemplateId == "" || in.IdempotencyKey == "" {
			writeError(w, requestID, "INVALID_ARGUMENT", "create requires template_id and Idempotency-Key; only demo body fields are admitted", nil)
			return
		}
		if in.Resources == nil {
			in.Resources = &api.Resources{Vcpus: 2, MemoryMib: 4096, DiskGib: 20, PidsMax: 1024}
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
		name, key, wait = "sandbox_create", in.IdempotencyKey, in.WaitMs
		in.WaitMs = nil
		input = in
		sb = newSandbox()
		sb.TemplateId = in.TemplateId
		sb.EngineId = in.EngineId
		sb.Resources = in.Resources
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
		if err != nil || source == "" || strings.Contains(source, "/") || in.IdempotencyKey == "" || (in.SandboxId != "" && in.SandboxId != source) || (in.ForkMode != nil && *in.ForkMode != "memory") || (in.Durability != nil && *in.Durability != "volatile") {
			writeError(w, requestID, "INVALID_ARGUMENT", "fork requires matching sandbox_id, If-Match and Idempotency-Key headers; only volatile memory fork is admitted", nil)
			return
		}
		in.SandboxId = source
		name, key, wait = "sandbox_fork", in.IdempotencyKey, in.WaitMs
		in.WaitMs = nil
		input = in
		sb = newSandbox()
		sb.ParentSandboxId = pointer(source)
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
		ex = api.Exec{Id: newID("ex"), Kind: "exec", FleetId: "fl_local", OwnerId: "pr_local", Revision: 1, CreatedAt: now, UpdatedAt: now, State: "accepted", Labels: api.StringMap{}, Visibility: "private", SandboxId: pointer(source), Argv: &in.Argv, Cwd: in.Cwd, Env: in.Env}
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
	journalKey := name + "\n" + key
	s.mu.Lock()
	if s.broken {
		s.mu.Unlock()
		writeError(w, requestID, "DATA_UNAVAILABLE", "journal unavailable", nil)
		return
	}
	for id, rec := range s.journal.Operations {
		if rec.Key == journalKey {
			if rec.Hash != hash {
				s.mu.Unlock()
				writeError(w, requestID, "IDEMPOTENCY_CONFLICT", "key was already used with different inputs", pointer(id))
				return
			}
			s.mu.Unlock()
			s.respond(w, r, requestID, id, true, wait)
			return
		}
	}
	if len(s.journal.Operations) >= maxOperations {
		s.mu.Unlock()
		writeError(w, requestID, "CONTROL_QUOTA_EXCEEDED", "demo journal operation limit reached", nil)
		return
	}
	if source != "" {
		parent, ok := s.journal.Sandboxes[source]
		if !ok {
			s.mu.Unlock()
			writeError(w, requestID, "NOT_FOUND", "sandbox not found", nil)
			return
		}
		if name == "sandbox_fork" && parent.Revision != input.(api.SandboxForkInput).ExpectedRevision {
			s.mu.Unlock()
			writeError(w, requestID, "REVISION_CONFLICT", "sandbox revision changed", nil)
			return
		}
		if parent.State != "ready" || s.busy[source] {
			s.mu.Unlock()
			writeError(w, requestID, "RESOURCE_IN_USE", "sandbox is not ready or has an in-flight operation", nil)
			return
		}
		if name == "sandbox_fork" {
			sb.TemplateId = parent.TemplateId
			sb.EngineId = parent.EngineId
			sb.Resources = parent.Resources
			parent.Revision++
			parent.UpdatedAt = timestamp()
			s.journal.Sandboxes[source] = parent
		}
	}
	ref := api.ResourceRef{Kind: "sandbox", Id: sb.Id}
	if name == "exec_create" {
		ref = api.ResourceRef{Kind: "exec", Id: ex.Id}
		s.journal.Execs[ex.Id] = ex
	} else {
		s.journal.Sandboxes[sb.Id] = sb
	}
	now := timestamp()
	id := newID("op")
	refs := []api.ResourceRef{ref}
	op := api.Operation{Id: id, Kind: "operation", FleetId: "fl_local", OwnerId: "pr_local", Revision: 1, CreatedAt: now, UpdatedAt: now, State: "accepted", Labels: api.StringMap{}, Visibility: "private", OperationName: pointer(name), ResourceIds: &refs, RequestHash: pointer(hash), AcceptedAt: &now, CleanupState: pointer(api.OperationCleanupStateNotNeeded), Durability: pointer(api.OperationDurabilityVolatile)}
	s.journal.Operations[id] = &record{Operation: op, Hash: hash, Key: journalKey, Resource: ref}
	s.done[id] = make(chan struct{})
	if source != "" {
		s.busy[source] = true
	}
	if err := s.save(); err != nil {
		s.broken = true
		s.mu.Unlock()
		writeError(w, requestID, "DATA_UNAVAILABLE", "cannot persist operation admission", nil)
		return
	}
	s.wg.Add(1)
	go s.execute(id, source)
	s.mu.Unlock()
	s.respond(w, r, requestID, id, false, wait)
}
func newSandbox() api.Sandbox {
	now := timestamp()
	return api.Sandbox{Id: newID("sb"), Kind: "sandbox", FleetId: "fl_local", OwnerId: "pr_local", Revision: 1, CreatedAt: now, UpdatedAt: now, State: "accepted", Labels: api.StringMap{}, Visibility: "private", Generation: pointer(uint64(1)), EffectivePolicyRevision: pointer(uint64(1)), Durability: pointer(api.SandboxDurabilityVolatile)}
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
	var send func()
	defer func() { s.mu.Unlock(); send() }()
	if s.broken {
		send = prepareError(w, requestID, api.ErrorDetail{Code: "DATA_UNAVAILABLE", Message: "journal unavailable", OperationId: pointer(id)})
		return
	}
	rec := s.journal.Operations[id]
	op := rec.Operation
	if op.Error != nil {
		send = prepareError(w, requestID, *op.Error)
		return
	}
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
	rec.Operation.State = "running"
	rec.Operation.Revision++
	rec.Operation.UpdatedAt = timestamp()
	ref := rec.Resource
	name := *rec.Operation.OperationName
	sb := s.journal.Sandboxes[ref.Id]
	ex := s.journal.Execs[ref.Id]
	if ref.Kind == "sandbox" {
		sb.State = "preparing"
		sb.Revision++
		sb.UpdatedAt = timestamp()
		s.journal.Sandboxes[sb.Id] = sb
	} else {
		sb = s.journal.Sandboxes[source]
	}
	s.mu.Unlock()
	generation := *sb.Generation
	request := func(step, sandbox string, generation uint64) *pb.Request {
		deadline, _ := ctx.Deadline()
		return &pb.Request{Context: &pb.Context{RequestId: newID("req"), OperationId: id + "." + step, DeadlineMs: uint64(deadline.UnixMilli()), SandboxId: sandbox, Generation: generation, GrantId: s.worker.config.GrantID, PolicyRevision: 1}}
	}
	allocate := func() error {
		r := request("allocate", sb.Id, generation)
		resources := sb.Resources
		r.Command = &pb.Request_Allocate{Allocate: &pb.Allocate{Resources: &pb.Resources{Vcpus: uint32(resources.Vcpus), MemoryMib: resources.MemoryMib, DiskGib: resources.DiskGib, PidsMax: resources.PidsMax}, EngineId: value(sb.EngineId)}}
		return s.worker.perform(ctx, r, "allocated")
	}
	restore := func(capture string) error {
		r := request("restore", sb.Id, generation)
		r.Command = &pb.Request_Restore{Restore: &pb.Restore{CaptureId: capture}}
		return s.worker.perform(ctx, r, "restored")
	}
	start := func(argv []string, cwd string, env map[string]string) error {
		r := request("start", sb.Id, generation)
		r.Command = &pb.Request_Start{Start: &pb.Start{Argv: argv, Cwd: cwd, Env: env}}
		return s.worker.perform(ctx, r, "command_started")
	}
	var err error
	switch name {
	case "sandbox_create":
		err = allocate()
		if err == nil {
			err = restore(value(sb.TemplateId))
		}
		if err == nil {
			err = start([]string{"/bin/true"}, "/", nil)
		}
	case "sandbox_fork":
		capture := newID("capture")
		r := request("capture", source, generation)
		r.Command = &pb.Request_Capture{Capture: &pb.Capture{CaptureId: capture, PreservesMemory: true, Consistency: "crash"}}
		err = s.worker.perform(ctx, r, "captured")
		if err == nil {
			err = allocate()
		}
		if err == nil {
			err = restore(capture)
		}
		if err == nil {
			err = start([]string{"/bin/true"}, "/", nil)
		}
	case "exec_create":
		var env map[string]string
		if ex.Env != nil {
			env = map[string]string(*ex.Env)
		}
		err = start(*ex.Argv, value(ex.Cwd), env)
	}
	now := timestamp()
	s.mu.Lock()
	defer s.mu.Unlock()
	rec = s.journal.Operations[id]
	op := &rec.Operation
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
		if source != "" && (name == "sandbox_fork" || failure.unknown) {
			parent := s.journal.Sandboxes[source]
			parent.State = "unavailable"
			parent.Revision++
			parent.UpdatedAt = now
			s.journal.Sandboxes[source] = parent
		}
		if ref.Kind == "sandbox" {
			sb.State = "failed"
		} else {
			ex.State = "failed"
			if failure.unknown {
				ex.State = "outcome_unknown"
			}
		}
	} else {
		op.State = "succeeded"
		var result api.AdapterDocument
		if ref.Kind == "sandbox" {
			sb.State = "ready"
			sb.Readiness = &api.Readiness{CommandStartedAt: &now, Checks: &[]api.ReadinessCheck{{Name: "command", State: "passed"}}}
			result, _ = json.Marshal(api.SandboxCreateCompletion{SandboxId: sb.Id, SessionIds: []string{}})
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
	if err := s.save(); err != nil {
		s.broken = true
	}
	delete(s.busy, source)
	close(s.done[id])
}
func value[T any](v *T) (zero T) {
	if v != nil {
		return *v
	}
	return zero
}
