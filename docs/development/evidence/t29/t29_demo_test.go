//go:build t29demo

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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"lamp.eilmer.si/internal/ci"
	"lamp.eilmer.si/pkg/api"
	pb "lamp.eilmer.si/pkg/workerpb"
)

type t29Live struct {
	Worker        WorkerConfig    `json:"worker"`
	StateAdmin    []string        `json:"state_admin"`
	NetworkReader []string        `json:"network_reader"`
	Repository    ci.Repository   `json:"repository"`
	Repositories  []ci.Repository `json:"repositories"`
	Template      string          `json:"template"`
	Forge         string          `json:"forge"`
	TokenFile     string          `json:"token_file"`
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
			t.Logf("vms on host: %s", func() string {
				b, _ := exec.Command("pgrep", "-c", "firecracker").Output()
				return strings.TrimSpace(string(b))
			}())
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
	if os.Getenv("LAMP_T29_POLICY") != "" {
		go t29PolicyObserver(t, s, live)
	}

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
			if os.Getenv("LAMP_T29_POLICY") != "" {
				t29Retention(t, s)
			}
			t.Logf("forge runners still registered: %d", t29Runners(t, live))
			return
		}
		t29Triggers(t, s, live, root)
		return
	}
	if os.Getenv("LAMP_T29_ONLY") == "extra" {
		t29Extra(t, s, live, root, config)
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
		id, j := t29Job(s, func(j *ciJob) bool {
			return j.Workflow == ".forgejo/workflows/slow.yml" && !ciTerminal(textField(j.R, "state"))
		})
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
		id, j := t29Job(s, func(j *ciJob) bool {
			return j.Workflow == ".forgejo/workflows/slow.yml" && !ciTerminal(textField(j.R, "state"))
		})
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

// t29PolicyObserver logs, once per CI job, the root-owned network attachment
// its live VM actually received: the exact private-service exceptions.
func t29PolicyObserver(t *testing.T, s *Service, live t29Live) {
	seen := map[string]bool{}
	for {
		time.Sleep(time.Second)
		var todo [][3]string
		s.readLock()
		for id, j := range s.life.Jobs {
			sb := textField(j.R, "sandbox_id")
			if j.Trigger == "" && sb != "" && !seen[id] && textField(j.R, "state") == "running" {
				todo = append(todo, [3]string{id, sb, textField(j.R, "trust_class")})
			}
		}
		s.mu.Unlock()
		for _, x := range todo {
			seen[x[0]] = true
			gen := strconv.FormatUint(s.sandboxGeneration(x[1]), 10)
			raw, err := exec.Command(live.NetworkReader[0], append(append([]string{}, live.NetworkReader[1:]...), x[1], gen)...).Output()
			var att struct {
				Exceptions []map[string]any `json:"exceptions"`
			}
			json.Unmarshal(raw, &att)
			var eps []string
			for _, e := range att.Exceptions {
				eps = append(eps, fmt.Sprintf("%v:%v", e["address"], e["port"]))
			}
			_, j := t29Job(s, func(j *ciJob) bool { return textField(j.R, "id") == x[0] })
			t.Logf("policy job %s trust=%s attested exceptions (%d) %v err=%v; broker grant issued=%v", x[0], x[2], len(eps), eps, err, j.BrokerGrant != "")
		}
	}
}

// t29Retention reports each CI job's VM fate, then deletes retained VMs.
func t29Retention(t *testing.T, s *Service) {
	s.readLock()
	type kept struct{ job, sb, trust, state string }
	var all []kept
	for id, j := range s.life.Jobs {
		if j.Trigger == "" {
			sb := textField(j.R, "sandbox_id")
			all = append(all, kept{id, sb, textField(j.R, "trust_class"), string(s.journal.Sandboxes[sb].State)})
		}
	}
	s.mu.Unlock()
	for _, k := range all {
		t.Logf("retention: job %s trust=%s retain_stopped=true sandbox %s is %s", k.job, k.trust, k.sb, k.state)
		if k.state != "" && k.state != "deleted" {
			s.readLock()
			sb := s.journal.Sandboxes[k.sb]
			s.mu.Unlock()
			s.internalDelete(sb, "t29-inspected")
			time.Sleep(5 * time.Second)
			s.readLock()
			t.Logf("retention: inspected VM %s then deleted by the owner: %s", k.sb, s.journal.Sandboxes[k.sb].State)
			s.mu.Unlock()
		}
	}
}

// t29Extra: a Linear delivery, a live cancel of trigger work, and one
// schedule shared by two fleet hosts.
func t29Extra(t *testing.T, s *Service, live t29Live, root string, base Config) {
	if os.Getenv("LAMP_T29_FLEET_ONLY") != "" {
		s.Close()
		t29Fleet(t, live, root, base)
		return
	}
	code, out := t29Call(t, s, "POST", "/v1/recipes", map[string]any{"name": "t29-extra", "template_id": live.Template, "resources": map[string]any{"vcpus": 2, "memory_mib": 4096, "disk_gib": 20, "pids_max": 4096}}, "")
	if code >= 300 {
		t.Fatalf("recipe HTTP %d %v", code, out)
	}
	recipe := textField(t29Wait(t, s, out), "recipe_id")
	secret := filepath.Join(root, "signing-secret")
	os.WriteFile(secret, []byte(newID("whsec")+newID("x")), 0600)
	key, _ := os.ReadFile(secret)
	sign := func(msg string) string {
		m := hmac.New(sha256.New, []byte(strings.TrimSpace(string(key))))
		m.Write([]byte(msg))
		return hex.EncodeToString(m.Sum(nil))
	}
	mk := func(plugin, source, agent string) string {
		code, out := t29Call(t, s, "POST", "/v1/triggers", map[string]any{"plugin_id": plugin, "source_ref": source, "recipe_id": recipe,
			"verification_policy": map[string]any{"secret_file": secret}, "labels": map[string]any{entrypointLabel: agent}}, "")
		if code >= 300 {
			t.Fatalf("trigger HTTP %d %v", code, out)
		}
		return textField(t29Wait(t, s, out), "trigger_id")
	}
	work := func(trigger string) string {
		var id string
		t29Until(t, "work for "+trigger, 20*time.Minute, func() bool {
			var j ciJob
			id, j = t29Job(s, func(j *ciJob) bool { return j.Trigger == trigger })
			return id != "" && ciTerminal(textField(j.R, "state"))
		})
		t29Report(t, s, id)
		return id
	}

	// 1. Linear: Linear-Signature is hex HMAC-SHA256 of the raw body.
	agent := `["sh","-c","echo source=$LAMP_TRIGGER_SOURCE job=$LAMP_JOB_ID; cat $LAMP_TRIGGER_EVENT; echo; test ! -e /var/tmp/lamp-previous-job && echo fresh-sandbox; echo agent-done"]`
	linear := mk("pg_linear", "org-t29", agent)
	ms := time.Now().UnixMilli()
	body := []byte(fmt.Sprintf(`{"action":"create","type":"Issue","organizationId":"org-t29","webhookId":"wh-t29","webhookTimestamp":%d,"data":{"title":"Summarize README"}}`, ms))
	h := map[string]string{"Linear-Signature": sign(string(body)), "Linear-Delivery": "lin-dlv-1"}
	c1, r1 := t29Hook(s, linear, body, h)
	c2, r2 := t29Hook(s, linear, body, h) // Linear redelivers the same delivery
	c3, r3 := t29Hook(s, linear, body, map[string]string{"Linear-Signature": sign(string(body) + "x"), "Linear-Delivery": "lin-dlv-2"})
	stale := []byte(fmt.Sprintf(`{"action":"create","type":"Issue","organizationId":"org-t29","webhookId":"wh-t29","webhookTimestamp":%d,"data":{}}`, time.Now().Add(-time.Hour).UnixMilli()))
	c4, r4 := t29Hook(s, linear, stale, map[string]string{"Linear-Signature": sign(string(stale)), "Linear-Delivery": "lin-dlv-3"})
	t.Logf("linear delivery HTTP %d replayed=%v; redelivery HTTP %d replayed=%v same-operation=%v; forged HTTP %d %v; stale HTTP %d %v",
		c1, r1["replayed"], c2, r2["replayed"], r1["operation_id"] == r2["operation_id"], c3, r3["error"], c4, r4["error"])
	if os.Getenv("LAMP_T29_REPLAY_ONLY") != "" {
		// A captured signed delivery resent with a fresh, unsigned delivery-ID
		// header must replay as the same operation, never start new work.
		resend := func(name, trigger string, body []byte, first, again map[string]string) {
			c1, r1 := t29Hook(s, trigger, body, first)
			work(trigger)
			c2, r2 := t29Hook(s, trigger, body, again)
			time.Sleep(5 * time.Second)
			n := 0
			s.readLock()
			for _, j := range s.life.Jobs {
				if j.Trigger == trigger {
					n++
				}
			}
			s.mu.Unlock()
			t.Logf("replay %s: first HTTP %d; resent with new delivery ID HTTP %d replayed=%v same-operation=%v; jobs=%d", name, c1, c2, r2["replayed"], r1["operation_id"] == r2["operation_id"], n)
		}
		resend("linear", linear, body, h, map[string]string{"Linear-Signature": h["Linear-Signature"], "Linear-Delivery": "lin-dlv-fresh"})
		quick := `["sh","-c","echo agent-done"]`
		lamp := mk("pg_webhook", "t29-replay-lamp", quick)
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		lb := []byte(`{"ask":"replay lamp"}`)
		lsig := "sha256=" + sign(ts+"."+string(lb))
		resend("lamp-scheme", lamp, lb, map[string]string{"X-Lamp-Timestamp": ts, "X-Lamp-Delivery": "r-1", "X-Lamp-Signature": lsig},
			map[string]string{"X-Lamp-Timestamp": ts, "X-Lamp-Delivery": "r-2-fresh", "X-Lamp-Signature": lsig})
		hub := mk("pg_webhook", "t29-replay-hub", quick)
		hb := []byte(`{"ref":"refs/heads/main","after":"replay"}`)
		hsig := "sha256=" + sign(string(hb))
		resend("forge-scheme", hub, hb, map[string]string{"X-Hub-Signature-256": hsig, "X-Gitea-Delivery": "g-1"},
			map[string]string{"X-Hub-Signature-256": hsig, "X-Gitea-Delivery": "g-2-fresh"})
		s.Close()
		return
	}
	lj := work(linear)
	t29WorkOutput(t, s, lj)
	n := 0
	s.readLock()
	for _, j := range s.life.Jobs {
		if j.Trigger == linear {
			n++
		}
	}
	s.mu.Unlock()
	t.Logf("linear jobs created from 4 deliveries (1 redelivery, 1 forged, 1 stale): %d", n)

	// 2. Cancel live trigger work through Lamp.
	hook := mk("pg_webhook", "t29-cancel", `["sh","-c","echo started; sleep 900; echo never"]`)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	cb := []byte(`{"ask":"long task"}`)
	if c, r := t29Hook(s, hook, cb, map[string]string{"X-Lamp-Timestamp": ts, "X-Lamp-Delivery": "dlv-cancel", "X-Lamp-Signature": "sha256=" + sign(ts+"."+string(cb))}); c != 202 {
		t.Fatalf("cancel delivery HTTP %d %v", c, r)
	}
	var cid string
	t29Until(t, "cancel work running", 15*time.Minute, func() bool {
		var j ciJob
		cid, j = t29Job(s, func(j *ciJob) bool { return j.Trigger == hook })
		return cid != "" && textField(j.R, "state") == "running"
	})
	time.Sleep(5 * time.Second)
	_, cj := t29Job(s, func(j *ciJob) bool { return textField(j.R, "id") == cid })
	csb := textField(cj.R, "sandbox_id")
	t.Logf("cancel: work %s running in %s; cancelling through Lamp", cid, csb)
	if code, out := t29Call(t, s, "POST", "/v1/jobs/"+cid+":cancel", map[string]any{}, strconv.FormatUint(uintField(cj.R, "revision"), 10)); code >= 300 {
		t.Fatalf("cancel HTTP %d %v", code, out)
	}
	work(hook)
	s.readLock()
	sbState, released := s.journal.Sandboxes[csb].State, s.auditReleased[csb]
	running := 0
	for _, ex := range s.journal.Execs {
		if value(ex.SandboxId) == csb && (ex.State == "running" || ex.State == "accepted") {
			running++
		}
	}
	s.mu.Unlock()
	t.Logf("cancel: sandbox %s %s; guard audit released=%v; execs still running in it=%d; firecracker on host=%s", csb, sbState, released, running,
		func() string {
			b, _ := exec.Command("pgrep", "-c", "firecracker").Output()
			return strings.TrimSpace(string(b))
		}())
	s.Close()

	// 3. One schedule on two fleet hosts runs each occurrence once fleet-wide.
	t29Fleet(t, live, root, base)
}

func t29WorkOutput(t *testing.T, s *Service, id string) {
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

// t29Fleet opens two control hosts in one raft fleet (mutual TLS from a
// throwaway CA), enrolls the same schedule on both under one fleet_key, and
// counts which host ran each occurrence.
func t29Fleet(t *testing.T, live t29Live, root string, base Config) {
	dir := filepath.Join(root, "fleet")
	os.MkdirAll(dir, 0700)
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "t29 fleet CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(48 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	caCert, _ := x509.ParseCertificate(caDER)
	os.WriteFile(filepath.Join(dir, "ca.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0600)
	port := func() int {
		l, _ := net.Listen("tcp", "127.0.0.1:0")
		defer l.Close()
		return l.Addr().(*net.TCPAddr).Port
	}
	ids := []string{"hs_t29a", "hs_t29b"}
	var hosts []FleetHost
	for i, id := range ids {
		k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		tpl := &x509.Certificate{SerialNumber: big.NewInt(int64(i + 2)), Subject: pkix.Name{CommonName: id}, DNSNames: []string{id}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(48 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
		der, _ := x509.CreateCertificate(rand.Reader, tpl, caCert, &k.PublicKey, caKey)
		kd, _ := x509.MarshalECPrivateKey(k)
		os.WriteFile(filepath.Join(dir, id+".pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600)
		os.WriteFile(filepath.Join(dir, id+".key"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd}), 0600)
		// One VM's worth of capacity each: both hosts share this laptop's worker.
		hosts = append(hosts, FleetHost{ID: id, API: fmt.Sprintf("https://127.0.0.1:%d", port()), Raft: fmt.Sprintf("127.0.0.1:%d", port()), OS: "linux", Arch: "amd64", Engines: []string{"firecracker"},
			Capacity: api.Capacity{Vcpus: pointer(uint64(2)), MemoryMib: pointer(uint64(4096)), DiskGib: pointer(uint64(20)), PidsMax: pointer(uint64(4096))}})
	}
	sum := sha256.Sum256([]byte(newID("bootstrap")))
	identity := &IdentityConfig{OwnerID: "pr_t29owner", BootstrapSHA256: hex.EncodeToString(sum[:])}
	owner := caller{Principal: "pr_t29owner"}
	var svcs []*Service
	var fleets []*Fleet
	for i, id := range ids {
		cfg := base
		cfg.StateDir = filepath.Join(dir, id, "state")
		cfg.CI = nil
		cfg.Identity = identity
		svc, err := Open(cfg)
		if err != nil {
			t.Fatalf("open %s: %v", id, err)
		}
		f, err := OpenFleet(svc, FleetConfig{HostID: id, Hosts: hosts, CAFile: filepath.Join(dir, "ca.pem"), CertFile: filepath.Join(dir, id+".pem"), KeyFile: filepath.Join(dir, id+".key"), GuardDir: filepath.Join(dir, id, "guard"), Bootstrap: i == 0})
		if err != nil {
			t.Fatalf("fleet %s: %v", id, err)
		}
		svcs, fleets = append(svcs, svc), append(fleets, f)
	}
	defer func() {
		for i := range svcs {
			fleets[i].Close()
			svcs[i].Close()
		}
	}()
	t29Until(t, "fleet leader and authority leases", 2*time.Minute, func() bool {
		return fleets[0].leaderID() != "" && fleets[1].leaderID() != "" && fleets[0].leaseValid() && fleets[1].leaseValid()
	})
	t.Logf("fleet: hosts %v, leader %s", ids, fleets[0].leaderID())
	startWait := func(svc *Service, name string, in map[string]any) string {
		in["idempotency_key"] = newID("t29")
		op, _, err := svc.lifeStart(owner, name, in)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		svc.readLock()
		done := svc.done[op]
		svc.mu.Unlock()
		if done != nil {
			<-done
		}
		svc.readLock()
		defer svc.mu.Unlock()
		rec := svc.journal.Operations[op]
		if rec.Operation.Error != nil {
			t.Fatalf("%s failed: %s %s", name, rec.Operation.Error.Code, rec.Operation.Error.Message)
		}
		return rec.Resource.Id
	}
	// Each occurrence runs 150 s, longer than the one-minute interval, under
	// overlap_policy=skip: the next occurrences must not start on either host.
	agent := `["sh","-c","echo fleet occurrence on $(hostname); sleep 150; echo agent-done"]`
	var scheds []string
	for _, svc := range svcs {
		rc := startWait(svc, "recipe_create", map[string]any{"name": "t29-fleet", "template_id": live.Template, "resources": map[string]any{"vcpus": 2, "memory_mib": 4096, "disk_gib": 20, "pids_max": 4096}})
		sc := startWait(svc, "schedule_create", map[string]any{"recipe_id": rc, "cron": "* * * * *", "timezone": "America/New_York", "fleet_key": "t29-nightly", "overlap_policy": "skip", "labels": map[string]any{entrypointLabel: agent}})
		scheds = append(scheds, sc)
	}
	t.Logf("fleet: schedule fleet_key=t29-nightly enrolled as %s on %s and %s on %s", scheds[0], ids[0], scheds[1], ids[1])
	// Let four occurrences come due, then stop enrolling more. Midway, show
	// the replicated active occurrence that blocks the other host.
	time.Sleep(time.Until(time.Now().Truncate(time.Minute).Add(2*time.Minute + 30*time.Second)))
	t.Logf("fleet: replicated active occurrence mid-run: %v", fleets[1].fsm.snapshot().Active)
	time.Sleep(time.Until(time.Now().Truncate(time.Minute).Add(2*time.Minute + 20*time.Second)))
	for i, svc := range svcs {
		svc.readLock()
		rev := uintField(svc.life.Schedules[scheds[i]].R, "revision")
		svc.mu.Unlock()
		startWait(svc, "schedule_pause", map[string]any{"schedule_id": scheds[i], "expected_revision": jsonNumber(rev)})
	}
	t29Until(t, "fleet work to finish", 25*time.Minute, func() bool {
		for _, svc := range svcs {
			if id, _ := t29Job(svc, func(j *ciJob) bool { return j.Trigger != "" && !ciTerminal(textField(j.R, "state")) }); id != "" {
				return false
			}
		}
		return true
	})
	occ := map[string][]string{}
	for i, svc := range svcs {
		svc.readLock()
		for id, j := range svc.life.Jobs {
			if j.Trigger == scheds[i] {
				at := strings.TrimPrefix(textField(j.R, "external_id"), scheds[i]+"/schedule:")
				occ[at] = append(occ[at], fmt.Sprintf("%s:%s(%s/%s created %s ended %s %v)", ids[i], id, textField(j.R, "state"), textField(j.R, "result"), textField(j.R, "created_at"), textField(j.R, "updated_at"), object(j.R["labels"])["lamp.error"]))
			}
		}
		t.Logf("fleet: %s skipped %d occurrence(s) (claimed elsewhere or overlap)", ids[i], svc.life.Schedules[scheds[i]].Skipped)
		svc.mu.Unlock()
	}
	keys := make([]string, 0, len(occ))
	for k := range occ {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Logf("fleet: occurrence %s ran %d time(s) fleet-wide: %v", k, len(occ[k]), occ[k])
	}
	homes := fleets[0].fsm.snapshot().Homes
	for k, v := range homes {
		if strings.Contains(k, "fleet-t29-nightly") {
			t.Logf("fleet: replicated claim %s -> %s", k, v)
		}
	}
}
