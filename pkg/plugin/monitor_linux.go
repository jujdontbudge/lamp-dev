package plugin

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

func platformLimits(unit string, l Limits) runtimeProof {
	var dir, group string
	var pid int
	check := func() error {
		if dir == "" || pid <= 0 {
			return errors.New("kernel resource limits not qualified")
		}
		if err := verifyCgroupLimits(dir, l); err != nil {
			return err
		}
		member, err := smallFile(fmt.Sprintf("/proc/%d/cgroup", pid))
		if err != nil || member != "0::"+group {
			return errors.New("plugin service lost verified cgroup membership")
		}
		return nil
	}
	qualify := func(ctx context.Context) error {
		var fs unix.Statfs_t
		if err := unix.Statfs("/sys/fs/cgroup", &fs); err != nil || fs.Type != unix.CGROUP2_SUPER_MAGIC {
			return errors.New("verified cgroup v2 mount required")
		}
		out, err := exec.CommandContext(ctx, "/usr/bin/systemctl", "--user", "show", unit+".service", "--property=ControlGroup", "--property=MainPID").Output()
		if err != nil {
			return fmt.Errorf("cannot identify plugin cgroup: %w", err)
		}
		fields := map[string]string{}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			key, value, ok := strings.Cut(line, "=")
			if !ok || (key != "ControlGroup" && key != "MainPID") || fields[key] != "" {
				return errors.New("invalid plugin service observation")
			}
			fields[key] = value
		}
		group = fields["ControlGroup"]
		pid, err = strconv.Atoi(fields["MainPID"])
		if err != nil || pid <= 0 || !strings.HasPrefix(group, "/") || filepath.Clean(group) != group || filepath.Base(group) != unit+".service" {
			return errors.New("unassigned plugin service cgroup")
		}
		dir = filepath.Join("/sys/fs/cgroup", strings.TrimPrefix(group, "/"))
		return check()
	}
	return runtimeProof{qualify: qualify, check: check}
}

// A property on the unit is not proof. Re-read the actual controller files and
// service membership, and fence grants/effects if enforcement disappears.
func (w *Worker) monitor() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-w.stopped:
			return
		case <-ticker.C:
			if w.checkRuntime() != nil {
				return
			}
		}
	}
}
