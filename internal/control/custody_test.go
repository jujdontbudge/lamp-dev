package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JohanSardar/lamp/pkg/api"
)

func TestShareReaderCanAccessOnlyTheCleanProjection(t *testing.T) {
	s := &Service{readers: map[uint32]string{65534: "pr_reader"}, journal: journal{
		Shares:    map[string]api.Share{"sh_handoff": {State: "active", SubjectId: pointer("pr_reader"), ResourceKind: pointer("template"), ResourceId: pointer("tp_clean")}},
		Templates: map[string]cleanExport{"tp_clean": {TemplateID: "tp_clean", BlobID: "bl_clean"}},
	}}
	reader := context.WithValue(context.Background(), peerContextKey{}, publicPeer{principal: "pr_reader"})
	for _, path := range []string{"/v1/shares/sh_handoff", "/v1/templates/tp_clean", "/v1/blobs/bl_clean", "/v1/blobs/bl_clean/content"} {
		if !s.authorizePublicRequest(reader, "GET", path) {
			t.Fatalf("clean projection denied: %s", path)
		}
		if s.authorizePublicRequest(reader, "POST", path) {
			t.Fatalf("reader acquired mutation authority: %s", path)
		}
	}
	for _, path := range []string{"/v1/sandboxes/sb_private", "/v1/snapshots/ss_private", "/v1/operations/op_private", "/v1/execs/ex_private", "/v1/templates/tp_other", "/v1/blobs/bl_other/content"} {
		if s.authorizePublicRequest(reader, "GET", path) {
			t.Fatalf("private source leaked through share: %s", path)
		}
	}
	if s.authorizePublicRequest(context.Background(), "GET", "/v1/templates/tp_clean") {
		t.Fatal("missing authenticated peer was treated as owner")
	}
	share := s.journal.Shares["sh_handoff"]
	share.State = "revoked"
	s.journal.Shares["sh_handoff"] = share
	if s.authorizePublicRequest(reader, "GET", "/v1/blobs/bl_clean/content") {
		t.Fatal("revoked share retained access")
	}
}

func TestCleanExportDownloadVerifiesObjectAndNeverFollowsSubstitutedPaths(t *testing.T) {
	dir := t.TempDir()
	payload := []byte("nonsecret clean reconstruction bundle")
	sum := sha256.Sum256(payload)
	id := "bl_" + hex.EncodeToString(sum[:])
	s := &Service{exportsDir: dir, journal: journal{Templates: map[string]cleanExport{"tp_clean": {TemplateID: "tp_clean", BlobID: id, OwnerID: "pr_local"}}}}
	path := filepath.Join(dir, id+".tar")
	if err := os.WriteFile(path, payload, 0400); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/blobs/"+id+"/content", nil)
	response := httptest.NewRecorder()
	s.serveExport(response, request, "req_download")
	if response.Code != 200 || response.Body.String() != string(payload) {
		t.Fatalf("clean export not delivered: %d", response.Code)
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("tampered bundle"), 0400); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	s.serveExport(response, request, "req_tampered")
	if response.Code != 503 || strings.Contains(response.Body.String(), "tampered bundle") {
		t.Fatal("tampered export was delivered")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(dir, "private-capture")
	if err := os.WriteFile(private, []byte("private subscription capture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(private, path); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	s.serveExport(response, request, "req_substituted")
	if response.Code != 503 || strings.Contains(response.Body.String(), "private subscription capture") {
		t.Fatal("export followed a substituted private capture path")
	}
}

func TestSubscriptionCommandDataIsRejectedBeforeJournalAdmission(t *testing.T) {
	nonce := strings.Repeat("a", 32)
	for _, argv := range [][]string{{"/bin/true"}, {"/usr/local/bin/lamp-subscription", "sign-in"}, {"/usr/local/bin/lamp-subscription", "init", nonce}} {
		if !admittedSubscriptionExec(api.Exec{Argv: &argv}) {
			t.Fatalf("installed operation refused: %v", argv[:1])
		}
	}
	for _, argv := range [][]string{
		{"/bin/sh", "-c", "private-bearer"},
		{"/usr/local/bin/lamp-subscription", "sign-in", "private-bearer"},
		{"/usr/local/bin/lamp-subscription", "init", "private-bearer"},
		{"/usr/local/bin/lamp-subscription", "daemon"},
	} {
		if admittedSubscriptionExec(api.Exec{Argv: &argv}) {
			t.Fatal("unbounded command data admitted")
		}
	}
	argv := []string{"/usr/local/bin/lamp-subscription", "sign-in"}
	env := api.StringMap{"ANTHROPIC_API_KEY": "private-bearer"}
	if admittedSubscriptionExec(api.Exec{Argv: &argv, Env: &env}) {
		t.Fatal("credential environment admitted")
	}
}

func TestCleanProjectionAndPrivateSnapshotSurviveJournalReopen(t *testing.T) {
	config := storeConfig(t)
	service, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	service.journal.Snapshots["ss_private"] = savedSnapshot{Snapshot: api.Snapshot{Id: "ss_private", CaptureTrust: pointer(api.SnapshotCaptureTrustOwnerPrivate)}, CaptureID: "capture_private"}
	service.journal.Templates["tp_clean"] = cleanExport{TemplateID: "tp_clean", BlobID: "bl_clean", OwnerID: "pr_local"}
	service.journal.Shares["sh_handoff"] = api.Share{Id: "sh_handoff", State: "active", SubjectId: pointer("pr_reader"), ResourceKind: pointer("template"), ResourceId: pointer("tp_clean")}
	if err := service.save("ss_private", "tp_clean", "sh_handoff"); err != nil {
		t.Fatal(err)
	}
	service.Close()
	service, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if service.journal.Snapshots["ss_private"].CaptureID != "capture_private" || value(service.journal.Snapshots["ss_private"].Snapshot.CaptureTrust) != "owner_private" || service.journal.Templates["tp_clean"].BlobID != "bl_clean" || value(service.journal.Shares["sh_handoff"].ResourceId) != "tp_clean" {
		t.Fatal("custody projection changed across committed replay")
	}
}
