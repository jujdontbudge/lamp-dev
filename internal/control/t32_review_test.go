package control

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func t32InstalledService(t *testing.T) *Service {
	t.Helper()
	config := storeConfig(t)
	config.InstallDir = t.TempDir()
	tokenHash := sha256.Sum256([]byte("review-disposable-owner"))
	config.Identity = &IdentityConfig{OwnerID: "pr_owner", BootstrapSHA256: hex.EncodeToString(tokenHash[:])}
	if err := os.MkdirAll(filepath.Join(config.InstallDir, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	const template = `{"id":"tpl_default","kind":"template","owner_id":"pr_owner","revision":1,"state":"ready","visibility":"private","labels":{"lamp.default":"true"},"default_resources":{"vcpus":2,"memory_mib":2048,"disk_gib":20,"pids_max":4096}}`
	if err := os.WriteFile(filepath.Join(config.InstallDir, "config", "default-template.json"), []byte(template), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestT32InstalledTemplateDeleteDoesNotPanic(t *testing.T) {
	s := t32InstalledService(t)
	// The public handler normally recovers this panic in net/http but retains
	// the service mutex. Release it here solely to let the fixture shut down.
	defer func() {
		if p := recover(); p != nil {
			s.mu.Unlock()
			t.Fatalf("authenticated DELETE of listed default template panicked: %v (service mutex retained)", p)
		}
	}()
	req := httptest.NewRequest("DELETE", "/v1/templates/tpl_default", nil)
	req.Header.Set("Authorization", "Bearer review-disposable-owner")
	req.Header.Set("Idempotency-Key", "review-delete-default")
	req.Header.Set("If-Match", "1")
	response := httptest.NewRecorder()
	s.ServeHTTP(response, req)
	if response.Code >= 500 {
		t.Fatalf("delete returned %d: %s", response.Code, response.Body.String())
	}
}

func TestT32HTTPDefaultTemplateWithLifecycle(t *testing.T) {
	s := t32InstalledService(t)
	req := httptest.NewRequest("POST", "/v1/sandboxes", strings.NewReader(`{"lifecycle":{"idle_grace_seconds":600}}`))
	req.Header.Set("Authorization", "Bearer review-disposable-owner")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "review-default-lifecycle")
	response := httptest.NewRecorder()
	s.ServeHTTP(response, req)
	// T33 now probes capacity before accepting a create. This fixture has no
	// worker: reaching that boundary proves the default-template rewrite was
	// valid, while the original bug fails earlier with template_id required.
	if response.Code != 503 || !strings.Contains(response.Body.String(), "HOST_UNAVAILABLE") || !strings.Contains(response.Body.String(), "/unused") {
		t.Fatalf("default-template create did not reach worker admission: HTTP %d: %s", response.Code, response.Body.String())
	}
}
