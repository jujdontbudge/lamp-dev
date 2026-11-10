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
	// A partial final write must not hide earlier acknowledged commits.
	f, err := os.OpenFile(filepath.Join(config.StateDir, "journal.log"), os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"version":1,"sequence":`)
	f.Close()
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

// A commit cut off at a sector boundary before fdatasync returned was never
// acknowledged: replay keeps every earlier commit and drops only that one.
func TestJournalRecoversTornFinalCommit(t *testing.T) {
	config, data, starts := journalWithCommits(t, 3, 3000)
	torn := append([]byte(nil), data...)
	// Keep the third record's first sector-aligned chunk; zero the rest of it,
	// as if its later sectors never reached the disk.
	cut := (starts[2]/sectorSize + 2) * sectorSize
	if cut >= starts[3] {
		t.Fatal("record too small to tear")
	}
	for i := cut; i < starts[3]; i++ {
		torn[i] = 0
	}
	writeLog(t, config, torn)
	s, err := Open(config)
	if err != nil {
		t.Fatalf("torn final commit refused: %v", err)
	}
	if len(s.journal.Operations) != 2 || s.keys["key_op_0"] != "op_0" || s.keys["key_op_1"] != "op_1" {
		t.Fatalf("committed history lost: %d operations", len(s.journal.Operations))
	}
	// Recovery compacted; new commits after it replay normally.
	s.journal.Operations["op_new"] = &record{Key: "key_new", Hash: "h", Operation: api.Operation{Id: "op_new", State: "succeeded"}}
	if err := s.save("op_new"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if len(s.journal.Operations) != 3 || s.keys["key_new"] != "op_new" {
		t.Fatal("commit after torn-tail recovery lost")
	}
}

// A sector torn out of the middle of the final record is still a torn write.
func TestJournalRecoversFinalCommitWithMissingMiddleSector(t *testing.T) {
	config, data, starts := journalWithCommits(t, 2, 3000)
	torn := append([]byte(nil), data...)
	sector := (starts[1]/sectorSize + 2) * sectorSize
	for i := sector; i < sector+sectorSize; i++ {
		torn[i] = 0
	}
	writeLog(t, config, torn)
	s, err := Open(config)
	if err != nil {
		t.Fatalf("torn final commit refused: %v", err)
	}
	defer s.Close()
	if len(s.journal.Operations) != 1 || s.keys["key_op_0"] != "op_0" {
		t.Fatal("committed history lost")
	}
}
