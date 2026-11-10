package control

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	public "github.com/JohanSardar/lamp/contracts/public"
	"github.com/JohanSardar/lamp/docs/spec"
	"github.com/JohanSardar/lamp/internal/surface"
	"github.com/JohanSardar/lamp/pkg/api"
)

type coreError struct{ code, message string }

func (e *coreError) Error() string                { return e.message }
func reject(code, message string) error           { return &coreError{code, message} }
func textField(m map[string]any, k string) string { v, _ := m[k].(string); return v }
func uintField(m map[string]any, k string) uint64 {
	v, _ := strconv.ParseUint(fmt.Sprint(m[k]), 10, 64)
	return v
}
func object(v any) map[string]any { b, _ := json.Marshal(v); m, _ := surface.Decode(b); return m }
func assign(m map[string]any, v any) {
	b, _ := json.Marshal(m)
	if err := json.Unmarshal(b, v); err != nil {
		panic(err)
	}
}
func newResource(kind, owner string) map[string]any {
	now := timestamp()
	return map[string]any{"id": newID(kind), "kind": kind, "fleet_id": "fl_local", "owner_id": owner, "revision": json.Number("1"), "created_at": now, "updated_at": now, "state": "active", "labels": map[string]any{}, "visibility": "private"}
}
func editResource(m map[string]any) {
	m["revision"] = json.Number(strconv.FormatUint(uintField(m, "revision")+1, 10))
	m["updated_at"] = timestamp()
}
func acceptFields(in map[string]any, fields string) error {
	for k := range in {
		if k == "idempotency_key" || k == "wait_ms" || k == "expected_revision" {
			continue
		}
		if !strings.Contains(" "+fields+" ", " "+k+" ") {
			return reject("INVALID_ARGUMENT", "unsupported field: "+k)
		}
	}
	return nil
}
func (s *Service) coreHTTP(w http.ResponseWriter, r *http.Request, id string, op api.OperationDescriptor, in map[string]any) {
	if r.Method == "GET" {
		q, err := parseQuery(r)
		if err != nil || surface.Query(op, q, in) != nil {
			writeError(w, id, "INVALID_ARGUMENT", "invalid query parameters", nil)
			return
		}
		if op.Name == "event_list" {
			s.eventsHTTP(w, r, id, in)
			return
		}
		if op.Name == "operation_wait" {
			s.waitOperation(w, r, id, in)
			return
		}
	}
	if op.Mutation {
		if r.URL.RawQuery != "" {
			writeError(w, id, "INVALID_ARGUMENT", "query parameters not accepted", nil)
			return
		}
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 128<<10))
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
				writeError(w, id, "INVALID_ARGUMENT", err.Error(), nil)
				return
			}
			in["expected_revision"] = json.Number(strconv.FormatUint(rev, 10))
		}
		if err := surface.Validate(op.InputSchema, in); err != nil {
			writeError(w, id, "INVALID_ARGUMENT", err.Error(), nil)
			return
		}
		if uintField(in, "wait_ms") > 60000 {
			writeError(w, id, "INVALID_ARGUMENT", "wait_ms limit is 60000", nil)
			return
		}
	}
	if op.Name == "connection_invoke" {
		s.invokeProtected(w, r, id, in)
		return
	}
	snapshot := &responseSnapshot{header: w.Header().Clone()}
	original := w
	w = snapshot
	s.readLock()
	defer func() {
		s.mu.Unlock()
		for k, v := range snapshot.header {
			original.Header()[k] = v
		}
		if snapshot.status == 0 {
			snapshot.status = 200
		}
		original.WriteHeader(snapshot.status)
		original.Write(snapshot.body.Bytes())
	}()
	if s.broken {
		writeError(w, id, "DATA_UNAVAILABLE", "journal unavailable", nil)
		return
	}
	a := requestCaller(r)
	if !s.can(a, op.Scope) {
		writeError(w, id, "SCOPE_DENIED", "authority changed", nil)
		return
	}
	if !op.Mutation {
		s.coreRead(w, id, a, op, in)
		return
	}
	delete(in, "wait_ms")
	raw, _ := json.Marshal(in)
	hash := bearerHash(op.Name + "\n" + string(raw))
	key := a.Principal + "\n" + op.Name + "\n" + textField(in, "idempotency_key")
	if old, ok := s.keys[key]; ok {
		rec := s.journal.Operations[old]
		if !s.operationAllowed(a, rec) {
			writeError(w, id, "NOT_FOUND", "operation not found", nil)
			return
		}
		if rec.Hash != hash {
			writeError(w, id, "IDEMPOTENCY_CONFLICT", "key already used with different inputs", pointer(old))
			return
		}
		s.sendCoreMutation(w, id, rec, true, nil)
		return
	}
	if len(s.journal.Operations) >= maxOperations {
		writeError(w, id, "CONTROL_QUOTA_EXCEEDED", "operation retention limit reached", nil)
		return
	}
	// Stage management state privately; any validation failure discards all edits.
	old := s.journal.Control
	clone := &controlState{}
	b, _ := json.Marshal(old)
	json.Unmarshal(b, clone)
	s.journal.Control = clone
	ref, result, secret, changed, err := s.coreMutation(a, op, in)
	if err != nil {
		s.journal.Control = old
		code := "INVALID_ARGUMENT"
		if e, ok := err.(*coreError); ok {
			code = e.code
		}
		writeError(w, id, code, err.Error(), nil)
		return
	}
	now := timestamp()
	operationID := newID("op")
	refs := []api.ResourceRef{ref}
	resultRaw, _ := json.Marshal(result)
	adapter := api.AdapterDocument(resultRaw)
	operation := api.Operation{Id: operationID, Kind: "operation", FleetId: "fl_local", OwnerId: a.Principal, Revision: 1, CreatedAt: now, UpdatedAt: now, State: "succeeded", Labels: api.StringMap{}, Visibility: "private", OperationName: pointer(op.Name), ResourceIds: &refs, RequestHash: pointer(hash), AcceptedAt: &now, CompletedAt: &now, CleanupState: pointer(api.OperationCleanupStateNotNeeded), Outcome: &api.Outcome{Result: &adapter}}
	rec := &record{Operation: operation, Hash: hash, Key: key, Resource: ref}
	s.journal.Operations[operationID] = rec
	s.keys[key] = operationID
	done := make(chan struct{})
	close(done)
	s.done[operationID] = done
	changed = append(changed, "$control", operationID)
	if err := s.save(changed...); err != nil {
		s.broken = true
		writeError(w, id, "DATA_UNAVAILABLE", "cannot commit management operation", pointer(operationID))
		return
	}
	s.sendCoreMutation(w, id, rec, false, secret)
}
func (s *Service) sendCoreMutation(w http.ResponseWriter, id string, rec *record, replay bool, secret *string) {
	op := rec.Operation
	result := op.Outcome.Result
	if secret != nil {
		m := object(*result)
		m["secret"] = *secret
		m["secret_available"] = true
		raw, _ := json.Marshal(m)
		transient := api.AdapterDocument(raw)
		result = &transient
	}
	status := 200
	if !terminal(op.State) {
		status = 202
		result = nil
	}
	if op.Error != nil {
		prepareError(w, id, *op.Error)()
		return
	}
	prepareJSON(w, status, api.MutationEnvelope{OperationId: op.Id, Operation: &op, ResourceIds: []api.ResourceRef{rec.Resource}, RequestId: id, Replayed: replay, Result: result})()
}
func (s *Service) coreRead(w http.ResponseWriter, id string, a caller, op api.OperationDescriptor, in map[string]any) {
	if op.Name == "api_describe" {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(public.OpenAPI)
		return
	}
	if op.Name == "docs_read" {
		topic := textField(in, "topic")
		var content string
		switch topic {
		case "", "requirements":
			content = spec.Requirements
		case "architecture":
			content = spec.Architecture
		default:
			if _, ok := surface.ByName[topic]; ok {
				content = spec.Requirements
			} else {
				for _, o := range api.Operations {
					if strings.HasPrefix(o.Name, topic+"_") {
						content = spec.Requirements
						break
					}
				}
			}
		}
		if content == "" {
			writeError(w, id, "NOT_FOUND", "documentation topic not found", nil)
			return
		}
		prepareJSON(w, 200, map[string]any{"topic": topic, "format": "text/markdown", "text": content, "installed_operations": surface.CoreNames, "request_id": id})()
		return
	}
	kind, action, _ := strings.Cut(op.Scope, ".")
	items := s.resources(a, kind)
	if action == "get" {
		wanted := textField(in, kind+"_id")
		for _, item := range items {
			if textField(item, "id") == wanted {
				prepareJSON(w, 200, map[string]any{"data": item, "request_id": id})()
				return
			}
		}
		writeError(w, id, "NOT_FOUND", "resource not found", nil)
		return
	}
	if action != "list" {
		writeError(w, id, "NOT_FOUND", "operation not installed", nil)
		return
	}
	sort.Slice(items, func(i, j int) bool { return textField(items[i], "id") < textField(items[j], "id") })
	limit := uint64(100)
	if _, ok := in["limit"]; ok {
		limit = uintField(in, "limit")
	}
	if limit > 1000 || uintField(in, "wait_ms") > 25000 {
		writeError(w, id, "INVALID_ARGUMENT", "list limit 1000 and wait_ms 25000", nil)
		return
	}
	cursor := textField(in, "cursor")
	start := 0
	if cursor != "" {
		last, err := s.readCursor(a, "list:"+kind, cursor)
		if err != nil {
			writeError(w, id, "CURSOR_EXPIRED", "invalid cursor; list again without cursor", nil)
			return
		}
		for start < len(items) && textField(items[start], "id") <= last {
			start++
		}
	}
	end := start + int(limit)
	if end > len(items) {
		end = len(items)
	}
	var next *string
	if end < len(items) && end > start {
		next = pointer(s.cursor(a, "list:"+kind, textField(items[end-1], "id")))
	}
	prepareJSON(w, 200, map[string]any{"items": items[start:end], "next_cursor": next, "request_id": id})()
}
func (s *Service) resources(a caller, kind string) []map[string]any {
	items := []map[string]any{}
	c := s.journal.Control
	switch kind {
	case "sandbox":
		for _, v := range s.journal.Sandboxes {
			if s.sandboxAllowed(a, v) {
				items = append(items, object(v))
			}
		}
	case "exec":
		for _, v := range s.journal.Execs {
			if s.execAllowed(a, v) {
				items = append(items, object(v))
			}
		}
	case "operation":
		for _, v := range s.durableOps {
			if s.operationAllowed(a, v) {
				items = append(items, object(v.Operation))
			}
		}
	case "fleet":
		items = append(items, object(c.Fleet))
	case "key":
		for _, v := range c.Keys {
			if value(v.Key.PrincipalId) == a.Principal || s.role(a) == "owner" {
				items = append(items, object(v.Key))
			}
		}
	case "principal":
		for _, v := range c.Principals {
			if v.Id == a.Principal || s.role(a) == "owner" || s.role(a) == "admin" {
				items = append(items, object(v))
			}
		}
	case "membership":
		for _, v := range c.Memberships {
			if value(v.PrincipalId) == a.Principal || s.role(a) == "owner" || s.role(a) == "admin" {
				items = append(items, object(v))
			}
		}
	case "approval":
		for _, v := range c.Approvals {
			if value(v.PrincipalId) == a.Principal || s.role(a) == "owner" {
				if !s.approvalSelectors(a, v) {
					continue
				}
				if expired(v.ExpiresAt) && v.State != "consumed" && v.State != "denied" && v.State != "revoked" {
					v.State = "expired"
				}
				items = append(items, object(v))
			}
		}
	}
	filtered := items[:0]
	for _, item := range items {
		if kind == "sandbox" || kind == "exec" || kind == "operation" || s.resourceAllowed(a, api.ResourceRef{Kind: api.ResourceKind(kind), Id: textField(item, "id")}) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}
func (s *Service) coreMutation(a caller, op api.OperationDescriptor, in map[string]any) (api.ResourceRef, map[string]any, *string, []string, error) {
	var ref api.ResourceRef
	result := map[string]any{}
	var secret *string
	changed := []string{}
	c := s.journal.Control
	fail := func(code, message string) (api.ResourceRef, map[string]any, *string, []string, error) {
		return ref, nil, nil, nil, reject(code, message)
	}
	kind, verb, _ := strings.Cut(op.Scope, ".")
	if kind == "operation" && verb == "cancel" {
		return s.cancelOperation(a, in)
	}
	if kind == "approval" {
		return s.approvalMutation(a, op, in)
	}
	if kind != "key" && kind != "principal" && kind != "membership" && kind != "fleet" {
		return fail("NOT_FOUND", "operation not installed")
	}
	if verb == "create" && (kind == "principal" || kind == "membership") {
		for _, scope := range s.scopes(a) {
			if scope.ResourceIds != nil {
				return fail("SCOPE_DENIED", "new management resource is outside resource selector")
			}
		}
	}
	var m map[string]any
	if verb != "create" {
		rid := textField(in, kind+"_id")
		for _, v := range s.resources(a, kind) {
			if textField(v, "id") == rid {
				m = v
				break
			}
		}
		if m == nil {
			return fail("NOT_FOUND", "resource not found")
		}
		if uintField(m, "revision") != uintField(in, "expected_revision") {
			return fail("REVISION_CONFLICT", fmt.Sprintf("current revision is %d", uintField(m, "revision")))
		}
		editResource(m)
	}
	switch kind {
	case "fleet":
		if err := acceptFields(in, "fleet_id mode name"); err != nil {
			return ref, nil, nil, nil, err
		}
		if verb != "update" {
			return fail("NOT_FOUND", "operation not installed")
		}
		for _, k := range []string{"mode", "name"} {
			if v, ok := in[k]; ok {
				m[k] = v
			}
		}
		assign(m, &c.Fleet)
	case "principal":
		if err := acceptFields(in, "principal_id issuer subject display_name disabled"); err != nil {
			return ref, nil, nil, nil, err
		}
		if verb == "create" {
			if textField(in, "issuer") == "" || textField(in, "subject") == "" {
				return fail("INVALID_ARGUMENT", "issuer and subject required")
			}
			for _, p := range c.Principals {
				if value(p.Issuer) == textField(in, "issuer") && value(p.Subject) == textField(in, "subject") {
					return fail("RESOURCE_IN_USE", "issuer/subject already enrolled")
				}
			}
			m = newResource(kind, c.OwnerID)
		}
		if verb == "update" || verb == "create" {
			for _, k := range []string{"issuer", "subject"} {
				if verb == "update" {
					if v, ok := in[k]; ok && v != m[k] {
						return fail("INVALID_ARGUMENT", "issuer and subject are immutable")
					}
				}
			}
			for _, k := range []string{"issuer", "subject", "display_name", "disabled"} {
				if v, ok := in[k]; ok {
					m[k] = v
				}
			}
		} else if verb == "delete" {
			m["state"] = "deleted"
		} else {
			return fail("NOT_FOUND", "operation not installed")
		}
		var v api.Principal
		assign(m, &v)
		c.Principals[v.Id] = v
	case "membership":
		if err := acceptFields(in, "membership_id principal_id role"); err != nil {
			return ref, nil, nil, nil, err
		}
		if verb == "create" {
			principal := textField(in, "principal_id")
			p, ok := c.Principals[principal]
			if !ok || p.State != "active" {
				return fail("NOT_FOUND", "principal not found")
			}
			for _, v := range c.Memberships {
				if value(v.PrincipalId) == principal && v.State == "active" {
					return fail("RESOURCE_IN_USE", "principal already has a membership")
				}
			}
			m = newResource(kind, c.OwnerID)
			m["principal_id"] = principal
		}
		if p, ok := in["principal_id"]; ok && verb != "create" && p != m["principal_id"] {
			return fail("INVALID_ARGUMENT", "membership principal is immutable")
		}
		role := textField(in, "role")
		if verb == "create" && role == "" {
			return fail("INVALID_ARGUMENT", "role required")
		}
		if role == "owner" || s.role(a) == "admin" && (role == "admin" || textField(m, "role") == "admin") {
			return fail("SCOPE_DENIED", "owner role and administrator promotion require owner authority")
		}
		if role != "" {
			m["role"] = role
		}
		if verb == "delete" {
			m["state"] = "deleted"
		} else if verb != "create" && verb != "update" {
			return fail("NOT_FOUND", "operation not installed")
		}
		var v api.Membership
		assign(m, &v)
		c.Memberships[v.Id] = v
	case "key":
		if err := acceptFields(in, "key_id principal_id scope expires_at parent_key_id"); err != nil {
			return ref, nil, nil, nil, err
		}
		if verb == "delete" {
			if !s.own(a, textField(m, "principal_id")) && s.role(a) != "owner" {
				return fail("SCOPE_DENIED", "key not owned")
			}
			m["state"] = "revoked"
			v := c.Keys[textField(m, "id")]
			assign(m, &v.Key)
			c.Keys[v.Key.Id] = v
			break
		}
		allowedActions := []api.ScopeActionsItem{}
		for _, candidate := range api.Operations {
			if s.can(a, candidate.Scope) {
				allowedActions = append(allowedActions, api.ScopeActionsItem(candidate.Scope))
			}
		}
		var scope api.Scope
		parent := a.KeyID
		principal := a.Principal
		var inheritedExpiry *string
		if verb == "rotate" {
			old := c.Keys[textField(m, "id")]
			if value(old.Key.PrincipalId) != a.Principal {
				return fail("SCOPE_DENIED", "key not owned")
			}
			if old.Key.State != "active" {
				return fail("SCOPE_DENIED", "key inactive")
			}
			principal = value(old.Key.PrincipalId)
			if old.Key.Scope != nil {
				scope = *old.Key.Scope
			}
			parent = value(old.Key.ParentKeyId)
			inheritedExpiry = old.Key.ExpiresAt
			// A different issuer must remain a live ancestor of the replacement.
			// Self-rotation keeps the old ancestry without depending on the
			// key that this transaction revokes.
			if a.KeyID != "" && a.KeyID != old.Key.Id {
				ancestor := value(old.Key.ParentKeyId)
				if ancestor != "" {
					found := false
					for id := a.KeyID; id != ""; id = value(c.Keys[id].Key.ParentKeyId) {
						if id == old.Key.Id {
							return fail("SCOPE_DENIED", "cannot rotate an ancestor of the issuer")
						}
						if id == ancestor {
							found = true
							break
						}
					}
					if !found {
						return fail("SCOPE_DENIED", "rotation would drop inherited ancestry")
					}
				}
				parent = a.KeyID
			}
			old.Key.State = "revoked"
			old.Key.Revision++
			old.Key.UpdatedAt = timestamp()
			c.Keys[old.Key.Id] = old
		} else if verb == "create" {
			if p := textField(in, "principal_id"); p != "" {
				principal = p
			}
			if principal != a.Principal {
				return fail("SCOPE_DENIED", "cannot issue another principal's key")
			}
			if _, ok := in["parent_key_id"]; ok && textField(in, "parent_key_id") != parent {
				return fail("SCOPE_DENIED", "parent must be authenticated issuer key")
			}
			if raw, ok := in["scope"]; ok {
				assign(raw.(map[string]any), &scope)
			}
		} else {
			return fail("NOT_FOUND", "operation not installed")
		}
		if principal != c.OwnerID {
			p, ok := c.Principals[principal]
			if !ok || p.State != "active" {
				return fail("NOT_FOUND", "principal not found")
			}
			if s.role(caller{Principal: principal}) == "" {
				return fail("SCOPE_DENIED", "principal not admitted in fleet mode")
			}
		}
		scope = narrow(api.Scope{Actions: &allowedActions}, scope)
		for _, p := range s.scopes(a) {
			scope = narrow(p, scope)
		}
		if parent != "" {
			p := c.Keys[parent]
			scope = narrow(value(p.Key.Scope), scope)
			if p.Key.ExpiresAt != nil && (inheritedExpiry == nil || earlier(*p.Key.ExpiresAt, *inheritedExpiry)) {
				inheritedExpiry = p.Key.ExpiresAt
			}
		}
		expiry := textField(in, "expires_at")
		if inheritedExpiry != nil && (expiry == "" || earlier(*inheritedExpiry, expiry)) {
			expiry = *inheritedExpiry
		}
		if scope.ExpiresAt != nil && (expiry == "" || earlier(*scope.ExpiresAt, expiry)) {
			expiry = *scope.ExpiresAt
		}
		if expiry == "" {
			return fail("INVALID_ARGUMENT", "key expires_at is required")
		}
		t, e := time.Parse(time.RFC3339Nano, expiry)
		if e != nil || !time.Now().Before(t) {
			return fail("INVALID_ARGUMENT", "key expiry must be in the future")
		}
		// Unsupported limit dimensions are denied rather than silently unenforced.
		if scope.Limits != nil {
			l := scope.Limits
			if l.WallTimeMs != nil || l.TokenLimit != nil || l.RetainedBytes != nil || l.ControlRecords != nil {
				return fail("INVALID_ARGUMENT", "limit dimension requires its owning subsystem")
			}
		}
		m = newResource(kind, principal)
		m["principal_id"] = principal
		m["scope"] = object(scope)
		m["expires_at"] = expiry
		if parent != "" {
			m["parent_key_id"] = parent
		}
		bytes := make([]byte, 32)
		if _, err := rand.Read(bytes); err != nil {
			return fail("DATA_UNAVAILABLE", "random source unavailable")
		}
		token := "lamp_" + base64.RawURLEncoding.EncodeToString(bytes)
		secret = &token
		var v api.Key
		assign(m, &v)
		c.Keys[v.Id] = storedKey{Key: v, SHA256: bearerHash(token)}
		result["secret_available"] = false
	}
	ref = api.ResourceRef{Kind: api.ResourceKind(kind), Id: textField(m, "id")}
	result[kind+"_id"] = ref.Id
	return ref, result, secret, changed, nil
}

// cancelOperation never claims to undo a dispatched worker command. Accepted
// work can be canceled before dispatch; running work retains an explicit error.
func (s *Service) cancelOperation(a caller, in map[string]any) (api.ResourceRef, map[string]any, *string, []string, error) {
	id := textField(in, "operation_id")
	rec, ok := s.journal.Operations[id]
	ref := api.ResourceRef{Kind: "operation", Id: id}
	if !ok || !s.operationAllowed(a, rec) {
		return ref, nil, nil, nil, reject("NOT_FOUND", "operation not found")
	}
	if rec.Operation.Revision != uintField(in, "expected_revision") {
		return ref, nil, nil, nil, reject("REVISION_CONFLICT", fmt.Sprintf("current revision is %d", rec.Operation.Revision))
	}
	if terminal(rec.Operation.State) {
		return ref, map[string]any{}, nil, nil, nil
	}
	if rec.Operation.State != "accepted" {
		return ref, nil, nil, nil, reject("OUTCOME_UNKNOWN", "work already dispatched; inspect the original operation and cleanup")
	}
	rec.Operation.CancelEffect = pointer(api.OperationCancelEffectPrevented)
	rec.Operation.State = "canceled"
	rec.Operation.Revision++
	rec.Operation.UpdatedAt = timestamp()
	rec.Operation.CompletedAt = pointer(timestamp())
	rec.Operation.CleanupState = pointer(api.OperationCleanupStateComplete)
	if rec.Resource.Kind == "sandbox" && value(rec.Operation.OperationName) != "sandbox_delete" {
		sb := s.journal.Sandboxes[rec.Resource.Id]
		sb.State = "failed"
		sb.Revision++
		sb.UpdatedAt = timestamp()
		s.journal.Sandboxes[sb.Id] = sb
	}
	if rec.Resource.Kind == "exec" {
		ex := s.journal.Execs[rec.Resource.Id]
		ex.State = "failed"
		ex.Revision++
		ex.UpdatedAt = timestamp()
		s.journal.Execs[ex.Id] = ex
	}
	return ref, map[string]any{}, nil, []string{id, rec.Resource.Id}, nil
}

// Keep SHA-256 action digests visually distinct from bearer hashes.
func digest(v any) string {
	raw, _ := json.Marshal(v)
	canonical, _ := surface.Decode(raw)
	if canonical != nil {
		raw, _ = json.Marshal(canonical)
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// responseSnapshot takes immutable response bytes under mu; network writes occur
// after unlocking, including management mutations and large documentation reads.
type responseSnapshot struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (w *responseSnapshot) Header() http.Header    { return w.header }
func (w *responseSnapshot) WriteHeader(status int) { w.status = status }
func (w *responseSnapshot) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	return w.body.Write(b)
}
