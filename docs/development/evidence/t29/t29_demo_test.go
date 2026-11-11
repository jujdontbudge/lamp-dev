package control

// T29 explicit manual demonstration, not a smoke gate. Copied into
// internal/control only for the run (as T26 did); never part of CI.
//
// Real things: a self-hosted Forgejo 17 (plain process, disposable sqlite),
// the official forgejo-runner 13 `one-job`, Lamp's production CI controller on
// real disposable Firecracker VMs, and real signed HTTP deliveries into the
// production trigger endpoint. Slack and Linear signatures are computed here
// with each vendor's documented scheme; they are not deliveries from Slack's
// or Linear's servers.
import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/JohanSardar/lamp/internal/ci"
	pb "github.com/JohanSardar/lamp/pkg/workerpb"
)

type t29Live struct {
	Worker        WorkerConfig  `json:"worker"`
	StateAdmin    []string      `json:"state_admin"`
	NetworkReader []string      `json:"network_reader"`
	Repository    ci.Repository   `json:"repository"`
	Repositories  []ci.Repository `json:"repositories"`
	Template      string        `json:"template"`
	Forge         string        `json:"forge"`
	TokenFile     string        `json:"token_file"`
}

func t29Call(t *testing.T, s *Service, method, path string, body any, rev string) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r.Header.Set("Idempotency-Key", newID("t29"))
	if rev != "" {
		r.Header.Set("If-Match", rev)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}
func t29Hook(s *Service, trigger string, body []byte, headers map[string]string) (int, map[string]any) {
	r := httptest.NewRequest("POST", "/hooks/triggers/"+trigger, bytes.NewReader(body))
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}
func t29Forge(t *testing.T, live t29Live, method, path string, body any) map[string]any {
	t.Helper()
	tok, _ := os.ReadFile(live.TokenFile)
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, live.Forge+"/api/v1/repos/"+live.Repository.Name+path, bytes.NewReader(raw))
	req.Header.Set("Authorization", "token "+strings.TrimSpace(string(tok)))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal("forge unavailable")
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	var out map[string]any
	json.Unmarshal(b, &out)
	if res.StatusCode >= 300 {
		t.Fatalf("forge %s %s HTTP %d", method, path, res.StatusCode)
	}
	return out
}
func t29Wait(t *testing.T, s *Service, out map[string]any) map[string]any {
	t.Helper()
	opID := textField(out, "operation_id")
	s.readLock()
	done := s.done[opID]
	s.mu.Unlock()
	if done != nil {
		<-done
	}
	s.readLock()
	defer s.mu.Unlock()
	rec := s.journal.Operations[opID]
	if rec == nil || rec.Operation.State != "succeeded" {
		t.Fatalf("operation %s did not succeed", opID)
	}
	var result map[string]any
	json.Unmarshal(*rec.Operation.Outcome.Result, &result)
	return result
}
func t29Job(s *Service, pred func(*ciJob) bool) (string, ciJob) {
	s.readLock()
	defer s.mu.Unlock()
	for id, j := range s.life.Jobs {
		if pred(j) {
			b, _ := json.Marshal(j)
			var c ciJob
			json.Unmarshal(b, &c)
			return id, c
		}
	}
	return "", ciJob{}
}
var t29Store *Service

func t29Until(t *testing.T, what string, d time.Duration, f func() bool) {
	t.Helper()
	defer func() {
		if t.Failed() && t29Store != nil {
			t29Store.readLock()
			for id, j := range t29Store.life.Jobs {
				if j.Trigger != "" {
					t.Logf("work %s trigger=%s state=%s attempts=%d sandbox=%s labels=%v", id, j.Trigger, textField(j.R, "state"), j.Attempts, textField(j.R, "sandbox_id"), j.R["labels"])
				}
			}
			t29Store.mu.Unlock()
		}
	}()
	deadline := time.Now().Add(d)
	for !f() {
		if time.Now().After(deadline) {
			t.Logf("vms on host: %s", func() string { b, _ := exec.Command("pgrep", "-c", "firecracker").Output(); return strings.TrimSpace(string(b)) }())
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Second)
	}
}
func t29Report(t *testing.T, s *Service, id string) {
	_, j := t29Job(s, func(j *ciJob) bool { return textField(j.R, "id") == id })
	s.readLock()
	sb := s.journal.Sandboxes[textField(j.R, "sandbox_id")]
	var outs []string
	for _, a := range stringsField(j.R, "artifact_ids") {
		if art := s.life.Artifacts[a]; art != nil {
			labels, _ := art.R["labels"].(map[string]any)
			outs = append(outs, fmt.Sprintf("%s(%s,%d bytes)", textField(labels, "native_output"), textField(art.R, "state"), uintField(art.R, "present_bytes")))
		}
	}
	s.mu.Unlock()
	if e := textField(object(j.R["labels"]), "lamp.error"); e != "" {
		t.Logf("job %s error: %s", id, e)
	}
	defer func() {
		if !ciTerminal(textField(j.R, "state")) {
			t.Logf("job %s still %s", id, textField(j.R, "state"))
		}
	}()
	t.Logf("job %s plugin=%s external=%s state=%s result=%s trust=%s runner=%s sandbox=%s(%s) outputs=%v",
		id, textField(j.R, "plugin_id"), textField(j.R, "external_id"), textField(j.R, "state"), textField(j.R, "result"), textField(j.R, "trust_class"), textField(j.R, "runner_ref"), sb.Id, sb.State, outs)
}
func t29Runners(t *testing.T, live t29Live) int {
	tok, _ := os.ReadFile(live.TokenFile)
	req, _ := http.NewRequest("GET", live.Forge+"/api/v1/repos/"+live.Repository.Name+"/actions/runners", nil)
	req.Header.Set("Authorization", "token "+strings.TrimSpace(string(tok)))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	var list []any
	if json.Unmarshal(b, &list) != nil {
		var obj struct{ Runners []any }
		json.Unmarshal(b, &obj)
		list = obj.Runners
	}
	return len(list)
}

func TestT29Demonstration(t *testing.T) {
	raw, err := os.ReadFile(os.Getenv("LAMP_T29_LIVE_CONFIG"))
	if err != nil {
		t.Skip("manual demonstration; set LAMP_T29_LIVE_CONFIG")
	}
	var live t29Live
	if json.Unmarshal(raw, &live) != nil {
		t.Fatal("invalid live config")
	}
	root := t.TempDir()
	config := Config{StateDir: filepath.Join(root, "state"), Worker: live.Worker, StateAdmin: live.StateAdmin,
		CI: &ci.Config{NetworkReader: live.NetworkReader, Repositories: append([]ci.Repository{live.Repository}, live.Repositories...), PollSeconds: 3}}
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	t29Store = s
	t.Logf("enrolled plugins: %v", func() (out []string) {
		for p := range s.forges {
			out = append(out, p)
		}
		return
	}())

	// Demo-only observer: the runner's own output tail from inside the VM.
	go func() {
		for range time.Tick(45 * time.Second) {
			_, j := t29Job(s, func(j *ciJob) bool { return j.Command != "" && textField(j.R, "state") == "running" })
			if j.Command == "" {
				continue
			}
			sb := textField(j.R, "sandbox_id")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			q := s.workerRequest(ctx, "t29-observe", sb, s.sandboxGeneration(sb))
			q.Command = &pb.Request_Inspect{Inspect: &pb.Inspect{CommandOperationId: j.Command}}
			if r, e := s.worker.call(ctx, q); e == nil && r.Command != nil {
				tail := func(b []byte) string {
					if len(b) > 1500 {
						b = b[len(b)-1500:]
					}
					return string(b)
				}
				t.Logf("runner output tail (%s):\n%s\n--stderr--\n%s", textField(j.R, "id"), tail(r.Command.StdoutTail), tail(r.Command.StderrTail))
			}
			cancel()
		}
	}()

	if os.Getenv("LAMP_T29_ONLY") == "systems" {
		defer s.Close()
		// One real native job per further self-hosted system, each on a fresh VM.
		plugins := []string{"pg_gitea", "pg_jenkins", "pg_woodpecker"}
		if only := os.Getenv("LAMP_T29_PLUGINS"); only != "" {
			plugins = strings.Split(only, ",")
		}
		for _, plugin := range plugins {
			var id string
			t29Until(t, plugin+" job", 25*time.Minute, func() bool {
				id, _ = t29Job(s, func(j *ciJob) bool { return j.Plugin == plugin && ciTerminal(textField(j.R, "state")) })
				return id != ""
			})
			t29Report(t, s, id)
		}
		if os.Getenv("LAMP_T29_PLUGINS") != "" {
			t.Logf("forge runners still registered: %d", t29Runners(t, live))
			return
		}
		t29Triggers(t, s, live, root)
		return
	}
	if os.Getenv("LAMP_T29_ONLY") == "gitlab" {
		defer s.Close()
		t29GitLab(t, s, live)
		return
	}
	if os.Getenv("LAMP_T29_ONLY") == "triggers" {
		defer s.Close()
		t29Triggers(t, s, live, root)
		return
	}
	// 1. A pushed Forgejo workflow: the controller finds the waiting job,
	// verifies it, and runs it on a fresh VM with a one-job ephemeral runner.
	before := t29Runners(t, live)
	var first string
	t29Until(t, "first Forgejo job to finish", 8*time.Minute, func() bool {
		id, j := t29Job(s, func(j *ciJob) bool { return j.Plugin == "pg_forgejo" && j.Workflow == ".forgejo/workflows/ci.yml" })
		first = id
		return id != "" && ciTerminal(textField(j.R, "state"))
	})
	t29Report(t, s, first)
	native := t29Forge(t, live, "GET", "/actions/runs/1", nil)
	t.Logf("native Forgejo run 1: status=%v (runners registered before=%d after=%d)", native["status"], before, t29Runners(t, live))

	// 2. Cancellation through Lamp is native cancellation in Forgejo.
	t29Forge(t, live, "POST", "/actions/workflows/slow.yml/dispatches", map[string]any{"ref": "main"})
	var slow string
	t29Until(t, "slow job running", 20*time.Minute, func() bool {
		id, j := t29Job(s, func(j *ciJob) bool { return j.Workflow == ".forgejo/workflows/slow.yml" && !ciTerminal(textField(j.R, "state")) })
		slow = id
		return id != "" && textField(j.R, "state") == "running"
	})
	time.Sleep(10 * time.Second)
	_, j := t29Job(s, func(j *ciJob) bool { return textField(j.R, "id") == slow })
	if code, out := t29Call(t, s, "POST", "/v1/jobs/"+slow+":cancel", map[string]any{}, strconv.FormatUint(uintField(j.R, "revision"), 10)); code >= 300 {
		t.Fatalf("job cancel HTTP %d %v", code, out)
	}
	t29Until(t, "canceled job cleanup", 3*time.Minute, func() bool {
		_, j := t29Job(s, func(j *ciJob) bool { return textField(j.R, "id") == slow })
		return ciTerminal(textField(j.R, "state"))
	})
	t29Report(t, s, slow)
	_, j = t29Job(s, func(j *ciJob) bool { return textField(j.R, "id") == slow })
	native = t29Forge(t, live, "GET", fmt.Sprintf("/actions/runs/%d", j.Run), nil)
	t.Logf("native Forgejo run %d after Lamp cancel: status=%v", j.Run, native["status"])

	// 3. Controller loss mid-job: a new controller never reruns the runner; it
	// cancels the native job, deregisters the runner and deletes the VM.
	t29Forge(t, live, "POST", "/actions/workflows/slow.yml/dispatches", map[string]any{"ref": "main"})
	var lost string
	t29Until(t, "second slow job running", 20*time.Minute, func() bool {
		id, j := t29Job(s, func(j *ciJob) bool { return j.Workflow == ".forgejo/workflows/slow.yml" && !ciTerminal(textField(j.R, "state")) })
		lost = id
		return id != "" && textField(j.R, "state") == "running"
	})
	s.Close()
	t.Logf("controller stopped while job %s was running", lost)
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	t29Until(t, "recovery cleanup", 3*time.Minute, func() bool {
		_, j := t29Job(s, func(j *ciJob) bool { return textField(j.R, "id") == lost })
		return ciTerminal(textField(j.R, "state"))
	})
	t29Report(t, s, lost)
	_, j = t29Job(s, func(j *ciJob) bool { return textField(j.R, "id") == lost })
	native = t29Forge(t, live, "GET", fmt.Sprintf("/actions/runs/%d", j.Run), nil)
	t.Logf("native Forgejo run %d after controller recovery: status=%v; runners left registered=%d", j.Run, native["status"], t29Runners(t, live))

	t29Triggers(t, s, live, root)
}

func t29Triggers(t *testing.T, s *Service, live t29Live, root string) {
	// 4. Triggers start agent work from the owner's recipe.
	code, out := t29Call(t, s, "POST", "/v1/recipes", map[string]any{"name": "t29-agent", "template_id": live.Template, "resources": map[string]any{"vcpus": 2, "memory_mib": 4096, "disk_gib": 20, "pids_max": 4096}}, "")
	if code >= 300 {
		t.Fatalf("recipe HTTP %d %v", code, out)
	}
	recipe := textField(t29Wait(t, s, out), "recipe_id")
	secret := filepath.Join(root, "signing-secret")
	os.WriteFile(secret, []byte(newID("whsec")+newID("x")), 0600)
	key, _ := os.ReadFile(secret)
	agent := `["sh","-c","echo source=$LAMP_TRIGGER_SOURCE job=$LAMP_JOB_ID; cat $LAMP_TRIGGER_EVENT; echo; test ! -e /var/tmp/lamp-previous-job && echo fresh-sandbox; echo agent-done"]`
	mk := func(plugin, source string) string {
		code, out := t29Call(t, s, "POST", "/v1/triggers", map[string]any{"plugin_id": plugin, "source_ref": source, "recipe_id": recipe,
			"verification_policy": map[string]any{"secret_file": secret}, "labels": map[string]any{entrypointLabel: agent}}, "")
		if code >= 300 {
			t.Fatalf("trigger HTTP %d %v", code, out)
		}
		return textField(t29Wait(t, s, out), "trigger_id")
	}
	hook, slack := mk("pg_webhook", "t29-fixture"), mk("pg_slack", "T0T29FIXTURE")
	sign := func(msg string) string {
		m := hmac.New(sha256.New, []byte(strings.TrimSpace(string(key))))
		m.Write([]byte(msg))
		return hex.EncodeToString(m.Sum(nil))
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	body := []byte(`{"action":"opened","project":"public-fixture","ask":"summarize README"}`)
	h := map[string]string{"X-Lamp-Timestamp": ts, "X-Lamp-Delivery": "dlv-1", "X-Lamp-Signature": "sha256=" + sign(ts+"."+string(body))}
	c1, r1 := t29Hook(s, hook, body, h)
	c2, r2 := t29Hook(s, hook, body, h) // vendor retry of the same delivery
	forged := map[string]string{"X-Lamp-Timestamp": ts, "X-Lamp-Delivery": "dlv-2", "X-Lamp-Signature": "sha256=" + sign(ts+".tampered")}
	c3, r3 := t29Hook(s, hook, body, forged)
	t.Logf("webhook delivery HTTP %d replayed=%v; retry HTTP %d replayed=%v same-operation=%v; forged HTTP %d %v",
		c1, r1["replayed"], c2, r2["replayed"], r1["operation_id"] == r2["operation_id"], c3, r3["error"])
	slackBody := []byte(fmt.Sprintf(`{"type":"event_callback","team_id":"T0T29FIXTURE","event_id":"Ev0T29","event":{"type":"app_mention","text":"<@lamp> run the audit"}}`))
	sts := strconv.FormatInt(time.Now().Unix(), 10)
	c4, _ := t29Hook(s, slack, slackBody, map[string]string{"X-Slack-Request-Timestamp": sts, "X-Slack-Signature": "v0=" + sign("v0:"+sts+":"+string(slackBody))})
	old := strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10)
	c5, r5 := t29Hook(s, slack, slackBody, map[string]string{"X-Slack-Request-Timestamp": old, "X-Slack-Signature": "v0=" + sign("v0:"+old+":"+string(slackBody))})
	t.Logf("slack event HTTP %d; replayed-old timestamp HTTP %d %v", c4, c5, r5["error"])
	for _, src := range []string{hook, slack} {
		var id string
		t29Until(t, "trigger work "+src, 20*time.Minute, func() bool {
			var j ciJob
			id, j = t29Job(s, func(j *ciJob) bool { return j.Trigger == src })
			return id != "" && ciTerminal(textField(j.R, "state"))
		})
		t29Report(t, s, id)
		_, j := t29Job(s, func(j *ciJob) bool { return textField(j.R, "id") == id })
		for _, a := range stringsField(j.R, "artifact_ids") {
			s.readLock()
			art := s.life.Artifacts[a]
			blob := s.life.Blobs[textField(art.R, "download_blob_id")]
			s.mu.Unlock()
			if blob != nil {
				data, _ := os.ReadFile(s.objectPath(trimDigest(textField(blob.R, "sha256"))))
				t.Logf("work output:\n%s", strings.TrimSpace(string(data)))
			}
		}
	}
	n := 0
	s.readLock()
	for _, j := range s.life.Jobs {
		if j.Trigger == hook {
			n++
		}
	}
	s.mu.Unlock()
	t.Logf("webhook jobs created from 3 deliveries (1 retry, 1 forged): %d", n)

	// 5. A schedule in a DST zone fires real work; DST arithmetic is shown on
	// the production next() for this year's America/New_York transitions.
	ny, _ := time.LoadLocation("America/New_York")
	c, _ := parseCron("30 2 * * *")
	for _, at := range []string{"2026-03-07T12:00:00Z", "2026-03-08T12:00:00Z", "2026-10-31T12:00:00Z"} {
		after, _ := time.Parse(time.RFC3339, at)
		shift, _ := c.next(after, ny, "shift")
		skip, _ := c.next(after, ny, "skip")
		t.Logf("02:30 daily after %s: shift=%s skip=%s", at, shift.In(ny).Format(time.RFC3339), skip.In(ny).Format(time.RFC3339))
	}
	c2r, _ := parseCron("30 1 * * *")
	nov1, _ := time.Parse(time.RFC3339, "2026-11-01T04:00:00Z")
	first1, _ := c2r.next(nov1, ny, "shift")
	again, _ := c2r.next(first1, ny, "shift")
	t.Logf("01:30 daily on the fall-back day runs once: %s then %s", first1.In(ny).Format(time.RFC3339), again.In(ny).Format(time.RFC3339))
	code, out = t29Call(t, s, "POST", "/v1/schedules", map[string]any{"recipe_id": recipe, "cron": "* * * * *", "timezone": "America/New_York", "overlap_policy": "skip", "labels": map[string]any{entrypointLabel: agent}}, "")
	if code >= 300 {
		t.Fatalf("schedule HTTP %d %v", code, out)
	}
	sched := textField(t29Wait(t, s, out), "schedule_id")
	var sj string
	t29Until(t, "scheduled work", 20*time.Minute, func() bool {
		var j ciJob
		sj, j = t29Job(s, func(j *ciJob) bool { return j.Trigger == sched })
		return sj != "" && ciTerminal(textField(j.R, "state"))
	})
	t29Report(t, s, sj)
	s.readLock()
	m := s.life.Schedules[sched].R
	t.Logf("schedule %s next_run_at=%s skipped_overlap=%s", sched, textField(m, "next_run_at"), textField(object(m["labels"]), "lamp.skipped_overlap"))
	rev := strconv.FormatUint(uintField(m, "revision"), 10)
	s.mu.Unlock()
	t29Call(t, s, "POST", "/v1/schedules/"+sched+":pause", map[string]any{}, rev)
	t29Until(t, "all work to finish", 20*time.Minute, func() bool {
		id, _ := t29Job(s, func(j *ciJob) bool { return !ciTerminal(textField(j.R, "state")) })
		return id == ""
	})
	live2 := 0
	s.readLock()
	for _, sb := range s.journal.Sandboxes {
		if sb.State != "deleted" {
			live2++
		}
	}
	s.mu.Unlock()
	t.Logf("sandboxes not deleted at end: %d; forge runners still registered: %d", live2, t29Runners(t, live))
}

// t29GitLab serves this Lamp API on a local socket and runs a host GitLab
// Runner whose Custom executor and Fleeting plugin reach Lamp only through it.
func t29GitLab(t *testing.T, s *Service, live t29Live) {
	code, out := t29Call(t, s, "POST", "/v1/recipes", map[string]any{"name": "t29-gitlab", "template_id": live.Template, "resources": map[string]any{"vcpus": 2, "memory_mib": 4096, "disk_gib": 20, "pids_max": 4096}}, "")
	if code >= 300 {
		t.Fatalf("recipe HTTP %d %v", code, out)
	}
	recipe := textField(t29Wait(t, s, out), "recipe_id")
	sock := filepath.Join(t.TempDir(), "lamp.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: s, ConnContext: s.PeerContext}
	go srv.Serve(&t29Local{l})
	defer srv.Close()
	cmd := exec.Command("python3", os.Getenv("LAMP_T29_GITLAB_DRIVER"))
	cmd.Env = append(os.Environ(), "LAMP_SOCKET="+sock, "LAMP_GITLAB_RECIPE="+recipe)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	err = cmd.Run()
	// The driver's runner and its Fleeting plugin must not outlive this stage:
	// a graceful quit lets the plugin delete its instances, then force.
	syscall.Kill(-cmd.Process.Pid, syscall.SIGQUIT)
	time.Sleep(30 * time.Second)
	syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	live2 := 0
	s.readLock()
	for _, sb := range s.journal.Sandboxes {
		if sb.State != "deleted" {
			live2++
		}
	}
	s.mu.Unlock()
	t.Logf("gitlab driver exit=%v; sandboxes not deleted at end: %d", err, live2)
	if err != nil {
		t.Fail()
	}
}

type t29Local struct{ net.Listener }

func (l *t29Local) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if AuthenticateLocal(c.(*net.UnixConn)) != nil {
			c.Close()
			continue
		}
		return c, nil
	}
}
