// Command lamp-gitlab-executor is a GitLab Runner Custom executor driver: each
// GitLab job runs in a fresh Lamp sandbox that is deleted at cleanup. It uses
// only Lamp's public API (LAMP_SOCKET or LAMP_API_URL, bearer in LAMP_TOKEN
// or LAMP_TOKEN_FILE).
//
// config.toml:
//
//	[runners.custom]
//	  config_exec  = "lamp-gitlab-executor"  config_args  = ["config"]
//	  prepare_exec = "lamp-gitlab-executor"  prepare_args = ["prepare"]
//	  run_exec     = "lamp-gitlab-executor"  run_args     = ["run"]
//	  cleanup_exec = "lamp-gitlab-executor"  cleanup_args = ["cleanup"]
//
// LAMP_GITLAB_RECIPE is the trusted recipe; LAMP_GITLAB_PUBLIC_RECIPE is used
// for anything a stranger can steer (fork merge requests, public projects,
// external pull requests). With no public recipe such jobs are refused.
// `lamp-gitlab-executor reap` deletes sandboxes left by a lost runner host.
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type client struct {
	http  *http.Client
	base  string
	token string
}

func newClient() (*client, error) {
	c := &client{http: &http.Client{Timeout: 90 * time.Second}, base: strings.TrimSuffix(os.Getenv("LAMP_API_URL"), "/")}
	if c.base == "" {
		socket := os.Getenv("LAMP_SOCKET")
		if socket == "" {
			return nil, errors.New("LAMP_SOCKET or LAMP_API_URL required")
		}
		c.base = "http://lamp"
		c.http.Transport = &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		}}
	}
	c.token = os.Getenv("LAMP_TOKEN")
	if f := os.Getenv("LAMP_TOKEN_FILE"); f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, errors.New("LAMP_TOKEN_FILE unreadable")
		}
		c.token = strings.TrimSpace(string(b))
	}
	return c, nil
}

func (c *client) do(method, path string, body any, headers map[string]string) (map[string]any, error) {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, r)
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, errors.New("Lamp API unavailable")
	}
	defer res.Body.Close()
	var out map[string]any
	json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(&out)
	if res.StatusCode >= 300 {
		msg := ""
		if e, ok := out["error"].(map[string]any); ok {
			msg = fmt.Sprint(e["code"], ": ", e["message"])
		}
		return out, fmt.Errorf("Lamp %s %s: HTTP %d %s", method, path, res.StatusCode, msg)
	}
	return out, nil
}

func env(k string) string { return os.Getenv("CUSTOM_ENV_" + k) }

// public reports work a stranger can steer.
func public() bool {
	src := env("CI_PIPELINE_SOURCE")
	fork := env("CI_MERGE_REQUEST_SOURCE_PROJECT_ID") != "" && env("CI_MERGE_REQUEST_SOURCE_PROJECT_ID") != env("CI_PROJECT_ID")
	return fork || env("CI_PROJECT_VISIBILITY") == "public" || src == "external_pull_request_event" || src == "chat"
}

func state(job string) string {
	dir := os.Getenv("LAMP_GITLAB_STATE_DIR")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "lamp-gitlab-executor")
	}
	os.MkdirAll(dir, 0700)
	return filepath.Join(dir, job)
}

func systemFailure(err error) int {
	fmt.Fprintln(os.Stderr, "lamp-gitlab-executor:", err)
	if code, e := strconv.Atoi(os.Getenv("SYSTEM_FAILURE_EXIT_CODE")); e == nil {
		return code
	}
	return 1
}

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 0 {
		return systemFailure(errors.New("usage: config|prepare|run <script> <stage>|cleanup|reap"))
	}
	if args[0] == "config" {
		json.NewEncoder(os.Stdout).Encode(map[string]any{"builds_dir": "/var/tmp/lamp-ci/builds", "cache_dir": "/var/tmp/lamp-ci/cache",
			"builds_dir_is_shared": false, "driver": map[string]string{"name": "lamp", "version": "1"}})
		return 0
	}
	c, err := newClient()
	if err != nil {
		return systemFailure(err)
	}
	job := env("CI_JOB_ID")
	if args[0] != "reap" && (job == "" || strings.ContainsAny(job, "/.")) {
		return systemFailure(errors.New("CUSTOM_ENV_CI_JOB_ID missing"))
	}
	switch args[0] {
	case "prepare":
		return prepare(c, job)
	case "run":
		if len(args) < 3 {
			return systemFailure(errors.New("run needs <script> <stage>"))
		}
		return runStage(c, job, args[1], args[2])
	case "cleanup":
		return cleanup(c, job)
	case "reap":
		return reap(c)
	}
	return systemFailure(errors.New("unknown stage " + args[0]))
}

func prepare(c *client, job string) int {
	recipe, trust := os.Getenv("LAMP_GITLAB_RECIPE"), "trusted"
	if public() {
		recipe, trust = os.Getenv("LAMP_GITLAB_PUBLIC_RECIPE"), "public_pr"
		if recipe == "" {
			return systemFailure(errors.New("public work needs LAMP_GITLAB_PUBLIC_RECIPE (deny-by-default network, no secrets)"))
		}
	}
	server := env("CI_SERVER_HOST")
	out, err := c.do("POST", "/v1/sandboxes", map[string]any{"recipe_id": recipe},
		map[string]string{"Idempotency-Key": "gitlab-custom:" + server + ":" + job})
	if err != nil {
		return systemFailure(err)
	}
	op, _ := out["operation_id"].(string)
	sandbox := ""
	if refs, ok := out["resource_ids"].([]any); ok && len(refs) > 0 {
		sandbox, _ = refs[0].(map[string]any)["id"].(string)
	}
	if err = os.WriteFile(state(job), []byte(sandbox), 0600); err != nil {
		return systemFailure(err)
	}
	if err = wait(c, op); err != nil {
		return systemFailure(err)
	}
	// Artifact and cache stages call the runner's own helper by name.
	execute(c, sandbox, []string{"sh", "-c", "[ -x /opt/gitlab-runner/gitlab-runner ] && ln -sf /opt/gitlab-runner/gitlab-runner /usr/local/bin/gitlab-runner || true"}, "gl:"+job+":helper", false)
	fmt.Printf("Lamp sandbox %s (%s) ready\n", sandbox, trust)
	return 0
}

func wait(c *client, op string) error {
	for {
		out, err := c.do("GET", "/v1/operations/"+op+"?wait_ms=25000", nil, nil)
		if err != nil {
			return err
		}
		data, _ := out["data"].(map[string]any)
		switch data["state"] {
		case "succeeded":
			return nil
		case "failed", "canceled":
			return fmt.Errorf("Lamp operation %s %v", op, data["state"])
		}
	}
}

func sandboxOf(job string) (string, error) {
	b, err := os.ReadFile(state(job))
	if err != nil || len(b) == 0 {
		return "", errors.New("no Lamp sandbox prepared for this job")
	}
	return string(b), nil
}

func execute(c *client, sandbox string, argv []string, key string, stream bool) (int64, error) {
	out, err := c.do("POST", "/v1/execs", map[string]any{"sandbox_id": sandbox, "argv": argv, "cwd": "/"}, map[string]string{"Idempotency-Key": key})
	if err != nil {
		return -1, err
	}
	id := ""
	if refs, ok := out["resource_ids"].([]any); ok && len(refs) > 0 {
		id, _ = refs[0].(map[string]any)["id"].(string)
	}
	// Interrupting the runner (GitLab job cancel or timeout) interrupts the
	// sandbox command; cleanup then deletes the sandbox.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sig)
	go func() {
		if _, ok := <-sig; ok {
			c.do("POST", "/v1/execs/"+id+":interrupt", map[string]any{}, map[string]string{"Idempotency-Key": "interrupt:" + id})
		}
	}()
	cursor := ""
	for {
		q := "/v1/execs/" + id + "/output?wait_ms=20000"
		if cursor != "" {
			q += "&cursor=" + cursor
		}
		w, err := c.do("GET", q, nil, nil)
		if err != nil {
			return -1, err
		}
		items, _ := w["items"].([]any)
		for _, it := range items {
			f, _ := it.(map[string]any)
			data, _ := base64.StdEncoding.DecodeString(fmt.Sprint(f["data_base64"]))
			switch f["channel"] {
			case "stdout":
				if stream {
					os.Stdout.Write(data)
				}
			case "stderr":
				if stream {
					os.Stderr.Write(data)
				}
			}
		}
		if next, _ := w["next_cursor"].(string); next != "" {
			cursor = next
		}
		g, err := c.do("GET", "/v1/execs/"+id, nil, nil)
		if err != nil {
			return -1, err
		}
		d, _ := g["data"].(map[string]any)
		if st := d["state"]; st == "exited" || st == "failed" || st == "outcome_unknown" {
			if len(items) > 0 {
				continue // drain remaining output first
			}
			if st != "exited" {
				return -1, fmt.Errorf("sandbox command %v", st)
			}
			code, _ := strconv.ParseInt(fmt.Sprint(d["exit_code"]), 10, 64)
			return code, nil
		}
	}
}

func runStage(c *client, job, script, stage string) int {
	sandbox, err := sandboxOf(job)
	if err != nil {
		return systemFailure(err)
	}
	body, err := os.ReadFile(script)
	if err != nil {
		return systemFailure(err)
	}
	target := "/var/tmp/lamp-ci/" + filepath.Base(stage) + ".sh"
	nonce := strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err = execute(c, sandbox, []string{"sh", "-c", "mkdir -p /var/tmp/lamp-ci && : > " + target}, "gl:"+job+":"+stage+":init:"+nonce, false); err != nil {
		return systemFailure(err)
	}
	for off, n := 0, 0; off < len(body); off, n = off+32<<10, n+1 {
		chunk := base64.StdEncoding.EncodeToString(body[off:min(off+32<<10, len(body))])
		code, err := execute(c, sandbox, []string{"python3", "-c", "import base64,sys;open(sys.argv[1],'ab').write(base64.b64decode(sys.argv[2]))", target, chunk}, fmt.Sprintf("gl:%s:%s:%s:%d", job, stage, nonce, n), false)
		if err != nil || code != 0 {
			return systemFailure(fmt.Errorf("script upload failed: %v", err))
		}
	}
	code, err := execute(c, sandbox, []string{"bash", target}, "gl:"+job+":"+stage+":run:"+nonce, true)
	if err != nil {
		return systemFailure(err)
	}
	if code != 0 {
		if b, e := strconv.Atoi(os.Getenv("BUILD_FAILURE_EXIT_CODE")); e == nil {
			return b
		}
		return 1
	}
	return 0
}

func deleteSandbox(c *client, sandbox, why string) error {
	out, err := c.do("GET", "/v1/sandboxes/"+sandbox, nil, nil)
	if err != nil {
		if strings.Contains(err.Error(), "HTTP 404") {
			return nil
		}
		return err
	}
	d, _ := out["data"].(map[string]any)
	if d["state"] == "deleted" {
		return nil
	}
	rev := fmt.Sprint(d["revision"])
	del, err := c.do("DELETE", "/v1/sandboxes/"+sandbox, nil, map[string]string{"Idempotency-Key": why + ":" + sandbox + ":" + rev, "If-Match": rev})
	if err != nil {
		return err
	}
	op, _ := del["operation_id"].(string)
	return wait(c, op)
}

func cleanup(c *client, job string) int {
	sandbox, err := sandboxOf(job)
	if err != nil {
		return 0 // prepare never created one
	}
	if err = deleteSandbox(c, sandbox, "gitlab-cleanup"); err != nil {
		return systemFailure(err)
	}
	os.Remove(state(job))
	return 0
}

// reap deletes this driver's sandboxes whose job state file is gone (a lost
// runner host or a cleanup that never ran) after a one-hour grace.
func reap(c *client) int {
	out, err := c.do("GET", "/v1/sandboxes?limit=1000", nil, nil)
	if err != nil {
		return systemFailure(err)
	}
	items, _ := out["items"].([]any)
	failed := 0
	for _, it := range items {
		sb, _ := it.(map[string]any)
		labels, _ := sb["labels"].(map[string]any)
		created, _ := time.Parse(time.RFC3339, fmt.Sprint(sb["created_at"]))
		if labels["lamp.ci"] != "gitlab" || sb["state"] == "deleted" || time.Since(created) < time.Hour {
			continue
		}
		if b, e := os.ReadFile(state(fmt.Sprint(labels["gitlab.job"]))); e == nil && string(b) == sb["id"] {
			continue
		}
		if deleteSandbox(c, fmt.Sprint(sb["id"]), "gitlab-reap") != nil {
			failed++
		}
	}
	if failed > 0 {
		return systemFailure(fmt.Errorf("%d orphan sandboxes need another reap", failed))
	}
	return 0
}
