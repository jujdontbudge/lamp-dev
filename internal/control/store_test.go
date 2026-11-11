package control

import (
	"github.com/JohanSardar/lamp/pkg/api"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func storeConfig(t *testing.T) Config {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "state")
	return Config{StateDir: dir, Worker: WorkerConfig{Socket: "/unused", WorkerID: "w", BootID: "b", GrantID: "g", UID: os.Getuid(), PID: os.Getpid(), Timeout: time.Second}}
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
