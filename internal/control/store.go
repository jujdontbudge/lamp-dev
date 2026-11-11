package control

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/JohanSardar/lamp/pkg/api"
	"golang.org/x/sys/unix"
)

const maxOperations = 1_000_000
const compactEvery = 16384

type record struct {
	Caller    caller          `json:"caller,omitempty"`
	Operation api.Operation   `json:"operation"`
	Hash      string          `json:"hash"`
	Key       string          `json:"key"`
	Resource  api.ResourceRef `json:"resource"`
	Custody   *custodyPlan    `json:"custody,omitempty"`
}
type journal struct {
	EventSequence uint64        `json:"event_sequence,omitempty"`
	EventFloor    uint64        `json:"event_floor,omitempty"`
	Control       *controlState `json:"control,omitempty"`
	Events        []storedEvent `json:"events,omitempty"`
	Sequence      uint64        `json:"sequence,omitempty"`
	// LogGeneration names the journal.log instance that follows this
	// snapshot; every compaction starts a new one (see commitMark).
	LogGeneration uint64                   `json:"log_generation,omitempty"`
	Outputs       map[string][]api.Frame   `json:"outputs,omitempty"`
	Version       int                      `json:"version"`
	Operations    map[string]*record       `json:"operations"`
	Sandboxes     map[string]api.Sandbox   `json:"sandboxes"`
	Execs         map[string]api.Exec      `json:"execs"`
	Snapshots     map[string]savedSnapshot `json:"snapshots,omitempty"`
	Shares        map[string]api.Share     `json:"shares,omitempty"`
	Templates     map[string]cleanExport   `json:"templates,omitempty"`
}
type Config struct {
	Fleet            *FleetConfig
	StateDir         string
	Worker           WorkerConfig
	Identity         *IdentityConfig
	ProtectedActions map[string]ProtectedAction
	ExportsDir       string
	SharedReaders    map[uint32]string
	Sessions         SessionConfig
}
type Service struct {
	fleet          *Fleet
	sessions       SessionConfig
	sessionFleet   bool
	mu             sync.Mutex
	committing     bool
	pending        []*pendingCommit
	durableOps     map[string]*record
	commitChanged  chan struct{}
	identity       IdentityConfig
	protected      map[string]ProtectedAction
	eventRevisions map[string]uint64
	journal        journal
	dir            string
	exportsDir     string
	readers        map[uint32]string
	lock           *os.File
	worker         workerClient
	rootContext    context.Context
	done           map[string]chan struct{}
	busy           map[string]bool
	starting       map[string]int
	wg             sync.WaitGroup
	cancel         func()
	broken         bool
	keys           map[string]string
	logRecords     int
	// changed is closed (and replaced) whenever a journal change becomes
	// durable, waking long-polling readers instead of having them spin.
	changed chan struct{}
	// log is journal.log, physically reserved past logEnd. Linux reserves
	// unwritten extents; other platforms initialize real zero bytes. Each
	// commit fdatasyncs record data and required retrieval metadata (see save).
	log    *os.File
	logEnd int64
	logCap int64
	// mark is journal.commit: the durable committed end of log (see
	// commitMark). A commit is acknowledged only once mark covers it.
	mark *os.File
}

// logPrealloc is how far journal.log is extended with zeros at a time.
const logPrealloc = 64 << 20

// extendLog reserves physical capacity up to at least need bytes and makes
// the new size durable. Called under mu.
func (s *Service) extendLog(need int64) error {
	end := s.logCap
	for end < need || end < s.logEnd+logPrealloc/2 {
		end += 1 << 20
	}
	return s.reserveLogEnd(end)
}

func (s *Service) reserveLogEnd(end int64) error {
	if reserved, err := reserveLog(s.log, end); reserved || err != nil {
		if err != nil {
			return err
		}
		s.logCap = end
		return s.log.Sync()
	}
	zeros := make([]byte, 1<<20)
	for s.logCap < end {
		if _, err := s.log.WriteAt(zeros, s.logCap); err != nil {
			return err
		}
		s.logCap += int64(len(zeros))
	}
	return s.log.Sync()
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
	if config.Fleet == nil {
		if _, err := os.Stat(filepath.Join(config.StateDir, "fleet")); err == nil {
			return nil, errors.New("fleet state cannot be reopened without enrolled fleet configuration")
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	w := config.Worker
	if config.Fleet != nil && w.Socket == "" {
		w = WorkerConfig{Socket: "/unused-control-only", WorkerID: "control-only", BootID: "control-only", GrantID: "none", UID: os.Geteuid(), PID: os.Getpid()}
	}
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
	s := &Service{dir: config.StateDir, lock: lock, worker: workerClient{config: w}, sessionFleet: config.Fleet != nil, done: map[string]chan struct{}{}, busy: map[string]bool{}, starting: map[string]int{}, keys: map[string]string{}, changed: make(chan struct{})}
	s.journal = journal{Version: 1, Operations: map[string]*record{}, Sandboxes: map[string]api.Sandbox{}, Execs: map[string]api.Exec{}, Outputs: map[string][]api.Frame{}}
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
	}
	s.initCustody()
	s.exportsDir = config.ExportsDir
	if len(config.SharedReaders) > 32 {
		return nil, errors.New("at most 32 local share readers are supported")
	}
	s.readers = map[uint32]string{}
	for uid, principal := range config.SharedReaders {
		if int(uid) == os.Geteuid() || !validPrincipal(principal) {
			return nil, errors.New("share readers require another OS UID and a principal ID")
		}
		s.readers[uid] = principal
	}
	if s.journal.Outputs == nil {
		s.journal.Outputs = map[string][]api.Frame{}
	}
	// Replay framed, checksummed commits (see frameRecord and replayLog).
	logPath := filepath.Join(s.dir, "journal.log")
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW, 0600)
	if err != nil {
		lock.Close()
		return nil, err
	}
	// Only the bytes inside the durable committed boundary are history;
	// anything past it was never acknowledged (see commitMark).
	logData, err := io.ReadAll(log)
	var committed []byte
	var mark commitMark
	var haveMark bool
	if err == nil {
		// An unpublished marker left by a killed start is never the marker.
		err = removeCommitTemps(s.dir)
	}
	if err == nil {
		mark, haveMark, err = readCommitMark(s.dir)
	}
	if err == nil {
		committed, err = committedLog(logData, mark, haveMark, s.journal.LogGeneration)
	}
	if err == nil {
		err = replayLog(committed, func(delta journal) error {
			if delta.Version != 1 || delta.Sequence == 0 {
				return errors.New("invalid journal record")
			}
			if delta.Sequence <= s.journal.Sequence {
				return nil
			}
			if delta.Sequence != s.journal.Sequence+1 {
				return errors.New("journal sequence gap")
			}
			for id, rec := range delta.Operations {
				s.journal.Operations[id] = rec
			}
			for id, sb := range delta.Sandboxes {
				s.journal.Sandboxes[id] = sb
			}
			for id, ex := range delta.Execs {
				s.journal.Execs[id] = ex
			}
			for id, frames := range delta.Outputs {
				s.journal.Outputs[id] = frames
			}
			for id, snapshot := range delta.Snapshots {
				s.journal.Snapshots[id] = snapshot
			}
			for id, share := range delta.Shares {
				s.journal.Shares[id] = share
			}
			for id, template := range delta.Templates {
				s.journal.Templates[id] = template
			}
			if delta.Control != nil {
				s.journal.Control = delta.Control
			}
			s.journal.Events = append(s.journal.Events, delta.Events...)
			s.journal.EventSequence = delta.EventSequence
			s.journal.Sequence = delta.Sequence
			s.logRecords++
			return nil
		})
	}
	if err == nil && haveMark && mark.Generation == s.journal.LogGeneration && s.journal.Sequence != mark.Sequence {
		err = fmt.Errorf("committed log ends at sequence %d, commit marker says %d", s.journal.Sequence, mark.Sequence)
	}
	if err == nil {
		err = log.Sync()
	}
	log.Close()
	if err != nil {
		lock.Close()
		return nil, fmt.Errorf("invalid operation log: %w", err)
	}
	if err := syncDirectory(s.dir); err != nil {
		lock.Close()
		return nil, err
	}
	if err := s.initAuthority(config); err != nil {
		lock.Close()
		return nil, err
	}
	if err := s.initSessions(config.Sessions); err != nil {
		lock.Close()
		return nil, err
	}
	s.protected = config.ProtectedActions
	s.eventRevisions = map[string]uint64{}
	for _, event := range s.journal.Events {
		if event.Event.Sequence > s.journal.EventSequence {
			s.journal.EventSequence = event.Event.Sequence
		}
	}
	s.indexEventRevisions()
	{
		for id, r := range s.journal.Operations {
			if strings.Count(r.Key, "\n") == 1 {
				name, _, _ := strings.Cut(r.Key, "\n")
				if name == "sandbox_create" || name == "sandbox_fork" || name == "sandbox_delete" || name == "exec_create" || name == "snapshot_create" || name == "share_create" || name == "sandbox_suspend" || name == "sandbox_resume" {
					r.Key = r.Operation.OwnerId + "\n" + r.Key
				}
			}
			s.keys[r.Key] = id
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
		// Reconcile running guests before declaring them ready again. A durable
		// suspension stays suspended; resume validates its capture with the worker.
		for id, sb := range s.journal.Sandboxes {
			if sb.State != "failed" && sb.State != "deleted" && sb.State != "suspended" {
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
		recovery := journal{Control: s.journal.Control, Operations: s.journal.Operations, Sandboxes: s.journal.Sandboxes, Execs: s.journal.Execs}
		s.addEvents(&recovery)
		s.journal.Events = append(s.journal.Events, recovery.Events...)
		s.journal.EventSequence = recovery.EventSequence
		s.pruneEvents()
		if err := s.compact(); err != nil {
			lock.Close()
			return nil, err
		}
	}
	s.durableOps = cloneOperations(s.journal.Operations)
	s.rootContext, s.cancel = context.WithCancel(context.Background())
	s.wg.Add(1)
	go s.driveSessions()
	for _, rec := range s.journal.Operations {
		if rec.Operation.State == "succeeded" && rec.Resource.Kind == "sandbox" && (value(rec.Operation.OperationName) == "sandbox_create" || value(rec.Operation.OperationName) == "sandbox_fork") {
			s.wg.Add(1)
			go s.reconcileSandbox(rec.Resource.Id, rec.Operation.Id)
		}
	}
	return s, nil
}
func (s *Service) context() context.Context { return s.rootContext }
func terminal(state api.OperationState) bool {
	return state == "succeeded" || state == "failed" || state == "outcome_unknown" || state == "canceled"
}

// save runs under mu. Only changed records are appended and fsynced before
// dispatch/publication. IDs explicitly name the bounded mutation write set.
func (s *Service) save(ids ...string) error {
	if s.broken {
		return errors.New("journal unavailable")
	}
	if s.fleet != nil {
		if err := s.fleet.commitControl(ids); err != nil {
			return err
		}
	}
	payload, delta, err := s.prepareDelta(ids...)
	if err != nil {
		return err
	}
	data := frameRecord(payload)
	// The held descriptor must still be the state directory's journal.log: a
	// moved or replaced state directory would otherwise take commits that a
	// restart can never find.
	named, err := os.Stat(filepath.Join(s.dir, "journal.log"))
	if err != nil {
		return err
	}
	held, err := s.log.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(named, held) {
		return errors.New("journal.log was moved or replaced")
	}
	if end := s.logEnd + int64(len(data)); end > s.logCap {
		if err := s.extendLog(end); err != nil {
			return err
		}
	}
	// An in-place overwrite of initialized blocks: fdatasync makes it
	// durable without waiting for unrelated metadata on the same disk.
	if _, err := writeRecordAt(s.log, data, s.logEnd); err != nil {
		return err
	}
	if err := datasync(s.log); err != nil {
		return err
	}
	// Publish the new committed end. Until this is durable the record is
	// not acknowledged, and a restart discards it.
	if err := s.writeCommitMark(commitMark{Generation: s.journal.LogGeneration, End: s.logEnd + int64(len(data)), Sequence: delta.Sequence}); err != nil {
		return err
	}
	if s.fleet != nil {
		if err := s.fleet.watermark(delta.Sequence); err != nil {
			return err
		}
	}
	s.publishOperations(delta.Operations)
	s.logEnd += int64(len(data))
	s.journal.Sequence = delta.Sequence
	s.journal.Events = append(s.journal.Events, delta.Events...)
	s.journal.EventSequence = delta.EventSequence
	s.pruneEvents()
	s.logRecords++
	close(s.changed)
	s.changed = make(chan struct{})
	if s.logRecords >= compactEvery {
		return s.compact()
	}
	return nil
}

// journal.log framing. Each commit is one line
//
//	J1 <crc32c of payload, 8 hex> <JSON payload>\n
//
// written in place over preallocated zero bytes and made durable with
// fdatasync before the next commit starts. JSON never contains a raw NUL or
// newline, so a committed record contains neither.
const recordMagic = "J1 "

var crcTable = crc32.MakeTable(crc32.Castagnoli)

func frameRecord(payload []byte) []byte {
	out := make([]byte, 0, len(recordMagic)+9+len(payload)+1)
	out = append(out, recordMagic...)
	out = fmt.Appendf(out, "%08x ", crc32.Checksum(payload, crcTable))
	out = append(out, payload...)
	return append(out, '\n')
}

// parseRecord validates one line (without its newline) and returns its payload.
func parseRecord(line []byte) ([]byte, bool) {
	header := len(recordMagic) + 9
	if len(line) <= header || string(line[:len(recordMagic)]) != recordMagic || line[header-1] != ' ' {
		return nil, false
	}
	var sum uint32
	if _, err := fmt.Sscanf(string(line[len(recordMagic):header-1]), "%08x", &sum); err != nil {
		return nil, false
	}
	payload := line[header:]
	if crc32.Checksum(payload, crcTable) != sum {
		return nil, false
	}
	return payload, true
}

func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

// writeRecordAt is the record write; tests replace it to inject a write that
// a dying process left partly done.
var writeRecordAt = func(f *os.File, data []byte, off int64) (int, error) { return f.WriteAt(data, off) }

// journal.commit holds one 512-byte sector, rewritten in place and
// fdatasynced after each record is durable and before it is acknowledged:
//
//	JC1 <generation> <committed end> <sequence> <crc32c of the preceding text, 8 hex>\n
//
// then zero padding. A process that dies at any point leaves either the old
// or the new marker: the write is under one page, and Linux's buffered write
// only stops between pages. Record bytes past the committed end were never
// acknowledged, whether a SIGKILL cut their write short or not, so replay
// discards them. Inside the end every byte must be valid history: damage
// there fails closed. The generation binds the marker to one journal.log
// instance: compaction writes a snapshot with a new generation, then resets
// the log and the marker; a marker older than the snapshot means the log
// only holds history the snapshot already contains.
type commitMark struct {
	Generation uint64
	End        int64
	Sequence   uint64
}

const markSize = 512

func (m commitMark) encode() []byte {
	text := fmt.Sprintf("JC1 %d %d %d", m.Generation, m.End, m.Sequence)
	out := make([]byte, markSize)
	copy(out, fmt.Sprintf("%s %08x\n", text, crc32.Checksum([]byte(text), crcTable)))
	return out
}

func readCommitMark(dir string) (commitMark, bool, error) {
	data, err := os.ReadFile(filepath.Join(dir, "journal.commit"))
	if os.IsNotExist(err) {
		return commitMark{}, false, nil
	}
	if err != nil {
		return commitMark{}, false, err
	}
	nl := bytes.IndexByte(data, '\n')
	if len(data) != markSize || nl < 0 || !allZero(data[nl+1:]) {
		return commitMark{}, false, errors.New("damaged commit marker")
	}
	line := string(data[:nl])
	cut := len(line) - 9
	var m commitMark
	var sum uint32
	if cut < 0 || line[cut] != ' ' {
		return commitMark{}, false, errors.New("damaged commit marker")
	}
	if _, err := fmt.Sscanf(line[cut+1:], "%08x", &sum); err != nil || crc32.Checksum([]byte(line[:cut]), crcTable) != sum {
		return commitMark{}, false, errors.New("damaged commit marker")
	}
	if n, err := fmt.Sscanf(line[:cut], "JC1 %d %d %d", &m.Generation, &m.End, &m.Sequence); err != nil || n != 3 || m.End < 0 {
		return commitMark{}, false, errors.New("damaged commit marker")
	}
	return m, true, nil
}

// writeCommitMark makes m durable. Called under mu.
func (s *Service) writeCommitMark(m commitMark) error {
	if _, err := s.mark.WriteAt(m.encode(), 0); err != nil {
		return err
	}
	return datasync(s.mark)
}

// committedLog returns the bytes of journal.log that are acknowledged history.
func committedLog(log []byte, m commitMark, haveMark bool, generation uint64) ([]byte, error) {
	switch {
	case !haveMark:
		// Before the first marker (a new or legacy state directory) no
		// record was ever acknowledged through this log.
		if !allZero(log) {
			return nil, errors.New("journal.log has records but no commit marker")
		}
		return nil, nil
	case m.Generation < generation:
		return nil, nil // a log the newer snapshot already contains
	case m.Generation > generation:
		return nil, fmt.Errorf("commit marker generation %d is newer than the snapshot's %d", m.Generation, generation)
	case int64(len(log)) < m.End:
		return nil, fmt.Errorf("journal.log is %d bytes, shorter than its committed end %d", len(log), m.End)
	}
	return log[:m.End], nil
}

// replayLog applies the committed records in order. Every byte of committed
// must belong to a valid record: inside the committed boundary, damage of any
// kind fails closed rather than dropping acknowledged history.
func replayLog(committed []byte, apply func(journal) error) error {
	for pos := 0; pos < len(committed); {
		rest := committed[pos:]
		nl := bytes.IndexByte(rest, '\n')
		if nl < 0 {
			return fmt.Errorf("damaged committed journal record at offset %d", pos)
		}
		payload, ok := parseRecord(rest[:nl])
		if !ok {
			return fmt.Errorf("damaged committed journal record at offset %d", pos)
		}
		var delta journal
		if err := json.Unmarshal(payload, &delta); err != nil {
			return fmt.Errorf("journal record at offset %d: %w", pos, err)
		}
		if err := apply(delta); err != nil {
			return fmt.Errorf("journal record at offset %d: %w", pos, err)
		}
		pos += nl + 1
	}
	return nil
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// A sequence-stamped atomic snapshot makes replay safe even if a crash occurs
// between the rename and truncation of the old log. Legacy journal.json opens
// migrate here without losing existing operation/idempotency history.
func (s *Service) compact() error {
	// The snapshot names the next log generation; the old log and marker
	// become stale the moment it is renamed into place.
	s.journal.LogGeneration++
	renamed := false
	defer func() {
		if !renamed {
			s.journal.LogGeneration--
		}
	}()
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
	renamed = true
	if err = syncDirectory(s.dir); err != nil {
		return err
	}
	if s.log == nil {
		if s.log, err = os.OpenFile(filepath.Join(s.dir, "journal.log"), os.O_RDWR|os.O_CREATE|unix.O_NOFOLLOW, 0600); err != nil {
			return err
		}
	}
	if err = s.log.Truncate(0); err != nil {
		return err
	}
	s.logEnd, s.logCap = 0, 0
	if err = s.extendLog(logPrealloc); err != nil {
		return err
	}
	m := commitMark{Generation: s.journal.LogGeneration, Sequence: s.journal.Sequence}
	if s.mark == nil {
		if err = s.openCommitMark(m); err != nil {
			return err
		}
	} else if err = s.writeCommitMark(m); err != nil {
		return err
	}
	s.logRecords = 0
	return nil
}

// openCommitMark opens journal.commit for in-place updates and writes m. A
// marker that does not exist yet (first start, or migration from a snapshot
// alone) is published atomically: written and synced under a temporary name,
// renamed into place, and the directory synced. A process killed at any point
// leaves either no marker, which Open accepts only with an all-zero log, or a
// complete one; never an empty or partial journal.commit.
func (s *Service) openCommitMark(m commitMark) error {
	path := filepath.Join(s.dir, "journal.commit")
	f, err := os.OpenFile(path, os.O_RDWR|unix.O_NOFOLLOW, 0600)
	if err == nil {
		s.mark = f
		return s.writeCommitMark(m)
	}
	if !os.IsNotExist(err) {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, commitTempPrefix)
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	markerStage("created")
	if _, err = tmp.Write(m.encode()[:markSize/2]); err == nil {
		markerStage("half-written")
		_, err = tmp.Write(m.encode()[markSize/2:])
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		markerStage("synced")
		err = os.Rename(tmp.Name(), path)
	}
	if err == nil {
		markerStage("renamed")
		err = syncDirectory(s.dir)
	}
	if err != nil {
		return err
	}
	markerStage("published")
	s.mark, err = os.OpenFile(path, os.O_RDWR|unix.O_NOFOLLOW, 0600)
	return err
}

// markerStage marks the steps of publishing a new marker; crash tests replace
// it to kill a real process at each one. Nothing outside tests sets it.
var markerStage = func(string) {}

// commitTempPrefix names unpublished markers; Open removes any left behind.
const commitTempPrefix = ".commit-"

// removeCommitTemps lists dir literally: a state directory whose name holds
// glob metacharacters must neither break Open nor reach another directory.
func removeCommitTemps(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), commitTempPrefix) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) Close() error {
	s.readLock()
	s.broken = true
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	s.worker.close()
	s.wg.Wait()
	if s.log != nil {
		s.log.Close()
	}
	if s.mark != nil {
		s.mark.Close()
	}
	return s.lock.Close()
}

// prepareDelta snapshots the bounded write set under mu. The encoded bytes
// never alias the mutable journal while the group writer flushes them.
func (s *Service) prepareDelta(ids ...string) ([]byte, journal, error) {
	delta := journal{Version: 1, Sequence: s.journal.Sequence + 1, Operations: map[string]*record{}, Sandboxes: map[string]api.Sandbox{}, Execs: map[string]api.Exec{}, Outputs: map[string][]api.Frame{}}
	delta.Snapshots = map[string]savedSnapshot{}
	delta.Shares = map[string]api.Share{}
	delta.Templates = map[string]cleanExport{}
	for _, id := range ids {
		if rec, ok := s.journal.Operations[id]; ok {
			delta.Operations[id] = rec
		}
		if sb, ok := s.journal.Sandboxes[id]; ok {
			delta.Sandboxes[id] = sb
		}
		if ex, ok := s.journal.Execs[id]; ok {
			delta.Execs[id] = ex
		}
		if frames, ok := s.journal.Outputs[id]; ok {
			delta.Outputs[id] = frames
		}
		if snapshot, ok := s.journal.Snapshots[id]; ok {
			delta.Snapshots[id] = snapshot
		}
		if share, ok := s.journal.Shares[id]; ok {
			delta.Shares[id] = share
		}
		if template, ok := s.journal.Templates[id]; ok {
			delta.Templates[id] = template
		}
	}
	for _, id := range ids {
		if id == "$control" {
			delta.Control = s.journal.Control
		}
	}
	s.addEvents(&delta)
	payload, err := json.Marshal(delta)
	if err != nil {
		return nil, delta, err
	}
	return payload, delta, nil
}

type pendingCommit struct {
	operations map[string]*record
	data       []byte
	mark       commitMark
	done       chan struct{}
	err        error
}

// readLock never exposes a staged record. Writers may reserve disjoint work
// while a group is flushing, but readers and management transactions wait for
// the complete staged prefix. On any flush error the service fails closed.
func (s *Service) readLock() {
	s.mu.Lock()
	for s.committing {
		changed := s.commitChanged
		s.mu.Unlock()
		<-changed
		s.mu.Lock()
	}
}

// saveConcurrent has save's durable-return contract, but releases mu while
// waiting so concurrent admissions can share the two ordered sync barriers.
// Callers must not retain mutable map entries across this call. Only the engine
// write paths use it; management and protected actions take readLock first.
func (s *Service) saveConcurrent(ids ...string) error {
	if s.broken {
		return errors.New("journal unavailable")
	}
	payload, delta, err := s.prepareDelta(ids...)
	if err != nil {
		return err
	}
	entry := &pendingCommit{data: frameRecord(payload), operations: cloneOperations(delta.Operations), done: make(chan struct{})}
	s.logEnd += int64(len(entry.data))
	s.journal.Sequence = delta.Sequence
	s.journal.Events = append(s.journal.Events, delta.Events...)
	s.journal.EventSequence = delta.EventSequence
	s.logRecords++
	entry.mark = commitMark{Generation: s.journal.LogGeneration, End: s.logEnd, Sequence: delta.Sequence}
	s.pending = append(s.pending, entry)
	if !s.committing {
		s.committing = true
		s.commitChanged = make(chan struct{})
		go s.flushGroups()
	}
	s.mu.Unlock()
	<-entry.done
	s.mu.Lock()
	return entry.err
}

func (s *Service) flushGroups() {
	// A bounded coalescing window lets simultaneous callers share a barrier.
	time.Sleep(200 * time.Microsecond)
	for {
		s.mu.Lock()
		batch := s.pending
		s.pending = nil
		end := batch[len(batch)-1].mark
		data := []byte{}
		for _, entry := range batch {
			data = append(data, entry.data...)
		}
		off := end.End - int64(len(data))
		// Only this writer touches log/mark until committing becomes false.
		// Extending is outside the caller mutex too.
		s.mu.Unlock()
		named, err := os.Stat(filepath.Join(s.dir, "journal.log"))
		var held os.FileInfo
		if err == nil {
			held, err = s.log.Stat()
		}
		if err == nil && !os.SameFile(named, held) {
			err = errors.New("journal.log was moved or replaced")
		}
		if err == nil && end.End > s.logCap {
			err = s.extendGroupLog(end.End)
		}
		if err == nil {
			_, err = writeRecordAt(s.log, data, off)
		}
		if err == nil {
			err = datasync(s.log)
		}
		if err == nil {
			err = s.writeCommitMark(end)
		}
		s.mu.Lock()
		if err == nil && s.fleet != nil {
			err = s.fleet.watermark(end.Sequence)
		}
		if err != nil {
			s.broken = true
			batch = append(batch, s.pending...)
			s.pending = nil
		}
		// Compact only when the entire staged prefix is durable. Snapshot
		// publication and log-generation changes cannot race the writer.
		if err == nil && len(s.pending) == 0 && s.logRecords >= compactEvery {
			err = s.compact()
			if err != nil {
				s.broken = true
			}
		}
		for _, entry := range batch {
			if err == nil {
				for id, rec := range entry.operations {
					s.durableOps[id] = rec
				}
			}
			entry.err = err
			close(entry.done)
		}
		close(s.changed)
		s.changed = make(chan struct{})
		if len(s.pending) == 0 {
			s.pruneEvents()
			s.committing = false
			close(s.commitChanged)
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
	}
}

// Unlike extendLog, the reservation end can advance concurrently. This writer
// extends from its own batch boundary and never reads a mutable reservation.
func (s *Service) extendGroupLog(need int64) error {
	end := s.logCap
	for end < need+logPrealloc/2 {
		end += 1 << 20
	}
	return s.reserveLogEnd(end)
}

// publishOperations installs only immutable, committed response records. Engine
// replies can use their own durable result without waiting for unrelated output
// observations that have since joined another group.
func cloneOperations(records map[string]*record) map[string]*record {
	raw, _ := json.Marshal(records)
	var snapshot map[string]*record
	json.Unmarshal(raw, &snapshot)
	return snapshot
}
func (s *Service) publishOperations(records map[string]*record) {
	if s.durableOps == nil {
		s.durableOps = map[string]*record{}
	}
	for id, rec := range cloneOperations(records) {
		s.durableOps[id] = rec
	}
}

// Exec starts may coexist; fork/delete hold an exclusive lifecycle admission.
// The reservation remains held until the durable command-start result (or an
// explicit failure). Completion of one start cannot release another's fence.
func (s *Service) releaseAdmission(rec *record, source string) {
	if value(rec.Operation.OperationName) == "exec_create" {
		if s.starting[source] > 1 {
			s.starting[source]--
		} else {
			delete(s.starting, source)
		}
	} else {
		delete(s.busy, source)
	}
}
