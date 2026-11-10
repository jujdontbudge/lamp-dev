package control

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/JohanSardar/lamp/pkg/api"
	"golang.org/x/sys/unix"
)

const maxOperations = 1024

type record struct {
	Operation api.Operation   `json:"operation"`
	Hash      string          `json:"hash"`
	Key       string          `json:"key"`
	Resource  api.ResourceRef `json:"resource"`
}
type journal struct {
	Version    int                    `json:"version"`
	Operations map[string]*record     `json:"operations"`
	Sandboxes  map[string]api.Sandbox `json:"sandboxes"`
	Execs      map[string]api.Exec    `json:"execs"`
}
type Config struct {
	StateDir string
	Worker   WorkerConfig
}
type Service struct {
	mu          sync.Mutex
	journal     journal
	dir         string
	lock        *os.File
	worker      workerClient
	rootContext context.Context
	done        map[string]chan struct{}
	busy        map[string]bool
	wg          sync.WaitGroup
	cancel      func()
	broken      bool
}

func pointer[T any](v T) *T { return &v }
func timestamp() string     { return time.Now().UTC().Format(time.RFC3339Nano) }
func newID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}

// PrivateDir rejects permissive or substituted final directories. The caller
// chooses a trusted parent; neither socket nor state files are public endpoints.
func PrivateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 || int(st.Uid) != os.Geteuid() {
		return errors.New("directory must be owned by caller with mode 0700")
	}
	return nil
}
func Open(config Config) (*Service, error) {
	w := config.Worker
	if w.Socket == "" || w.WorkerID == "" || w.BootID == "" || w.GrantID == "" || w.UID < 0 || w.PID <= 0 {
		return nil, errors.New("assigned worker socket, identity, boot, UID, PID and grant are required")
	}
	if w.Timeout == 0 {
		w.Timeout = 30 * time.Second
	}
	if w.Timeout < time.Millisecond || w.Timeout > time.Minute {
		return nil, errors.New("worker timeout must be between 1ms and 1m")
	}
	if err := PrivateDir(config.StateDir); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(config.StateDir, "lock"), os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("state directory is already in use: %w", err)
	}
	s := &Service{dir: config.StateDir, lock: lock, worker: workerClient{config: w}, done: map[string]chan struct{}{}, busy: map[string]bool{}}
	s.journal = journal{1, map[string]*record{}, map[string]api.Sandbox{}, map[string]api.Exec{}}
	path := filepath.Join(s.dir, "journal.json")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		lock.Close()
		return nil, err
	}
	if err == nil {
		if err := json.Unmarshal(data, &s.journal); err != nil {
			lock.Close()
			return nil, fmt.Errorf("invalid operation journal: %w", err)
		}
		if s.journal.Version != 1 || s.journal.Operations == nil || s.journal.Sandboxes == nil || s.journal.Execs == nil {
			lock.Close()
			return nil, errors.New("invalid operation journal version or maps")
		}
		for id, r := range s.journal.Operations {
			ch := make(chan struct{})
			close(ch)
			s.done[id] = ch
			if !terminal(r.Operation.State) {
				r.Operation.State = "outcome_unknown"
				r.Operation.Error = &api.ErrorDetail{Code: "OUTCOME_UNKNOWN", Message: "daemon restarted before terminal evidence; effect was not replayed", OperationId: pointer(id)}
				r.Operation.CompletedAt = pointer(timestamp())
				r.Operation.CleanupState = pointer(api.OperationCleanupStatePending)
				r.Operation.Revision++
				r.Operation.UpdatedAt = timestamp()
			}
		}
		// Reconciliation is T10. Never assert live engine readiness after a daemon
		// restart merely because a previous process persisted a successful operation.
		for id, sb := range s.journal.Sandboxes {
			if sb.State != "failed" {
				sb.State = "unavailable"
				sb.Revision++
				sb.UpdatedAt = timestamp()
				s.journal.Sandboxes[id] = sb
			}
		}
		for id, ex := range s.journal.Execs {
			if ex.State == "running" || ex.State == "accepted" {
				ex.State = "outcome_unknown"
				ex.Revision++
				ex.UpdatedAt = timestamp()
				s.journal.Execs[id] = ex
			}
		}
		if err := s.save(); err != nil {
			lock.Close()
			return nil, err
		}
	}
	s.rootContext, s.cancel = context.WithCancel(context.Background())
	return s, nil
}
func (s *Service) context() context.Context { return s.rootContext }
func terminal(state api.OperationState) bool {
	return state == "succeeded" || state == "failed" || state == "outcome_unknown" || state == "canceled"
}

// save runs under mu. One atomic, fsynced journal commit precedes admission and
// another precedes publication of terminal evidence. No engine bytes live here.
func (s *Service) save() error {
	data, err := json.Marshal(s.journal)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.dir, ".journal-")
	if err != nil {
		return err
	}
	path := f.Name()
	defer os.Remove(path)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(path, filepath.Join(s.dir, "journal.json")); err != nil {
		return err
	}
	dir, err := os.Open(s.dir)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func (s *Service) Close() error {
	s.mu.Lock()
	s.broken = true
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	s.worker.close()
	s.wg.Wait()
	return s.lock.Close()
}
