package control

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/JohanSardar/lamp/internal/surface"
	"github.com/JohanSardar/lamp/pkg/api"
)

// Kit metadata belongs to the normal durable control journal. Private page data,
// input, native vault output and backend bearers never enter this structure.
type kitResource struct {
	Data map[string]any    `json:"data"`
	Jobs map[string]string `json:"jobs,omitempty"`
}
type kitState struct {
	Browsers map[string]*kitResource `json:"browsers"`
	Kits     map[string]*kitResource `json:"kits"`
}

func (s *Service) initKits() {
	if s.journal.Control.Kits == nil {
		s.journal.Control.Kits = &kitState{}
	}
	k := s.journal.Control.Kits
	if k.Browsers == nil {
		k.Browsers = map[string]*kitResource{}
	}
	if k.Kits == nil {
		k.Kits = map[string]*kitResource{}
	}
	// A backend restart revokes its task grants and fences recovered browsers.
	for _, b := range k.Browsers {
		if textField(b.Data, "state") != "closed" {
			b.Data["state"] = "human_control"
		}
	}
}
func kitOperation(name string) bool {
	return strings.HasPrefix(name, "browser_") || strings.HasPrefix(name, "kit_")
}
func (s *Service) kitOwner(a caller) bool {
	return a.KeyID == "" && a.Principal == s.journal.Control.OwnerID
}
func (s *Service) privateBrowserDenied(a caller, sb api.Sandbox) bool {
	return !s.kitOwner(a) && sb.Labels["lamp.browser-custody"] == "external"
}
func (s *Service) kitCall(ctx context.Context, in map[string]any) (map[string]any, error) {
	raw, _ := json.Marshal(in)
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", s.kitsSocket)
	}}
	defer transport.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(ctx, "POST", "http://kits/owner", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		return nil, reject("HOST_UNAVAILABLE", "kits backend unavailable; outcome must be reconciled, not replayed")
	}
	defer response.Body.Close()
	raw, err = io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return nil, reject("DATA_UNAVAILABLE", "kits response unavailable")
	}
	var out map[string]any
	if json.Unmarshal(raw, &out) != nil {
		return nil, reject("DATA_UNAVAILABLE", "invalid kits response")
	}
	if response.StatusCode != 200 {
		reason := textField(out, "error")
		switch reason {
		case "BROWSER_SETUP_FAILED_CLEANUP_REQUIRED", "BROWSER_SETUP_FAILED_CLEANUP_REQUIRED:boot.key", "BROWSER_SETUP_FAILED_CLEANUP_REQUIRED:boot.start", "BROWSER_SETUP_FAILED_CLEANUP_REQUIRED:boot.cdp", "BROWSER_SETUP_FAILED_CLEANUP_REQUIRED:boot.context":
			return nil, reject("HOST_UNAVAILABLE", reason)
		case "PRIVATE_CONTROL_ACTIVE", "PRIVATE_TYPING_REQUIRED", "PRIVATE_TAKEOVER_REQUIRED", "READY_VM_REQUIRED", "GENERATION_CHANGED", "APPROVAL_REQUIRED", "APPROVAL_PLAN_REQUIRED", "OWNER_SANDBOX_CAPACITY":
			return nil, reject("SCOPE_DENIED", reason)
		}
		return nil, reject("SCOPE_DENIED", "kits action refused; use private owner control for unsupported forms or locked vaults")
	}
	return out, nil
}

func (s *Service) kitsHTTP(w http.ResponseWriter, r *http.Request, id string, op api.OperationDescriptor, in map[string]any) {
	// Serialize kit transitions, not the main journal mutex: backend effects call
	// Lamp's public sandbox/exec API, whose handlers must remain able to run.
	s.kitsMu.Lock()
	defer s.kitsMu.Unlock()
	if s.kitsSocket == "" {
		writeError(w, id, "HOST_UNAVAILABLE", "start lampd with --kits-config or an enrolled --kits-socket", nil)
		return
	}
	if r.Method == "GET" {
		q, err := parseQuery(r)
		if err != nil || surface.Query(op, q, in) != nil {
			writeError(w, id, "INVALID_ARGUMENT", "invalid query", nil)
			return
		}
	} else {
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 128<<10))
		if err != nil {
			writeError(w, id, "INVALID_ARGUMENT", "body exceeds limit", nil)
			return
		}
		body, err := surface.Decode(raw)
		if err != nil {
			writeError(w, id, "INVALID_ARGUMENT", "invalid JSON input", nil)
			return
		}
		for k, v := range body {
			if _, exists := in[k]; exists || k == "idempotency_key" || k == "expected_revision" {
				writeError(w, id, "INVALID_ARGUMENT", "selectors belong in path/headers", nil)
				return
			}
			in[k] = v
		}
		keys := r.Header.Values("Idempotency-Key")
		if len(keys) != 1 || len(keys[0]) == 0 || len(keys[0]) > 256 {
			writeError(w, id, "INVALID_ARGUMENT", "Idempotency-Key required", nil)
			return
		}
		in["idempotency_key"] = keys[0]
		if _, ok := surface.Definition(op.InputSchema)["properties"].(map[string]any)["expected_revision"]; ok {
			rev, e := expectedRevision(r)
			if e != nil {
				writeError(w, id, "INVALID_ARGUMENT", "If-Match required", nil)
				return
			}
			in["expected_revision"] = json.Number(strconvUint(rev))
		}
	}
	if err := surface.Validate(op.InputSchema, in); err != nil {
		writeError(w, id, "INVALID_ARGUMENT", "input does not match public schema", nil)
		return
	}
	a := requestCaller(r)
	s.readLock()
	owner := s.kitOwner(a)
	if !s.can(a, op.Scope) {
		s.mu.Unlock()
		writeError(w, id, "SCOPE_DENIED", "authority changed", nil)
		return
	}
	state := s.journal.Control.Kits
	resources := state.Browsers
	kind := "browser"
	if strings.HasPrefix(op.Name, "kit_") {
		resources = state.Kits
		kind = "kit"
	}
	resourceID := textField(in, kind+"_id")
	entry := resources[resourceID]
	permitted := func(e *kitResource) bool {
		return e != nil && textField(e.Data, "owner_id") == a.Principal && s.resourceAllowed(a, api.ResourceRef{Kind: api.ResourceKind(kind), Id: textField(e.Data, "id")})
	}
	if strings.HasSuffix(op.Name, "_list") {
		items := []any{}
		for _, e := range resources {
			if permitted(e) {
				items = append(items, e.Data)
			}
		}
		send := prepareJSON(w, 200, map[string]any{"request_id": id, "items": items})
		s.mu.Unlock()
		send()
		return
	}
	if op.Name == "kit_install_status" {
		if !permitted(entry) {
			s.mu.Unlock()
			writeError(w, id, "NOT_FOUND", "kit not found", nil)
			return
		}
		job := textField(in, "job_id")
		sandbox := entry.Jobs[job]
		if sandbox == "" || !s.sandboxAllowed(a, s.journal.Sandboxes[sandbox]) {
			s.mu.Unlock()
			writeError(w, id, "SCOPE_DENIED", "installation not accessible", nil)
			return
		}
		s.mu.Unlock()
		out, err := s.kitCall(r.Context(), map[string]any{"operation": "install_status", "sandbox_id": sandbox, "job_id": job})
		if err != nil {
			writeError(w, id, "HOST_UNAVAILABLE", "installation status unavailable", nil)
			return
		}
		prepareJSON(w, 200, map[string]any{"request_id": id, "data": out})()
		return
	}
	if op.Name == "browser_get" || op.Name == "kit_get" {
		if !permitted(entry) {
			s.mu.Unlock()
			writeError(w, id, "NOT_FOUND", "resource not found", nil)
			return
		}
		send := prepareJSON(w, 200, map[string]any{"request_id": id, "data": entry.Data})
		s.mu.Unlock()
		send()
		return
	}
	creating := op.Name == "browser_create" || op.Name == "kit_create"
	if !creating && !permitted(entry) {
		s.mu.Unlock()
		writeError(w, id, "NOT_FOUND", "resource not found", nil)
		return
	}
	if creating && !owner || (op.Name == "browser_takeover" || op.Name == "browser_return_control" || op.Name == "browser_delete" || op.Name == "kit_delete" || op.Name == "browser_update" || op.Name == "kit_update") && !owner || op.Name == "browser_fill_password" && textField(in, "approval_id") != "" && !owner {
		s.mu.Unlock()
		writeError(w, id, "SCOPE_DENIED", "private owner authority required", nil)
		return
	}
	raw, _ := json.Marshal(in)
	mac := hmac.New(sha256.New, []byte(s.journal.Control.CursorSecret))
	mac.Write(raw)
	hash := hex.EncodeToString(mac.Sum(nil))
	key := a.Principal + "\n" + op.Name + "\n" + textField(in, "idempotency_key")
	if old := s.keys[key]; old != "" {
		rec := s.journal.Operations[old]
		if rec.Hash != hash {
			s.mu.Unlock()
			writeError(w, id, "IDEMPOTENCY_CONFLICT", "key already used", nil)
			return
		}
		if !s.operationAllowed(a, rec) {
			s.mu.Unlock()
			writeError(w, id, "SCOPE_DENIED", "operation not accessible", nil)
			return
		}
		s.sendKitMutation(w, id, rec, true)
		s.mu.Unlock()
		return
	}
	if !creating {
		if _, ok := in["expected_revision"]; ok && uintField(in, "expected_revision") != uintField(entry.Data, "revision") {
			s.mu.Unlock()
			writeError(w, id, "REVISION_CONFLICT", "stale revision", nil)
			return
		}
	}
	if len(s.journal.Operations) >= maxOperations {
		s.mu.Unlock()
		writeError(w, id, "CONTROL_QUOTA_EXCEEDED", "operation retention limit", nil)
		return
	}
	if creating {
		data := newResource(kind, a.Principal)
		resourceID = textField(data, "id")
		entry = &kitResource{Data: data, Jobs: map[string]string{}}
		if kind == "browser" {
			entry.Data["state"] = "starting"
			entry.Data["sandbox_id"] = in["sandbox_id"]
			entry.Data["mode"] = "external"
			if in["mode"] != nil {
				entry.Data["mode"] = in["mode"]
			}
		} else {
			entry.Data["name"] = textField(in, "name")
			entry.Data["tools"] = in["tools"]
		}
		resources[resourceID] = entry
	}
	// Persist an intent before every effect. A crash cannot turn an unknown
	// private type/fill/create into a silently replayed mutation.
	operationID := s.resourceID("op")
	now := timestamp()
	ref := api.ResourceRef{Kind: api.ResourceKind(kind), Id: resourceID}
	refs := []api.ResourceRef{ref}
	rec := &record{Caller: a, Hash: hash, Key: key, Resource: ref, Operation: api.Operation{Id: operationID, Kind: "operation", FleetId: "fl_local", OwnerId: a.Principal, Revision: 1, CreatedAt: now, UpdatedAt: now, State: "running", Labels: api.StringMap{}, Visibility: "private", OperationName: pointer(op.Name), ResourceIds: &refs, AcceptedAt: &now}}
	s.journal.Operations[operationID] = rec
	s.keys[key] = operationID
	s.done[operationID] = make(chan struct{})
	if err := s.save("$control", operationID); err != nil {
		s.mu.Unlock()
		writeError(w, id, "DATA_UNAVAILABLE", "cannot persist kit intent", nil)
		return
	}
	// Sandbox authorization is checked against the normal journal, never against
	// backend-supplied IDs. The backend never receives an agent's core credential.
	sandbox := textField(in, "sandbox_id")
	if sandbox != "" && !s.sandboxAllowed(a, s.journal.Sandboxes[sandbox]) {
		s.mu.Unlock()
		s.finishKit(w, r, id, rec, entry, nil, reject("SCOPE_DENIED", "sandbox not accessible"), false)
		return
	}
	copyEntry := &kitResource{Data: object(entry.Data), Jobs: map[string]string{}}
	for k, v := range entry.Jobs {
		copyEntry.Jobs[k] = v
	}
	entry = copyEntry
	s.mu.Unlock()
	result, err := s.kitEffect(r.Context(), op.Name, in, entry, resourceID, owner)
	private := op.Name == "browser_action" && textField(in, "action") == "screenshot"
	s.finishKit(w, r, id, rec, entry, result, err, private)
}
func strconvUint(n uint64) string { return strconv.FormatUint(n, 10) }

// Failed and pending kit intents do not have a successful completion document.
// Preserve their saved error/operation ID instead of dereferencing nil or replaying.
func (s *Service) sendKitMutation(w http.ResponseWriter, id string, rec *record, replay bool) {
	if rec.Operation.Error != nil {
		detail := *rec.Operation.Error
		detail.OperationId = pointer(rec.Operation.Id)
		prepareError(w, id, detail)()
		return
	}
	if rec.Operation.Outcome == nil || rec.Operation.Outcome.Result == nil {
		writeError(w, id, "OUTCOME_UNKNOWN", "kit intent has no committed completion; reconcile before retry", pointer(rec.Operation.Id))
		return
	}
	s.sendCoreMutation(w, id, rec, replay, nil)
}

func (s *Service) finishKit(w http.ResponseWriter, r *http.Request, id string, rec *record, entry *kitResource, result map[string]any, err error, private bool) {
	s.readLock()
	defer s.mu.Unlock()
	now := timestamp()
	rec.Operation.CompletedAt = &now
	rec.Operation.UpdatedAt = now
	rec.Operation.Revision++
	if err != nil {
		code := "DATA_UNAVAILABLE"
		if e, ok := err.(*coreError); ok {
			code = e.code
		}
		rec.Operation.State = "failed"
		rec.Operation.Error = &api.ErrorDetail{Code: api.ErrorCode(code), Message: err.Error()}
	}
	if err == nil {
		if rec.Resource.Kind == "browser" {
			s.journal.Control.Kits.Browsers[rec.Resource.Id] = entry
		} else {
			s.journal.Control.Kits.Kits[rec.Resource.Id] = entry
		}
		rec.Operation.State = "succeeded"
		editResource(entry.Data)
		stored := result
		if private {
			stored = map[string]any{"observations": map[string]any{"private_output_available": false}}
		}
		raw, _ := json.Marshal(stored)
		adapter := api.AdapterDocument(raw)
		rec.Operation.Outcome = &api.Outcome{Result: &adapter}
	}
	if e := s.save("$control", rec.Operation.Id); e != nil {
		s.broken = true
		writeError(w, id, "DATA_UNAVAILABLE", "cannot commit kit outcome", nil)
		return
	}
	close(s.done[rec.Operation.Id])
	if !s.current(requestCaller(r)) {
		writeError(w, id, "SCOPE_DENIED", "authority changed", nil)
		return
	}
	if private && err == nil {
		raw, _ := json.Marshal(result)
		adapter := api.AdapterDocument(raw)
		copy := *rec
		copy.Operation = rec.Operation
		copy.Operation.Outcome = &api.Outcome{Result: &adapter}
		s.sendKitMutation(w, id, &copy, false)
		return
	}
	s.sendKitMutation(w, id, rec, false)
}
func (s *Service) kitEffect(ctx context.Context, name string, in map[string]any, e *kitResource, id string, owner bool) (map[string]any, error) {
	request := map[string]any{"browser_id": id}
	var completion = map[string]any{}
	switch name {
	case "kit_create":
		return map[string]any{"kit_id": id}, nil
	case "kit_delete":
		e.Data["state"] = "deleted"
		return completion, nil
	case "kit_update", "browser_update":
		return nil, reject("INVALID_ARGUMENT", "immutable kit/browser binding; create a replacement")
	case "kit_install":
		request = map[string]any{"operation": "install", "sandbox_id": in["sandbox_id"], "tools": e.Data["tools"]}
	case "kit_install_status":
		job := textField(in, "job_id")
		sandbox := e.Jobs[job]
		if sandbox == "" {
			return nil, reject("NOT_FOUND", "installation not found")
		}
		request = map[string]any{"operation": "install_status", "sandbox_id": sandbox, "job_id": job}
	case "browser_create":
		request["operation"] = "create"
		request["agent_sandbox_id"] = in["sandbox_id"]
		for _, k := range []string{"mode", "acknowledge_agent_can_read_logins", "source_browser_id", "login_origin"} {
			if v, ok := in[k]; ok {
				request[k] = v
			}
		}
	case "browser_delete":
		request["operation"] = "delete"
	case "browser_takeover":
		request["operation"] = "takeover"
	case "browser_return_control":
		request["operation"] = "return"
	case "browser_fill_password":
		if textField(in, "approval_id") == "" {
			request["operation"] = "propose_fill"
			request["vault_id"] = in["credential_source_id"]
			request["item_id"] = in["secret_ref"]
			request["origin"] = in["origin"]
			request["submit"] = in["submit"]
		} else {
			request["operation"] = "approve_fill"
			request["approval_id"] = in["approval_id"]
			request["plan"] = in["plan"]
		}
	case "browser_action":
		args, _ := in["arguments"].(map[string]any)
		for k, v := range args {
			if k != "selector" && k != "text" && k != "url" {
				return nil, reject("INVALID_ARGUMENT", "unsupported browser argument")
			}
			request[k] = v
		}
		action := textField(in, "action")
		if action == "read" {
			action = "snapshot"
		}
		if action == "screenshot" {
			if !owner {
				return nil, reject("SCOPE_DENIED", "screenshots require private owner authority")
			}
			action = "private_view"
		}
		if action == "type" && owner && textField(e.Data, "state") == "human_control" {
			action = "private_type"
		}
		if action != "navigate" && action != "click" && action != "type" && action != "snapshot" && action != "private_view" && action != "private_type" {
			return nil, reject("INVALID_ARGUMENT", "unsupported action")
		}
		request["operation"] = action
	default:
		return nil, reject("NOT_FOUND", "unknown kit action")
	}
	if !owner && strings.HasPrefix(name, "browser_") {
		request = map[string]any{"operation": "task_dispatch", "request": request}
	}
	out, err := s.kitCall(ctx, request)
	if err != nil {
		return nil, err
	}
	switch name {
	case "browser_create":
		e.Data["state"] = "agent_control"
		completion["browser_id"] = id
		if textField(out, "mode") == "copied" {
			e.Data["warning"] = "AGENT_CAN_READ_COPIED_LOGINS"
		}
	case "browser_takeover":
		e.Data["state"] = "human_control"
	case "browser_return_control":
		e.Data["state"] = "agent_control"
	case "browser_delete":
		e.Data["state"] = "closed"
	case "browser_fill_password":
		if out["private"] == true {
			e.Data["state"] = "human_control"
		}
		completion = out
	case "browser_action":
		completion["observations"] = out
	case "kit_install":
		if e.Jobs == nil {
			e.Jobs = map[string]string{}
		}
		e.Jobs[textField(out, "job_id")] = textField(in, "sandbox_id")
		completion = out
	case "kit_install_status":
		completion = out
	}
	return completion, nil
}
