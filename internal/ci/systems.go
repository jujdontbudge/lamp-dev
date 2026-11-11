package ci

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Adapters for CI systems other than GitHub. Each speaks only the system's own
// documented runner/agent contract; Lamp's control service owns everything
// else. Where a system has no native one-job mode, Agent.OneJob is false and
// the controller deletes the VM after the native job's final state, which ends
// the agent; a per-job tag or label limits it to that one job meanwhile.

func publicFor(r Repository, untrusted bool) bool {
	return untrusted || r.Options["visibility"] == "public"
}

func basic(user string) func(*http.Request, string) {
	return func(req *http.Request, token string) {
		u, p, ok := strings.Cut(token, ":")
		if !ok {
			u, p = user, token
		}
		req.SetBasicAuth(u, p)
	}
}

func headerAuth(name, prefix string) func(*http.Request, string) {
	return func(req *http.Request, token string) { req.Header.Set(name, prefix+token) }
}

func deregisterOK(err error) error {
	if err != nil && strings.Contains(err.Error(), "HTTP 404") {
		return nil
	}
	return err
}

// ---------------------------------------------------------------- GitLab --

// GitLab registers one project runner per job and runs `gitlab-runner
// run-single --max-builds 1`, GitLab's native single-build mode, in the fresh
// VM. The per-job runner token is created and deleted through the REST API.
// The Custom executor and Fleeting plugin (cmd/lamp-gitlab-executor,
// cmd/lamp-fleeting) are the other two GitLab contracts.
type GitLab struct {
	hostClient
	Repositories []Repository
}

func (g *GitLab) Plugin() string             { return "pg_gitlab" }
func (g *GitLab) System() string             { return "gitlab" }
func (g *GitLab) Issuer(r Repository) string { return strings.TrimSuffix(r.BaseURL, "/") }
func (g *GitLab) Repos() []Repository        { return g.Repositories }
func (g *GitLab) api(ctx context.Context, r Repository, method, path string, in, out any) error {
	return g.call(ctx, r, method, "/api/v4"+path, in, out, headerAuth("PRIVATE-TOKEN", ""))
}

type gitlabJob struct {
	ID       int64    `json:"id"`
	Status   string   `json:"status"`
	Name     string   `json:"name"`
	TagList  []string `json:"tag_list"`
	Pipeline struct {
		ID        int64  `json:"id"`
		ProjectID int64  `json:"project_id"`
		SHA       string `json:"sha"`
		Source    string `json:"source"`
	} `json:"pipeline"`
	Runner *struct {
		ID int64 `json:"id"`
	} `json:"runner"`
	Artifacts []struct {
		FileType string `json:"file_type"`
		Size     uint64 `json:"size"`
	} `json:"artifacts"`
}

func (g *GitLab) project(r Repository) string { return url.PathEscape(r.Project) }
func (g *GitLab) Queued(ctx context.Context, r Repository) ([]Job, error) {
	var jobs []gitlabJob
	if err := g.api(ctx, r, "GET", "/projects/"+g.project(r)+"/jobs?scope[]=pending&per_page=100", nil, &jobs); err != nil {
		return nil, err
	}
	var out []Job
	for _, j := range jobs {
		if slices.Contains(j.TagList, r.Label) {
			out = append(out, Job{ID: j.ID, RunID: j.Pipeline.ID, Status: "queued", Labels: j.TagList, Name: j.Name})
		}
	}
	return out, nil
}
func (g *GitLab) get(ctx context.Context, r Repository, id int64) (gitlabJob, error) {
	var j gitlabJob
	err := g.api(ctx, r, "GET", fmt.Sprintf("/projects/%s/jobs/%d", g.project(r), id), nil, &j)
	return j, err
}
func (g *GitLab) Verify(ctx context.Context, r Repository, id int64) (Demand, error) {
	d := Demand{Repository: r}
	j, err := g.get(ctx, r, id)
	if err != nil {
		return d, err
	}
	if j.ID != id || j.Status != "pending" || !slices.Contains(j.TagList, r.Label) {
		return d, errors.New("job is not pending for the enrolled runner tag")
	}
	for _, t := range j.TagList {
		if t != r.Label && t != "linux" && t != "x64" {
			return d, errors.New("requested runner tags are unsupported by the enrolled template")
		}
	}
	var p struct {
		ID         int64 `json:"id"`
		Visibility string
	}
	if err = g.api(ctx, r, "GET", "/projects/"+g.project(r), nil, &p); err != nil {
		return d, err
	}
	// Merge-request pipelines may run a fork's code; external and chat sources
	// are steered by people outside the project. Child and multi-project
	// pipelines (pipeline, parent_pipeline) may descend from a fork's MR
	// pipeline and are not traced upstream here, so they are public too.
	trusted := map[string]bool{"push": true, "web": true, "schedule": true, "api": true}
	d.Public = publicFor(r, p.Visibility == "public" || !trusted[j.Pipeline.Source])
	d.Job = Job{ID: j.ID, RunID: j.Pipeline.ID, Status: "queued", Labels: j.TagList, Name: j.Name}
	d.Run.ID, d.Run.HeadSHA, d.Run.Event, d.Run.Path = j.Pipeline.ID, j.Pipeline.SHA, j.Pipeline.Source, ".gitlab-ci.yml"
	d.Run.Repository.Private = p.Visibility != "public"
	d.Route = "pipeline"
	return d, nil
}
func (g *GitLab) Register(ctx context.Context, r Repository, d Demand, name string) (Agent, error) {
	var out struct {
		ID    int64  `json:"id"`
		Token string `json:"token"`
	}
	in := map[string]any{"runner_type": "project_type", "project_id": d.Repository.Options["project_id"], "description": name, "tag_list": strings.Join(d.Job.Labels, ","), "run_untagged": false, "locked": true, "maximum_timeout": 3600}
	if err := g.api(ctx, r, "POST", "/user/runners", in, &out); err != nil {
		return Agent{}, err
	}
	if out.ID == 0 || out.Token == "" {
		return Agent{ID: out.ID}, errors.New("GitLab returned incomplete runner authority")
	}
	return Agent{ID: out.ID, OneJob: true, Argv: []string{"./gitlab-runner", "run-single", "--url", strings.TrimSuffix(r.BaseURL, "/"), "--token", "$SECRET:TOKEN",
		"--name", name, "--executor", "shell", "--max-builds", "1", "--wait-timeout", "600", "--builds-dir", "/var/tmp/lamp-ci-runner/builds", "--cache-dir", "/var/tmp/lamp-ci-runner/cache"},
		Secret: map[string]string{"TOKEN": out.Token}}, nil
}
func (g *GitLab) GetJob(ctx context.Context, r Repository, id int64) (Job, error) {
	j, err := g.get(ctx, r, id)
	out := Job{ID: j.ID, RunID: j.Pipeline.ID, Labels: j.TagList, Name: j.Name}
	switch j.Status {
	case "created", "pending", "waiting_for_resource", "preparing", "scheduled", "manual":
		out.Status = "queued"
	case "running":
		out.Status = "in_progress"
	default:
		out.Status, out.Conclusion = "completed", conclusion(j.Status)
	}
	return out, err
}
func (g *GitLab) Cancel(ctx context.Context, r Repository, _, job int64) error {
	return g.api(ctx, r, "POST", fmt.Sprintf("/projects/%s/jobs/%d/cancel", g.project(r), job), nil, nil)
}
func (g *GitLab) DeregisterJob(ctx context.Context, r Repository, name string, known int64) error {
	if known != 0 {
		if err := deregisterOK(g.api(ctx, r, "DELETE", fmt.Sprintf("/runners/%d", known), nil, nil)); err != nil {
			return err
		}
	}
	var runners []struct {
		ID          int64  `json:"id"`
		Description string `json:"description"`
	}
	if err := g.api(ctx, r, "GET", "/projects/"+g.project(r)+"/runners?type=project_type&per_page=100", nil, &runners); err != nil {
		return err
	}
	for _, v := range runners {
		if v.Description == name {
			if err := deregisterOK(g.api(ctx, r, "DELETE", fmt.Sprintf("/runners/%d", v.ID), nil, nil)); err != nil {
				return err
			}
		}
	}
	return nil
}
func (g *GitLab) Outputs(ctx context.Context, r Repository, _, job int64) ([]Output, error) {
	j, err := g.get(ctx, r, job)
	out := []Output{{Name: "gitlab-job.log", Path: fmt.Sprintf("/api/v4/projects/%s/jobs/%d/trace", g.project(r), job)}}
	for _, a := range j.Artifacts {
		if a.FileType == "archive" {
			out = append(out, Output{Name: "artifacts.zip", Path: fmt.Sprintf("/api/v4/projects/%s/jobs/%d/artifacts", g.project(r), job), Size: a.Size})
		}
	}
	return out, err
}
func (g *GitLab) Download(ctx context.Context, r Repository, path string, limit int64) ([]byte, error) {
	return g.download(ctx, r, path, limit, headerAuth("PRIVATE-TOKEN", ""))
}

// ------------------------------------------------------------- Buildkite --

// Buildkite runs `buildkite-agent start --acquire-job <uuid>`: the agent
// accepts exactly that job and exits when it finishes. Lamp job IDs are build
// numbers; the build must have exactly one scheduled job on the enrolled queue.
type Buildkite struct {
	hostClient
	Repositories []Repository
}

func (b *Buildkite) Plugin() string           { return "pg_buildkite" }
func (b *Buildkite) System() string           { return "buildkite" }
func (b *Buildkite) Issuer(Repository) string { return "https://buildkite.com" }
func (b *Buildkite) Repos() []Repository      { return b.Repositories }
func (b *Buildkite) api(ctx context.Context, r Repository, method, path string, in, out any) error {
	return b.call(ctx, r, method, "/v2/organizations/"+url.PathEscape(r.Options["organization"])+path, in, out, bearer)
}

type bkBuild struct {
	Number  int64  `json:"number"`
	State   string `json:"state"`
	Commit  string `json:"commit"`
	Source  string `json:"source"`
	Branch  string `json:"branch"`
	PullReq *struct {
		Repository string `json:"repository"`
	} `json:"pull_request"`
	Jobs []struct {
		ID         string   `json:"id"`
		Type       string   `json:"type"`
		State      string   `json:"state"`
		AgentRules []string `json:"agent_query_rules"`
		Agent      *struct {
			Name string `json:"name"`
		} `json:"agent"`
	} `json:"jobs"`
}

func (b *Buildkite) build(ctx context.Context, r Repository, n int64) (bkBuild, error) {
	var out bkBuild
	err := b.api(ctx, r, "GET", fmt.Sprintf("/pipelines/%s/builds/%d", url.PathEscape(r.Project), n), nil, &out)
	return out, err
}
func (b *Buildkite) queueJob(r Repository, out bkBuild) (int, error) {
	found := -1
	for i, j := range out.Jobs {
		if j.Type == "script" && slices.Contains(j.AgentRules, "queue="+r.Label) {
			if found >= 0 {
				return -1, errors.New("build has more than one job for the enrolled queue")
			}
			found = i
		}
	}
	if found < 0 {
		return -1, errors.New("build has no job for the enrolled queue")
	}
	return found, nil
}
func (b *Buildkite) Queued(ctx context.Context, r Repository) ([]Job, error) {
	var builds []bkBuild
	if err := b.api(ctx, r, "GET", "/pipelines/"+url.PathEscape(r.Project)+"/builds?state[]=scheduled&state[]=running&per_page=100", nil, &builds); err != nil {
		return nil, err
	}
	var out []Job
	for _, v := range builds {
		if i, err := b.queueJob(r, v); err == nil && v.Jobs[i].State == "scheduled" {
			out = append(out, Job{ID: v.Number, RunID: v.Number, Status: "queued", Name: v.Jobs[i].ID})
		}
	}
	return out, nil
}
func (b *Buildkite) Verify(ctx context.Context, r Repository, id int64) (Demand, error) {
	d := Demand{Repository: r}
	v, err := b.build(ctx, r, id)
	if err != nil {
		return d, err
	}
	i, err := b.queueJob(r, v)
	if err != nil {
		return d, err
	}
	if v.Jobs[i].State != "scheduled" {
		return d, errors.New("job is no longer scheduled")
	}
	for _, rule := range v.Jobs[i].AgentRules {
		if rule != "queue="+r.Label && rule != "os=linux" {
			return d, errors.New("job's agent rules are unsupported by the enrolled template")
		}
	}
	// A pull request from another repository is a fork build: public work.
	d.Public = publicFor(r, v.PullReq != nil && v.PullReq.Repository != "" && v.PullReq.Repository != r.Options["repository"])
	d.Job = Job{ID: v.Number, RunID: v.Number, Status: "queued", Name: v.Jobs[i].ID}
	d.Run.ID, d.Run.HeadSHA, d.Run.Event, d.Run.Path = v.Number, v.Commit, v.Source, "pipeline.yml"
	d.Run.Repository.Private = r.Options["visibility"] != "public"
	d.Route = "pipeline"
	return d, nil
}
func (b *Buildkite) Register(_ context.Context, r Repository, d Demand, name string) (Agent, error) {
	token, err := b.token(Repository{TokenFile: r.Options["agent_token_file"]})
	if err != nil {
		return Agent{}, errors.New("Buildkite agent token unavailable")
	}
	return Agent{OneJob: true, Ref: d.Job.Name, Argv: []string{"./buildkite-agent", "start", "--acquire-job", d.Job.Name, "--name", name,
		"--tags", "queue=" + r.Label, "--build-path", "/var/tmp/lamp-ci-runner/builds", "--disconnect-after-job"},
		Secret: map[string]string{"env:BUILDKITE_AGENT_TOKEN": token}}, nil
}
func (b *Buildkite) GetJob(ctx context.Context, r Repository, id int64) (Job, error) {
	v, err := b.build(ctx, r, id)
	if err != nil {
		return Job{}, err
	}
	i, err := b.queueJob(r, v)
	if err != nil {
		return Job{}, err
	}
	out := Job{ID: id, RunID: id, Name: v.Jobs[i].ID}
	switch s := v.Jobs[i].State; s {
	case "pending", "waiting", "scheduled", "assigned", "accepted", "limited", "limiting", "blocked":
		out.Status = "queued"
	case "running", "canceling":
		out.Status = "in_progress"
	default:
		out.Status, out.Conclusion = "completed", conclusion(map[string]string{"passed": "success"}[s]+s)
		if s == "passed" {
			out.Conclusion = "success"
		}
	}
	return out, nil
}
func (b *Buildkite) Cancel(ctx context.Context, r Repository, run, _ int64) error {
	return b.api(ctx, r, "PUT", fmt.Sprintf("/pipelines/%s/builds/%d/cancel", url.PathEscape(r.Project), run), nil, nil)
}
func (b *Buildkite) DeregisterJob(ctx context.Context, r Repository, name string, _ int64) error {
	var agents []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := b.api(ctx, r, "GET", "/agents?name="+url.QueryEscape(name), nil, &agents); err != nil {
		return err
	}
	for _, a := range agents {
		if a.Name == name {
			if err := deregisterOK(b.api(ctx, r, "PUT", "/agents/"+url.PathEscape(a.ID)+"/stop", map[string]bool{"force": true}, nil)); err != nil {
				return err
			}
		}
	}
	return nil
}
func (b *Buildkite) Outputs(ctx context.Context, r Repository, run, _ int64) ([]Output, error) {
	v, err := b.build(ctx, r, run)
	if err != nil {
		return nil, err
	}
	i, err := b.queueJob(r, v)
	if err != nil {
		return nil, err
	}
	base := fmt.Sprintf("/v2/organizations/%s/pipelines/%s/builds/%d/jobs/%s", url.PathEscape(r.Options["organization"]), url.PathEscape(r.Project), run, v.Jobs[i].ID)
	out := []Output{{Name: "buildkite-job.log", Path: base + "/log.txt"}}
	var arts []struct {
		ID       string `json:"id"`
		Filename string `json:"filename"`
		Size     uint64 `json:"file_size"`
	}
	if b.api(ctx, r, "GET", fmt.Sprintf("/pipelines/%s/builds/%d/jobs/%s/artifacts", url.PathEscape(r.Project), run, v.Jobs[i].ID), nil, &arts) == nil {
		for _, a := range arts {
			out = append(out, Output{Name: a.Filename, Path: base + "/artifacts/" + a.ID + "/download", Size: a.Size})
		}
	}
	return out, nil
}
func (b *Buildkite) Download(ctx context.Context, r Repository, path string, limit int64) ([]byte, error) {
	return b.download(ctx, r, path, limit, bearer)
}

// --------------------------------------------------------------- Jenkins --

// Jenkins: Lamp creates an exclusive one-executor inbound node carrying the
// enrolled label, the fresh VM connects with that node's own secret
// (`agent.jar -webSocket`), and the node is deleted when the build ends.
// Lamp job IDs are Jenkins queue item IDs.
type Jenkins struct {
	hostClient
	Repositories []Repository
}

func (j *Jenkins) Plugin() string             { return "pg_jenkins" }
func (j *Jenkins) System() string             { return "jenkins" }
func (j *Jenkins) Issuer(r Repository) string { return strings.TrimSuffix(r.BaseURL, "/") }
func (j *Jenkins) Repos() []Repository        { return j.Repositories }
func (j *Jenkins) jobPath(r Repository) string {
	parts := strings.Split(r.Project, "/")
	for i := range parts {
		parts[i] = "job/" + url.PathEscape(parts[i])
	}
	return "/" + strings.Join(parts, "/")
}
func (j *Jenkins) crumbAuth(ctx context.Context, r Repository) (func(*http.Request, string), error) {
	var c struct {
		Field string `json:"crumbRequestField"`
		Crumb string `json:"crumb"`
	}
	err := j.call(ctx, r, "GET", "/crumbIssuer/api/json", nil, &c, basic("lamp"))
	if err != nil && !strings.Contains(err.Error(), "HTTP 404") {
		return nil, err
	}
	return func(req *http.Request, token string) {
		basic("lamp")(req, token)
		if c.Field != "" {
			req.Header.Set(c.Field, c.Crumb)
		}
	}, nil
}
func (j *Jenkins) post(ctx context.Context, r Repository, path string, form url.Values) error {
	auth, err := j.crumbAuth(ctx, r)
	if err != nil {
		return err
	}
	if form == nil {
		form = url.Values{}
	}
	err = j.call(ctx, r, "POST", path, form, nil, auth)
	// Jenkins answers successful form posts with a redirect to the new page.
	if err != nil && (strings.Contains(err.Error(), "HTTP 302") || strings.Contains(err.Error(), "HTTP 303")) {
		return nil
	}
	return err
}

type jenkinsItem struct {
	ID         int64  `json:"id"`
	Why        string `json:"why"`
	Cancelled  bool   `json:"cancelled"`
	Buildable  bool   `json:"buildable"`
	Task       struct{ Name, URL string }
	Executable *struct {
		Number int64 `json:"number"`
	} `json:"executable"`
	Actions []struct {
		Causes []struct {
			Class string `json:"_class"`
		} `json:"causes"`
	} `json:"actions"`
}

func (j *Jenkins) Queued(ctx context.Context, r Repository) ([]Job, error) {
	var q struct{ Items []jenkinsItem }
	if err := j.call(ctx, r, "GET", "/queue/api/json", nil, &q, basic("lamp")); err != nil {
		return nil, err
	}
	var out []Job
	for _, it := range q.Items {
		if strings.TrimSuffix(it.Task.URL, "/") == strings.TrimSuffix(r.BaseURL, "/")+j.jobPath(r) && it.Executable == nil && !it.Cancelled {
			out = append(out, Job{ID: it.ID, Status: "queued", Name: it.Task.Name})
		}
	}
	return out, nil
}
func (j *Jenkins) item(ctx context.Context, r Repository, id int64) (jenkinsItem, error) {
	var it jenkinsItem
	err := j.call(ctx, r, "GET", fmt.Sprintf("/queue/item/%d/api/json", id), nil, &it, basic("lamp"))
	return it, err
}
func (j *Jenkins) Verify(ctx context.Context, r Repository, id int64) (Demand, error) {
	d := Demand{Repository: r}
	it, err := j.item(ctx, r, id)
	if err != nil {
		return d, err
	}
	if it.ID != id || it.Cancelled || it.Executable != nil || strings.TrimSuffix(it.Task.URL, "/") != strings.TrimSuffix(r.BaseURL, "/")+j.jobPath(r) {
		return d, errors.New("queue item is not waiting for the enrolled job")
	}
	untrusted := strings.Contains(r.Project, "/PR-") || strings.Contains(r.Project, "/MR-")
	for _, a := range it.Actions {
		for _, c := range a.Causes {
			// SCM-change and pull-request causes from multibranch sources may run
			// a stranger's code; an upstream job may itself be a PR build and is
			// not traced here. User and timer causes are trusted.
			if strings.Contains(c.Class, "PullRequest") || strings.Contains(c.Class, "ChangeRequest") || strings.Contains(c.Class, "RemoteCause") || strings.Contains(c.Class, "UpstreamCause") {
				untrusted = true
			}
		}
	}
	d.Public = publicFor(r, untrusted)
	d.Job = Job{ID: id, RunID: id, Status: "queued", Name: it.Task.Name}
	d.Run.ID, d.Run.Event, d.Run.Path = id, "queue", r.Project
	d.Run.Repository.Private = r.Options["visibility"] != "public"
	d.Route = "build"
	return d, nil
}
func (j *Jenkins) Register(ctx context.Context, r Repository, d Demand, name string) (Agent, error) {
	node, _ := json.Marshal(map[string]any{"name": name, "nodeDescription": "Lamp one-build agent", "numExecutors": "1", "remoteFS": "/var/tmp/lamp-ci-runner/work",
		"labelString": r.Label, "mode": "EXCLUSIVE", "retentionStrategy": map[string]any{"stapler-class": "hudson.slaves.RetentionStrategy$Always"},
		"nodeProperties": map[string]any{"stapler-class-bag": "true"}, "launcher": map[string]any{"stapler-class": "hudson.slaves.JNLPLauncher", "webSocket": true},
		"type": "hudson.slaves.DumbSlave"})
	if err := j.post(ctx, r, "/computer/doCreateItem", url.Values{"name": {name}, "type": {"hudson.slaves.DumbSlave"}, "json": {string(node)}}); err != nil {
		return Agent{}, err
	}
	var jnlp []byte
	if err := j.call(ctx, r, "GET", "/computer/"+url.PathEscape(name)+"/jenkins-agent.jnlp", nil, &jnlp, basic("lamp")); err != nil {
		return Agent{}, err
	}
	m := regexp.MustCompile(`<application-desc>\s*<argument>([0-9a-f]{64})</argument>`).FindSubmatch(jnlp)
	if m == nil {
		return Agent{}, errors.New("Jenkins returned no inbound agent secret")
	}
	return Agent{Ref: name, Argv: []string{"java", "-jar", "agent.jar", "-url", strings.TrimSuffix(r.BaseURL, "/") + "/", "-secret", "$SECRET:NODE", "-name", name, "-webSocket", "-workDir", "/var/tmp/lamp-ci-runner/work"},
		Secret: map[string]string{"NODE": string(m[1])}}, nil
}
func (j *Jenkins) build(ctx context.Context, r Repository, id int64) (it jenkinsItem, b struct {
	Building bool   `json:"building"`
	Result   string `json:"result"`
	BuiltOn  string `json:"builtOn"`
}, err error) {
	if it, err = j.item(ctx, r, id); err != nil || it.Executable == nil {
		return
	}
	err = j.call(ctx, r, "GET", fmt.Sprintf("%s/%d/api/json", j.jobPath(r), it.Executable.Number), nil, &b, basic("lamp"))
	return
}
func (j *Jenkins) GetJob(ctx context.Context, r Repository, id int64) (Job, error) {
	it, b, err := j.build(ctx, r, id)
	out := Job{ID: id, RunID: id, Status: "queued"}
	switch {
	case err != nil:
	case it.Cancelled:
		out.Status, out.Conclusion = "completed", "cancelled"
	case it.Executable != nil && b.Building:
		out.Status = "in_progress"
	case it.Executable != nil && b.Result != "":
		out.Status, out.Conclusion = "completed", conclusion(b.Result)
	}
	return out, err
}
func (j *Jenkins) Cancel(ctx context.Context, r Repository, _, id int64) error {
	it, err := j.item(ctx, r, id)
	if err != nil {
		return err
	}
	if it.Executable == nil {
		return j.post(ctx, r, fmt.Sprintf("/queue/cancelItem?id=%d", id), nil)
	}
	return j.post(ctx, r, fmt.Sprintf("%s/%d/stop", j.jobPath(r), it.Executable.Number), nil)
}
func (j *Jenkins) DeregisterJob(ctx context.Context, r Repository, name string, _ int64) error {
	return deregisterOK(j.post(ctx, r, "/computer/"+url.PathEscape(name)+"/doDelete", nil))
}
func (j *Jenkins) Outputs(ctx context.Context, r Repository, _, id int64) ([]Output, error) {
	it, err := j.item(ctx, r, id)
	if err != nil || it.Executable == nil {
		return nil, err
	}
	base := fmt.Sprintf("%s/%d", j.jobPath(r), it.Executable.Number)
	var b struct {
		Artifacts []struct{ RelativePath string } `json:"artifacts"`
	}
	out := []Output{{Name: "jenkins-console.log", Path: base + "/consoleText"}}
	if j.call(ctx, r, "GET", base+"/api/json?tree=artifacts[relativePath]", nil, &b, basic("lamp")) == nil && len(b.Artifacts) > 0 {
		out = append(out, Output{Name: "archive.zip", Path: base + "/artifact/*zip*/archive.zip"})
	}
	return out, nil
}
func (j *Jenkins) Download(ctx context.Context, r Repository, path string, limit int64) ([]byte, error) {
	return j.download(ctx, r, path, limit, basic("lamp"))
}

// -------------------------------------------------------------- CircleCI --

// CircleCI machine runner 3 in `single-task` mode claims one task and exits.
// Lamp mints a resource-class runner token per job and deletes it afterwards.
// Lamp job IDs are CircleCI job numbers.
type CircleCI struct {
	hostClient
	Repositories []Repository
}

func (c *CircleCI) Plugin() string           { return "pg_circleci" }
func (c *CircleCI) System() string           { return "circleci" }
func (c *CircleCI) Issuer(Repository) string { return "https://circleci.com" }
func (c *CircleCI) Repos() []Repository      { return c.Repositories }
func (c *CircleCI) api(ctx context.Context, r Repository, method, path string, in, out any) error {
	return c.call(ctx, r, method, path, in, out, headerAuth("Circle-Token", ""))
}
func (c *CircleCI) runner(r Repository) Repository {
	r.BaseURL = r.Options["runner_url"]
	if r.BaseURL == "" {
		r.BaseURL = "https://runner.circleci.com"
	}
	return r
}

type circleJob struct {
	Number   int64  `json:"job_number"`
	Status   string `json:"status"`
	Name     string `json:"name"`
	Executor struct {
		ResourceClass string `json:"resource_class"`
	} `json:"executor"`
	Pipeline struct {
		ID string `json:"id"`
	} `json:"pipeline"`
	LatestWorkflow struct {
		ID string `json:"id"`
	} `json:"latest_workflow"`
}

func (c *CircleCI) Queued(ctx context.Context, r Repository) ([]Job, error) {
	var ps struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := c.api(ctx, r, "GET", "/api/v2/project/"+r.Project+"/pipeline", nil, &ps); err != nil {
		return nil, err
	}
	var out []Job
	for _, p := range ps.Items[:min(len(ps.Items), 10)] {
		var ws struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
		}
		if c.api(ctx, r, "GET", "/api/v2/pipeline/"+p.ID+"/workflow", nil, &ws) != nil {
			continue
		}
		for _, w := range ws.Items {
			var js struct {
				Items []struct {
					Number int64  `json:"job_number"`
					Status string `json:"status"`
					Name   string `json:"name"`
				} `json:"items"`
			}
			if c.api(ctx, r, "GET", "/api/v2/workflow/"+w.ID+"/job", nil, &js) != nil {
				continue
			}
			for _, j := range js.Items {
				if j.Number != 0 && (j.Status == "queued" || j.Status == "not_running") {
					out = append(out, Job{ID: j.Number, Status: "queued", Name: j.Name})
				}
			}
		}
	}
	return out, nil
}
func (c *CircleCI) get(ctx context.Context, r Repository, n int64) (circleJob, error) {
	var j circleJob
	err := c.api(ctx, r, "GET", fmt.Sprintf("/api/v2/project/%s/job/%d", r.Project, n), nil, &j)
	return j, err
}
func (c *CircleCI) Verify(ctx context.Context, r Repository, id int64) (Demand, error) {
	d := Demand{Repository: r}
	j, err := c.get(ctx, r, id)
	if err != nil {
		return d, err
	}
	if j.Number != id || (j.Status != "queued" && j.Status != "not_running") || j.Executor.ResourceClass != r.Label {
		return d, errors.New("job is not queued for the enrolled resource class")
	}
	var p struct {
		Number int64 `json:"number"`
		Vcs    struct {
			Revision string `json:"revision"`
			Origin   string `json:"origin_repository_url"`
			Target   string `json:"target_repository_url"`
		} `json:"vcs"`
		Trigger struct {
			Type string `json:"type"`
		} `json:"trigger"`
	}
	if err = c.api(ctx, r, "GET", "/api/v2/pipeline/"+j.Pipeline.ID, nil, &p); err != nil {
		return d, err
	}
	// Different origin and target repositories mean a fork pull request.
	d.Public = publicFor(r, p.Vcs.Origin != p.Vcs.Target)
	d.Job = Job{ID: id, RunID: p.Number, Status: "queued", Name: j.Name}
	d.Run.ID, d.Run.HeadSHA, d.Run.Event, d.Run.Path = p.Number, p.Vcs.Revision, p.Trigger.Type, ".circleci/config.yml"
	d.Run.Repository.Private = r.Options["visibility"] != "public"
	d.Route = "workflow"
	return d, nil
}
func (c *CircleCI) Register(ctx context.Context, r Repository, _ Demand, name string) (Agent, error) {
	var out struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := c.api(ctx, c.runner(r), "POST", "/api/v3/runner/token", map[string]string{"resource_class": r.Label, "nickname": name}, &out); err != nil {
		return Agent{}, err
	}
	if out.Token == "" {
		return Agent{Ref: out.ID}, errors.New("CircleCI returned no runner token")
	}
	return Agent{OneJob: true, Ref: out.ID, Argv: []string{"./circleci-runner", "machine", "--runner.name", name, "--runner.mode", "single-task", "--runner.working_directory", "/var/tmp/lamp-ci-runner/work/%s", "--runner.cleanup_working_directory"},
		Secret: map[string]string{"env:CIRCLECI_RUNNER_API_AUTH_TOKEN": out.Token}}, nil
}
func (c *CircleCI) GetJob(ctx context.Context, r Repository, id int64) (Job, error) {
	j, err := c.get(ctx, r, id)
	out := Job{ID: id, Name: j.Name}
	switch j.Status {
	case "queued", "not_running", "blocked", "on_hold":
		out.Status = "queued"
	case "running":
		out.Status = "in_progress"
	default:
		out.Status, out.Conclusion = "completed", conclusion(j.Status)
	}
	return out, err
}
func (c *CircleCI) Cancel(ctx context.Context, r Repository, _, id int64) error {
	return c.api(ctx, r, "POST", fmt.Sprintf("/api/v2/project/%s/job/%d/cancel", r.Project, id), nil, nil)
}
func (c *CircleCI) DeregisterJob(ctx context.Context, r Repository, name string, _ int64) error {
	var out struct {
		Items []struct {
			ID       string `json:"id"`
			Nickname string `json:"nickname"`
		} `json:"items"`
	}
	if err := c.api(ctx, c.runner(r), "GET", "/api/v3/runner/token?resource-class="+url.QueryEscape(r.Label), nil, &out); err != nil {
		return err
	}
	for _, t := range out.Items {
		if t.Nickname == name {
			if err := deregisterOK(c.api(ctx, c.runner(r), "DELETE", "/api/v3/runner/token/"+url.PathEscape(t.ID), nil, nil)); err != nil {
				return err
			}
		}
	}
	return nil
}
func (c *CircleCI) Outputs(ctx context.Context, r Repository, _, id int64) ([]Output, error) {
	var a struct {
		Items []struct {
			Path string `json:"path"`
			URL  string `json:"url"`
		} `json:"items"`
	}
	out := []Output{{Name: "circleci-job.json", Path: fmt.Sprintf("/api/v1.1/project/%s/%d", r.Project, id)}}
	err := c.api(ctx, r, "GET", fmt.Sprintf("/api/v2/project/%s/%d/artifacts", r.Project, id), nil, &a)
	for _, v := range a.Items {
		if u, e := url.Parse(v.URL); e == nil && u.Scheme == "https" {
			out = append(out, Output{Name: v.Path, Path: u.RequestURI()})
		}
	}
	return out, err
}
func (c *CircleCI) Download(ctx context.Context, r Repository, path string, limit int64) ([]byte, error) {
	if !strings.HasPrefix(path, "/api/") {
		r.BaseURL = "https://output.circle-artifacts.com"
	}
	return c.download(ctx, r, path, limit, headerAuth("Circle-Token", ""))
}

// ------------------------------------------------------- Azure Pipelines --

// Azure Pipelines: the fresh VM configures a uniquely named agent in the
// enrolled pool and runs `./run.sh --once`, the agent's native single-job mode.
// Lamp removes the agent registration afterwards. Lamp job IDs are build IDs.
// The registration credential must be a PAT scoped to Agent Pools only.
type Azure struct {
	hostClient
	Repositories []Repository
}

func (a *Azure) Plugin() string             { return "pg_azure_pipelines" }
func (a *Azure) System() string             { return "azure-pipelines" }
func (a *Azure) Issuer(r Repository) string { return strings.TrimSuffix(r.BaseURL, "/") }
func (a *Azure) Repos() []Repository        { return a.Repositories }
func (a *Azure) api(ctx context.Context, r Repository, method, path string, in, out any) error {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return a.call(ctx, r, method, path+sep+"api-version=7.1", in, out, basic(""))
}

type azureBuild struct {
	ID            int64  `json:"id"`
	Status        string `json:"status"`
	Result        string `json:"result"`
	Reason        string `json:"reason"`
	SourceVersion string `json:"sourceVersion"`
	Queue         struct {
		Pool struct{ Name string } `json:"pool"`
	} `json:"queue"`
	Definition struct {
		ID int64 `json:"id"`
	} `json:"definition"`
	TriggerInfo map[string]string `json:"triggerInfo"`
}

func (a *Azure) proj(r Repository) string { return "/" + url.PathEscape(r.Project) }
func (a *Azure) Queued(ctx context.Context, r Repository) ([]Job, error) {
	var out struct{ Value []azureBuild }
	if err := a.api(ctx, r, "GET", a.proj(r)+"/_apis/build/builds?statusFilter=notStarted", nil, &out); err != nil {
		return nil, err
	}
	var jobs []Job
	for _, b := range out.Value {
		if b.Queue.Pool.Name == r.Label {
			jobs = append(jobs, Job{ID: b.ID, RunID: b.ID, Status: "queued"})
		}
	}
	return jobs, nil
}
func (a *Azure) get(ctx context.Context, r Repository, id int64) (azureBuild, error) {
	var b azureBuild
	err := a.api(ctx, r, "GET", fmt.Sprintf("%s/_apis/build/builds/%d", a.proj(r), id), nil, &b)
	return b, err
}
func (a *Azure) Verify(ctx context.Context, r Repository, id int64) (Demand, error) {
	d := Demand{Repository: r}
	b, err := a.get(ctx, r, id)
	if err != nil {
		return d, err
	}
	if b.ID != id || b.Status != "notStarted" || b.Queue.Pool.Name != r.Label {
		return d, errors.New("build is not waiting for the enrolled pool")
	}
	fork := b.TriggerInfo["pr.isFork"] == "True" || b.TriggerInfo["pr.isFork"] == "true"
	d.Public = publicFor(r, b.Reason == "pullRequest" && (fork || r.Options["trust_pull_requests"] != "true"))
	d.Job = Job{ID: id, RunID: id, Status: "queued"}
	d.Run.ID, d.Run.HeadSHA, d.Run.Event, d.Run.Path = id, b.SourceVersion, b.Reason, "azure-pipelines.yml"
	d.Run.Repository.Private = r.Options["visibility"] != "public"
	d.Route = "pipeline"
	return d, nil
}
func (a *Azure) Register(_ context.Context, r Repository, _ Demand, name string) (Agent, error) {
	pat, err := a.token(Repository{TokenFile: r.Options["agent_token_file"]})
	if err != nil {
		return Agent{}, errors.New("Azure agent-pool credential unavailable")
	}
	script := `./config.sh --unattended --url "$1" --auth pat --token "$2" --pool "$3" --agent "$4" --work _work --replace --acceptTeeEula && exec ./run.sh --once`
	return Agent{OneJob: true, Ref: name, Argv: []string{"bash", "-c", script, "lamp", strings.TrimSuffix(r.BaseURL, "/"), "$SECRET:PAT", r.Label, name},
		Secret: map[string]string{"PAT": pat}, Env: map[string]string{"AGENT_ALLOW_RUNASROOT": "1"}}, nil
}
func (a *Azure) GetJob(ctx context.Context, r Repository, id int64) (Job, error) {
	b, err := a.get(ctx, r, id)
	out := Job{ID: id, RunID: id}
	switch b.Status {
	case "notStarted", "postponed", "none":
		out.Status = "queued"
	case "inProgress", "cancelling":
		out.Status = "in_progress"
	default:
		out.Status, out.Conclusion = "completed", conclusion(b.Result)
	}
	return out, err
}
func (a *Azure) Cancel(ctx context.Context, r Repository, run, _ int64) error {
	return a.call(ctx, r, "PATCH", fmt.Sprintf("%s/_apis/build/builds/%d?api-version=7.1", a.proj(r), run), map[string]string{"status": "cancelling"}, nil, basic(""))
}
func (a *Azure) DeregisterJob(ctx context.Context, r Repository, name string, _ int64) error {
	var pools struct {
		Value []struct {
			ID int64 `json:"id"`
		}
	}
	if err := a.api(ctx, r, "GET", "/_apis/distributedtask/pools?poolName="+url.QueryEscape(r.Label), nil, &pools); err != nil {
		return err
	}
	for _, p := range pools.Value {
		var agents struct {
			Value []struct {
				ID int64 `json:"id"`
			}
		}
		if err := a.api(ctx, r, "GET", fmt.Sprintf("/_apis/distributedtask/pools/%d/agents?agentName=%s", p.ID, url.QueryEscape(name)), nil, &agents); err != nil {
			return err
		}
		for _, v := range agents.Value {
			if err := deregisterOK(a.api(ctx, r, "DELETE", fmt.Sprintf("/_apis/distributedtask/pools/%d/agents/%d", p.ID, v.ID), nil, nil)); err != nil {
				return err
			}
		}
	}
	return nil
}
func (a *Azure) Outputs(ctx context.Context, r Repository, run, _ int64) ([]Output, error) {
	var logs struct {
		Value []struct {
			ID int64 `json:"id"`
		}
	}
	err := a.api(ctx, r, "GET", fmt.Sprintf("%s/_apis/build/builds/%d/logs", a.proj(r), run), nil, &logs)
	var out []Output
	for _, l := range logs.Value {
		out = append(out, Output{Name: fmt.Sprintf("azure-log-%d.txt", l.ID), Path: fmt.Sprintf("%s/_apis/build/builds/%d/logs/%d?api-version=7.1", a.proj(r), run, l.ID)})
	}
	var arts struct {
		Value []struct {
			Name     string `json:"name"`
			Resource struct {
				DownloadURL string `json:"downloadUrl"`
			} `json:"resource"`
		}
	}
	if a.api(ctx, r, "GET", fmt.Sprintf("%s/_apis/build/builds/%d/artifacts", a.proj(r), run), nil, &arts) == nil {
		base := strings.TrimSuffix(r.BaseURL, "/")
		for _, v := range arts.Value {
			if strings.HasPrefix(v.Resource.DownloadURL, base+"/") {
				out = append(out, Output{Name: v.Name + ".zip", Path: strings.TrimPrefix(v.Resource.DownloadURL, base)})
			}
		}
	}
	return out, err
}
func (a *Azure) Download(ctx context.Context, r Repository, path string, limit int64) ([]byte, error) {
	return a.download(ctx, r, path, limit, basic(""))
}

// ------------------------------------------------------------- Bitbucket --

// Bitbucket Pipelines: Lamp creates a repository runner with a per-job label
// set, the fresh VM runs Atlassian's Linux shell runner with that runner's own
// OAuth client, and Lamp deletes the runner when the step ends (the runner has
// no native one-step exit). Lamp job IDs are pipeline build numbers.
type Bitbucket struct {
	hostClient
	Repositories []Repository
}

func (b *Bitbucket) Plugin() string           { return "pg_bitbucket" }
func (b *Bitbucket) System() string           { return "bitbucket" }
func (b *Bitbucket) Issuer(Repository) string { return "https://bitbucket.org" }
func (b *Bitbucket) Repos() []Repository      { return b.Repositories }
func (b *Bitbucket) api(ctx context.Context, r Repository, method, path string, in, out any) error {
	return b.call(ctx, r, method, path, in, out, basic(""))
}
func (b *Bitbucket) repo(r Repository) string { return "/repositories/" + r.Project }

type bbPipeline struct {
	UUID        string `json:"uuid"`
	BuildNumber int64  `json:"build_number"`
	State       struct {
		Name   string `json:"name"`
		Result *struct {
			Name string `json:"name"`
		} `json:"result"`
		Stage *struct {
			Name string `json:"name"`
		} `json:"stage"`
	} `json:"state"`
	Target struct {
		Type   string `json:"type"`
		Commit struct {
			Hash string `json:"hash"`
		} `json:"commit"`
		Source      string `json:"source"`
		Destination string `json:"destination"`
	} `json:"target"`
	Trigger struct {
		Name string `json:"name"`
	} `json:"trigger"`
}

func (b *Bitbucket) get(ctx context.Context, r Repository, n int64) (bbPipeline, error) {
	var p bbPipeline
	err := b.api(ctx, r, "GET", fmt.Sprintf("/2.0%s/pipelines/%d", b.repo(r), n), nil, &p)
	return p, err
}
func (b *Bitbucket) Queued(ctx context.Context, r Repository) ([]Job, error) {
	var out struct{ Values []bbPipeline }
	if err := b.api(ctx, r, "GET", "/2.0"+b.repo(r)+"/pipelines?sort=-created_on&pagelen=50", nil, &out); err != nil {
		return nil, err
	}
	var jobs []Job
	for _, p := range out.Values {
		if p.State.Name == "PENDING" || p.State.Name == "IN_PROGRESS" && p.State.Stage != nil && p.State.Stage.Name == "PENDING" {
			jobs = append(jobs, Job{ID: p.BuildNumber, RunID: p.BuildNumber, Status: "queued"})
		}
	}
	return jobs, nil
}
func (b *Bitbucket) Verify(ctx context.Context, r Repository, id int64) (Demand, error) {
	d := Demand{Repository: r}
	p, err := b.get(ctx, r, id)
	if err != nil {
		return d, err
	}
	if p.BuildNumber != id || p.State.Name == "COMPLETED" {
		return d, errors.New("pipeline is no longer waiting")
	}
	d.Public = publicFor(r, p.Target.Type == "pipeline_pullrequest_target")
	d.Job = Job{ID: id, RunID: id, Status: "queued", Name: p.UUID}
	d.Run.ID, d.Run.HeadSHA, d.Run.Event, d.Run.Path = id, p.Target.Commit.Hash, p.Trigger.Name, "bitbucket-pipelines.yml"
	d.Run.Repository.Private = r.Options["visibility"] != "public"
	d.Route = "step"
	return d, nil
}
func (b *Bitbucket) Register(ctx context.Context, r Repository, _ Demand, name string) (Agent, error) {
	var out struct {
		UUID  string `json:"uuid"`
		OAuth struct {
			ID       string `json:"id"`
			Secret   string `json:"secret"`
			Endpoint string `json:"token_endpoint"`
			Audience string `json:"audience"`
		} `json:"oauth_client"`
	}
	if err := b.api(ctx, r, "POST", "/internal"+b.repo(r)+"/pipelines-config/runners", map[string]any{"name": name, "labels": []string{"self.hosted", "linux.shell", r.Label}}, &out); err != nil {
		return Agent{}, err
	}
	if out.UUID == "" || out.OAuth.Secret == "" {
		return Agent{Ref: out.UUID}, errors.New("Bitbucket returned incomplete runner authority")
	}
	env := map[string]string{"ACCOUNT_UUID": r.Options["workspace_uuid"], "REPOSITORY_UUID": r.Options["repository_uuid"], "RUNNER_UUID": out.UUID,
		"OAUTH_CLIENT_ID": out.OAuth.ID, "RUNTIME_PREREQUISITES_ENABLED": "true", "WORKING_DIRECTORY": "/var/tmp/lamp-ci-runner/temp"}
	return Agent{Ref: out.UUID, Argv: []string{"bash", "./start.sh"}, Env: env, Secret: map[string]string{"env:OAUTH_CLIENT_SECRET": out.OAuth.Secret}}, nil
}
func (b *Bitbucket) GetJob(ctx context.Context, r Repository, id int64) (Job, error) {
	p, err := b.get(ctx, r, id)
	out := Job{ID: id, RunID: id, Name: p.UUID}
	switch p.State.Name {
	case "PENDING":
		out.Status = "queued"
	case "IN_PROGRESS":
		out.Status = "in_progress"
	default:
		res := "failure"
		if p.State.Result != nil {
			res = map[string]string{"SUCCESSFUL": "success", "STOPPED": "cancelled"}[p.State.Result.Name]
		}
		out.Status, out.Conclusion = "completed", conclusion(res)
	}
	return out, err
}
func (b *Bitbucket) Cancel(ctx context.Context, r Repository, run, _ int64) error {
	return b.api(ctx, r, "POST", fmt.Sprintf("/2.0%s/pipelines/%d/stopPipeline", b.repo(r), run), nil, nil)
}
func (b *Bitbucket) DeregisterJob(ctx context.Context, r Repository, name string, _ int64) error {
	var out struct {
		Values []struct {
			UUID string `json:"uuid"`
			Name string `json:"name"`
		}
	}
	if err := b.api(ctx, r, "GET", "/internal"+b.repo(r)+"/pipelines-config/runners", nil, &out); err != nil {
		return err
	}
	for _, v := range out.Values {
		if v.Name == name {
			if err := deregisterOK(b.api(ctx, r, "DELETE", "/internal"+b.repo(r)+"/pipelines-config/runners/"+url.PathEscape(v.UUID), nil, nil)); err != nil {
				return err
			}
		}
	}
	return nil
}
func (b *Bitbucket) Outputs(ctx context.Context, r Repository, run, _ int64) ([]Output, error) {
	var steps struct {
		Values []struct {
			UUID string `json:"uuid"`
			Name string `json:"name"`
		}
	}
	err := b.api(ctx, r, "GET", fmt.Sprintf("/2.0%s/pipelines/%d/steps", b.repo(r), run), nil, &steps)
	var out []Output
	for i, s := range steps.Values {
		out = append(out, Output{Name: fmt.Sprintf("bitbucket-step-%d.log", i+1), Path: fmt.Sprintf("/2.0%s/pipelines/%d/steps/%s/log", b.repo(r), run, url.PathEscape(s.UUID))})
	}
	return out, err
}
func (b *Bitbucket) Download(ctx context.Context, r Repository, path string, limit int64) ([]byte, error) {
	return b.download(ctx, r, path, limit, basic(""))
}

// ------------------------------------------------------------ Woodpecker --

// Woodpecker: Lamp creates a dedicated agent with its own token and a per-job
// label, the fresh VM runs `woodpecker-agent` with the local backend and one
// workflow slot, and Lamp deletes the agent after the pipeline's final state.
// Lamp job IDs are pipeline numbers; Project is the numeric repository ID.
type Woodpecker struct {
	hostClient
	Repositories []Repository
}

func (w *Woodpecker) Plugin() string             { return "pg_woodpecker" }
func (w *Woodpecker) System() string             { return "woodpecker" }
func (w *Woodpecker) Issuer(r Repository) string { return strings.TrimSuffix(r.BaseURL, "/") }
func (w *Woodpecker) Repos() []Repository        { return w.Repositories }
func (w *Woodpecker) api(ctx context.Context, r Repository, method, path string, in, out any) error {
	return w.call(ctx, r, method, "/api"+path, in, out, bearer)
}

type wpPipeline struct {
	Number    int64  `json:"number"`
	Status    string `json:"status"`
	Event     string `json:"event"`
	Commit    string `json:"commit"`
	FromFork  bool   `json:"from_fork"`
	Workflows []struct {
		Children []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"children"`
	} `json:"workflows"`
}

func (w *Woodpecker) get(ctx context.Context, r Repository, n int64) (wpPipeline, error) {
	var p wpPipeline
	err := w.api(ctx, r, "GET", fmt.Sprintf("/repos/%s/pipelines/%d", r.Project, n), nil, &p)
	return p, err
}
func (w *Woodpecker) Queued(ctx context.Context, r Repository) ([]Job, error) {
	var ps []wpPipeline
	if err := w.api(ctx, r, "GET", "/repos/"+r.Project+"/pipelines?per_page=50", nil, &ps); err != nil {
		return nil, err
	}
	var out []Job
	for _, p := range ps {
		if p.Status == "pending" {
			out = append(out, Job{ID: p.Number, RunID: p.Number, Status: "queued"})
		}
	}
	return out, nil
}
func (w *Woodpecker) Verify(ctx context.Context, r Repository, id int64) (Demand, error) {
	d := Demand{Repository: r}
	p, err := w.get(ctx, r, id)
	if err != nil {
		return d, err
	}
	if p.Number != id || p.Status != "pending" {
		return d, errors.New("pipeline is no longer pending")
	}
	var repo struct {
		Visibility string `json:"visibility"`
	}
	if err = w.api(ctx, r, "GET", "/repos/"+r.Project, nil, &repo); err != nil {
		return d, err
	}
	d.Public = publicFor(r, p.FromFork || p.Event == "pull_request" || p.Event == "pull_request_closed" || repo.Visibility == "public")
	d.Job = Job{ID: id, RunID: id, Status: "queued"}
	d.Run.ID, d.Run.HeadSHA, d.Run.Event, d.Run.Path = id, p.Commit, p.Event, ".woodpecker"
	d.Run.Repository.Private = repo.Visibility != "public"
	d.Route = "pipeline"
	return d, nil
}
func (w *Woodpecker) Register(ctx context.Context, r Repository, _ Demand, name string) (Agent, error) {
	var a struct {
		ID    int64  `json:"id"`
		Token string `json:"token"`
	}
	if err := w.api(ctx, r, "POST", "/agents", map[string]any{"name": name, "no_schedule": false}, &a); err != nil {
		return Agent{}, err
	}
	if a.ID == 0 || a.Token == "" {
		return Agent{ID: a.ID}, errors.New("Woodpecker returned incomplete agent authority")
	}
	env := map[string]string{"WOODPECKER_SERVER": r.Options["grpc_address"], "WOODPECKER_BACKEND": "local", "WOODPECKER_MAX_WORKFLOWS": "1",
		"WOODPECKER_HOSTNAME": name, "WOODPECKER_AGENT_LABELS": "lamp=" + r.Label, "WOODPECKER_HEALTHCHECK": "false",
		"WOODPECKER_BACKEND_LOCAL_TEMP_DIR": "/var/tmp/lamp-ci-runner/work", "WOODPECKER_AGENT_CONFIG_FILE": "/var/tmp/lamp-ci-runner/agent.conf"}
	if r.Options["grpc_secure"] == "true" {
		env["WOODPECKER_GRPC_SECURE"] = "true"
	}
	return Agent{ID: a.ID, Argv: []string{"sh", "-c", "mkdir -p /var/tmp/lamp-ci-runner/work && exec ./woodpecker-agent"}, Env: env, Secret: map[string]string{"env:WOODPECKER_AGENT_SECRET": a.Token}}, nil
}
func (w *Woodpecker) GetJob(ctx context.Context, r Repository, id int64) (Job, error) {
	p, err := w.get(ctx, r, id)
	out := Job{ID: id, RunID: id}
	switch p.Status {
	case "pending", "blocked", "created":
		out.Status = "queued"
	case "running":
		out.Status = "in_progress"
	default:
		out.Status, out.Conclusion = "completed", conclusion(p.Status)
	}
	return out, err
}
func (w *Woodpecker) Cancel(ctx context.Context, r Repository, run, _ int64) error {
	return w.api(ctx, r, "POST", fmt.Sprintf("/repos/%s/pipelines/%d/cancel", r.Project, run), nil, nil)
}
func (w *Woodpecker) DeregisterJob(ctx context.Context, r Repository, name string, known int64) error {
	if known != 0 {
		if err := deregisterOK(w.api(ctx, r, "DELETE", fmt.Sprintf("/agents/%d", known), nil, nil)); err != nil {
			return err
		}
	}
	var agents []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if err := w.api(ctx, r, "GET", "/agents?per_page=100", nil, &agents); err != nil {
		return err
	}
	for _, a := range agents {
		if a.Name == name {
			if err := deregisterOK(w.api(ctx, r, "DELETE", fmt.Sprintf("/agents/%d", a.ID), nil, nil)); err != nil {
				return err
			}
		}
	}
	return nil
}
func (w *Woodpecker) Outputs(ctx context.Context, r Repository, run, _ int64) ([]Output, error) {
	p, err := w.get(ctx, r, run)
	var out []Output
	for _, wf := range p.Workflows {
		for _, s := range wf.Children {
			out = append(out, Output{Name: "woodpecker-" + s.Name + ".log", Path: fmt.Sprintf("/api/repos/%s/logs/%d/%d", r.Project, run, s.ID)})
		}
	}
	return out, err
}
func (w *Woodpecker) Download(ctx context.Context, r Repository, path string, limit int64) ([]byte, error) {
	raw, err := w.download(ctx, r, path, limit, bearer)
	if err != nil {
		return nil, err
	}
	// Log entries are JSON lines with base64 data; return the plain text log.
	var lines []struct {
		Data string `json:"data"`
	}
	if json.Unmarshal(raw, &lines) != nil {
		return raw, nil
	}
	var text []byte
	for _, l := range lines {
		b, e := base64.StdEncoding.DecodeString(l.Data)
		if e != nil {
			b = []byte(l.Data)
		}
		text = append(append(text, b...), '\n')
	}
	return text, nil
}

var _ = strconv.Itoa
