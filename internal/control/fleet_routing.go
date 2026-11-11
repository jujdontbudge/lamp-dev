package control

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/JohanSardar/lamp/internal/surface"
	"github.com/JohanSardar/lamp/pkg/api"
)

func (s *Service) hostID() string {
	if s.fleet != nil {
		return s.fleet.config.HostID
	}
	return "hs_local"
}
func (s *Service) resourceID(prefix string) string {
	id := newID(prefix)
	if s.fleet != nil {
		id += "_h_" + s.hostID()
	}
	return id
}
func sandboxHost(sb api.Sandbox) string {
	if sb.HostId == nil {
		return "hs_local"
	}
	return *sb.HostId
}
func resourceHome(id string) string {
	_, home, ok := strings.Cut(id, "_h_")
	if !ok {
		return ""
	}
	return home
}
func (f *Fleet) activate(expiry time.Time) {
	ticks, err := authorityTicks()
	if err != nil {
		return
	}
	now := time.Now()
	f.mu.Lock()
	active := f.active
	f.mu.Unlock()
	if active {
		now = f.authorityNow()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.leaseExpiry = expiry
	f.deadline = ticks + int64(expiry.Sub(now))
	f.active = true
}
func (f *Fleet) leaseValid() bool {
	ticks, err := authorityTicks()
	f.mu.Lock()
	defer f.mu.Unlock()
	return err == nil && f.active && ticks < f.deadline
}
func (f *Fleet) authorityActivated() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.active
}
func (f *Fleet) authorityNow() time.Time {
	ticks, err := authorityTicks()
	f.mu.Lock()
	deadline, active := f.deadline, f.active
	f.mu.Unlock()
	f.mu.Lock()
	expiry := f.leaseExpiry
	f.mu.Unlock()
	if err != nil || !active {
		return time.Unix(1<<62, 0)
	}
	// Fixed at quorum activation; a wall-clock rollback cannot extend a grant.
	now := expiry.Add(time.Duration(ticks - deadline))
	if wall := time.Now(); wall.After(now) {
		return wall
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if now.Before(f.lastAuthorityTime) {
		return f.lastAuthorityTime
	}
	f.lastAuthorityTime = now
	return now
}
func (s *Service) expired(expires *string) bool {
	if expires == nil {
		return false
	}
	t, err := time.Parse(time.RFC3339Nano, *expires)
	return err != nil || !s.enforcementNow().Before(t)
}
func (f *Fleet) serve(w http.ResponseWriter, r *http.Request, routed bool) {
	id := newID("req")
	if err := f.project(); err != nil {
		writeError(w, id, "QUORUM_REQUIRED", err.Error(), nil)
		return
	}
	op, selectors, ok := surface.Match(r.Method, r.URL.Path)
	if !ok {
		writeError(w, id, "NOT_FOUND", "unknown route", nil)
		return
	}
	s := f.service
	s.readLock()
	a, authenticated := s.authenticate(r)
	allowed := authenticated && s.can(a, op.Scope)
	s.mu.Unlock()
	if !authenticated {
		writeError(w, id, "UNAUTHENTICATED", "valid fleet credential required", nil)
		return
	}
	if !allowed {
		writeError(w, id, "SCOPE_DENIED", "action not permitted", nil)
		return
	}
	// Frozen offline authority is useful only during its declared permit horizon.
	if !f.leaseValid() {
		writeError(w, id, "QUORUM_REQUIRED", "local authority requires a quorum activation or has expired", nil)
		return
	}
	if op.Name == "host_get" || op.Name == "host_list" {
		f.hostsHTTP(w, r, id, a, selectors)
		return
	}
	if op.Name == "sandbox_list" || op.Name == "exec_list" {
		f.catalogHTTP(w, r, id, a, op, selectors)
		return
	}
	if op.Name == "event_list" && !routed {
		f.eventsHTTP(w, r, id, a, selectors)
		return
	}
	if op.Name == "fleet_get" || op.Name == "fleet_list" {
		s.readLock()
		fleet := s.journal.Control.Fleet
		policy := s.journal.Control.updatePolicy()
		fleet.UpdatePolicy = &policy
		fleet.CoordinatorHostId = pointer(f.leaderID())
		fleet.CoordinationState = pointer(api.FleetCoordinationStateDegraded)
		if f.raft.State().String() == "Leader" {
			fleet.CoordinationState = pointer(api.FleetCoordinationStateHealthy)
		}
		s.mu.Unlock()
		if op.Name == "fleet_get" {
			if textField(selectors, "fleet_id") != fleet.Id {
				writeError(w, id, "NOT_FOUND", "fleet not found", nil)
				return
			}
			prepareJSON(w, 200, map[string]any{"data": fleet, "request_id": id})()
		} else {
			prepareJSON(w, 200, map[string]any{"items": []api.Fleet{fleet}, "request_id": id})()
		}
		return
	}
	if op.Mutation && strings.HasPrefix(r.Header.Get("Idempotency-Key"), "lk1.") {
		parts := strings.Split(r.Header.Get("Idempotency-Key"), ".")
		if len(parts) != 4 || parts[3] == "" || parts[1] != strconv.FormatUint(f.fsm.snapshot().Epoch, 10) {
			writeError(w, id, "KEY_EPOCH_EXPIRED", "invalid or old structured retry key", nil)
			return
		}
		if _, ok := f.peers[parts[2]]; !ok {
			writeError(w, id, "KEY_EPOCH_EXPIRED", "retry home is not enrolled", nil)
			return
		}
	}
	kind, _, _ := strings.Cut(op.Scope, ".")
	global := op.Mutation && (kind == "fleet" || kind == "key" || kind == "membership" || kind == "principal" || kind == "update")
	if global {
		leader := f.leaderID()
		if leader == "" {
			writeError(w, id, "QUORUM_REQUIRED", "global authority needs quorum", nil)
			return
		}
		if leader != f.config.HostID {
			if routed {
				writeError(w, id, "QUORUM_REQUIRED", "coordinator changed; retry same key", nil)
			} else {
				f.forward(w, r, leader, "QUORUM_REQUIRED")
			}
			return
		}
		f.global.Lock()
		defer f.global.Unlock()
		if err := f.raft.VerifyLeader().Error(); err != nil {
			writeError(w, id, "QUORUM_REQUIRED", "global authority needs quorum", nil)
			return
		}
		if err := f.project(); err != nil {
			writeError(w, id, "DATA_UNAVAILABLE", err.Error(), nil)
			return
		}
		s.ServeHTTP(w, r)
		return
	}
	home := ""
	for _, field := range []string{"sandbox_id", "exec_id", "operation_id", "approval_id", "session_id", "lease_id", "share_id", "budget_id", "task_id", "audit_record_id"} {
		if target := textField(selectors, field); target != "" {
			home = resourceHome(target)
		}
	}
	if op.Name == "operation_get" || op.Name == "operation_wait" {
		if rec := f.fsm.snapshot().Records[textField(selectors, "operation_id")]; rec != nil && terminal(rec.Operation.State) && rec.Resource.Kind != "sandbox" && rec.Resource.Kind != "exec" {
			// Engine receipts still require their home's sandbox/exec records
			// for authorization, even when a terminal receipt was replicated.
			home = ""
		}
	}
	if op.Mutation {
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 128<<10))
		if err != nil {
			writeError(w, id, "INVALID_ARGUMENT", "request exceeds limit", nil)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		r.ContentLength = int64(len(raw))
		input, err := surface.Decode(raw)
		if len(raw) == 0 {
			input = map[string]any{}
			err = nil
		}
		if err != nil {
			writeError(w, id, "INVALID_ARGUMENT", err.Error(), nil)
			return
		}
		if home == "" {
			home = resourceHome(textField(input, "sandbox_id"))
			if op.Name == "audit_record_export" {
				filter, _ := input["filter"].(map[string]any)
				home = resourceHome(textField(filter, "sandbox_id"))
			}
			if home == "" {
				home = resourceHome(textField(input, "task_id"))
			}
			if home == "" {
				home = resourceHome(textField(input, "parent_task_id"))
			}
			if home == "" {
				home = resourceHome(textField(input, "budget_id"))
			}
		}
		if kind == "session" && op.Name == "session_create" {
			location, _ := input["location"].(map[string]any)
			if textField(location, "kind") == "host" {
				home = textField(location, "host_id")
			}
		}
		if home == "" && (kind == "lease" || kind == "share") {
			home = resourceHome(textField(input, "resource_id"))
		}
		if kind == "approval" {
			if home == "" {
				home = resourceHome(textField(selectors, "approval_id"))
			}
		}
		if op.Name == "connection_invoke" {
			home = resourceHome(textField(input, "approval_id"))
			if home == "" {
				writeError(w, id, "APPROVAL_REQUIRED", "approval with a committed home required", nil)
				return
			}
		}
		key := r.Header.Get("Idempotency-Key")
		if key == "" || len(key) > 256 {
			writeError(w, id, "INVALID_ARGUMENT", "Idempotency-Key of 1–256 bytes required", nil)
			return
		}
		if strings.HasPrefix(key, "lk1.") {
			parts := strings.Split(key, ".")
			if len(parts) != 4 || parts[3] == "" || parts[1] != strconv.FormatUint(f.fsm.snapshot().Epoch, 10) {
				writeError(w, id, "KEY_EPOCH_EXPIRED", "invalid or old structured retry key", nil)
				return
			}
			if _, ok := f.peers[parts[2]]; !ok {
				writeError(w, id, "KEY_EPOCH_EXPIRED", "retry home is not enrolled", nil)
				return
			}
			if home != "" && home != parts[2] {
				writeError(w, id, "INVALID_ARGUMENT", "retry key home must match resource home", nil)
				return
			}
			home = parts[2]
		} else {
			identity := a.Principal + "\n" + op.Name + "\n" + key
			state := f.fsm.snapshot()
			if old := state.Homes[identity]; old != "" {
				home = old
			} else {
				if home == "" {
					home = f.choose(a, input)
				}
				if home == "" {
					writeError(w, id, "NO_CAPACITY", "no compatible host has reserved capacity", nil)
					return
				}
				if err := f.command(fleetCommand{HomeKey: identity, Home: home}); err != nil {
					writeError(w, id, "IDEMPOTENCY_HOME_UNAVAILABLE", "opaque retry keys need a quorum home registration; use a structured key for reserved survivor work", nil)
					return
				}
			}
		}
		if op.Name == "sandbox_create" {
			pin := textField(input, "host_id")
			if pin != "" && pin != home {
				writeError(w, id, "INVALID_ARGUMENT", "strict host pin conflicts with immutable retry home", nil)
				return
			}
			// Automatic opaque-key placement selected once above. Structured keys already
			// select their host explicitly; neither path migrates after a lost response.
		}
	}
	if op.Name == "sandbox_get" && home != "" && home != f.config.HostID {
		f.mu.Lock()
		observation := f.observations[home]
		online := f.online[home]
		f.mu.Unlock()
		if !online {
			s.readLock()
			sb, ok := observation.Sandboxes[textField(selectors, "sandbox_id")]
			allowed := ok && s.sandboxAllowed(a, sb)
			s.mu.Unlock()
			if allowed {
				if sb.State != "deleted" {
					sb.State = "unavailable"
					sb.Readiness = coreReadiness(false, nil)
				}
				prepareJSON(w, 200, map[string]any{"data": sb, "request_id": id})()
				return
			}
		}
	}
	if home != "" && home != f.config.HostID {
		if routed {
			writeError(w, id, "HOST_UNAVAILABLE", "request reached a different immutable home", nil)
			return
		}
		f.forward(w, r, home, "IDEMPOTENCY_HOME_UNAVAILABLE")
		return
	}
	s.ServeHTTP(w, r)
}
func (f *Fleet) choose(a caller, input map[string]any) string {
	pin := textField(input, "host_id")
	f.mu.Lock()
	observations := map[string]fleetObservation{}
	online := map[string]bool{}
	for id, o := range f.observations {
		observations[id] = o
		online[id] = f.online[id]
	}
	f.mu.Unlock()
	candidates := []string{}
	for id, peer := range f.peers {
		if pin != "" && id != pin || id != f.config.HostID && !online[id] {
			continue
		}
		engine := textField(input, "engine_id")
		compatible := engine == "" && len(peer.host.Engines) > 0
		for _, item := range peer.host.Engines {
			if item == engine {
				compatible = true
			}
		}
		if !compatible {
			continue
		}
		s := f.service
		s.readLock()
		scopes := s.scopes(a)
		allowed := true
		for _, scope := range scopes {
			if !has(scope.HostIds, id) {
				allowed = false
			}
		}
		s.mu.Unlock()
		if !allowed {
			continue
		}
		var requested api.Resources
		if input["resources"] != nil {
			assign(object(input["resources"]), &requested)
		} else {
			requested = api.Resources{Vcpus: 2, MemoryMib: 4096, DiskGib: 20, PidsMax: 4096}
		}
		if capacityFits(peer.host.Capacity, observations[id].Sandboxes, requested) {
			candidates = append(candidates, id)
		}
	}
	sort.Strings(candidates)
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0]
}
func capacityFits(capacity api.Capacity, sandboxes map[string]api.Sandbox, request api.Resources) bool {
	free := api.Resources{Vcpus: value(capacity.Vcpus), MemoryMib: value(capacity.MemoryMib), DiskGib: value(capacity.DiskGib), PidsMax: value(capacity.PidsMax)}
	subtract := func(available *uint64, used uint64) {
		if used > *available {
			*available = 0
		} else {
			*available -= used
		}
	}
	for _, sb := range sandboxes {
		if sb.State != "deleted" && sb.Resources != nil {
			subtract(&free.Vcpus, sb.Resources.Vcpus)
			subtract(&free.MemoryMib, sb.Resources.MemoryMib)
			subtract(&free.DiskGib, sb.Resources.DiskGib)
			subtract(&free.PidsMax, sb.Resources.PidsMax)
		}
	}
	return request.Vcpus <= free.Vcpus && request.MemoryMib <= free.MemoryMib && request.DiskGib <= free.DiskGib && request.PidsMax <= free.PidsMax
}

// Runs under the local admission mutex. A fixed physical slice is never lent to
// another host. Unknown/failed cleanup continues consuming its reservation.
func (f *Fleet) admit(a caller, name string, sb api.Sandbox) error {
	if !f.leaseValid() {
		return reject("QUORUM_REQUIRED", "reserved authority expired or needs activation")
	}
	if name == "sandbox_create" || name == "sandbox_fork" {
		if sb.Resources == nil || !capacityFits(f.local.Capacity, f.service.journal.Sandboxes, *sb.Resources) {
			return reject("NO_CAPACITY", "local reserved slice exhausted")
		}
		engine := value(sb.EngineId)
		compatible := engine == "" && len(f.local.Engines) > 0
		for _, item := range f.local.Engines {
			if engine == item {
				compatible = true
			}
		}
		if !compatible {
			return reject("ENGINE_UNQUALIFIED", "requested engine is not enrolled on pinned host")
		}
	}
	if name != "sandbox_delete" {
		// Finite concurrency authority is divided deterministically; a partition can
		// spend only its own fraction, even when the same key is used on both sides.
		for _, scope := range f.service.scopes(a) {
			if scope.Limits != nil && scope.Limits.Concurrency != nil {
				index := 0
				for i, host := range f.config.Hosts {
					if host.ID == f.config.HostID {
						index = i
					}
				}
				count := uint64(len(f.config.Hosts))
				limit := *scope.Limits.Concurrency / count
				if uint64(index) < *scope.Limits.Concurrency%count {
					limit++
				}
				var charged uint64
				if name == "exec_create" {
					for _, ex := range f.service.journal.Execs {
						if ex.OwnerId == a.Principal && f.service.execCharged(ex) {
							charged++
						}
					}
				} else {
					for _, item := range f.service.journal.Sandboxes {
						if item.OwnerId == a.Principal && f.service.sandboxCharged(item) {
							charged++
						}
					}
				}
				if charged >= limit {
					return reject("NO_CAPACITY", "principal concurrency slice exhausted on this host")
				}
			}
		}
	}
	return nil
}
func (f *Fleet) hostsHTTP(w http.ResponseWriter, r *http.Request, id string, a caller, in map[string]any) {
	items := []map[string]any{}
	f.mu.Lock()
	observations := map[string]fleetObservation{}
	online := map[string]bool{}
	for id, o := range f.observations {
		observations[id] = o
		online[id] = f.online[id]
	}
	f.mu.Unlock()
	for _, host := range f.config.Hosts {
		s := f.service
		s.readLock()
		allowed := s.resourceAllowed(a, api.ResourceRef{Kind: "host", Id: host.ID})
		for _, scope := range s.scopes(a) {
			allowed = allowed && has(scope.HostIds, host.ID)
		}
		owner := s.journal.Control.OwnerID
		s.mu.Unlock()
		if !allowed {
			continue
		}
		state := "unavailable"
		if host.ID == f.config.HostID || online[host.ID] {
			state = "active"
		}
		capacity := object(host.Capacity)
		used := api.Resources{}
		for _, sb := range observations[host.ID].Sandboxes {
			if sb.State != "deleted" && sb.Resources != nil {
				used.Vcpus += sb.Resources.Vcpus
				used.MemoryMib += sb.Resources.MemoryMib
				used.DiskGib += sb.Resources.DiskGib
				used.PidsMax += sb.Resources.PidsMax
			}
		}
		reserved := object(used)
		m := map[string]any{"id": host.ID, "kind": "host", "fleet_id": "fl_local", "owner_id": owner, "revision": 1, "created_at": f.fsm.snapshot().Control.Fleet.CreatedAt, "updated_at": observations[host.ID].At, "state": state, "labels": map[string]string{"permit_expires_at": f.fsm.snapshot().ExpiresAt, "allocation": "fixed local slice"}, "visibility": "private", "os": host.OS, "arch": host.Arch, "roles": []string{"control"}, "engine_ids": host.Engines, "capacity": capacity, "reserved": reserved, "heartbeat_at": observations[host.ID].At}
		if len(host.Engines) > 0 {
			m["roles"] = []string{"control", "compute"}
		}
		if r.URL.Path == "/v1/hosts/"+host.ID {
			prepareJSON(w, 200, map[string]any{"data": m, "request_id": id})()
			return
		}
		items = append(items, m)
	}
	if textField(in, "host_id") != "" {
		writeError(w, id, "NOT_FOUND", "host not found", nil)
		return
	}
	prepareJSON(w, 200, map[string]any{"items": items, "request_id": id})()
}
func (f *Fleet) catalogHTTP(w http.ResponseWriter, r *http.Request, id string, a caller, op api.OperationDescriptor, in map[string]any) {
	query, err := parseQuery(r)
	if err != nil || surface.Query(op, query, in) != nil {
		writeError(w, id, "INVALID_ARGUMENT", "invalid list query", nil)
		return
	}
	// Collect on request as well as heartbeat, so a just-created sandbox is visible.
	f.collect()
	items := []map[string]any{}
	f.mu.Lock()
	observations := map[string]fleetObservation{}
	online := map[string]bool{}
	for host, o := range f.observations {
		observations[host] = o
		online[host] = f.online[host]
	}
	f.mu.Unlock()
	s := f.service
	s.readLock()
	defer s.mu.Unlock()
	for host, observation := range observations {
		for _, sb := range observation.Sandboxes {
			if !s.sandboxAllowed(a, sb) {
				continue
			}
			if op.Name == "sandbox_list" {
				if !online[host] && sb.State != "deleted" {
					sb.State = "unavailable"
					sb.Readiness = coreReadiness(false, nil)
				}
				items = append(items, object(sb))
			} else {
				for _, ex := range observation.Execs {
					if value(ex.SandboxId) == sb.Id && ex.OwnerId == a.Principal && s.resourceAllowed(a, api.ResourceRef{Kind: "exec", Id: ex.Id}, api.ResourceRef{Kind: "sandbox", Id: sb.Id}) {
						if !online[host] && ex.State == "running" {
							ex.State = "unavailable"
						}
						items = append(items, object(ex))
					}
				}
			}
		}
	}
	sort.Slice(items, func(i, j int) bool { return textField(items[i], "id") < textField(items[j], "id") })
	limit := uintField(in, "limit")
	if limit == 0 {
		limit = 100
	}
	if limit > 1000 {
		writeError(w, id, "INVALID_ARGUMENT", "list limit is 1000", nil)
		return
	}
	if cursor := textField(in, "cursor"); cursor != "" {
		last, err := s.readCursor(a, "fleet:"+op.Name, cursor)
		if err != nil {
			writeError(w, id, "CURSOR_EXPIRED", "list again without cursor", nil)
			return
		}
		start := sort.Search(len(items), func(i int) bool { return textField(items[i], "id") > last })
		items = items[start:]
	}
	var next *string
	if len(items) > int(limit) {
		items = items[:limit]
		next = pointer(s.cursor(a, "fleet:"+op.Name, fmt.Sprint(items[len(items)-1]["id"])))
	}
	prepareJSON(w, 200, map[string]any{"items": items, "next_cursor": next, "request_id": id})()
}

// Continuous host ticks include sleep and prevent wall-clock rollback during a
// daemon lifetime from extending task and derived-key deadlines.
func (s *Service) enforcementNow() time.Time {
	if s.fleet != nil && s.fleet.authorityActivated() {
		return s.fleet.authorityNow()
	}
	now := time.Now()
	if !s.clockWall.IsZero() {
		ticks, err := authorityTicks()
		if err != nil {
			return time.Unix(1<<62, 0)
		}
		continuous := s.clockWall.Add(time.Duration(ticks - s.clockTicks))
		if continuous.After(now) {
			return continuous
		}
	}
	return now
}
