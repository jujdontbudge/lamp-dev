package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"lamp.eilmer.si/internal/surface"
	"lamp.eilmer.si/pkg/api"
)

func emit(out, errs io.Writer, data []byte, status int, asJSON bool) {
	target := out
	if status >= 400 || operationFailed(data) {
		target = errs
	}
	if asJSON {
		fmt.Fprintln(target, string(data))
	} else {
		humanResult(target, data)
	}
}
func operationFailed(raw []byte) bool {
	m, _ := surface.Decode(raw)
	for _, key := range []string{"operation", "data"} {
		if op, ok := m[key].(map[string]any); ok && op["kind"] == "operation" {
			switch op["state"] {
			case "failed", "canceled", "outcome_unknown":
				return true
			}
		}
	}
	return false
}
func callProgress(ctx context.Context, client *surface.Client, op api.OperationDescriptor, in map[string]any, errs io.Writer, asJSON bool) ([]byte, int, error) {
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		if asJSON {
			return
		}
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				fmt.Fprintln(errs, "Waiting for", op.CLI+"…")
			}
		}
	}()
	data, status, err := client.Call(ctx, op, in)
	close(done)
	<-stopped
	return data, status, err
}
func waitFor(ctx context.Context, client *surface.Client, op api.OperationDescriptor, in map[string]any, desired string, timeout time.Duration, out, errs io.Writer, asJSON bool) int {
	if timeout <= 0 {
		cliError(errs, "INVALID_ARGUMENT", "--timeout must be positive", asJSON)
		return 2
	}
	if op.Name == "sandbox_get" {
		switch desired {
		case "ready", "suspended", "deleted", "failed", "unavailable", "accepted", "preparing":
		default:
			cliError(errs, "INVALID_ARGUMENT", "unknown sandbox state "+desired, asJSON)
			return 2
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	lastState := ""
	for {
		if op.Name == "operation_wait" {
			if _, ok := in["timeout_ms"]; !ok {
				in["timeout_ms"] = uint64(1000)
			}
		}
		raw, status, err := client.Call(ctx, op, in)
		if err != nil {
			cliError(errs, "DATA_UNAVAILABLE", "wait ended: "+err.Error()+"; inspect the resource or retry the wait", asJSON)
			return 6
		}
		if status >= 400 {
			emit(out, errs, raw, status, asJSON)
			return statusExitCode(status)
		}
		m, err := surface.Decode(raw)
		if err != nil {
			cliError(errs, "DATA_UNAVAILABLE", "invalid wait response", asJSON)
			return 6
		}
		resource, _ := m["data"].(map[string]any)
		state, _ := resource["state"].(string)
		if state == "" {
			cliError(errs, "DATA_UNAVAILABLE", "wait response has no state", asJSON)
			return 6
		}
		if !asJSON && state != lastState {
			fmt.Fprintf(errs, "Waiting: %s\n", state)
			lastState = state
		}
		if op.Name == "sandbox_get" && state == desired || op.Name == "operation_wait" && state == "succeeded" {
			emit(out, errs, raw, status, asJSON)
			return 0
		}
		if state == "failed" || state == "canceled" || state == "outcome_unknown" || state == "deleted" || state == "unavailable" {
			if op.Name == "operation_wait" {
				emit(out, errs, raw, 400, asJSON)
			} else {
				detail := "sandbox is " + state + "; inspect lamp sandbox get for readiness details"
				if readiness, ok := resource["readiness"].(map[string]any); ok {
					b, _ := json.Marshal(readiness)
					detail += ": " + string(b)
				}
				cliError(errs, "RESOURCE_IN_USE", detail, asJSON)
			}
			return 1
		}
		select {
		case <-ctx.Done():
			cliError(errs, "DATA_UNAVAILABLE", "wait timed out; inspect the resource or increase --timeout", asJSON)
			return 6
		case <-time.After(200 * time.Millisecond):
		}
	}
}
