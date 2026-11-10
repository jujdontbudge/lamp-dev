package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const startupGate = "lamp.plugin/gate/1\n"

type runtimeProof struct {
	qualify func(context.Context) error
	check   func() error
}

// Only the pinned, trusted bootstrap runs before qualification. No plugin code
// or RPC reader/writer runs until the actual kernel limits have been verified.
func (w *Worker) admitRuntime(ctx context.Context) error {
	if w.limits.qualify == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(w.pkg.manifest.Limits.DeadlineMS)*time.Millisecond)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { w.trip(ctx.Err()) })
	defer stop()
	banner := make([]byte, len(startupGate))
	if _, err := io.ReadFull(w.stdout, banner); err != nil {
		return fmt.Errorf("resource admission gate: %w", err)
	}
	if string(banner) != startupGate {
		return errors.New("invalid resource admission gate")
	}
	if err := w.limits.qualify(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := w.stdin.Write([]byte{'A'})
	return err
}
func (w *Worker) checkRuntime() error {
	if w.limits.check == nil {
		return nil
	}
	if err := w.limits.check(); err != nil {
		w.trip(err)
		return err
	}
	return nil
}

func smallFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return "", err
	}
	if len(b) > 4096 {
		return "", errors.New("oversized cgroup observation")
	}
	return strings.TrimSpace(string(b)), nil
}

// Pure parser seam: production calls this only on the verified cgroup-v2 mount.
// Missing files mean missing controllers, not successful property assignment.
func verifyCgroupLimits(dir string, l Limits) error {
	bounded := func(file string, ceiling uint64) error {
		value, err := smallFile(filepath.Join(dir, file))
		if err != nil {
			return fmt.Errorf("required kernel limit %s unavailable: %w", file, err)
		}
		n, err := strconv.ParseUint(value, 10, 64)
		if err != nil || n > ceiling {
			return fmt.Errorf("required kernel limit %s not enforced: %s", file, value)
		}
		return nil
	}
	if err := bounded("memory.max", l.MemoryBytes); err != nil {
		return err
	}
	if err := bounded("memory.swap.max", 0); err != nil {
		return err
	}
	if err := bounded("pids.max", 32); err != nil {
		return err
	}
	cpu, err := smallFile(filepath.Join(dir, "cpu.max"))
	if err != nil {
		return fmt.Errorf("required kernel CPU controller unavailable: %w", err)
	}
	parts := strings.Fields(cpu)
	if len(parts) != 2 {
		return errors.New("invalid kernel CPU limit")
	}
	quota, qerr := strconv.ParseUint(parts[0], 10, 64)
	period, perr := strconv.ParseUint(parts[1], 10, 64)
	// Kernel periods are at most 1 s. Bounding before multiplication avoids
	// overflow and compares the requested fraction exactly (no float rounding).
	if qerr != nil || perr != nil || quota == 0 || period == 0 || period > 1_000_000 || quota > period || quota*100 > period*uint64(l.CPUPercent) {
		return fmt.Errorf("required kernel CPU quota not enforced: %s", cpu)
	}
	return nil
}
