package ci

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Agent is a native one-job agent launch for a fresh sandbox. Secret values
// live only in Secret; Argv may reference them as "$SECRET:NAME". The guest
// wrapper substitutes them and removes them from the environment before exec,
// so they never reach public exec argv, environment or operation metadata.
type Agent struct {
	ID     int64
	Ref    string
	Argv   []string
	Env    map[string]string
	Secret map[string]string
	// OneJob is false for systems without a native one-job mode. The controller
	// then stops the agent itself after the native job reaches a final state.
	OneJob bool
}

// Forge is one CI system's runner contract. Execution, durable ownership,
// sandboxes, network, secrets and retention belong to the control service and
// are identical for every system; a Forge only speaks the system's native API.
type Forge interface {
	// Plugin is the stable Lamp plugin ID, e.g. "pg_forgejo".
	Plugin() string
	// System is the CI system's public name, e.g. "forgejo".
	System() string
	Issuer(r Repository) string
	Repos() []Repository
	Queued(ctx context.Context, r Repository) ([]Job, error)
	// Verify re-reads the job from the system and decides trust. Public is true
	// for anything a stranger can steer (forks, merge requests, public repos).
	Verify(ctx context.Context, r Repository, id int64) (Demand, error)
	Register(ctx context.Context, r Repository, d Demand, name string) (Agent, error)
	// GetJob reports Status queued|in_progress|completed and Conclusion
	// success|failure|cancelled once completed.
	GetJob(ctx context.Context, r Repository, id int64) (Job, error)
	Cancel(ctx context.Context, r Repository, run, job int64) error
	// DeregisterJob removes the one-job agent, including one whose registration
	// response was lost before its ID became durable (found by name).
	DeregisterJob(ctx context.Context, r Repository, name string, known int64) error
	Outputs(ctx context.Context, r Repository, run, job int64) ([]Output, error)
	Download(ctx context.Context, r Repository, path string, limit int64) ([]byte, error)
}

// Systems lists every shipped CI plugin, in the order `lamp ci list` shows.
var Systems = []string{"github-actions", "gitlab", "buildkite", "jenkins", "forgejo", "gitea", "circleci", "azure-pipelines", "bitbucket", "woodpecker"}

// NewForges builds every enrolled CI system. GitHub stays reachable for OIDC.
func NewForges(config Config) (*GitHub, map[string]Forge, error) {
	g, err := New(config)
	if err != nil {
		return nil, nil, err
	}
	out := map[string]Forge{}
	by := map[string][]Repository{}
	for _, r := range config.Repositories {
		by[r.system()] = append(by[r.system()], r)
	}
	for system, repos := range by {
		var f Forge
		h := hostClient{Config: config, HTTP: g.HTTP}
		switch system {
		case "github-actions":
			f = g
		case "forgejo", "gitea":
			f = &Gitea{hostClient: h, Kind: system, Repositories: repos}
		case "woodpecker":
			f = &Woodpecker{hostClient: h, Repositories: repos}
		case "jenkins":
			f = &Jenkins{hostClient: h, Repositories: repos}
		case "buildkite":
			f = &Buildkite{hostClient: h, Repositories: repos}
		case "gitlab":
			f = &GitLab{hostClient: h, Repositories: repos}
		case "circleci":
			f = &CircleCI{hostClient: h, Repositories: repos}
		case "azure-pipelines":
			f = &Azure{hostClient: h, Repositories: repos}
		case "bitbucket":
			f = &Bitbucket{hostClient: h, Repositories: repos}
		default:
			return nil, nil, fmt.Errorf("CI_PLUGIN_INCOMPATIBLE: unknown CI system %q", system)
		}
		out[f.Plugin()] = f
	}
	return g, out, nil
}

func (r Repository) system() string {
	if r.System == "" {
		return "github-actions"
	}
	return r.System
}

// hostClient is the shared credentialed transport for non-GitHub systems. The
// enrolled base URL is fixed host policy; paths are relative to it. Bodies and
// credentials never enter errors.
type hostClient struct {
	Config Config
	HTTP   *http.Client
}

func (h hostClient) token(r Repository) (string, error) {
	file := r.TokenFile
	if file == "" {
		file = h.Config.TokenFile
	}
	st, err := os.Stat(file)
	if err != nil || st.Mode().Perm()&0077 != 0 {
		return "", errors.New("CI credential must exist and be private")
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return "", errors.New("CI credential unavailable")
	}
	return strings.TrimSpace(string(b)), nil
}

// call sends one request. auth decorates it (bearer, basic, token header).
func (h hostClient) call(ctx context.Context, r Repository, method, path string, input, out any, auth func(*http.Request, string)) error {
	if strings.Contains(path, "..") || !strings.HasPrefix(path, "/") {
		return errors.New("invalid CI API path")
	}
	base, err := url.Parse(r.BaseURL)
	if err != nil || (base.Scheme != "https" && base.Scheme != "http") || base.User != nil {
		return errors.New("invalid enrolled CI base URL")
	}
	token, err := h.token(r)
	if err != nil {
		return err
	}
	var body io.Reader
	ctype := "application/json"
	switch v := input.(type) {
	case nil:
	case url.Values:
		body = strings.NewReader(v.Encode())
		ctype = "application/x-www-form-urlencoded"
	case []byte:
		body = bytes.NewReader(v)
		ctype = "application/xml"
	default:
		b, e := json.Marshal(v)
		if e != nil {
			return e
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(r.BaseURL, "/")+path, body)
	if err != nil {
		return errors.New("invalid CI request")
	}
	req.Header.Set("Content-Type", ctype)
	req.Header.Set("Accept", "application/json")
	auth(req, token)
	res, err := h.HTTP.Do(req)
	if err != nil {
		return errors.New("CI transport unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("CI request rejected (HTTP %d)", res.StatusCode)
	}
	switch o := out.(type) {
	case nil:
	case *[]byte:
		*o, err = io.ReadAll(io.LimitReader(res.Body, 64<<20))
		return err
	default:
		return json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(out)
	}
	return nil
}

func (h hostClient) download(ctx context.Context, r Repository, path string, limit int64, auth func(*http.Request, string)) ([]byte, error) {
	var b []byte
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := h.call(ctx, r, "GET", path, nil, &b, auth); err != nil {
		return nil, errors.New("native output unavailable")
	}
	if int64(len(b)) > limit {
		return nil, errors.New("native output exceeds collection allowance")
	}
	return b, nil
}

func bearer(req *http.Request, token string) { req.Header.Set("Authorization", "Bearer "+token) }

// conclusion maps native final states to Lamp's three results.
func conclusion(native string) string {
	switch strings.ToLower(native) {
	case "success", "passed", "succeeded", "successful", "ok":
		return "success"
	case "cancelled", "canceled", "killed", "aborted", "stopped", "skipped", "canceling":
		return "cancelled"
	}
	return "failure"
}
