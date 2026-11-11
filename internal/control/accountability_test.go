package control

import (
	"testing"
	"time"
)

// Ninety elapsed days cannot be demonstrated in an interactive owner journey.
func TestUsageRetentionBoundary(t *testing.T) {
	now := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	cut := now.Add(-90 * 24 * time.Hour).Unix()
	samples := []usageSample{{At: cut - 1, Count: 1}, {At: cut, Count: 2}, {At: now.Unix(), DurationMS: 3}}
	kept := pruneUsageSamples(samples, now)
	if len(kept) != 2 || kept[0].Count != 2 || kept[1].DurationMS != 3 {
		t.Fatal(kept)
	}
}
