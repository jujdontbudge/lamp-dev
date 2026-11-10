package control

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
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
	// Bytes past the committed end were never acknowledged (a write that a
	// dying process left partly done): replay discards them and keeps
	// every acknowledged commit.
	f, err := os.OpenFile(filepath.Join(config.StateDir, "journal.log"), os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`J1 00000000 {"version":1,"sequence":`)
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
	config, data, starts := journalWithCommits(t, 1, 0)
	damaged := append([]byte(nil), data...)
	copy(damaged[starts[0]:starts[1]-1], strings.Repeat("corrupt ", 64))
	writeLog(t, config, damaged)
	mustRefuse(t, config, "complete corrupt committed record")
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
		"record zeroed": func(b []byte) {
			for i := starts[1]; i < starts[2]; i++ {
				b[i] = 0
			}
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
// Output bytes are 'x', which JSON never escapes, so the record grows by
// exactly one byte per byte of padding: one probe gives the padding needed.
// Every store preallocates a 64 MiB log, so the probe is deleted at once.
func recordEndingAt(t *testing.T, newline int64) (Config, []byte, []int64) {
	t.Helper()
	probe, _, starts := journalWithCommits(t, 1, 1)
	removeStore(t, probe)
	pad := 1 + newline - (starts[1] - 1)
	if pad < 1 {
		t.Fatalf("a record cannot end as early as %d", newline)
	}
	config, data, starts := journalWithCommits(t, 1, int(pad))
	if starts[1]-1 != newline {
		t.Fatalf("newline at %d, want %d", starts[1]-1, newline)
	}
	return config, data, starts
}

// removeStore deletes a test store's directory (and its preallocated log)
// as soon as a case is done, instead of at the end of the test.
func removeStore(t *testing.T, config Config) {
	t.Helper()
	if err := os.RemoveAll(filepath.Dir(config.StateDir)); err != nil {
		t.Fatal(err)
	}
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
		removeStore(t, config)
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
	// Inside the committed end, even a fully zeroed final record is damage.
	clean := append([]byte(nil), data...)
	zero(clean, starts[2], starts[3])
	writeLog(t, config, clean)
	mustRefuse(t, config, "acknowledged final record zeroed")
}

func markerPath(config Config) string { return filepath.Join(config.StateDir, "journal.commit") }

// Bytes past the committed end, in any shape, are uncommitted and discarded.
func TestJournalDiscardsBytesPastTheCommittedEnd(t *testing.T) {
	config, data, starts := journalWithCommits(t, 2, 0)
	for name, tail := range map[string]string{
		"record prefix":        `J1 0bad0bad {"version":1,"sequence":3,"operations":{"op`,
		"complete record":      string(frameRecord([]byte(`{"version":1,"sequence":3,"operations":{}}`))),
		"garbage with newline": "garbage\n",
	} {
		changed := append([]byte(nil), data...)
		copy(changed[starts[2]:], tail)
		copy(changed[starts[2]+5000:], tail) // and further out
		writeLog(t, config, changed)
		s, err := Open(config)
		if err != nil {
			t.Fatalf("%s: restart refused: %v", name, err)
		}
		if len(s.journal.Operations) != 2 || s.keys["key_op_1"] != "op_1" || s.journal.Sequence != 2 {
			t.Fatalf("%s: acknowledged history changed", name)
		}
		s.Close()
		// Restart rewrote the state; start the next case from a fresh store.
		removeStore(t, config)
		config, data, starts = journalWithCommits(t, 2, 0)
	}
}

// A writer that dies in the middle of a record write, through the real save
// path: the injected write stores only a prefix, as SIGKILL between page
// copies does. Restart is clean; every acknowledged record is intact.
func TestJournalRestartsAfterWriterDiesMidRecord(t *testing.T) {
	for _, keep := range []float64{0, 0.01, 0.5, 0.99} {
		config, _, _ := journalWithCommits(t, 2, 0)
		s, err := Open(config)
		if err != nil {
			t.Fatal(err)
		}
		s.journal.Operations["op_big"] = &record{Key: "key_big", Hash: "h", Operation: api.Operation{Id: "op_big", State: "succeeded"}}
		s.journal.Outputs["op_big"] = []api.Frame{{DataBase64: strings.Repeat("y", 3<<20)}}
		writeRecordAt = func(f *os.File, data []byte, off int64) (int, error) {
			n := int(float64(len(data)) * keep)
			if _, err := f.WriteAt(data[:n], off); err != nil {
				return 0, err
			}
			return n, errors.New("killed mid-write")
		}
		err = s.save("op_big")
		writeRecordAt = func(f *os.File, data []byte, off int64) (int, error) { return f.WriteAt(data, off) }
		if err == nil {
			t.Fatal("injected death not seen")
		}
		s.Close()
		s, err = Open(config)
		if err != nil {
			t.Fatalf("keep %.2f: restart refused after a partial write: %v", keep, err)
		}
		if len(s.journal.Operations) != 2 || s.keys["key_op_0"] != "op_0" || s.keys["key_op_1"] != "op_1" || s.keys["key_big"] != "" {
			t.Fatalf("keep %.2f: history wrong after restart: %d operations", keep, len(s.journal.Operations))
		}
		s.Close()
		removeStore(t, config)
	}
}

// Death after the record is durable but before the marker covers it: the
// record was not acknowledged, so restart discards it cleanly.
func TestJournalRestartsAfterDeathBeforeMarker(t *testing.T) {
	// Three records are durable in the log, but the process died before the
	// marker covered the third: put the marker back to its second commit.
	config, _, starts := journalWithCommits(t, 3, 0)
	m, ok, err := readCommitMark(config.StateDir)
	if err != nil || !ok || m.End != starts[3] {
		t.Fatalf("marker %+v %v %v", m, ok, err)
	}
	m.End, m.Sequence = starts[2], m.Sequence-1
	if err := os.WriteFile(markerPath(config), m.encode(), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(config)
	if err != nil {
		t.Fatalf("restart refused: %v", err)
	}
	defer s.Close()
	if len(s.journal.Operations) != 2 || s.keys["key_op_1"] != "op_1" || s.keys["key_op_2"] != "" {
		t.Fatalf("unacknowledged record kept or history lost: %d operations", len(s.journal.Operations))
	}
}

// Damage to the marker itself, or a marker that disagrees with the log, is
// refused.
func TestJournalRefusesDamagedOrInconsistentMarker(t *testing.T) {
	config, _, starts := journalWithCommits(t, 2, 0)
	good, err := os.ReadFile(markerPath(config))
	if err != nil {
		t.Fatal(err)
	}
	m, ok, err := readCommitMark(config.StateDir)
	if err != nil || !ok || m.End != starts[2] || m.Sequence != 2 {
		t.Fatalf("marker %+v %v %v, want end %d sequence 2", m, ok, err, starts[2])
	}
	for name, marker := range map[string][]byte{
		"flipped byte":        func() []byte { b := append([]byte(nil), good...); b[5] ^= 1; return b }(),
		"zeroed":              make([]byte, markSize),
		"short":               good[:100],
		"end inside a record": commitMark{Generation: m.Generation, End: starts[1] + 10, Sequence: 2}.encode(),
		"end past the file":   commitMark{Generation: m.Generation, End: 1 << 40, Sequence: 2}.encode(),
		"wrong sequence":      commitMark{Generation: m.Generation, End: starts[2], Sequence: 5}.encode(),
		"future generation":   commitMark{Generation: m.Generation + 1, End: starts[2], Sequence: 2}.encode(),
	} {
		if err := os.WriteFile(markerPath(config), marker, 0600); err != nil {
			t.Fatal(err)
		}
		mustRefuse(t, config, "marker "+name)
	}
	if err := os.Remove(markerPath(config)); err != nil {
		t.Fatal(err)
	}
	mustRefuse(t, config, "records without a marker")
}

// A real writer process is SIGKILLed while it commits multi-page records.
// Every restart is clean and keeps every record the writer saw acknowledged.
func TestJournalRestartsAfterWriterProcessIsKilled(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns writer processes")
	}
	partial := 0
	for round := 0; round < 8; round++ {
		config := storeConfig(t)
		if s, err := Open(config); err != nil {
			t.Fatal(err)
		} else {
			s.Close()
		}
		cmd := exec.Command(os.Args[0], "-test.run=^TestJournalWriterProcess$")
		cmd.Env = append(os.Environ(), "LAMP_JOURNAL_WRITER_DIR="+config.StateDir)
		out, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		lines := bufio.NewScanner(out)
		var acked []string
		for lines.Scan() {
			line := lines.Text()
			if id, ok := strings.CutPrefix(line, "acked "); ok {
				acked = append(acked, id)
			}
			// Kill while a multi-page record is being written: the writer
			// announces it from inside the real record write.
			if line == "pwrite" && len(acked) >= 2+round%3 {
				if round%2 == 1 {
					time.Sleep(200 * time.Microsecond)
				}
				cmd.Process.Kill()
				break
			}
		}
		cmd.Wait()
		go io.Copy(io.Discard, out)
		logData, _ := os.ReadFile(filepath.Join(config.StateDir, "journal.log"))
		m, _, _ := readCommitMark(config.StateDir)
		if m.End < int64(len(logData)) && !allZero(logData[m.End:]) {
			partial++
		}
		s, err := Open(config)
		if err != nil {
			t.Fatalf("round %d: restart after SIGKILL refused: %v", round, err)
		}
		for _, id := range acked {
			if _, ok := s.journal.Operations[id]; !ok {
				t.Fatalf("round %d: acknowledged %s lost", round, id)
			}
		}
		s.Close()
		removeStore(t, config)
	}
	t.Logf("%d of 8 kills left uncommitted bytes past the committed end", partial)
}

// TestJournalWriterProcess is the writer for the test above; it runs only in
// a child process.
func TestJournalWriterProcess(t *testing.T) {
	dir := os.Getenv("LAMP_JOURNAL_WRITER_DIR")
	if dir == "" {
		t.Skip("writer child only")
	}
	config := storeConfig(t)
	config.StateDir = dir
	s, err := Open(config)
	if err != nil {
		fmt.Println("open failed", err)
		os.Exit(2)
	}
	writeRecordAt = func(f *os.File, data []byte, off int64) (int, error) {
		if len(data) > 1<<20 {
			fmt.Println("pwrite")
		}
		return f.WriteAt(data, off)
	}
	for i := 0; ; i++ {
		id := fmt.Sprintf("op_%d", i)
		s.journal.Operations[id] = &record{Key: "key_" + id, Hash: id, Operation: api.Operation{Id: id, State: "succeeded"}}
		size := 100
		if i%2 == 1 {
			size = 32 << 20 // many pages: the write takes milliseconds
		}
		s.journal.Outputs[id] = []api.Frame{{DataBase64: strings.Repeat("z", size)}}
		if err := s.save(id); err != nil {
			fmt.Println("save failed", err)
			os.Exit(2)
		}
		fmt.Println("acked", id)
		delete(s.journal.Outputs, id)
	}
}
