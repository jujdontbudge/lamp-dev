package control

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"lamp.eilmer.si/internal/surface"
	"lamp.eilmer.si/pkg/api"
	pb "lamp.eilmer.si/pkg/workerpb"
)

//go:embed session_runner.py
var sessionRunnerPTY string

//go:embed session_artifact.py
var sessionArtifacts string

var sessionRunner = sessionArtifacts + "\n" + sessionRunnerPTY

//go:embed native_harness.py
var nativeHarness string

func (s *Service) isSessionShare(r *http.Request, selectors map[string]any) bool {
	if r.Method == "GET" {
		s.readLock()
		_, ok := s.journal.Control.SessionShares[textField(selectors, "share_id")]
		s.mu.Unlock()
		return ok
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, (128<<10)+1))
	r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(data))
	if err != nil || len(data) > 128<<10 {
		return false
	}
	in, err := surface.Decode(data)
	return err == nil && textField(in, "resource_kind") == "session"
}

type SessionHarness struct {
	OwnerID    string            `json:"owner_id"`
	Argv       []string          `json:"argv"`
	Cwd        string            `json:"cwd"`
	Env        map[string]string `json:"env"`
	AuthModes  []string          `json:"auth_modes"`
	Native     *NativeHarness    `json:"native,omitempty"`
	Repository string            `json:"repository,omitempty"`
}

type NativeHarness struct {
	Managed        bool     `json:"managed,omitempty"`
	Kind           string   `json:"kind"`
	Binary         string   `json:"binary"`
	Model          string   `json:"model,omitempty"`
	Endpoint       string   `json:"endpoint,omitempty"`
	CredentialFile string   `json:"credential_file,omitempty"`
	MCPConfig      string   `json:"mcp_config,omitempty"`
	MCPCommand     []string `json:"mcp_command,omitempty"`
	// Subagents lets the harness use its own sub-agent feature; each one is
	// then recorded as a separate observed child session.
	Subagents bool                     `json:"native_subagents,omitempty"`
	Profiles  map[string]NativeProfile `json:"profiles,omitempty"`
}

type NativeProfile struct {
	Binary         string   `json:"binary,omitempty"`
	Endpoint       string   `json:"endpoint,omitempty"`
	CredentialFile string   `json:"credential_file,omitempty"`
	CAFile         string   `json:"ca_file,omitempty"`
	MCPCommand     []string `json:"mcp_command,omitempty"`
}

type SessionHost struct {
	OwnerID string   `json:"owner_id"`
	Command []string `json:"command"`
	Root    string   `json:"root"`
	Socket  string   `json:"socket,omitempty"`
}

type SessionConfig struct {
	Harnesses map[string]SessionHarness `json:"harnesses"`
	Hosts     map[string]SessionHost    `json:"hosts"`
	Recipes   map[string]SessionRecipe  `json:"recipes,omitempty"`
}

type SessionRecipe struct {
	HarnessID string `json:"harness_id"`
	AuthMode  string `json:"auth_mode"`
}

type sessionWork struct {
	ID       string `json:"id"`
	Action   string `json:"action"`
	Lease    string `json:"lease,omitempty"`
	Sequence uint64 `json:"sequence,omitempty"`
	Data     string `json:"data,omitempty"`
	Rows     uint64 `json:"rows,omitempty"`
	Columns  uint64 `json:"columns,omitempty"`
}

type managedSession struct {
	Resource    map[string]any    `json:"resource"`
	Pending     []sessionWork     `json:"pending,omitempty"`
	Cancelled   []sessionWork     `json:"cancelled,omitempty"`
	Next        map[string]uint64 `json:"next"`
	Offset      uint64            `json:"offset"`
	Frames      []api.Frame       `json:"frames"`
	Bytes       uint64            `json:"bytes"`
	More        bool              `json:"more,omitempty"`
	EventOffset uint64            `json:"event_offset,omitempty"`
	Failure     string            `json:"failure,omitempty"`
	GitBusy     bool              `json:"git_busy,omitempty"`
	Checkpoint  string            `json:"checkpoint,omitempty"`
	// Observed marks a native sub-agent recorded from its parent's harness
	// events; it has no runner of its own and accepts no input.
	Observed bool              `json:"observed,omitempty"`
	Children map[string]string `json:"children,omitempty"`
}

func (s *Service) initSessions(config SessionConfig) error {
	for id, h := range config.Harnesses {
		if id == "" || h.OwnerID == "" || len(h.Argv) == 0 || !strings.HasPrefix(h.Cwd, "/") && !strings.HasPrefix(h.Cwd, "{session_dir}/") {
			return fmt.Errorf("session harnesses require an owner, argv and absolute cwd")
		}
		for _, mode := range h.AuthModes {
			if mode != "none" && mode != "api_key" && mode != "subscription" && mode != "delegated" {
				return fmt.Errorf("unsupported session authentication mode")
			}
		}
		if h.Native != nil && (catalogueByKind(h.Native.Kind) == nil || !strings.HasPrefix(h.Native.Binary, "/")) {
			return fmt.Errorf("native session harness requires a catalogue harness kind and an enrolled absolute official binary")
		}
		if h.Native != nil {
			// Only routes the catalogue offers: never one that would hand a
			// real vendor credential to the agent.
			entry := catalogueByKind(h.Native.Kind)
			for _, mode := range h.AuthModes {
				if mode != "none" && !slices.Contains(entry.AuthModes, mode) {
					return fmt.Errorf("native harness %s does not offer the %s credential route", entry.ID, mode)
				}
			}
		}
		for name := range h.Env {
			switch name {
			case "PATH", "HOME", "LANG", "LC_ALL", "TERM", "LAMP_API_SOCKET", "LAMP_TOKEN_FILE", "LAMP_BROKER_SOCKET", "LAMP_BROKER_TOKEN_FILE":
			default:
				return fmt.Errorf("session environment accepts only terminal settings and host-held credential paths")
			}
		}
	}
	for id, host := range config.Hosts {
		if id == "" || host.OwnerID == "" || len(host.Command) == 0 && host.Socket == "" || !strings.HasPrefix(host.Root, "/") {
			return fmt.Errorf("session hosts require an owner, runner command and private absolute root")
		}
	}
	s.sessions = config
	if s.journal.Control.Sessions == nil {
		s.journal.Control.Sessions = map[string]*managedSession{}
	}
	if s.journal.Control.InputLeases == nil {
		s.journal.Control.InputLeases = map[string]map[string]any{}
	}
	if s.journal.Control.SessionShares == nil {
		s.journal.Control.SessionShares = map[string]map[string]any{}
	}
	if s.journal.Control.SessionTransfers == nil {
		s.journal.Control.SessionTransfers = map[string]*sessionTransfer{}
	}
	if s.journal.Control.ChangeSets == nil {
		s.journal.Control.ChangeSets = map[string]*sessionChangeSet{}
	}
	for _, session := range s.journal.Control.Sessions {
		if textField(session.Resource, "state") == "active" || textField(session.Resource, "state") == "starting" {
			session.Resource["observation_state"] = "stale"
		}
	}
	return nil
}

func (s *Service) sessionAllowed(a caller, m map[string]any, action string) bool {
	if m == nil || !s.current(a) || !s.resourceAllowed(a, api.ResourceRef{Kind: "session", Id: textField(m, "id")}) {
		return false
	}
	for _, scope := range s.scopes(a) {
		if !has(scope.SandboxIds, textField(m, "sandbox_id")) {
			return false
		}
		location, _ := m["location"].(map[string]any)
		if !has(scope.HostIds, textField(location, "host_id")) || scope.RecipeIds != nil {
			return false
		}
	}
	if s.own(a, textField(m, "owner_id")) {
		return true
	}
	for _, share := range s.journal.Control.SessionShares {
		if textField(share, "resource_id") != textField(m, "id") || textField(share, "subject_id") != a.Principal || textField(share, "state") != "active" || sessionExpired(share) {
			continue
		}
		actions, _ := share["actions"].([]any)
		for _, grant := range actions {
			if grant == action {
				return true
			}
		}
	}
	return false
}

func sessionExpired(m map[string]any) bool {
	expiry := textField(m, "expires_at")
	return expiry != "" && expired(&expiry)
}

func (s *Service) chargedSessions(owner string) uint64 {
	var count uint64
	for _, work := range s.journal.Control.Sessions {
		if textField(work.Resource, "owner_id") == owner && textField(work.Resource, "state") != "ended" {
			sb, exists := s.journal.Sandboxes[textField(work.Resource, "sandbox_id")]
			if !exists || sb.State != "deleted" {
				count++
			}
		}
	}
	return count
}

func (s *Service) sessionMutation(a caller, op api.OperationDescriptor, in map[string]any) (api.ResourceRef, map[string]any, *string, []string, error) {
	c := s.journal.Control
	fail := func(code, message string) (api.ResourceRef, map[string]any, *string, []string, error) {
		return api.ResourceRef{}, nil, nil, nil, reject(code, message)
	}
	kind, verb, _ := strings.Cut(op.Scope, ".")
	result := map[string]any{}
	var m map[string]any
	var work *managedSession
	switch kind {
	case "session":
		if verb == "create" {
			if err := acceptFields(in, "sandbox_id harness_id auth_mode task_id parent_session_id agent_mode location prompt"); err != nil {
				return api.ResourceRef{}, nil, nil, nil, err
			}
			if parent := textField(in, "parent_session_id"); parent != "" {
				if p := c.Sessions[parent]; p == nil || textField(p.Resource, "owner_id") != a.Principal || !s.sessionAllowed(a, p.Resource, "session.get") {
					return fail("NOT_FOUND", "parent session not found")
				}
			}
			harness, ok := s.sessions.Harnesses[textField(in, "harness_id")]
			if !ok || harness.OwnerID != a.Principal {
				return fail("NOT_FOUND", "enrolled session harness not found")
			}
			auth := textField(in, "auth_mode")
			if auth == "" {
				auth = "none"
				if harness.Native != nil && harness.Native.Managed && s.life.Agents[textField(in, "harness_id")].Source != "" {
					auth = "api_key"
					if harness.Native.Kind == "codex" {
						auth = "subscription"
					}
				}
			}
			if auth != "none" && harness.Native == nil {
				return fail("AUTH_TRANSPORT_UNSUPPORTED", "model authentication requires a dedicated broker or native custody adapter, not a general PTY")
			}
			allowed := false
			for _, mode := range harness.AuthModes {
				allowed = allowed || mode == auth
			}
			if !allowed {
				return fail("AUTH_TRANSPORT_UNSUPPORTED", "harness authentication route is not enrolled")
			}
			location, _ := in["location"].(map[string]any)
			sbID := textField(in, "sandbox_id")
			if location == nil {
				sb := s.journal.Sandboxes[sbID]
				location = map[string]any{"kind": "sandbox", "host_id": sandboxHost(sb), "sandbox_id": sbID}
			}
			if textField(location, "kind") == "sandbox" {
				sb, ok := s.journal.Sandboxes[sbID]
				if !ok || !s.sandboxAllowed(a, sb) || sbID != textField(location, "sandbox_id") || sandboxHost(sb) != textField(location, "host_id") {
					return fail("NOT_FOUND", "session sandbox not found")
				}
				if sb.State != "ready" {
					return fail("RESOURCE_IN_USE", "session requires a ready sandbox")
				}
				if value(sb.CaptureTrust) == "owner_private" {
					return fail("AUTH_TRANSPORT_UNSUPPORTED", "native subscription custody requires its dedicated runner, not a general PTY")
				}
				if !s.admissionAllowed(a, "exec_create", sb) {
					return fail("SCOPE_DENIED", "session exceeds sandbox execution scope")
				}
			} else {
				host, ok := s.sessions.Hosts[textField(location, "host_id")]
				if !ok || host.OwnerID != a.Principal {
					return fail("SCOPE_DENIED", "native session location is not enrolled for this principal")
				}
				if sbID != "" {
					sb, ok := s.journal.Sandboxes[sbID]
					if !ok || !s.sandboxAllowed(a, sb) || textField(in, "agent_mode") != "outside" {
						return fail("NOT_FOUND", "outside session target sandbox not found")
					}
				}
				for _, scope := range s.scopes(a) {
					if scope.Limits != nil && scope.Limits.Concurrency != nil {
						count := s.chargedSessions(a.Principal)
						for _, ex := range s.journal.Execs {
							if ex.OwnerId == a.Principal && s.execCharged(ex) {
								count++
							}
						}
						if count >= *scope.Limits.Concurrency {
							return fail("SCOPE_DENIED", "native session exceeds execution concurrency scope")
						}
					}
				}
			}
			if parent := textField(in, "parent_session_id"); parent != "" {
				p := c.Sessions[parent]
				if p == nil || !s.own(a, textField(p.Resource, "owner_id")) || !s.sessionAllowed(a, p.Resource, "session.get") {
					return fail("NOT_FOUND", "parent session not found")
				}
			}
			if len(c.Sessions) >= 1024 {
				return fail("CONTROL_QUOTA_EXCEEDED", "session retention limit reached")
			}
			m = newResource("session", a.Principal)
			m["id"] = s.resourceID("se")
			m["state"], m["status"], m["observation_state"] = "starting", "idle", "unknown"
			m["sandbox_id"], m["harness_id"], m["auth_mode"], m["agent_mode"] = sbID, textField(in, "harness_id"), auth, "inside"
			if mode := textField(in, "agent_mode"); mode != "" {
				m["agent_mode"] = mode
			}
			if m["agent_mode"] == "outside" && textField(location, "kind") != "host" {
				return fail("INVALID_ARGUMENT", "outside harnesses require an enrolled host location")
			}
			m["location"], m["location_epoch"] = location, json.Number("1")
			for _, field := range []string{"parent_session_id", "task_id"} {
				if v := textField(in, field); v != "" {
					m[field] = v
				}
			}
			if !s.sessionAllowed(a, m, "session.get") {
				return fail("SCOPE_DENIED", "new session is outside key selectors")
			}
			work = &managedSession{Resource: m, Next: map[string]uint64{}, Frames: []api.Frame{}, Pending: []sessionWork{{ID: newID("dispatch"), Action: "start"}}}
			if prompt := textField(in, "prompt"); prompt != "" {
				if !s.can(a, "session.input") || len(prompt) > 32768 {
					return fail("SCOPE_DENIED", "initial prompt requires session.input authority and at most 32768 bytes")
				}
				work.Pending = append(work.Pending, sessionWork{ID: newID("dispatch"), Action: "input", Lease: "initial-prompt", Data: base64.StdEncoding.EncodeToString([]byte(prompt))})
			}
			c.Sessions[textField(m, "id")] = work
			result["session_id"], result["sandbox_id"] = m["id"], sbID
			break
		}
		work = c.Sessions[textField(in, "session_id")]
		if work == nil || !s.sessionAllowed(a, work.Resource, "session.get") {
			return fail("NOT_FOUND", "session not found")
		}
		m = work.Resource
		if uintField(m, "revision") != uintField(in, "expected_revision") {
			return fail("REVISION_CONFLICT", "session revision changed")
		}
		owner := s.own(a, textField(m, "owner_id"))
		if verb != "attach" && verb != "input" && verb != "resize" && !owner {
			return fail("SCOPE_DENIED", "session owner authority required")
		}
		if verb != "end" && verb != "delete" && textField(m, "state") != "active" {
			return fail("RESOURCE_IN_USE", "session is not active")
		}
		switch verb {
		case "attach", "input", "resize":
			if work.GitBusy {
				return fail("RESOURCE_IN_USE", "git changes are being applied")
			}
			lease := c.InputLeases[textField(in, "lease_id")]
			if !s.sessionAllowed(a, m, "session.input") || lease == nil || textField(lease, "holder_id") != a.Principal || textField(lease, "resource_id") != textField(m, "id") || textField(lease, "state") != "active" || sessionExpired(lease) || uintField(lease, "location_epoch") != uintField(m, "location_epoch") {
				return fail("SCOPE_DENIED", "a live input lease and explicit session.input grant are required")
			}
			if verb == "input" {
				if uintField(in, "location_epoch") != uintField(m, "location_epoch") {
					return fail("REVISION_CONFLICT", "session location epoch changed")
				}
				data, err := base64.StdEncoding.DecodeString(textField(in, "data_base64"))
				if err != nil || len(data) == 0 || len(data) > 32768 {
					return fail("INVALID_ARGUMENT", "input must contain 1–32768 base64 encoded bytes")
				}
				leaseID := textField(lease, "id")
				sequence := uintField(in, "sequence")
				if sequence != work.Next[leaseID] {
					return fail("REVISION_CONFLICT", "input sequence must equal the next lease sequence")
				}
				if len(work.Pending) >= 64 {
					return fail("CONTROL_QUOTA_EXCEEDED", "session input queue is full")
				}
				work.Pending = append(work.Pending, sessionWork{ID: newID("dispatch"), Action: "input", Lease: leaseID, Sequence: sequence, Data: textField(in, "data_base64")})
				work.Next[leaseID]++
			}
			if verb == "resize" {
				rows, columns := uintField(in, "rows"), uintField(in, "columns")
				if rows < 1 || columns < 1 || rows > 1000 || columns > 1000 || len(work.Pending) >= 64 {
					return fail("INVALID_ARGUMENT", "terminal dimensions must be 1–1000 and queue available")
				}
				work.Pending = append(work.Pending, sessionWork{ID: newID("dispatch"), Action: "resize", Rows: rows, Columns: columns})
			}
			if verb == "attach" {
				result["lease_id"] = lease["id"]
			}
		case "update":
			if err := acceptFields(in, "session_id status status_reason"); err != nil {
				return api.ResourceRef{}, nil, nil, nil, err
			}
			for _, key := range []string{"status", "status_reason"} {
				if v, ok := in[key]; ok {
					m[key] = v
				}
			}
		case "end", "delete":
			if textField(m, "state") != "ended" {
				work.Cancelled = append(work.Cancelled, work.Pending...)
				work.Pending = []sessionWork{{ID: newID("dispatch"), Action: "end"}}
				work.Failure = ""
				m["status_reason"] = "end requested; awaiting runner confirmation"
			}
		default:
			return fail("AUTH_TRANSPORT_UNSUPPORTED", "this harness has no enrolled continuation or git handoff adapter")
		}
		editResource(m)
	case "lease":
		if verb == "create" {
			work = c.Sessions[textField(in, "resource_id")]
			if work == nil || !s.sessionAllowed(a, work.Resource, "session.get") {
				return fail("NOT_FOUND", "session not found")
			}
			if textField(in, "type") != "input" || !s.sessionAllowed(a, work.Resource, "session.input") {
				return fail("SCOPE_DENIED", "explicit session.input grant required")
			}
			if textField(work.Resource, "state") != "active" {
				return fail("RESOURCE_IN_USE", "session is not active")
			}
			if work.Observed {
				return fail("AUTH_TRANSPORT_UNSUPPORTED", "native sub-agents are driven by their parent harness; send input to the parent session")
			}
			if holder := textField(in, "holder_id"); holder != "" && holder != a.Principal {
				return fail("SCOPE_DENIED", "input leases are acquired by their holder")
			}
			for _, old := range c.InputLeases {
				if textField(old, "resource_id") == textField(work.Resource, "id") && textField(old, "state") == "active" && !sessionExpired(old) {
					return fail("RESOURCE_IN_USE", "session already has an input writer")
				}
			}
			if len(c.InputLeases) >= 16384 {
				return fail("CONTROL_QUOTA_EXCEEDED", "input lease retention limit reached")
			}
			m = newResource("lease", a.Principal)
			m["id"] = s.resourceID("le")
			m["type"], m["resource_id"], m["holder_id"] = "input", work.Resource["id"], a.Principal
			m["location_epoch"], m["generation"] = work.Resource["location_epoch"], json.Number("1")
			c.InputLeases[textField(m, "id")] = m
			result["lease_id"] = m["id"]
		} else {
			m = c.InputLeases[textField(in, "lease_id")]
			if m == nil || textField(m, "holder_id") != a.Principal || !s.resourceAllowed(a, api.ResourceRef{Kind: "lease", Id: textField(m, "id")}) {
				return fail("NOT_FOUND", "input lease not found")
			}
			if uintField(m, "revision") != uintField(in, "expected_revision") {
				return fail("REVISION_CONFLICT", "lease revision changed")
			}
			if verb == "release" || verb == "delete" {
				m["state"] = "released"
			} else if verb != "renew" {
				return fail("INVALID_ARGUMENT", "unsupported input lease operation")
			}
			editResource(m)
		}
		if verb == "create" || verb == "renew" {
			session := c.Sessions[textField(m, "resource_id")]
			if session == nil || !s.sessionAllowed(a, session.Resource, "session.input") || textField(m, "state") != "active" || verb == "renew" && sessionExpired(m) {
				return fail("SCOPE_DENIED", "input authority expired")
			}
			expiry := textField(in, "expires_at")
			when, err := time.Parse(time.RFC3339Nano, expiry)
			if err != nil || !when.After(time.Now()) || when.After(time.Now().Add(5*time.Minute)) {
				return fail("INVALID_ARGUMENT", "input lease expiry must be within five minutes")
			}
			m["expires_at"] = expiry
		}
	case "share":
		if verb == "create" {
			work = c.Sessions[textField(in, "resource_id")]
			if textField(in, "resource_kind") != "session" || work == nil || !s.own(a, textField(work.Resource, "owner_id")) || !s.sessionAllowed(a, work.Resource, "session.get") {
				return fail("NOT_FOUND", "owned session not found")
			}
			if textField(work.Resource, "auth_mode") == "subscription" {
				return fail("CLEAN_REBUILD_REQUIRED", "subscription sessions cannot be shared through a general PTY")
			}
			subject := textField(in, "subject_id")
			if p, ok := c.Principals[subject]; !ok || p.State != "active" || value(p.Disabled) {
				return fail("NOT_FOUND", "share recipient not found")
			}
			actions, _ := in["actions"].([]any)
			if len(c.SessionShares) >= 16384 {
				return fail("CONTROL_QUOTA_EXCEEDED", "session share retention limit reached")
			}
			watch := false
			for _, action := range actions {
				if action != "session.get" && action != "session.watch" && action != "session.input" {
					return fail("INVALID_ARGUMENT", "session shares grant only session.get, session.watch and session.input")
				}
				watch = watch || action == "session.get"
			}
			if !watch {
				return fail("INVALID_ARGUMENT", "a session share must explicitly include session.get")
			}
			m = newResource("share", a.Principal)
			m["id"] = s.resourceID("sh")
			m["resource_kind"], m["resource_id"], m["subject_id"], m["actions"] = "session", work.Resource["id"], subject, actions
			if expiry := textField(in, "expires_at"); expiry != "" {
				when, err := time.Parse(time.RFC3339Nano, expiry)
				if err != nil || !when.After(time.Now()) {
					return fail("INVALID_ARGUMENT", "share expiry must be in the future")
				}
				m["expires_at"] = expiry
			}
			c.SessionShares[textField(m, "id")] = m
			result["share_id"] = m["id"]
		} else {
			m = c.SessionShares[textField(in, "share_id")]
			if m == nil || !s.own(a, textField(m, "owner_id")) {
				return fail("NOT_FOUND", "share not found")
			}
			if !s.resourceAllowed(a, api.ResourceRef{Kind: "share", Id: textField(m, "id")}) {
				return fail("SCOPE_DENIED", "share is outside key selectors")
			}
			if uintField(m, "revision") != uintField(in, "expected_revision") {
				return fail("REVISION_CONFLICT", "share revision changed")
			}
			if verb != "revoke" && verb != "delete" {
				return fail("INVALID_ARGUMENT", "session shares must be revoked and re-created to change grants")
			}
			m["state"] = "revoked"
			editResource(m)
			for _, lease := range c.InputLeases {
				if lease["resource_id"] == m["resource_id"] && lease["holder_id"] == m["subject_id"] {
					lease["state"] = "revoked"
					editResource(lease)
				}
			}
		}
	}
	if m == nil {
		return fail("NOT_FOUND", "session operation not installed")
	}
	return api.ResourceRef{Kind: api.ResourceKind(kind), Id: textField(m, "id")}, result, nil, nil, nil
}

func (s *Service) watchSession(w http.ResponseWriter, r *http.Request, id string, in map[string]any) {
	a := requestCaller(r)
	limit := uintField(in, "limit")
	if limit == 0 {
		limit = 100
	}
	wait := uintField(in, "wait_ms")
	if limit > 1000 || wait > 25000 {
		writeError(w, id, "INVALID_ARGUMENT", "watch limit 1000; wait_ms 25000", nil)
		return
	}
	sessionID := textField(in, "session_id")
	position := uint64(0)
	deadline := time.NewTimer(time.Duration(wait) * time.Millisecond)
	defer deadline.Stop()
	for {
		s.readLock()
		work := s.journal.Control.Sessions[sessionID]
		if s.broken || !s.can(a, "session.watch") || work == nil || !s.sessionAllowed(a, work.Resource, "session.watch") {
			s.mu.Unlock()
			writeError(w, id, "NOT_FOUND", "session not found", nil)
			return
		}
		if cursor := textField(in, "cursor"); cursor != "" {
			raw, err := s.readCursor(a, "session:"+sessionID, cursor)
			if err != nil {
				s.mu.Unlock()
				writeError(w, id, "CURSOR_EXPIRED", "invalid session cursor", nil)
				return
			}
			n, err := strconv.ParseUint(raw, 10, 64)
			if err != nil || n > uint64(len(work.Frames)) {
				s.mu.Unlock()
				writeError(w, id, "CURSOR_EXPIRED", "session cursor is unavailable", nil)
				return
			}
			position = n
		}
		end := position + limit
		if end > uint64(len(work.Frames)) {
			end = uint64(len(work.Frames))
		}
		frames := append([]api.Frame{}, work.Frames[position:end]...)
		next := s.cursor(a, "session:"+sessionID, strconv.FormatUint(end, 10))
		var cursor *string
		if end < uint64(len(work.Frames)) || work.More || textField(work.Resource, "state") != "ended" && textField(work.Resource, "state") != "failed" {
			cursor = &next
		}
		changed := s.changed
		s.mu.Unlock()
		send := func() {
			prepareJSON(w, 200, map[string]any{"items": frames, "next_cursor": cursor, "request_id": id})()
		}
		if len(frames) > 0 || cursor == nil || wait == 0 {
			send()
			return
		}
		select {
		case <-changed:
		case <-deadline.C:
			send()
			return
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Service) driveSessions() {
	defer s.wg.Done()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-s.context().Done():
			return
		case <-ticker.C:
		}
		s.readLock()
		for id, runtime := range s.agentRuntimes {
			session := s.journal.Control.Sessions[id]
			if session == nil || textField(session.Resource, "state") == "ended" || textField(session.Resource, "state") == "failed" {
				runtime.cancel()
				delete(s.agentRuntimes, id)
			}
		}
		ids := []string{}
		for id, work := range s.journal.Control.Sessions {
			if s.fleetEnabled && (s.fleet == nil || resourceHome(id) != s.hostID() || !s.fleet.leaseValid()) {
				continue
			}
			if !work.Observed && work.Failure == "" && (textField(work.Resource, "state") == "active" || textField(work.Resource, "state") == "starting" || textField(work.Resource, "state") == "moving" || len(work.Pending) > 0 || work.More) {
				ids = append(ids, id)
			}
		}
		s.mu.Unlock()
		for _, id := range ids {
			s.driveSession(id)
		}
		s.readLock()
		transfers := []string{}
		for id, transfer := range s.journal.Control.SessionTransfers {
			if transfer.Phase != "done" && transfer.OperationID != "" && (!s.fleetEnabled || s.fleet != nil && resourceHome(id) == s.hostID() && s.fleet.leaseValid()) {
				transfers = append(transfers, id)
			}
		}
		s.mu.Unlock()
		for _, id := range transfers {
			s.driveSessionTransfer(id)
		}
	}
}

func (s *Service) wakeSessionInput(ctx context.Context, a caller, in map[string]any) error {
	s.readLock()
	work := s.journal.Control.Sessions[textField(in, "session_id")]
	lease := s.journal.Control.InputLeases[textField(in, "lease_id")]
	if work == nil || lease == nil || !s.can(a, "session.input") || !s.sessionAllowed(a, work.Resource, "session.input") ||
		textField(work.Resource, "state") != "active" || work.GitBusy ||
		uintField(work.Resource, "revision") != uintField(in, "expected_revision") ||
		textField(lease, "holder_id") != a.Principal || textField(lease, "resource_id") != textField(work.Resource, "id") ||
		textField(lease, "state") != "active" || sessionExpired(lease) ||
		uintField(lease, "location_epoch") != uintField(work.Resource, "location_epoch") ||
		uintField(in, "location_epoch") != uintField(work.Resource, "location_epoch") ||
		uintField(in, "sequence") != work.Next[textField(lease, "id")] {
		s.mu.Unlock()
		return nil
	}
	data, err := base64.StdEncoding.DecodeString(textField(in, "data_base64"))
	if err != nil || len(data) == 0 || len(data) > 32768 || len(work.Pending) >= 64 {
		s.mu.Unlock()
		return nil
	}
	id, owner := textField(work.Resource, "sandbox_id"), textField(work.Resource, "owner_id")
	if sb, exists := s.journal.Sandboxes[id]; exists && s.sandboxMustStop(sb) {
		s.mu.Unlock()
		return reject("RESOURCE_IN_USE", "session sandbox authority or budget has ended")
	}
	s.mu.Unlock()
	if id == "" {
		return nil
	}
	return s.Wake(ctx, caller{Principal: owner}, id)
}

func (s *Service) sessionSandboxBusy(id string) bool {
	for _, work := range s.journal.Control.Sessions {
		if textField(work.Resource, "sandbox_id") == id && textField(work.Resource, "state") == "active" &&
			(len(work.Pending) > 0 || textField(work.Resource, "status") == "working") {
			return true
		}
	}
	for _, transfer := range s.journal.Control.SessionTransfers {
		if transfer.Phase != "done" && (textField(transfer.Source, "sandbox_id") == id || textField(transfer.Target, "sandbox_id") == id) {
			return true
		}
	}
	return false
}

func (s *Service) driveSession(id string) {
	s.readLock()
	if s.broken {
		s.mu.Unlock()
		return
	}
	if s.fleetEnabled && (s.fleet == nil || resourceHome(id) != s.hostID() || !s.fleet.leaseValid()) {
		s.mu.Unlock()
		return
	}
	work := s.journal.Control.Sessions[id]
	if transfer := s.journal.Control.SessionTransfers[id]; transfer != nil && transfer.Kind == "spawn" && transfer.Phase != "start" && transfer.Phase != "done" {
		s.mu.Unlock()
		return
	}
	m := work.Resource
	action := sessionWork{Action: "read"}
	if len(work.Pending) > 0 {
		action = work.Pending[0]
	}
	location := object(m["location"])
	harness := s.sessions.Harnesses[textField(m, "harness_id")]
	host, enrolled := s.sessions.Hosts[textField(location, "host_id")]
	native := textField(location, "kind") == "host"
	sb := s.journal.Sandboxes[textField(m, "sandbox_id")]
	owner := textField(m, "owner_id")
	offset, epoch := work.Offset, uintField(m, "location_epoch")
	moving := textField(m, "state") == "moving"
	if transfer := s.journal.Control.SessionTransfers[id]; transfer != nil && moving {
		epoch = transfer.SourceEpoch
	}
	eventOffset := work.EventOffset
	if harness.OwnerID != owner || native && (!enrolled || host.OwnerID != owner) {
		s.mu.Unlock()
		s.failSession(id, "enrolled runner is unavailable")
		return
	}
	if !native && (sb.State == "suspended" || sb.State == "suspending" || sb.State == "resuming") {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	request := object(action)
	request["id"], request["epoch"], request["offset"] = id, epoch, offset
	request["event_offset"] = eventOffset
	if action.Action == "input" {
		request["sequence"] = action.Sequence
	}
	request["root"] = "/tmp/lamp-sessions"
	if native {
		request["root"] = host.Root
	}
	if action.Action == "start" {
		request["argv"], request["cwd"], request["env"] = harness.Argv, harness.Cwd, harness.Env
		request["repository"] = harness.Repository
		if harness.Native != nil {
			config := object(harness.Native)
			config["auth"] = textField(m, "auth_mode")
			config["agent_mode"] = textField(m, "agent_mode")
			config["session_id"], config["sandbox_id"] = id, textField(m, "sandbox_id")
			config["location_kind"] = textField(location, "kind")
			if profile, exists := harness.Native.Profiles[textField(location, "kind")+":"+textField(location, "host_id")]; exists {
				for key, value := range object(profile) {
					config[key] = value
				}
			}
			request["native"], request["native_source"] = config, nativeHarness
		}
	}
	ctx, cancel := context.WithTimeout(s.context(), 30*time.Second)
	defer cancel()
	if !native && harness.Native != nil && harness.Native.Managed {
		config, _ := request["native"].(map[string]any)
		if config == nil {
			config = map[string]any{}
		}
		if err := s.prepareAgent(ctx, id, sb, config); err != nil {
			s.failSession(id, err.Error())
			return
		}
	}
	raw, _ := json.Marshal(request)
	encoded := base64.StdEncoding.EncodeToString(raw)
	var reply map[string]any
	var err error
	if native && host.Socket != "" {
		var connection net.Conn
		connection, err = (&net.Dialer{}).DialContext(ctx, "unix", host.Socket)
		if err == nil {
			connection.SetDeadline(time.Now().Add(10 * time.Second))
			err = json.NewEncoder(connection).Encode(request)
			if err == nil {
				decoder := json.NewDecoder(io.LimitReader(connection, 128<<10))
				decoder.UseNumber()
				err = decoder.Decode(&reply)
			}
			connection.Close()
		}
	} else if native {
		argv := append(append([]string{}, host.Command[1:]...), "-c", sessionRunner, encoded)
		var data []byte
		data, err = exec.CommandContext(ctx, host.Command[0], argv...).Output()
		if err == nil {
			reply, err = surface.Decode(data)
		}
	} else {
		var data []byte
		data, err = s.sessionGuestCall(ctx, sb, []string{"python3", "-c", sessionRunner, encoded})
		if err == nil {
			reply, err = surface.Decode(data)
		}
	}
	if errors.Is(err, errSessionPaused) {
		return
	}
	if err != nil || reply["error"] != nil {
		if s.context().Err() != nil {
			return
		}
		s.failSessionAt(id, "runner unavailable; acknowledged pending input retained, no automatic restart", location, epoch)
		return
	}
	s.readLock()
	defer s.mu.Unlock()
	work = s.journal.Control.Sessions[id]
	if work == nil || !s.sessionReplyCurrent(id, work.Resource, location, epoch) {
		return
	}
	before := &controlState{}
	raw, _ = json.Marshal(s.journal.Control)
	json.Unmarshal(raw, before)
	m = work.Resource
	moving = textField(m, "state") == "moving"
	data, err := base64.StdEncoding.DecodeString(textField(reply, "data"))
	if err != nil || uintField(reply, "offset") != work.Offset+uint64(len(data)) {
		work.Failure = "invalid runner output offset"
	} else if work.Bytes+uint64(len(data)) > 16<<20 {
		work.Failure = "session output retention quota exceeded"
	} else {
		if len(data) > 0 {
			work.Frames = append(work.Frames, api.Frame{StreamId: id, Sequence: uint64(len(work.Frames)), Channel: "stdout", DataBase64: textField(reply, "data"), ByteOffset: strconv.FormatUint(work.Offset, 10), LocationEpoch: epoch})
			work.Offset = uintField(reply, "offset")
			work.Bytes += uint64(len(data))
		}
		work.More = work.Offset < uintField(reply, "size")
		if events, ok := reply["events"].([]any); ok {
			for _, value := range events {
				event := object(value)
				raw, _ := json.Marshal(event)
				work.Frames = append(work.Frames, api.Frame{StreamId: id, Sequence: uint64(len(work.Frames)), Channel: "control", DataBase64: base64.StdEncoding.EncodeToString(raw), ByteOffset: strconv.FormatUint(work.Offset, 10), LocationEpoch: epoch})
				if textField(event, "type") == "status" {
					m["status"] = textField(event, "status")
					editResource(m)
				}
				if ref := textField(event, "continuation_ref"); ref != "" {
					m["continuation_ref"] = ref
				}
				if textField(event, "type") == "native_subagent" {
					s.observeSubagent(id, work, event, epoch)
				}
			}
		}
		work.EventOffset = uintField(reply, "event_offset")
		if action.Action != "read" && reply["input_delivery"] != "pending" && len(work.Pending) > 0 && work.Pending[0].ID == action.ID {
			work.Pending = work.Pending[1:]
		}
		state := textField(reply, "state")
		if action.Action == "end" {
			state = "ended"
		}
		if moving && (state == "active" || state == "fenced") {
			m["observation_state"] = "fresh"
		} else if state != "active" && state != "ended" && state != "failed" {
			work.Failure = "invalid runner state"
		} else {
			changed := textField(m, "state") != state || textField(m, "observation_state") != "fresh"
			m["state"], m["observation_state"] = state, "fresh"
			if state != "active" {
				for _, childID := range work.Children {
					if child := s.journal.Control.Sessions[childID]; child != nil && textField(child.Resource, "state") == "active" {
						child.Resource["state"], child.Resource["status"] = "ended", "idle"
						editResource(child.Resource)
					}
				}
			}
			if changed {
				m["observed_at"] = timestamp()
				editResource(m)
			}
		}
	}
	if work.Failure != "" {
		m["state"], m["observation_state"], m["status_reason"] = "failed", "unknown", work.Failure
		editResource(m)
	}
	if len(data) > 0 || work.EventOffset != eventOffset || action.Action != "read" || textField(m, "state") != "active" {
		if err := s.save("$control"); err != nil {
			if errors.Is(err, errAuthorityConflict) {
				s.journal.Control = before
			} else {
				s.broken = true
			}
		}
	}
}

func (s *Service) failSession(id, reason string) {
	s.failSessionAt(id, reason, nil, 0)
}

func (s *Service) sessionReplyCurrent(id string, resource, location map[string]any, epoch uint64) bool {
	if state := textField(resource, "state"); state == "ended" || state == "failed" {
		return false
	}
	currentEpoch := uintField(resource, "location_epoch")
	if textField(resource, "state") == "moving" {
		if transfer := s.journal.Control.SessionTransfers[id]; transfer != nil {
			currentEpoch = transfer.SourceEpoch
		}
	}
	return currentEpoch == epoch && digest(object(resource["location"])) == digest(location)
}

func (s *Service) failSessionAt(id, reason string, location map[string]any, epoch uint64) {
	s.readLock()
	defer s.mu.Unlock()
	work := s.journal.Control.Sessions[id]
	if work == nil || location != nil && !s.sessionReplyCurrent(id, work.Resource, location, epoch) {
		return
	}
	before := &controlState{}
	raw, _ := json.Marshal(s.journal.Control)
	json.Unmarshal(raw, before)
	work.Failure = reason
	work.Resource["observation_state"], work.Resource["status_reason"] = "unknown", reason
	work.Resource["state"] = "failed"
	editResource(work.Resource)
	if err := s.save("$control"); err != nil {
		if errors.Is(err, errAuthorityConflict) {
			s.journal.Control = before
		} else {
			s.broken = true
		}
	}
}

var errSessionPaused = errors.New("session sandbox is paused")

func (s *Service) sessionGuestCall(ctx context.Context, sb api.Sandbox, argv []string) ([]byte, error) {
	if len(argv) != 4 || argv[0] != "python3" || argv[1] != "-c" || argv[2] != sessionRunner {
		return nil, errors.New("invalid session relay command")
	}
	data, err := base64.StdEncoding.DecodeString(argv[3])
	if err != nil {
		return nil, err
	}
	s.readLock()
	current, exists := s.journal.Sandboxes[sb.Id]
	if !exists || current.State != "ready" || s.busy[sb.Id] {
		s.mu.Unlock()
		return nil, errSessionPaused
	}
	s.starting[sb.Id]++
	s.mu.Unlock()
	defer func() {
		s.readLock()
		s.starting[sb.Id]--
		s.mu.Unlock()
	}()
	deadline, _ := ctx.Deadline()
	r := &pb.Request{Context: &pb.Context{RequestId: newID("req"), OperationId: newID("session"), DeadlineMs: uint64(deadline.UnixMilli()), SandboxId: sb.Id, Generation: value(sb.Generation), GrantId: s.worker.config.GrantID, PolicyRevision: 1}}
	r.Command = &pb.Request_SessionCall{SessionCall: &pb.SessionCall{RequestJson: data, BridgeSource: sessionRunner}}
	reply, err := s.worker.call(ctx, r)
	if err != nil {
		return nil, err
	}
	if reply.State != "session_reply" {
		return nil, errors.New("session relay did not reply")
	}
	return []byte(reply.Detail["response_json"]), nil
}

// observeSubagent records a harness's own sub-agent as a separate child
// session. Its output is what the parent harness reported for that sub-agent.
func (s *Service) observeSubagent(parentID string, parent *managedSession, event map[string]any, epoch uint64) {
	native := textField(event, "native_id")
	if native == "" {
		return
	}
	if parent.Children == nil {
		parent.Children = map[string]string{}
	}
	sessions := s.journal.Control.Sessions
	child := sessions[parent.Children[native]]
	if child == nil {
		if len(sessions) >= 1024 || len(parent.Children) >= 64 {
			return
		}
		p := parent.Resource
		m := newResource("session", textField(p, "owner_id"))
		m["id"] = s.resourceID("se")
		for _, field := range []string{"sandbox_id", "harness_id", "auth_mode", "task_id", "location", "location_epoch"} {
			if v, ok := p[field]; ok {
				m[field] = v
			}
		}
		m["parent_session_id"], m["agent_mode"], m["continuation_ref"] = parentID, "inside", native
		m["state"], m["observation_state"], m["observed_at"] = "active", "fresh", timestamp()
		m["labels"] = map[string]any{"lamp.native_subagent": "true", "lamp.native_label": textField(event, "label")}
		child = &managedSession{Resource: m, Next: map[string]uint64{}, Observed: true}
		id := textField(m, "id")
		sessions[id], parent.Children[native] = child, id
	}
	m := child.Resource
	if text := textField(event, "text"); text != "" && child.Bytes < 1<<20 {
		data := []byte(text + "\n")
		child.Frames = append(child.Frames, api.Frame{StreamId: textField(m, "id"), Sequence: uint64(len(child.Frames)), Channel: "stdout", DataBase64: base64.StdEncoding.EncodeToString(data), ByteOffset: strconv.FormatUint(child.Offset, 10), LocationEpoch: epoch})
		child.Offset += uint64(len(data))
		child.Bytes += uint64(len(data))
	}
	if textField(m, "state") == "ended" {
		editResource(m)
		return
	}
	m["status"] = "working"
	if textField(event, "status") == "completed" {
		m["status"], m["state"] = "idle", "ended"
	}
	editResource(m)
}
