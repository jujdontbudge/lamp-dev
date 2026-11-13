package control

import (
	"context"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"lamp.eilmer.si/pkg/api"
)

func TestT37UpdatedHandoffExpiryRevokesRecipient(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// An already completed clean handoff to an enrolled read-only peer.
		s := &Service{journal: journal{
			Control: &controlState{OwnerID: "pr_owner"},
			Shares: map[string]api.Share{"sh_handoff": {
				Id: "sh_handoff", OwnerId: "pr_owner", Revision: 2, State: "active",
				SubjectId: pointer("pr_reader"), ResourceKind: pointer("template"), ResourceId: pointer("tp_clean"),
			}},
			Templates: map[string]cleanExport{"tp_clean": {TemplateID: "tp_clean", BlobID: "bl_clean", OwnerID: "pr_owner"}},
		}}
		expiry := time.Now().Add(time.Minute).Format(time.RFC3339Nano)
		_, _, _, _, err := s.updateControlShare(caller{Principal: "pr_owner"}, map[string]any{
			"share_id": "sh_handoff", "expected_revision": 2, "expires_at": expiry,
		})
		if err != nil {
			t.Fatal(err)
		}
		read := func() int {
			r := httptest.NewRequest("GET", "/v1/templates/tp_clean", nil)
			r = r.WithContext(context.WithValue(r.Context(), peerContextKey{}, publicPeer{principal: "pr_reader"}))
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			return w.Code
		}
		if status := read(); status != 200 {
			t.Fatalf("before expiry: HTTP %d, want 200", status)
		}
		time.Sleep(2 * time.Minute) // Advances synctest's fake clock only.
		if status := read(); status == 200 {
			t.Fatalf("after accepted share expiry: recipient GET /v1/templates/tp_clean returned HTTP 200; want access denied")
		}
	})
}
