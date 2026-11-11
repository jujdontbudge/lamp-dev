package ci

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
)

// Gitea covers Forgejo and Gitea as two distinct, versioned contracts that
// share Gitea's REST shape:
//
//   - forgejo (Forgejo >= 15, runner >= 12): Lamp registers an ephemeral
//     repository runner through the API and the fresh VM runs
//     `forgejo-runner one-job --handle <job attempt>`, which requests exactly
//     that job attempt and exits.
//   - gitea (Gitea >= 1.24, act_runner >= 0.2.12): Lamp mints a repository
//     registration token, the fresh VM registers an `--ephemeral` runner with
//     a per-job label and runs `act_runner daemon --once`.
//
// Lamp job IDs are the forge's run job IDs; Name is "owner/repo".
type Gitea struct {
	hostClient
	Kind         string
	Repositories []Repository
}

func (g *Gitea) Plugin() string             { return "pg_" + g.Kind }
func (g *Gitea) System() string             { return g.Kind }
func (g *Gitea) Issuer(r Repository) string { return strings.TrimSuffix(r.BaseURL, "/") }
func (g *Gitea) Repos() []Repository        { return g.Repositories }
func (g *Gitea) api(ctx context.Context, r Repository, method, path string, in, out any) error {
	return g.call(ctx, r, method, "/api/v1/repos/"+r.Name+path, in, out, func(req *http.Request, t string) { req.Header.Set("Authorization", "token "+t) })
}

type giteaJob struct {
	ID      int64    `json:"id"`
	RunID   int64    `json:"run_id"`
	Name    string   `json:"name"`
	Status  string   `json:"status"`
	RunsOn  []string `json:"runs_on"`
	Labels  []string `json:"labels"`
	Handle  string   `json:"handle"`
	Attempt int64    `json:"attempt"`
	TaskID  int64    `json:"task_id"`
	Runner  int64    `json:"runner_id"`
	// Gitea reports conclusion separately from status.
	Conclusion string `json:"conclusion"`
}

func (j giteaJob) labels() []string {
	if len(j.RunsOn) > 0 {
		return j.RunsOn
	}
	return j.Labels
}

type giteaRun struct {
	ID         int64  `json:"id"`
	Event      string `json:"event"`
	Trigger    string `json:"trigger_event"`
	CommitSHA  string `json:"commit_sha"`
	HeadSHA    string `json:"head_sha"`
	Workflow   string `json:"workflow_id"`
	Path       string `json:"path"`
	Fork       bool   `json:"is_fork_pull_request"`
	Status     string `json:"status"`
	Repository struct {
		ID      int64 `json:"id"`
		Private bool  `json:"private"`
		Owner   struct {
			ID int64 `json:"id"`
		} `json:"owner"`
	} `json:"repository"`
}

func (g *Gitea) Queued(ctx context.Context, r Repository) ([]Job, error) {
	if g.Kind == "forgejo" {
		var jobs []giteaJob
		if err := g.api(ctx, r, "GET", "/actions/runners/jobs?labels="+r.Label, nil, &jobs); err != nil {
			return nil, err
		}
		var out []Job
		for _, j := range jobs {
			if j.Status == "waiting" && slices.Contains(j.labels(), r.Label) {
				out = append(out, Job{ID: j.ID, RunID: j.RunID, Status: "queued", Labels: j.labels(), Name: j.Name})
			}
		}
		return out, nil
	}
	var runs struct {
		Runs []giteaRun `json:"workflow_runs"`
	}
	if err := g.api(ctx, r, "GET", "/actions/runs?status=queued&limit=50", nil, &runs); err != nil {
		return nil, err
	}
	var out []Job
	for _, run := range runs.Runs {
		var jobs struct {
			Jobs []giteaJob `json:"jobs"`
		}
		if g.api(ctx, r, "GET", fmt.Sprintf("/actions/runs/%d/jobs", run.ID), nil, &jobs) != nil {
			continue
		}
		for _, j := range jobs.Jobs {
			if (j.Status == "queued" || j.Status == "waiting") && slices.Contains(j.labels(), r.Label) {
				out = append(out, Job{ID: j.ID, RunID: run.ID, Status: "queued", Labels: j.labels(), Name: j.Name})
			}
		}
	}
	return out, nil
}
func (g *Gitea) job(ctx context.Context, r Repository, id int64) (giteaJob, error) {
	var j giteaJob
	err := g.api(ctx, r, "GET", fmt.Sprintf("/actions/jobs/%d", id), nil, &j)
	return j, err
}
func (g *Gitea) Verify(ctx context.Context, r Repository, id int64) (Demand, error) {
	d := Demand{Repository: r}
	j, err := g.job(ctx, r, id)
	if err != nil {
		return d, err
	}
	if j.ID != id || (j.Status != "waiting" && j.Status != "queued") || !slices.Contains(j.labels(), r.Label) {
		return d, errors.New("job is not waiting for the enrolled runner label")
	}
	for _, l := range j.labels() {
		if l != r.Label {
			return d, errors.New("requested runner labels are unsupported by the enrolled template")
		}
	}
	var run giteaRun
	if err = g.api(ctx, r, "GET", fmt.Sprintf("/actions/runs/%d", j.RunID), nil, &run); err != nil {
		return d, err
	}
	if r.ID != 0 && run.Repository.ID != 0 && run.Repository.ID != r.ID {
		return d, errors.New("immutable forge identity mismatch")
	}
	event := run.Trigger
	if event == "" {
		event = run.Event
	}
	sha := run.CommitSHA
	if sha == "" {
		sha = run.HeadSHA
	}
	// pull_request_target and workflow_run carry base-branch secrets while a
	// stranger steers them; refuse them like GitHub's adapter does.
	if event == "pull_request_target" || event == "workflow_run" {
		return d, errors.New("privileged trigger cannot execute CI head code")
	}
	d.Public = publicFor(r, !run.Repository.Private || run.Fork || !TrustedEvent(event))
	path := run.Path
	if path == "" && run.Workflow != "" {
		dir := ".forgejo/workflows/"
		if g.Kind == "gitea" {
			dir = ".gitea/workflows/"
		}
		path = dir + run.Workflow
	}
	if path, _, _ = strings.Cut(path, "@"); strings.Contains(path, "..") {
		return d, errors.New("unsupported workflow source")
	}
	// Reviewed workflow digests are enforced exactly as for GitHub whenever
	// the owner pinned any: an unreviewed workflow cannot reach a runner.
	if len(r.WorkflowSHA256) > 0 {
		var file struct {
			Content string `json:"content"`
		}
		if err = g.api(ctx, r, "GET", "/contents/"+path+"?ref="+sha, nil, &file); err != nil {
			return d, err
		}
		raw, e := base64.StdEncoding.DecodeString(strings.ReplaceAll(file.Content, "\n", ""))
		sum := sha256.Sum256(raw)
		if e != nil || r.WorkflowSHA256[path] != hex.EncodeToString(sum[:]) {
			return d, errors.New("workflow has not been reviewed at this exact digest")
		}
		d.Workflow = raw
		if d.Public {
			if err = PublicWorkflow(raw); err != nil {
				return d, err
			}
		}
	}
	d.Job = Job{ID: j.ID, RunID: j.RunID, Status: "queued", Labels: j.labels(), Name: j.Handle, RunAttempt: int(j.Attempt)}
	d.Run.ID, d.Run.HeadSHA, d.Run.Event, d.Run.Path = j.RunID, sha, event, path
	d.Run.Repository.Private = run.Repository.Private
	d.RunnerLabel = r.Label
	d.Route = "workflow"
	return d, nil
}
func (g *Gitea) Register(ctx context.Context, r Repository, d Demand, name string) (Agent, error) {
	base := strings.TrimSuffix(r.BaseURL, "/")
	if g.Kind == "forgejo" {
		if d.Job.Name == "" {
			return Agent{}, errors.New("CI_PLUGIN_INCOMPATIBLE: Forgejo >= 15 job handles are required")
		}
		var out struct {
			ID    int64  `json:"id"`
			UUID  string `json:"uuid"`
			Token string `json:"token"`
		}
		if err := g.api(ctx, r, "POST", "/actions/runners", map[string]any{"name": name, "description": "Lamp one-job runner", "ephemeral": true}, &out); err != nil {
			return Agent{}, err
		}
		if out.ID == 0 || out.UUID == "" || out.Token == "" {
			return Agent{ID: out.ID}, errors.New("forge returned incomplete one-job authority")
		}
		script := `umask 077; printf %s "$1" > .token; exec ./forgejo-runner one-job --url "$2" --uuid "$3" --token-url file:/var/tmp/lamp-ci-runner/.token --handle "$4" --label "$5:host" --wait`
		return Agent{ID: out.ID, Ref: out.UUID, OneJob: true, Argv: []string{"sh", "-c", script, "lamp", "$SECRET:TOKEN", base, out.UUID, d.Job.Name, r.Label},
			Secret: map[string]string{"TOKEN": out.Token}}, nil
	}
	var tok struct {
		Token string `json:"token"`
	}
	if err := g.api(ctx, r, "POST", "/actions/runners/registration-token", nil, &tok); err != nil {
		return Agent{}, err
	}
	if tok.Token == "" {
		return Agent{}, errors.New("forge returned no registration token")
	}
	script := `./act_runner register --no-interactive --ephemeral --instance "$2" --token "$1" --name "$3" --labels "$4:host" >/dev/null && exec ./act_runner daemon --once`
	return Agent{Ref: name, OneJob: true, Argv: []string{"sh", "-c", script, "lamp", "$SECRET:TOKEN", base, name, r.Label}, Secret: map[string]string{"TOKEN": tok.Token}}, nil
}
func (g *Gitea) GetJob(ctx context.Context, r Repository, id int64) (Job, error) {
	j, err := g.job(ctx, r, id)
	out := Job{ID: j.ID, RunID: j.RunID, Labels: j.labels(), Name: j.Name}
	switch j.Status {
	case "waiting", "queued", "blocked", "unknown", "pending":
		out.Status = "queued"
	case "running", "in_progress":
		out.Status = "in_progress"
	case "completed":
		out.Status, out.Conclusion = "completed", conclusion(j.Conclusion)
	default:
		out.Status, out.Conclusion = "completed", conclusion(j.Status)
	}
	return out, err
}
func (g *Gitea) Cancel(ctx context.Context, r Repository, run, _ int64) error {
	return g.api(ctx, r, "POST", fmt.Sprintf("/actions/runs/%d/cancel", run), nil, nil)
}
func (g *Gitea) DeregisterJob(ctx context.Context, r Repository, name string, known int64) error {
	if known != 0 {
		if err := deregisterOK(g.api(ctx, r, "DELETE", fmt.Sprintf("/actions/runners/%d", known), nil, nil)); err != nil {
			return err
		}
	}
	var list struct {
		Runners []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"runners"`
	}
	var plain []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	err := g.api(ctx, r, "GET", "/actions/runners?limit=100", nil, &list)
	if err != nil && strings.Contains(err.Error(), "decode") {
		err = g.api(ctx, r, "GET", "/actions/runners?limit=100", nil, &plain)
	}
	if err != nil {
		// Forgejo returns a bare array; retry decoding as that shape.
		if e := g.api(ctx, r, "GET", "/actions/runners?limit=100", nil, &plain); e != nil {
			return err
		}
	}
	for _, v := range append(list.Runners, plain...) {
		if v.Name == name {
			if err := deregisterOK(g.api(ctx, r, "DELETE", fmt.Sprintf("/actions/runners/%d", v.ID), nil, nil)); err != nil {
				return err
			}
		}
	}
	return nil
}
func (g *Gitea) Outputs(ctx context.Context, r Repository, run, job int64) ([]Output, error) {
	prefix := "/api/v1/repos/" + r.Name
	out := []Output{{Name: g.Kind + "-job.log", Path: fmt.Sprintf("%s/actions/jobs/%d/logs", prefix, job)}}
	var a struct {
		Artifacts []struct {
			ID      int64  `json:"id"`
			Name    string `json:"name"`
			Size    uint64 `json:"size_in_bytes"`
			Expired bool   `json:"expired"`
		} `json:"artifacts"`
	}
	err := g.api(ctx, r, "GET", fmt.Sprintf("/actions/runs/%d/artifacts", run), nil, &a)
	for _, v := range a.Artifacts {
		if !v.Expired {
			out = append(out, Output{Name: v.Name + ".zip", Path: fmt.Sprintf("%s/actions/artifacts/%d/zip", prefix, v.ID), Size: v.Size})
		}
	}
	return out, err
}
func (g *Gitea) Download(ctx context.Context, r Repository, path string, limit int64) ([]byte, error) {
	return g.download(ctx, r, path, limit, func(req *http.Request, t string) { req.Header.Set("Authorization", "token "+t) })
}
