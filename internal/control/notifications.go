package control

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/JohanSardar/lamp/pkg/api"
)

// NotificationConfig is the owner's private sink configuration (OPS-003).
// A sink's destination_ref must name one of these destinations; the URL and
// signing secret never enter the journal, events or API responses.
type NotificationConfig struct {
	// PublicURL is where the dashboard is reached; action links are
	// PublicURL + "/#/..." and carry no credential: opening one requires
	// signing in to the dashboard.
	PublicURL    string                             `json:"public_url"`
	Destinations map[string]NotificationDestination `json:"destinations"`
}
type NotificationDestination struct {
	// URL receives a signed JSON POST (plugin "webhook").
	URL string `json:"url"`
	// SecretFile holds the HMAC-SHA256 signing key (mode 0600).
	SecretFile string `json:"secret_file"`
}

func LoadNotificationConfig(path string) (*NotificationConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c NotificationConfig
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&c); err != nil {
		return nil, fmt.Errorf("notifications config: %w", err)
	}
	for ref, dest := range c.Destinations {
		u, err := url.Parse(dest.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" {
			return nil, fmt.Errorf("notifications config: destination %s needs an http(s) URL without credentials or query", ref)
		}
		if info, err := os.Stat(dest.SecretFile); err != nil || info.Mode().Perm()&0o077 != 0 {
			return nil, fmt.Errorf("notifications config: destination %s secret_file must exist with mode 0600", ref)
		}
	}
	return &c, nil
}

// Default retry schedule (architecture §17.4): 1, 5, 30, 300 and 1800 s after
// the first failed attempt, then failed. attempt_count is at most 6.
var notifyBackoff = []time.Duration{time.Second, 5 * time.Second, 30 * time.Second, 300 * time.Second, 1800 * time.Second}

var notifyEvents = map[string]bool{"task.done": true, "task.stuck": true, "task.needs_decision": true}

type notificationRecord struct {
	Notification api.Notification `json:"notification"`
	EventType    string           `json:"event_type"`
	ResourceRef  api.ResourceRef  `json:"resource_ref"`
	Summary      string           `json:"summary"`
	Link         string           `json:"link"`
	Attempts     []notifyAttempt  `json:"attempts"`
}
type notifyAttempt struct {
	At     string `json:"at"`
	Status int    `json:"status,omitempty"`
	Error  string `json:"error,omitempty"`
}

func (s *Service) initNotifications(config *NotificationConfig) {
	s.notifyConfig = config
	s.notifyState()
	s.wg.Add(1)
	go s.notifyLoop()
}

// notifyState returns the accountability record with notification maps
// present; journal replay can replace the record after Open. Under mu.
func (s *Service) notifyState() *accountability {
	c := s.journal.Accountability
	if c.Sinks == nil {
		c.Sinks = map[string]api.NotificationSink{}
	}
	if c.Notifications == nil {
		c.Notifications = map[string]*notificationRecord{}
	}
	if !c.NotifyStarted {
		// History before notifications existed is not replayed. An explicit
		// marker: an empty map does not survive the journal's omitempty.
		c.NotifyStarted = true
		c.NotifySequence = s.journal.EventSequence
	}
	return c
}

// notificationView is the API projection: the canonical resource plus the
// inspectable delivery trail (attempt times, HTTP status, redacted errors).
func notificationView(r *notificationRecord) map[string]any {
	m := object(r.Notification)
	m["event_type"] = r.EventType
	m["resource_ref"] = r.ResourceRef
	m["summary"] = r.Summary
	m["action_link"] = r.Link
	m["attempts"] = r.Attempts
	return m
}

func (s *Service) notifyResources(a caller, kind string) []map[string]any {
	items := []map[string]any{}
	c := s.notifyState()
	switch kind {
	case "notification_sink":
		for _, v := range c.Sinks {
			if v.OwnerId == a.Principal && v.State != "deleted" {
				items = append(items, object(v))
			}
		}
	case "notification":
		for _, v := range c.Notifications {
			if v.Notification.OwnerId == a.Principal {
				items = append(items, notificationView(v))
			}
		}
	}
	return items
}

func (s *Service) notifyMutation(a caller, op api.OperationDescriptor, in map[string]any) (api.ResourceRef, map[string]any, *string, []string, error) {
	c := s.notifyState()
	kind, verb, _ := strings.Cut(op.Scope, ".")
	ref := api.ResourceRef{Kind: api.ResourceKind(kind)}
	fail := func(code, msg string) (api.ResourceRef, map[string]any, *string, []string, error) {
		return ref, nil, nil, nil, reject(code, msg)
	}
	saved := []string{"$accountability"}
	if kind == "notification" {
		r, ok := c.Notifications[textField(in, "notification_id")]
		if !ok || r.Notification.OwnerId != a.Principal {
			return fail("NOT_FOUND", "notification not found")
		}
		ref.Id = r.Notification.Id
		if r.Notification.Revision != uintField(in, "expected_revision") {
			return fail("REVISION_CONFLICT", fmt.Sprintf("current revision is %d", r.Notification.Revision))
		}
		n := &r.Notification
		switch verb {
		case "acknowledge":
			n.State = "acknowledged"
			n.NextAttemptAt = nil
		case "retry":
			if n.State == "acknowledged" {
				return fail("INVALID_ARGUMENT", "acknowledged notifications are closed")
			}
			// A manual retry starts a fresh backoff round; the attempt
			// history (r.Attempts) is kept.
			n.State = "pending"
			n.NextAttemptAt = pointer(timestamp())
			n.AttemptCount = pointer(uint64(0))
		default:
			return fail("NOT_FOUND", "operation not installed")
		}
		n.Revision++
		n.UpdatedAt = timestamp()
		s.notifyWake()
		return ref, map[string]any{"notification": notificationView(r)}, nil, saved, nil
	}
	var sink api.NotificationSink
	if verb == "create" {
		if uint64(len(c.Sinks)) >= s.quotas.Records {
			return fail("CONTROL_QUOTA_EXCEEDED", "retained resource quota reached")
		}
		m := newResource("notification_sink", a.Principal)
		m["id"] = s.resourceID("ns")
		m["state"] = "active"
		assign(m, &sink)
	} else {
		var ok bool
		sink, ok = c.Sinks[textField(in, "notification_sink_id")]
		if !ok || sink.OwnerId != a.Principal || sink.State == "deleted" {
			return fail("NOT_FOUND", "notification sink not found")
		}
		if sink.Revision != uintField(in, "expected_revision") && verb != "test" {
			return fail("REVISION_CONFLICT", fmt.Sprintf("current revision is %d", sink.Revision))
		}
	}
	ref.Id = sink.Id
	switch verb {
	case "create", "update":
		m := object(sink)
		for _, k := range []string{"plugin_id", "destination_ref", "event_types", "enabled", "labels", "visibility"} {
			if v, ok := in[k]; ok {
				m[k] = v
			}
		}
		assign(m, &sink)
		if value(sink.PluginId) != "webhook" {
			return fail("INVALID_ARGUMENT", "installed notification plugin is webhook (signed HTTPS POST)")
		}
		if s.notifyConfig == nil {
			return fail("INVALID_ARGUMENT", "no notification destinations are enrolled on this host (--notifications-config)")
		}
		if _, ok := s.notifyConfig.Destinations[value(sink.DestinationRef)]; !ok {
			return fail("SCOPE_DENIED", "destination_ref is not an enrolled destination")
		}
		// Enrolled destinations are the host owner's (their phone, their pager):
		// another principal cannot route signed Lamp notifications to them.
		if a.Principal != s.journal.Control.OwnerID {
			return fail("SCOPE_DENIED", "enrolled notification destinations belong to the owner")
		}
		if sink.EventTypes == nil || len(*sink.EventTypes) == 0 {
			sink.EventTypes = &[]string{"task.done", "task.stuck", "task.needs_decision"}
		}
		for _, t := range *sink.EventTypes {
			if !notifyEvents[t] {
				return fail("INVALID_ARGUMENT", "event_types are task.done, task.stuck and task.needs_decision")
			}
		}
		if sink.Enabled == nil {
			sink.Enabled = pointer(true)
		}
		sink.State = "active"
		if !*sink.Enabled {
			sink.State = "disabled"
		}
	case "delete":
		sink.State = "deleted"
	case "test":
		if sink.State != "active" {
			return fail("INVALID_ARGUMENT", "sink is disabled")
		}
		id := s.queueNotification(sink, "notification.test", api.ResourceRef{Kind: "notification_sink", Id: sink.Id}, "", "Test notification from Lamp", s.dashboardLink("notifications"))
		s.notifyWake()
		return ref, map[string]any{"notification_id": id}, nil, saved, nil
	default:
		return fail("NOT_FOUND", "operation not installed")
	}
	if verb != "create" {
		editSink := object(sink)
		editResource(editSink)
		assign(editSink, &sink)
	}
	c.Sinks[sink.Id] = sink
	result := map[string]any{}
	if verb == "create" {
		result["notification_sink_id"] = sink.Id
	}
	return ref, result, nil, saved, nil
}

func (s *Service) dashboardLink(fragment string) string {
	base := ""
	if s.notifyConfig != nil {
		base = strings.TrimRight(s.notifyConfig.PublicURL, "/")
	}
	return base + "/#/" + fragment
}

func (s *Service) queueNotification(sink api.NotificationSink, eventType string, ref api.ResourceRef, taskID, summary, link string) string {
	m := newResource("notification", sink.OwnerId)
	m["id"] = s.resourceID("nt")
	m["state"] = "pending"
	m["sink_id"] = sink.Id
	m["attempt_count"] = 0
	m["next_attempt_at"] = timestamp()
	if taskID != "" {
		m["task_id"] = taskID
	}
	var n api.Notification
	assign(m, &n)
	s.notifyState().Notifications[n.Id] = &notificationRecord{Notification: n, EventType: eventType, ResourceRef: ref, Summary: summary, Link: link, Attempts: []notifyAttempt{}}
	return n.Id
}

func (s *Service) notifyWake() {
	select {
	case s.notifyKick <- struct{}{}:
	default:
	}
}

// describeEvent turns a task-level event into a human summary and the
// dashboard place where the person acts on it.
func (s *Service) describeEvent(e api.Event) (taskID, summary, link string) {
	c := s.journal.Control
	switch e.ResourceKind {
	case "task":
		taskID = e.ResourceId
		t := s.journal.Accountability.Tasks[e.ResourceId]
		summary = fmt.Sprintf("Task %s is %s", e.ResourceId, t.State)
		if e.Type == "task.stuck" {
			summary = fmt.Sprintf("Task %s is blocked by its watchdog", e.ResourceId)
		}
		link = s.dashboardLink("tasks/" + e.ResourceId)
	case "approval":
		ap := c.Approvals[e.ResourceId]
		taskID = value(ap.TaskId)
		effect := "an action"
		if p := ap.ActionPlan; p != nil {
			effect = value(p.Operation)
			if p.ResourceRef != nil {
				effect += " on " + string(p.ResourceRef.Kind) + " " + p.ResourceRef.Id
			}
			if p.Destination != nil {
				if raw, err := json.Marshal(p.Destination); err == nil {
					effect += " to " + string(raw)
				}
			}
		}
		summary = "Decision needed: approve " + effect
		link = s.dashboardLink("approvals/" + e.ResourceId)
	case "session":
		if w := c.Sessions[e.ResourceId]; w != nil {
			taskID = textField(w.Resource, "task_id")
		}
		summary = fmt.Sprintf("Session %s is waiting for you", e.ResourceId)
		link = s.dashboardLink("sessions/" + e.ResourceId)
	}
	return
}

type delivery struct {
	id, url, secretFile string
	body                []byte
}

func (s *Service) notifyLoop() {
	defer s.wg.Done()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for {
		select {
		case <-s.context().Done():
			return
		case <-tick.C:
		case <-s.notifyKick:
		}
		s.readLock()
		if s.broken {
			s.mu.Unlock()
			continue
		}
		c := s.notifyState()
		changed := false
		for _, se := range s.journal.Events {
			e := se.Event
			if e.Sequence <= c.NotifySequence {
				continue
			}
			c.NotifySequence = e.Sequence
			changed = true
			if !notifyEvents[string(e.Type)] {
				continue
			}
			for _, sink := range c.Sinks {
				if sink.OwnerId != se.Owner || sink.State != "active" || sink.EventTypes == nil {
					continue
				}
				for _, t := range *sink.EventTypes {
					if t == string(e.Type) {
						taskID, summary, link := s.describeEvent(e)
						id := s.queueNotification(sink, t, api.ResourceRef{Kind: api.ResourceKind(e.ResourceKind), Id: e.ResourceId}, taskID, summary, link)
						c.Notifications[id].Notification.EventId = pointer(e.EventId)
					}
				}
			}
		}
		now := time.Now()
		var due []delivery
		for id, r := range c.Notifications {
			n := r.Notification
			if n.State != "pending" || s.notifyInflight[id] {
				continue
			}
			if at, err := time.Parse(time.RFC3339Nano, value(n.NextAttemptAt)); err == nil && at.After(now) {
				continue
			}
			sink, ok := c.Sinks[value(n.SinkId)]
			var dest NotificationDestination
			if ok && s.notifyConfig != nil {
				dest, ok = s.notifyConfig.Destinations[value(sink.DestinationRef)]
			}
			if !ok {
				s.recordAttempt(r, 0, "destination is no longer enrolled")
				changed = true
				continue
			}
			body, _ := json.Marshal(map[string]any{
				"notification_id": id, "event_type": r.EventType, "event_id": value(n.EventId),
				"task_id": value(n.TaskId), "resource": r.ResourceRef, "summary": r.Summary,
				"action_link": r.Link, "attempt": value(n.AttemptCount) + 1, "sent_at": timestamp(),
			})
			s.notifyInflight[id] = true
			due = append(due, delivery{id, dest.URL, dest.SecretFile, body})
		}
		if changed {
			if err := s.save("$accountability"); err != nil {
				s.broken = true
			}
		}
		s.mu.Unlock()
		for _, d := range due {
			status, errText := s.deliver(client, d)
			s.readLock()
			delete(s.notifyInflight, d.id)
			if r := s.notifyState().Notifications[d.id]; r != nil && r.Notification.State == "pending" && !s.broken {
				s.recordAttempt(r, status, errText)
				if err := s.save("$accountability"); err != nil {
					s.broken = true
				}
			}
			s.mu.Unlock()
		}
	}
}

func (s *Service) deliver(client *http.Client, d delivery) (int, string) {
	secret, err := os.ReadFile(d.secretFile)
	if err != nil {
		return 0, "signing secret unavailable"
	}
	mac := hmac.New(sha256.New, bytes.TrimSpace(secret))
	mac.Write(d.body)
	ctx, cancel := context.WithTimeout(s.context(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", d.url, bytes.NewReader(d.body))
	if err != nil {
		return 0, "invalid destination"
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lamp-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	req.Header.Set("X-Lamp-Notification", d.id)
	resp, err := client.Do(req)
	if err != nil {
		// Transport errors can include the URL; report only the class.
		msg := "destination unreachable"
		if ctx.Err() != nil {
			msg = "destination timed out"
		}
		return 0, msg
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return resp.StatusCode, fmt.Sprintf("destination answered HTTP %d", resp.StatusCode)
	}
	return resp.StatusCode, ""
}

// recordAttempt runs under mu.
func (s *Service) recordAttempt(r *notificationRecord, status int, errText string) {
	n := &r.Notification
	count := value(n.AttemptCount) + 1
	n.AttemptCount = &count
	r.Attempts = append(r.Attempts, notifyAttempt{At: timestamp(), Status: status, Error: errText})
	if len(r.Attempts) > 20 {
		r.Attempts = r.Attempts[len(r.Attempts)-20:]
	}
	switch {
	case errText == "":
		n.State = "delivered"
		n.NextAttemptAt = nil
	case int(count) > len(notifyBackoff):
		n.State = "failed"
		n.NextAttemptAt = nil
	default:
		n.NextAttemptAt = pointer(time.Now().Add(notifyBackoff[count-1]).UTC().Format(time.RFC3339Nano))
	}
	n.Revision++
	n.UpdatedAt = timestamp()
}

// withCountdowns adds the dashboard's observable timers to a sandbox
// projection: when the idle action fires if nothing happens, and how long
// saved (suspended) state is kept. Both come from the live idle monitor.
func (s *Service) withCountdowns(sb api.Sandbox) map[string]any {
	m := object(sb)
	action, grace, retention, expires := lifecycleOf(sb)
	idle := map[string]any{"action": action, "grace_seconds": grace}
	if s.life != nil {
		if l := s.life.Sandboxes[sb.Id]; l != nil {
			if !l.idleFrom.IsZero() && sb.State == "ready" {
				idle["idle_since"] = l.idleFrom.UTC().Format(time.RFC3339Nano)
				idle["fires_at"] = l.idleFrom.Add(time.Duration(grace) * time.Second).UTC().Format(time.RFC3339Nano)
			}
			if at, err := time.Parse(time.RFC3339Nano, l.SuspendedAt); err == nil && sb.State == "suspended" {
				m["saved_state_until"] = at.Add(time.Duration(retention) * time.Second).UTC().Format(time.RFC3339Nano)
			}
		}
	}
	if !expires.IsZero() {
		idle["expires_at"] = expires.UTC().Format(time.RFC3339Nano)
	}
	m["idle_countdown"] = idle
	return m
}
