// lamp-installer installs, updates, rolls back and removes signed Lamp
// releases on one host, and rolls a release across hosts one at a time.
//
// A release is a directory: manifest.json, manifest.sig (Ed25519 over the
// manifest bytes by the release key) and payload/. The host pins the release
// public key at install. Updates verify signature, platform, per-file SHA-256
// and anti-rollback before anything is applied. Owner data lives under
// <prefix>/state, outside releases, so updates never touch it and uninstall
// only removes it when the owner explicitly chooses delete.
package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
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

	"github.com/JohanSardar/lamp/internal/control"
	"github.com/JohanSardar/lamp/internal/release"
)

type fileEntry = release.FileEntry
type component = release.Component
type manifest = release.Manifest

// installState is <prefix>/config/install.json.
type installState struct {
	Name           string   `json:"name"`
	Service        string   `json:"service"`
	Current        string   `json:"current"`
	Previous       string   `json:"previous,omitempty"`
	HighestVersion string   `json:"highest_version"`
	LampdArgs      []string `json:"lampd_args"`
	// HostID is this host's id; update checks cover only its own work.
	HostID string `json:"host_id,omitempty"`
	// Supervisor, when set, is the VZ supervisor this installation runs.
	Supervisor *supervisorState `json:"supervisor,omitempty"`
}

type updatePolicy struct {
	Automatic      bool   `json:"automatic"`
	MaxUnavailable int    `json:"max_unavailable"`
	Channel        string `json:"channel"`
	DrainTimeoutMs int    `json:"drain_timeout_ms"`
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmds := map[string]func([]string) error{
		"keygen": keygen, "recovery-keygen": recoveryKeygen, "build": build, "verify": verifyCmd,
		"install": install, "update": update, "auto": auto, "policy": policy, "rollback": rollbackCmd,
		"status": status, "uninstall": uninstall, "rollout": rollout, "recover": recoverCmd, "index": indexCmd, "publish": publish, "agent": agentCmd, "restart": restartCmd,
	}
	fn, ok := cmds[os.Args[1]]
	if !ok {
		usage()
	}
	if err := fn(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "lamp-installer:", err)
		var ex exitErr
		if errors.As(err, &ex) {
			os.Exit(int(ex))
		}
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: lamp-installer keygen|recovery-keygen|build|verify|install|update|auto|policy|rollback|status|uninstall|rollout|recover|index|publish|agent|restart [flags]")
	os.Exit(2)
}

// exitErr 3 means the update failed and the host was rolled back.
type exitErr int

func (e exitErr) Error() string { return "exit " + strconv.Itoa(int(e)) }

func say(format string, a ...any) { fmt.Printf(format+"\n", a...) }

func event(prefix string, v map[string]any) {
	v["at"] = time.Now().UTC().Format(time.RFC3339)
	raw, _ := json.Marshal(v)
	say("%s", raw)
	if prefix != "" {
		f, err := os.OpenFile(filepath.Join(prefix, "config", "updates.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err == nil {
			f.Write(append(raw, '\n'))
			f.Close()
		}
	}
}

// ---- keys ----

func writePrivate(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func keygen(args []string) error {
	fl := flag.NewFlagSet("keygen", flag.ExitOnError)
	out := fl.String("out", "", "new private release signing key file (keep outside the repository)")
	pubOut := fl.String("public", "", "public trust file to write")
	fl.Parse(args)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := writePrivate(*out, []byte(base64.StdEncoding.EncodeToString(priv.Seed())+"\n")); err != nil {
		return err
	}
	line := release.TrustPrefix + base64.StdEncoding.EncodeToString(pub) + "\n"
	if err := os.WriteFile(*pubOut, []byte(line), 0644); err != nil {
		return err
	}
	sum := sha256.Sum256(pub)
	say("release key fingerprint sha256:%s", hex.EncodeToString(sum[:]))
	return nil
}

// recoveryKeygen is the owner's secret-once ceremony: the private file is
// written once for the owner to move off the machine; only the public file
// is given to lampd.
func recoveryKeygen(args []string) error {
	fl := flag.NewFlagSet("recovery-keygen", flag.ExitOnError)
	out := fl.String("out", "", "new owner recovery key file (move it off the machine)")
	pubOut := fl.String("public", "", "recovery public key file for lampd")
	fl.Parse(args)
	key, line, err := control.NewRecoveryKey()
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(key)
	if err := writePrivate(*out, append(raw, '\n')); err != nil {
		return err
	}
	if err := os.WriteFile(*pubOut, []byte(line), 0644); err != nil {
		return err
	}
	pub, _ := base64.StdEncoding.DecodeString(key.PublicKey)
	say("recovery key fingerprint %s", control.RecoveryFingerprint(pub))
	return nil
}

func readTrust(path string) (ed25519.PublicKey, error) { return release.ReadTrust(path) }

// ---- build ----

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

func build(args []string) error {
	fl := flag.NewFlagSet("build", flag.ExitOnError)
	version := fl.String("version", "", "release version a.b.c")
	channel := fl.String("channel", "stable", "release channel")
	goos := fl.String("os", runtime.GOOS, "target OS")
	arch := fl.String("arch", runtime.GOARCH, "target architecture")
	key := fl.String("key", "", "private release signing key file")
	out := fl.String("out", "", "new release directory")
	snapshotFormat := fl.String("snapshot-format", "", "with an engine in the payload: the saved-machine-state format it reads and writes")
	var files multi
	fl.Var(&files, "file", "payload/path=source (repeatable); bin/lampd and bin/lamp-installer are required; Linux releases also need tools/guard-audit-release.py")
	fl.Parse(args)
	if _, err := parseVersion(*version); err != nil {
		return err
	}
	seedRaw, err := os.ReadFile(*key)
	if err != nil {
		return err
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(seedRaw)))
	if err != nil || len(seed) != ed25519.SeedSize {
		return errors.New("invalid release signing key")
	}
	priv := ed25519.NewKeyFromSeed(seed)
	*out = filepath.Join(*out, *goos+"-"+*arch)
	m := manifest{Format: release.ManifestFormat, Version: *version, Channel: *channel, OS: *goos, Arch: *arch, CreatedAt: time.Now().UTC().Format(time.RFC3339), Files: map[string]fileEntry{}, Components: map[string][]component{}}
	if err := os.MkdirAll(filepath.Join(*out, "payload"), 0755); err != nil {
		return err
	}
	for _, f := range files {
		dst, src, ok := strings.Cut(f, "=")
		if !ok || !validPath(dst) {
			return fmt.Errorf("bad --file %q", f)
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		info, _ := os.Stat(src)
		mode := uint32(0644)
		if info.Mode()&0111 != 0 {
			mode = 0755
		}
		target := filepath.Join(*out, "payload", dst)
		os.MkdirAll(filepath.Dir(target), 0755)
		if err := os.WriteFile(target, data, fs.FileMode(mode)); err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		m.Files[dst] = fileEntry{SHA256: hex.EncodeToString(sum[:]), Mode: mode, Size: int64(len(data))}
		// Record the exact reused module graph of each Go binary (ENG-002).
		if bi, err := buildinfo.ReadFile(src); err == nil {
			var cs []component
			for _, d := range bi.Deps {
				if d.Replace != nil {
					d = d.Replace
				}
				cs = append(cs, component{Path: d.Path, Version: d.Version, Sum: d.Sum})
			}
			cs = append(cs, component{Path: "go", Version: bi.GoVersion})
			m.Components[dst] = cs
		}
	}
	// Sign the engine identity: saved machine state is bound to it.
	for _, name := range []string{"lamp-vz", "firecracker", "jailer"} {
		if e, ok := m.Files["bin/"+name]; ok {
			if m.Engine == nil {
				m.Engine = map[string]string{}
			}
			m.Engine[name+"_sha256"] = e.SHA256
		}
	}
	if m.Engine != nil {
		if *snapshotFormat == "" {
			return errors.New("a release shipping an engine needs --snapshot-format")
		}
		m.Engine["snapshot_format"] = *snapshotFormat
	}
	required := []string{"bin/lampd", "bin/lamp-installer", "LICENSE", "THIRD_PARTY.md"}
	if *goos == "linux" {
		// Without it an installed lampd never frees deleted sandboxes' spool slots.
		required = append(required, "tools/guard-audit-release.py")
	}
	for _, req := range required {
		if _, ok := m.Files[req]; !ok {
			return fmt.Errorf("release needs %s", req)
		}
	}
	raw, _ := json.MarshalIndent(m, "", " ")
	if err := os.WriteFile(filepath.Join(*out, "manifest.json"), raw, 0644); err != nil {
		return err
	}
	sig := ed25519.Sign(priv, raw)
	if err := os.WriteFile(filepath.Join(*out, "manifest.sig"), []byte(base64.StdEncoding.EncodeToString(sig)+"\n"), 0644); err != nil {
		return err
	}
	say("built %s %s/%s with %d files", *version, *goos, *arch, len(m.Files))
	return nil
}

func validPath(p string) bool {
	return p != "" && !filepath.IsAbs(p) && filepath.Clean(p) == p && !strings.HasPrefix(p, "..")
}

// ---- verification ----

func parseVersion(v string) ([3]int, error) { return release.ParseVersion(v) }
func newer(a, b string) bool                { return release.Newer(a, b) }

// verifyRelease checks index trust, this platform's manifest and every file.
func verifyRelease(dir string, trust ed25519.PublicKey) (manifest, error) {
	_, m, _, err := release.VerifyPlatform(dir, trust)
	return m, err
}

func verifyCmd(args []string) error {
	fl := flag.NewFlagSet("verify", flag.ExitOnError)
	release := fl.String("release", "", "release directory")
	trust := fl.String("trust", "", "release trust file")
	fl.Parse(args)
	key, err := readTrust(*trust)
	if err != nil {
		return err
	}
	m, err := verifyRelease(*release, key)
	if err != nil {
		return err
	}
	say("verified %s %s/%s", m.Version, m.OS, m.Arch)
	return nil
}

// stage copies a verified payload into <prefix>/releases/<version>.
func stage(prefix, dir string, m manifest, trust ed25519.PublicKey) (string, error) {
	target := filepath.Join(prefix, "releases", m.Version)
	if _, err := os.Stat(target); err == nil {
		// Staged by an earlier attempt: reuse it only if it is still exactly
		// the signed release being applied, else stage it again.
		want, derr := release.ManifestDigest(filepath.Join(dir, release.Platform()))
		if derr == nil {
			if _, verr := release.VerifyStaged(target, trust, want); verr == nil {
				return target, nil
			}
		}
		if err := os.RemoveAll(target); err != nil {
			return "", err
		}
	}
	tmp := target + ".staging"
	os.RemoveAll(tmp)
	dir = filepath.Join(dir, release.Platform())
	for rel, e := range m.Files {
		data, err := os.ReadFile(filepath.Join(dir, "payload", rel))
		if err != nil {
			return "", err
		}
		// Recheck after copy: the staged bytes are what will run.
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != e.SHA256 {
			return "", fmt.Errorf("payload file %s changed during staging", rel)
		}
		p := filepath.Join(tmp, rel)
		os.MkdirAll(filepath.Dir(p), 0755)
		if err := os.WriteFile(p, data, fs.FileMode(e.Mode)); err != nil {
			return "", err
		}
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
	sig, _ := os.ReadFile(filepath.Join(dir, "manifest.sig"))
	os.WriteFile(filepath.Join(tmp, "manifest.json"), raw, 0644)
	os.WriteFile(filepath.Join(tmp, "manifest.sig"), sig, 0644)
	return target, os.Rename(tmp, target)
}

// point atomically switches <prefix>/current to a staged version.
func point(prefix, version string) error {
	tmp := filepath.Join(prefix, ".current.tmp")
	os.Remove(tmp)
	if err := os.Symlink(filepath.Join("releases", version), tmp); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(prefix, "current"))
}

// ---- state ----

func loadState(prefix string) (installState, error) {
	var st installState
	raw, err := os.ReadFile(filepath.Join(prefix, "config", "install.json"))
	if err != nil {
		return st, fmt.Errorf("no Lamp installation at %s", prefix)
	}
	return st, json.Unmarshal(raw, &st)
}

func saveState(prefix string, st installState) error {
	raw, _ := json.MarshalIndent(st, "", " ")
	tmp := filepath.Join(prefix, "config", "install.json.tmp")
	if err := os.WriteFile(tmp, raw, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(prefix, "config", "install.json"))
}

func loadPolicy(prefix string) updatePolicy {
	p := updatePolicy{Automatic: false, MaxUnavailable: 1, Channel: "stable", DrainTimeoutMs: 60000}
	if raw, err := os.ReadFile(filepath.Join(prefix, "config", "update-policy.json")); err == nil {
		json.Unmarshal(raw, &p)
	}
	return p
}

// ---- service management ----

// A job is one supervised process: lampd itself, or the updater agent that
// runs beside it so stopping lampd during an update never stops the updater.
type job struct {
	suffix string // "" for lampd, "-agent" for the updater
	argv   []string
}

func lampdJob(prefix string, st installState) job {
	return job{"", append([]string{filepath.Join(prefix, "current", "bin", "lampd")}, st.LampdArgs...)}
}

// agentJob runs the installer binary copied outside releases, so an update
// swapping current/ never replaces the running updater underneath itself.
func agentJob(prefix string, st installState) job {
	return job{"-agent", []string{filepath.Join(prefix, "config", "lamp-installer"), "agent", "--prefix", prefix}}
}

func label(st installState, j job) string    { return "si.eilmer.lamp." + st.Name + j.suffix }
func unitName(st installState, j job) string { return "lamp-" + st.Name + j.suffix + ".service" }
func plistPath(st installState, j job) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", label(st, j)+".plist")
}
func logName(j job) string { return "lampd" + j.suffix + ".log" }
func guiDomain() string    { return "gui/" + strconv.Itoa(os.Getuid()) }

func sudo(args ...string) error {
	cmd := exec.Command("sudo", append([]string{"-n"}, args...)...)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	return cmd.Run()
}

func writeService(prefix string, st installState, j job) error {
	switch st.Service {
	case "systemd":
		quoted := make([]string, len(j.argv))
		for i, a := range j.argv {
			quoted[i] = strconv.Quote(a)
		}
		unit := fmt.Sprintf("[Unit]\nDescription=Lamp %s (%s)\nAfter=network-online.target\n\n[Service]\nUser=%d\nExecStart=%s\nRestart=on-failure\nRestartSec=2\nKillSignal=SIGTERM\nTimeoutStopSec=60\n\n[Install]\nWantedBy=multi-user.target\n", strings.TrimPrefix("daemon"+j.suffix, ""), st.Name, os.Getuid(), strings.Join(quoted, " "))
		tmp := filepath.Join(prefix, "config", unitName(st, j))
		if err := os.WriteFile(tmp, []byte(unit), 0644); err != nil {
			return err
		}
		if err := sudo("install", "-m", "0644", tmp, "/etc/systemd/system/"+unitName(st, j)); err != nil {
			return err
		}
		if err := sudo("systemctl", "daemon-reload"); err != nil {
			return err
		}
		return sudo("systemctl", "enable", unitName(st, j))
	case "launchd":
		// A per-user LaunchAgent in the gui/<uid> domain; never a system daemon.
		var b strings.Builder
		for _, a := range j.argv {
			b.WriteString("<string>" + xmlEscape(a) + "</string>")
		}
		logf := xmlEscape(filepath.Join(prefix, "log", logName(j)))
		plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>Label</key><string>%s</string><key>ProgramArguments</key><array>%s</array><key>RunAtLoad</key><true/><key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict><key>ThrottleInterval</key><integer>2</integer><key>ProcessType</key><string>Background</string><key>StandardOutPath</key><string>%s</string><key>StandardErrorPath</key><string>%s</string></dict></plist>
`, label(st, j), b.String(), logf, logf)
		os.MkdirAll(filepath.Dir(plistPath(st, j)), 0755)
		return os.WriteFile(plistPath(st, j), []byte(plist), 0644)
	case "process":
		return nil
	}
	return fmt.Errorf("unknown service manager %q", st.Service)
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	for _, r := range s {
		switch r {
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '&':
			b.WriteString("&amp;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func pidFile(prefix string, j job) string {
	return filepath.Join(prefix, "run", "lampd"+j.suffix+".pid")
}

func startService(prefix string, st installState, j job) error {
	switch st.Service {
	case "systemd":
		return sudo("systemctl", "start", unitName(st, j))
	case "launchd":
		out, err := exec.Command("launchctl", "bootstrap", guiDomain(), plistPath(st, j)).CombinedOutput()
		if err != nil {
			return fmt.Errorf("launchctl bootstrap: %v: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	logf, err := os.OpenFile(filepath.Join(prefix, "log", logName(j)), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(j.argv[0], j.argv[1:]...)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	pid := cmd.Process.Pid
	// Reap it when it exits, or a long-running agent would see a zombie
	// as still alive.
	go cmd.Wait()
	return os.WriteFile(pidFile(prefix, j), []byte(strconv.Itoa(pid)), 0600)
}

// restartService restarts a running job in place (systemctl restart /
// launchctl kickstart -k).
func restartService(prefix string, st installState, j job) error {
	switch st.Service {
	case "systemd":
		return sudo("systemctl", "restart", unitName(st, j))
	case "launchd":
		out, err := exec.Command("launchctl", "kickstart", "-k", guiDomain()+"/"+label(st, j)).CombinedOutput()
		if err != nil {
			return fmt.Errorf("launchctl kickstart: %v: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	if err := stopService(prefix, st, j, 60*time.Second); err != nil {
		return err
	}
	return startService(prefix, st, j)
}

// stopService drains a job with SIGTERM: the journal is durable, so active
// and saved work is reloaded by the next daemon.
func stopService(prefix string, st installState, j job, drain time.Duration) error {
	switch st.Service {
	case "systemd":
		return sudo("systemctl", "stop", unitName(st, j))
	case "launchd":
		exec.Command("launchctl", "bootout", guiDomain()+"/"+label(st, j)).Run()
		deadline := time.Now().Add(drain)
		for time.Now().Before(deadline) {
			if exec.Command("launchctl", "print", guiDomain()+"/"+label(st, j)).Run() != nil {
				return nil
			}
			time.Sleep(200 * time.Millisecond)
		}
		return fmt.Errorf("%s did not stop within %s", label(st, j), drain)
	}
	raw, err := os.ReadFile(pidFile(prefix, j))
	if err != nil {
		return nil
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	if pid <= 1 {
		return nil
	}
	proc, _ := os.FindProcess(pid)
	if proc.Signal(syscall.SIGTERM) != nil {
		os.Remove(pidFile(prefix, j))
		return nil
	}
	deadline := time.Now().Add(drain)
	for time.Now().Before(deadline) {
		if proc.Signal(syscall.Signal(0)) != nil {
			os.Remove(pidFile(prefix, j))
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("process %d did not drain within %s", pid, drain)
}

func removeService(prefix string, st installState, j job) {
	switch st.Service {
	case "systemd":
		sudo("systemctl", "disable", unitName(st, j))
		sudo("rm", "-f", "/etc/systemd/system/"+unitName(st, j))
		sudo("systemctl", "daemon-reload")
	case "launchd":
		os.Remove(plistPath(st, j))
	}
}

// ---- health and work preservation ----

func client(prefix string) *http.Client {
	sock := filepath.Join(prefix, "run", "api.sock")
	return &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}}}
}

func apiGet(prefix, path string) (map[string]any, error) {
	token, err := os.ReadFile(filepath.Join(prefix, "config", "owner.token"))
	if err != nil {
		return nil, err
	}
	req, _ := http.NewRequest("GET", "http://lamp"+path, nil)
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	resp, err := client(prefix).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GET %s: HTTP %d", path, resp.StatusCode)
	}
	var out map[string]any
	return out, json.Unmarshal(body, &out)
}

// inventory is this host's saved and active work as the API reports it:
// sandboxes it runs and resources it holds a copy of. Fleet records without a
// host are included; activity on other hosts never counts.
func inventory(prefix string) ([]string, error) {
	st, _ := loadState(prefix)
	mine := func(m map[string]any) bool {
		if st.HostID == "" {
			return true
		}
		h, hasHost := m["host_id"].(string)
		custody, hasCustody := m["custody_host_ids"].([]any)
		if !hasHost && !hasCustody {
			return true
		}
		if h == st.HostID {
			return true
		}
		for _, c := range custody {
			if c == st.HostID {
				return true
			}
		}
		return false
	}
	var rows []string
	for _, kind := range []string{"sandboxes", "volumes", "caches", "artifacts", "backups", "blobs"} {
		out, err := apiGet(prefix, "/v1/"+kind+"?limit=500")
		if err != nil {
			return nil, err
		}
		items, _ := out["items"].([]any)
		for _, it := range items {
			m, _ := it.(map[string]any)
			if !mine(m) {
				continue
			}
			if kind == "sandboxes" {
				rows = append(rows, fmt.Sprintf("%s %v %v %v", kind, m["id"], m["host_id"], m["state"]))
				continue
			}
			rows = append(rows, fmt.Sprintf("%s %v %v", kind, m["id"], firstOf(m, "manifest_digest", "sha256", "generation_digest")))
		}
	}
	sort.Strings(rows)
	return rows, nil
}

// lost returns the earlier items missing or changed afterwards. New items
// are fine; an update must only never lose or alter work.
func lost(before, after []string) []string {
	have := map[string]bool{}
	for _, r := range after {
		have[r] = true
	}
	out := []string{}
	for _, r := range before {
		if !have[r] {
			out = append(out, r)
		}
	}
	return out
}

func firstOf(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v
		}
	}
	return ""
}

func waitHealthy(prefix string, timeout time.Duration) ([]string, error) {
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		inv, err := inventory(prefix)
		if err == nil {
			return inv, nil
		}
		last = err
		time.Sleep(250 * time.Millisecond)
	}
	return nil, fmt.Errorf("health check failed: %v", last)
}

// ---- install ----

func install(args []string) error {
	fl := flag.NewFlagSet("install", flag.ExitOnError)
	release := fl.String("release", "", "release directory")
	prefix := fl.String("prefix", "", "installation directory")
	name := fl.String("name", "default", "installation name (service lamp-<name>)")
	trust := fl.String("trust", "", "release trust file to pin")
	service := fl.String("service", defaultService(), "systemd|launchd|process")
	recovery := fl.String("recovery-public-key", "", "owner recovery public key (omit to make backups visibly nonrecoverable)")
	identityFile := fl.String("identity-file", "", "existing owner enrollment (fleet hosts share one); default generates a new owner bearer")
	tokenFile := fl.String("token-file", "", "with --identity-file: the owner bearer file the updater and health checks use")
	agent := fl.Bool("agent", true, "run the updater agent service beside lampd")
	wait := fl.Bool("wait", true, "wait for lampd to answer (fleet hosts answer only once quorum forms)")
	var supArgs multi
	fl.Var(&supArgs, "supervisor-arg", "run the VZ supervisor as a Lamp component with this serve argument (repeatable; include --socket)")
	supDir := fl.String("supervisor-dir", "", "root-owned directory for release supervisor copies (default /usr/local/lib/lamp-<name>)")
	supSystem := fl.String("supervisor-system", "/usr/local/lib/lamp", "supervisor used by releases that ship none")
	guardAuditDir := fl.String("guard-audit-dir", defaultGuardAuditDir(), "host guard audit spool root that lampd releases deleted sandboxes' spools from")
	var extra multi
	fl.Var(&extra, "lampd-arg", "extra lampd argument (repeatable), e.g. enrolled worker or --fleet-config flags")
	fl.Parse(args)
	key, err := readTrust(*trust)
	if err != nil {
		return err
	}
	m, err := verifyRelease(*release, key)
	if err != nil {
		return fmt.Errorf("refused before install: %w", err)
	}
	p, _ := filepath.Abs(*prefix)
	if _, err := os.Stat(filepath.Join(p, "config", "install.json")); err == nil {
		return errors.New("already installed here; use update")
	}
	for _, d := range []string{"config", "releases", "mirror", "run", "log", "state"} {
		if err := os.MkdirAll(filepath.Join(p, d), 0700); err != nil {
			return err
		}
	}
	trustRaw, _ := os.ReadFile(*trust)
	os.WriteFile(filepath.Join(p, "config", "release-trust.pub"), trustRaw, 0644)
	// The owner bearer: only its SHA-256 goes to lampd's enrollment.
	tokenPath := filepath.Join(p, "config", "owner.token")
	if *identityFile != "" {
		ident, err1 := os.ReadFile(*identityFile)
		tok, err2 := os.ReadFile(*tokenFile)
		if err1 != nil || err2 != nil {
			return errors.New("--identity-file needs a readable --token-file")
		}
		if err := writePrivate(tokenPath, tok); err != nil {
			return err
		}
		os.WriteFile(filepath.Join(p, "config", "identity.json"), ident, 0600)
	} else if _, err := os.Stat(tokenPath); err != nil {
		tok := make([]byte, 32)
		rand.Read(tok)
		t := hex.EncodeToString(tok)
		if err := writePrivate(tokenPath, []byte(t+"\n")); err != nil {
			return err
		}
		sum := sha256.Sum256([]byte(t))
		ident, _ := json.Marshal(map[string]string{"owner_id": "pr_owner", "bootstrap_sha256": hex.EncodeToString(sum[:])})
		os.WriteFile(filepath.Join(p, "config", "identity.json"), ident, 0600)
	}
	policyRaw, _ := json.MarshalIndent(loadPolicy(p), "", " ")
	os.WriteFile(filepath.Join(p, "config", "update-policy.json"), policyRaw, 0600)
	lampdArgs := []string{"--socket", filepath.Join(p, "run", "api.sock"), "--state-dir", filepath.Join(p, "state", "control"), "--identity-file", filepath.Join(p, "config", "identity.json"), "--install-dir", p}
	if *recovery != "" {
		raw, err := os.ReadFile(*recovery)
		if err != nil {
			return err
		}
		os.WriteFile(filepath.Join(p, "config", "recovery.pub"), raw, 0644)
		lampdArgs = append(lampdArgs, "--recovery-public-key", filepath.Join(p, "config", "recovery.pub"))
	}
	if !hasWorker(extra) && !hasArg(extra, "--fleet-config") {
		// Control-only host until a supervisor is enrolled.
		lampdArgs = append(lampdArgs, "--worker-socket", filepath.Join(p, "run", "worker.sock"), "--worker-id", "unenrolled", "--worker-boot-id", "unenrolled", "--grant-id", "none", "--worker-uid", strconv.Itoa(os.Getuid()), "--worker-pid", "1")
	}
	lampdArgs = append(lampdArgs, extra...)
	if _, err := stage(p, *release, m, key); err != nil {
		return err
	}
	// Keep the signed release in the mirror too, so a later owner rollback
	// to it can verify it against its signed index.
	if _, err := copyTree(*release, filepath.Join(p, "mirror", m.Version)); err != nil {
		return err
	}
	if err := point(p, m.Version); err != nil {
		return err
	}
	// Deleted sandboxes hold a retained guard-spool reservation until lampd has
	// captured and removed their spools, so an installed lampd always gets a
	// release command. Linux spools are root-owned and the release ships the
	// tool. Mac sandboxes leave no host spools: an empty capture is "no
	// records", so /usr/bin/true frees the slot without Python (the CLT shim
	// would prompt to install developer tools) or sudo.
	if !hasArg(lampdArgs, "--audit-release") {
		argv := []string{"/usr/bin/true"}
		if runtime.GOOS == "linux" {
			argv = []string{"/usr/bin/python3", filepath.Join(p, "current", "tools", "guard-audit-release.py"), *guardAuditDir}
			if os.Geteuid() != 0 {
				argv = append([]string{"sudo", "-n"}, argv...)
			}
		}
		raw, _ := json.Marshal(argv)
		lampdArgs = append(lampdArgs, "--audit-release", string(raw))
	}
	// lampd's own host id, which its sandboxes carry: the fleet config's
	// host_id, else the single-host default. Never taken from the caller, so
	// it cannot disagree with lampd.
	st := installState{Name: *name, Service: *service, Current: m.Version, HighestVersion: m.Version, LampdArgs: lampdArgs}
	{
		st.HostID = "hs_local"
		for i, a := range lampdArgs {
			if a == "--fleet-config" && i+1 < len(lampdArgs) {
				var fc struct {
					HostID string `json:"host_id"`
				}
				if readFile(lampdArgs[i+1], &fc) && fc.HostID != "" {
					st.HostID = fc.HostID
				}
			}
		}
	}
	if len(supArgs) > 0 {
		sup := &supervisorState{Dir: *supDir, System: *supSystem, Args: supArgs, HostID: st.HostID}
		if sup.Dir == "" {
			sup.Dir = "/usr/local/lib/lamp-" + *name
		}
		for i, a := range supArgs {
			if a == "--socket" && i+1 < len(supArgs) {
				sup.Socket = supArgs[i+1]
			}
		}
		if sup.Socket == "" {
			return errors.New("--supervisor-arg needs --socket PATH")
		}
		st.Supervisor = sup
		if err := startSupervisor(p, &st, m.Version); err != nil {
			return err
		}
	}
	if err := saveState(p, st); err != nil {
		return err
	}
	if err := writeService(p, st, lampdJob(p, st)); err != nil {
		return err
	}
	if err := startService(p, st, lampdJob(p, st)); err != nil {
		return err
	}
	if *wait {
		if _, err := waitHealthy(p, 60*time.Second); err != nil {
			return err
		}
	}
	if *agent {
		self, err := os.Executable()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(self)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(p, "config", "lamp-installer"), data, 0755); err != nil {
			return err
		}
		if err := writeService(p, st, agentJob(p, st)); err != nil {
			return err
		}
		if err := startService(p, st, agentJob(p, st)); err != nil {
			return err
		}
	}
	event(p, map[string]any{"event": "installed", "version": m.Version, "service": *service, "prefix": p, "automatic_updates": false})
	return nil
}

func defaultService() string {
	if runtime.GOOS == "darwin" {
		return "launchd"
	}
	return "systemd"
}

func hasArg(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func hasWorker(args []string) bool {
	for _, a := range args {
		if a == "--worker-socket" {
			return true
		}
	}
	return false
}

// ---- update / rollback ----

func update(args []string) error {
	fl := flag.NewFlagSet("update", flag.ExitOnError)
	release := fl.String("release", "", "release directory")
	prefix := fl.String("prefix", "", "installation directory")
	fl.Parse(args)
	p, _ := filepath.Abs(*prefix)
	return applyUpdate(p, *release, "", "")
}

// applyUpdate applies a release. wantDigest, when set, is the release digest
// the fleet Update names; channel, when set, is the fleet channel the
// coordinator checked (else this host's policy channel).
func applyUpdate(p, relDir string, wantDigest, channel string) error {
	st, err := loadState(p)
	if err != nil {
		return err
	}
	pol := loadPolicy(p)
	key, err := readTrust(filepath.Join(p, "config", "release-trust.pub"))
	if err != nil {
		return err
	}
	_, m, digest, err := release.VerifyPlatform(relDir, key)
	if err == nil && wantDigest != "" && digest != wantDigest {
		err = fmt.Errorf("release digest %s is not the update's %s", digest, wantDigest)
	}
	if err == nil {
		err = release.CheckRevoked(filepath.Join(p, "mirror"), key, digest)
	}
	if err != nil {
		event(p, map[string]any{"event": "update_refused", "reason": err.Error(), "current": st.Current})
		return fmt.Errorf("refused before apply: %w", err)
	}
	if channel == "" {
		channel = pol.Channel
	}
	if m.Channel != channel {
		event(p, map[string]any{"event": "update_refused", "reason": "channel " + m.Channel + " is not " + channel, "current": st.Current})
		return fmt.Errorf("refused before apply: release channel %s, fleet follows %s", m.Channel, channel)
	}
	if !newer(m.Version, st.HighestVersion) {
		event(p, map[string]any{"event": "update_refused", "reason": "anti-rollback: " + m.Version + " is not newer than " + st.HighestVersion, "current": st.Current})
		return fmt.Errorf("refused before apply: %s is not newer than installed %s", m.Version, st.HighestVersion)
	}
	before, err := waitHealthy(p, 5*time.Second)
	if err != nil {
		return fmt.Errorf("host is not healthy before update: %w", err)
	}
	if _, err := stage(p, relDir, m, key); err != nil {
		return err
	}
	if err := engineGuard(p, st.Supervisor, st.Current, m.Version); err != nil {
		event(p, map[string]any{"event": "update_refused", "reason": err.Error(), "current": st.Current})
		return fmt.Errorf("refused before apply: %w", err)
	}
	event(p, map[string]any{"event": "draining", "from": st.Current, "to": m.Version, "saved_items": len(before)})
	old := st.Current
	var held []string
	event(p, map[string]any{"event": "applying", "version": m.Version})
	err = transition(p, &st, pol, m.Version, &held)
	var after []string
	if err == nil {
		after, err = waitHealthy(p, 90*time.Second)
	}
	if gone := lost(before, after); err == nil && len(gone) > 0 {
		err = fmt.Errorf("saved or active work lost or changed after update: %s", strings.Join(gone, "; "))
	}
	if err != nil {
		event(p, map[string]any{"event": "check_failed", "version": m.Version, "reason": err.Error()})
		if err := revert(p, &st, pol, old, before, &held); err != nil {
			event(p, map[string]any{"event": "failed", "reason": err.Error()})
			return exitErr(4)
		}
		event(p, map[string]any{"event": "rolled_back", "version": st.Current, "saved_items_preserved": len(before)})
		return exitErr(3)
	}
	st.HighestVersion = m.Version
	saveState(p, st)
	event(p, map[string]any{"event": "completed", "version": m.Version, "previous": old, "saved_items_preserved": len(after)})
	return nil
}

// revert returns to release `to` and checks it serves the same work.
func revert(p string, st *installState, pol updatePolicy, to string, before []string, held *[]string) error {
	if to == "" {
		return errors.New("no previous release to roll back to")
	}
	if err := transition(p, st, pol, to, held); err != nil {
		return err
	}
	after, err := waitHealthy(p, 90*time.Second)
	if err != nil {
		return err
	}
	if gone := lost(before, after); len(gone) > 0 {
		return fmt.Errorf("saved or active work lost or changed after rollback: %s", strings.Join(gone, "; "))
	}
	return nil
}

// rollbackTo is an owner-requested rollback to an earlier staged release
// (update_rollback). The release is checked again: its staged files against
// its signed manifest, and that manifest against a signed index in the mirror.
// On a failed check the host goes back to the release it was on.
func rollbackTo(p, version string) error {
	st, err := loadState(p)
	if err != nil {
		return err
	}
	pol := loadPolicy(p)
	key, err := readTrust(filepath.Join(p, "config", "release-trust.pub"))
	if err != nil {
		return err
	}
	dir := filepath.Join(p, "releases", version)
	ix, digest, err := release.FindByVersion(filepath.Join(p, "mirror"), key, version)
	if err == nil {
		err = release.CheckRevoked(filepath.Join(p, "mirror"), key, digest)
	}
	if err == nil {
		_, err = release.VerifyStaged(dir, key, ix.Platforms[release.Platform()])
	}
	if err == nil {
		err = engineGuard(p, st.Supervisor, st.Current, version)
	}
	if err != nil {
		event(p, map[string]any{"event": "rollback_refused", "reason": err.Error(), "current": st.Current})
		return fmt.Errorf("refused before rollback: %w", err)
	}
	if st.Current == version {
		return nil
	}
	before, err := waitHealthy(p, 5*time.Second)
	if err != nil {
		return fmt.Errorf("host is not healthy before rollback: %w", err)
	}
	from := st.Current
	var held []string
	event(p, map[string]any{"event": "rolling_back", "from": from, "to": version})
	err = revert(p, &st, pol, version, before, &held)
	if err != nil {
		event(p, map[string]any{"event": "check_failed", "version": version, "reason": err.Error()})
		if err := revert(p, &st, pol, from, before, &held); err != nil {
			return exitErr(4)
		}
		return exitErr(3)
	}
	event(p, map[string]any{"event": "rolled_back", "version": st.Current, "from": from, "saved_items_preserved": len(before)})
	return nil
}

func rollbackCmd(args []string) error {
	fl := flag.NewFlagSet("rollback", flag.ExitOnError)
	prefix := fl.String("prefix", "", "installation directory")
	fl.Parse(args)
	p, _ := filepath.Abs(*prefix)
	st, err := loadState(p)
	if err != nil {
		return err
	}
	before, _ := inventory(p)
	var held []string
	if err := revert(p, &st, loadPolicy(p), st.Previous, before, &held); err != nil {
		return err
	}
	event(p, map[string]any{"event": "rolled_back", "version": st.Current})
	return nil
}

// auto is what a periodic timer runs. It does nothing unless the owner
// turned automatic updates on.
func auto(args []string) error {
	fl := flag.NewFlagSet("auto", flag.ExitOnError)
	prefix := fl.String("prefix", "", "installation directory")
	release := fl.String("release", "", "newest release directory on the host's channel")
	fl.Parse(args)
	p, _ := filepath.Abs(*prefix)
	if !loadPolicy(p).Automatic {
		event("", map[string]any{"event": "auto_skipped", "reason": "automatic updates are disabled"})
		return nil
	}
	return applyUpdate(p, *release, "", "")
}

func policy(args []string) error {
	fl := flag.NewFlagSet("policy", flag.ExitOnError)
	prefix := fl.String("prefix", "", "installation directory")
	automatic := fl.String("automatic", "", "true|false")
	fl.Parse(args)
	p, _ := filepath.Abs(*prefix)
	pol := loadPolicy(p)
	switch *automatic {
	case "true":
		pol.Automatic = true
	case "false":
		pol.Automatic = false
	case "":
	default:
		return errors.New("--automatic is true or false")
	}
	pol.MaxUnavailable = 1
	raw, _ := json.MarshalIndent(pol, "", " ")
	if err := os.WriteFile(filepath.Join(p, "config", "update-policy.json"), raw, 0600); err != nil {
		return err
	}
	say("%s", raw)
	return nil
}

func status(args []string) error {
	fl := flag.NewFlagSet("status", flag.ExitOnError)
	prefix := fl.String("prefix", "", "installation directory")
	fl.Parse(args)
	p, _ := filepath.Abs(*prefix)
	st, err := loadState(p)
	if err != nil {
		return err
	}
	inv, ierr := inventory(p)
	out := map[string]any{"name": st.Name, "service": st.Service, "current": st.Current, "previous": st.Previous, "highest_version": st.HighestVersion, "update_policy": loadPolicy(p), "healthy": ierr == nil, "saved_items": len(inv)}
	if st.Supervisor != nil {
		paths, fromRelease := componentPaths(p, st.Supervisor, st.Supervisor.Version)
		sums := map[string]string{}
		for name, path := range paths {
			sums[name], _ = fileSum(path)
		}
		out["supervisor"] = map[string]any{"release": st.Supervisor.Version, "from_release": fromRelease, "binary_sha256": sums["lamp-supervisor"], "sha256": sums, "pid": st.Supervisor.PID, "alive": supAlive(st.Supervisor)}
	}
	raw, _ := json.Marshal(out)
	say("%s", raw)
	return nil
}

// ---- uninstall ----

func uninstall(args []string) error {
	fl := flag.NewFlagSet("uninstall", flag.ExitOnError)
	prefix := fl.String("prefix", "", "installation directory")
	data := fl.String("data", "", "owner data: retain | export | delete (required)")
	exportDir := fl.String("export-dir", "", "with --data export: new directory for the owner data copy")
	confirm := fl.String("confirm-delete", "", "with --data delete: the installation name")
	fl.Parse(args)
	p, _ := filepath.Abs(*prefix)
	st, err := loadState(p)
	if err != nil {
		return err
	}
	stateDir := filepath.Join(p, "state")
	switch *data {
	case "retain":
	case "export":
		if *exportDir == "" {
			return errors.New("--data export needs --export-dir")
		}
		if _, err := os.Stat(*exportDir); err == nil {
			return errors.New("--export-dir must not exist yet")
		}
	case "delete":
		if *confirm != st.Name {
			return fmt.Errorf("deleting owner data needs --confirm-delete %s", st.Name)
		}
	default:
		return errors.New("choose what happens to owner data: --data retain, --data export --export-dir DIR, or --data delete --confirm-delete NAME; nothing was changed")
	}
	stopService(p, st, agentJob(p, st), 60*time.Second)
	removeService(p, st, agentJob(p, st))
	if err := stopService(p, st, lampdJob(p, st), 60*time.Second); err != nil {
		return err
	}
	removeService(p, st, lampdJob(p, st))
	if st.Supervisor != nil {
		stopSupervisor(p, &st)
		sudo("rm", "-rf", st.Supervisor.Dir)
	}
	os.Remove(filepath.Join(p, "run", "api.sock"))
	if *data == "export" {
		n, err := copyTree(stateDir, *exportDir)
		if err != nil {
			return fmt.Errorf("export failed; owner data left in place: %w", err)
		}
		say("exported %d files of owner data to %s", n, *exportDir)
	}
	for _, d := range []string{"releases", "incoming", "mirror", "current", "run", "log", ".current.tmp", "config/lamp-installer"} {
		os.RemoveAll(filepath.Join(p, d))
	}
	kept := "retained at " + stateDir
	switch *data {
	case "delete":
		os.RemoveAll(p)
		kept = "deleted"
	default:
		// Keep the owner's enrollment and token beside the retained data so a
		// reinstall can reopen it; drop only installer bookkeeping.
		os.Remove(filepath.Join(p, "config", "install.json"))
	}
	event("", map[string]any{"event": "uninstalled", "name": st.Name, "owner_data": *data, "data": kept})
	return nil
}

func copyTree(src, dst string) (int, error) {
	n := 0
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0600); err != nil {
			return err
		}
		got, err := os.ReadFile(target)
		if err != nil || !bytes.Equal(got, data) {
			return fmt.Errorf("copy of %s does not verify", rel)
		}
		n++
		return nil
	})
	return n, err
}

// ---- rollout ----

// rollout updates hosts strictly one at a time over SSH. Each host's own
// installed lamp-installer verifies the release against its pinned key; the
// first failure or rollback stops the rollout, leaving later hosts untouched.
func rollout(args []string) error {
	fl := flag.NewFlagSet("rollout", flag.ExitOnError)
	var hosts multi
	fl.Var(&hosts, "host", "ssh-alias|local=prefix=release-dir (repeatable, in order)")
	fl.Parse(args)
	for i, h := range hosts {
		parts := strings.SplitN(h, "=", 3)
		if len(parts) != 3 {
			return fmt.Errorf("bad --host %q", h)
		}
		alias, prefix, rel := parts[0], parts[1], parts[2]
		event("", map[string]any{"event": "rollout_host", "index": i + 1, "of": len(hosts), "host": alias})
		var err error
		if alias == "local" {
			err = applyUpdate(prefix, rel, "", "")
		} else {
			remote := prefix + "/incoming/" + filepath.Base(rel)
			if err = run("ssh", alias, "mkdir -p "+shq(prefix+"/incoming")+" && rm -rf "+shq(remote)); err == nil {
				if err = run("scp", "-q", "-r", rel, alias+":"+remote); err == nil {
					err = run("ssh", alias, shq(prefix+"/current/bin/lamp-installer")+" update --prefix "+shq(prefix)+" --release "+shq(remote))
				}
			}
		}
		if err != nil {
			skipped := []string{}
			for _, rest := range hosts[i+1:] {
				skipped = append(skipped, strings.SplitN(rest, "=", 2)[0])
			}
			event("", map[string]any{"event": "rollout_stopped", "host": alias, "reason": err.Error(), "not_updated": skipped})
			return exitErr(3)
		}
	}
	event("", map[string]any{"event": "rollout_completed", "hosts": len(hosts)})
	return nil
}

func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// recoverCmd adopts sealed backup archives on a replacement host. lampd runs
// once with the owner's recovery key to import and verify them, rewrapping
// each data key to this host; the key is never written into the service
// definition or state. The normal service then serves the recovered backups.
func recoverCmd(args []string) error {
	fl := flag.NewFlagSet("recover", flag.ExitOnError)
	prefix := fl.String("prefix", "", "installation directory")
	archives := fl.String("archives", "", "directory of sealed .lbk archives")
	key := fl.String("recovery-key", "", "owner recovery key file")
	fl.Parse(args)
	p, _ := filepath.Abs(*prefix)
	st, err := loadState(p)
	if err != nil {
		return err
	}
	if err := stopService(p, st, lampdJob(p, st), 60*time.Second); err != nil {
		return err
	}
	once := st
	once.Service = "process"
	once.LampdArgs = append(append([]string{}, st.LampdArgs...), "--recovery-key-file", *key, "--recovery-import-dir", *archives)
	if err := startService(p, once, lampdJob(p, once)); err != nil {
		return err
	}
	inv, err := waitHealthy(p, 20*time.Second)
	stopService(p, once, lampdJob(p, once), 60*time.Second)
	if err != nil {
		startService(p, st, lampdJob(p, st))
		return fmt.Errorf("import failed, nothing imported (see %s): %w", filepath.Join(p, "log", "lampd.log"), err)
	}
	if err := startService(p, st, lampdJob(p, st)); err != nil {
		return err
	}
	if _, err := waitHealthy(p, 20*time.Second); err != nil {
		return err
	}
	event(p, map[string]any{"event": "recovered", "saved_items": len(inv)})
	return nil
}

// indexCmd signs the multi-platform index of a release version directory.
func indexCmd(args []string) error {
	fl := flag.NewFlagSet("index", flag.ExitOnError)
	dir := fl.String("release", "", "release version directory holding <os>-<arch>/ builds")
	key := fl.String("key", "", "private release signing key file")
	metadata := fl.Uint64("metadata-version", 1, "monotonic signed metadata version")
	var revoked multi
	fl.Var(&revoked, "revoke", "sha256:DIGEST=reason (repeatable): a revoked earlier release")
	fl.Parse(args)
	priv, err := signingKey(*key)
	if err != nil {
		return err
	}
	ix := release.Index{Format: release.IndexFormat, MetadataVersion: *metadata, CreatedAt: time.Now().UTC().Format(time.RFC3339), Platforms: map[string]string{}, Revocations: []release.Revocation{},
		Migration: release.Migration{LogVersion: 1, SnapshotVersion: 1, ProjectionVersion: 1, Phase: "expand", RollbackSafe: true}}
	for _, r := range revoked {
		d, reason, _ := strings.Cut(r, "=")
		ix.Revocations = append(ix.Revocations, release.Revocation{Digest: d, EffectiveAt: ix.CreatedAt, Reason: reason})
	}
	entries, _ := os.ReadDir(*dir)
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(*dir, e.Name(), "manifest.json"))
		if err != nil {
			continue
		}
		var m manifest
		if json.Unmarshal(raw, &m) != nil {
			return fmt.Errorf("%s: bad manifest", e.Name())
		}
		if ix.Version == "" {
			ix.Version, ix.Channel = m.Version, m.Channel
		} else if ix.Version != m.Version || ix.Channel != m.Channel {
			return errors.New("platform builds disagree on version or channel")
		}
		ix.Platforms[e.Name()] = release.Digest(raw)
	}
	if len(ix.Platforms) == 0 {
		return errors.New("no platform builds")
	}
	raw, _ := json.MarshalIndent(ix, "", " ")
	if err := os.WriteFile(filepath.Join(*dir, "index.json"), raw, 0644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*dir, "index.sig"), []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, raw))+"\n"), 0644); err != nil {
		return err
	}
	say("indexed %s (%d platforms) release_digest %s", ix.Version, len(ix.Platforms), release.Digest(raw))
	return nil
}

func signingKey(path string) (ed25519.PrivateKey, error) {
	seedRaw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(seedRaw)))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("invalid release signing key")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// publish copies a release into this host's mirror, where lampd and the
// updater find it by digest. Publishing verifies nothing is applied yet.
func publish(args []string) error {
	fl := flag.NewFlagSet("publish", flag.ExitOnError)
	prefix := fl.String("prefix", "", "installation directory")
	dir := fl.String("release", "", "release version directory")
	fl.Parse(args)
	p, _ := filepath.Abs(*prefix)
	target := filepath.Join(p, "mirror", filepath.Base(*dir))
	os.RemoveAll(target + ".tmp")
	if _, err := copyTree(*dir, target+".tmp"); err != nil {
		return err
	}
	os.RemoveAll(target)
	if err := os.Rename(target+".tmp", target); err != nil {
		return err
	}
	raw, _ := os.ReadFile(filepath.Join(target, "index.json"))
	event("", map[string]any{"event": "published", "release": filepath.Base(*dir), "release_digest": release.Digest(raw)})
	return nil
}

type agentRequest struct {
	UpdateID      string `json:"update_id"`
	ReleaseDigest string `json:"release_digest"`
	Version       string `json:"version"`
	Rollback      bool   `json:"rollback,omitempty"`
	Channel       string `json:"channel"`
}

type agentResult struct {
	UpdateID string `json:"update_id"`
	State    string `json:"state"` // completed | rolled_back | failed
	Version  string `json:"version"`
	Detail   string `json:"detail,omitempty"`
	At       string `json:"at"`
}

// agentCmd is the updater service. lampd writes update-request.json when
// the fleet's Update reaches this host; the agent drains, applies, checks
// and if needed rolls back, then leaves update-result.json for lampd to
// commit to the fleet. It never decides which host goes next.
func agentCmd(args []string) error {
	fl := flag.NewFlagSet("agent", flag.ExitOnError)
	prefix := fl.String("prefix", "", "installation directory")
	fl.Parse(args)
	p, _ := filepath.Abs(*prefix)
	reqPath := filepath.Join(p, "config", "update-request.json")
	resPath := filepath.Join(p, "config", "update-result.json")
	say("lamp-installer agent watching %s", reqPath)
	waiting := map[string]time.Time{}
	for {
		time.Sleep(time.Second)
		raw, err := os.ReadFile(reqPath)
		if err != nil {
			continue
		}
		var req agentRequest
		if json.Unmarshal(raw, &req) != nil || req.UpdateID == "" {
			os.Remove(reqPath)
			continue
		}
		if old, err := os.ReadFile(resPath); err == nil {
			var r agentResult
			if json.Unmarshal(old, &r) == nil && r.UpdateID == req.UpdateID {
				continue // lampd has not collected it yet
			}
		}
		res := agentResult{UpdateID: req.UpdateID, Version: req.Version}
		key, err := readTrust(filepath.Join(p, "config", "release-trust.pub"))
		var dir string
		if err == nil && !req.Rollback {
			dir, _, err = release.FindByDigest(filepath.Join(p, "mirror"), key, req.ReleaseDigest)
			if err != nil {
				// The release may still be on its way to this host's mirror.
				if first, ok := waiting[req.UpdateID]; !ok {
					waiting[req.UpdateID] = time.Now()
					continue
				} else if time.Since(first) < 10*time.Minute {
					continue
				}
			}
		}
		if req.Rollback {
			event(p, map[string]any{"event": "agent_rolling_back", "update_id": req.UpdateID, "version": req.Version})
			err = rollbackTo(p, req.Version)
		} else if err == nil {
			event(p, map[string]any{"event": "agent_applying", "update_id": req.UpdateID, "version": req.Version})
			err = applyUpdate(p, dir, req.ReleaseDigest, req.Channel)
		}
		var ex exitErr
		switch {
		case err == nil:
			res.State = "completed"
		case errors.As(err, &ex) && ex == 3 && req.Rollback:
			res.State = "failed"
			res.Detail = "rollback check failed; host returned to the release it was on"
		case errors.As(err, &ex) && ex == 3:
			res.State = "rolled_back"
			res.Detail = "check failed; previous release restored with saved and active work"
		default:
			res.State = "failed"
			res.Detail = err.Error()
		}
		st, _ := loadState(p)
		if res.State != "completed" {
			res.Version = st.Current
			// Never leave the host without its daemon after a failed attempt.
			if _, herr := waitHealthy(p, 3*time.Second); herr != nil {
				startService(p, st, lampdJob(p, st))
				waitHealthy(p, 90*time.Second)
			}
		}
		res.At = time.Now().UTC().Format(time.RFC3339)
		out, _ := json.Marshal(res)
		os.WriteFile(resPath+".tmp", out, 0600)
		os.Rename(resPath+".tmp", resPath)
		os.Remove(reqPath)
		event(p, map[string]any{"event": "agent_result", "update_id": res.UpdateID, "state": res.State, "version": res.Version})
	}
}

func restartCmd(args []string) error {
	fl := flag.NewFlagSet("restart", flag.ExitOnError)
	prefix := fl.String("prefix", "", "installation directory")
	fl.Parse(args)
	p, _ := filepath.Abs(*prefix)
	st, err := loadState(p)
	if err != nil {
		return err
	}
	before, err := inventory(p)
	if err != nil {
		return err
	}
	if err := restartService(p, st, lampdJob(p, st)); err != nil {
		return err
	}
	time.Sleep(time.Second)
	after, err := waitHealthy(p, 90*time.Second)
	if err != nil {
		return err
	}
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		return errors.New("saved work differs after restart")
	}
	event(p, map[string]any{"event": "restarted", "service": st.Service, "saved_items_preserved": len(after)})
	return nil
}

func readFile(path string, v any) bool {
	raw, err := os.ReadFile(path)
	return err == nil && json.Unmarshal(raw, v) == nil
}

// defaultGuardAuditDir is the Linux engine's guard audit spool root; Macs have
// no host guard spools.
func defaultGuardAuditDir() string {
	if runtime.GOOS == "linux" {
		return "/var/lib/lamp-audit"
	}
	return ""
}
