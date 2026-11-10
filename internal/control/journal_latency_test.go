package control

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/JohanSardar/lamp/pkg/api"
)

// TestJournalCommitLatency is a microbenchmark, not a gate: set
// LAMP_JOURNAL_LATENCY_DIR to a directory on the disk under test. It reports
// per-commit save latency for a typical operation-sized record.
func TestJournalCommitLatency(t *testing.T) {
	dir := os.Getenv("LAMP_JOURNAL_LATENCY_DIR")
	if dir == "" {
		t.Skip("set LAMP_JOURNAL_LATENCY_DIR to measure")
	}
	config := storeConfig(t)
	config.StateDir = filepath.Join(dir, fmt.Sprintf("latency-%d", time.Now().UnixNano()))
	defer os.RemoveAll(config.StateDir)
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	const n = 1000
	samples := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("op_%d", i)
		s.journal.Operations[id] = &record{Key: "key_" + id, Hash: "sha256:0123456789abcdef", Operation: api.Operation{Id: id, State: "succeeded"}}
		start := time.Now()
		if err := s.save(id); err != nil {
			t.Fatal(err)
		}
		samples = append(samples, time.Since(start))
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	pct := func(p float64) float64 { return float64(samples[int(p*float64(n-1))].Microseconds()) / 1000 }
	fmt.Printf("JOURNAL_COMMIT_LATENCY n=%d p50=%.3fms p95=%.3fms p99=%.3fms max=%.3fms\n", n, pct(.5), pct(.95), pct(.99), pct(1))
}
