package control

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

type TrustedNativeJob struct {
	JobID                string `json:"job_id"`
	RepositoryVisibility string `json:"repository_visibility"`
}

// NativeFallbackConfig enrolls the weaker, non-VM fallback. Runtime is
// "app_sandbox" (macOS, the default) or "gvisor" (Linux: Launcher is the
// root-run `lamp-supervisor gvisor-task` launcher and Rootfs its read-only
// task root). Either way the result is labeled weaker than a VM.
type NativeFallbackConfig struct {
	Runtime      string                      `json:"runtime,omitempty"`
	Launcher     string                      `json:"launcher"`
	HelperBinary string                      `json:"helper_binary,omitempty"`
	Packager     string                      `json:"packager,omitempty"`
	Rootfs       string                      `json:"rootfs,omitempty"`
	AllowWithVM  bool                        `json:"allow_with_vm,omitempty"`
	Jobs         map[string]TrustedNativeJob `json:"jobs"`
}

func (c *NativeFallbackConfig) gvisor() bool { return c != nil && c.Runtime == "gvisor" }

func (c *NativeFallbackConfig) boundary() string {
	if c.gvisor() {
		return "gvisor_sandbox"
	}
	return "app_sandbox_process"
}

func ReadNativeFallback(path string) (*NativeFallbackConfig, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return nil, err
	}
	if stat.Uid != uint32(os.Geteuid()) || stat.Mode&0o022 != 0 {
		return nil, errors.New("native authority file must be owned by the custodian and not group/world writable")
	}
	var cfg NativeFallbackConfig
	decoder := json.NewDecoder(io.LimitReader(f, 65537))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return nil, err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, errors.New("one native authority document required")
	}
	return &cfg, nil
}

func (s *Service) initNative(config Config) error {
	if config.NativeFallback == nil {
		return nil
	}
	if config.Fleet != nil || config.Identity == nil {
		return errors.New("native fallback requires a credentialed local control plane")
	}
	raw, err := json.Marshal(config.NativeFallback)
	if err != nil {
		return err
	}
	var enrolled NativeFallbackConfig
	if err := json.Unmarshal(raw, &enrolled); err != nil {
		return err
	}
	paths := []string{enrolled.Launcher, enrolled.HelperBinary, enrolled.Packager}
	switch enrolled.Runtime {
	case "", "app_sandbox":
	case "gvisor":
		paths = []string{enrolled.Launcher, enrolled.Rootfs}
	default:
		return errors.New("native runtime must be app_sandbox or gvisor")
	}
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			return errors.New("native runtime paths must be absolute trusted installation paths")
		}
	}
	for hash, job := range enrolled.Jobs {
		b, err := hex.DecodeString(hash)
		if err != nil || len(b) != 32 || job.JobID == "" || (job.RepositoryVisibility != "public" && job.RepositoryVisibility != "private") {
			return errors.New("native job authority requires a credential SHA-256, job ID and known repository visibility")
		}
	}
	s.native = &enrolled
	return nil
}

func (s *Service) nativeOrigin(r *http.Request, a caller) (string, *TrustedNativeJob, error) {
	if s.native == nil {
		return "", nil, errors.New("native fallback is not configured")
	}
	token := r.Header.Get("Authorization")
	if len(token) >= len("Bearer ") {
		if job, ok := s.native.Jobs[bearerHash(token[len("Bearer "):])]; ok {
			if job.RepositoryVisibility == "public" {
				return "", &job, errors.New("public-repository CI requires a VM; weaker native execution is forbidden")
			}
			return "private_repository_ci", &job, nil
		}
	}
	if a.Principal == s.journal.Control.OwnerID && a.KeyID == "" {
		return "owner_task", nil, nil
	}
	return "", nil, errors.New("native execution requires custodian-enrolled job authority; request origin claims are not authority")
}

func (s *Service) nativeRefusal(w http.ResponseWriter, r *http.Request, id string, a caller) bool {
	s.mu.Lock()
	_, job, err := s.nativeOrigin(r, a)
	s.mu.Unlock()
	if err == nil {
		return false
	}
	w.Header().Set("X-Lamp-Boundary", s.native.boundary())
	w.Header().Set("X-Lamp-Isolation", "weaker_than_vm")
	message := err.Error()
	if job != nil {
		message = job.JobID + " (" + job.RepositoryVisibility + "): " + message
	}
	writeError(w, id, "VM_REQUIRED", message, nil)
	return true
}

func (s *Service) nativeHTTP(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.Lock()
	a, authenticated := s.authenticate(r)
	allowed := authenticated && s.can(a, "exec.create")
	broken := s.broken
	s.mu.Unlock()
	if broken {
		writeError(w, id, "DATA_UNAVAILABLE", "journal unavailable", nil)
		return
	}
	if !authenticated {
		writeError(w, id, "UNAUTHENTICATED", "valid caller credential required", nil)
		return
	}
	if !allowed {
		writeError(w, id, "SCOPE_DENIED", "exec.create authority required", nil)
		return
	}
	if s.nativeRefusal(w, r, id, a) {
		return
	}
	var task struct {
		Argv                []string `json:"argv"`
		MemoryMiB           uint64   `json:"memory_mib"`
		PidsMax             uint64   `json:"pids_max"`
		TimeoutSeconds      uint64   `json:"timeout_seconds"`
		Login               bool     `json:"login"`
		ProtectedConnection bool     `json:"protected_connection"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&task); err != nil {
		writeError(w, id, "INVALID_ARGUMENT", err.Error(), nil)
		return
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF || len(task.Argv) == 0 || task.MemoryMiB == 0 || task.TimeoutSeconds == 0 || task.TimeoutSeconds > 300 {
		writeError(w, id, "INVALID_ARGUMENT", "one bounded native task is required", nil)
		return
	}
	if task.Login || task.ProtectedConnection {
		writeError(w, id, "VM_REQUIRED", "logins and protected connections require a VM", nil)
		return
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || len(keys[0]) == 0 || len(keys[0]) > 256 {
		writeError(w, id, "INVALID_ARGUMENT", "one Idempotency-Key required", nil)
		return
	}
	s.mu.Lock()
	origin, job, err := s.nativeOrigin(r, a)
	current := s.current(a)
	s.mu.Unlock()
	if err != nil || !current {
		writeError(w, id, "SCOPE_DENIED", "job authority changed", nil)
		return
	}
	marker := filepath.Join(s.dir, "native-request-"+bearerHash(r.Header.Get("Authorization")+"\n"+keys[0]))
	reserved, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		writeError(w, id, "OUTCOME_UNKNOWN", "native request may have executed; reconcile before retrying", nil)
		return
	}
	started := false
	defer func() {
		if !started {
			os.Remove(marker)
			syncDirectory(s.dir)
		}
	}()
	if err := reserved.Sync(); err != nil {
		reserved.Close()
		writeError(w, id, "DATA_UNAVAILABLE", err.Error(), nil)
		return
	}
	reserved.Close()
	if err := syncDirectory(s.dir); err != nil {
		writeError(w, id, "DATA_UNAVAILABLE", err.Error(), nil)
		return
	}
	dir, err := os.MkdirTemp(s.dir, "native-task-")
	if err != nil {
		writeError(w, id, "HOST_UNAVAILABLE", err.Error(), nil)
		return
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(task.TimeoutSeconds+10)*time.Second)
	defer cancel()
	if s.native.gvisor() {
		s.mu.Lock()
		current = s.current(a)
		s.mu.Unlock()
		if !current {
			writeError(w, id, "SCOPE_DENIED", "task authority revoked before dispatch", nil)
			return
		}
		fields := map[string]any{"origin": origin, "argv": task.Argv, "memory_mib": task.MemoryMiB, "timeout_seconds": task.TimeoutSeconds, "login": false, "protected_connection": false}
		if task.PidsMax != 0 {
			fields["pids_max"] = task.PidsMax
		}
		input, _ := json.Marshal(fields)
		argv := []string{"gvisor-task", "--rootfs", s.native.Rootfs, "--state", filepath.Join("/run/lamp-gvisor", filepath.Base(dir))}
		if s.native.AllowWithVM {
			argv = append(argv, "--allow-with-vm")
		}
		command := exec.CommandContext(ctx, s.native.Launcher, argv...)
		command.Env = []string{"PATH=/usr/bin:/bin"}
		command.Stdin = bytes.NewReader(input)
		var stderr bytes.Buffer
		command.Stderr = &stderr
		output, err := command.Output()
		started = command.Process != nil
		var result map[string]any
		if json.Unmarshal(output, &result) != nil {
			writeError(w, id, "HOST_UNAVAILABLE", "gvisor task failed: "+err.Error()+": "+stderr.String(), nil)
			return
		}
		w.Header().Set("X-Lamp-Boundary", "gvisor_sandbox")
		w.Header().Set("X-Lamp-Isolation", "weaker_than_vm")
		if e, refused := result["error"].(map[string]any); refused {
			code, _ := e["code"].(string)
			message, _ := e["message"].(string)
			writeError(w, id, code, message, nil)
			return
		}
		prepareJSON(w, 200, map[string]any{"boundary": "gvisor_sandbox", "isolation": "weaker_than_vm", "job": job, "result": result, "request_id": id})()
		return
	}
	app := filepath.Join(dir, "Task.app")
	pack := exec.CommandContext(ctx, "/bin/sh", s.native.Packager, s.native.HelperBinary, app)
	if out, err := pack.CombinedOutput(); err != nil {
		writeError(w, id, "HOST_UNAVAILABLE", string(out), nil)
		return
	}
	s.mu.Lock()
	current = s.current(a)
	s.mu.Unlock()
	if !current {
		writeError(w, id, "SCOPE_DENIED", "task authority revoked before dispatch", nil)
		return
	}
	input, _ := json.Marshal(map[string]any{"origin": origin, "argv": task.Argv, "memory_mib": task.MemoryMiB, "timeout_seconds": task.TimeoutSeconds, "login": false, "protected_connection": false})
	command := exec.CommandContext(ctx, s.native.Launcher, "native-task", "--helper", filepath.Join(app, "Contents", "MacOS", "lamp-native"))
	command.Env = []string{"PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(input)
	output, err := command.Output()
	started = command.Process != nil
	if err != nil {
		writeError(w, id, "HOST_UNAVAILABLE", "native task failed: "+err.Error(), nil)
		return
	}
	prepareJSON(w, 200, map[string]any{"boundary": "app_sandbox_process", "isolation": "weaker_than_vm", "job": job, "output": string(output), "request_id": id})()
}
