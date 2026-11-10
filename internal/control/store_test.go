package control

import (
	"fmt"
	"os"
	"path/filepath"
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
