package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"lamp.eilmer.si/internal/surface"
)

type createMount struct{ kind, resource, path string }

func mountFlag(kind string, mounts *[]createMount) func(string) error {
	return func(raw string) error {
		resource, path, ok := strings.Cut(raw, ":")
		if !ok || resource == "" || !strings.HasPrefix(path, "/") {
			return fmt.Errorf("%s must be name-or-id:/absolute/path", kind)
		}
		*mounts = append(*mounts, createMount{kind, resource, path})
		return nil
	}
}
func createAttachments(ctx context.Context, c *surface.Client, created []byte, mounts []createMount, key string) error {
	call := func(name string, in map[string]any) (map[string]any, error) {
		data, status, err := c.Call(ctx, surface.ByName[name], in)
		if err != nil {
			return nil, err
		}
		if status >= 400 {
			return nil, fmt.Errorf("%s", data)
		}
		var out map[string]any
		err = json.Unmarshal(data, &out)
		return out, err
	}
	var out map[string]any
	if err := json.Unmarshal(created, &out); err != nil {
		return err
	}
	refs, ok := out["resource_ids"].([]any)
	if !ok || len(refs) == 0 {
		return fmt.Errorf("create returned no sandbox")
	}
	sandbox := refs[0].(map[string]any)["id"].(string)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		result, err := call("sandbox_get", map[string]any{"sandbox_id": sandbox})
		if err != nil {
			return err
		}
		state := result["data"].(map[string]any)["state"]
		if state == "ready" {
			break
		}
		if state == "failed" || state == "unavailable" {
			return fmt.Errorf("sandbox %s is %s", sandbox, state)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	for i, m := range mounts {
		resource := m.resource
		prefix := "vo_"
		if m.kind == "cache" {
			prefix = "ca_"
		}
		if !strings.HasPrefix(resource, prefix) {
			result, err := call(m.kind+"_list", map[string]any{"limit": 1000})
			if err != nil {
				return err
			}
			resource = ""
			for _, item := range result["items"].([]any) {
				r := item.(map[string]any)
				if r["name"] == m.resource && r["state"] != "deleted" {
					if resource != "" {
						return fmt.Errorf("ambiguous %s name %s; use its ID", m.kind, m.resource)
					}
					resource = r["id"].(string)
				}
			}
			if resource == "" {
				return fmt.Errorf("%s %s not found; sandbox %s remains ready", m.kind, m.resource, sandbox)
			}
		}
		if _, err := call("attachment_create", map[string]any{"sandbox_id": sandbox, "resource_kind": m.kind, "resource_id": resource, "mount_path": m.path, "mode": "read_write", "idempotency_key": fmt.Sprintf("%s-mount-%d", key, i), "wait_ms": 60000}); err != nil {
			return fmt.Errorf("sandbox %s storage attachment: %w", sandbox, err)
		}
	}
	return nil
}
