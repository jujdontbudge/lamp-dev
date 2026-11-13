package control

import (
	"encoding/json"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/hashicorp/raft"
	"lamp.eilmer.si/pkg/api"
)

func TestT38FleetGetWithStrandedSandbox(t *testing.T) {
	// Restore an ingress's last observation of a now-offline compute host.
	s := &Service{dir: t.TempDir()}
	if err := s.initAuthority(Config{}); err != nil {
		t.Fatal(err)
	}
	hosts := []FleetHost{{ID: "hs_ingress"}, {ID: "hs_compute"}}
	f := &Fleet{
		service: s, config: FleetConfig{HostID: "hs_ingress", Hosts: hosts},
		raft: &raft.Raft{}, peers: map[string]fleetPeer{},
		fsm:           &fleetFSM{state: fleetState{Control: s.journal.Control, Configuration: digest(hosts)}},
		controlDigest: digest(s.journal.Control),
		online:        map[string]bool{"hs_compute": false},
		observations: map[string]fleetObservation{"hs_compute": {
			Host: "hs_compute", Sandboxes: map[string]api.Sandbox{
				"sb_saved_h_hs_compute": {Id: "sb_saved_h_hs_compute", OwnerId: "pr_local", State: "suspended"},
			},
		}},
	}
	s.fleet = f
	f.activate(time.Now().Add(time.Hour))
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		f.ServeHTTP(response, httptest.NewRequest("GET", "/v1/fleets/fl_local", nil))
		close(done)
	}()
	select {
	case <-done:
		var out struct {
			Data api.Fleet `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if response.Code != 200 || out.Data.StrandedSandboxIds == nil || len(*out.Data.StrandedSandboxIds) != 1 {
			t.Fatalf("expected stranded sandbox, got %d %s", response.Code, response.Body.String())
		}
	case <-time.After(time.Second):
		stack := make([]byte, 16384)
		n := runtime.Stack(stack, true)
		t.Fatalf("fleet get deadlocked while reporting a stranded sandbox:\n%s", stack[:n])
	}
}
