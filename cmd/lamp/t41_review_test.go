package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lamp.eilmer.si/internal/control"
)

func reviewService(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	var credential [32]byte
	if _, err := rand.Read(credential[:]); err != nil {
		t.Fatal(err)
	}
	token := hex.EncodeToString(credential[:])
	hash := sha256.Sum256([]byte(token))
	s, err := control.Open(control.Config{
		StateDir: filepath.Join(dir, "state"),
		Identity: &control.IdentityConfig{OwnerID: "pr_review", BootstrapSHA256: hex.EncodeToString(hash[:])},
		Worker:   control.WorkerConfig{Socket: filepath.Join(dir, "absent.sock"), WorkerID: "review", BootID: "review", GrantID: "review", UID: os.Getuid(), PID: os.Getpid()},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	server := httptest.NewServer(s)
	t.Cleanup(server.Close)
	t.Setenv("LAMP_TOKEN_FILE", "")
	t.Setenv("LAMP_TOKEN", token)
	t.Setenv("LAMP_API_URL", server.URL)
	return server.URL
}

func TestReviewHumanKeyCreationPreservesOneTimeResult(t *testing.T) {
	reviewService(t)
	args := []string{"key", "create", "--expires-at", time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "--idempotency-key", "review-key"}
	var out, errs bytes.Buffer
	if code := run(args, &out, &errs); code != 0 {
		t.Fatalf("key create exit %d: %s", code, &errs)
	}
	human := out.String()
	out.Reset()
	errs.Reset()
	if code := run(append(args, "--json"), &out, &errs); code != 0 {
		t.Fatalf("key replay exit %d: %s", code, &errs)
	}
	var replay struct {
		Replayed bool `json:"replayed"`
		Result   struct {
			Secret string `json:"secret"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &replay); err != nil {
		t.Fatal(err)
	}
	if !replay.Replayed || replay.Result.Secret != "" {
		t.Fatal("expected replay to preserve one-time delivery")
	}
	if !strings.Contains(human, "lamp_") {
		t.Fatalf("human key create discarded the only secret delivery; replay cannot recover it. Output:\n%s", human)
	}
}

func TestReviewTelemetryUpdateFetchesRevision(t *testing.T) {
	reviewService(t)
	var out, errs bytes.Buffer
	if code := run([]string{"telemetry", "update", "--enabled", "false", "--expected-revision", "1", "--json"}, &out, &errs); code != 0 {
		t.Fatalf("explicit revision update exit %d: %s", code, &errs)
	}
	out.Reset()
	errs.Reset()
	if code := run([]string{"telemetry", "update", "--enabled", "false", "--json"}, &out, &errs); code != 0 {
		t.Fatalf("automatic revision update exit %d: %s", code, &errs)
	}
}
