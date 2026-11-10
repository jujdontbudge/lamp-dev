package plugin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func randomID() string {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

// Native runners never fall back to an unsandboxed exec. Executables are copied
// into a private read-only staging directory AFTER their admitted bytes are
// checked. No plugin-selected paths, arguments, environment or inherited FDs.
func prepare(o StartOptions, proof *runtimeProof) (*exec.Cmd, func() error, error) {
	info, err := os.Stat(o.Executable)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 256<<20 {
		return nil, nil, errors.New("invalid plugin executable")
	}
	bin, err := os.ReadFile(o.Executable)
	if err != nil {
		return nil, nil, err
	}
	if Digest(bin) != o.Package.manifest.Digest {
		return nil, nil, errors.New("executable changed since admission")
	}
	if !filepath.IsAbs(o.StateDir) {
		return nil, nil, errors.New("absolute private staging directory required")
	}
	dir, err := os.MkdirTemp(o.StateDir, "plugin-")
	if err != nil {
		return nil, nil, err
	}
	cleanDir := func() error { return os.RemoveAll(dir) }
	path := filepath.Join(dir, "worker")
	if err := os.WriteFile(path, bin, 0500); err != nil {
		_ = cleanDir()
		return nil, nil, err
	}
	// Seatbelt compares canonical vnode paths (/var and /tmp are symlinks).
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		_ = cleanDir()
		return nil, nil, err
	}
	l := o.Package.manifest.Limits
	var cmd *exec.Cmd
	var unit string
	switch runtime.GOOS {
	case "darwin":
		if !o.DiagnosticSoftLimits {
			_ = cleanDir()
			return nil, nil, errors.New("macOS native hard resource quotas are unqualified; diagnostic soft limits require explicit opt-in")
		}
		// deny-default: no network, Mach/Unix IPC, process fork, writes, host
		// configuration, user files or other executables. dyld's public runtime is
		// readable; these roots must never contain owner credentials.
		profile := `(version 1)(deny default)(allow process-exec (literal ` + strconv.Quote(path) + `))(allow file-read* (literal "/") (literal ` + strconv.Quote(path) + `) (subpath "/usr/lib") (subpath "/System/Library"))(allow sysctl-read (sysctl-name "hw.ncpu") (sysctl-name "hw.pagesize") (sysctl-name "hw.pagesize_compat") (sysctl-name "kern.osrelease"))`
		cmd = exec.Command("/usr/bin/sandbox-exec", "-p", profile, path)
	case "linux":
		unit = "lamp-plugin-" + randomID()
		*proof = platformLimits(unit, l)
		args := []string{"--user", "--quiet", "--wait", "--pipe", "--collect", "--unit=" + unit, "--service-type=exec", "--property=MemoryMax=" + strconv.FormatUint(l.MemoryBytes, 10), "--property=MemorySwapMax=0", "--property=CPUQuota=" + strconv.Itoa(l.CPUPercent) + "%", "--property=TasksMax=32", "--property=KillMode=control-group", "--property=RuntimeMaxSec=3600", "--property=NoNewPrivileges=yes", "--property=LimitCORE=0", "--property=LimitNOFILE=64", "--property=UMask=0077", "--", o.Launcher, "--await-admission", "/usr/bin/bwrap", "--unshare-all", "--die-with-parent", "--new-session", "--cap-drop", "ALL", "--clearenv", "--setenv", "LANG", "C", "--ro-bind", "/usr/lib", "/usr/lib", "--ro-bind", "/usr/lib64", "/usr/lib64", "--symlink", "usr/lib", "/lib", "--symlink", "usr/lib64", "/lib64", "--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp", "--ro-bind", path, "/worker", "--chdir", "/", "/worker"}
		// /usr/lib64 is absent on some distributions (including arm64).
		if _, err := os.Stat("/usr/lib64"); os.IsNotExist(err) {
			for i := 0; i < len(args)-2; i++ {
				if args[i] == "--ro-bind" && args[i+1] == "/usr/lib64" {
					args = append(args[:i], args[i+3:]...)
					break
				}
			}
		}
		cmd = exec.Command("/usr/bin/systemd-run", args...)
	default:
		_ = cleanDir()
		return nil, nil, errors.New("no qualified native service sandbox for this OS")
	}
	launcher, err := os.ReadFile(o.Launcher)
	if err != nil || !filepath.IsAbs(o.Launcher) || !ValidDigest(o.LauncherDigest) || Digest(launcher) != o.LauncherDigest {
		_ = cleanDir()
		return nil, nil, errors.New("pinned host plugin launcher required")
	}
	cmd = exec.Command(o.Launcher, cmd.Args...)
	cmd.Dir = "/"
	cmd.Env = []string{"LANG=C", "HOME=/nonexistent"}
	// systemd-run needs the *host* user-manager socket, never forwarded into bwrap.
	if runtime.GOOS == "linux" {
		cmd.Env = append(cmd.Env, "XDG_RUNTIME_DIR=/run/user/"+strconv.Itoa(os.Getuid()))
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cleanup := func() error {
		var errs []error
		if unit != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, "/usr/bin/systemctl", "--user", "stop", unit+".service").CombinedOutput()
			if err != nil && !strings.Contains(string(out), "not loaded") && !strings.Contains(string(out), "not found") {
				errs = append(errs, fmt.Errorf("plugin unit cleanup: %w: %s", err, out))
			}
		}
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		if err := cleanDir(); err != nil {
			errs = append(errs, err)
		}
		return errors.Join(errs...)
	}
	return cmd, cleanup, nil
}
