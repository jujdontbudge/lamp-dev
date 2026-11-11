package surface

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// WakeSandbox is the activity wake for fronts that hold only public API
// access (SSH attach, allowed dev requests): a suspended sandbox is resumed
// and the call returns once it is ready. A ready sandbox is left alone;
// concurrent wakes coalesce on Lamp's single resume of a revision.
func WakeSandbox(ctx context.Context, c *Client, id string) error {
	for {
		raw, status, err := c.Call(ctx, ByName["sandbox_get"], map[string]any{"sandbox_id": id})
		if err != nil {
			return err
		}
		m, err := Decode(raw)
		if err != nil {
			return fmt.Errorf("invalid control response")
		}
		if status < 200 || status >= 300 {
			return fmt.Errorf("sandbox_get: %s", errorCode(m))
		}
		sb, _ := m["data"].(map[string]any)
		state, _ := sb["state"].(string)
		switch state {
		case "ready":
			return nil
		case "suspended":
			revision, _ := strconv.ParseUint(fmt.Sprint(sb["revision"]), 10, 64)
			in := map[string]any{"sandbox_id": id, "expected_revision": revision, "wait_ms": uint64(60000),
				"idempotency_key": "lamp-front-wake:" + id + ":" + strconv.FormatUint(revision, 10)}
			raw, status, err := c.Call(ctx, ByName["sandbox_resume"], in)
			if err != nil {
				return err
			}
			if status >= 300 {
				m, _ := Decode(raw)
				// Another wake already moved it; observe again.
				if code := errorCode(m); code != "REVISION_CONFLICT" && code != "RESOURCE_IN_USE" {
					return fmt.Errorf("sandbox_resume: %s", code)
				}
			}
		case "resuming", "suspending":
		default:
			return fmt.Errorf("sandbox is %s", state)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func errorCode(m map[string]any) string {
	if e, ok := m["error"].(map[string]any); ok {
		if code, ok := e["code"].(string); ok {
			return code
		}
	}
	return "DATA_UNAVAILABLE"
}
