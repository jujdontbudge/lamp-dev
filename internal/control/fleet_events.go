package control

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"lamp.eilmer.si/internal/surface"
	"lamp.eilmer.si/pkg/api"
)

type fleetEventPage struct {
	items []api.Event
	next  uint64
	err   *api.ErrorDetail
}

type fleetEventCursor struct {
	Positions map[string]uint64 `json:"positions"`
	NextHost  int               `json:"next_host"`
}

// Peer pages use the public event API and the original bearer, so authorization
// occurs at the resource home. Routed requests read only that home's journal;
// they must not recursively fan out to the fleet.
func (f *Fleet) eventPage(r *http.Request, a caller, host string, position, limit uint64) fleetEventPage {
	s := f.service
	s.readLock()
	cursor := s.cursor(a, "events:"+host, strconv.FormatUint(position, 10))
	s.mu.Unlock()
	query := url.Values{"cursor": {cursor}, "limit": {strconv.FormatUint(limit, 10)}, "wait_ms": {"0"}}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	headers := r.Header.Clone()
	headers.Set("Accept", "application/json")
	headers.Set("X-Lamp-Fleet-Hop", "1")
	var raw []byte
	var status int
	if host == f.config.HostID {
		request := r.Clone(ctx)
		request.URL.Path = "/v1/events"
		request.URL.RawQuery = query.Encode()
		request.Header = headers
		reply := &responseSnapshot{header: http.Header{}}
		s.ServeHTTP(reply, request)
		status, raw = reply.status, reply.body.Bytes()
	} else {
		reply, err := f.peerRequest(ctx, host, "GET", "/v1/events?"+query.Encode(), nil, headers)
		if err != nil {
			return fleetEventPage{err: &api.ErrorDetail{Code: "HOST_UNAVAILABLE", Message: "event home unavailable"}}
		}
		defer reply.Body.Close()
		status = reply.StatusCode
		raw, err = io.ReadAll(io.LimitReader(reply.Body, 128<<10))
		if err != nil {
			return fleetEventPage{err: &api.ErrorDetail{Code: "HOST_UNAVAILABLE", Message: "event home disconnected"}}
		}
	}
	if status != http.StatusOK {
		var failure api.ErrorEnvelope
		if json.Unmarshal(raw, &failure) != nil || failure.Error.Code == "" {
			failure.Error = api.ErrorDetail{Code: "DATA_UNAVAILABLE", Message: "invalid event home response"}
		}
		return fleetEventPage{err: &failure.Error}
	}
	var result api.EventListResult
	if json.Unmarshal(raw, &result) != nil || result.NextCursor == nil {
		return fleetEventPage{err: &api.ErrorDetail{Code: "DATA_UNAVAILABLE", Message: "invalid event home page"}}
	}
	s.readLock()
	next, err := s.readCursor(a, "events:"+host, *result.NextCursor)
	s.mu.Unlock()
	sequence, parseErr := strconv.ParseUint(next, 10, 64)
	if err != nil || parseErr != nil || sequence < position {
		return fleetEventPage{err: &api.ErrorDetail{Code: "CURSOR_EXPIRED", Message: "event home cursor invalid; resync resource lists"}}
	}
	return fleetEventPage{items: result.Items, next: sequence}
}

// A signed vector tracks each host separately. An unreachable host keeps its
// position, and a page fetched but not emitted never advances past unsent events.
// The vector travels in every event cursor, so replay also works at another
// ingress after disconnecting in the middle of a page.
func (f *Fleet) eventsHTTP(w http.ResponseWriter, r *http.Request, id string, a caller, in map[string]any) {
	query, err := parseQuery(r)
	if err != nil || surface.Query(surface.ByName["event_list"], query, in) != nil {
		writeError(w, id, "INVALID_ARGUMENT", "invalid event query", nil)
		return
	}
	limit := uint64(100)
	if _, ok := in["limit"]; ok {
		limit = uintField(in, "limit")
	}
	wait := uintField(in, "wait_ms")
	streaming := r.Header.Get("Accept") == "text/event-stream"
	if limit > 1000 || wait > 25000 || streaming && limit == 0 {
		writeError(w, id, "INVALID_ARGUMENT", "limit 1000 (positive for streams); wait_ms 25000", nil)
		return
	}
	s := f.service
	vector := fleetEventCursor{Positions: map[string]uint64{}}
	s.readLock()
	if cursor := textField(in, "cursor"); cursor != "" {
		var raw string
		raw, err = s.readCursor(a, "fleet:events", cursor)
		if err == nil {
			err = json.Unmarshal([]byte(raw), &vector)
		}
		if vector.Positions == nil || vector.NextHost < 0 || vector.NextHost >= len(f.config.Hosts) {
			err = fmt.Errorf("invalid event positions")
		}
	}
	s.mu.Unlock()
	if err != nil {
		writeError(w, id, "CURSOR_EXPIRED", "fleet cursor invalid; resync resource lists", nil)
		return
	}
	positions := vector.Positions
	for host := range positions {
		if _, ok := f.peers[host]; !ok {
			writeError(w, id, "CURSOR_EXPIRED", "event host is no longer enrolled", nil)
			return
		}
	}
	encodeCursor := func() string {
		raw, _ := json.Marshal(vector)
		s.readLock()
		defer s.mu.Unlock()
		return s.cursor(a, "fleet:events", string(raw))
	}
	started := false
	fail := func(detail api.ErrorDetail) {
		if !started {
			prepareError(w, id, detail)()
		}
	}
	until := time.Now().Add(time.Duration(wait) * time.Millisecond)
	for {
		if r.Context().Err() != nil || f.ctx.Err() != nil {
			return
		}
		if err := f.project(); err != nil {
			fail(api.ErrorDetail{Code: "DATA_UNAVAILABLE", Message: "fleet authority unavailable"})
			return
		}
		s.readLock()
		allowed := !s.broken && s.can(a, "event.list")
		changed := s.changed
		s.mu.Unlock()
		if !allowed {
			fail(api.ErrorDetail{Code: "SCOPE_DENIED", Message: "event authority unavailable"})
			return
		}
		pages := make([]fleetEventPage, len(f.config.Hosts))
		var group sync.WaitGroup
		for index, host := range f.config.Hosts {
			group.Add(1)
			go func(index int, host string, position uint64) {
				defer group.Done()
				pages[index] = f.eventPage(r, a, host, position, limit)
			}(index, host.ID, positions[host.ID])
		}
		group.Wait()
		s.readLock()
		allowed = !s.broken && s.can(a, "event.list")
		s.mu.Unlock()
		if !allowed {
			fail(api.ErrorDetail{Code: "SCOPE_DENIED", Message: "event authority changed"})
			return
		}
		for index, page := range pages {
			if page.err != nil {
				switch page.err.Code {
				case "HOST_UNAVAILABLE", "QUORUM_REQUIRED", "UNAUTHENTICATED", "SCOPE_DENIED":
					// Preserve the home position until it can authorize reads again.
					continue
				default:
					fail(*page.err)
					return
				}
			}
			if len(page.items) == 0 {
				positions[f.config.Hosts[index].ID] = page.next
			}
		}
		items := []api.Event{}
		encodedBytes := 0
		// Preserve the next host across pages too, including a limit of one.
		startHost := vector.NextHost
	merge:
		for offset := 0; ; offset++ {
			progress := false
			for turn := range pages {
				index := (startHost + turn) % len(pages)
				page := pages[index]
				if offset >= len(page.items) || page.err != nil {
					continue
				}
				if uint64(len(items)) >= limit {
					break merge
				}
				host := f.config.Hosts[index].ID
				event := page.items[offset]
				before := positions[host]
				beforeNext := vector.NextHost
				positions[host] = event.Sequence
				vector.NextHost = (index + 1) % len(pages)
				if offset == len(page.items)-1 {
					positions[host] = page.next
				}
				// Normalize older journal entries that predate fleet host IDs too.
				event.HostId = host
				event.EventId = fmt.Sprintf("%s-%d-%d", host, event.HostEpoch, event.Sequence)
				event.Cursor = encodeCursor()
				raw, _ := json.Marshal(event)
				if encodedBytes+len(raw) > 90000 {
					positions[host] = before
					vector.NextHost = beforeNext
					break merge
				}
				encodedBytes += len(raw)
				items = append(items, event)
				progress = true
			}
			if !progress {
				break
			}
		}
		next := encodeCursor()
		if !streaming && (len(items) > 0 || limit == 0 || !time.Now().Before(until)) {
			prepareJSON(w, 200, map[string]any{"items": items, "next_cursor": next, "request_id": id})()
			return
		}
		if streaming {
			if !started {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-store")
				w.WriteHeader(200)
				started = true
			}
			controller := http.NewResponseController(w)
			_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
			for _, event := range items {
				raw, _ := json.Marshal(event)
				if _, err := fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", event.Cursor, event.Type, raw); err != nil {
					return
				}
			}
			if len(items) == 0 {
				if _, err := fmt.Fprintf(w, ": cursor %s\n\n", next); err != nil {
					return
				}
			}
			if controller.Flush() != nil {
				return
			}
			_ = controller.SetWriteDeadline(time.Time{})
			limit -= uint64(len(items))
			if limit == 0 || wait > 0 && !time.Now().Before(until) {
				return
			}
			if len(items) > 0 {
				continue
			}
		}
		delay := time.Until(until)
		if streaming && wait == 0 || delay > time.Second {
			delay = time.Second
		}
		if delay <= 0 {
			continue
		}
		timer := time.NewTimer(delay)
		select {
		case <-r.Context().Done():
			timer.Stop()
			return
		case <-f.ctx.Done():
			timer.Stop()
			return
		case <-changed:
			timer.Stop()
		case <-timer.C:
		}
	}
}
