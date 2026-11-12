package ci

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

type Output struct {
	Name string
	Path string
	Size uint64
}

func (g *GitHub) Outputs(ctx context.Context, r Repository, run, job int64) ([]Output, error) {
	out := []Output{{Name: "github-job.log", Path: fmt.Sprintf("/repos/%s/actions/jobs/%d/logs", r.Name, job)}}
	for page := 1; page <= 10; page++ {
		var a struct {
			Artifacts []struct {
				ID      int64  `json:"id"`
				Name    string `json:"name"`
				Size    uint64 `json:"size_in_bytes"`
				Expired bool   `json:"expired"`
			} `json:"artifacts"`
		}
		if err := g.API(ctx, "GET", fmt.Sprintf("/repos/%s/actions/runs/%d/artifacts?per_page=100&page=%d", r.Name, run, page), nil, &a); err != nil {
			return out, err
		}
		for _, v := range a.Artifacts {
			if !v.Expired {
				out = append(out, Output{Name: v.Name + ".zip", Path: fmt.Sprintf("/repos/%s/actions/artifacts/%d/zip", r.Name, v.ID), Size: v.Size})
			}
		}
		if len(a.Artifacts) < 100 {
			break
		}
	}
	return out, nil
}

// Download follows GitHub's signed artifact URL without forwarding host auth.
// URLs and bodies never enter errors, journals or evidence metadata.
func (g *GitHub) Download(ctx context.Context, path string, limit int64) ([]byte, error) {
	token, err := os.ReadFile(g.Config.TokenFile)
	if err != nil {
		return nil, errors.New("forge credential unavailable")
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.github.com"+path, nil)
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	res, err := g.HTTP.Do(req)
	if err != nil {
		return nil, errors.New("native output unavailable")
	}
	if res.StatusCode == 302 {
		location := res.Header.Get("Location")
		res.Body.Close()
		u, e := url.Parse(location)
		if e != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || !(strings.HasSuffix(u.Hostname(), ".blob.core.windows.net") || strings.HasSuffix(u.Hostname(), ".actions.githubusercontent.com") || u.Hostname() == "results-receiver.actions.githubusercontent.com") {
			return nil, errors.New("native output destination rejected")
		}
		req, _ = http.NewRequestWithContext(ctx, "GET", location, nil)
		res, err = g.HTTP.Do(req)
		if err != nil {
			return nil, errors.New("native output unavailable")
		}
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, errors.New("native output not ready")
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, errors.New("native output exceeds collection allowance")
	}
	return b, nil
}
