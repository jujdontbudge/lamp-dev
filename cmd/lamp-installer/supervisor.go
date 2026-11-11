package main

// The VZ supervisor (and its lamp-vz worker) is a Lamp component like lampd.
// A darwin release may ship bin/lamp-supervisor and bin/lamp-vz; the
// installer copies a release's pair into a root-owned directory
// (<supervisor dir>/<version>/) and runs the supervisor as root with
// `sudo -n`. A release without them uses the supervisor installed outside
// releases (the system directory). VZ machines live in their supervisor's
// worker processes and do not survive it, so before a swap every running
// sandbox on the host is durably suspended through Lamp's API and resumed
// after the new supervisor and lampd are up.

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type supervisorState struct {
	Dir     string   `json:"dir"`     // root-owned directory for release copies
	System  string   `json:"system"`  // supervisor installed outside releases
	Args    []string `json:"args"`    // serve arguments
	Socket  string   `json:"socket"`  // its lamp.worker socket
	HostID  string   `json:"host_id"` // this host, whose sandboxes it runs
	Version string   `json:"version"` // release whose supervisor is running
	PID     int      `json:"pid"`
}

// supPaths returns the supervisor and worker binaries a release uses, and
// whether they come from the release itself.
func supPaths(p string, sup *supervisorState, ver string) (string, string, bool) {
	rel := filepath.Join(p, "releases", ver, "bin")
	if _, err := os.Stat(filepath.Join(rel, "lamp-supervisor")); err == nil {
		d := filepath.Join(sup.Dir, ver)
		worker := filepath.Join(sup.System, "lamp-vz")
		if _, err := os.Stat(filepath.Join(rel, "lamp-vz")); err == nil {
			worker = filepath.Join(d, "lamp-vz")
		}
		return filepath.Join(d, "lamp-supervisor"), worker, true
	}
	return filepath.Join(sup.System, "lamp-supervisor"), filepath.Join(sup.System, "lamp-vz"), false
}

func fileSum(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:]), nil
}

// supDigest identifies the supervisor component of a release by the bytes
// it would run (the release's staged copies, else the system ones).
func supDigest(p string, sup *supervisorState, ver string) (string, error) {
	rel := filepath.Join(p, "releases", ver, "bin")
	out := []string{}
	for _, name := range []string{"lamp-supervisor", "lamp-vz"} {
		src := filepath.Join(rel, name)
		if _, err := os.Stat(src); err != nil {
			src = filepath.Join(sup.System, name)
		}
		s, err := fileSum(src)
		if err != nil {
			return "", err
		}
		out = append(out, s)
	}
	return strings.Join(out, "+"), nil
}

// installSupervisor copies a staged release's supervisor binaries into the
// root-owned directory and checks the copies against the staged bytes, which
// were verified against the signed manifest.
func installSupervisor(p string, sup *supervisorState, ver string) error {
	supBin, workerBin, fromRelease := supPaths(p, sup, ver)
	if !fromRelease {
		return nil
	}
	rel := filepath.Join(p, "releases", ver, "bin")
	if err := sudo("mkdir", "-p", filepath.Dir(supBin)); err != nil {
		return err
	}
	for src, dst := range map[string]string{filepath.Join(rel, "lamp-supervisor"): supBin, filepath.Join(rel, "lamp-vz"): workerBin} {
		if _, err := os.Stat(src); err != nil {
			continue
		}
		want, err := fileSum(src)
		if err != nil {
			return err
		}
		if got, _ := fileSum(dst); got == want {
			continue
		}
		if err := sudo("install", "-o", "root", "-g", "wheel", "-m", "0755", src, dst); err != nil {
			return err
		}
		if got, _ := fileSum(dst); got != want {
			return fmt.Errorf("installed %s does not match the signed release", dst)
		}
	}
	return nil
}

func supPidFile(p string) string { return filepath.Join(p, "run", "supervisor.pid") }

func startSupervisor(p string, st *installState, ver string) error {
	sup := st.Supervisor
	if err := installSupervisor(p, sup, ver); err != nil {
		return err
	}
	supBin, workerBin, _ := supPaths(p, sup, ver)
	sudo("rm", "-f", sup.Socket)
	os.Remove(supPidFile(p))
	logf, err := os.OpenFile(filepath.Join(p, "log", "supervisor.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer logf.Close()
	argv := append([]string{"-n", "sh", "-c", `echo "$$" > "$1"; shift; exec "$@"`, "sh", supPidFile(p), supBin, "serve", "--worker", workerBin}, sup.Args...)
	cmd := exec.Command("sudo", argv...)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		raw, _ := os.ReadFile(supPidFile(p))
		pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
		if _, err := os.Stat(sup.Socket); err == nil && pid > 0 {
			sup.PID, sup.Version = pid, ver
			setWorkerPID(st)
			return saveState(p, *st)
		}
		time.Sleep(500 * time.Millisecond)
	}
	return errors.New("supervisor did not open its socket")
}

func stopSupervisor(st *installState) error {
	pid := strconv.Itoa(st.Supervisor.PID)
	if st.Supervisor.PID <= 0 || sudo("kill", "-0", pid) != nil {
		return nil
	}
	sudo("kill", "-TERM", pid)
	for i := 0; i < 120; i++ {
		if exec.Command("sudo", "-n", "kill", "-0", pid).Run() != nil {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	sudo("kill", "-KILL", pid)
	return nil
}

// setWorkerPID points lampd at the running supervisor; lampd authenticates
// its peer by PID.
func setWorkerPID(st *installState) {
	args := []string{}
	for i := 0; i < len(st.LampdArgs); i++ {
		a := st.LampdArgs[i]
		if a == "--worker-pid" {
			i++
			continue
		}
		if strings.HasPrefix(a, "--worker-pid=") {
			continue
		}
		args = append(args, a)
	}
	st.LampdArgs = append(args, "--worker-pid="+strconv.Itoa(st.Supervisor.PID))
}

// apiPost sends a mutation the way the public API takes it: idempotency key
// and expected revision in headers, the rest in the body.
func apiPost(p, path string, revision uint64, body map[string]any) (map[string]any, error) {
	token, err := os.ReadFile(filepath.Join(p, "config", "owner.token"))
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", "http://lamp"+path, bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", idemKey())
	req.Header.Set("If-Match", strconv.FormatUint(revision, 10))
	c := client(p)
	c.Timeout = 15 * time.Minute
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	var m map[string]any
	json.Unmarshal(out, &m)
	if resp.StatusCode/100 != 2 {
		return m, fmt.Errorf("POST %s: HTTP %d: %s", path, resp.StatusCode, strings.TrimSpace(string(out)))
	}
	return m, nil
}

func idemKey() string {
	b := make([]byte, 12)
	rand.Read(b)
	return "upd_" + hex.EncodeToString(b)
}

func sandboxNow(p, id string) (map[string]any, error) {
	out, err := apiGet(p, "/v1/sandboxes/"+id)
	if err != nil {
		return nil, err
	}
	if d, ok := out["data"].(map[string]any); ok {
		return d, nil
	}
	return out, nil
}

func revisionOf(m map[string]any) uint64 {
	f, _ := m["revision"].(float64)
	return uint64(f)
}

func waitState(p, id, want string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	last := ""
	for time.Now().Before(deadline) {
		if m, err := sandboxNow(p, id); err == nil {
			last, _ = m["state"].(string)
			if last == want {
				return nil
			}
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("sandbox %s is %q, not %s", id, last, want)
}

// suspendRunning durably suspends every running sandbox on this host.
func suspendRunning(p, host string) ([]string, error) {
	out, err := apiGet(p, "/v1/sandboxes?limit=500")
	if err != nil {
		return nil, err
	}
	items, _ := out["items"].([]any)
	held := []string{}
	for _, it := range items {
		m, _ := it.(map[string]any)
		id, _ := m["id"].(string)
		state, _ := m["state"].(string)
		if m["host_id"] != host || (state != "ready" && state != "running") {
			continue
		}
		if _, err := apiPost(p, "/v1/sandboxes/"+id+":suspend", revisionOf(m), map[string]any{"durability": "local"}); err != nil {
			return held, err
		}
		if err := waitState(p, id, "suspended", 15*time.Minute); err != nil {
			return held, err
		}
		held = append(held, id)
		event(p, map[string]any{"event": "sandbox_suspended", "sandbox_id": id, "reason": "supervisor swap"})
	}
	return held, nil
}

func resumeHeld(p string, held *[]string) error {
	for len(*held) > 0 {
		id := (*held)[0]
		m, err := sandboxNow(p, id)
		if err != nil {
			return err
		}
		if m["state"] == "suspended" {
			if _, err := apiPost(p, "/v1/sandboxes/"+id+":resume", revisionOf(m), map[string]any{}); err != nil {
				return err
			}
		}
		if err := waitState(p, id, "ready", 15*time.Minute); err != nil {
			return err
		}
		event(p, map[string]any{"event": "sandbox_resumed", "sandbox_id": id})
		*held = (*held)[1:]
	}
	return nil
}

// transition moves the host to a staged release: lampd always, and the
// supervisor when the release's supervisor bytes differ from the running
// one. Running sandboxes are suspended first and resumed last; held keeps
// the ones still to resume if a step fails, so a revert resumes them.
func transition(p string, st *installState, pol updatePolicy, to string, held *[]string) error {
	drain := time.Duration(pol.DrainTimeoutMs) * time.Millisecond
	swap := false
	if sup := st.Supervisor; sup != nil {
		a, err1 := supDigest(p, sup, sup.Version)
		b, err2 := supDigest(p, sup, to)
		if err1 != nil || err2 != nil {
			return fmt.Errorf("supervisor component unreadable: %v %v", err1, err2)
		}
		swap = a != b
		if swap {
			if _, err := waitHealthy(p, 3*time.Second); err == nil {
				ids, err := suspendRunning(p, sup.HostID)
				*held = append(*held, ids...)
				if err != nil {
					resumeHeld(p, held)
					return fmt.Errorf("suspend before supervisor swap: %w", err)
				}
			}
		}
	}
	if err := stopService(p, *st, lampdJob(p, *st), drain); err != nil {
		return err
	}
	if swap {
		from := st.Supervisor.Version
		if err := stopSupervisor(st); err != nil {
			return err
		}
		if err := startSupervisor(p, st, to); err != nil {
			return err
		}
		supBin, _, _ := supPaths(p, st.Supervisor, to)
		sum, _ := fileSum(supBin)
		event(p, map[string]any{"event": "supervisor_swapped", "from": from, "to": to, "binary_sha256": sum, "pid": st.Supervisor.PID})
	}
	if err := point(p, to); err != nil {
		return err
	}
	if st.Current != to {
		st.Previous, st.Current = st.Current, to
	}
	saveState(p, *st)
	if swap {
		if err := writeService(p, *st, lampdJob(p, *st)); err != nil {
			return err
		}
	}
	if err := startService(p, *st, lampdJob(p, *st)); err != nil {
		return err
	}
	if _, err := waitHealthy(p, 90*time.Second); err != nil {
		return err
	}
	return resumeHeld(p, held)
}
