package control

// One explicit demonstration, not a smoke gate. The forge is controlled because
// GitHub signup is unavailable from the owner's machines. With LIVE_CONFIG it
// also exercises the production controller on real, disposable Firecracker VMs.
// It does NOT constitute evidence of native GitHub Actions or a real GitHub JWT.
import (
	"bytes"
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JohanSardar/lamp/internal/ci"
)

type ciTransport func(*http.Request) (*http.Response, error)

func (f ciTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func ciResponse(v any) *http.Response {
	b, _ := json.Marshal(v)
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(b))}
}
func ciCall(t *testing.T, s *Service, method, path string, body any, token string, revision string) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r.Header.Set("Idempotency-Key", newID("demo"))
	if revision != "" {
		r.Header.Set("If-Match", revision)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	var out map[string]any
	if json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatal("non-JSON control response")
	}
	return w.Code, out
}
func TestCIControlledDemonstration(t *testing.T) {
	root := t.TempDir()
	config := Config{StateDir: filepath.Join(root, "state"), Worker: WorkerConfig{Socket: "/unused-ci-demo", WorkerID: "ci-demo", BootID: "ci-demo", UID: os.Getuid(), PID: os.Getpid(), GrantID: "ci-demo"}}
	live := os.Getenv("LAMP_CI_LIVE_CONFIG")
	template := ""
	if live != "" {
		raw, e := os.ReadFile(live)
		if e != nil {
			t.Fatal(e)
		}
		var in struct {
			Worker        WorkerConfig `json:"worker"`
			StateAdmin    []string     `json:"state_admin"`
			Template      string       `json:"template"`
			NetworkReader []string     `json:"network_reader"`
		}
		if json.Unmarshal(raw, &in) != nil {
			t.Fatal("invalid live demonstration config")
		}
		config.Worker = in.Worker
		config.StateAdmin = in.StateAdmin
		template = in.Template
		config.CI = &ci.Config{NetworkReader: in.NetworkReader}
	}
	if config.CI == nil {
		config.CI = &ci.Config{}
	}
	tokenFile := filepath.Join(root, "forge-token")
	os.WriteFile(tokenFile, []byte("disposable-forge-fixture-not-a-credential"), 0600)
	config.CI.TokenFile = tokenFile
	policy := ci.OIDCPolicy{RepositoryID: "1", OwnerID: "2", WorkflowRef: "disposable/fixture/.github/workflows/ci.yml@refs/heads/main", WorkflowSHA: strings.Repeat("a", 40), Ref: "refs/heads/main", Event: "push", ResourceIDs: []string{"sb_demo"}, Actions: []string{"sandbox.get"}}
	config.CI.OIDC = []ci.OIDCPolicy{policy}
	s, e := Open(config)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	s.github.HTTP = &http.Client{Transport: ciTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != ci.GitHubIssuer+"/.well-known/jwks" {
			return nil, fmt.Errorf("unexpected identity destination")
		}
		return ciResponse(map[string]any{"keys": []any{map[string]any{"kid": "demo", "alg": "RS256", "kty": "RSA", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": "AQAB"}}}), nil
	})}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	thumb := sha256.Sum256(pub)
	source := map[string]any{"issuer": ci.GitHubIssuer, "repository_id": policy.RepositoryID, "repository_owner_id": policy.OwnerID, "workflow_ref": policy.WorkflowRef, "workflow_sha": policy.WorkflowSHA, "ref": policy.Ref, "event": policy.Event}
	status, flow := ciCall(t, s, "POST", "/v1/auth-flows:bootstrap", map[string]any{"fleet_id": "fl_local", "purpose": "ci_exchange", "proof_key_thumbprint": hex.EncodeToString(thumb[:]), "source_identity": source}, "", "")
	if status != 200 {
		t.Fatalf("bootstrap HTTP %d", status)
	}
	header, _ := json.Marshal(map[string]any{"alg": "RS256", "kid": "demo"})
	claims := map[string]any{"iss": ci.GitHubIssuer, "aud": flow["audience"], "iat": time.Now().Unix(), "nbf": time.Now().Unix(), "exp": time.Now().Add(5 * time.Minute).Unix(), "repository_id": policy.RepositoryID, "repository_owner_id": policy.OwnerID, "repository_visibility": "private", "workflow_ref": policy.WorkflowRef, "workflow_sha": policy.WorkflowSHA, "ref": policy.Ref, "event_name": policy.Event}
	payload, _ := json.Marshal(claims)
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(unsigned))
	signature, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	jwt := unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
	proof, _ := json.Marshal(map[string]any{"auth_flow_id": flow["auth_flow_id"], "challenge": flow["challenge"], "jwt_sha256": bearerHash(jwt), "audience": flow["audience"]})
	sig := ed25519.Sign(priv, proof)
	exchange := map[string]any{"jwt": jwt, "audience": flow["audience"], "proof": map[string]any{"public_key": base64.StdEncoding.EncodeToString(pub), "signature": base64.StdEncoding.EncodeToString(sig)}}
	path := "/v1/auth-flows/" + fmt.Sprint(flow["auth_flow_id"]) + ":exchange"
	status, out := ciCall(t, s, "POST", path, exchange, "", "1")
	if status != 200 {
		t.Fatalf("exchange HTTP %d", status)
	}
	secret := textField(out["result"].(map[string]any), "secret")
	if secret == "" {
		t.Fatal("no transient key")
	}
	if status, _ = ciCall(t, s, "GET", "/v1/sandboxes/sb_demo", nil, secret, ""); status != 404 {
		t.Fatalf("scoped key read HTTP %d", status)
	}
	if status, _ = ciCall(t, s, "GET", "/v1/keys", nil, secret, ""); status != 403 {
		t.Fatal("exchanged key escaped its scope")
	}
	if status, _ = ciCall(t, s, "POST", path, exchange, "", "1"); status != 401 {
		t.Fatal("exchange replay was not rejected")
	}
	filepath.Walk(config.StateDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			b, _ := os.ReadFile(path)
			if bytes.Contains(b, []byte(jwt)) || bytes.Contains(b, []byte(secret)) {
				t.Error("transient credential reached durable state")
			}
		}
		return nil
	})
	t.Log("controlled RSA issuer + Ed25519 proof: scoped key works, authority escalation and replay denied; JWT and key absent from durable files")
	if live == "" {
		t.Log("real VM portion not requested; set LAMP_CI_LIVE_CONFIG for the explicit manual demonstration")
		return
	}
	ciLiveVM(t, s, template, root)
}

func ciWait(t *testing.T, s *Service, out map[string]any) map[string]any {
	t.Helper()
	opID := textField(out, "operation_id")
	s.readLock()
	done := s.done[opID]
	s.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-time.After(3 * time.Minute):
			t.Fatal("demonstration operation did not finish")
		}
	}
	s.readLock()
	defer s.mu.Unlock()
	rec := s.journal.Operations[opID]
	if rec == nil || rec.Operation.State != "succeeded" {
		if rec != nil {
			t.Fatalf("demonstration operation failed: %v", rec.Operation.Error)
		}
		t.Fatal("demonstration operation missing")
	}
	raw := *rec.Operation.Outcome.Result
	var result map[string]any
	json.Unmarshal(raw, &result)
	return result
}
func ciLiveVM(t *testing.T, s *Service, base, root string) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	dist := "/opt/lamp-ci-demo-dist"
	saved := base
	if os.Getenv("LAMP_CI_TEMPLATE_READY") == "" {
		dist = "/var/tmp/lamp-ci-demo-dist"
		if e := ownerMachineAdmission(); e != nil {
			t.Fatal(e)
		}
		status, out := ciCall(t, s, "POST", "/v1/sandboxes", map[string]any{"template_id": base}, "", "")
		if status >= 400 {
			t.Fatalf("seed admission HTTP %d", status)
		}
		seed := textField(ciWait(t, s, out), "sandbox_id")
		// Only public third-party code is a workload. No owner repository checkout.
		archive, e := os.ReadFile(os.Getenv("LAMP_CI_ZLIB_ARCHIVE"))
		if e != nil {
			t.Fatal(e)
		}
		if e = s.guestWriteFile(ctx, seed, dist+"/zlib.tgz", archive); e != nil {
			t.Fatal(e)
		}
		script := `#!/bin/sh
set -eu
test ! -e /tmp/lamp-ci-private-marker
echo fresh-private-root > /tmp/lamp-ci-demo.log
echo private-job-state > /tmp/lamp-ci-private-marker
mkdir /tmp/zlib-build
tar xzf ./zlib.tgz -C /tmp/zlib-build --strip-components=1
cd /tmp/zlib-build
touch crc32.h
CFLAGS=-DDYNAMIC_CRC_TABLE ./configure --static >> /tmp/lamp-ci-demo.log 2>&1
gcc -DDYNAMIC_CRC_TABLE -O2 -I. test/minigzip.c adler32.c crc32.c deflate.c infback.c inffast.c inflate.c inftrees.c trees.c zutil.c compress.c uncompr.c gzclose.c gzlib.c gzread.c gzwrite.c -o minigzip >> /tmp/lamp-ci-demo.log 2>&1
./minigzip < README > /tmp/readme.gz
./minigzip -d < /tmp/readme.gz > /tmp/readme.restored
cmp README /tmp/readme.restored
echo zlib-compression-roundtrip-ok >> /tmp/lamp-ci-demo.log
touch /tmp/lamp-ci-demo-finished
`
		if e = s.guestWriteFile(ctx, seed, dist+"/run.sh", []byte(script)); e != nil {
			t.Fatal(e)
		}
		if _, e = s.guestRun(ctx, seed, "chmod", "+x", dist+"/run.sh"); e != nil {
			t.Fatal(e)
		}
		s.readLock()
		rev := s.journal.Sandboxes[seed].Revision
		s.mu.Unlock()
		// save_template uses its own fresh builder: count and memory are observed
		// immediately before requesting that second, short-lived VM as well.
		if e = ownerMachineAdmission(); e != nil {
			t.Fatal(e)
		}
		status, out = ciCall(t, s, "POST", "/v1/sandboxes/"+seed+":save-template", map[string]any{"name": "ci-disposable-public-zlib", "selected_paths": []string{dist}}, "", fmt.Sprint(rev))
		if status >= 400 {
			t.Fatalf("save public workload template HTTP %d", status)
		}
		saved = textField(ciWait(t, s, out), "template_id")
		s.readLock()
		sb := s.journal.Sandboxes[seed]
		s.mu.Unlock()
		s.internalDelete(sb, "ci-demo-seed")
		for {
			s.readLock()
			deleted := s.journal.Sandboxes[seed].State == "deleted"
			s.mu.Unlock()
			if deleted {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("seed cleanup did not finish")
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	workflow := []byte("name: public-fixture\non: workflow_dispatch\npermissions:\n  contents: read\njobs:\n  build:\n    runs-on: [self-hosted, lamp-demo, 'lamp-demo-${{ github.run_id }}-${{ github.run_attempt }}']\n    steps:\n      - run: echo public fixture\n")
	digest := sha256.Sum256(workflow)
	repo := ci.Repository{Name: "madler/zlib", ID: 2359378, OwnerID: 240717, Label: "lamp-demo", TemplateID: saved, RunnerDirectory: dist, WorkflowSHA256: map[string]string{".github/workflows/ci.yml": hex.EncodeToString(digest[:])}}
	s.github.Config.Repositories = []ci.Repository{repo}
	var mu sync.Mutex
	var active string
	var log []byte
	jobID := int64(1)
	jitCount := 0
	deregCount := 0
	cancelCount := 0
	cancelMode := false
	trusted := false
	defer func() {
		if t.Failed() {
			mu.Lock()
			defer mu.Unlock()
			tail := log
			if len(tail) > 3000 {
				tail = tail[len(tail)-3000:]
			}
			t.Logf("public zlib build output: %s", tail)
		}
	}()
	s.github.HTTP = &http.Client{Transport: ciTransport(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		p := r.URL.Path
		if strings.HasSuffix(p, "/generate-jitconfig") {
			jitCount++
			return ciResponse(map[string]any{"runner": map[string]any{"id": jobID + 100}, "encoded_jit_config": "disposable-forge-fixture"}), nil
		}
		if strings.Contains(p, "/actions/runners/") && r.Method == "DELETE" {
			deregCount++
			return ciResponse(map[string]any{}), nil
		}
		if strings.HasSuffix(p, "/actions/runners") {
			return ciResponse(map[string]any{"runners": []any{}}), nil
		}
		if strings.HasSuffix(p, "/cancel") {
			cancelCount++
			return ciResponse(map[string]any{}), nil
		}
		if strings.HasSuffix(p, "/logs") {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(log))}, nil
		}
		if strings.HasSuffix(p, "/artifacts") {
			return ciResponse(map[string]any{"artifacts": []any{}}), nil
		}
		if strings.Contains(p, "/contents/") {
			return ciResponse(map[string]any{"encoding": "base64", "content": base64.StdEncoding.EncodeToString(workflow)}), nil
		}
		job := map[string]any{"id": jobID, "run_id": 42, "run_attempt": 1, "status": "queued", "labels": []string{"self-hosted", "lamp-demo", "lamp-demo-42-1"}}
		if active != "" {
			j := s.ciSnapshot(active)
			if textField(j.R, "state") == "running" {
				job["status"] = "in_progress"
				job["runner_id"] = jobID + 100
				sandbox := textField(j.R, "sandbox_id")
				exists, _, size, err := s.guestStat(ctx, sandbox, "/tmp/lamp-ci-demo-finished")
				if err == nil && exists && !cancelMode {
					job["status"] = "completed"
					job["conclusion"] = "success"
				}
				_, _, size, _ = s.guestStat(ctx, sandbox, "/tmp/lamp-ci-demo.log")
				if size > 0 {
					log, _ = s.guestReadFile(ctx, sandbox, "/tmp/lamp-ci-demo.log", size, 1<<20)
				}
			}
		}
		if strings.Contains(p, "/actions/jobs/") {
			return ciResponse(job), nil
		}
		if strings.HasSuffix(p, "/jobs") {
			return ciResponse(map[string]any{"jobs": []any{job}}), nil
		}
		if strings.Contains(p, "/actions/runs/") {
			return ciResponse(map[string]any{"id": 42, "event": map[bool]string{true: "push", false: "pull_request"}[trusted], "head_sha": strings.Repeat("b", 40), "head_branch": "stranger", "path": ".github/workflows/ci.yml", "run_attempt": 1, "repository": map[string]any{"id": repo.ID, "private": trusted, "owner": map[string]any{"id": repo.OwnerID}}}), nil
		}
		return nil, fmt.Errorf("unexpected controlled-forge request")
	})}
	run := func(cancelJob bool) string {
		status, out := ciCall(t, s, "POST", "/v1/jobs", map[string]any{"plugin_id": "pg_github", "external_id": fmt.Sprintf("madler/zlib/%d", jobID)}, "", "")
		if status >= 400 {
			t.Fatalf("job admission HTTP %d", status)
		}
		id := textField(out["resource_ids"].([]any)[0].(map[string]any), "id")
		mu.Lock()
		active = id
		cancelMode = cancelJob
		mu.Unlock()
		if cancelJob {
			for {
				j := s.ciSnapshot(id)
				if textField(j.R, "state") == "running" {
					sandbox := textField(j.R, "sandbox_id")
					_, _, size, e := s.guestStat(ctx, sandbox, "/tmp/lamp-ci-demo.log")
					if e == nil && size > 0 {
						data, _ := s.guestReadFile(ctx, sandbox, "/tmp/lamp-ci-demo.log", size, 1<<20)
						if !bytes.Contains(data, []byte("fresh-private-root")) {
							t.Fatal("second job inherited private root")
						}
						mu.Lock()
						log = data
						mu.Unlock()
						status, _ = ciCall(t, s, "POST", "/v1/jobs/"+id+":cancel", map[string]any{}, "", fmt.Sprint(j.R["revision"]))
						if status >= 400 {
							t.Fatal("cancel was rejected")
						}
						break
					}
				}
				select {
				case <-ctx.Done():
					t.Fatal("second runner did not start")
				case <-time.After(50 * time.Millisecond):
				}
			}
		}
		ciWait(t, s, out)
		j := s.ciSnapshot(id)
		want := "completed"
		if cancelJob {
			want = "canceled"
		}
		if textField(j.R, "state") != want {
			t.Fatalf("job state %s", textField(j.R, "state"))
		}
		sandbox := textField(j.R, "sandbox_id")
		s.readLock()
		sandboxState := string(s.journal.Sandboxes[sandbox].State)
		s.mu.Unlock()
		wantSandbox := "deleted"
		if trusted {
			wantSandbox = "suspended"
		}
		if sandboxState != wantSandbox {
			t.Fatalf("job VM state %s, want %s", sandboxState, wantSandbox)
		}
		t.Logf("real Firecracker job %s: %s, sandbox %s %s", id, want, sandbox, wantSandbox)
		return id
	}
	first := run(false)
	mu.Lock()
	firstLog := append([]byte{}, log...)
	firstSandbox := textField(s.ciSnapshot(first).R, "sandbox_id")
	jobID = 2
	active = ""
	mu.Unlock()
	if !bytes.Contains(firstLog, []byte("zlib-compression-roundtrip-ok")) {
		t.Fatal("public third-party workload did not complete")
	}
	second := run(true)
	if textField(s.ciSnapshot(second).R, "sandbox_id") == firstSandbox {
		t.Fatal("sandbox identity was reused")
	}
	j := s.ciSnapshot(first)
	artifacts := stringsField(j.R, "artifact_ids")
	if len(artifacts) != 1 {
		t.Fatal("durable native-output artifact missing")
	}
	s.readLock()
	artifact := s.life.Artifacts[artifacts[0]]
	blob := s.life.Blobs[textField(artifact.R, "download_blob_id")]
	data, e := s.readObject(textField(blob.R, "sha256"))
	s.mu.Unlock()
	if e != nil || !bytes.Equal(data, firstLog) {
		t.Fatal("output did not survive VM deletion")
	}
	mu.Lock()
	jobID = 3
	active = ""
	trusted = true
	repo.RetainStopped = true
	s.github.Config.Repositories = []ci.Repository{repo}
	mu.Unlock()
	retained := run(false)
	j = s.ciSnapshot(retained)
	sandbox := textField(j.R, "sandbox_id")
	status, _ := ciCall(t, s, "POST", "/v1/sandboxes/"+sandbox+":resume", map[string]any{}, "", func() string {
		s.readLock()
		defer s.mu.Unlock()
		return fmt.Sprint(s.journal.Sandboxes[sandbox].Revision)
	}())
	if status != 409 {
		t.Fatal("old job authority resumed the retained VM")
	}
	status, budgetCreated := ciCall(t, s, "POST", "/v1/budgets", map[string]any{"wall_time_ms": 600000}, "", "")
	if status >= 400 {
		t.Fatal("inspection budget creation failed")
	}
	budget := textField(ciWait(t, s, budgetCreated), "budget_id")
	status, created := ciCall(t, s, "POST", "/v1/tasks", map[string]any{"budget_id": budget}, "", "")
	if status >= 400 {
		t.Fatal("inspection Task creation failed")
	}
	task := textField(ciWait(t, s, created), "task_id")
	s.readLock()
	rev := s.journal.Sandboxes[sandbox].Revision
	s.mu.Unlock()
	if e := ownerMachineAdmission(); e != nil {
		t.Fatal(e)
	}
	status, resumed := ciCall(t, s, "POST", "/v1/sandboxes/"+sandbox+":resume", map[string]any{"continuation_task_id": task, "expected_task_id": textField(j.R, "task_id")}, "", fmt.Sprint(rev))
	if status >= 400 {
		t.Fatalf("new Task inspection resume HTTP %d", status)
	}
	ciWait(t, s, resumed)
	res, e := s.guestRun(ctx, sandbox, "cat", "/tmp/lamp-ci-private-marker")
	if e != nil || res.Exit != 0 || !bytes.Contains(res.Stdout, []byte("private-job-state")) {
		t.Fatal("retained inspection state was lost")
	}
	s.readLock()
	inspect := s.journal.Sandboxes[sandbox]
	s.mu.Unlock()
	s.internalDelete(inspect, "ci-demo-inspection")
	for {
		s.readLock()
		deleted := s.journal.Sandboxes[sandbox].State == "deleted"
		s.mu.Unlock()
		if deleted {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("inspection cleanup incomplete")
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Log("trusted retention stopped and released execution; old Task resume denied; new Task inspected the saved memory, then deleted it")
	mu.Lock()
	defer mu.Unlock()
	if jitCount != 3 || deregCount != 3 || cancelCount != 1 {
		t.Fatal("one-job issue/revoke/cancel lifecycle incorrect")
	}
	t.Log("distinct private roots; second job cancellation; durable output readable after deletion; controlled forge issued and revoked exactly one authority per job")
}
