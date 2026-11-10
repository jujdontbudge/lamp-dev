package control

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JohanSardar/lamp/pkg/api"
)

func storeConfig(t *testing.T) Config {
	t.Helper()
	return Config{StateDir: filepath.Join(t.TempDir(), "state"), Worker: WorkerConfig{Socket: "/unused", WorkerID: "w", BootID: "b", GrantID: "g", UID: os.Getuid(), PID: os.Getpid(), Timeout: time.Second}}
}

func TestAppendJournalGateScaleReplayAndCompaction(t *testing.T) {
	config := storeConfig(t)
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	// Cross the old quota and the compaction boundary in one daemon lifetime.
	for i := 0; i < 20000; i++ {
		id := fmt.Sprintf("op_%d", i)
		s.journal.Operations[id] = &record{Key: id, Hash: id, Operation: api.Operation{Id: id, State: "succeeded"}}
		if err := s.save(id); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := os.Stat(filepath.Join(config.StateDir, "journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Size() < 1000 || s.logRecords != 20000-compactEvery {
		t.Fatal("compaction did not run")
	}
	// An ordinary append must contain just the changed record, never all history.
	before, _ := os.Stat(filepath.Join(config.StateDir, "journal.log"))
	s.journal.Operations["op_last"] = &record{Key: "last", Hash: "hash", Operation: api.Operation{Id: "op_last", State: "accepted"}}
	if err := s.save("op_last"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(filepath.Join(config.StateDir, "journal.log"))
	if after.Size()-before.Size() > 1024 {
		t.Fatal("append grew with retained history")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// Unrecognized bytes after the last commit are refused, never dropped:
	// they cannot be told apart from a damaged acknowledged record.
	logPath := filepath.Join(config.StateDir, "journal.log")
	clean, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(logPath, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`J1 00000000 {"version":1,"sequence":`)
	f.Close()
	if s, err := Open(config); err == nil {
		s.Close()
		t.Fatal("partial bytes after the last commit were dropped")
	}
	// Once the operator removes them, all acknowledged history is intact.
	writeLog(t, config, clean)
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if len(s.journal.Operations) != 20001 || s.keys["last"] != "op_last" || s.keys["op_0"] != "op_0" {
		t.Fatal("history or key index lost on replay")
	}
	if s.journal.Operations["op_last"].Operation.State != "outcome_unknown" {
		t.Fatal("restart replayed incomplete admission")
	}
	if s.journal.Operations["op_19999"].Operation.State != "succeeded" {
		t.Fatal("terminal history changed")
	}
}

func TestJournalReplaySnapshotBeforeLogTruncation(t *testing.T) {
	config := storeConfig(t)
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	s.journal.Sandboxes["sb_deleted"] = api.Sandbox{Id: "sb_deleted", State: "deleted"}
	if err := s.save("sb_deleted"); err != nil {
		t.Fatal(err)
	}
	oldLog, err := os.ReadFile(filepath.Join(config.StateDir, "journal.log"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.compact(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	// Simulate death after durable snapshot rename, before truncating its log.
	if err := os.WriteFile(filepath.Join(config.StateDir, "journal.log"), oldLog, 0600); err != nil {
		t.Fatal(err)
	}
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.journal.Sandboxes["sb_deleted"].State != "deleted" {
		t.Fatal("deleted sandbox resurrected")
	}
}

func TestJournalRejectsCompleteCorruptRecord(t *testing.T) {
	config := storeConfig(t)
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if err := os.WriteFile(filepath.Join(config.StateDir, "journal.log"), []byte("corrupt\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(config); err == nil {
		s.Close()
		t.Fatal("corrupt committed log accepted")
	}
}

// journalWithCommits opens a store, makes n committed operations (op_0..),
// closes it without compaction and returns its config and the log bytes plus
// each record's start offset.
func journalWithCommits(t *testing.T, n int, outputBytes int) (Config, []byte, []int64) {
	t.Helper()
	config := storeConfig(t)
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	var starts []int64
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("op_%d", i)
		s.journal.Operations[id] = &record{Key: "key_" + id, Hash: id, Operation: api.Operation{Id: id, State: "succeeded"}}
		if outputBytes > 0 {
			s.journal.Outputs[id] = []api.Frame{{DataBase64: strings.Repeat("x", outputBytes)}}
		}
		starts = append(starts, s.logEnd)
		if err := s.save(id); err != nil {
			t.Fatal(err)
		}
	}
	starts = append(starts, s.logEnd)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(config.StateDir, "journal.log"))
	if err != nil {
		t.Fatal(err)
	}
	return config, data, starts
}

func writeLog(t *testing.T, config Config, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(config.StateDir, "journal.log"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func mustRefuse(t *testing.T, config Config, why string) {
	t.Helper()
	path := filepath.Join(config.StateDir, "journal.log")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(config)
	if err == nil {
		n := len(s.journal.Operations)
		s.Close()
		t.Fatalf("%s: damaged history accepted (%d operations)", why, n)
	}
	// Refusing must leave the damaged log in place for inspection, not
	// compact a partial state over it.
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("%s: log changed after refusal", why)
	}
}

// The review reproduction: two commits, one byte of the first damaged.
func TestJournalRefusesDamageBeforeLaterCommits(t *testing.T) {
	config, data, starts := journalWithCommits(t, 2, 0)
	damaged := append([]byte(nil), data...)
	damaged[starts[0]+20] ^= 0x01
	writeLog(t, config, damaged)
	mustRefuse(t, config, "flipped byte in first record")

	damaged = append([]byte(nil), data...)
	damaged[starts[0]+20] = 0
	writeLog(t, config, damaged)
	mustRefuse(t, config, "NUL byte in first record")

	damaged = append([]byte(nil), data...)
	damaged[starts[1]-1] = 'x' // first record's newline
	writeLog(t, config, damaged)
	mustRefuse(t, config, "lost newline between records")
}

// The final record was acknowledged too: damage to it is not a torn write.
func TestJournalRefusesDamagedFinalRecord(t *testing.T) {
	config, data, starts := journalWithCommits(t, 2, 0)
	for name, mutate := range map[string]func([]byte){
		"flipped byte": func(b []byte) { b[starts[1]+20] ^= 0x01 },
		"NUL byte":     func(b []byte) { b[starts[1]+20] = 0 },
		"lost newline": func(b []byte) { b[starts[2]-1] = 'x' },
		"garbage after the zero tail starts": func(b []byte) {
			copy(b[starts[2]+4096:], "J1 00000000 {}\n")
		},
	} {
		damaged := append([]byte(nil), data...)
		mutate(damaged)
		writeLog(t, config, damaged)
		mustRefuse(t, config, name)
	}
}

const sector = 512

// recordEndingAt returns a log whose first record's newline is at byte offset
// newline (the review case uses 512, the first byte of the second sector).
func recordEndingAt(t *testing.T, newline int64) (Config, []byte, []int64) {
	t.Helper()
	for pad := 0; pad < 2*sector; pad++ {
		config, data, starts := journalWithCommits(t, 1, pad)
		if starts[1]-1 == newline {
			return config, data, starts
		}
		if starts[1]-1 > newline {
			break
		}
	}
	t.Fatalf("no padding puts the newline at %d", newline)
	return Config{}, nil, nil
}

// The review reproduction: an acknowledged record whose newline is the first
// byte of a sector, with only that newline turned into NUL, must not reopen
// as an empty journal and compact its idempotency key away.
func TestJournalRefusesNULNewlineAtSectorBoundary(t *testing.T) {
	for _, newline := range []int64{sector - 1, sector, sector + 1} {
		config, data, starts := recordEndingAt(t, newline)
		damaged := append([]byte(nil), data...)
		damaged[starts[1]-1] = 0
		writeLog(t, config, damaged)
		mustRefuse(t, config, fmt.Sprintf("NUL newline at %d", newline))
		// Undamaged, the same log reopens with its key.
		writeLog(t, config, data)
		s, err := Open(config)
		if err != nil {
			t.Fatal(err)
		}
		if s.keys["key_op_0"] != "op_0" {
			t.Fatal("record lost")
		}
		s.Close()
	}
}

// Whole zeroed sectors look exactly like an interrupted write. Wherever they
// fall, Open refuses rather than guessing that the record was unacknowledged.
func TestJournalRefusesSectorAlignedDamage(t *testing.T) {
	config, data, starts := journalWithCommits(t, 3, 3000)
	zero := func(b []byte, from, to int64) {
		for i := from; i < to; i++ {
			b[i] = 0
		}
	}
	firstFull := func(start int64) int64 { return (start/sector + 1) * sector }
	for name, mutate := range map[string]func([]byte) []byte{
		"final record's later sectors zero": func(b []byte) []byte {
			zero(b, firstFull(starts[2])+sector, starts[3])
			return b
		},
		"one middle sector of the final record zero": func(b []byte) []byte {
			at := firstFull(starts[2])
			zero(b, at, at+sector)
			return b
		},
		"one sector of an earlier record zero": func(b []byte) []byte {
			at := firstFull(starts[0])
			zero(b, at, at+sector)
			return b
		},
		"final record's first sector zero": func(b []byte) []byte {
			zero(b, starts[2], firstFull(starts[2]))
			return b
		},
		"file cut at a sector boundary inside the final record": func(b []byte) []byte {
			return b[:firstFull(starts[2])]
		},
	} {
		damaged := mutate(append([]byte(nil), data...))
		writeLog(t, config, damaged)
		mustRefuse(t, config, name)
	}
	// A commit none of whose bytes reached the disk is just zero tail.
	clean := append([]byte(nil), data...)
	zero(clean, starts[2], starts[3])
	writeLog(t, config, clean)
	s, err := Open(config)
	if err != nil {
		t.Fatalf("unwritten final commit refused: %v", err)
	}
	defer s.Close()
	if len(s.journal.Operations) != 2 || s.keys["key_op_1"] != "op_1" {
		t.Fatal("history lost")
	}
}
