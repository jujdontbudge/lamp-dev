package control

// Release agent enrollment. Upstream keys live only under the private control
// directory. The guest receives a revocable transport capability, never a vendor
// key. Runtime capabilities are deliberately absent from durable captures.
import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"lamp.eilmer.si/internal/broker"
	"lamp.eilmer.si/internal/surface"
	"lamp.eilmer.si/pkg/api"
)

//go:embed agent_cli.py
var agentCLI string

var agentEnvName = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,79}$`)

var agentName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,79}$`)

type agentCallerKey struct{}

type agentEnrollment struct {
	Model       string   `json:"model"`
	Source      string   `json:"source,omitempty"`
	Nested      bool     `json:"nested,omitempty"`
	Settings    []string `json:"settings,omitempty"`
	Connections []string `json:"connections,omitempty"`
	Secrets     []string `json:"secrets,omitempty"`
}

type agentRuntime struct {
	mu          sync.Mutex
	sandbox     string
	generation  uint64
	session     string
	token       string
	endpoint    string
	broker      *broker.Broker
	grant       string
	brokerToken string
	cancel      context.CancelFunc
	children    map[string]bool
	enrollment  agentEnrollment
	operations  map[string][]string
}

func init() {
	for _, kind := range []string{"credential_source", "settings_source", "auth_flow", "identity_provider"} {
		lifeKinds[kind] = true
		for _, verb := range []string{"get", "list"} {
			surface.CoreNames[kind+"_"+verb] = true
		}
	}
	for _, kind := range []string{"credential_source", "settings_source"} {
		for _, verb := range []string{"create", "update", "delete"} {
			k, v := kind, verb
			name := k + "_" + v
			surface.CoreNames[name] = true
			lifeHandlers[name] = func(s *Service, a caller, in map[string]any) (lifePlan, error) {
				return s.planAgentResource(a, k, v, in)
			}
		}
	}
	surface.CoreNames["credential_source_revoke"] = true
	lifeHandlers["credential_source_revoke"] = func(s *Service, a caller, in map[string]any) (lifePlan, error) {
		return s.planAgentResource(a, "credential_source", "delete", in)
	}
	surface.CoreNames["harness_enroll"] = true
	lifeHandlers["harness_enroll"] = (*Service).planHarnessEnroll
	surface.CoreNames["secret_set"] = true
}

func releaseHarness(owner string, e agentEnrollment) SessionHarness {
	return SessionHarness{OwnerID: owner, Argv: []string{"/usr/local/bin/opencode"}, Cwd: "{session_dir}/workspace", Env: map[string]string{"HOME": "{session_dir}/home", "PATH": "/usr/local/bin:/opt/venv/bin:/usr/bin:/bin"}, AuthModes: []string{"none", "api_key"}, Native: &NativeHarness{Kind: "opencode", Binary: "/usr/local/bin/opencode", Model: e.Model, Managed: true}}
}
func (s *Service) initAgents() error {
	s.agentRuntimes = map[string]*agentRuntime{}
	s.agentEntries = make(chan struct{}, 16)
	if s.life.Agents == nil {
		s.life.Agents = map[string]agentEnrollment{}
	}
	if s.sessions.Harnesses == nil {
		s.sessions.Harnesses = map[string]SessionHarness{}
	}
	if _, exists := s.sessions.Harnesses["opencode"]; !exists {
		s.sessions.Harnesses["opencode"] = releaseHarness(s.journal.Control.OwnerID, agentEnrollment{Model: "opencode/deepseek-v4.1-flash-free"})
	}
	for id, e := range s.life.Agents {
		s.sessions.Harnesses[id] = releaseHarness(s.journal.Control.OwnerID, e)
	}
	n := s.addNetwork("ip", "identity_provider", s.journal.Control.OwnerID)
	delete(s.life.Networks, textField(n.R, "id"))
	n.R["id"] = "openrouter"
	n.R["type"] = "oidc"
	n.R["issuer"] = "https://openrouter.ai"
	n.R["enabled"] = true
	s.life.Networks["openrouter"] = n
	return PrivateDir(filepath.Join(s.dir, "credentials"))
}
func (s *Service) agentOwner(a caller) bool {
	return a.KeyID == "" && a.Principal == s.journal.Control.OwnerID && s.current(a)
}

func (s *Service) planHarnessEnroll(a caller, in map[string]any) (lifePlan, error) {
	if !s.agentOwner(a) {
		return lifePlan{}, reject("SCOPE_DENIED", "harness enrollment requires the owner credential")
	}
	if textField(in, "harness_id") != "opencode" {
		return lifePlan{}, reject("AUTH_TRANSPORT_UNSUPPORTED", "release enrollment currently supports opencode; other harnesses require --sessions-config")
	}
	e := agentEnrollment{Model: textField(in, "model"), Source: textField(in, "credential_source_id")}
	e.Nested, _ = in["nested_create"].(bool)
	agentAssign(in["settings_source_ids"], &e.Settings)
	agentAssign(in["connection_ids"], &e.Connections)
	agentAssign(in["secret_names"], &e.Secrets)
	for _, name := range e.Secrets {
		if !agentEnvName.MatchString(name) || name == "PATH" || name == "HOME" || strings.HasPrefix(name, "LAMP_") || strings.HasPrefix(name, "LD_") || strings.HasPrefix(name, "DYLD_") {
			return lifePlan{}, reject("INVALID_ARGUMENT", "secret placeholders require an uppercase application environment name")
		}
	}
	if e.Model == "" {
		e.Model = "opencode/deepseek-v4.1-flash-free"
	}
	if e.Source != "" {
		n := s.life.Networks[e.Source]
		if n == nil || textField(n.R, "kind") != "credential_source" || textField(n.R, "state") != "ready" {
			return lifePlan{}, reject("NOT_FOUND", "ready credential source required")
		}
		if textField(n.R, "provider") != "openrouter" {
			return lifePlan{}, reject("AUTH_TRANSPORT_UNSUPPORTED", "opencode enrollment supports OpenRouter sources")
		}
		if !strings.HasSuffix(e.Model, ":free") {
			return lifePlan{}, reject("SCOPE_DENIED", "OpenRouter enrollment requires an explicit :free model")
		}
	}
	for _, id := range append(append([]string{}, e.Settings...), e.Connections...) {
		n := s.life.Networks[id]
		if n == nil || !s.lifeOwned(a, textField(n.R, "kind"), n.R) || textField(n.R, "state") == "revoked" {
			return lifePlan{}, reject("NOT_FOUND", "enrolled settings source or connection not found")
		}
	}
	s.life.Agents["opencode"] = e
	s.sessions.Harnesses["opencode"] = releaseHarness(a.Principal, e)
	return lifePlan{ref: lifeRef("harness", "opencode"), result: map[string]any{"harness_id": "opencode"}}, nil
}

func (s *Service) planAgentResource(a caller, kind, verb string, in map[string]any) (lifePlan, error) {
	if !s.agentOwner(a) {
		return lifePlan{}, reject("SCOPE_DENIED", "host credential and settings enrollment requires the owner credential")
	}
	var n *lifeNetwork
	if verb == "create" {
		if len(s.life.Networks) >= 4096 {
			return lifePlan{}, reject("CONTROL_QUOTA_EXCEEDED", "agent resource limit reached")
		}
		if kind == "credential_source" {
			if textField(in, "provider") != "openrouter" || !agentName.MatchString(textField(in, "account_ref")) {
				return lifePlan{}, reject("INVALID_ARGUMENT", "use provider=openrouter and the name entered with lamp secret set as account_ref")
			}
			if _, err := s.readAgentSecret(textField(in, "account_ref")); err != nil {
				return lifePlan{}, err
			}
		} else {
			root := textField(in, "source_ref")
			var paths []string
			agentAssign(in["path_allowlist"], &paths)
			cfg := broker.Config{Settings: map[string]broker.SettingsSource{"source": {Root: root, Paths: paths}}}
			if cfg.Validate() != nil {
				return lifePlan{}, reject("INVALID_ARGUMENT", "settings require an absolute source_ref directory and relative path_allowlist")
			}
		}
		n = s.addNetwork(kind, kind, a.Principal)
	} else {
		n = s.life.Networks[textField(in, kind+"_id")]
		if n == nil || textField(n.R, "kind") != kind || !s.lifeOwned(a, kind, n.R) {
			return lifePlan{}, reject("NOT_FOUND", kind+" not found")
		}
		if err := lifeRevision(n.R, in); err != nil {
			return lifePlan{}, err
		}
		if verb == "update" {
			return lifePlan{}, reject("INVALID_ARGUMENT", "delete and recreate an enrolled source to change its custody or paths")
		}
	}
	if verb == "delete" {
		n.R["state"] = "revoked"
		if kind == "settings_source" {
			n.R["state"] = "deleted"
		}
	} else {
		for _, k := range []string{"provider", "account_ref", "source_ref", "host_id", "path_allowlist", "categories", "labels"} {
			if v, ok := in[k]; ok {
				n.R[k] = v
			}
		}
		n.R["state"] = "ready"
		if kind == "settings_source" {
			n.R["state"] = "active"
		}
	}
	editResource(n.R)
	id := textField(n.R, "id")
	return lifePlan{ref: lifeRef(kind, id), result: map[string]any{kind + "_id": id}}, nil
}

func (s *Service) readAgentSecret(name string) ([]byte, error) {
	if !agentName.MatchString(name) {
		return nil, reject("INVALID_ARGUMENT", "invalid secret name")
	}
	p := filepath.Join(s.dir, "credentials", name)
	st, err := os.Lstat(p)
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || st.Size() > 16384 {
		return nil, reject("DATA_UNAVAILABLE", "private credential unavailable; use lamp secret set")
	}
	raw, err := os.ReadFile(p)
	if err != nil || len(bytes.TrimSpace(raw)) < 8 {
		return nil, reject("DATA_UNAVAILABLE", "credential unavailable")
	}
	return bytes.TrimSpace(raw), nil
}

// Secret values bypass both the operation journal and request hash inputs.
func (s *Service) secretSetHTTP(w http.ResponseWriter, r *http.Request, id string) {
	a := requestCaller(r)
	s.readLock()
	allowed := s.agentOwner(a)
	s.mu.Unlock()
	if !allowed {
		writeError(w, id, "SCOPE_DENIED", "secret entry requires the owner credential", nil)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 20000))
	if err != nil {
		writeError(w, id, "INVALID_ARGUMENT", "secret exceeds limit", nil)
		return
	}
	in, err := surface.Decode(raw)
	if err != nil || !agentName.MatchString(textField(in, "name")) || len(textField(in, "value")) < 8 || len(textField(in, "value")) > 16384 || strings.ContainsAny(textField(in, "value"), "\r\n\x00") {
		writeError(w, id, "INVALID_ARGUMENT", "use a name and a single-line secret of 8–16384 bytes", nil)
		return
	}
	var hosts []string
	agentAssign(in["hosts"], &hosts)
	if err = s.storeSecret(textField(in, "name"), textField(in, "value"), hosts); err != nil {
		writeError(w, id, "DATA_UNAVAILABLE", "cannot retain private secret", nil)
		return
	}

	prepareJSON(w, 200, map[string]any{"result": map[string]any{"name": in["name"]}, "request_id": id})()
}

func brokerOwner(b *broker.Broker, path string, in any) (map[string]any, error) {
	raw, _ := json.Marshal(in)
	r := httptest.NewRequest("POST", path, bytes.NewReader(raw))
	w := httptest.NewRecorder()
	b.OwnerHandler().ServeHTTP(w, r)
	var reply map[string]any
	json.Unmarshal(w.Body.Bytes(), &reply)
	if w.Code != 200 {
		return nil, reject("SCOPE_DENIED", "broker refused enrolled capability")
	}
	return reply, nil
}

func (s *Service) prepareAgent(ctx context.Context, id string, sb api.Sandbox, config map[string]any) error {
	runtimePath := filepath.Join(s.dir, "agent-runtimes", id+".json")
	var saved struct {
		Token      string
		Address    string
		Enrollment agentEnrollment
		Generation uint64
		Sandbox    string
		Children   map[string]bool
	}
	if raw, err := os.ReadFile(runtimePath); err == nil {
		if err = json.Unmarshal(raw, &saved); err != nil {
			return err
		}
		if saved.Generation != value(sb.Generation) || saved.Sandbox != sb.Id {
			return reject("SCOPE_DENIED", "agent transport belongs to a previous sandbox generation; start a fresh session")
		}
	}
	s.readLock()
	e := s.life.Agents["opencode"]
	if saved.Token != "" {
		e = saved.Enrollment
	}
	existing := s.agentRuntimes[id]
	s.mu.Unlock()
	if existing != nil {
		config["endpoint"] = existing.endpoint
		config["credential_file"] = "{session_dir}/broker.token"
		config["transport_token"] = existing.token
		return nil
	}
	if e.Source == "" && !e.Nested && len(e.Settings) == 0 && len(e.Connections) == 0 && len(e.Secrets) == 0 {
		return nil
	}
	cfg := broker.Config{AccountabilitySocket: s.agentSocket, Connections: map[string]broker.Connection{}, Settings: map[string]broker.SettingsSource{}}
	s.readLock()
	if e.Source != "" {
		n := s.life.Networks[e.Source]
		if n == nil || textField(n.R, "state") != "ready" {
			s.mu.Unlock()
			return reject("SCOPE_DENIED", "credential source was revoked")
		}
		fields := map[string]string{"model": "string", "messages": "array", "stream": "boolean?", "stream_options": "object?", "tools": "array?", "tool_choice": "string_or_object?", "temperature": "number?", "top_p": "number?", "max_tokens": "number?", "max_completion_tokens": "number?", "presence_penalty": "number?", "frequency_penalty": "number?", "stop": "array?", "seed": "number?", "response_format": "object?", "reasoning": "object?", "provider": "object?", "parallel_tool_calls": "boolean?"}
		cfg.Connections["model"] = broker.Connection{Kind: "model", Access: "full", Endpoint: "https://openrouter.ai/api/v1/chat/completions", CredentialCommand: []string{"/bin/cat", filepath.Join(s.dir, "credentials", textField(n.R, "account_ref"))}, Models: []string{e.Model}, FreeOnly: true, Operations: map[string]broker.Operation{"chat_completions": {Write: true, Fields: fields}}}
	}
	for _, sid := range e.Settings {
		n := s.life.Networks[sid]
		if n == nil || textField(n.R, "state") != "active" {
			s.mu.Unlock()
			return reject("NOT_FOUND", "settings source unavailable")
		}
		var paths []string
		agentAssign(n.R["path_allowlist"], &paths)
		cfg.Settings[sid] = broker.SettingsSource{Root: textField(n.R, "source_ref"), Paths: paths}
	}
	s.mu.Unlock()
	for _, cid := range e.Connections {
		s.readLock()
		n := s.life.Networks[cid]
		if n == nil || textField(n.R, "state") != "ready" {
			s.mu.Unlock()
			return reject("NOT_FOUND", "connection unavailable")
		}
		c, err := s.agentConnection(n.R)
		s.mu.Unlock()
		if err != nil {
			return err
		}
		cfg.Connections[cid] = c
	}
	b, err := broker.New(cfg, nil)
	if err != nil {
		return err
	}
	rt := &agentRuntime{sandbox: sb.Id, generation: value(sb.Generation), session: id, token: newID("transport"), broker: b, children: map[string]bool{}, enrollment: e}
	address := "127.0.0.1:0"
	if saved.Token != "" {
		address = saved.Address
		rt.token = saved.Token
		if saved.Children != nil {
			rt.children = saved.Children
		}
	}
	listener, err := net.Listen("tcp4", address)
	if err != nil {
		return err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	// Same alias is safe across isolated per-sandbox directors; port belongs to
	// this session only and the host service admits no other loopback port.
	alias := "198.19.250.43"
	rt.endpoint = fmt.Sprintf("http://%s:%d/v1/models/model/native", alias, port)
	rt.operations = map[string][]string{}
	for cid, c := range cfg.Connections {
		for name := range c.Operations {
			rt.operations[cid] = append(rt.operations[cid], name)
		}
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.agentProxy(rt, w, r) }), ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 16384}
	runtimeCtx, cancel := context.WithCancel(s.context())
	rt.cancel = cancel
	context.AfterFunc(runtimeCtx, func() { server.Close() })
	go server.Serve(listener)
	// A daemon restart rebinds the exact private listener. The running
	// generation's network director already owns its alias attachment; trying
	// to attach it again correctly refuses the duplicate destination.
	if saved.Token == "" {
		_, err = s.networkCall(ctx, sb.Id, value(sb.Generation), map[string]any{"operation": "host_service", "address": listener.Addr().String(), "alias": alias, "port": port, "access": "full", "connection_id": "agent_" + id, "expires_at": time.Now().Add(24 * time.Hour)})
		if err != nil {
			cancel()
			return err
		}
	}
	if err = PrivateDir(filepath.Dir(runtimePath)); err != nil {
		cancel()
		return err
	}
	saved.Token, saved.Address, saved.Enrollment, saved.Generation, saved.Sandbox = rt.token, listener.Addr().String(), e, rt.generation, sb.Id
	saved.Children = rt.children
	raw, _ := json.Marshal(saved)
	if err = os.WriteFile(runtimePath, raw, 0600); err != nil {
		cancel()
		return err
	}
	s.readLock()
	s.agentRuntimes[id] = rt
	s.mu.Unlock()
	config["endpoint"], config["credential_file"], config["transport_token"] = rt.endpoint, "{session_dir}/broker.token", rt.token
	config["model"] = e.Model
	config["guest_cli_source"] = agentCLI
	config["gateway"] = fmt.Sprintf("http://%s:%d", alias, port)
	tools := []map[string]any{}
	for cid, c := range cfg.Connections {
		if cid == "model" {
			continue
		}
		for op, spec := range c.Operations {
			props := map[string]any{}
			required := []string{}
			for name, typ := range spec.Fields {
				t := strings.TrimSuffix(typ, "?")
				props[name] = map[string]any{"type": t}
				if !strings.HasSuffix(typ, "?") {
					required = append(required, name)
				}
			}
			toolID := fmt.Sprintf("%x", sha256.Sum256([]byte(cid+":"+op)))
			label := op
			if len(label) > 32 {
				label = label[:32]
			}
			tools = append(tools, map[string]any{"name": label + "_" + toolID[:12], "description": "Owner-enrolled Lamp connection: " + op, "_meta": map[string]string{"lamp.connection_id": cid, "lamp.operation": op}, "inputSchema": map[string]any{"type": "object", "properties": props, "required": required}})
		}
	}
	config["gateway_tools"] = tools
	config["settings_sources"] = e.Settings
	placeholders := map[string]string{}
	for _, name := range e.Secrets {
		placeholders[name] = secretPlaceholder(rt, name)
	}
	config["secret_placeholders"] = placeholders
	if e.Source != "" {
		config["auth"] = "api_key"
	}
	return nil
}

func (s *Service) agentProxy(rt *agentRuntime, w http.ResponseWriter, r *http.Request) {
	s.readLock()
	sb, ok := s.journal.Sandboxes[rt.sandbox]
	session := s.journal.Control.Sessions[rt.session]
	valid := ok && sb.State == "ready" && value(sb.Generation) == rt.generation && session != nil && textField(session.Resource, "sandbox_id") == rt.sandbox && (textField(session.Resource, "state") == "active" || textField(session.Resource, "state") == "starting") && !s.sandboxMustStop(sb)
	if rt.enrollment.Source != "" {
		n := s.life.Networks[rt.enrollment.Source]
		valid = valid && n != nil && textField(n.R, "state") == "ready"
	}
	for _, rid := range append(append([]string{}, rt.enrollment.Connections...), rt.enrollment.Settings...) {
		n := s.life.Networks[rid]
		valid = valid && n != nil && textField(n.R, "state") != "revoked" && textField(n.R, "state") != "deleted"
	}
	s.mu.Unlock()
	if !valid || r.Header.Get("Authorization") != "Bearer "+rt.token {
		writeError(w, "", "SCOPE_DENIED", "agent capability expired or revoked", nil)
		return
	}
	if r.URL.Path == "/v1/secrets/use" {
		s.secretUse(rt, w, r)
		return
	}
	if r.URL.Path == "/v1/secrets:request" {
		s.secretRequestHTTP(w, r, "", rt)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/v1/settings/") || r.URL.Path == "/v1/invoke" || r.URL.Path == "/v1/models/model/native/chat/completions" {
		connections := rt.operations
		rt.mu.Lock()
		var g map[string]any
		var err error
		if rt.grant != "" {
			_, err = brokerOwner(rt.broker, "/owner/renew", map[string]any{"grant_id": rt.grant, "ttl_seconds": 300})
		}
		if rt.grant == "" || err != nil {
			g, err = brokerOwner(rt.broker, "/owner/grants", map[string]any{"sandbox_id": rt.sandbox, "generation": rt.generation, "peer_ip": "127.0.0.1", "ttl_seconds": 300, "connections": connections, "settings": rt.enrollment.Settings})
			if err == nil {
				rt.grant = textField(g, "grant_id")
				rt.brokerToken = textField(g, "token")
			}
		}
		token := rt.brokerToken
		rt.mu.Unlock()
		if err != nil {
			writeError(w, "", "SCOPE_DENIED", err.Error(), nil)
			return
		}

		r.Header.Set("Authorization", "Bearer "+token)
		r.RemoteAddr = "127.0.0.1:1"
		rt.broker.TaskHandler().ServeHTTP(w, r)
		return
	}
	if rt.enrollment.Nested {
		s.agentNested(rt, w, r)
		return
	}
	writeError(w, "", "SCOPE_DENIED", "agent route outside capability", nil)
}

// All nested calls still enter the public API. The capability authorizes only
// this parent's recorded children, and expires when the parent stops running.
func (s *Service) agentNested(rt *agentRuntime, w http.ResponseWriter, r *http.Request) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	op, selectors, ok := surface.Match(r.Method, r.URL.Path)
	if !ok {
		writeError(w, "", "SCOPE_DENIED", "nested operation not permitted", nil)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 128<<10))
	if err != nil {
		writeError(w, "", "INVALID_ARGUMENT", "request too large", nil)
		return
	}
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	body, err := surface.Decode(raw)
	if err != nil {
		writeError(w, "", "INVALID_ARGUMENT", "invalid JSON", nil)
		return
	}
	switch op.Name {
	case "sandbox_create":
		s.readLock()
		parent := s.journal.Sandboxes[rt.sandbox]
		count := 0
		for _, sb := range s.journal.Sandboxes {
			if sb.State != "deleted" && sb.State != "failed" {
				count++
			}
		}
		journalKey := s.journal.Control.OwnerID + "\nsandbox_create\n" + "agent:" + rt.session + ":" + r.Header.Get("Idempotency-Key")
		_, replayed := s.keys[journalKey]
		s.mu.Unlock()
		if !replayed && (count >= 2 || len(rt.children) >= 1) {
			writeError(w, "", "NO_CAPACITY", "nested capability permits one child within two live sandboxes", nil)
			return
		}
		// A child cannot escalate host, template, resources, task or auth scope.
		for k := range body {
			if k != "wait_ms" {
				writeError(w, "", "SCOPE_DENIED", "nested create accepts only wait_ms; inherits its parent's template, resources and task", nil)
				return
			}
		}
		body["template_id"] = value(parent.TemplateId)
		body["resources"] = parent.Resources
		if parent.TaskId != nil {
			body["task_id"] = *parent.TaskId
		}
	case "sandbox_get", "sandbox_delete", "exec_create", "exec_get", "exec_watch":
		target := textField(selectors, "sandbox_id")
		if op.Name == "exec_create" {
			target = textField(body, "sandbox_id")
		}
		if strings.HasPrefix(op.Name, "exec_") && op.Name != "exec_create" {
			s.readLock()
			ex := s.journal.Execs[textField(selectors, "exec_id")]
			target = value(ex.SandboxId)
			s.mu.Unlock()
		}
		if op.Name == "sandbox_delete" {
			body["wait_ms"] = json.Number("60000")
		}
		if !rt.children[target] {
			writeError(w, "", "SCOPE_DENIED", "only this parent's child sandboxes are accessible", nil)
			return
		}
	default:
		writeError(w, "", "SCOPE_DENIED", "nested capability allows create, get, exec and delete only", nil)
		return
	}
	data, _ := json.Marshal(body)
	r.Body = io.NopCloser(bytes.NewReader(data))
	r.Header.Del("Authorization")
	r.Header.Set("Idempotency-Key", "agent:"+rt.session+":"+r.Header.Get("Idempotency-Key"))
	// Owner context is used only after the narrow capability above has checked
	// every resource selector. Never forward a guest-chosen authorization header.
	r = r.WithContext(context.WithValue(r.Context(), agentCallerKey{}, caller{Principal: s.journal.Control.OwnerID}))
	out := httptest.NewRecorder()
	s.ServeHTTP(out, r)
	if op.Name == "sandbox_create" && out.Code < 300 {
		var v map[string]any
		json.Unmarshal(out.Body.Bytes(), &v)
		for _, ref := range toObjects(v["resource_ids"]) {
			if textField(ref, "kind") == "sandbox" {
				rt.children[textField(ref, "id")] = true
			}
		}
	}
	if op.Name == "sandbox_delete" && out.Code < 300 {
		delete(rt.children, textField(selectors, "sandbox_id"))
	}
	runtimePath := filepath.Join(s.dir, "agent-runtimes", rt.session+".json")
	if raw, err := os.ReadFile(runtimePath); err == nil {
		var saved map[string]any
		if json.Unmarshal(raw, &saved) == nil {
			saved["Children"] = rt.children
			raw, _ = json.Marshal(saved)
			if err = atomicSecret(runtimePath, raw); err != nil {
				writeError(w, "", "DATA_UNAVAILABLE", "nested child receipt could not be persisted", nil)
				return
			}
		}
	}
	for k, v := range out.Header() {
		w.Header()[k] = v
	}
	w.WriteHeader(out.Code)
	w.Write(out.Body.Bytes())
}

func toObjects(v any) []map[string]any { var out []map[string]any; agentAssign(v, &out); return out }

func agentAssign(v any, out any) { raw, _ := json.Marshal(v); _ = json.Unmarshal(raw, out) }

func (s *Service) agentConnection(m map[string]any) (broker.Connection, error) {
	c := broker.Connection{Kind: textField(m, "type"), Endpoint: textField(m, "endpoint"), Access: textField(m, "access"), Adapter: textField(m, "adapter_id")}
	doc := object(m["resource_policy"])
	agentAssign(doc["operations"], &c.Operations)
	c.Binary = textField(doc, "binary")
	c.Resource = textField(doc, "resource")
	c.Region = textField(doc, "region")
	if name := textField(doc, "secret_name"); name != "" {
		if _, err := s.readAgentSecret(name); err != nil {
			return c, err
		}
		c.CredentialCommand = []string{"/bin/cat", filepath.Join(s.dir, "credentials", name)}
	}
	var allowed []string
	agentAssign(m["allowed_operations"], &allowed)
	if len(allowed) != len(c.Operations) {
		return c, reject("INVALID_ARGUMENT", "allowed_operations must name every resource_policy.operations entry")
	}
	for _, op := range allowed {
		if _, ok := c.Operations[op]; !ok {
			return c, reject("INVALID_ARGUMENT", "operation is not enrolled")
		}
	}
	if err := (broker.Config{Connections: map[string]broker.Connection{"enrolled": c}}).Validate(); err != nil {
		return c, reject("INVALID_ARGUMENT", err.Error())
	}
	return c, nil
}
func (s *Service) planAgentConnection(a caller, in map[string]any) (lifePlan, error) {
	if !s.agentOwner(a) {
		return lifePlan{}, reject("SCOPE_DENIED", "connection enrollment requires owner credential")
	}
	if _, err := s.agentConnection(in); err != nil {
		return lifePlan{}, err
	}
	n := s.addNetwork("cn", "connection", a.Principal)
	n.Runtime = "agent"
	for _, k := range []string{"type", "endpoint", "access", "adapter_id", "allowed_operations", "resource_policy", "labels"} {
		if v, ok := in[k]; ok {
			n.R[k] = v
		}
	}
	n.R["state"] = "ready"
	return lifePlan{ref: lifeRef("connection", textField(n.R, "id")), result: map[string]any{"connection_id": n.R["id"]}}, nil
}

func init() {
	surface.CoreNames["settings_source_apply"] = true
	lifeHandlers["settings_source_apply"] = (*Service).planSettingsApply
}
func (s *Service) planSettingsApply(a caller, in map[string]any) (lifePlan, error) {
	if !s.agentOwner(a) {
		return lifePlan{}, reject("SCOPE_DENIED", "settings return requires explicit owner approval")
	}
	rt := s.agentRuntimes[textField(in, "session_id")]
	source := textField(in, "settings_source_id")
	n := s.life.Networks[source]
	if rt == nil || n == nil || textField(n.R, "state") != "active" || !slices.Contains(rt.enrollment.Settings, source) {
		return lifePlan{}, reject("NOT_FOUND", "session settings source unavailable")
	}
	change := textField(in, "settings_change_id")
	if !agentName.MatchString("c" + change) {
		return lifePlan{}, reject("INVALID_ARGUMENT", "invalid settings change id")
	}
	return lifePlan{ref: lifeRef("settings_source", source), run: func(ctx context.Context) (map[string]any, error) {
		s.readLock()
		current := s.life.Networks[source]
		active := current != nil && textField(current.R, "state") == "active"
		s.mu.Unlock()
		if !active {
			return nil, reject("SCOPE_DENIED", "settings source was revoked")
		}
		request := httptest.NewRequest("GET", "/owner/settings/"+change, nil)
		response := httptest.NewRecorder()
		rt.broker.OwnerHandler().ServeHTTP(response, request)
		var proposal map[string]any
		json.Unmarshal(response.Body.Bytes(), &proposal)
		if response.Code != 200 || textField(proposal, "source_id") != source {
			return nil, reject("SCOPE_DENIED", "proposal does not belong to this settings source")
		}
		_, err := brokerOwner(rt.broker, "/owner/settings/"+change, map[string]any{"base_revision": in["base_revision"], "selected_paths": in["selected_paths"]})
		return map[string]any{"settings_source_id": source}, err
	}}, nil
}
