package control

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/JohanSardar/lamp/internal/surface"
	"github.com/JohanSardar/lamp/pkg/api"
)

// IdentityConfig enrolls the owner without persisting a reusable bootstrap bearer.
// Empty configuration is the UID-authenticated, private Unix socket owner mode.
// Once enrolled, a journal cannot be reopened in unauthenticated mode.
type IdentityConfig struct {
	OwnerID         string              `json:"owner_id"`
	BootstrapSHA256 string              `json:"bootstrap_sha256"`
	Subjects        []SubjectEnrollment `json:"subjects,omitempty"`
}

// SubjectEnrollment is supplied by the trusted identity custodian, never by
// fleet management APIs. Only the subject receives the corresponding bearer.
type SubjectEnrollment struct {
	Issuer  string `json:"issuer"`
	Subject string `json:"subject"`
	SHA256  string `json:"sha256"`
}
type storedKey struct {
	Key    api.Key `json:"key"`
	SHA256 string  `json:"sha256"`
}
type controlState struct {
	OwnerID      string                    `json:"owner_id"`
	Credentialed bool                      `json:"credentialed"`
	CursorSecret string                    `json:"cursor_secret"`
	Fleet        api.Fleet                 `json:"fleet"`
	Principals   map[string]api.Principal  `json:"principals"`
	Memberships  map[string]api.Membership `json:"memberships"`
	Keys         map[string]storedKey      `json:"keys"`
	Approvals    map[string]api.Approval   `json:"approvals"`
	// Updates are fleet-wide rollouts (HST-005), replicated with authority.
	Updates map[string]api.Update `json:"updates,omitempty"`
}
type caller struct{ Principal, KeyID string }
type callerContext struct{}

func requestCaller(r *http.Request) caller {
	c, _ := r.Context().Value(callerContext{}).(caller)
	return c
}
func (s *Service) initAuthority(config Config) error {
	if s.journal.Control == nil {
		owner := "pr_local"
		credentialed := config.Identity != nil
		if credentialed {
			owner = config.Identity.OwnerID
			if owner == "" {
				return errors.New("owner ID required")
			}
		}
		now := timestamp()
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return err
		}
		s.journal.Control = &controlState{OwnerID: owner, Credentialed: credentialed, CursorSecret: hex.EncodeToString(secret), Fleet: api.Fleet{Id: "fl_local", Kind: "fleet", FleetId: "fl_local", OwnerId: owner, Revision: 1, CreatedAt: now, UpdatedAt: now, State: "active", Labels: api.StringMap{}, Visibility: "private", Mode: pointer(api.FleetModeSingleOwner), AuthorityEpoch: pointer(uint64(1))}, Principals: map[string]api.Principal{}, Memberships: map[string]api.Membership{}, Keys: map[string]storedKey{}, Approvals: map[string]api.Approval{}}
	}
	c := s.journal.Control
	if c.Credentialed != (config.Identity != nil) {
		return errors.New("journal credential mode cannot be changed at startup")
	}
	if config.Identity != nil {
		hash, err := hex.DecodeString(config.Identity.BootstrapSHA256)
		if err != nil || len(hash) != 32 || config.Identity.OwnerID != c.OwnerID {
			return errors.New("invalid owner enrollment or bootstrap SHA-256")
		}
		s.identity = *config.Identity
		s.identity.Subjects = append([]SubjectEnrollment{}, config.Identity.Subjects...)
		seen := map[string]bool{config.Identity.BootstrapSHA256: true}
		for _, subject := range s.identity.Subjects {
			hash, err := hex.DecodeString(subject.SHA256)
			if err != nil || len(hash) != 32 || subject.Issuer == "" || subject.Subject == "" || seen[subject.SHA256] {
				return errors.New("invalid or duplicate subject enrollment")
			}
			seen[subject.SHA256] = true
		}
	}
	return nil
}
func bearerHash(v string) string { sum := sha256.Sum256([]byte(v)); return hex.EncodeToString(sum[:]) }
func (s *Service) authenticate(r *http.Request) (caller, bool) {
	c := s.journal.Control
	values := r.Header.Values("Authorization")
	if len(values) == 0 && !c.Credentialed {
		return caller{Principal: c.OwnerID}, true
	}
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
		return caller{}, false
	}
	token := strings.TrimPrefix(values[0], "Bearer ")
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return caller{}, false
	}
	hash := bearerHash(token)
	if c.Credentialed && hash == s.identity.BootstrapSHA256 {
		return caller{Principal: c.OwnerID}, true
	}
	for _, subject := range s.identity.Subjects {
		if hash != subject.SHA256 {
			continue
		}
		for _, principal := range c.Principals {
			if value(principal.Issuer) == subject.Issuer && value(principal.Subject) == subject.Subject {
				a := caller{Principal: principal.Id}
				return a, s.current(a)
			}
		}
		return caller{}, false
	}
	for id, k := range c.Keys {
		if hash == k.SHA256 {
			a := caller{Principal: value(k.Key.PrincipalId), KeyID: id}
			return a, s.current(a)
		}
	}
	return caller{}, false
}
func expired(t *string) bool {
	if t == nil {
		return false
	}
	v, e := time.Parse(time.RFC3339Nano, *t)
	return e != nil || !time.Now().Before(v)
}
func (s *Service) role(a caller) string {
	c := s.journal.Control
	if a.Principal == c.OwnerID {
		return "owner"
	}
	p, ok := c.Principals[a.Principal]
	if !ok || value(p.Disabled) || p.State != "active" {
		return ""
	}
	if value(c.Fleet.Mode) == "single_owner" {
		return ""
	}
	for _, m := range c.Memberships {
		if value(m.PrincipalId) == a.Principal && m.State == "active" {
			return string(value(m.Role))
		}
	}
	return ""
}
func (s *Service) current(a caller) bool {
	if s.fleet != nil && !s.fleet.leaseValid() {
		return false
	}
	if s.role(a) == "" {
		return false
	}
	seen := map[string]bool{}
	for id := a.KeyID; id != ""; {
		if seen[id] {
			return false
		}
		seen[id] = true
		k, ok := s.journal.Control.Keys[id]
		if !ok || k.Key.State != "active" || s.expired(k.Key.ExpiresAt) || k.Key.Scope != nil && s.expired(k.Key.Scope.ExpiresAt) {
			return false
		}
		id = value(k.Key.ParentKeyId)
	}
	return true
}
func has[T ~string](xs *[]T, v string) bool {
	if xs == nil {
		return true
	}
	for _, x := range *xs {
		if string(x) == v {
			return true
		}
	}
	return false
}
func (s *Service) scopes(a caller) []api.Scope {
	if a.KeyID == "" {
		return nil
	}
	k := s.journal.Control.Keys[a.KeyID]
	if k.Key.Scope == nil {
		return nil
	}
	return []api.Scope{*k.Key.Scope}
}
func (s *Service) can(a caller, action string) bool {
	if !s.current(a) {
		return false
	}
	role := s.role(a)
	kind, verb, _ := strings.Cut(action, ".")
	if role != "owner" {
		switch kind {
		case "fleet":
			if verb != "get" && verb != "list" {
				return false
			}
		case "membership", "principal":
			if verb != "get" && verb != "list" && role != "admin" {
				return false
			}
		case "approval":
			if verb == "approve" || verb == "deny" || verb == "revoke" {
				return false
			}
		}
		if role == "viewer" && verb != "get" && verb != "list" && verb != "watch" && verb != "read" && verb != "describe" && verb != "wait" {
			return false
		}
	}
	for _, sc := range s.scopes(a) {
		if !has(sc.Actions, action) {
			return false
		}
	}
	return true
}
func (s *Service) own(a caller, owner string) bool { return a.Principal == owner }
func (s *Service) resourceAllowed(a caller, refs ...api.ResourceRef) bool {
	for _, sc := range s.scopes(a) {
		if sc.ResourceIds == nil {
			continue
		}
		found := false
		for _, allowed := range *sc.ResourceIds {
			for _, ref := range refs {
				if allowed == ref {
					found = true
				}
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func (s *Service) sandboxSelectors(a caller, sb api.Sandbox) bool {
	if !s.current(a) || !s.own(a, sb.OwnerId) {
		return false
	}
	for _, sc := range s.scopes(a) {
		if !has(sc.SandboxIds, sb.Id) || !has(sc.RecipeIds, value(sb.RecipeId)) || !has(sc.HostIds, sandboxHost(sb)) {
			return false
		}
	}
	return true
}
func (s *Service) sandboxAllowed(a caller, sb api.Sandbox) bool {
	return s.sandboxSelectors(a, sb) && s.resourceAllowed(a, api.ResourceRef{Kind: "sandbox", Id: sb.Id})
}
func (s *Service) operationAllowed(a caller, rec *record) bool {
	if rec == nil || !s.current(a) || !s.own(a, rec.Operation.OwnerId) {
		return false
	}
	switch rec.Resource.Kind {
	case "sandbox":
		return s.sandboxAllowed(a, s.journal.Sandboxes[rec.Resource.Id])
	case "exec":
		return s.execAllowed(a, s.journal.Execs[rec.Resource.Id])
	}
	return s.resourceAllowed(a, api.ResourceRef{Kind: "operation", Id: rec.Operation.Id}, rec.Resource)
}
func (s *Service) execAllowed(a caller, ex api.Exec) bool {
	return s.own(a, ex.OwnerId) && s.sandboxSelectors(a, s.journal.Sandboxes[value(ex.SandboxId)]) && s.resourceAllowed(a, api.ResourceRef{Kind: "exec", Id: ex.Id}, api.ResourceRef{Kind: "sandbox", Id: value(ex.SandboxId)})
}
func (s *Service) admissionAllowed(a caller, name string, sb api.Sandbox) bool {
	if !s.can(a, surface.ByName[name].Scope) {
		return false
	}
	for _, sc := range s.scopes(a) {
		if !has(sc.HostIds, sandboxHost(sb)) {
			return false
		}
		if (name == "sandbox_create" && (sc.SandboxIds != nil || sc.ResourceIds != nil || sc.RecipeIds != nil)) || (name == "sandbox_fork" && (sc.SandboxIds != nil || sc.ResourceIds != nil)) {
			return false
		}
		if sc.Limits != nil && name != "sandbox_delete" {
			l := sc.Limits
			if sb.Resources != nil && (l.Vcpus != nil && sb.Resources.Vcpus > *l.Vcpus || l.MemoryMib != nil && sb.Resources.MemoryMib > *l.MemoryMib || l.DiskGib != nil && sb.Resources.DiskGib > *l.DiskGib) {
				return false
			}
			if l.Concurrency != nil {
				var n uint64
				if name == "sandbox_create" || name == "sandbox_fork" {
					for _, sandbox := range s.journal.Sandboxes {
						if sandbox.OwnerId == a.Principal && s.sandboxCharged(sandbox) {
							n++
						}
					}
				} else {
					for _, ex := range s.journal.Execs {
						if ex.OwnerId == a.Principal && s.execCharged(ex) {
							n++
						}
					}
				}
				if n >= *l.Concurrency {
					return false
				}
			}
		}
	}
	return true
}

// A live or uncertain command retains its reservation until completion or
// independently confirmed sandbox release. Keep its historical outcome even
// after release: deletion proves capacity was freed, not how the command ended.
func (s *Service) execCharged(ex api.Exec) bool {
	if ex.State != "accepted" && ex.State != "running" && ex.State != "outcome_unknown" {
		return false
	}
	sb, ok := s.journal.Sandboxes[value(ex.SandboxId)]
	return !ok || sb.State != "deleted"
}

func (s *Service) sandboxCharged(sb api.Sandbox) bool {
	if sb.State == "deleted" {
		return false
	} // Stop/Release independently confirmed.
	if sb.State == "failed" {
		for _, rec := range s.journal.Operations {
			if rec.Resource.Kind == "sandbox" && rec.Resource.Id == sb.Id && (value(rec.Operation.OperationName) == "sandbox_create" || value(rec.Operation.OperationName) == "sandbox_fork") && rec.Operation.State == "canceled" && value(rec.Operation.CancelEffect) == "prevented" && value(rec.Operation.CleanupState) == "complete" {
				return false
			}
		}
	}
	return true
}
func intersect[T comparable](parent, child *[]T) *[]T {
	if parent == nil {
		return child
	}
	if child == nil {
		return parent
	}
	out := []T{}
	for _, c := range *child {
		for _, p := range *parent {
			if c == p {
				out = append(out, c)
				break
			}
		}
	}
	return &out
}
func narrow(parent, child api.Scope) api.Scope {
	child.Actions = intersect(parent.Actions, child.Actions)
	child.SandboxIds = intersect(parent.SandboxIds, child.SandboxIds)
	child.RecipeIds = intersect(parent.RecipeIds, child.RecipeIds)
	child.HostIds = intersect(parent.HostIds, child.HostIds)
	child.ResourceIds = intersect(parent.ResourceIds, child.ResourceIds)
	if parent.ExpiresAt != nil && (child.ExpiresAt == nil || earlier(*parent.ExpiresAt, *child.ExpiresAt)) {
		child.ExpiresAt = parent.ExpiresAt
	}
	if parent.Limits != nil {
		var a, b map[string]uint64
		raw, _ := json.Marshal(parent.Limits)
		json.Unmarshal(raw, &a)
		raw, _ = json.Marshal(child.Limits)
		json.Unmarshal(raw, &b)
		if b == nil {
			b = map[string]uint64{}
		}
		for k, v := range a {
			if x, ok := b[k]; !ok || v < x {
				b[k] = v
			}
		}
		raw, _ = json.Marshal(b)
		child.Limits = &api.Limits{}
		json.Unmarshal(raw, child.Limits)
	}
	return child
}
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/internal/accountability/") {
		s.accountabilityBridge(w, r)
		return
	}
	id := newID("req")
	w.Header().Set("X-Request-ID", id)
	if r.Method == "POST" && r.URL.Path == "/internal/native-tasks" {
		s.nativeHTTP(w, r, id)
		return
	}
	op, selectors, ok := surface.Match(r.Method, r.URL.Path)
	if !ok {
		writeError(w, id, "NOT_FOUND", "unknown route", nil)
		return
	}
	if peer, present := r.Context().Value(peerContextKey{}).(publicPeer); present && !peer.owner {
		if r.URL.RawQuery != "" || !s.authorizePublicRequest(r.Context(), r.Method, r.URL.Path) {
			writeError(w, id, "SCOPE_DENIED", "peer has no access to this resource", nil)
			return
		}
		if !s.serveCustody(w, r, id) {
			writeError(w, id, "NOT_FOUND", "shared projection not found", nil)
		}
		return
	}
	if (op.Name == "auth_flow_bootstrap" || op.Name == "auth_flow_exchange") && s.github != nil {
		s.ciAuthHTTP(w, r, id, op.Name, selectors)
		return
	}
	s.readLock()
	a, authenticated := s.authenticate(r)
	allowed := authenticated && s.can(a, op.Scope)
	broken := s.broken
	s.mu.Unlock()
	if broken {
		writeError(w, id, "DATA_UNAVAILABLE", "journal unavailable", nil)
		return
	}
	if !authenticated {
		writeError(w, id, "UNAUTHENTICATED", "valid caller credential required", nil)
		return
	}
	if !allowed {
		writeError(w, id, "SCOPE_DENIED", "action not permitted", nil)
		return
	}
	if s.native != nil && op.Mutation && (strings.HasPrefix(op.Name, "login_") || op.Name == "connection_invoke") {
		writeError(w, id, "VM_REQUIRED", "logins and protected connections require VM isolation", nil)
		return
	}
	if !surface.CoreNames[op.Name] {
		writeError(w, id, "NOT_FOUND", "operation not installed", nil)
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), callerContext{}, a))
	if op.Name == "plugin_get" && textField(selectors, "plugin_id") == "pg_github" {
		s.ciPluginHTTP(w, r, id)
		return
	}
	if s.cleanExportRead(op.Name, selectors) {
		// T9's clean-export templates and blobs keep their own read path.
		s.engineHTTP(w, r)
		return
	}
	if lifeOperation(op.Name) {
		s.lifeHTTP(w, r, id, op, selectors)
		return
	}
	if op.Name == "sandbox_delete" && !s.deleteKeepsWrites(w, id, textField(selectors, "sandbox_id")) {
		return
	}
	if op.Name == "exec_create" && !s.wakeForExec(w, r, id) {
		return
	}
	if op.Name == "sandbox_create" && s.lifeCreate(w, r, id) {
		return
	}
	switch op.Name {
	case "sandbox_create", "sandbox_fork", "sandbox_delete", "exec_create", "sandbox_get", "exec_get", "exec_watch", "operation_get",
		"sandbox_suspend", "sandbox_resume", "snapshot_create", "snapshot_get",
		"share_create", "share_get", "template_get", "blob_get", "blob_download":
		s.engineHTTP(w, r)
	default:
		s.coreHTTP(w, r, id, op, selectors)
	}
}

func earlier(a, b string) bool {
	ta, ea := time.Parse(time.RFC3339Nano, a)
	tb, eb := time.Parse(time.RFC3339Nano, b)
	return ea != nil || eb != nil || ta.Before(tb)
}
