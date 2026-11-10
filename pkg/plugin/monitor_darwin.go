package plugin

import (
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// macOS has no cgroup MemoryMax/CPUQuota equivalent. A fail-closed watchdog
// enforces sampled RSS and cumulative CPU, but this is NOT hard resource-cap
// qualification: allocations can exceed RSS between samples. See README.
func (w *Worker) monitor() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	start := time.Now()
	for {
		select {
		case <-w.stopped:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			out, err := exec.CommandContext(ctx, "/bin/ps", "-o", "rss=,time=", "-p", strconv.Itoa(w.cmd.Process.Pid)).Output()
			cancel()
			if err != nil {
				w.trip(errors.New("plugin resource observer unavailable"))
				return
			}
			fields := strings.Fields(string(out))
			if len(fields) != 2 {
				w.trip(errors.New("invalid plugin resource sample"))
				return
			}
			rss, err := strconv.ParseUint(fields[0], 10, 64)
			if err != nil {
				w.trip(err)
				return
			}
			parts := strings.Split(fields[1], ":")
			seconds := 0.0
			for _, p := range parts {
				n, err := strconv.ParseFloat(p, 64)
				if err != nil {
					w.trip(err)
					return
				}
				seconds = seconds*60 + n
			}
			l := w.pkg.manifest.Limits
			if rss*1024 > l.MemoryBytes || (time.Since(start) > time.Second && seconds > time.Since(start).Seconds()*float64(l.CPUPercent)/100) {
				w.trip(errors.New("plugin resource limit exceeded"))
				return
			}
		}
	}
}
