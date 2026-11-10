package control

import (
	"encoding/json"
	"errors"
	"io"
	"sync"

	"github.com/hashicorp/raft"
)

// Only authority and retry-home metadata enter consensus. Worker journals,
// outputs and guest bytes remain at their immutable home.
type fleetState struct {
	Control       *controlState      `json:"control,omitempty"`
	Records       map[string]*record `json:"records"`
	Homes         map[string]string  `json:"homes"`
	Epoch         uint64             `json:"epoch"`
	ExpiresAt     string             `json:"expires_at"`
	Configuration string             `json:"configuration"`
}
type fleetCommand struct {
	RenewExpiresAt string             `json:"renew_expires_at,omitempty"`
	Expected       string             `json:"expected,omitempty"`
	Control        *controlState      `json:"control,omitempty"`
	Records        map[string]*record `json:"records,omitempty"`
	HomeKey        string             `json:"home_key,omitempty"`
	Home           string             `json:"home,omitempty"`
	Genesis        *fleetState        `json:"genesis,omitempty"`
}
type fleetFSM struct {
	mu    sync.Mutex
	state fleetState
}

// A definite CAS rejection precedes any local journal write or dispatch. It is
// safe to discard the staged mutation and let the caller retry after projection.
var errAuthorityConflict = errors.New("authority revision changed")

func (f *fleetFSM) snapshot() fleetState {
	f.mu.Lock()
	defer f.mu.Unlock()
	raw, _ := json.Marshal(f.state)
	var state fleetState
	_ = json.Unmarshal(raw, &state)
	return state
}
func (f *fleetFSM) Apply(log *raft.Log) any {
	var command fleetCommand
	if err := json.Unmarshal(log.Data, &command); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if command.Genesis != nil {
		if f.state.Control != nil {
			return errors.New("fleet already initialized")
		}
		f.state = *command.Genesis
		return nil
	}
	if f.state.Control == nil {
		return errors.New("fleet not initialized")
	}
	if command.RenewExpiresAt != "" {
		f.state.ExpiresAt = command.RenewExpiresAt
	}
	if command.Control != nil {
		if command.Expected != digest(f.state.Control) {
			return errAuthorityConflict
		}
		f.state.Control = command.Control
		for id, rec := range command.Records {
			f.state.Records[id] = rec
		}
	}
	if command.HomeKey != "" {
		if old, ok := f.state.Homes[command.HomeKey]; ok && old != command.Home {
			return errors.New("retry home already assigned")
		}
		if len(f.state.Homes) >= maxOperations && f.state.Homes[command.HomeKey] == "" {
			return errors.New("retry home retention limit reached")
		}
		f.state.Homes[command.HomeKey] = command.Home
	}
	return nil
}

type fleetSnapshot struct{ raw []byte }

func (f *fleetFSM) Snapshot() (raft.FSMSnapshot, error) {
	raw, err := json.Marshal(f.snapshot())
	return &fleetSnapshot{raw}, err
}
func (s *fleetSnapshot) Persist(sink raft.SnapshotSink) error {
	if _, err := sink.Write(s.raw); err != nil {
		_ = sink.Cancel()
		return err
	}
	return sink.Close()
}
func (s *fleetSnapshot) Release() {}
func (f *fleetFSM) Restore(reader io.ReadCloser) error {
	defer reader.Close()
	var state fleetState
	if err := json.NewDecoder(reader).Decode(&state); err != nil {
		return err
	}
	if state.Control == nil || state.Homes == nil || state.Records == nil {
		return errors.New("invalid fleet snapshot")
	}
	f.mu.Lock()
	f.state = state
	f.mu.Unlock()
	return nil
}
