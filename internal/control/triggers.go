package control

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JohanSardar/lamp/internal/surface"
)

// Triggers and schedules start agent work: a fresh sandbox from the owner's
// recipe running the owner's entrypoint, with the verified event as data.
// A payload can never pick a recipe, entrypoint, credential or network policy;
// those are fixed on the trigger by the owner. Each start is an ordinary Lamp
// job (plugin_id = the trigger plugin, recipe_id set), so job_get/list/cancel,
// artifacts and the dashboard see it like any other job.

var triggerPlugins = map[string]string{"pg_slack": "slack", "pg_linear": "linear", "pg_webhook": "webhook"}

type lifeTrigger struct {
	R map[string]any `json:"r"`
	// Seen maps verified source event IDs to the job they started (bounded).
	Seen  map[string]string `json:"seen,omitempty"`
	Order []string          `json:"order,omitempty"`
}
type lifeSchedule struct {
	R      map[string]any `json:"r"`
	Active string         `json:"active,omitempty"`
	Last   string         `json:"last,omitempty"`
	// Skipped counts occurrences not run because of overlap or missed policy.
	Skipped uint64 `json:"skipped,omitempty"`
}

const triggerSeenLimit = 2048
const entrypointLabel = "lamp.entrypoint"

func init() {
	lifeKinds["trigger"] = true
	lifeKinds["schedule"] = true
	lifeHandlers["trigger_create"] = (*Service).planTriggerCreate
	lifeHandlers["trigger_update"] = (*Service).planTriggerUpdate
	lifeHandlers["trigger_delete"] = (*Service).planTriggerDelete
	lifeHandlers["trigger_deliver"] = (*Service).planTriggerDeliver
	lifeHandlers["schedule_create"] = (*Service).planScheduleCreate
	lifeHandlers["schedule_update"] = (*Service).planScheduleUpdate
	lifeHandlers["schedule_delete"] = (*Service).planScheduleDelete
	lifeHandlers["schedule_run"] = (*Service).planScheduleRun
	lifeHandlers["schedule_pause"] = (*Service).planSchedulePause
	lifeHandlers["schedule_resume"] = (*Service).planScheduleResume
	for _, n := range []string{"trigger_create", "trigger_get", "trigger_list", "trigger_update", "trigger_delete", "trigger_deliver",
		"schedule_create", "schedule_get", "schedule_list", "schedule_update", "schedule_delete", "schedule_run", "schedule_pause", "schedule_resume"} {
		surface.CoreNames[n] = true
	}
}

func (s *Service) ownerOnly(a caller) error {
	if a.Principal != s.journal.Control.OwnerID {
		return reject("SCOPE_DENIED", "triggers and schedules start work with the owner's recipes; owner only")
	}
	return nil
}

// entrypoint is the owner-set argv the work runs, as a JSON array label.
func entrypoint(m map[string]any) ([]string, error) {
	labels, _ := m["labels"].(map[string]any)
	var argv []string
	if json.Unmarshal([]byte(textField(labels, entrypointLabel)), &argv) != nil || len(argv) == 0 || len(argv) > 64 {
		return nil, reject("INVALID_ARGUMENT", "labels."+entrypointLabel+" must be a JSON argv array")
	}
	return argv, nil
}

func (s *Service) workRecipe(a caller, m map[string]any) error {
	r := s.lifeFind(a, "recipe", textField(m, "recipe_id"))
	if r == nil || textField(r, "state") == "deleted" {
		return reject("NOT_FOUND", "recipe not found")
	}
	m["recipe_id"] = textField(r, "id")
	return nil
}

// ------------------------------------------------------------- triggers --

func (s *Service) triggerFields(a caller, m, in map[string]any) error {
	if err := onlyFields(in, "trigger_id owner_id plugin_id source_ref principal_id recipe_id verification_policy replay_window_seconds enabled"); err != nil {
		return err
	}
	for _, k := range []string{"plugin_id", "source_ref", "recipe_id", "verification_policy", "replay_window_seconds", "enabled"} {
		if v, ok := in[k]; ok {
			m[k] = v
		}
	}
	applyLabels(m, in)
	if p := textField(in, "principal_id"); p != "" && p != a.Principal {
		return reject("SCOPE_DENIED", "trigger work runs as its owner")
	}
	if _, ok := triggerPlugins[textField(m, "plugin_id")]; !ok {
		return reject("AUTH_TRANSPORT_UNSUPPORTED", "CI_PLUGIN_INCOMPATIBLE: trigger plugin must be pg_slack, pg_linear or pg_webhook")
	}
	if textField(m, "source_ref") == "" {
		return reject("INVALID_ARGUMENT", "source_ref names the Slack team, Linear organization or webhook source")
	}
	if _, ok := m["enabled"]; !ok {
		m["enabled"] = true
	}
	if _, ok := m["replay_window_seconds"]; !ok {
		m["replay_window_seconds"] = jsonNumber(300)
	}
	if w := uintField(m, "replay_window_seconds"); w == 0 || w > 3600 {
		return reject("INVALID_ARGUMENT", "replay_window_seconds must be 1–3600")
	}
	if _, err := s.triggerSecret(m); err != nil {
		return err
	}
	if _, err := entrypoint(m); err != nil {
		return err
	}
	m["state"] = map[bool]string{true: "active", false: "disabled"}[m["enabled"] == true]
	return s.workRecipe(a, m)
}

// triggerSecret reads the vendor signing secret from an owner-private file.
// The secret itself never enters the journal, the resource or any error.
func (s *Service) triggerSecret(m map[string]any) ([]byte, error) {
	policy, _ := m["verification_policy"].(map[string]any)
	path := textField(policy, "secret_file")
	if !filepath.IsAbs(path) {
		return nil, reject("INVALID_ARGUMENT", "verification_policy.secret_file must be an absolute owner-private path")
	}
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 {
		return nil, reject("INVALID_ARGUMENT", "verification secret must be a private (0600) file")
	}
	b, err := os.ReadFile(path)
	if err != nil || len(strings.TrimSpace(string(b))) < 16 {
		return nil, reject("INVALID_ARGUMENT", "verification secret unavailable or too short")
	}
	return []byte(strings.TrimSpace(string(b))), nil
}

func (s *Service) planTriggerCreate(a caller, in map[string]any) (lifePlan, error) {
	if err := s.ownerOnly(a); err != nil {
		return lifePlan{}, err
	}
	if s.life.Triggers == nil {
		s.life.Triggers = map[string]*lifeTrigger{}
	}
	m := s.lifeResource("tr", "trigger", a.Principal)
	if err := s.triggerFields(a, m, in); err != nil {
		return lifePlan{}, err
	}
	id := textField(m, "id")
	s.life.Triggers[id] = &lifeTrigger{R: m, Seen: map[string]string{}}
	return lifePlan{ref: lifeRef("trigger", id), result: map[string]any{"trigger_id": id}}, nil
}
func (s *Service) planTriggerUpdate(a caller, in map[string]any) (lifePlan, error) {
	m, err := s.ownedOrMissing(a, "trigger", textField(in, "trigger_id"))
	if err != nil {
		return lifePlan{}, err
	}
	if err = lifeRevision(m, in); err != nil {
		return lifePlan{}, err
	}
	if err = s.triggerFields(a, m, in); err != nil {
		return lifePlan{}, err
	}
	editResource(m)
	return lifePlan{ref: lifeRef("trigger", textField(m, "id")), result: map[string]any{}}, nil
}
func (s *Service) planTriggerDelete(a caller, in map[string]any) (lifePlan, error) {
	m, err := s.ownedOrMissing(a, "trigger", textField(in, "trigger_id"))
	if err != nil {
		return lifePlan{}, err
	}
	if err = lifeRevision(m, in); err != nil {
		return lifePlan{}, err
	}
	m["state"], m["enabled"] = "deleted", false
	editResource(m)
	return lifePlan{ref: lifeRef("trigger", textField(m, "id")), result: map[string]any{}}, nil
}

// delivery is a vendor request reduced to what verification needs.
type delivery struct {
	Body    []byte
	Headers http.Header
}

// verifyDelivery checks the vendor's signature scheme and freshness, and
// returns the stable source event ID used for deduplication.
func verifyDelivery(plugin string, secret []byte, d delivery, window time.Duration, now time.Time) (string, error) {
	mac := func(msg string) []byte {
		h := hmac.New(sha256.New, secret)
		h.Write([]byte(msg))
		return h.Sum(nil)
	}
	fresh := func(ts int64) error {
		if t := time.Unix(ts, 0); now.Sub(t) > window || t.Sub(now) > window {
			return errors.New("TRIGGER_UNAUTHORIZED: delivery outside the replay window")
		}
		return nil
	}
	hexEqual := func(got string, want []byte) bool {
		b, err := hex.DecodeString(got)
		return err == nil && hmac.Equal(b, want)
	}
	switch plugin {
	case "pg_slack":
		// Slack v0: HMAC-SHA256("v0:" + timestamp + ":" + body).
		ts, err := strconv.ParseInt(d.Headers.Get("X-Slack-Request-Timestamp"), 10, 64)
		if err != nil || fresh(ts) != nil {
			return "", errors.New("TRIGGER_UNAUTHORIZED: stale or missing Slack timestamp")
		}
		sig, ok := strings.CutPrefix(d.Headers.Get("X-Slack-Signature"), "v0=")
		if !ok || !hexEqual(sig, mac("v0:"+strconv.FormatInt(ts, 10)+":"+string(d.Body))) {
			return "", errors.New("TRIGGER_UNAUTHORIZED: Slack signature mismatch")
		}
		var ev struct {
			EventID string `json:"event_id"`
			Type    string `json:"type"`
		}
		if json.Unmarshal(d.Body, &ev) == nil && ev.EventID != "" {
			return "slack:" + ev.EventID, nil
		}
		if ev.Type == "url_verification" {
			return "", nil
		}
		// Slash commands and interactions are form posts without an event ID;
		// their trigger_id is unique per invocation.
		form, _ := url.ParseQuery(string(d.Body))
		if id := form.Get("trigger_id"); id != "" {
			return "slack:" + id, nil
		}
		return "", errors.New("TRIGGER_UNAUTHORIZED: Slack event has no stable ID")
	case "pg_linear":
		// Linear: hex HMAC-SHA256(body) in Linear-Signature; webhookTimestamp (ms) in body.
		if !hexEqual(d.Headers.Get("Linear-Signature"), mac(string(d.Body))) {
			return "", errors.New("TRIGGER_UNAUTHORIZED: Linear signature mismatch")
		}
		var ev struct {
			Timestamp int64  `json:"webhookTimestamp"`
			ID        string `json:"webhookId"`
		}
		if json.Unmarshal(d.Body, &ev) != nil || fresh(ev.Timestamp/1000) != nil {
			return "", errors.New("TRIGGER_UNAUTHORIZED: stale or missing Linear timestamp")
		}
		id := d.Headers.Get("Linear-Delivery")
		if id == "" {
			return "", errors.New("TRIGGER_UNAUTHORIZED: Linear delivery has no ID")
		}
		return "linear:" + id, nil
	case "pg_webhook":
		// Lamp scheme: X-Lamp-Signature: sha256=hex(HMAC(timestamp + "." + body)).
		id := d.Headers.Get("X-Lamp-Delivery")
		if sig, ok := strings.CutPrefix(d.Headers.Get("X-Lamp-Signature"), "sha256="); ok {
			ts, err := strconv.ParseInt(d.Headers.Get("X-Lamp-Timestamp"), 10, 64)
			if err != nil || fresh(ts) != nil {
				return "", errors.New("TRIGGER_UNAUTHORIZED: stale or missing webhook timestamp")
			}
			if !hexEqual(sig, mac(strconv.FormatInt(ts, 10)+"."+string(d.Body))) {
				return "", errors.New("TRIGGER_UNAUTHORIZED: webhook signature mismatch")
			}
		} else if sig, ok := strings.CutPrefix(d.Headers.Get("X-Hub-Signature-256"), "sha256="); ok {
			// Forge-style (GitHub, Forgejo, Gitea): body HMAC; replay is bounded
			// by delivery-ID deduplication, as these schemes carry no timestamp.
			if !hexEqual(sig, mac(string(d.Body))) {
				return "", errors.New("TRIGGER_UNAUTHORIZED: webhook signature mismatch")
			}
			for _, h := range []string{"X-GitHub-Delivery", "X-Forgejo-Delivery", "X-Gitea-Delivery"} {
				if id == "" {
					id = d.Headers.Get(h)
				}
			}
		} else {
			return "", errors.New("TRIGGER_UNAUTHORIZED: unsigned webhook")
		}
		if id == "" || len(id) > 200 {
			return "", errors.New("TRIGGER_UNAUTHORIZED: webhook delivery has no ID")
		}
		return "webhook:" + id, nil
	}
	return "", errors.New("TRIGGER_UNAUTHORIZED: unknown trigger plugin")
}

// triggerAdmit verifies a delivery and plans its work. Called under mu.
func (s *Service) triggerAdmit(a caller, triggerID string, d delivery) (lifePlan, error) {
	t := s.life.Triggers[triggerID]
	if t == nil || textField(t.R, "state") != "active" || textField(t.R, "owner_id") != a.Principal {
		return lifePlan{}, reject("UNAUTHENTICATED", "TRIGGER_UNAUTHORIZED: the source event cannot start this recipe")
	}
	secret, err := s.triggerSecret(t.R)
	if err != nil {
		return lifePlan{}, reject("UNAUTHENTICATED", "TRIGGER_UNAUTHORIZED: trigger verification unavailable")
	}
	window := time.Duration(uintField(t.R, "replay_window_seconds")) * time.Second
	event, err := verifyDelivery(textField(t.R, "plugin_id"), secret, d, window, time.Now())
	if err != nil {
		return lifePlan{}, reject("UNAUTHENTICATED", err.Error())
	}
	if event == "" {
		return lifePlan{ref: lifeRef("trigger", triggerID), result: map[string]any{}}, nil
	}
	// The verified payload must also come from the enrolled source.
	if !triggerSourceMatches(textField(t.R, "plugin_id"), textField(t.R, "source_ref"), d) {
		return lifePlan{}, reject("UNAUTHENTICATED", "TRIGGER_UNAUTHORIZED: event source is not the enrolled workspace")
	}
	if job := t.Seen[event]; job != "" {
		return lifePlan{ref: lifeRef("job", job), result: map[string]any{"job_id": job}}, nil
	}
	plan, err := s.planWork(a, textField(t.R, "plugin_id"), triggerID, event, t.R, d.Body)
	if err != nil {
		return plan, err
	}
	t.Seen[event] = plan.ref.Id
	t.Order = append(t.Order, event)
	for len(t.Order) > triggerSeenLimit {
		delete(t.Seen, t.Order[0])
		t.Order = t.Order[1:]
	}
	return plan, nil
}

func triggerSourceMatches(plugin, source string, d delivery) bool {
	var ev map[string]any
	if json.Unmarshal(d.Body, &ev) != nil {
		form, _ := url.ParseQuery(string(d.Body))
		return plugin == "pg_slack" && form.Get("team_id") == source
	}
	switch plugin {
	case "pg_slack":
		return textField(ev, "team_id") == source
	case "pg_linear":
		return textField(ev, "organizationId") == source
	}
	return true // a generic webhook's source is its own secret
}

func (s *Service) planTriggerDeliver(a caller, in map[string]any) (lifePlan, error) {
	t, err := s.ownedOrMissing(a, "trigger", textField(in, "trigger_id"))
	if err != nil {
		return lifePlan{}, err
	}
	b := s.life.Blobs[textField(in, "payload_blob_id")]
	if b == nil || textField(b.R, "owner_id") != a.Principal || textField(b.R, "state") != "ready" {
		return lifePlan{}, reject("NOT_FOUND", "payload blob not found")
	}
	body, err := os.ReadFile(s.objectPath(trimDigest(textField(b.R, "sha256"))))
	if err != nil || len(body) > 256<<10 {
		return lifePlan{}, reject("INVALID_ARGUMENT", "payload unavailable or over 256 KiB")
	}
	headers := http.Header{}
	meta, _ := in["signature_metadata"].(map[string]any)
	for k, v := range meta {
		if str, ok := v.(string); ok {
			headers.Set(k, str)
		}
	}
	return s.triggerAdmit(a, textField(t, "id"), delivery{Body: body, Headers: headers})
}

// triggerHookHTTP is the vendor-facing endpoint: POST /hooks/triggers/{id}.
// Vendors cannot hold Lamp keys, so the vendor signature is the authority;
// the trigger's owner is the caller and the source event ID is the
// idempotency key, so vendor retries replay instead of starting new work.
func (s *Service) triggerHookHTTP(w http.ResponseWriter, r *http.Request, id string) {
	triggerID := strings.TrimPrefix(r.URL.Path, "/hooks/triggers/")
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 256<<10))
	if err != nil || r.Method != "POST" {
		writeError(w, id, "INVALID_ARGUMENT", "POST a body of at most 256 KiB", nil)
		return
	}
	s.readLock()
	t := s.life.Triggers[triggerID]
	owner := ""
	if t != nil {
		owner = textField(t.R, "owner_id")
	}
	s.mu.Unlock()
	if t == nil {
		writeError(w, id, "UNAUTHENTICATED", "TRIGGER_UNAUTHORIZED: the source event cannot start this recipe", nil)
		return
	}
	d := delivery{Body: body, Headers: r.Header}
	var challenge struct {
		Type      string `json:"type"`
		Challenge string `json:"challenge"`
	}
	json.Unmarshal(body, &challenge)
	a := caller{Principal: owner}
	// Verify before any idempotent replay: a forged or stale request must be
	// refused even when its body matches an earlier valid delivery.
	s.readLock()
	secret, err := s.triggerSecret(t.R)
	plugin, window := textField(t.R, "plugin_id"), time.Duration(uintField(t.R, "replay_window_seconds"))*time.Second
	s.mu.Unlock()
	event := ""
	if err == nil {
		event, err = verifyDelivery(plugin, secret, d, window, time.Now())
	}
	if err != nil {
		writeError(w, id, "UNAUTHENTICATED", err.Error(), nil)
		return
	}
	key := "hook:" + triggerID + ":" + event
	if event == "" {
		key = "hook:" + triggerID + ":verification:" + sha256Hex(body)
	}
	opID, replayed, err := s.lifeStartWith(a, "trigger_deliver", map[string]any{"trigger_id": triggerID, "idempotency_key": key}, func(s *Service, a caller, _ map[string]any) (lifePlan, error) {
		return s.triggerAdmit(a, triggerID, d)
	})
	if err != nil {
		code := "UNAUTHENTICATED"
		var e *coreError
		if errors.As(err, &e) {
			code = e.code
		}
		writeError(w, id, code, err.Error(), nil)
		return
	}
	if challenge.Type == "url_verification" {
		prepareJSON(w, 200, map[string]any{"challenge": challenge.Challenge})()
		return
	}
	prepareJSON(w, 202, map[string]any{"operation_id": opID, "replayed": replayed, "request_id": id})()
}

// ------------------------------------------------------------ schedules --

func (s *Service) scheduleFields(a caller, m, in map[string]any) error {
	if err := onlyFields(in, "schedule_id owner_id principal_id recipe_id cron timezone dst_policy missed_policy overlap_policy next_run_at enabled"); err != nil {
		return err
	}
	if p := textField(in, "principal_id"); p != "" && p != a.Principal {
		return reject("SCOPE_DENIED", "scheduled work runs as its owner")
	}
	for _, k := range []string{"recipe_id", "cron", "timezone", "dst_policy", "missed_policy", "overlap_policy", "enabled"} {
		if v, ok := in[k]; ok {
			m[k] = v
		}
	}
	applyLabels(m, in)
	defaults := map[string]string{"timezone": "UTC", "dst_policy": "shift", "missed_policy": "skip", "overlap_policy": "skip"}
	for k, v := range defaults {
		if textField(m, k) == "" {
			m[k] = v
		}
	}
	if _, ok := m["enabled"]; !ok {
		m["enabled"] = true
	}
	allowed := map[string][]string{"dst_policy": {"shift", "skip"}, "missed_policy": {"skip", "run_once"}, "overlap_policy": {"skip", "allow", "cancel_previous"}}
	for k, values := range allowed {
		ok := false
		for _, v := range values {
			ok = ok || textField(m, k) == v
		}
		if !ok {
			return reject("INVALID_ARGUMENT", k+" must be one of "+strings.Join(values, ", "))
		}
	}
	c, err := parseCron(textField(m, "cron"))
	if err != nil {
		return reject("INVALID_ARGUMENT", err.Error())
	}
	loc, err := time.LoadLocation(textField(m, "timezone"))
	if err != nil {
		return reject("INVALID_ARGUMENT", "unknown IANA timezone")
	}
	if _, err := entrypoint(m); err != nil {
		return err
	}
	m["state"] = map[bool]string{true: "active", false: "disabled"}[m["enabled"] == true]
	if next, ok := c.next(time.Now(), loc, textField(m, "dst_policy")); ok {
		m["next_run_at"] = next.UTC().Format(time.RFC3339)
	} else {
		delete(m, "next_run_at")
	}
	return s.workRecipe(a, m)
}
func (s *Service) planScheduleCreate(a caller, in map[string]any) (lifePlan, error) {
	if err := s.ownerOnly(a); err != nil {
		return lifePlan{}, err
	}
	if s.life.Schedules == nil {
		s.life.Schedules = map[string]*lifeSchedule{}
	}
	m := s.lifeResource("sc", "schedule", a.Principal)
	if err := s.scheduleFields(a, m, in); err != nil {
		return lifePlan{}, err
	}
	id := textField(m, "id")
	s.life.Schedules[id] = &lifeSchedule{R: m}
	return lifePlan{ref: lifeRef("schedule", id), result: map[string]any{"schedule_id": id}}, nil
}
func (s *Service) scheduleMutate(a caller, in map[string]any, fn func(*lifeSchedule) error) (lifePlan, error) {
	m, err := s.ownedOrMissing(a, "schedule", textField(in, "schedule_id"))
	if err != nil {
		return lifePlan{}, err
	}
	if err = lifeRevision(m, in); err != nil {
		return lifePlan{}, err
	}
	sc := s.life.Schedules[textField(m, "id")]
	if err = fn(sc); err != nil {
		return lifePlan{}, err
	}
	editResource(sc.R)
	return lifePlan{ref: lifeRef("schedule", textField(m, "id")), result: map[string]any{}}, nil
}
func (s *Service) planScheduleUpdate(a caller, in map[string]any) (lifePlan, error) {
	return s.scheduleMutate(a, in, func(sc *lifeSchedule) error { return s.scheduleFields(a, sc.R, in) })
}
func (s *Service) planScheduleDelete(a caller, in map[string]any) (lifePlan, error) {
	return s.scheduleMutate(a, in, func(sc *lifeSchedule) error {
		sc.R["state"], sc.R["enabled"] = "deleted", false
		delete(sc.R, "next_run_at")
		return nil
	})
}
func (s *Service) planSchedulePause(a caller, in map[string]any) (lifePlan, error) {
	return s.scheduleMutate(a, in, func(sc *lifeSchedule) error {
		sc.R["state"], sc.R["enabled"] = "disabled", false
		return nil
	})
}
func (s *Service) planScheduleResume(a caller, in map[string]any) (lifePlan, error) {
	return s.scheduleMutate(a, in, func(sc *lifeSchedule) error {
		sc.R["enabled"] = true
		// Resuming never back-fills the paused interval.
		return s.scheduleFields(a, sc.R, map[string]any{})
	})
}

// schedule_run starts one occurrence now (or the named one) under the same
// overlap policy and occurrence deduplication as timer-fired runs.
func (s *Service) planScheduleRun(a caller, in map[string]any) (lifePlan, error) {
	m, err := s.ownedOrMissing(a, "schedule", textField(in, "schedule_id"))
	if err != nil {
		return lifePlan{}, err
	}
	if err = lifeRevision(m, in); err != nil {
		return lifePlan{}, err
	}
	at := time.Now().UTC().Truncate(time.Second)
	if v := textField(in, "occurrence_at"); v != "" {
		if at, err = time.Parse(time.RFC3339, v); err != nil {
			return lifePlan{}, reject("INVALID_ARGUMENT", "occurrence_at must be RFC 3339 UTC")
		}
	}
	plan, err := s.scheduleOccurrence(a, s.life.Schedules[textField(m, "id")], at)
	if err != nil {
		return lifePlan{}, err
	}
	if plan.ref.Id == "" {
		return lifePlan{ref: lifeRef("schedule", textField(m, "id")), result: map[string]any{}}, nil
	}
	return plan, nil
}

// scheduleOccurrence applies the overlap policy and plans one occurrence's
// work. An occurrence is identified by its UTC instant: it can start at most
// once on this host (Last) and, in a fleet, once across hosts (fleet claim).
func (s *Service) scheduleOccurrence(a caller, sc *lifeSchedule, at time.Time) (lifePlan, error) {
	key := at.UTC().Format(time.RFC3339)
	if sc.Last != "" && key <= sc.Last {
		return lifePlan{}, nil
	}
	if active := s.life.Jobs[sc.Active]; active != nil && !ciTerminal(textField(active.R, "state")) {
		switch textField(sc.R, "overlap_policy") {
		case "skip":
			sc.Last = key
			sc.Skipped++
			labels := object(sc.R["labels"])
			labels["lamp.skipped_overlap"] = strconv.FormatUint(sc.Skipped, 10)
			sc.R["labels"] = labels
			editResource(sc.R)
			return lifePlan{}, nil
		case "cancel_previous":
			active.Cancel = true
			editResource(active.R)
		}
	}
	plan, err := s.planWork(a, "pg_schedule", textField(sc.R, "id"), "schedule:"+key, sc.R, []byte(fmt.Sprintf(`{"schedule_id":%q,"occurrence_at":%q}`, textField(sc.R, "id"), key)))
	if err != nil {
		return plan, err
	}
	sc.Last, sc.Active = key, plan.ref.Id
	return plan, nil
}

// scheduler fires due occurrences once per second. It never runs while the
// journal is broken, and in a fleet it claims each occurrence through the
// replicated first-writer-wins home map, so only one host starts it.
func (s *Service) scheduler() {
	defer s.wg.Done()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-s.context().Done():
			return
		case <-tick.C:
		}
		now := time.Now()
		type due struct {
			id, owner string
			at        time.Time
		}
		var fire []due
		s.readLock()
		for id, sc := range s.life.Schedules {
			next, err := time.Parse(time.RFC3339, textField(sc.R, "next_run_at"))
			if err != nil || textField(sc.R, "state") != "active" || next.After(now) {
				continue
			}
			fire = append(fire, due{id, textField(sc.R, "owner_id"), next})
		}
		s.mu.Unlock()
		sort.Slice(fire, func(i, j int) bool { return fire[i].at.Before(fire[j].at) })
		for _, f := range fire {
			s.fireSchedule(f.id, f.owner, f.at, now)
		}
	}
}

func (s *Service) fireSchedule(id, owner string, at, now time.Time) {
	s.readLock()
	sc := s.life.Schedules[id]
	c, err := parseCron(textField(sc.R, "cron"))
	loc, lerr := time.LoadLocation(textField(sc.R, "timezone"))
	policy, missed := textField(sc.R, "dst_policy"), textField(sc.R, "missed_policy")
	s.mu.Unlock()
	if err != nil || lerr != nil {
		return
	}
	// Late by more than a minute (host asleep, controller down): the missed
	// policy decides between skipping the backlog and running one catch-up.
	late := now.Sub(at) > time.Minute
	run := !late || missed == "run_once"
	key := at.UTC().Format(time.RFC3339)
	if run && s.fleet != nil {
		if s.fleet.command(fleetCommand{HomeKey: "schedule:" + owner + ":" + id + ":" + key, Home: s.hostID()}) != nil {
			run = false // another host owns this occurrence, or no quorum
		}
	}
	if run {
		s.lifeStart(caller{Principal: owner}, "schedule_run", map[string]any{"schedule_id": id, "occurrence_at": key, "idempotency_key": "schedule:" + id + ":" + key, "expected_revision": jsonNumber(s.scheduleRevision(id))})
	}
	s.readLock()
	defer s.mu.Unlock()
	sc = s.life.Schedules[id]
	if sc == nil || textField(sc.R, "state") != "active" {
		return
	}
	if !run && sc.Last < key {
		sc.Last = key
		sc.Skipped++
	}
	from := at
	if late {
		from = now
	}
	if next, ok := c.next(from, loc, policy); ok {
		sc.R["next_run_at"] = next.UTC().Format(time.RFC3339)
	} else {
		delete(sc.R, "next_run_at")
	}
	editResource(sc.R)
	if s.saveLife() != nil {
		s.broken = true
	}
}
func (s *Service) scheduleRevision(id string) uint64 {
	s.readLock()
	defer s.mu.Unlock()
	return uintField(s.life.Schedules[id].R, "revision")
}

// ----------------------------------------------------------------- work --

// planWork records one unit of agent work as a job and returns its run plan.
func (s *Service) planWork(a caller, plugin, source, event string, def map[string]any, payload []byte) (lifePlan, error) {
	argv, err := entrypoint(def)
	if err != nil {
		return lifePlan{}, err
	}
	if uint64(len(s.life.Jobs)) >= s.quotas.Records {
		return lifePlan{}, reject("CONTROL_QUOTA_EXCEEDED", "retained job quota reached")
	}
	m := s.lifeResource("jb", "job", a.Principal)
	m["state"] = "queued"
	m["plugin_id"] = plugin
	m["external_id"] = source + "/" + event
	m["recipe_id"] = textField(def, "recipe_id")
	m["trust_class"] = "trigger"
	m["artifact_ids"] = []string{}
	m["source_identity"] = map[string]any{"issuer": "lamp:" + plugin, "repository_id": source, "event": event}
	m["labels"] = map[string]any{"source": source, "source_event": event}
	id := textField(m, "id")
	s.life.Jobs[id] = &ciJob{R: m, Plugin: plugin, Trigger: source, Argv: argv, Payload: base64.StdEncoding.EncodeToString(payload)}
	return lifePlan{ref: lifeRef("job", id), timeout: 6 * time.Hour, run: func(ctx context.Context) (map[string]any, error) {
		return map[string]any{"job_id": id}, s.runWork(ctx, id)
	}}, nil
}

func workJob(j *ciJob) bool { return j != nil && j.Trigger != "" }

// runWork starts the work in a fresh sandbox, retrying admission and sandbox
// creation failures with backoff (up to 5 attempts) before giving up.
func (s *Service) runWork(ctx context.Context, id string) (err error) {
	defer func() {
		if err != nil {
			// Errors here are Lamp's own messages (never payloads or secrets).
			msg := err.Error()
			if len(msg) > 300 {
				msg = msg[:300]
			}
			s.ciUpdate(id, func(j *ciJob) {
				labels := object(j.R["labels"])
				labels["lamp.error"] = msg
				j.R["labels"] = labels
			})
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
		if e := s.cleanupWork(cleanup, id); e != nil {
			err = errors.Join(err, e)
		}
	}()
	j := s.ciSnapshot(id)
	a := caller{Principal: textField(j.R, "owner_id")}
	delay := 15 * time.Second
	var sandbox string
	for attempt := 1; ; attempt++ {
		if s.ciSnapshot(id).Cancel {
			return context.Canceled
		}
		s.ciUpdate(id, func(j *ciJob) { j.R["state"] = "preparing"; j.Attempts = attempt })
		sandbox, err = s.workSandbox(ctx, id, a, attempt)
		if err == nil {
			break
		}
		if sandbox != "" {
			s.releaseWorkSandbox(ctx, id, sandbox)
		}
		if attempt >= 5 {
			return fmt.Errorf("work could not start after %d attempts: %w", attempt, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		delay *= 2
	}
	raw, _ := base64.StdEncoding.DecodeString(j.Payload)
	// The event is data for the agent, written in bounded chunks (argv limit).
	if _, err = s.guestPython(ctx, sandbox, "import os;os.makedirs('/var/tmp/lamp-trigger',exist_ok=True);open('/var/tmp/lamp-trigger/event.json','wb').close()"); err != nil {
		return err
	}
	for off := 0; off < len(raw); off += 64 << 10 {
		chunk := base64.StdEncoding.EncodeToString(raw[off:min(off+64<<10, len(raw))])
		if _, err = s.guestPython(ctx, sandbox, "import base64,sys;open('/var/tmp/lamp-trigger/event.json','ab').write(base64.b64decode(sys.argv[1]))", chunk); err != nil {
			return err
		}
	}
	if err = s.ciState(id, "running"); err != nil {
		return err
	}
	run, stop := context.WithCancel(ctx)
	defer stop()
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-run.Done():
				return
			case <-t.C:
				if s.ciSnapshot(id).Cancel {
					stop()
					return
				}
			}
		}
	}()
	plugin := textField(j.R, "plugin_id")
	argv := append([]string{"env", "HOME=/tmp", "PATH=/usr/local/bin:/usr/bin:/bin", "LAMP_TRIGGER_EVENT=/var/tmp/lamp-trigger/event.json", "LAMP_TRIGGER_SOURCE=" + strings.TrimPrefix(plugin, "pg_"), "LAMP_JOB_ID=" + id}, j.Argv...)
	res, err := s.guestRun(run, sandbox, argv...)
	if s.ciSnapshot(id).Cancel {
		s.ciUpdate(id, func(j *ciJob) { j.R["result"] = "cancelled" })
		return nil
	}
	if err != nil {
		return err
	}
	result := "success"
	if res.Exit != 0 {
		result = "failure"
	}
	s.ciUpdate(id, func(j *ciJob) { j.R["result"] = result })
	out := append(append(res.Stdout, []byte("\n--- stderr ---\n")...), res.Stderr...)
	s.workArtifact(id, "agent-output.log", out)
	return nil
}

// workAdmission serializes the owner-machine VM check with VM creation, so
// concurrent deliveries cannot both pass the ceiling before either VM exists.
var workAdmission sync.Mutex

func (s *Service) workSandbox(ctx context.Context, id string, a caller, attempt int) (string, error) {
	workAdmission.Lock()
	defer workAdmission.Unlock()
	for {
		if err := ownerMachineAdmission(); err == nil {
			break
		} else if !strings.Contains(err.Error(), "ceiling") && !strings.Contains(err.Error(), "available memory") {
			return "", err
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	j := s.ciSnapshot(id)
	code, out := s.internalHTTP(a, "POST", "/v1/sandboxes", map[string]string{"Idempotency-Key": fmt.Sprintf("work-create:%s:%d", id, attempt)}, map[string]any{"recipe_id": textField(j.R, "recipe_id")})
	if code >= 400 {
		return "", fmt.Errorf("sandbox admission failed (HTTP %d)", code)
	}
	opID, _ := out["operation_id"].(string)
	s.readLock()
	rec := s.journal.Operations[opID]
	if rec == nil {
		s.mu.Unlock()
		return "", errors.New("sandbox identity unavailable")
	}
	sandbox, done := rec.Resource.Id, s.done[opID]
	s.mu.Unlock()
	if err := s.ciUpdate(id, func(j *ciJob) { j.R["sandbox_id"] = sandbox }); err != nil {
		return sandbox, err
	}
	select {
	case <-ctx.Done():
		return sandbox, ctx.Err()
	case <-done:
	}
	s.readLock()
	sb := s.journal.Sandboxes[sandbox]
	s.mu.Unlock()
	if sb.State != "ready" {
		return sandbox, errors.New("fresh sandbox did not become ready")
	}
	s.ciUpdate(id, func(j *ciJob) { j.R["task_id"] = value(sb.TaskId) })
	return sandbox, nil
}

func (s *Service) releaseWorkSandbox(ctx context.Context, id, sandbox string) error {
	s.readLock()
	sb := s.journal.Sandboxes[sandbox]
	s.mu.Unlock()
	if sb.Id == "" || sb.State == "deleted" {
		return nil
	}
	why := "work-end-" + strconv.FormatUint(sb.Revision, 10)
	s.internalDelete(sb, why)
	s.readLock()
	sb = s.journal.Sandboxes[sandbox]
	s.mu.Unlock()
	if sb.State != "deleted" {
		return errors.New("work sandbox cleanup is not complete")
	}
	return s.releaseGuardAudit(ctx, sandbox)
}

func (s *Service) cleanupWork(ctx context.Context, id string) error {
	s.ciState(id, "collecting")
	j := s.ciSnapshot(id)
	if err := s.releaseWorkSandbox(ctx, id, textField(j.R, "sandbox_id")); err != nil {
		s.ciState(id, "cleanup_failed")
		return err
	}
	state := map[string]string{"success": "completed", "cancelled": "canceled"}[textField(j.R, "result")]
	if j.Cancel {
		state = "canceled"
	}
	if state == "" {
		state = "failed"
	}
	return s.ciState(id, state)
}

func (s *Service) workArtifact(id, name string, data []byte) {
	if len(data) > 8<<20 {
		data = data[len(data)-8<<20:]
	}
	sum, err := s.putObject(data)
	if err != nil {
		return
	}
	s.readLock()
	defer s.mu.Unlock()
	owner := textField(s.life.Jobs[id].R, "owner_id")
	b := s.newReadyBlob(owner, sum, int64(len(data)), "text/plain", "")
	plan, err := s.planArtifactCreate(caller{Principal: owner}, map[string]any{"job_id": id, "download_blob_id": textField(b, "id")})
	if err != nil {
		return
	}
	s.life.Artifacts[plan.ref.Id].R["labels"] = map[string]any{"native_output": name}
	j := s.life.Jobs[id]
	j.R["artifact_ids"] = appendOnce(stringsField(j.R, "artifact_ids"), plan.ref.Id)
	editResource(j.R)
	if s.saveLife() != nil {
		s.broken = true
	}
}

// workRecovery runs once at start: work interrupted by a controller loss is
// never resumed blindly (its agent may have acted); its sandbox is released
// and the job is marked failed, so the source can retry with a new event.
func (s *Service) workRecovery() {
	defer s.wg.Done()
	var pending []string
	s.readLock()
	for id, j := range s.life.Jobs {
		if workJob(j) && (!ciTerminal(textField(j.R, "state")) || textField(j.R, "state") == "cleanup_failed") {
			pending = append(pending, id)
		}
	}
	s.mu.Unlock()
	for _, id := range pending {
		ctx, cancel := context.WithTimeout(s.context(), 2*time.Minute)
		s.cleanupWork(ctx, id)
		cancel()
	}
}

// ----------------------------------------------------------------- cron --

// cron is a standard 5-field expression (minute hour day-of-month month
// day-of-week) with lists, ranges and steps, or @hourly/@daily/@weekly/
// @monthly/@yearly. Day-of-month and day-of-week combine with OR when both
// are restricted, as in Vixie cron.
type cron struct {
	min, hour, dom, mon, dow [64]bool
	domStar, dowStar         bool
}

func parseCron(expr string) (cron, error) {
	alias := map[string]string{"@hourly": "0 * * * *", "@daily": "0 0 * * *", "@midnight": "0 0 * * *", "@weekly": "0 0 * * 0", "@monthly": "0 0 1 * *", "@yearly": "0 0 1 1 *", "@annually": "0 0 1 1 *"}
	if v, ok := alias[strings.TrimSpace(expr)]; ok {
		expr = v
	}
	f := strings.Fields(expr)
	var c cron
	if len(f) != 5 {
		return c, errors.New("cron must have 5 fields")
	}
	limits := [][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}
	sets := []*[64]bool{&c.min, &c.hour, &c.dom, &c.mon, &c.dow}
	for i, field := range f {
		for _, part := range strings.Split(field, ",") {
			rng, stepText, hasStep := strings.Cut(part, "/")
			step := 1
			if hasStep {
				n, err := strconv.Atoi(stepText)
				if err != nil || n <= 0 {
					return c, errors.New("invalid cron step")
				}
				step = n
			}
			lo, hi := limits[i][0], limits[i][1]
			if rng != "*" {
				a, b, isRange := strings.Cut(rng, "-")
				x, err := strconv.Atoi(a)
				if err != nil {
					return c, errors.New("invalid cron value")
				}
				lo, hi = x, x
				if isRange {
					if hi, err = strconv.Atoi(b); err != nil {
						return c, errors.New("invalid cron range")
					}
				} else if hasStep {
					hi = limits[i][1]
				}
			}
			if lo < limits[i][0] || hi > limits[i][1] || lo > hi {
				return c, errors.New("cron value out of range")
			}
			for v := lo; v <= hi; v += step {
				sets[i][v] = true
			}
		}
	}
	if c.dow[7] {
		c.dow[0] = true
	}
	c.domStar, c.dowStar = f[2] == "*", f[4] == "*"
	return c, nil
}

func (c cron) day(t time.Time) bool {
	dom, dow := c.dom[t.Day()], c.dow[int(t.Weekday())]
	switch {
	case c.domStar && c.dowStar:
		return true
	case c.domStar:
		return dow
	case c.dowStar:
		return dom
	}
	return dom || dow
}

// next returns the first occurrence strictly after `after`. Matching is on
// local wall-clock time in loc. Daylight saving:
//   - a wall time that does not exist (spring forward) runs shifted by the gap
//     ("shift", e.g. 02:30 → 03:30) or not at all ("skip");
//   - a wall time that happens twice (fall back) runs once, at its first
//     instant.
func (c cron) next(after time.Time, loc *time.Location, dst string) (time.Time, bool) {
	local := after.In(loc)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	for i := 0; i < 366*5; i++ {
		d := day.AddDate(0, 0, i)
		if !c.mon[int(d.Month())] || !c.day(d) {
			continue
		}
		for h := 0; h < 24; h++ {
			if !c.hour[h] {
				continue
			}
			for m := 0; m < 60; m++ {
				if !c.min[m] {
					continue
				}
				t := time.Date(d.Year(), d.Month(), d.Day(), h, m, 0, 0, loc)
				if l := t.In(loc); l.Hour() != h || l.Minute() != m || l.Day() != d.Day() {
					if dst == "skip" {
						continue
					}
					// Go may resolve a nonexistent wall time to either side of the
					// gap; "shift" always means the requested time plus the gap.
					want := time.Date(d.Year(), d.Month(), d.Day(), h, m, 0, 0, time.UTC)
					got := time.Date(l.Year(), l.Month(), l.Day(), l.Hour(), l.Minute(), 0, 0, time.UTC)
					if got.Before(want) {
						t = t.Add(want.Sub(got))
					}
				} else if earlier := t.Add(-time.Hour); earlier.In(loc).Hour() == h && earlier.In(loc).Minute() == m {
					t = earlier // ambiguous wall time: its first instant
				}
				if t.After(after) {
					return t, true
				}
			}
		}
	}
	return time.Time{}, false
}
