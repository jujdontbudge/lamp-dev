package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"lamp.eilmer.si/internal/surface"
)

func templateFile(ctx context.Context, c *surface.Client, file string, stdout, stderr io.Writer, asJSON bool) int {
	raw, err := os.ReadFile(file)
	if err != nil || len(raw) > 256<<10 {
		cliError(stderr, "INVALID_ARGUMENT", "template file must be readable and at most 256 KiB", asJSON)
		return 2
	}
	key := fmt.Sprintf("template-file-%d", time.Now().UnixNano())
	call := func(name string, in map[string]any) (map[string]any, error) {
		if surface.ByName[name].Mutation {
			in["idempotency_key"] = key + "-" + name
			in["wait_ms"] = 60000
		}
		data, status, err := c.Call(ctx, surface.ByName[name], in)
		if err != nil {
			return nil, err
		}
		var out map[string]any
		if err = json.Unmarshal(data, &out); err != nil {
			return nil, err
		}
		if status >= 400 {
			return nil, templateResponseError(data)
		}
		return out, nil
	}
	fail := func(err error) int {
		code := "TEMPLATE_BUILD_FAILED"
		var apiErr *templateAPIError
		if errors.As(err, &apiErr) {
			code = apiErr.code
		}
		cliError(stderr, code, err.Error(), asJSON)
		return 1
	}
	hash := sha256.Sum256(raw)
	digest := hex.EncodeToString(hash[:])
	created, err := call("blob_create", map[string]any{"size_bytes": len(raw), "sha256": digest})
	if err != nil {
		return fail(err)
	}
	refs := created["resource_ids"].([]any)
	blob := refs[0].(map[string]any)["id"].(string)
	if _, err = call("blob_upload", map[string]any{"blob_id": blob, "expected_revision": 1, "offset_bytes": 0, "chunk_sha256": digest, "data_base64": base64.StdEncoding.EncodeToString(raw)}); err != nil {
		return fail(err)
	}
	if _, err = call("blob_finalize", map[string]any{"blob_id": blob, "expected_revision": 2, "size_bytes": len(raw), "sha256": digest}); err != nil {
		return fail(err)
	}
	made, err := call("template_create", map[string]any{"name": file, "definition_blob_id": blob})
	if err != nil {
		return fail(err)
	}
	template := made["resource_ids"].([]any)[0].(map[string]any)["id"].(string)
	// Accept immediately, then print stages as the durable template reports them.
	data, status, err := c.Call(ctx, surface.ByName["template_build"], map[string]any{"template_id": template, "definition_blob_id": blob, "expected_revision": 1, "idempotency_key": key + "-build"})
	if err != nil {
		return fail(err)
	}
	if status >= 400 {
		return fail(templateResponseError(data))
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	last := ""
	for {
		result, err := call("template_get", map[string]any{"template_id": template})
		if err != nil {
			return fail(err)
		}
		resource := result["data"].(map[string]any)
		if labels, ok := resource["labels"].(map[string]any); ok {
			log, _ := labels["build_log"].(string)
			if log != last {
				if len(log) >= len(last) && log[:len(last)] == last {
					fmt.Fprint(stderr, log[len(last):])
				} else {
					fmt.Fprintln(stderr, log)
				}
				last = log
			}
		}
		switch resource["state"] {
		case "ready":
			json.NewEncoder(stdout).Encode(result)
			return 0
		case "failed":
			return fail(fmt.Errorf("template %s failed; see template get for retained build log", template))
		}
		select {
		case <-ctx.Done():
			return fail(ctx.Err())
		case <-ticker.C:
		}
	}
}

type templateAPIError struct{ code, message string }

func (e *templateAPIError) Error() string { return e.message }
func templateResponseError(data []byte) error {
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &envelope) == nil && envelope.Error.Code != "" {
		return &templateAPIError{envelope.Error.Code, envelope.Error.Message}
	}
	return fmt.Errorf("%s", data)
}
