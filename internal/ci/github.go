// Package ci contains forge adapters; execution and durable ownership belong to
// the control service. Credentials never enter demand records or error text.
package ci

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const GitHubIssuer = "https://token.actions.githubusercontent.com"

// Repository is host-enrolled policy, not workflow input. IDs survive renames.
// Public workflow bytes must be explicitly reviewed and pinned by the owner.
// ModelBroker is trusted host enrollment. Workflows select exact broker
// connection/operation scopes; neither labels nor workflow input can enroll it.
type ModelBroker struct {
	OwnerSocket string                         `json:"owner_socket"`
	Endpoint    string                         `json:"endpoint"`
	CAFile      string                         `json:"ca_file"`
	PeerIP      string                         `json:"peer_ip"`
	Workflows   map[string]map[string][]string `json:"workflows"`
}
type Repository struct {
	Name            string            `json:"name"`
	ID              int64             `json:"id"`
	OwnerID         int64             `json:"owner_id"`
	Label           string            `json:"label"`
	TemplateID      string            `json:"template_id"`
	RunnerDirectory string            `json:"runner_directory"`
	WorkflowSHA256  map[string]string `json:"workflow_sha256"`
	RetainStopped   bool              `json:"retain_stopped"`
	// Nil preserves reviewed-private-workflow trust. An explicit empty list
	// trusts no actor; PR events remain untrusted even for an enrolled actor.
	TrustedActorIDs []int64      `json:"trusted_actor_ids"`
	EnableCopilot   bool         `json:"enable_copilot"`
	ModelBroker     *ModelBroker `json:"model_broker,omitempty"`
}
type Config struct {
	TokenFile      string       `json:"token_file"`
	AuditDirectory string       `json:"audit_directory"`
	NetworkReader  []string     `json:"network_reader"`
	Repositories   []Repository `json:"repositories"`
	PollSeconds    int          `json:"poll_seconds"`
	// Exact immutable trusted identities; public/PR identities never get a key.
	OIDC []OIDCPolicy `json:"oidc"`
}
type OIDCPolicy struct {
	RepositoryID string   `json:"repository_id"`
	OwnerID      string   `json:"repository_owner_id"`
	WorkflowRef  string   `json:"workflow_ref"`
	WorkflowSHA  string   `json:"workflow_sha"`
	Ref          string   `json:"ref"`
	Event        string   `json:"event"`
	ResourceIDs  []string `json:"resource_ids"`
	Actions      []string `json:"actions"`
}
type GitHub struct {
	Config Config
	HTTP   *http.Client
}

func New(config Config) (*GitHub, error) {
	if config.PollSeconds < 0 || config.PollSeconds > 3600 {
		return nil, errors.New("invalid CI poll interval")
	}
	seen := map[string]bool{}
	for _, r := range config.Repositories {
		if len(strings.Split(r.Name, "/")) != 2 || strings.ContainsAny(r.Name, " ?#%\\\r\n") || r.ID <= 0 || r.OwnerID <= 0 || r.Label == "" || r.TemplateID == "" || !strings.HasPrefix(r.RunnerDirectory, "/") || seen[r.Name] {
			return nil, errors.New("invalid CI repository enrollment")
		}
		if b := r.ModelBroker; b != nil {
			u, e := url.Parse(b.Endpoint)
			if e != nil || u == nil {
				return nil, errors.New("invalid model broker endpoint")
			}
			ip := net.ParseIP(u.Hostname())
			if u.Scheme != "https" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || ip == nil || !ip.IsPrivate() || u.Port() == "" || !filepath.IsAbs(b.OwnerSocket) || !filepath.IsAbs(b.CAFile) || net.ParseIP(b.PeerIP) == nil || len(b.Workflows) == 0 {
				return nil, errors.New("model broker needs an enrolled private HTTPS endpoint, CA and owner socket")
			}
			for path, connections := range b.Workflows {
				if r.WorkflowSHA256[path] == "" || len(connections) == 0 {
					return nil, errors.New("model broker workflow must be reviewed and scoped")
				}
				for name, ops := range connections {
					if name == "" || len(ops) == 0 {
						return nil, errors.New("model broker needs exact connections and operations")
					}
					for _, op := range ops {
						if op != "messages" && op != "responses" && op != "chat" {
							return nil, errors.New("CI model grant cannot authorize other broker operations")
						}
					}
				}
			}
		}
		seen[r.Name] = true
	}
	for _, p := range config.OIDC {
		if p.RepositoryID == "" || p.OwnerID == "" || p.WorkflowRef == "" || len(p.WorkflowSHA) != 40 || p.Ref == "" || p.Event == "" || strings.Contains(p.Event, "pull_request") || len(p.ResourceIDs) == 0 || len(p.Actions) == 0 {
			return nil, errors.New("OIDC policy needs exact trusted identity and resource/action scope")
		}
		for _, a := range p.Actions {
			if !strings.HasSuffix(a, ".get") && !strings.HasSuffix(a, ".list") && a != "exec.create" && a != "exec.watch" {
				return nil, errors.New("OIDC action cannot mint or manage authority")
			}
		}
	}
	return &GitHub{Config: config, HTTP: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// API deliberately returns only a status, never GitHub bodies or credentials.
func (g *GitHub) API(ctx context.Context, method, path string, input, out any) error {
	if !strings.HasPrefix(path, "/repos/") || strings.Contains(path, "..") {
		return errors.New("invalid forge API path")
	}
	token, err := os.ReadFile(g.Config.TokenFile)
	if err != nil {
		return errors.New("forge credential unavailable")
	}
	st, err := os.Stat(g.Config.TokenFile)
	if err != nil || st.Mode().Perm()&0077 != 0 {
		return errors.New("forge credential must be private")
	}
	var body io.Reader
	if input != nil {
		b, e := json.Marshal(input)
		if e != nil {
			return e
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://api.github.com"+path, body)
	if err != nil {
		return errors.New("invalid forge request")
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")
	res, err := g.HTTP.Do(req)
	if err != nil {
		return errors.New("forge transport unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("forge request rejected (HTTP %d)", res.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(out)
	}
	return nil
}

type Job struct {
	ID         int64    `json:"id"`
	RunID      int64    `json:"run_id"`
	RunAttempt int      `json:"run_attempt"`
	Status     string   `json:"status"`
	Conclusion string   `json:"conclusion"`
	Labels     []string `json:"labels"`
	RunnerID   int64    `json:"runner_id"`
	Name       string   `json:"name"`
}
type Run struct {
	Actor struct {
		ID int64 `json:"id"`
	} `json:"actor"`
	ID         int64  `json:"id"`
	Event      string `json:"event"`
	HeadSHA    string `json:"head_sha"`
	HeadBranch string `json:"head_branch"`
	Path       string `json:"path"`
	Status     string `json:"status"`
	RunAttempt int    `json:"run_attempt"`
	Repository struct {
		ID      int64 `json:"id"`
		Private bool  `json:"private"`
		Owner   struct {
			ID int64 `json:"id"`
		} `json:"owner"`
	} `json:"repository"`
}
type Demand struct {
	Job         Job
	Run         Run
	Repository  Repository
	Public      bool
	Workflow    []byte
	RunnerLabel string
	Route       string
}

func (g *GitHub) GetJob(ctx context.Context, r Repository, id int64) (Job, error) {
	var j Job
	err := g.API(ctx, "GET", fmt.Sprintf("/repos/%s/actions/jobs/%d", r.Name, id), nil, &j)
	return j, err
}
func (g *GitHub) Verify(ctx context.Context, r Repository, id int64) (Demand, error) {
	d := Demand{Repository: r}
	var err error
	d.Job, err = g.GetJob(ctx, r, id)
	if err != nil {
		return d, err
	}
	if d.Job.ID != id || d.Job.Status != "queued" {
		return d, errors.New("job is no longer queued")
	}
	found := false
	for _, l := range d.Job.Labels {
		if l == r.Label {
			found = true
		}
	}
	if !found {
		return d, errors.New("job does not request enrolled runner label")
	}
	err = g.API(ctx, "GET", fmt.Sprintf("/repos/%s/actions/runs/%d", r.Name, d.Job.RunID), nil, &d.Run)
	if err != nil {
		return d, err
	}
	if d.Run.Repository.ID != r.ID || d.Run.Repository.Owner.ID != r.OwnerID || d.Run.RunAttempt != d.Job.RunAttempt {
		return d, errors.New("immutable forge identity mismatch")
	}
	if d.Run.Event == "pull_request_target" || d.Run.Event == "workflow_run" {
		return d, errors.New("privileged trigger cannot execute CI head code")
	}
	d.Public = !d.Run.Repository.Private || !TrustedEvent(d.Run.Event)
	if r.TrustedActorIDs != nil {
		trusted := false
		for _, id := range r.TrustedActorIDs {
			if d.Run.Actor.ID == id {
				trusted = true
				break
			}
		}
		d.Public = d.Public || !trusted
	}
	sourcePath := d.Run.Path
	d.Route = "workflow"
	if d.Run.Path == "dynamic/copilot-swe-agent/copilot" {
		// This reserved path is synthesized by GitHub, not supplied by repository
		// YAML. Opt in only for the immutable GitHub Copilot bot in a trusted private
		// repository; review the setup file it actually inherits at this head SHA.
		if !r.EnableCopilot || d.Public || d.Run.Actor.ID != 198982749 || d.Run.Event != "dynamic" {
			return d, errors.New("Copilot source/auth route is not enrolled")
		}
		sourcePath = ".github/workflows/copilot-setup-steps.yml"
		d.Route = "copilot_cloud_agent"
	}
	if strings.ContainsAny(sourcePath, "@?#%\\") || strings.Contains(sourcePath, "..") || !strings.HasPrefix(sourcePath, ".github/workflows/") || len(d.Run.HeadSHA) != 40 {
		return d, errors.New("unsupported workflow source")
	}
	var file struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err = g.API(ctx, "GET", "/repos/"+r.Name+"/contents/"+sourcePath+"?ref="+url.QueryEscape(d.Run.HeadSHA), nil, &file); err != nil {
		return d, err
	}
	if file.Encoding != "base64" {
		return d, errors.New("workflow content unavailable")
	}
	d.Workflow, err = base64.StdEncoding.DecodeString(strings.ReplaceAll(file.Content, "\n", ""))
	if err != nil {
		return d, errors.New("workflow content unavailable")
	}
	digest := sha256.Sum256(d.Workflow)
	if r.WorkflowSHA256[sourcePath] != hex.EncodeToString(digest[:]) {
		return d, errors.New("workflow has not been reviewed at this exact digest")
	}
	prefix := RunLabel(r, d.Run)
	unique := ""
	for _, l := range d.Job.Labels {
		if l == prefix || strings.HasPrefix(l, prefix+"-") {
			if unique != "" {
				return d, errors.New("ambiguous per-run label")
			}
			unique = l
		}
	}
	d.RunnerLabel = unique
	for _, label := range d.Job.Labels {
		supported := false
		for _, allowed := range []string{"self-hosted", "linux", "x64", r.Label, unique} {
			if strings.EqualFold(label, allowed) {
				supported = true
				break
			}
		}
		if !supported {
			return d, errors.New("requested runner labels are unsupported by the enrolled Linux x64 template")
		}
	}
	if unique == "" {
		return d, errors.New("job needs the per-run attempt runner label")
	}
	jobs, err := g.jobs(ctx, r, d.Run.ID)
	if err != nil {
		return d, err
	}
	matches := 0
	for _, j := range jobs {
		for _, l := range j.Labels {
			if l == unique {
				matches++
				break
			}
		}
	}
	if matches != 1 {
		return d, errors.New("per-run label must select exactly one queued job")
	}
	if d.Public {
		if err = PublicWorkflow(d.Workflow); err != nil {
			return d, err
		}
	}
	return d, nil
}

// PublicWorkflow is deliberately conservative. Pinned host review remains
// mandatory as native actions can request additional credentials themselves.
func PublicWorkflow(data []byte) error {
	lower := strings.ToLower(string(data))
	if strings.Contains(lower, "secrets") || strings.Contains(lower, "pull_request_target") || strings.Contains(lower, "workflow_run") {
		return errors.New("public workflow requests privileged context")
	}
	var w struct {
		Permissions map[string]string `yaml:"permissions"`
		Jobs        map[string]struct {
			Permissions map[string]string `yaml:"permissions"`
			Environment any               `yaml:"environment"`
			Uses        string            `yaml:"uses"`
		} `yaml:"jobs"`
	}
	if yaml.Unmarshal(data, &w) != nil || w.Permissions == nil {
		return errors.New("public workflow needs explicit read-only permissions")
	}
	readOnly := func(p map[string]string) bool {
		for _, v := range p {
			if v != "read" && v != "none" {
				return false
			}
		}
		return true
	}
	if !readOnly(w.Permissions) {
		return errors.New("public workflow permissions exceed read-only")
	}
	for _, j := range w.Jobs {
		if j.Environment != nil || j.Uses != "" || !readOnly(j.Permissions) {
			return errors.New("public workflow has privileged job or reusable workflow")
		}
	}
	return nil
}
func RunLabel(r Repository, run Run) string {
	return fmt.Sprintf("%s-%d-%d", r.Label, run.ID, run.RunAttempt)
}
func (g *GitHub) JIT(ctx context.Context, r Repository, uniqueLabel string, name string) (int64, string, error) {
	var out struct {
		Runner struct {
			ID int64 `json:"id"`
		} `json:"runner"`
		Encoded string `json:"encoded_jit_config"`
	}
	err := g.API(ctx, "POST", "/repos/"+r.Name+"/actions/runners/generate-jitconfig", map[string]any{"name": name, "runner_group_id": 1, "labels": []string{"self-hosted", "linux", "x64", r.Label, uniqueLabel}, "work_folder": "_work"}, &out)
	if err == nil && (out.Runner.ID == 0 || out.Encoded == "") {
		err = errors.New("forge returned incomplete one-job authority")
	}
	return out.Runner.ID, out.Encoded, err
}
func (g *GitHub) Deregister(ctx context.Context, r Repository, id int64) error {
	if id == 0 {
		return nil
	}
	err := g.API(ctx, "DELETE", "/repos/"+r.Name+"/actions/runners/"+strconv.FormatInt(id, 10), nil, nil)
	if err != nil && strings.Contains(err.Error(), "HTTP 404") {
		return nil
	}
	return err
}

// DeregisterJob also finds a JIT registration whose successful response was
// lost before its ID became durable. Runner names are host-generated UUIDs.
func (g *GitHub) DeregisterJob(ctx context.Context, r Repository, name string, known int64) error {
	if err := g.Deregister(ctx, r, known); err != nil {
		return err
	}
	for page := 1; ; page++ {
		var out struct {
			Runners []struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"runners"`
		}
		if err := g.API(ctx, "GET", fmt.Sprintf("/repos/%s/actions/runners?per_page=100&page=%d", r.Name, page), nil, &out); err != nil {
			return err
		}
		for _, v := range out.Runners {
			if v.Name == name {
				if err := g.Deregister(ctx, r, v.ID); err != nil {
					return err
				}
			}
		}
		if len(out.Runners) < 100 {
			return nil
		}
	}
}
func (g *GitHub) Cancel(ctx context.Context, r Repository, run int64) error {
	return g.API(ctx, "POST", fmt.Sprintf("/repos/%s/actions/runs/%d/cancel", r.Name, run), nil, nil)
}
func (g *GitHub) Queued(ctx context.Context, r Repository) ([]Job, error) {
	var all []Job
	// GitHub's run list is bounded per request; pagination prevents a busy queue
	// hiding older work. No persistent runner is registered by the poller.
	for page := 1; page <= 10; page++ {
		var runs struct {
			Runs []Run `json:"workflow_runs"`
		}
		if err := g.API(ctx, "GET", fmt.Sprintf("/repos/%s/actions/runs?status=queued&per_page=100&page=%d", r.Name, page), nil, &runs); err != nil {
			return nil, err
		}
		// in_progress runs can have queued jobs after their dependencies finish;
		// caller also queries those through QueuedInProgress below.
		for _, run := range runs.Runs {
			jobs, err := g.jobs(ctx, r, run.ID)
			if err != nil {
				return nil, err
			}
			all = append(all, jobs...)
		}
		if len(runs.Runs) < 100 {
			break
		}
	}
	return all, nil
}
func (g *GitHub) jobs(ctx context.Context, r Repository, run int64) ([]Job, error) {
	var all []Job
	for page := 1; page <= 10; page++ {
		var out struct {
			Jobs []Job `json:"jobs"`
		}
		if err := g.API(ctx, "GET", fmt.Sprintf("/repos/%s/actions/runs/%d/jobs?filter=latest&per_page=100&page=%d", r.Name, run, page), nil, &out); err != nil {
			return nil, err
		}
		for _, j := range out.Jobs {
			if j.Status == "queued" {
				for _, l := range j.Labels {
					if l == r.Label {
						all = append(all, j)
						break
					}
				}
			}
		}
		if len(out.Jobs) < 100 {
			break
		}
	}
	return all, nil
}
func (g *GitHub) QueuedInProgress(ctx context.Context, r Repository) ([]Job, error) {
	var all []Job
	for page := 1; page <= 10; page++ {
		var out struct {
			Runs []Run `json:"workflow_runs"`
		}
		if err := g.API(ctx, "GET", fmt.Sprintf("/repos/%s/actions/runs?status=in_progress&per_page=100&page=%d", r.Name, page), nil, &out); err != nil {
			return nil, err
		}
		for _, run := range out.Runs {
			jobs, err := g.jobs(ctx, r, run.ID)
			if err != nil {
				return nil, err
			}
			all = append(all, jobs...)
		}
		if len(out.Runs) < 100 {
			break
		}
	}
	return all, nil
}

// TrustedEvent reports whether a workflow event runs only code that a trusted
// repository writer chose. Every other event (pull_request, the PR review and
// comment events, issue_comment, and any event GitHub adds later) may run or be
// steered by a stranger's pull request and is handled as public_pr.
func TrustedEvent(event string) bool {
	switch event {
	case "push", "workflow_dispatch", "schedule", "merge_group", "dynamic":
		return true
	}
	return false
}
