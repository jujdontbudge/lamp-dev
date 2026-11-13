package control

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"lamp.eilmer.si/internal/surface"
	"lamp.eilmer.si/pkg/api"
)

// lifePlan is what a T19 handler decides while holding mu. An immediate plan
// commits its result with the operation; an asynchronous plan runs without
// mu and then finishes under mu before the outcome becomes durable.
type lifePlan struct {
	ref     api.ResourceRef
	result  map[string]any
	run     func(ctx context.Context) (map[string]any, error)
	finish  func(result map[string]any, err error) error
	ids     []string
	timeout time.Duration
	// more names resources the finished operation produced.
	more func() []api.ResourceRef
}

type lifeHandler func(s *Service, a caller, in map[string]any) (lifePlan, error)

// lifeHandlers are the mutations this subsystem installs.
var lifeHandlers = map[string]lifeHandler{}

// lifeReads are reads served from lifecycle state (get/list by kind).
var lifeKinds = map[string]bool{"blob": true, "cache": true, "volume": true, "attachment": true, "artifact": true, "backup": true,
	"snapshot": true, "template": true, "recipe": true, "warm_pool": true, "change_set": true}

func lifeOperation(name string) bool {
	if strings.HasPrefix(name, "change_set_") {
		return false
	}
	if _, ok := lifeHandlers[name]; ok {
		return true
	}
	if name == "blob_download" || name == "artifact_download" {
		return true
	}
	op, ok := surface.ByName[name]
	if !ok {
		return false
	}
	kind, verb, _ := strings.Cut(op.Scope, ".")
	return lifeKinds[kind] && (verb == "get" || verb == "list")
}

func (s *Service) lifeHTTP(w http.ResponseWriter, r *http.Request, id string, op api.OperationDescriptor, in map[string]any) {
	a := requestCaller(r)
	if !op.Mutation {
		q, err := parseQuery(r)
		if err != nil || surface.Query(op, q, in) != nil {
			writeError(w, id, "INVALID_ARGUMENT", "invalid query parameters", nil)
			return
		}
		s.readLock()
		send := s.lifeRead(w, id, a, op, in)
		s.mu.Unlock()
		send()
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, id, "INVALID_ARGUMENT", "query parameters not accepted", nil)
		return
	}
	limit := int64(256 << 10)
	if op.Name == "blob_upload" {
		limit = 1536 << 10
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		writeError(w, id, "INVALID_ARGUMENT", "body exceeds limit", nil)
		return
	}
	if len(data) == 0 {
		data = []byte("{}")
	}
	body, err := surface.Decode(data)
	if err != nil {
		writeError(w, id, "INVALID_ARGUMENT", err.Error(), nil)
		return
	}
	for k, v := range body {
		if _, ok := in[k]; ok || k == "idempotency_key" || k == "expected_revision" {
			writeError(w, id, "INVALID_ARGUMENT", "authority and path selectors belong in headers/path", nil)
			return
		}
		in[k] = v
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || strings.TrimSpace(keys[0]) == "" || len(keys[0]) > 256 {
		writeError(w, id, "INVALID_ARGUMENT", "one Idempotency-Key of 1–256 bytes required", nil)
		return
	}
	in["idempotency_key"] = keys[0]
	props := surface.Definition(op.InputSchema)["properties"].(map[string]any)
	if _, ok := props["expected_revision"]; ok {
		rev, err := expectedRevision(r)
		if err != nil {
			writeError(w, id, "INVALID_ARGUMENT", "one uint64 If-Match header is required", nil)
			return
		}
		in["expected_revision"] = jsonNumber(rev)
	}
	if err := surface.Validate(op.InputSchema, in); err != nil {
		writeError(w, id, "INVALID_ARGUMENT", err.Error(), nil)
		return
	}
	wait := uintField(in, "wait_ms")
	// Share URLs are transient: operation reads cannot recover a bearer.
	// Give the initial response time to carry its one permitted disclosure.
	if op.Name == "port_share_link" && wait == 0 {
		wait = 60000
	}
	if wait > 60000 {
		writeError(w, id, "INVALID_ARGUMENT", "wait_ms limit is 60000", nil)
		return
	}
	delete(in, "wait_ms")
	opID, replayed, err := s.lifeStart(a, op.Name, in)
	if err != nil {
		code := "INVALID_ARGUMENT"
		var e *coreError
		if errors.As(err, &e) {
			code = e.code
		}
		var operation *string
		if opID != "" {
			operation = &opID
		}
		writeError(w, id, code, err.Error(), operation)
		return
	}
	s.lifeRespond(w, r, id, opID, replayed, wait)
}

// lifeStart admits a mutation: idempotent replay, the handler's plan under
// mu, then a durable accepted (or completed) Operation before any effect.
func (s *Service) lifeStart(a caller, name string, in map[string]any) (string, bool, error) {
	return s.lifeStartWith(a, name, in, lifeHandlers[name])
}

func (s *Service) lifeStartWith(a caller, name string, in map[string]any, handler lifeHandler) (string, bool, error) {
	raw, _ := json.Marshal(in)
	hash := bearerHash(name + "\n" + string(raw))
	key := a.Principal + "\n" + name + "\n" + textField(in, "idempotency_key")
	s.readLock()
	retryHash := s.retryHash(name, in)
	if s.broken {
		s.mu.Unlock()
		return "", false, reject("DATA_UNAVAILABLE", "journal unavailable")
	}
	if !s.can(a, surface.ByName[name].Scope) {
		s.mu.Unlock()
		return "", false, reject("SCOPE_DENIED", "authority changed")
	}
	if old, ok := s.keys[key]; ok {
		rec := s.journal.Operations[old]
		s.mu.Unlock()
		if !s.lifeOperationAllowed(a, rec) {
			return "", false, reject("NOT_FOUND", "operation not found")
		}
		if rec.Hash != hash && (rec.RetryHashV1 == "" || rec.RetryHashV1 != retryHash) {
			return old, false, reject("IDEMPOTENCY_CONFLICT", "key already used with different inputs")
		}
		return old, true, nil
	}
	if len(s.journal.Operations) >= maxOperations {
		s.mu.Unlock()
		return "", false, reject("CONTROL_QUOTA_EXCEEDED", "operation retention limit reached")
	}
	before, _ := json.Marshal(s.life)
	plan, err := handler(s, a, in)
	if err != nil {
		// A rejected plan leaves no trace: restore the staged state.
		s.restoreLife(before)
		s.mu.Unlock()
		return "", false, err
	}
	now := timestamp()
	opID := s.resourceID("op")
	refs := []api.ResourceRef{plan.ref}
	operation := api.Operation{Id: opID, Kind: "operation", FleetId: "fl_local", OwnerId: a.Principal, Revision: 1, CreatedAt: now, UpdatedAt: now,
		State: "accepted", Labels: api.StringMap{}, Visibility: "private", OperationName: pointer(name), ResourceIds: &refs, RequestHash: pointer(hash),
		AcceptedAt: &now, CleanupState: pointer(api.OperationCleanupStateNotNeeded), Durability: pointer(api.OperationDurabilityLocal)}
	done := make(chan struct{})
	if plan.run == nil {
		resultRaw, _ := json.Marshal(plan.result)
		adapter := api.AdapterDocument(resultRaw)
		operation.State = "succeeded"
		operation.CompletedAt = &now
		operation.Outcome = &api.Outcome{Result: &adapter}
		close(done)
	}
	rec := &record{Operation: operation, Hash: hash, RetryHashV1: retryHash, Key: key, Resource: plan.ref, Caller: a}
	s.journal.Operations[opID] = rec
	s.keys[key] = opID
	s.done[opID] = done
	if err := s.saveLife(); err != nil {
		s.broken = true
		s.mu.Unlock()
		return opID, false, reject("DATA_UNAVAILABLE", "cannot persist lifecycle state")
	}
	if err := s.save(append([]string{opID}, plan.ids...)...); err != nil {
		s.broken = true
		s.mu.Unlock()
		return opID, false, reject("DATA_UNAVAILABLE", "cannot persist operation admission")
	}
	if plan.run != nil {
		s.wg.Add(1)
		go s.lifeRun(opID, plan)
	}
	s.mu.Unlock()
	return opID, false, nil
}

// restoreLife puts back the state encoded in before. Running operations hold
// pointers to lifecycle objects and to their resource maps, so every object
// that survives is restored in place; only objects the rejected plan added
// or removed come and go. Under mu.
func (s *Service) restoreLife(before []byte) {
	restored := newLifeState()
	decodeNumbers(before, restored)
	s.keepEphemeral(restored)
	cur, next := reflect.ValueOf(s.life).Elem(), reflect.ValueOf(restored).Elem()
	for i := 0; i < cur.NumField(); i++ {
		cf, nf := cur.Field(i), next.Field(i)
		switch {
		case cf.Kind() == reflect.Map && cf.Type().Elem().Kind() == reflect.Pointer:
			for _, k := range cf.MapKeys() {
				if v := nf.MapIndex(k); v.IsValid() {
					restoreInPlace(cf.MapIndex(k).Elem(), v.Elem())
				} else {
					cf.SetMapIndex(k, reflect.Value{})
				}
			}
			for _, k := range nf.MapKeys() {
				if !cf.MapIndex(k).IsValid() {
					cf.SetMapIndex(k, nf.MapIndex(k))
				}
			}
		case cf.Kind() == reflect.Map && !cf.IsNil() && !nf.IsNil():
			cf.Clear()
			for _, k := range nf.MapKeys() {
				cf.SetMapIndex(k, nf.MapIndex(k))
			}
		default:
			cf.Set(nf)
		}
	}
}

// restoreInPlace copies src's fields into dst, keeping dst's top-level maps
// (resource documents that a running plan's closures still edit).
func restoreInPlace(dst, src reflect.Value) {
	for i := 0; i < dst.NumField(); i++ {
		df, sf := dst.Field(i), src.Field(i)
		if !df.CanSet() {
			continue // in-memory fields; keepEphemeral already carried them
		}
		if df.Kind() == reflect.Map && !df.IsNil() && !sf.IsNil() {
			df.Clear()
			for _, k := range sf.MapKeys() {
				df.SetMapIndex(k, sf.MapIndex(k))
			}
			continue
		}
		df.Set(sf)
	}
}

// keepEphemeral carries in-memory activity fields across a state restore.
func (s *Service) keepEphemeral(restored *lifeState) {
	for id, old := range s.life.Sandboxes {
		if n, ok := restored.Sandboxes[id]; ok {
			n.Activity, n.IdleFrom, n.waking, n.reconciling = old.Activity, old.IdleFrom, old.waking, old.reconciling
		}
	}
}

func (s *Service) lifeRun(opID string, plan lifePlan) {
	defer s.wg.Done()
	timeout := plan.timeout
	if timeout == 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(s.context(), timeout)
	defer cancel()
	s.readLock()
	rec := s.journal.Operations[opID]
	rec.Operation.State = "running"
	rec.Operation.Revision++
	rec.Operation.UpdatedAt = timestamp()
	s.mu.Unlock()
	result, err := plan.run(ctx)
	s.readLock()
	defer s.mu.Unlock()
	ids := plan.ids
	if plan.finish != nil {
		err = plan.finish(result, err)
	}
	if plan.more != nil {
		more := plan.more()
		refs := append(*rec.Operation.ResourceIds, more...)
		for _, ref := range more {
			ids = append(ids, ref.Id)
		}
		rec.Operation.ResourceIds = &refs
	}
	now := timestamp()
	op := &rec.Operation
	op.CompletedAt = &now
	op.UpdatedAt = now
	op.Revision++
	if err != nil {
		code, message, unknown := "DATA_UNAVAILABLE", err.Error(), false
		var ce *coreError
		var wf *workerFailure
		if errors.As(err, &ce) {
			code = ce.code
		} else if errors.As(err, &wf) {
			code, unknown = wf.code, wf.unknown
		}
		if _, ok := errorStatuses[code]; !ok {
			code = "DATA_UNAVAILABLE"
		}
		op.State = "failed"
		if unknown {
			op.State, code = "outcome_unknown", "OUTCOME_UNKNOWN"
			op.CleanupState = pointer(api.OperationCleanupStatePending)
		}
		op.Error = &api.ErrorDetail{Code: api.ErrorCode(code), Message: message, OperationId: pointer(opID)}
	} else {
		if result == nil {
			result = map[string]any{}
		}
		raw, _ := json.Marshal(result)
		adapter := api.AdapterDocument(raw)
		op.State = "succeeded"
		op.Outcome = &api.Outcome{Result: &adapter}
	}
	if s.broken {
		close(s.done[opID])
		return
	}
	if e := s.saveLife(); e != nil {
		s.broken = true
	} else if e := s.save(append([]string{opID}, ids...)...); e != nil {
		s.broken = true
	}
	close(s.done[opID])
}

func (s *Service) lifeOperationAllowed(a caller, rec *record) bool {
	if rec == nil {
		return false
	}
	s.readLock()
	defer s.mu.Unlock()
	return s.current(a) && s.own(a, rec.Operation.OwnerId)
}

// cleanExportRead reports whether a template or blob read names one of the
// clean exports kept by custody (T9) rather than a T19 resource.
func (s *Service) cleanExportRead(name string, selectors map[string]any) bool {
	if name != "template_get" && name != "blob_get" && name != "blob_download" {
		return false
	}
	s.readLock()
	defer s.mu.Unlock()
	if name == "template_get" {
		_, ok := s.journal.Templates[textField(selectors, "template_id")]
		return ok
	}
	blob := textField(selectors, "blob_id")
	for _, t := range s.journal.Templates {
		if t.BlobID == blob {
			return true
		}
	}
	return false
}

// lifeRespond mirrors respond, with the operation's canonical scope.
func (s *Service) lifeRespond(w http.ResponseWriter, r *http.Request, requestID, id string, replayed bool, wait uint64) {
	s.mu.Lock()
	done := s.done[id]
	s.mu.Unlock()
	if wait > 0 && done != nil {
		timer := time.NewTimer(time.Duration(wait) * time.Millisecond)
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
	rec := s.durableOps[id]
	if s.broken || rec == nil {
		send = prepareError(w, requestID, api.ErrorDetail{Code: "DATA_UNAVAILABLE", Message: "journal unavailable", OperationId: pointer(id)})
		return
	}
	op := rec.Operation
	refs := []api.ResourceRef{rec.Resource}
	if op.ResourceIds != nil {
		refs = *op.ResourceIds
	}
	result := api.MutationEnvelope{OperationId: id, ResourceIds: refs, RequestId: requestID, Replayed: replayed, Operation: &op}
	status := 202
	if terminal(op.State) {
		status = 200
	}
	if op.State == "succeeded" && op.Outcome != nil {
		result.Result = op.Outcome.Result
		if value(op.OperationName) == "port_share_link" && op.Outcome != nil && op.Outcome.Result != nil && !replayed {
			var out map[string]any
			json.Unmarshal(*op.Outcome.Result, &out)
			grant := textField(out, "grant_id")
			if url := s.previewSecrets[grant]; url != "" {
				out["url"], out["secret_available"] = url, true
				delete(s.previewSecrets, grant)
				raw, _ := json.Marshal(out)
				transient := api.AdapterDocument(raw)
				result.Result = &transient
			}
		}
	}
	send = prepareJSON(w, status, result)
}

// lifeWait blocks until an internal operation completes and returns its error.
func (s *Service) lifeWait(ctx context.Context, opID string) error {
	s.mu.Lock()
	done := s.done[opID]
	s.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return reject("OUTCOME_UNKNOWN", "operation still running")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := s.journal.Operations[opID]
	if rec == nil {
		return reject("NOT_FOUND", "operation not found")
	}
	if rec.Operation.Error != nil {
		return reject(string(rec.Operation.Error.Code), rec.Operation.Error.Message)
	}
	if rec.Operation.State != "succeeded" {
		return reject("OUTCOME_UNKNOWN", "operation did not succeed")
	}
	return nil
}

// --- reads -----------------------------------------------------------------

func (s *Service) lifeOwned(a caller, kind string, m map[string]any) bool {
	return m != nil && s.current(a) && s.own(a, textField(m, "owner_id")) && s.resourceAllowed(a, api.ResourceRef{Kind: api.ResourceKind(kind), Id: textField(m, "id")})
}

func (s *Service) lifeItems(a caller, kind string) []map[string]any {
	var all []map[string]any
	add := func(m map[string]any) {
		if s.lifeOwned(a, kind, m) {
			all = append(all, m)
		}
	}
	switch kind {
	case "port", "network_policy", "network_attachment", "grant", "configuration", "connection":
		for _, n := range s.life.Networks {
			if textField(n.R, "kind") == kind {
				add(n.R)
			}
		}
	case "snapshot":
		for _, snap := range s.journal.Snapshots {
			if snap.Snapshot.State != "deleted" && s.snapshotAllowed(a, snap) {
				all = append(all, object(snap.Snapshot))
			}
		}
	case "job":
		for _, v := range s.life.Jobs {
			add(v.R)
		}
	case "blob":
		for _, v := range s.life.Blobs {
			add(v.R)
		}
	case "cache":
		for _, v := range s.life.Caches {
			add(v.R)
		}
	case "volume":
		for _, v := range s.life.Volumes {
			add(v.R)
		}
	case "attachment":
		for _, v := range s.life.Attachments {
			add(v.R)
		}
	case "artifact":
		for _, v := range s.life.Artifacts {
			add(v.R)
		}
	case "backup":
		for _, v := range s.life.Backups {
			add(v.R)
		}
	case "template":
		if s.installedTemplate != nil {
			raw, _ := json.Marshal(s.installedTemplate)
			var m map[string]any
			json.Unmarshal(raw, &m)
			add(m)
		}
		for _, v := range s.life.Templates {
			add(v.R)
		}
	case "recipe":
		for _, v := range s.life.Recipes {
			add(v.R)
		}
	case "warm_pool":
		for _, v := range s.life.WarmPools {
			// ready_count is observed, not a revisioned field.
			n := 0
			for _, state := range v.Entries {
				if state == "ready" {
					n++
				}
			}
			v.R["ready_count"] = jsonNumber(uint64(n))
			add(v.R)
		}
	case "change_set":
		for _, v := range s.life.ChangeSets {
			add(v.R)
		}
	case "trigger":
		for _, v := range s.life.Triggers {
			add(v.R)
		}
	case "schedule":
		for _, v := range s.life.Schedules {
			add(v.R)
		}
	}
	sort.Slice(all, func(i, j int) bool { return textField(all[i], "id") < textField(all[j], "id") })
	return all
}

func (s *Service) lifeFind(a caller, kind, id string) map[string]any {
	if kind == "recipe" && !strings.HasPrefix(id, "rc_") {
		// A recipe name resolves to its current immutable version.
		id = s.life.RecipeNames[a.Principal+"\n"+id]
	}
	for _, m := range s.lifeItems(a, kind) {
		if textField(m, "id") == id {
			return m
		}
	}
	return nil
}

func (s *Service) lifeRead(w http.ResponseWriter, id string, a caller, op api.OperationDescriptor, in map[string]any) func() {
	if s.broken {
		return prepareError(w, id, api.ErrorDetail{Code: "DATA_UNAVAILABLE", Message: "journal unavailable"})
	}
	if !s.can(a, op.Scope) {
		return prepareError(w, id, api.ErrorDetail{Code: "SCOPE_DENIED", Message: "authority changed"})
	}
	switch op.Name {
	case "blob_download", "artifact_download":
		return s.downloadChunk(w, id, a, op.Name, in)
	}
	kind, verb, _ := strings.Cut(op.Scope, ".")
	if verb == "get" {
		if m := s.lifeFind(a, kind, textField(in, kind+"_id")); m != nil {
			return prepareJSON(w, 200, map[string]any{"data": m, "request_id": id})
		}
		return prepareError(w, id, api.ErrorDetail{Code: "NOT_FOUND", Message: "resource not found"})
	}
	items := s.lifeItems(a, kind)
	limit := uint64(100)
	if _, ok := in["limit"]; ok {
		limit = uintField(in, "limit")
	}
	if limit > 1000 {
		return prepareError(w, id, api.ErrorDetail{Code: "INVALID_ARGUMENT", Message: "list limit 1000"})
	}
	start := 0
	if cursor := textField(in, "cursor"); cursor != "" {
		last, err := s.readCursor(a, "list:"+kind, cursor)
		if err != nil {
			return prepareError(w, id, api.ErrorDetail{Code: "CURSOR_EXPIRED", Message: "invalid cursor; list again without cursor"})
		}
		for start < len(items) && textField(items[start], "id") <= last {
			start++
		}
	}
	end := min(start+int(limit), len(items))
	var next *string
	if end < len(items) && end > start {
		next = pointer(s.cursor(a, "list:"+kind, textField(items[end-1], "id")))
	}
	if items == nil {
		items = []map[string]any{}
	}
	return prepareJSON(w, 200, map[string]any{"items": items[start:end], "next_cursor": next, "request_id": id})
}

// --- shared handler helpers ------------------------------------------------

func (s *Service) lifeResource(prefix, kind, owner string) map[string]any {
	m := newResource(kind, owner)
	m["id"] = s.resourceID(prefix)
	return m
}

func lifeRevision(m map[string]any, in map[string]any) error {
	if uintField(m, "revision") != uintField(in, "expected_revision") {
		return reject("REVISION_CONFLICT", "current revision is "+strconv.FormatUint(uintField(m, "revision"), 10))
	}
	return nil
}

func lifeRef(kind, id string) api.ResourceRef {
	return api.ResourceRef{Kind: api.ResourceKind(kind), Id: id}
}

// lifeSandboxFor returns an owned sandbox or NOT_FOUND without disclosure.
func (s *Service) lifeSandboxFor(a caller, id string) (api.Sandbox, error) {
	sb, ok := s.journal.Sandboxes[id]
	if !ok || !s.sandboxAllowed(a, sb) {
		return sb, reject("NOT_FOUND", "sandbox not found")
	}
	// A warm-pool entry stays never-used and signed out until it is
	// claimed: no uploads, attachments, saves or edits reach it.
	if l := s.life.Sandboxes[id]; l != nil && l.Pool != "" {
		return sb, reject("RESOURCE_IN_USE", "unclaimed warm-pool sandbox; claim it with sandbox_create {recipe_id}")
	}
	return sb, nil
}

func (s *Service) lifeSB(id string) *lifeSandbox {
	l := s.life.Sandboxes[id]
	if l == nil {
		l = &lifeSandbox{}
		s.life.Sandboxes[id] = l
	}
	return l
}

func stringsField(m map[string]any, k string) []string {
	raw, ok := m[k].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
