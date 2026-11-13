package surface

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

func terminalKey() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return "terminal-" + hex.EncodeToString(b[:])
}

// Terminal uses only the public exec API. Reusing execID reconnects to a PTY;
// callers retain the returned ID when they want to disconnect and reconnect.
func Terminal(ctx context.Context, c *Client, sandbox, execID string, input io.Reader, stdout, stderr io.Writer, sizes <-chan [2]uint64, terminalTypes ...string) (string, int, error) {
	terminalType := "xterm-256color"
	if len(terminalTypes) > 0 && terminalTypes[0] != "" {
		terminalType = terminalTypes[0]
	}
	return execChannel(ctx, c, sandbox, execID, []string{"/bin/sh", "-i"}, true, input, stdout, stderr, sizes, terminalType)
}

// ExecChannel exposes a binary SSH command channel without allocating a PTY.
func ExecChannel(ctx context.Context, c *Client, sandbox string, argv []string, input io.Reader, stdout, stderr io.Writer) (string, int, error) {
	return execChannel(ctx, c, sandbox, "", argv, false, input, stdout, stderr, nil, "")
}
func execChannel(ctx context.Context, c *Client, sandbox, execID string, argv []string, pty bool, input io.Reader, stdout, stderr io.Writer, sizes <-chan [2]uint64, terminalType string) (string, int, error) {

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	call := func(name string, in map[string]any) (map[string]any, error) {
		op := ByName[name]
		if op.Mutation {
			in["idempotency_key"] = terminalKey()
			in["wait_ms"] = uint64(60000)
		}
		bytes, status, err := c.Call(ctx, op, in)
		if err != nil {
			return nil, err
		}
		m, err := Decode(bytes)
		if err != nil {
			return nil, err
		}
		if status < 200 || status >= 300 {
			return nil, fmt.Errorf("%s: %s", name, string(bytes))
		}
		return m, nil
	}
	if execID == "" {
		in := map[string]any{"sandbox_id": sandbox, "argv": argv, "pty": pty, "stdin_open": !pty}
		if pty {
			in["env"] = map[string]string{"TERM": terminalType}
		}
		m, err := call("exec_create", in)
		if err != nil {
			return "", 0, err
		}
		result, _ := m["result"].(map[string]any)
		execID, _ = result["exec_id"].(string)
		if execID == "" {
			return "", 0, fmt.Errorf("terminal start did not complete; inspect operation %v", m["operation_id"])
		}
	}
	m, err := call("exec_get", map[string]any{"exec_id": execID})
	if err != nil {
		return execID, 0, err
	}
	ex, _ := m["data"].(map[string]any)
	if ex["sandbox_id"] != sandbox || (pty && ex["pty"] != true) {
		return execID, 0, fmt.Errorf("exec is not a terminal in this sandbox")
	}
	if !pty {
		defer func() {
			cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer stop()
			result, err := Invoke(cleanup, c, "exec_get", map[string]any{"exec_id": execID})
			if err == nil {
				current, _ := result["data"].(map[string]any)
				if current["state"] == "running" {
					_, _ = Invoke(cleanup, c, "exec_interrupt", map[string]any{"exec_id": execID, "expected_revision": current["revision"], "signal": uint64(9)})
				}
			}
		}()
	}
	revision := ex["revision"]
	failure := make(chan error, 2)
	if input != nil {
		go func() {
			bytes := make([]byte, 4096)
			var sequence uint64
			for {
				n, err := input.Read(bytes)
				if n > 0 {
					_, e := call("exec_input", map[string]any{"exec_id": execID, "expected_revision": revision, "sequence": sequence, "data_base64": base64.StdEncoding.EncodeToString(bytes[:n])})
					if e != nil {
						failure <- e
						return
					}
					sequence++
				}
				if err != nil {
					if err == io.EOF && !pty {
						_, e := call("exec_input", map[string]any{"exec_id": execID, "expected_revision": revision, "sequence": sequence, "data_base64": "", "eof": true})
						if e != nil {
							failure <- e
						}
					}
					return
				}
			}
		}()
	}
	if sizes != nil {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case size, ok := <-sizes:
					if !ok {
						return
					}
					_, err := call("exec_resize", map[string]any{"exec_id": execID, "expected_revision": revision, "rows": size[0], "columns": size[1]})
					if err != nil {
						failure <- err
						return
					}
				}
			}
		}()
	}
	cursor := ""
	for {
		select {
		case err := <-failure:
			return execID, 0, err
		default:
		}
		in := map[string]any{"exec_id": execID, "wait_ms": uint64(1000)}
		if cursor != "" {
			in["cursor"] = cursor
		}
		result, err := call("exec_watch", in)
		if err != nil {
			return execID, 0, err
		}
		frames, _ := result["items"].([]any)
		for _, raw := range frames {
			frame, _ := raw.(map[string]any)
			data, err := base64.StdEncoding.DecodeString(fmt.Sprint(frame["data_base64"]))
			if err != nil {
				return execID, 0, err
			}
			writer := stdout
			if frame["channel"] == "stderr" {
				writer = stderr
			}
			if _, err = writer.Write(data); err != nil {
				return execID, 0, err
			}
		}
		cursor, _ = result["next_cursor"].(string)
		if cursor == "" {
			m, err := call("exec_get", map[string]any{"exec_id": execID})
			if err != nil {
				return execID, 0, err
			}
			ex, _ := m["data"].(map[string]any)
			if ex["state"] != "exited" {
				return execID, 0, fmt.Errorf("terminal result %v", ex["state"])
			}
			code, _ := ex["exit_code"].(json.Number)
			n, _ := code.Int64()
			return execID, int(n), nil
		}
		if len(frames) == 0 {
			select {
			case <-ctx.Done():
				return execID, 0, ctx.Err()
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
}
