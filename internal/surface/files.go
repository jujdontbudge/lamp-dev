package surface

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
)

// Invoke waits for a public mutation's recorded completion, including transfers
// that outlive one HTTP long poll. It never replays an uncertain operation.
func Invoke(ctx context.Context, c *Client, name string, in map[string]any) (map[string]any, error) {
	op := ByName[name]
	if op.Mutation {
		in["idempotency_key"] = terminalKey()
		in["wait_ms"] = uint64(60000)
	}
	bytes, status, err := c.Call(ctx, op, in)
	if err != nil {
		return nil, err
	}
	result, err := Decode(bytes)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("%s: %s", name, string(bytes))
	}
	if !op.Mutation {
		return result, nil
	}
	if done, ok := result["result"].(map[string]any); ok {
		return done, nil
	}
	id, _ := result["operation_id"].(string)
	if id == "" {
		return nil, fmt.Errorf("missing operation identity")
	}
	for {
		bytes, status, err = c.Call(ctx, ByName["operation_wait"], map[string]any{"operation_id": id, "timeout_ms": uint64(60000)})
		if err != nil {
			return nil, err
		}
		if status != 200 {
			return nil, fmt.Errorf("operation %s: %s", id, string(bytes))
		}
		result, err = Decode(bytes)
		if err != nil {
			return nil, err
		}
		operation, _ := result["data"].(map[string]any)
		if operation == nil {
			operation = result
		}
		switch operation["state"] {
		case "succeeded":
			outcome, _ := operation["outcome"].(map[string]any)
			done, _ := outcome["result"].(map[string]any)
			return done, nil
		case "failed", "outcome_unknown", "canceled":
			return nil, fmt.Errorf("operation %s: %s", id, string(bytes))
		}
	}
}
func resourceRevision(ctx context.Context, c *Client, kind, id string) (any, error) {
	m, err := Invoke(ctx, c, kind+"_get", map[string]any{kind + "_id": id})
	if err != nil {
		return nil, err
	}
	data, _ := m["data"].(map[string]any)
	return data["revision"], nil
}
func UploadFile(ctx context.Context, c *Client, sandbox, path string, source io.Reader) error {
	created, err := Invoke(ctx, c, "blob_create", map[string]any{})
	if err != nil {
		return err
	}
	blob, _ := created["blob_id"].(string)
	if blob == "" {
		return fmt.Errorf("blob identity missing")
	}
	defer func() {
		rev, err := resourceRevision(context.WithoutCancel(ctx), c, "blob", blob)
		if err == nil {
			_, _ = Invoke(context.WithoutCancel(ctx), c, "blob_delete", map[string]any{"blob_id": blob, "expected_revision": rev})
		}
	}()
	digest := sha256.New()
	buffer := make([]byte, 1<<20)
	var offset uint64
	for {
		n, readErr := source.Read(buffer)
		if n > 0 {
			bytes := buffer[:n]
			digest.Write(bytes)
			sum := sha256.Sum256(bytes)
			revision, err := resourceRevision(ctx, c, "blob", blob)
			if err != nil {
				return err
			}
			_, err = Invoke(ctx, c, "blob_upload", map[string]any{"blob_id": blob, "offset_bytes": offset, "data_base64": base64.StdEncoding.EncodeToString(bytes), "chunk_sha256": hex.EncodeToString(sum[:]), "expected_revision": revision})
			if err != nil {
				return err
			}
			offset += uint64(n)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	revision, err := resourceRevision(ctx, c, "blob", blob)
	if err != nil {
		return err
	}
	_, err = Invoke(ctx, c, "blob_finalize", map[string]any{"blob_id": blob, "sha256": hex.EncodeToString(digest.Sum(nil)), "size_bytes": offset, "expected_revision": revision})
	if err != nil {
		return err
	}
	revision, err = resourceRevision(ctx, c, "sandbox", sandbox)
	if err != nil {
		return err
	}
	_, err = Invoke(ctx, c, "sandbox_file_transfer", map[string]any{"sandbox_id": sandbox, "path": path, "blob_id": blob, "direction": "upload", "expected_revision": revision})
	return err
}
func DownloadFile(ctx context.Context, c *Client, sandbox, path string, destination io.Writer) error {
	revision, err := resourceRevision(ctx, c, "sandbox", sandbox)
	if err != nil {
		return err
	}
	result, err := Invoke(ctx, c, "sandbox_file_transfer", map[string]any{"sandbox_id": sandbox, "path": path, "direction": "download", "expected_revision": revision})
	if err != nil {
		return err
	}
	blob, _ := result["blob_id"].(string)
	if blob == "" {
		return fmt.Errorf("download blob identity missing")
	}
	defer func() {
		rev, err := resourceRevision(context.WithoutCancel(ctx), c, "blob", blob)
		if err == nil {
			_, _ = Invoke(context.WithoutCancel(ctx), c, "blob_delete", map[string]any{"blob_id": blob, "expected_revision": rev})
		}
	}()
	digest := sha256.New()
	var offset uint64
	for {
		result, err = Invoke(ctx, c, "blob_download", map[string]any{"blob_id": blob, "offset_bytes": offset, "length_bytes": uint64(1 << 20)})
		if err != nil {
			return err
		}
		bytes, err := base64.StdEncoding.DecodeString(fmt.Sprint(result["data_base64"]))
		if err != nil {
			return err
		}
		sum := sha256.Sum256(bytes)
		if hex.EncodeToString(sum[:]) != result["chunk_sha256"] {
			return fmt.Errorf("download chunk SHA-256 mismatch")
		}
		digest.Write(bytes)
		if _, err = destination.Write(bytes); err != nil {
			return err
		}
		offset += uint64(len(bytes))
		if result["eof"] == true {
			size, _ := result["size_bytes"].(json.Number)
			n, _ := size.Int64()
			if uint64(n) != offset || hex.EncodeToString(digest.Sum(nil)) != result["sha256"] {
				return fmt.Errorf("download SHA-256 or size mismatch")
			}
			return nil
		}
		if len(bytes) == 0 {
			return fmt.Errorf("download made no progress")
		}
	}
}
