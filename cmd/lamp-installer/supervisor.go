package main

// The supervisor and its VM engine (lamp-vz on macOS; firecracker and jailer
// on Linux) are Lamp components like lampd. A release may ship
// bin/lamp-supervisor and its engine binaries; the installer copies a
// release's set into a root-owned directory (<supervisor dir>/<version>/)
// and runs the supervisor as root with `sudo -n`. A release without them
// uses the set installed outside releases (the system directory). VMs live
// in their supervisor's worker processes and do not survive it, so before a
// swap every ready sandbox on the host is durably suspended through Lamp's
// API and resumed after the new supervisor and lampd are up. Saved machine
// state is bound to the engine build, so an engine change is refused while
// such state exists.

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
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type supervisorState struct {
	GrantID string   `json:"grant_id,omitempty"`
	Dir     string   `json:"dir"`     // root-owned directory for release copies
	System  string   `json:"system"`  // supervisor set installed outside releases
	Args    []string `json:"args"`    // serve arguments
	Socket  string   `json:"socket"`  // its lamp.worker socket
	HostID  string   `json:"host_id"` // this host, whose sandboxes it runs
	// Version is the release whose supervisor is running; empty while the
	// supervisor is stopped (a swap in progress or a failed start).
	Version string `json:"version"`
	PID     int    `json:"pid"`
}

type engineFile struct{ name, flag string }

// engineFiles are the engine binaries the supervisor runs, by OS.
func engineFiles() []engineFile {
	if runtime.GOOS == "darwin" {
		return []engineFile{{"lamp-vz", "--worker"}}
	}
	return []engineFile{{"firecracker", "--firecracker"}, {"jailer", "--jailer"}}
}

func componentNames() []string {
	out := []string{"lamp-supervisor"}
	if runtime.GOOS == "linux" {
		out = append(out, "lamp-guard")
	}
	for _, f := range engineFiles() {
		out = append(out, f.name)
	}
	return out
}

func shipsSupervisor(p, ver string) bool {
	_, err := os.Stat(filepath.Join(p, "releases", ver, "bin", "lamp-supervisor"))
	return ver != "" && err == nil
}

// componentPaths returns where each component of a release runs from, and
// whether the release ships the supervisor itself.
func componentPaths(p string, sup *supervisorState, ver string) (map[string]string, bool) {
	out := map[string]string{}
	from := shipsSupervisor(p, ver)
	for _, name := range componentNames() {
		out[name] = filepath.Join(sup.System, name)
		if _, err := os.Stat(filepath.Join(p, "releases", ver, "bin", name)); from && err == nil {
			out[name] = filepath.Join(sup.Dir, ver, name)
		}
	}
	return out, from
}

// componentSources are the verified bytes each component is installed from:
// the release's staged copy, else the system one.
func componentSources(p string, sup *supervisorState, ver string) map[string]string {
	out := map[string]string{}
	from := shipsSupervisor(p, ver)
	for _, name := range componentNames() {
		out[name] = filepath.Join(sup.System, name)
		src := filepath.Join(p, "releases", ver, "bin", name)
		if _, err := os.Stat(src); from && err == nil {
			out[name] = src
		}
	}
	return out
}

func fileSum(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:]), nil
}

// supDigest identifies the supervisor set of a release by its bytes.
func supDigest(p string, sup *supervisorState, ver string) (string, error) {
	src := componentSources(p, sup, ver)
	out := []string{}
	for _, name := range componentNames() {
		sum, err := fileSum(src[name])
		if err != nil {
			return "", err
		}
		out = append(out, sum)
	}
	return strings.Join(out, "+"), nil
}

// engineIdentity is what saved machine state is bound to: each engine
// binary's digest and the snapshot format the release signs in its manifest.
func engineIdentity(p string, sup *supervisorState, ver string) (map[string]string, error) {
	id := map[string]string{}
	src := componentSources(p, sup, ver)
	for _, f := range engineFiles() {
		sum, err := fileSum(src[f.name])
		if err != nil {
			return nil, err
		}
		id[f.name+"_sha256"] = sum
	}
	var m manifest
	if raw, err := os.ReadFile(filepath.Join(p, "releases", ver, "manifest.json")); err == nil && json.Unmarshal(raw, &m) == nil {
		id["snapshot_format"] = m.Engine["snapshot_format"]
		for k, v := range m.Engine {
			if strings.HasSuffix(k, "_sha256") && id[k] != "" && id[k] != v {
				return nil, fmt.Errorf("engine binary %s differs from its signed digest", strings.TrimSuffix(k, "_sha256"))
			}
		}
	}
	return id, nil
}

func describeIdentity(id map[string]string) string {
	keys := []string{}
	for k := range id {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := []string{}
	for _, k := range keys {
		v := id[k]
		if len(v) > 12 {
			v = v[:12]
		}
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, " ")
}

func supStore(sup *supervisorState) string {
	for i, a := range sup.Args {
		if a == "--store" && i+1 < len(sup.Args) {
			return sup.Args[i+1]
		}
	}
	return ""
}

// savedState lists machine state in the supervisor's store that its current
// engine build made: templates, captures and sandboxes (running or
// suspended, both of which a swap would leave as saved state).
func savedState(sup *supervisorState) ([]string, error) {
	store := supStore(sup)
	if store == "" {
		return nil, errors.New("supervisor has no --store")
	}
	found := []string{}
	// templates, captures and sandboxes (sb/ on macOS); on Linux running
	// machines also live under jail/firecracker/ and durable snapshots under
	// state/objects/.
	for _, d := range []string{"templates", "captures", "sb", "jail/firecracker", "state/objects"} {
		out, err := exec.Command("sudo", "-n", "find", filepath.Join(store, d), "-mindepth", "1", "-maxdepth", "1").Output()
		if err != nil {
			continue // absent directory
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line != "" && filepath.Base(line) != "names" {
				found = append(found, d+"/"+filepath.Base(line))
			}
		}
	}
	return found, nil
}

// refusal is an update refused before anything changed.
type refusal struct{ msg string }

func (r refusal) Error() string { return r.msg }

// engineGuard refuses an engine change while saved machine state depends on
// the running engine build: the new build could not resume it.
func engineGuard(p string, sup *supervisorState, from, to string) error {
	if sup == nil || from == "" {
		return nil
	}
	a, err1 := engineIdentity(p, sup, from)
	b, err2 := engineIdentity(p, sup, to)
	if err1 != nil || err2 != nil {
		return refusal{fmt.Sprintf("engine identity unreadable: %v %v", err1, err2)}
	}
	if describeIdentity(a) == describeIdentity(b) {
		return nil
	}
	saved, err := savedState(sup)
	if err != nil {
		return refusal{err.Error()}
	}
	if len(saved) > 0 {
		sort.Strings(saved)
		return refusal{fmt.Sprintf("release %s changes the VM engine (%s -> %s); %d saved machine state(s) made by the running engine could not be resumed by the new one (%s); delete or re-prepare them, then update", to, describeIdentity(a), describeIdentity(b), len(saved), strings.Join(saved, ", "))}
	}
	return nil
}

// installSupervisor copies a staged release's supervisor set into the
// root-owned directory and checks the copies against the staged bytes, which
// were verified against the signed manifest.
func installSupervisor(p string, sup *supervisorState, ver string) error {
	dst, fromRelease := componentPaths(p, sup, ver)
	if !fromRelease {
		return nil
	}
	if err := sudo("mkdir", "-p", filepath.Join(sup.Dir, ver)); err != nil {
		return err
	}
	src := componentSources(p, sup, ver)
	for _, name := range componentNames() {
		if !strings.HasPrefix(dst[name], sup.Dir) {
			continue // from the system set
		}
		want, err := fileSum(src[name])
		if err != nil {
			return err
		}
		if got, _ := fileSum(dst[name]); got == want {
			continue
		}
		if err := sudo("install", "-o", "root", "-g", "0", "-m", "0755", src[name], dst[name]); err != nil {
			return err
		}
		if got, _ := fileSum(dst[name]); got != want {
			return fmt.Errorf("installed %s does not match the signed release", dst[name])
		}
	}
	return nil
}

func supPidFile(p string) string { return filepath.Join(p, "run", "supervisor.pid") }

func supAlive(sup *supervisorState) bool {
	if sup.PID <= 0 || exec.Command("sudo", "-n", "kill", "-0", strconv.Itoa(sup.PID)).Run() != nil {
		return false
	}
	_, err := os.Stat(sup.Socket)
	return err == nil
}

func startSupervisor(p string, st *installState, ver string) error {
	sup := st.Supervisor
	if err := installSupervisor(p, sup, ver); err != nil {
		return err
	}
	paths, _ := componentPaths(p, sup, ver)
	sudo("rm", "-f", sup.Socket)
	os.Remove(supPidFile(p))
	logf, err := os.OpenFile(filepath.Join(p, "log", "supervisor.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer logf.Close()
	argv := []string{"-n", "sh", "-c", `echo "$$" > "$1"; shift; exec "$@"`, "sh", supPidFile(p), paths["lamp-supervisor"], "serve"}
	for _, f := range engineFiles() {
		argv = append(argv, f.flag, paths[f.name])
	}
	cmd := exec.Command("sudo", append(argv, sup.Args...)...)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-exited:
			return fmt.Errorf("supervisor %s exited during start: %v", ver, err)
		default:
		}
		raw, _ := os.ReadFile(supPidFile(p))
		pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
		if _, err := os.Stat(sup.Socket); err == nil && pid > 0 {
			sup.PID, sup.Version = pid, ver
			setWorkerPID(st)
			return saveState(p, *st)
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("supervisor %s did not open its socket", ver)
}

// stopSupervisor stops the running supervisor and records that none runs,
// so a later transition always starts one again.
func stopSupervisor(p string, st *installState) error {
	sup := st.Supervisor
	pid := strconv.Itoa(sup.PID)
	if sup.PID > 0 && exec.Command("sudo", "-n", "kill", "-0", pid).Run() == nil {
		sudo("kill", "-TERM", pid)
		stopped := false
		for i := 0; i < 120; i++ {
			if exec.Command("sudo", "-n", "kill", "-0", pid).Run() != nil {
				stopped = true
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		if !stopped {
			sudo("kill", "-KILL", pid)
		}
	}
	sup.Version, sup.PID = "", 0
	return saveState(p, *st)
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

// waitOp follows a lifecycle operation to its end and returns its error.
func waitOp(p string, resp map[string]any, timeout time.Duration) error {
	op, _ := resp["operation_id"].(string)
	if op == "" {
		return nil
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := apiGet(p, "/v1/operations/"+op)
		if err == nil {
			d, _ := out["data"].(map[string]any)
			if d == nil {
				d = out
			}
			switch d["state"] {
			case "succeeded":
				return nil
			case "failed", "cancelled":
				e, _ := json.Marshal(d["error"])
				return fmt.Errorf("operation %s %v: %s", op, d["state"], e)
			}
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("operation %s did not finish", op)
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

// suspendRunning durably suspends every ready sandbox on this host.
func suspendRunning(p, host string, drain time.Duration) ([]string, error) {
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
		// Sandbox states (schema): only a ready sandbox has a live machine to
		// suspend; it comes back ready.
		if m["host_id"] != host || state != "ready" {
			continue
		}
		// Drain: a command still running in the sandbox blocks suspension;
		// wait for it up to the policy's drain timeout.
		var resp map[string]any
		deadline := time.Now().Add(drain)
		for {
			resp, err = apiPost(p, "/v1/sandboxes/"+id+":suspend", revisionOf(m), map[string]any{"durability": "local"})
			if err == nil || !strings.Contains(err.Error(), "RESOURCE_IN_USE") || time.Now().After(deadline) {
				break
			}
			time.Sleep(2 * time.Second)
			if cur, gerr := sandboxNow(p, id); gerr == nil {
				m = cur
			}
		}
		if err == nil {
			err = waitOp(p, resp, 15*time.Minute)
		}
		if err != nil {
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
			resp, err := apiPost(p, "/v1/sandboxes/"+id+":resume", revisionOf(m), map[string]any{})
			if err == nil {
				err = waitOp(p, resp, 15*time.Minute)
			}
			if err != nil {
				return fmt.Errorf("resume %s: %w", id, err)
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
// supervisor when it is not running or the release's supervisor set differs
// from the running one. Ready sandboxes are suspended first and resumed last;
// held keeps the ones still to resume if a step fails, so a revert resumes
// them.
func transition(p string, st *installState, pol updatePolicy, to string, held *[]string) error {
	drain := time.Duration(pol.DrainTimeoutMs) * time.Millisecond
	swap := false
	if sup := st.Supervisor; sup != nil {
		if sup.Version == "" || !supAlive(sup) {
			swap = true
		} else {
			a, err1 := supDigest(p, sup, sup.Version)
			b, err2 := supDigest(p, sup, to)
			if err1 != nil || err2 != nil {
				return fmt.Errorf("supervisor component unreadable: %v %v", err1, err2)
			}
			swap = a != b
		}
		if swap {
			if _, err := waitHealthy(p, 3*time.Second); err == nil && sup.Version != "" && supAlive(sup) {
				ids, err := suspendRunning(p, sup.HostID, drain)
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
		if err := stopSupervisor(p, st); err != nil {
			return err
		}
		if err := startSupervisor(p, st, to); err != nil {
			event(p, map[string]any{"event": "supervisor_start_failed", "version": to, "reason": err.Error()})
			return err
		}
		paths, _ := componentPaths(p, st.Supervisor, to)
		sum, _ := fileSum(paths["lamp-supervisor"])
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
