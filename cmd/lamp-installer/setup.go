package main

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"lamp.eilmer.si/internal/release"
	"lamp.eilmer.si/pkg/api"
)

// All engine state is separate from /usr/local/lib/lamp and the system store.
// Privileged commands are noninteractive: lack of authority is an error.
func rootOutput(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	if os.Geteuid() != 0 {
		cmd = exec.Command("sudo", append([]string{"-n", name}, args...)...)
	}
	cmd.Stderr = os.Stderr
	return cmd.Output()
}
func rootRun(name string, args ...string) error { _, err := rootOutput(name, args...); return err }

// VZ saved state uses the console login session's Secure Enclave keys. SSH's
// bootstrap context cannot save it, even after changing the worker UID.
// asuser adopts that context without installing or starting any launch item.
func engineOutput(name string, args ...string) ([]byte, error) {
	if runtime.GOOS == "darwin" {
		return rootOutput("/bin/launchctl", append([]string{"asuser", strconv.Itoa(os.Getuid()), name}, args...)...)
	}
	return rootOutput(name, args...)
}

func engineLocation(p string) (string, string, uint32) {
	sum := sha256.Sum256([]byte(p))
	id := hex.EncodeToString(sum[:4])
	base, run := "/var/lib/lamp-i/"+id, "/run/lamp-"+id
	if runtime.GOOS == "darwin" {
		base, run = "/private/var/lamp-i/"+id, "/private/var/run/lamp-"+id
	}
	return base, run, 65536 + (binary.BigEndian.Uint32(sum[:4])%50000)*65536
}

func enginePreflight(payload string, gib uint64) error {
	if gib < 80 || gib > 1048576 {
		return fmt.Errorf("--store-gib must be between 80 and 1048576 (20 GiB per sandbox plus templates and saved state)")
	}
	names := append(componentNames(), "lamp-supervisor")
	for _, n := range names {
		if _, err := os.Stat(filepath.Join(payload, "bin", n)); err != nil {
			return fmt.Errorf("release lacks %s; use a release containing the sandbox engine", n)
		}
	}
	if _, err := os.Stat(filepath.Join(payload, "seed", "seed.json")); err != nil {
		return fmt.Errorf("release lacks a default seed; use a complete release")
	}
	if runtime.GOOS == "linux" {
		if _, err := os.Stat("/dev/kvm"); err != nil {
			return fmt.Errorf("this host has no /dev/kvm; this installer requires KVM and does not silently run code on the host; enable virtualization or install on a KVM host")
		}
		missing := false
		for _, n := range []string{"mkfs.xfs", "mount", "mountpoint", "truncate", "nft", "nsenter", "setpriv", "pasta", "ip", "python3"} {
			if _, err := exec.LookPath(n); err != nil {
				missing = true
			}
		}
		if missing {
			if _, err := exec.LookPath("apt-get"); err != nil {
				return fmt.Errorf("install host packages xfsprogs nftables util-linux passt iproute2 python3, then retry")
			}
			if os.Geteuid() != 0 {
				if err := exec.Command("sudo", "-n", "true").Run(); err != nil {
					return fmt.Errorf("host dependencies need noninteractive administrator access; sudo -n is unavailable")
				}
			}
			say("Installing host dependencies: xfsprogs nftables util-linux passt iproute2 python3")
			if err := rootRun("apt-get", "update", "-qq"); err != nil {
				return fmt.Errorf("could not update host package index: %w", err)
			}
			if err := rootRun("apt-get", "install", "-y", "--no-install-recommends", "xfsprogs", "nftables", "util-linux", "passt", "iproute2", "python3"); err != nil {
				return fmt.Errorf("could not install host dependencies: %w", err)
			}
		}
	} else if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return fmt.Errorf("engine requires Linux KVM or Apple Silicon macOS 26+")
	} else {
		version, err := exec.Command("sw_vers", "-productVersion").Output()
		major, _ := strconv.Atoi(strings.Split(strings.TrimSpace(string(version)), ".")[0])
		if err != nil || major < 26 {
			return fmt.Errorf("engine requires macOS 26 or later")
		}
		if _, err := defaultMacMemory(filepath.Join(payload, "bin", "lamp-vz")); err != nil {
			return err
		}
	}
	if os.Geteuid() != 0 {
		if err := exec.Command("sudo", "-n", "true").Run(); err != nil {
			return fmt.Errorf("engine setup needs noninteractive administrator access; sudo -n is unavailable (no password prompt was opened)")
		}
	}
	return nil
}

// Use the engine's observation and physical reserve, including its 256 MiB
// VMM charge. Leave a quarter of currently available pages for host activity.
func defaultMacMemory(worker string) (uint64, error) {
	raw, err := exec.Command(worker, "memory").Output()
	if err != nil {
		return 0, fmt.Errorf("cannot observe host memory: %w", err)
	}
	var m struct {
		Physical  uint64 `json:"physical_mib"`
		Available uint64 `json:"available_mib"`
		Reserve   uint64 `json:"host_reserve_mib"`
		Headroom  uint64 `json:"available_reserve_mib"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return 0, err
	}
	physicalBudget := uint64(0)
	if m.Physical > m.Reserve {
		physicalBudget = m.Physical - m.Reserve
	}
	availableBudget := uint64(0)
	if m.Available > m.Headroom {
		availableBudget = m.Available - m.Headroom
	}
	for _, guest := range []uint64{2048, 1024, 512, 256} {
		if guest+256 <= availableBudget && guest+256 <= physicalBudget {
			say("Host memory: physical=%d MiB, available=%d MiB, physical reserve=%d MiB, available headroom=%d MiB; default guest=%d MiB plus 256 MiB VMM allowance", m.Physical, m.Available, m.Reserve, m.Headroom, guest)
			return guest, nil
		}
	}
	return 0, fmt.Errorf("NO_CAPACITY: smallest first-run guest is 256 MiB plus 256 MiB VMM allowance; physical=%d MiB, available=%d MiB, physical reserve=%d MiB, available headroom=%d MiB; need at least 768 MiB available to retain host headroom; retry when memory is available or use another host", m.Physical, m.Available, m.Reserve, m.Headroom)
}

func setupEngine(p string, st *installState, gib uint64) (setupErr error) {
	base, runDir, uidBase := engineLocation(p)
	if st.Supervisor != nil {
		return fmt.Errorf("this prefix already has an enrolled engine; use lamp-installer restart or update")
	}
	if err := rootRun("mkdir", "-p", filepath.Dir(base)); err != nil {
		return err
	}
	// mkdir without -p refuses an existing engine, including a partial setup.
	if err := rootRun("mkdir", "-m", "0755", base); err != nil {
		return fmt.Errorf("engine directory %s exists or is unwritable; inspect it before retrying: %w", base, err)
	}
	var createdRecords []string
	defer func() {
		if setupErr != nil {
			if st.Supervisor != nil {
				if err := stopSupervisor(p, st); err != nil {
					fmt.Fprintln(os.Stderr, "engine cleanup:", err)
					return
				}
			}
			if runtime.GOOS == "darwin" {
				// A failed setup may have created only part of the account pool.
				// Remove only records this attempt established were absent.
				for i := len(createdRecords) - 1; i >= 0; i-- {
					if err := rootRun("dscl", ".", "-delete", createdRecords[i]); err != nil {
						fmt.Fprintln(os.Stderr, "engine account cleanup:", err)
					}
				}
				if err := rootRun("rm", "-rf", base, runDir); err != nil {
					fmt.Fprintln(os.Stderr, "engine cleanup:", err)
				}
				return
			}
			if err := removeManagedEngine(p); err != nil {
				fmt.Fprintln(os.Stderr, "engine cleanup:", err)
			}
		}
	}()
	if err := rootRun("mkdir", "-p", runDir); err != nil {
		return err
	}
	if err := rootRun("chmod", "0755", runDir); err != nil {
		return err
	}
	sup := &supervisorState{Dir: filepath.Join(base, "lib"), System: filepath.Join(base, "lib", st.Current), HostID: st.HostID, Socket: filepath.Join(runDir, "w.sock")}
	if err := installSupervisor(p, sup, st.Current); err != nil {
		return err
	}
	paths, _ := componentPaths(p, sup, st.Current)
	store := filepath.Join(base, "store")
	if err := rootRun("mkdir", "-p", store); err != nil {
		return err
	}
	args := []string{"--store", store}
	if runtime.GOOS == "linux" {
		// Reserve this UID range across prefix-scoped installs. A hash collision
		// must refuse setup rather than give two engines the same worker identity.
		reservation := filepath.Join(filepath.Dir(base), "uid-"+strconv.FormatUint(uint64(uidBase), 10))
		if err := rootRun("ln", "-s", base, reservation); err != nil {
			return fmt.Errorf("worker UID range is already reserved; choose a different prefix: %w", err)
		}

		say("Preparing a %d GiB sparse XFS engine store at %s (no existing engine is changed)", gib, store)
		image := filepath.Join(base, "store.img")
		if err := rootRun("truncate", "-s", strconv.FormatUint(gib, 10)+"G", image); err != nil {
			return err
		}
		if err := rootRun("mkfs.xfs", "-q", "-m", "reflink=1", image); err != nil {
			return err
		}
		if err := rootRun("mount", "-o", "loop", image, store); err != nil {
			return err
		}
		args = append(args, "--mem-dir", filepath.Join(runDir, "mem"), "--netns-dir", filepath.Join(runDir, "net"), "--uid-base", strconv.FormatUint(uint64(uidBase), 10), "--host-id", st.HostID, "--firecracker", paths["firecracker"], "--jailer", paths["jailer"])
		pasta, _ := exec.LookPath("pasta")
		network, _ := json.Marshal(map[string]any{"guard": paths["lamp-guard"], "pasta": pasta, "audit_dir": filepath.Join(base, "audit"), "revision": 1, "principal_id": "pr_local", "exceptions": []any{}})
		policy := filepath.Join(p, "config", "network.json")
		if err := writePrivate(policy, network); err != nil {
			return err
		}
		if err := rootRun("install", "-m", "0644", policy, filepath.Join(base, "network.json")); err != nil {
			return err
		}
		args = append(args, "--network-policy", filepath.Join(base, "network.json"))
	} else {
		// No 4 GiB RAM disk on the 8 GiB Air, and no global capture directory.
		// Each install gets two distinct hidden worker accounts.
		first := int(uidBase/65536)*2 + 100
		for i := first; i < first+2; i++ {
			name := fmt.Sprintf("_lampvm%02d", i)
			for _, kind := range []string{"/Users/", "/Groups/"} {
				if exec.Command("dscl", ".", "-read", kind+name).Run() == nil {
					return fmt.Errorf("worker account %s exists; refusing to share its identity", kind+name)
				}
			}
			uid := strconv.Itoa(100000 + i)
			for _, entry := range []struct{ path, key, value string }{
				{"/Groups/" + name, "PrimaryGroupID", uid},
				{"/Users/" + name, "UniqueID", uid}, {"/Users/" + name, "PrimaryGroupID", uid},
				{"/Users/" + name, "UserShell", "/usr/bin/false"}, {"/Users/" + name, "NFSHomeDirectory", "/var/empty"},
				{"/Users/" + name, "IsHidden", "1"}, {"/Users/" + name, "Password", "*"},
			} {
				if len(createdRecords) == 0 || createdRecords[len(createdRecords)-1] != entry.path {
					createdRecords = append(createdRecords, entry.path)
				}
				if err := rootRun("dscl", ".", "-create", entry.path, entry.key, entry.value); err != nil {
					return err
				}
			}
		}
		args = append(args, "--worker", paths["lamp-vz"], "--ram-dir", "", "--identities", fmt.Sprintf("%d-%d", first, first+1))
	}
	seed := filepath.Join(p, "current", "seed")
	rootSeed := filepath.Join(base, "seed")
	cpArgs := []string{"--reflink=auto", "--sparse=always", "-R", seed, rootSeed}
	if runtime.GOOS == "darwin" {
		cpArgs = []string{"-cR", seed, rootSeed}
	}
	say("Importing the signed default Linux seed and preparing its template…")
	if err := rootRun("cp", cpArgs...); err != nil {
		return err
	}
	if err := rootRun(filepath.Join(p, "current", "bin", "lamp-installer"), "unpack-seed", "--dir", rootSeed); err != nil {
		return err
	}
	var built []byte
	var err error
	memoryMiB := uint64(2048)
	if runtime.GOOS == "linux" {
		if _, err = rootOutput(paths["lamp-supervisor"], append([]string{"seed-import", rootSeed}, args...)...); err != nil {
			return err
		}
		raw, e := os.ReadFile(filepath.Join(seed, "seed.json"))
		if e != nil {
			return e
		}
		var meta struct{ Name, Arch string }
		if e = json.Unmarshal(raw, &meta); e != nil {
			return e
		}
		sum := sha256.Sum256(raw)
		def := fmt.Sprintf("format=\"lamp.template/v1\"\nname=\"default\"\nguest_os=\"linux\"\narch=%q\nseed_ref=%q\nmemory_mib=2048\n", meta.Arch, "lamp-seed/"+meta.Name+"@sha256:"+hex.EncodeToString(sum[:]))
		file := filepath.Join(p, "config", "default.lamp")
		if err = writePrivate(file, []byte(def)); err != nil {
			return err
		}
		built, err = rootOutput(paths["lamp-supervisor"], append([]string{"template-build", file}, args...)...)
	} else {
		memoryMiB, err = defaultMacMemory(paths["lamp-vz"])
		if err != nil {
			return err
		}
		built, err = engineOutput(paths["lamp-supervisor"], append([]string{"template-prepare", rootSeed, "default", "--memory-mib", strconv.FormatUint(memoryMiB, 10), "--vcpus", "2", "--disk-gib", "20"}, args...)...)
	}
	if err != nil {
		return fmt.Errorf("default template preparation failed: %w", err)
	}
	var result struct {
		ID string `json:"template_id"`
	}
	if err = json.Unmarshal(built, &result); err != nil || result.ID == "" {
		return fmt.Errorf("engine returned no default template: %s", built)
	}
	sup.Args = append(args, "--socket", sup.Socket, "--allow-uid", strconv.Itoa(os.Getuid()))
	st.Supervisor = sup
	if err = startSupervisor(p, st, st.Current); err != nil {
		return err
	}
	grant, err := rootOutput(paths["lamp-supervisor"], "grant-install", "--store", store, "--caller-uid", strconv.Itoa(os.Getuid()), "--max-generation", "18446744073709551615", "--ttl-s", "86400")
	if err != nil {
		return err
	}
	var g struct {
		ID string `json:"grant_id"`
	}
	if err = json.Unmarshal(grant, &g); err != nil {
		return err
	}
	sup.GrantID = g.ID
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if runtime.GOOS == "darwin" {
		boot, err = exec.Command("sysctl", "-n", "kern.bootsessionuuid").Output()
	}
	if err != nil {
		return err
	}
	host := st.HostID
	if runtime.GOOS == "darwin" {
		name, e := exec.Command("sysctl", "-n", "kern.hostname").Output()
		if e != nil {
			return e
		}
		host = strings.TrimSpace(string(name))
	}
	st.LampdArgs = append(st.LampdArgs, "--worker-socket", sup.Socket, "--worker-id", "lamp-supervisor@"+host, "--worker-boot-id", strings.TrimSpace(string(boot)), "--worker-uid", "0", "--grant-id", g.ID, "--worker-timeout", "1m")
	setWorkerPID(st)
	if runtime.GOOS == "linux" {
		argv, _ := json.Marshal([]string{"sudo", "-n", "/usr/bin/python3", filepath.Join(p, "current", "tools", "guard-audit-release.py"), filepath.Join(base, "audit")})
		if os.Geteuid() == 0 {
			argv, _ = json.Marshal([]string{"/usr/bin/python3", filepath.Join(p, "current", "tools", "guard-audit-release.py"), filepath.Join(base, "audit")})
		}
		st.LampdArgs = append(st.LampdArgs, "--audit-release", string(argv))
	}
	if runtime.GOOS == "darwin" {
		st.LampdArgs = append(st.LampdArgs, "--audit-release", `["/usr/bin/true"]`)
	}
	owner := "pr_owner"
	var identity struct {
		Owner string `json:"owner_id"`
	}
	if raw, e := os.ReadFile(filepath.Join(p, "config", "identity.json")); e == nil && json.Unmarshal(raw, &identity) == nil {
		owner = identity.Owner
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x86_64"
	}
	t := map[string]any{"id": result.ID, "kind": "template", "fleet_id": "fl_local", "owner_id": owner, "revision": 1, "created_at": now, "updated_at": now, "state": "ready", "visibility": "private", "labels": map[string]string{"lamp.default": "true"}, "name": "default", "guest_os": "linux", "arch": arch, "default_resources": api.Resources{Vcpus: 2, MemoryMib: memoryMiB, DiskGib: 20, PidsMax: 4096}}
	raw, _ := json.Marshal(t)
	return writePrivate(filepath.Join(p, "config", "default-template.json"), raw)
}

func refreshEngineGrant(p string, st installState) error {
	sup := st.Supervisor
	if sup == nil || sup.GrantID == "" {
		return nil
	}
	paths, _ := componentPaths(p, sup, sup.Version)
	_, err := rootOutput(paths["lamp-supervisor"], "grant-install", "--store", supStore(sup), "--caller-uid", strconv.Itoa(os.Getuid()), "--grant-id", sup.GrantID, "--max-generation", "18446744073709551615", "--ttl-s", "86400")
	return err
}

// unpackSeed is called only on the signed payload's protected engine copy.
func unpackSeed(args []string) error {
	fl := flag.NewFlagSet("unpack-seed", flag.ContinueOnError)
	dir := fl.String("dir", "", "seed directory")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if *dir == "" {
		return fmt.Errorf("--dir required")
	}
	f, err := os.Open(filepath.Join(*dir, "rootfs.ext4.gz"))
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	_, _, err = release.CopyReader(gz, filepath.Join(*dir, "rootfs.ext4"), 0600)
	if err != nil {
		return err
	}
	return os.Remove(filepath.Join(*dir, "rootfs.ext4.gz"))
}

// removeManagedEngine releases only paths derived from this installation's
// prefix. Retained stores and their engine binaries stay together.
func removeManagedEngine(p string) error {
	base, runDir, _ := engineLocation(p)
	if runtime.GOOS == "linux" {
		raw, err := exec.Command("findmnt", "-rn", "-o", "TARGET").Output()
		if err != nil {
			return err
		}
		mounts := strings.Fields(string(raw))
		sort.Slice(mounts, func(i, j int) bool { return len(mounts[i]) > len(mounts[j]) })
		for _, mount := range mounts {
			if mount == filepath.Join(base, "store") || strings.HasPrefix(mount, runDir+"/") {
				if err := rootRun("umount", mount); err != nil {
					return err
				}
			}
		}
	}
	if runtime.GOOS == "darwin" {
		_, _, uidBase := engineLocation(p)
		first := int(uidBase/65536)*2 + 100
		for i := first; i < first+2; i++ {
			name := fmt.Sprintf("_lampvm%02d", i)
			for _, kind := range []string{"/Users/", "/Groups/"} {
				if err := rootRun("dscl", ".", "-delete", kind+name); err != nil {
					return err
				}
			}
		}
	}
	if runtime.GOOS == "linux" {
		_, _, uidBase := engineLocation(p)
		reservation := filepath.Join(filepath.Dir(base), "uid-"+strconv.FormatUint(uint64(uidBase), 10))
		if target, err := os.Readlink(reservation); err == nil && target == base {
			if err := rootRun("rm", reservation); err != nil {
				return err
			}
		}
	}
	if err := rootRun("rm", "-rf", base, runDir); err != nil {
		return err
	}
	return nil
}
