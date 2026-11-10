package control

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/JohanSardar/lamp/pkg/api"
)

type storedEvent struct {
	Owner string    `json:"owner"`
	Event api.Event `json:"event"`
}

const eventRetention = 7 * 24 * time.Hour

func (s *Service) addEvents(delta *journal) {
	type item struct {
		kind, id, owner, state string
		revision               uint64
	}
	items := []item{}
	for id, r := range delta.Operations {
		items = append(items, item{"operation", id, r.Operation.OwnerId, string(r.Operation.State), r.Operation.Revision})
	}
	for id, r := range delta.Sandboxes {
		items = append(items, item{"sandbox", id, r.OwnerId, string(r.State), r.Revision})
	}
	for id, r := range delta.Execs {
		items = append(items, item{"exec", id, r.OwnerId, string(r.State), r.Revision})
	}
	if delta.Control != nil {
		c := delta.Control
		items = append(items, item{"fleet", c.Fleet.Id, c.OwnerID, string(c.Fleet.State), c.Fleet.Revision})
		for id, r := range c.Principals {
			items = append(items, item{"principal", id, c.OwnerID, string(r.State), r.Revision})
		}
		for id, r := range c.Memberships {
			items = append(items, item{"membership", id, c.OwnerID, string(r.State), r.Revision})
		}
		for id, r := range c.Keys {
			items = append(items, item{"key", id, value(r.Key.PrincipalId), string(r.Key.State), r.Key.Revision})
		}
		for id, r := range c.Approvals {
			items = append(items, item{"approval", id, value(r.PrincipalId), string(r.State), r.Revision})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].kind+"/"+items[i].id < items[j].kind+"/"+items[j].id })
	for _, r := range items {
		k := r.kind + "/" + r.id
		if r.revision <= s.eventRevisions[k] {
			continue
		}
		sequence := s.journal.EventSequence + uint64(len(delta.Events)) + 1
		eventID := fmt.Sprintf("hs_local-1-%d", sequence)
		raw, _ := json.Marshal(map[string]string{"state": r.state})
		typ := r.kind + ".state_changed"
		if r.kind == "operation" && terminal(api.OperationState(r.state)) {
			typ = "operation.completed"
		}
		if r.kind == "approval" && r.state == "consumed" {
			typ = "approval.consumed"
		}
		e := api.Event{Kind: "event", EventId: eventID, HostId: "hs_local", HostEpoch: 1, Sequence: sequence, ResourceKind: r.kind, ResourceId: r.id, ResourceRevision: r.revision, Type: api.EventType(typ), Data: raw, OccurredAt: timestamp(), Cursor: strconv.FormatUint(sequence, 10)}
		delta.Events = append(delta.Events, storedEvent{r.owner, e})
		s.eventRevisions[k] = r.revision
	}
	delta.EventSequence = s.journal.EventSequence + uint64(len(delta.Events))
}
func (s *Service) pruneEvents() {
	events := s.journal.Events
	cut := 0
	deadline := time.Now().Add(-eventRetention)
	for cut < len(events) {
		t, e := time.Parse(time.RFC3339Nano, events[cut].Event.OccurredAt)
		if e == nil && !t.Before(deadline) {
			break
		}
		cut++
	}
	if cut > 0 {
		s.journal.EventFloor = events[cut-1].Event.Sequence
		s.journal.Events = append([]storedEvent{}, events[cut:]...)
	}
}
func (s *Service) cursor(a caller, kind, pos string) string {
	raw, _ := json.Marshal([]string{a.Principal, a.KeyID, kind, pos})
	key, _ := hex.DecodeString(s.journal.Control.CursorSecret)
	mac := hmac.New(sha256.New, key)
	mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + hex.EncodeToString(mac.Sum(nil))
}
func (s *Service) readCursor(a caller, kind, cursor string) (string, error) {
	raw, sig, ok := strings.Cut(cursor, ".")
	data, e := base64.RawURLEncoding.DecodeString(raw)
	signature, e2 := hex.DecodeString(sig)
	if !ok || e != nil || e2 != nil {
		return "", fmt.Errorf("invalid cursor")
	}
	key, _ := hex.DecodeString(s.journal.Control.CursorSecret)
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	if !hmac.Equal(mac.Sum(nil), signature) {
		return "", fmt.Errorf("invalid cursor")
	}
	var parts []string
	if json.Unmarshal(data, &parts) != nil || len(parts) != 4 || parts[0] != a.Principal || parts[1] != a.KeyID || parts[2] != kind {
		return "", fmt.Errorf("foreign cursor")
	}
	return parts[3], nil
}
func parseQuery(r *http.Request) (url.Values, error) { return url.ParseQuery(r.URL.RawQuery) }
func (s *Service) eventAllowed(a caller, e storedEvent) bool {
	if e.Owner != a.Principal {
		return false
	}
	switch e.Event.ResourceKind {
	case "sandbox":
		return s.sandboxAllowed(a, s.journal.Sandboxes[e.Event.ResourceId])
	case "exec":
		return s.execAllowed(a, s.journal.Execs[e.Event.ResourceId])
	case "operation":
		return s.operationAllowed(a, s.journal.Operations[e.Event.ResourceId])
	}
	for _, sc := range s.scopes(a) {
		if sc.ResourceIds != nil {
			found := false
			for _, r := range *sc.ResourceIds {
				if string(r.Kind) == e.Event.ResourceKind && r.Id == e.Event.ResourceId {
					found = true
				}
			}
			if !found {
				return false
			}
		}
		if sc.SandboxIds != nil || sc.RecipeIds != nil {
			return false
		}
	}
	return true
}
func (s *Service) eventsHTTP(w http.ResponseWriter, r *http.Request, id string, in map[string]any) {
	a := requestCaller(r)
	limit := uint64(100)
	if _, ok := in["limit"]; ok {
		limit = uintField(in, "limit")
	}
	wait := uintField(in, "wait_ms")
	if limit > 1000 || wait > 25000 {
		writeError(w, id, "INVALID_ARGUMENT", "limit 1000; wait_ms 25000", nil)
		return
	}
	streaming := r.Header.Get("Accept") == "text/event-stream"
	if streaming && limit == 0 {
		writeError(w, id, "INVALID_ARGUMENT", "stream limit must be positive", nil)
		return
	}
	var position uint64
	first := true
	until := time.Now().Add(time.Duration(wait) * time.Millisecond)
	for {
		s.readLock()
		if s.broken || !s.can(a, "event.list") {
			s.mu.Unlock()
			if !streaming || first {
				writeError(w, id, "SCOPE_DENIED", "event authority unavailable", nil)
			}
			return
		}
		s.pruneEvents()
		if first && textField(in, "cursor") != "" {
			pos, err := s.readCursor(a, "events", textField(in, "cursor"))
			if err != nil {
				s.mu.Unlock()
				writeError(w, id, "CURSOR_EXPIRED", "cursor invalid; resync resource lists", nil)
				return
			}
			position, err = strconv.ParseUint(pos, 10, 64)
			if err != nil {
				s.mu.Unlock()
				writeError(w, id, "CURSOR_EXPIRED", "cursor invalid; resync resource lists", nil)
				return
			}
		}
		if position != 0 && position < s.journal.EventFloor {
			s.mu.Unlock()
			if first {
				writeError(w, id, "CURSOR_EXPIRED", "replay expired; resync resource lists", nil)
			}
			return
		}
		items := []api.Event{}
		encodedBytes := 0
		for _, stored := range s.journal.Events {
			e := stored.Event
			if e.Sequence <= position {
				continue
			}
			if s.eventAllowed(a, stored) {
				e.Cursor = s.cursor(a, "events", strconv.FormatUint(e.Sequence, 10))
				raw, _ := json.Marshal(e)
				if len(items) >= int(limit) || encodedBytes+len(raw) > 90000 {
					break
				}
				encodedBytes += len(raw)
				items = append(items, e)
			}
			position = e.Sequence
		}
		next := s.cursor(a, "events", strconv.FormatUint(position, 10))
		changed := s.changed
		if !streaming && (len(items) > 0 || !time.Now().Before(until) || limit == 0) {
			send := prepareJSON(w, 200, map[string]any{"items": items, "next_cursor": next, "request_id": id})
			s.mu.Unlock()
			send()
			return
		}
		s.mu.Unlock()
		if streaming {
			if first {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-store")
				w.WriteHeader(200)
				first = false
			}
			// No queue: a slow reader has a deadline and cannot hold the journal lock.
			controller := http.NewResponseController(w)
			controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
			for _, e := range items {
				raw, _ := json.Marshal(e)
				if _, err := fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", e.Cursor, e.Type, raw); err != nil {
					return
				}
			}
			if len(items) == 0 {
				if _, err := fmt.Fprintf(w, ": cursor %s\n\n", next); err != nil {
					return
				}
			}
			if err := controller.Flush(); err != nil {
				return
			}
			controller.SetWriteDeadline(time.Time{})
			if len(items) > 0 {
				continue
			}
		}
		delay := time.Until(until)
		if streaming || delay > time.Second {
			delay = time.Second
		}
		if delay <= 0 {
			delay = time.Millisecond
		}
		timer := time.NewTimer(delay)
		select {
		case <-r.Context().Done():
			timer.Stop()
			return
		case <-s.context().Done():
			timer.Stop()
			return
		case <-changed:
			timer.Stop()
		case <-timer.C:
		}
		first = false
	}
}
func (s *Service) waitOperation(w http.ResponseWriter, r *http.Request, id string, in map[string]any) {
	a := requestCaller(r)
	opID := textField(in, "operation_id")
	timeout := uintField(in, "timeout_ms")
	if timeout > 60000 {
		writeError(w, id, "INVALID_ARGUMENT", "timeout_ms limit is 60000", nil)
		return
	}
	until := time.Now().Add(time.Duration(timeout) * time.Millisecond)
	for {
		s.readLock()
		if s.broken {
			s.mu.Unlock()
			writeError(w, id, "DATA_UNAVAILABLE", "journal unavailable", nil)
			return
		}
		rec, ok := s.durableOps[opID]
		if !ok || !s.can(a, "operation.wait") || !s.operationAllowed(a, rec) {
			s.mu.Unlock()
			writeError(w, id, "NOT_FOUND", "operation not found", nil)
			return
		}
		if terminal(rec.Operation.State) || !time.Now().Before(until) {
			send := prepareJSON(w, 200, api.OperationGetResult{Data: rec.Operation, RequestId: id})
			s.mu.Unlock()
			send()
			return
		}
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
	}
}

func (s *Service) indexEventRevisions() {
	for id, r := range s.journal.Operations {
		s.eventRevisions["operation/"+id] = r.Operation.Revision
	}
	for id, r := range s.journal.Sandboxes {
		s.eventRevisions["sandbox/"+id] = r.Revision
	}
	for id, r := range s.journal.Execs {
		s.eventRevisions["exec/"+id] = r.Revision
	}
	c := s.journal.Control
	s.eventRevisions["fleet/"+c.Fleet.Id] = c.Fleet.Revision
	for id, r := range c.Principals {
		s.eventRevisions["principal/"+id] = r.Revision
	}
	for id, r := range c.Memberships {
		s.eventRevisions["membership/"+id] = r.Revision
	}
	for id, r := range c.Keys {
		s.eventRevisions["key/"+id] = r.Key.Revision
	}
	for id, r := range c.Approvals {
		s.eventRevisions["approval/"+id] = r.Revision
	}
}
