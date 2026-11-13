package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"

	"lamp.eilmer.si/pkg/api"
)

// ApprovalWebhook fixes the entire effect in owner enrollment, outside the API.
// The plan returned to the requester contains the exact JSON body and destination.
// Credentials are deliberately unsupported; enroll credentialed actions in the broker.
type ApprovalWebhook struct {
	Bound BoundAction `json:"bound"`
	URL   string      `json:"url"`
}

func LoadApprovalWebhooks(path string) (map[string]ProtectedAction, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 1<<20 {
		return nil, fmt.Errorf("approval actions require a private regular file of at most 1 MiB")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var entries []ApprovalWebhook
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&entries); err != nil {
		return nil, err
	}
	if dec.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("expected one approval actions document")
	}
	out := map[string]ProtectedAction{}
	for _, entry := range entries {
		bound := cloneBoundAction(entry.Bound)
		u, err := url.Parse(entry.URL)
		if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
			return nil, fmt.Errorf("approval webhook requires an http(s) URL without credentials, query or fragment")
		}
		if bound.PrincipalID == "" || bound.TaskID == "" || bound.SandboxID == "" || bound.Generation == 0 || bound.ResourceRevision == 0 || bound.Plan.ResourceRef == nil || bound.Plan.ResourceRef.Kind != "connection" || bound.Plan.ResourceRef.Id == "" || bound.Plan.Arguments == nil || !json.Valid(*bound.Plan.Arguments) || bound.Plan.Operation == nil || *bound.Plan.Operation != "post" {
			return nil, fmt.Errorf("approval webhook requires an exact principal, task, sandbox, generation, connection, revision and post JSON body")
		}
		port := uint64(443)
		if u.Scheme == "http" {
			port = 80
		}
		if u.Port() != "" {
			port, err = strconv.ParseUint(u.Port(), 10, 16)
			if err != nil || port == 0 {
				return nil, fmt.Errorf("invalid webhook port")
			}
		}
		dest := api.Destination{Hostname: pointer(u.Hostname()), Port: &port, Protocol: pointer(u.Scheme)}
		bound.Plan.Destination = &dest
		// All enrolled bytes, including the path, contribute to adapter identity.
		bound.Plan.AdapterDigest = pointer(digest(entry))
		id := bound.Plan.ResourceRef.Id
		if _, ok := out[id]; ok {
			return nil, fmt.Errorf("duplicate approval connection")
		}
		endpoint := entry.URL
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("approval webhook redirects denied") }}
		out[id] = ProtectedAction{
			Describe:         func() BoundAction { return cloneBoundAction(bound) },
			ValidateApproval: func(in api.ApprovalCreateInput) (BoundAction, error) { return cloneBoundAction(bound), nil },
			ValidateInvocation: func(in api.ConnectionInvokeInput) (BoundAction, error) {
				if in.ConnectionId != id || in.AttachmentId != bound.SandboxID || in.Operation != "post" || digest(in.Arguments) != digest(bound.Plan.Arguments) {
					return BoundAction{}, reject("APPROVAL_REQUIRED", "invocation differs from enrolled webhook effect")
				}
				return cloneBoundAction(bound), nil
			},
			Execute: func(ctx context.Context, b BoundAction) (api.AdapterDocument, error) {
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(*b.Plan.Arguments))
				if err != nil {
					return nil, fmt.Errorf("invalid webhook request")
				}
				req.Header.Set("Content-Type", "application/json")
				response, err := client.Do(req)
				if err != nil {
					return nil, reject("OUTCOME_UNKNOWN", "approval webhook delivery unavailable; effect may have occurred")
				}
				defer response.Body.Close()
				if response.StatusCode < 200 || response.StatusCode >= 300 {
					return nil, reject("OUTCOME_UNKNOWN", fmt.Sprintf("approval webhook returned HTTP %d; effect may have occurred", response.StatusCode))
				}
				result, _ := json.Marshal(map[string]any{"status": response.StatusCode})
				return api.AdapterDocument(result), nil
			},
		}
	}
	return out, nil
}
