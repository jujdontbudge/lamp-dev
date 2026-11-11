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
)

const manifestFormat = "lamp.release/v1"
const trustPrefix = "lamp-release-ed25519 "

type fileEntry struct {
	SHA256 string `json:"sha256"`
	Mode   uint32 `json:"mode"`
	Size   int64  `json:"size"`
}

type component struct {
	Path    string `json:"path"`
	Version string `json:"version"`
	Sum     string `json:"sum,omitempty"`
}

type manifest struct {
	Format     string                 `json:"format"`
	Version    string                 `json:"version"`
	Channel    string                 `json:"channel"`
	OS         string                 `json:"os"`
	Arch       string                 `json:"arch"`
	CreatedAt  string                 `json:"created_at"`
	Files      map[string]fileEntry   `json:"files"`
	Components map[string][]component `json:"components"`
}

// installState is <prefix>/config/install.json.
type installState struct {
	Name           string   `json:"name"`
	Service        string   `json:"service"`
	Current        string   `json:"current"`
	Previous       string   `json:"previous,omitempty"`
	HighestVersion string   `json:"highest_version"`
	LampdArgs      []string `json:"lampd_args"`
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
		"status": status, "uninstall": uninstall, "rollout": rollout, "recover": recoverCmd,
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
	fmt.Fprintln(os.Stderr, "usage: lamp-installer keygen|recovery-keygen|build|verify|install|update|auto|policy|rollback|status|uninstall|rollout|recover [flags]")
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
	line := trustPrefix + base64.StdEncoding.EncodeToString(pub) + "\n"
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

func readTrust(path string) (ed25519.PublicKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	line := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(line, trustPrefix) {
		return nil, errors.New("not a lamp release trust file")
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, trustPrefix))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, errors.New("not a lamp release trust file")
	}
	return b, nil
}

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
	var files multi
	fl.Var(&files, "file", "payload/path=source (repeatable); bin/lampd and bin/lamp-installer are required")
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
	m := manifest{Format: manifestFormat, Version: *version, Channel: *channel, OS: *goos, Arch: *arch, CreatedAt: time.Now().UTC().Format(time.RFC3339), Files: map[string]fileEntry{}, Components: map[string][]component{}}
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
	for _, req := range []string{"bin/lampd", "bin/lamp-installer", "LICENSE", "THIRD_PARTY.md"} {
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

func parseVersion(v string) ([3]int, error) {
	var out [3]int
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, fmt.Errorf("version %q is not a.b.c", v)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, fmt.Errorf("version %q is not a.b.c", v)
		}
		out[i] = n
	}
	return out, nil
}

func newer(a, b string) bool {
	x, _ := parseVersion(a)
	y, _ := parseVersion(b)
	for i := range x {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}

// verifyRelease checks trust, platform and every payload file before apply.
func verifyRelease(dir string, trust ed25519.PublicKey) (manifest, error) {
	var m manifest
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return m, err
	}
	sigRaw, err := os.ReadFile(filepath.Join(dir, "manifest.sig"))
	if err != nil {
		return m, errors.New("release is unsigned")
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigRaw)))
	if err != nil || !ed25519.Verify(trust, raw, sig) {
		return m, errors.New("release signature does not verify against the pinned release key")
	}
	if err := json.Unmarshal(raw, &m); err != nil || m.Format != manifestFormat {
		return m, errors.New("unknown release manifest format")
	}
	if _, err := parseVersion(m.Version); err != nil {
		return m, err
	}
	if m.OS != runtime.GOOS || m.Arch != runtime.GOARCH {
		return m, fmt.Errorf("release is for %s/%s, host is %s/%s", m.OS, m.Arch, runtime.GOOS, runtime.GOARCH)
	}
	seen := 0
	err = filepath.WalkDir(filepath.Join(dir, "payload"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(filepath.Join(dir, "payload"), p)
		e, ok := m.Files[rel]
		if !ok || !d.Type().IsRegular() {
			return fmt.Errorf("payload file %s is not in the signed manifest", rel)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != e.SHA256 {
			return fmt.Errorf("payload file %s does not match its signed digest", rel)
		}
		seen++
		return nil
	})
	if err != nil {
		return m, err
	}
	if seen != len(m.Files) {
		return m, errors.New("payload is missing signed files")
	}
	return m, nil
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
func stage(prefix, dir string, m manifest) (string, error) {
	target := filepath.Join(prefix, "releases", m.Version)
	if _, err := os.Stat(target); err == nil {
		return "", fmt.Errorf("release %s is already staged", m.Version)
	}
	tmp := target + ".staging"
	os.RemoveAll(tmp)
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

func label(st installState) string    { return "si.eilmer.lamp." + st.Name }
func unitName(st installState) string { return "lamp-" + st.Name + ".service" }

func lampdArgv(prefix string, st installState) []string {
	return append([]string{filepath.Join(prefix, "current", "bin", "lampd")}, st.LampdArgs...)
}

func sudo(args ...string) error {
	cmd := exec.Command("sudo", append([]string{"-n"}, args...)...)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	return cmd.Run()
}

func writeService(prefix string, st installState) error {
	argv := lampdArgv(prefix, st)
	switch st.Service {
	case "systemd":
		quoted := make([]string, len(argv))
		for i, a := range argv {
			quoted[i] = strconv.Quote(a)
		}
		unit := fmt.Sprintf("[Unit]\nDescription=Lamp control daemon (%s)\nAfter=network-online.target\n\n[Service]\nUser=%d\nExecStart=%s\nRestart=on-failure\nRestartSec=2\nKillSignal=SIGTERM\nTimeoutStopSec=60\n\n[Install]\nWantedBy=multi-user.target\n", st.Name, os.Getuid(), strings.Join(quoted, " "))
		tmp := filepath.Join(prefix, "config", unitName(st))
		if err := os.WriteFile(tmp, []byte(unit), 0644); err != nil {
			return err
		}
		if err := sudo("install", "-m", "0644", tmp, "/etc/systemd/system/"+unitName(st)); err != nil {
			return err
		}
		if err := sudo("systemctl", "daemon-reload"); err != nil {
			return err
		}
		return sudo("systemctl", "enable", unitName(st))
	case "launchd":
		var b strings.Builder
		for _, a := range argv {
			b.WriteString("<string>" + xmlEscape(a) + "</string>")
		}
		plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>Label</key><string>%s</string><key>ProgramArguments</key><array>%s</array><key>RunAtLoad</key><true/><key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict><key>StandardErrorPath</key><string>%s</string></dict></plist>`, label(st), b.String(), xmlEscape(filepath.Join(prefix, "log", "lampd.log")))
		home, _ := os.UserHomeDir()
		return os.WriteFile(filepath.Join(home, "Library", "LaunchAgents", label(st)+".plist"), []byte(plist), 0644)
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

func pidFile(prefix string) string { return filepath.Join(prefix, "run", "lampd.pid") }

func startService(prefix string, st installState) error {
	switch st.Service {
	case "systemd":
		return sudo("systemctl", "start", unitName(st))
	case "launchd":
		home, _ := os.UserHomeDir()
		return exec.Command("launchctl", "bootstrap", "gui/"+strconv.Itoa(os.Getuid()), filepath.Join(home, "Library", "LaunchAgents", label(st)+".plist")).Run()
	}
	argv := lampdArgv(prefix, st)
	logf, err := os.OpenFile(filepath.Join(prefix, "log", "lampd.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	pid := cmd.Process.Pid
	cmd.Process.Release()
	return os.WriteFile(pidFile(prefix), []byte(strconv.Itoa(pid)), 0600)
}

// stopService drains lampd with SIGTERM: the journal is durable, so active
// and saved work is reloaded by the next daemon.
func stopService(prefix string, st installState, drain time.Duration) error {
	switch st.Service {
	case "systemd":
		return sudo("systemctl", "stop", unitName(st))
	case "launchd":
		exec.Command("launchctl", "bootout", "gui/"+strconv.Itoa(os.Getuid())+"/"+label(st)).Run()
		return nil
	}
	raw, err := os.ReadFile(pidFile(prefix))
	if err != nil {
		return nil
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	if pid <= 1 {
		return nil
	}
	proc, _ := os.FindProcess(pid)
	if proc.Signal(syscall.SIGTERM) != nil {
		os.Remove(pidFile(prefix))
		return nil
	}
	deadline := time.Now().Add(drain)
	for time.Now().Before(deadline) {
		if proc.Signal(syscall.Signal(0)) != nil {
			os.Remove(pidFile(prefix))
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("lampd %d did not drain within %s", pid, drain)
}

func removeService(prefix string, st installState) {
	switch st.Service {
	case "systemd":
		sudo("systemctl", "disable", unitName(st))
		sudo("rm", "-f", "/etc/systemd/system/"+unitName(st))
		sudo("systemctl", "daemon-reload")
	case "launchd":
		home, _ := os.UserHomeDir()
		os.Remove(filepath.Join(home, "Library", "LaunchAgents", label(st)+".plist"))
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

// inventory is the owner's saved and active work as the API reports it.
func inventory(prefix string) ([]string, error) {
	var rows []string
	for _, kind := range []string{"sandboxes", "volumes", "caches", "artifacts", "backups", "blobs"} {
		out, err := apiGet(prefix, "/v1/"+kind+"?limit=500")
		if err != nil {
			return nil, err
		}
		items, _ := out["items"].([]any)
		for _, it := range items {
			m, _ := it.(map[string]any)
			rows = append(rows, fmt.Sprintf("%s %v %v %v %v", kind, m["id"], m["state"], m["revision"], firstOf(m, "manifest_digest", "sha256", "generation_digest")))
		}
	}
	sort.Strings(rows)
	return rows, nil
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
	var extra multi
	fl.Var(&extra, "lampd-arg", "extra lampd argument (repeatable), e.g. enrolled worker flags")
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
	for _, d := range []string{"config", "releases", "run", "log", "state"} {
		if err := os.MkdirAll(filepath.Join(p, d), 0700); err != nil {
			return err
		}
	}
	trustRaw, _ := os.ReadFile(*trust)
	os.WriteFile(filepath.Join(p, "config", "release-trust.pub"), trustRaw, 0644)
	// The owner bearer: only its SHA-256 goes to lampd's enrollment.
	tokenPath := filepath.Join(p, "config", "owner.token")
	if _, err := os.Stat(tokenPath); err != nil {
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
	lampdArgs := []string{"--socket", filepath.Join(p, "run", "api.sock"), "--state-dir", filepath.Join(p, "state", "control"), "--identity-file", filepath.Join(p, "config", "identity.json")}
	if *recovery != "" {
		raw, err := os.ReadFile(*recovery)
		if err != nil {
			return err
		}
		os.WriteFile(filepath.Join(p, "config", "recovery.pub"), raw, 0644)
		lampdArgs = append(lampdArgs, "--recovery-public-key", filepath.Join(p, "config", "recovery.pub"))
	}
	if !hasWorker(extra) {
		// Control-only host until a supervisor is enrolled.
		lampdArgs = append(lampdArgs, "--worker-socket", filepath.Join(p, "run", "worker.sock"), "--worker-id", "unenrolled", "--worker-boot-id", "unenrolled", "--grant-id", "none", "--worker-uid", strconv.Itoa(os.Getuid()), "--worker-pid", "1")
	}
	lampdArgs = append(lampdArgs, extra...)
	if _, err := stage(p, *release, m); err != nil {
		return err
	}
	if err := point(p, m.Version); err != nil {
		return err
	}
	st := installState{Name: *name, Service: *service, Current: m.Version, HighestVersion: m.Version, LampdArgs: lampdArgs}
	if err := saveState(p, st); err != nil {
		return err
	}
	if err := writeService(p, st); err != nil {
		return err
	}
	if err := startService(p, st); err != nil {
		return err
	}
	if _, err := waitHealthy(p, 20*time.Second); err != nil {
		return err
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
	return applyUpdate(p, *release)
}

func applyUpdate(p, release string) error {
	st, err := loadState(p)
	if err != nil {
		return err
	}
	pol := loadPolicy(p)
	key, err := readTrust(filepath.Join(p, "config", "release-trust.pub"))
	if err != nil {
		return err
	}
	m, err := verifyRelease(release, key)
	if err != nil {
		event(p, map[string]any{"event": "update_refused", "reason": err.Error(), "current": st.Current})
		return fmt.Errorf("refused before apply: %w", err)
	}
	if m.Channel != pol.Channel {
		return fmt.Errorf("refused before apply: release channel %s, host follows %s", m.Channel, pol.Channel)
	}
	if !newer(m.Version, st.HighestVersion) {
		event(p, map[string]any{"event": "update_refused", "reason": "anti-rollback: " + m.Version + " is not newer than " + st.HighestVersion, "current": st.Current})
		return fmt.Errorf("refused before apply: %s is not newer than installed %s", m.Version, st.HighestVersion)
	}
	before, err := waitHealthy(p, 5*time.Second)
	if err != nil {
		return fmt.Errorf("host is not healthy before update: %w", err)
	}
	if _, err := stage(p, release, m); err != nil {
		return err
	}
	event(p, map[string]any{"event": "draining", "from": st.Current, "to": m.Version, "saved_items": len(before)})
	if err := stopService(p, st, time.Duration(pol.DrainTimeoutMs)*time.Millisecond); err != nil {
		return err
	}
	old := st.Current
	if err := point(p, m.Version); err != nil {
		return err
	}
	st.Previous, st.Current = old, m.Version
	saveState(p, st)
	event(p, map[string]any{"event": "applying", "version": m.Version})
	startErr := startService(p, st)
	var after []string
	if startErr == nil {
		after, startErr = waitHealthy(p, 15*time.Second)
	}
	if startErr == nil && strings.Join(before, "\n") != strings.Join(after, "\n") {
		startErr = fmt.Errorf("saved work differs after update (%d items before, %d after)", len(before), len(after))
	}
	if startErr != nil {
		event(p, map[string]any{"event": "check_failed", "version": m.Version, "reason": startErr.Error()})
		if err := revert(p, &st, pol, before); err != nil {
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

// revert returns to the previous release and checks it serves the same work.
func revert(p string, st *installState, pol updatePolicy, before []string) error {
	if st.Previous == "" {
		return errors.New("no previous release to roll back to")
	}
	stopService(p, *st, time.Duration(pol.DrainTimeoutMs)*time.Millisecond)
	if err := point(p, st.Previous); err != nil {
		return err
	}
	st.Current, st.Previous = st.Previous, st.Current
	saveState(p, *st)
	if err := startService(p, *st); err != nil {
		return err
	}
	after, err := waitHealthy(p, 15*time.Second)
	if err != nil {
		return err
	}
	if before != nil && strings.Join(before, "\n") != strings.Join(after, "\n") {
		return errors.New("saved work differs after rollback")
	}
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
	if err := revert(p, &st, loadPolicy(p), before); err != nil {
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
	return applyUpdate(p, *release)
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
	if err := stopService(p, st, 60*time.Second); err != nil {
		return err
	}
	removeService(p, st)
	os.Remove(filepath.Join(p, "run", "api.sock"))
	if *data == "export" {
		n, err := copyTree(stateDir, *exportDir)
		if err != nil {
			return fmt.Errorf("export failed; owner data left in place: %w", err)
		}
		say("exported %d files of owner data to %s", n, *exportDir)
	}
	for _, d := range []string{"releases", "incoming", "current", "run", "log", ".current.tmp"} {
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
			err = applyUpdate(prefix, rel)
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
	if err := stopService(p, st, 60*time.Second); err != nil {
		return err
	}
	once := st
	once.Service = "process"
	once.LampdArgs = append(append([]string{}, st.LampdArgs...), "--recovery-key-file", *key, "--recovery-import-dir", *archives)
	if err := startService(p, once); err != nil {
		return err
	}
	inv, err := waitHealthy(p, 20*time.Second)
	stopService(p, once, 60*time.Second)
	if err != nil {
		startService(p, st)
		return fmt.Errorf("import failed, nothing imported (see %s): %w", filepath.Join(p, "log", "lampd.log"), err)
	}
	if err := startService(p, st); err != nil {
		return err
	}
	if _, err := waitHealthy(p, 20*time.Second); err != nil {
		return err
	}
	event(p, map[string]any{"event": "recovered", "saved_items": len(inv)})
	return nil
}
