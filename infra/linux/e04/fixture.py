#!/usr/bin/env python3
"""E04 real-filesystem storage fixture for Lamp on Linux.

The harness (`benchmarks/storage.py`) runs one scenario per invocation:

    sudo -n python3 -I fixture.py run --config CONFIG --root R --request JSON

Every scenario runs a real `lamp-supervisor` (a fault-injection build) on a
real filesystem (ext4 with Lamp's loop-backed dm-thin disks, XFS with
reflinks, or btrfs with reflinks). That filesystem lives on a disposable
volume and never on a live disk:

    tmpfs files -> loop -> dm-thin pool "e04-<lane>-pool" (this fixture's own)
      golden volume (id 1): mkfs + store + template, prepared once
      per scenario: snapshot of golden -> dm-log-writes -> mount -> Lamp

dm-log-writes records every completed bio with its FLUSH/FUA flags. When the
supervisor dies at the requested commit boundary (`_exit(79)`, armed through
its root-only admin socket), the fixture marks the log, tears everything
down and replays onto a fresh snapshot of golden only what a disk could
hold after losing power: everything before the last FLUSH, FUA writes, and
none (`discard`) or a random subset in random order (`reorder`) of the rest.
It then mounts that image, starts a new supervisor (Lamp's own recovery),
restores every committed generation and reads the resident workload's RAM
pattern, disk file (after dropping guest caches) and tick count from inside
the restored guest.

Only public surfaces are used: the lamp.worker/1 control client
(`lamp-supervisor ctl`), the documented admin socket (`lamp-supervisor
state`), and the guest workload. No Lamp serializer is imported.
Per-scenario transcripts, the barrier trace and supervisor logs are kept
under the configured artifact directory.

Subcommands: `prepare` (once per lane), `run` (one scenario), `teardown`.
"""
import argparse
import gzip
import hashlib
import json
import os
from pathlib import Path
import random
import shutil
import subprocess
import sys
import time

sys.dont_write_bytecode = True  # runs as root from a user's checkout
sys.path.insert(0, str(Path(__file__).resolve().parent))
import logwrites  # noqa: E402

MARKER = "lamp-disposable-fixture"
WORKLOAD = "/opt/lamp/bench-workload.py"
CRASH_EXIT = 79
RAM_BYTES = 8192
POOL_BLOCK = 128            # sectors (64 KiB)
VOLUME_SECTORS = 32 << 21   # 32 GiB virtual per disposable volume
LOG_SECTORS = 16 << 21      # 16 GiB virtual log device
GOLDEN, WORK, LOG, REPLAY, ROGUE, ROGUE_LOG = 1, 10, 11, 12, 13, 14
# Host memory (owner, 2026-11-10): available memory must stay at or above
# FLOOR with everything else running. A scenario starts only with
# FLOOR + BUDGET available, and a watchdog stops it below FLOOR.
GIB = 1 << 30
FLOOR = 12 * GIB
BUDGET = 4 * GIB
MEM_DIR_SIZE = "3G"
BEFORE_ACK = ("reserve", "capture", "seal", "object_flush", "directory_flush",
              "heads", "switch", "journal_flush", "publish")
LANES = {
    # Fully initialised inode tables and journal (no lazy background writes
    # inside a trace), sized so the whole volume stays small in host RAM.
    "ext4": {"mkfs": ["mkfs.ext4", "-F", "-q", "-i", "1048576", "-J", "size=128",
                      "-E", "nodiscard,lazy_itable_init=0,lazy_journal_init=0"],
             "mount": "commit=600", "backend": "thin"},
    "xfs": {"mkfs": ["mkfs.xfs", "-f", "-q", "-K", "-m", "reflink=1"],
            "mount": "", "backend": "reflink"},
    "btrfs": {"mkfs": ["mkfs.btrfs", "-f", "-q", "-K"],
              "mount": "commit=600", "backend": "reflink"},
}


class Fail(Exception):
    pass


class Done(Exception):
    pass


def sh(argv, check=True, timeout=120, **kw):
    r = subprocess.run(argv, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                       stdin=subprocess.DEVNULL, timeout=timeout, text=True, **kw)
    if check and r.returncode:
        raise Fail("%s: %s" % (" ".join(map(str, argv)), r.stderr.strip()[-400:]))
    return r


def dm_exists(name):
    return sh(["dmsetup", "info", name], check=False).returncode == 0


def dm_remove(name):
    for _ in range(100):
        if not dm_exists(name):
            return
        if sh(["dmsetup", "remove", name], check=False).returncode == 0:
            return
        time.sleep(0.05)
    raise Fail("cannot remove device-mapper device " + name)


def thin_activate(cfg, name, thin_id, sectors=VOLUME_SECTORS):
    sh(["dmsetup", "create", name, "--table",
        "0 %d thin /dev/mapper/%s %d" % (sectors, cfg["pool"], thin_id)])
    return "/dev/mapper/" + name


def pool_msg(cfg, msg, check=True):
    return sh(["dmsetup", "message", cfg["pool"], "0", msg], check=check)


def thin_delete(cfg, thin_id):
    pool_msg(cfg, "delete %d" % thin_id, check=False)


def available():
    with open("/proc/meminfo") as f:
        for line in f:
            if line.startswith("MemAvailable:"):
                return int(line.split()[1]) * 1024
    return 0


class Watchdog:
    """Kills this scenario's processes if host memory falls below FLOOR."""

    def __init__(self, work):
        import threading
        self.tripped = Path(work) / "memory-guard-tripped"
        self.instances = []
        self.stop = threading.Event()
        self.thread = threading.Thread(target=self.watch, daemon=True)
        self.thread.start()

    def watch(self):
        while not self.stop.wait(0.25):
            free = available()
            if free < FLOOR:
                self.tripped.write_text("MemAvailable %d at %s\n" % (free, time.ctime()))
                for inst in self.instances:
                    if inst.proc and inst.proc.poll() is None:
                        inst.proc.kill()
                    inst.kill_jails()
                return


def blkdiscard(dev):
    """Returns a volume's blocks to the pool, and so its RAM to the host."""
    sh(["blkdiscard", dev], check=False, timeout=300)


def mounted(path):
    return sh(["mountpoint", "-q", str(path)], check=False).returncode == 0


class Transcript:
    """Every public call made in a scenario, in order."""

    def __init__(self, path):
        self.path = path
        self.file = open(path, "w")

    def add(self, kind, argv, rc, out, err, ms):
        self.file.write(json.dumps({"kind": kind, "argv": argv, "rc": rc, "stdout": out[-4000:],
                                    "stderr": err[-2000:], "ms": round(ms, 3)}) + "\n")
        self.file.flush()

    def close(self):
        self.file.close()


class Instance:
    """One lamp-supervisor on one mounted disposable store."""

    def __init__(self, cfg, name, store, transcript, logs):
        self.cfg, self.name, self.store, self.t = cfg, name, Path(store), transcript
        self.run = Path(cfg["run"]) / name
        self.socket = self.run / "worker.sock"
        self.admin = self.run / "admin.sock"
        self.log_path = Path(logs) / ("supervisor-%s-%d.log" % (name, int(time.time() * 1000)))
        self.proc = None
        self.uid_base = cfg["uid_base"] + (0 if name == "main" else 5000)

    def start(self):
        self.run.mkdir(parents=True, exist_ok=True)
        for p in (self.socket, self.admin):
            if p.exists():
                p.unlink()
        argv = [self.cfg["binary"], "serve", "--store", str(self.store), "--socket", str(self.socket),
                "--admin-socket", str(self.admin), "--allow-uid", "0",
                "--mem-dir", str(self.run / "mem"), "--netns-dir", str(self.run / "netns"),
                "--uid-base", str(self.uid_base), "--disk-backend", self.cfg["backend"],
                "--thin-data-gib", "24", "--mem-dir-size", MEM_DIR_SIZE, "--no-capture-spare"]
        if WATCH:
            WATCH[0].instances.append(self)
        if self.name == "rogue":
            # The rogue host may use the shared template, but nobody enrolled
            # the rogue's own key.
            argv += ["--trust-producer", self.cfg["producer_public_key"]]
        self.logf = open(self.log_path, "w")
        self.proc = subprocess.Popen(argv, stdin=subprocess.DEVNULL, stdout=self.logf, stderr=self.logf,
                                     start_new_session=True)
        deadline = time.monotonic() + 60
        while time.monotonic() < deadline:
            if self.proc.poll() is not None:
                raise Fail("supervisor exited at start (%s)" % self.proc.returncode)
            if self.socket.exists() and self.admin.exists():
                return
            time.sleep(0.02)
        raise Fail("supervisor did not start")

    def ctl(self, *args, check=True, timeout=120):
        argv = [self.cfg["binary"], "ctl", "--socket", str(self.socket),
                "--memory-mib", str(self.cfg["memory_mib"]), *map(str, args)]
        env = dict(os.environ, LAMP_GRANT_ID=self.cfg["grant"])
        t = time.monotonic()
        try:
            r = subprocess.run(argv, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                               stdin=subprocess.DEVNULL, env=env, text=True, timeout=timeout)
        except subprocess.TimeoutExpired:
            self.t.add("ctl", argv[5:], None, "", "timeout", (time.monotonic() - t) * 1000)
            raise Fail("ctl %s timed out" % args[0])
        self.t.add("ctl", argv[5:], r.returncode, r.stdout, r.stderr, (time.monotonic() - t) * 1000)
        if check and r.returncode:
            raise Fail("ctl %s: %s" % (" ".join(map(str, args[:2])), r.stderr.strip()[-300:]))
        return r

    def state(self, *args):
        argv = [self.cfg["binary"], "state", "--admin-socket", str(self.admin), *map(str, args)]
        t = time.monotonic()
        r = subprocess.run(argv, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                           stdin=subprocess.DEVNULL, text=True, timeout=300)
        self.t.add("admin", argv[3:], r.returncode, r.stdout, r.stderr, (time.monotonic() - t) * 1000)
        try:
            return json.loads(r.stdout)
        except ValueError:
            return {"ok": False, "code": "HOST_UNAVAILABLE", "message": r.stderr.strip()[-300:]}

    def exited(self):
        return self.proc is not None and self.proc.poll() is not None

    def wait_exit(self, timeout):
        try:
            return self.proc.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            return None

    def stop(self):
        if self.proc and self.proc.poll() is None:
            self.proc.terminate()
            if self.wait_exit(60) is None:
                self.proc.kill()
                self.proc.wait()
        if self.proc:
            self.logf.close()

    def kill_jails(self):
        """Stops every VMM of this store (after a crash, nothing else will)."""
        jails = self.store / "jail" / "firecracker"
        ids = [e.name for e in jails.iterdir()] if jails.is_dir() else []
        for jid in ids:
            cg = Path("/sys/fs/cgroup/lamp") / jid
            if (cg / "cgroup.kill").exists():
                cg.joinpath("cgroup.kill").write_text("1")
        # A VMM that died mid-capture may sit in the shared layer cgroup.
        # Every jail pivots into its own mount namespace whose root is on
        # this store's filesystem; nothing else on the host is.
        dev = None
        with open("/proc/self/mountinfo") as mi:
            for line in mi:
                f = line.split()
                if f[4] == str(self.store):
                    dev = " %s " % f[2]
        host_ns = os.readlink("/proc/1/ns/mnt")
        for _ in range(100):
            left = []
            for pid in filter(str.isdigit, os.listdir("/proc")) if dev else ():
                try:
                    if os.readlink("/proc/%s/ns/mnt" % pid) == host_ns:
                        continue
                    with open("/proc/%s/mountinfo" % pid) as mi:
                        if any(dev in line for line in mi):
                            left.append(int(pid))
                except OSError:
                    pass
            if not left:
                break
            for pid in left:
                try:
                    os.kill(pid, 9)
                except OSError:
                    pass
            time.sleep(0.02)
        for jid in ids:
            cg = Path("/sys/fs/cgroup/lamp") / jid
            for _ in range(200):
                if not cg.exists() or "populated 0" in cg.joinpath("cgroup.events").read_text():
                    break
                time.sleep(0.01)
            try:
                cg.rmdir()
            except OSError:
                pass


def lamp_prefix(store):
    canon = os.path.realpath(store)
    return "lamp-" + hashlib.sha256(canon.encode()).hexdigest()[:10] + "-"


def release_store(cfg, inst, mnt):
    """Tears down what Lamp left on a mounted store, then unmounts it."""
    inst.stop()
    inst.kill_jails()
    # Jail bind mounts and network namespaces of this instance.
    mounts = sh(["findmnt", "-rn", "-o", "TARGET"]).stdout.split("\n")
    mine = [m for m in mounts if m.startswith(str(mnt) + "/") or m.startswith(str(inst.run) + "/")]
    for m in sorted(mine, key=len, reverse=True):
        sh(["umount", "-l", m], check=False)
    prefix = lamp_prefix(mnt)
    names = [line.split()[0] for line in sh(["dmsetup", "ls"]).stdout.splitlines()
             if line.startswith(prefix)]
    for n in sorted(names, key=lambda n: n.endswith("pool")):
        dm_remove(n)
    for line in sh(["losetup", "-ln", "-O", "NAME,BACK-FILE"]).stdout.splitlines():
        parts = line.split(None, 1)
        if len(parts) == 2 and parts[1].startswith(str(mnt) + "/"):
            sh(["losetup", "-d", parts[0]], check=False)
    for _ in range(100):
        if not mounted(mnt) or sh(["umount", str(mnt)], check=False).returncode == 0:
            break
        time.sleep(0.05)
    else:
        raise Fail("cannot unmount " + str(mnt))
    if mounted(inst.run / "mem"):
        sh(["umount", "-l", str(inst.run / "mem")], check=False)


def mount(cfg, dev, mnt, second=False):
    """`second`: a further snapshot of golden mounted at the same time,
    which needs its own filesystem identity."""
    Path(mnt).mkdir(parents=True, exist_ok=True)
    opts = [o for o in LANES[cfg["lane"]]["mount"].split(",") if o]
    if second and cfg["lane"] == "xfs":
        opts.append("nouuid")
    if second and cfg["lane"] == "btrfs":
        sh(["btrfstune", "-f", "-m", dev])
    sh(["mount", "-t", cfg["lane"]] + (["-o", ",".join(opts)] if opts else []) + [dev, str(mnt)])


# --- prepare -----------------------------------------------------------------

def prepare(args):
    lane = args.lane
    work = Path(args.work).resolve() / lane
    ram = work / "ram"
    ram.mkdir(parents=True, exist_ok=True)
    if not mounted(ram):
        sh(["mount", "-t", "tmpfs", "-o", "size=%dG,mode=0700" % args.ram_gib, "tmpfs", str(ram)])
    (ram / MARKER).write_text("disposable E04 volumes; no live block devices\n")
    cfg = {
        "lane": lane, "backend": LANES[lane]["backend"], "work": str(work), "ram": str(ram),
        "pool": "e04-%s-pool" % lane, "mnt": str(work / "mnt"), "rogue_mnt": str(work / "rogue"),
        "run": "/run/lamp-e04/" + lane, "binary": str(Path(args.binary).resolve()),
        "uid_base": args.uid_base, "artifacts": str(Path(args.artifacts).resolve() / lane),
    }
    if dm_exists(cfg["pool"]):
        raise Fail("lane %s is already prepared (teardown first)" % lane)
    data, meta = ram / "pool.data", ram / "pool.meta"
    for f, size in ((data, 64 << 30), (meta, 256 << 20)):
        with open(f, "wb") as out:
            out.truncate(size)
    cfg["data_loop"] = sh(["losetup", "-f", "--show", str(data)]).stdout.strip()
    cfg["meta_loop"] = sh(["losetup", "-f", "--show", str(meta)]).stdout.strip()
    sh(["dd", "if=/dev/zero", "of=" + cfg["meta_loop"], "bs=4096", "count=1", "status=none"])
    sectors = (64 << 30) // 512
    sh(["dmsetup", "create", cfg["pool"], "--table",
        "0 %d thin-pool %s %s %d 0 1 skip_block_zeroing" % (sectors, cfg["meta_loop"], cfg["data_loop"], POOL_BLOCK)])
    pool_msg(cfg, "create_thin %d" % GOLDEN)
    dev = thin_activate(cfg, "e04-%s-golden" % lane, GOLDEN)
    sh(LANES[lane]["mkfs"] + [dev])
    mnt = Path(cfg["mnt"])
    mount(cfg, dev, mnt)
    src = Path(args.template_store)
    tid = os.path.basename(os.readlink(src / "templates/names" / args.template))
    (mnt / "templates/names").mkdir(parents=True)
    sh(["cp", "-a", "--sparse=always", str(src / "templates" / tid), str(mnt / "templates") + "/"])
    os.symlink("../" + tid, mnt / "templates/names" / args.template)
    (mnt / "keys").mkdir(mode=0o700)
    sh(["cp", "-a", str(src / "keys/producer.key"), str(mnt / "keys/producer.key")])
    logs = work / "prepare-logs"
    logs.mkdir(exist_ok=True)
    t = Transcript(logs / "prepare.jsonl")
    grant = json.loads(sh([cfg["binary"], "grant-install", "--store", str(mnt), "--caller-uid", "0"]).stdout)
    cfg["grant"] = grant["grant_id"]
    cfg["memory_mib"] = template_memory(mnt / "templates" / tid / "manifest.json")
    inst = Instance(cfg, "main", mnt, t, logs)
    inst.start()
    status = inst.state("status")
    if not status.get("ok") or not status.get("fault_injection"):
        raise Fail("supervisor is not a fault-injection build: %s" % status)
    cfg["backend_name"] = status["backend"]
    cfg["producer_public_key"] = status["producer_public_key"]
    # One sandbox round trip proves the golden store works.
    inst.ctl("create", args.template, "sb_e04_probe")
    inst.ctl("release", "sb_e04_probe")
    release_store(cfg, inst, mnt)
    t.close()
    dm_remove("e04-%s-golden" % lane)
    cfg["template"] = args.template
    Path(cfg["artifacts"]).mkdir(parents=True, exist_ok=True)
    (work / "config.json").write_text(json.dumps(cfg, indent=1))
    print(json.dumps({"config": str(work / "config.json"), "backend": cfg["backend_name"]}))


def template_memory(path):
    m = json.loads(Path(path).read_text())
    return m["acceleration_variants"][0]["resources"]["memory_mib"]


def teardown(args):
    cfg = json.loads(Path(args.config).read_text())
    for n in ("rogue", "main"):
        inst = Instance(cfg, n, cfg["mnt"] if n == "main" else cfg["rogue_mnt"], None, cfg["work"])
        if mounted(inst.store):
            release_store(cfg, inst, inst.store)
    for name in ("lw", "s", "log", "replay", "rogue", "rogue-lw", "rogue-log", "golden"):
        dm_remove("e04-%s-%s" % (cfg["lane"], name))
    dm_remove(cfg["pool"])
    for loop in (cfg["data_loop"], cfg["meta_loop"]):
        sh(["losetup", "-d", loop], check=False)
    sh(["umount", cfg["ram"]], check=False)
    print(json.dumps({"torn_down": cfg["lane"]}))


# --- one scenario ------------------------------------------------------------

class Volume:
    """A scenario volume: snapshot of golden behind dm-log-writes."""

    def __init__(self, cfg, tag, snap_id, log_id):
        self.cfg, self.tag, self.snap_id, self.log_id = cfg, tag, snap_id, log_id
        lane = cfg["lane"]
        self.snap = "e04-%s-%s" % (lane, tag)
        self.log = "e04-%s-%slog" % (lane, "" if tag == "s" else tag + "-")
        self.lw = "e04-%s-%slw" % (lane, "" if tag == "s" else tag + "-")

    def create(self):
        for n in (self.lw, self.snap, self.log):
            dm_remove(n)
        thin_delete(self.cfg, self.snap_id)
        thin_delete(self.cfg, self.log_id)
        pool_msg(self.cfg, "create_snap %d %d" % (self.snap_id, GOLDEN))
        pool_msg(self.cfg, "create_thin %d" % self.log_id)
        snap = thin_activate(self.cfg, self.snap, self.snap_id)
        log = thin_activate(self.cfg, self.log, self.log_id, LOG_SECTORS)
        sh(["dd", "if=/dev/zero", "of=" + log, "bs=1M", "count=1", "oflag=direct", "status=none"])
        sh(["dmsetup", "create", self.lw, "--table",
            "0 %d log-writes %s %s" % (VOLUME_SECTORS, snap, log)])
        return "/dev/mapper/" + self.lw

    def mark(self, name):
        sh(["dmsetup", "message", self.lw, "0", "mark", name])

    def remove(self):
        dm_remove(self.lw)
        blkdiscard("/dev/mapper/" + self.snap)
        dm_remove(self.snap)

    def drop(self):
        blkdiscard("/dev/mapper/" + self.log)
        dm_remove(self.log)
        thin_delete(self.cfg, self.snap_id)
        thin_delete(self.cfg, self.log_id)


def power_cut_image(cfg, vol, fault, seed, mark, trace_path):
    """Replays the crash state onto a fresh golden snapshot; returns its device."""
    name = "e04-%s-replay" % cfg["lane"]
    dm_remove(name)
    thin_delete(cfg, REPLAY)
    pool_msg(cfg, "create_snap %d %d" % (REPLAY, GOLDEN))
    dev = thin_activate(cfg, name, REPLAY)
    log_dev = "/dev/mapper/" + vol.log
    sector_size, entries = logwrites.read_log(log_dev)
    crash = logwrites.find_mark(entries, mark)
    durable, volatile, last_flush = logwrites.crash_state(entries, crash, fault, seed)
    logwrites.apply(log_dev, sector_size, durable + volatile, dev)
    summary = logwrites.summary(entries, crash, durable, volatile, last_flush)
    applied = {e.index for e in volatile}
    with gzip.open(trace_path, "wt") as out:
        out.write(json.dumps({"fault": fault, "seed": seed, "mark": mark, "sector_size": sector_size,
                              "summary": summary}) + "\n")
        # index sector sectors flags fate (d=durable, v=volatile applied,
        # x=volatile dropped, a=after the crash mark, m=mark/other)
        durable_ix = {e.index for e in durable}
        for e in entries:
            if e.index >= crash:
                fate = "a"
            elif e.index in durable_ix:
                fate = "d"
            elif e.index in applied:
                fate = "v"
            elif logwrites.is_write(e):
                fate = "x"
            else:
                fate = "m"
            out.write("%d %d %d %d %s%s\n" % (e.index, e.sector, e.sectors, e.flags, fate,
                                              " " + e.mark if e.mark else ""))
    return dev, summary


def guest_state(inst, sandbox, path):
    # Disk bytes must come from the restored disk, not the guest page cache.
    inst.ctl("exec", sandbox, "--", "sh", "-c", "sync; echo 3 > /proc/sys/vm/drop_caches")
    r = inst.ctl("exec", sandbox, "--", "python3", WORKLOAD, "call", "--path", path,
                 "--request", json.dumps({"op": "state"}))
    return json.loads(r.stdout.strip().splitlines()[-1])


def scenario(cfg, root, request, out_events):
    if not (Path(root) / MARKER).is_file():
        raise Fail("refusing: %s has no %s marker" % (root, MARKER))
    if not (Path(cfg["ram"]) / MARKER).is_file():
        raise Fail("refusing: lane volumes are not the disposable fixture")
    nonce = request["nonce"]
    point, side = request["point"], request["side"]
    omit = request.get("omit")
    fault = request.get("fault") or "discard"
    unauthorized = bool(request.get("unauthorized"))
    tag = nonce[:12]
    adir = Path(cfg["artifacts"]) / ("%05d-%s" % (int(request.get("iteration", 99999)), tag))
    adir.mkdir(parents=True, exist_ok=False)
    (adir / "request.json").write_text(json.dumps(request))
    t = Transcript(adir / "transcript.jsonl")
    mnt = Path(cfg["mnt"])
    cp = {g: "cp_%s_%s" % (tag, g) for g in ("0", "1", "x")}
    marker_path = "/var/tmp/e04-" + tag
    vol = Volume(cfg, "s", WORK, LOG)
    dev = vol.create()
    mount(cfg, dev, mnt)
    inst = Instance(cfg, "main", mnt, t, adir)
    crash_exit = 0
    acked = []
    notes = {}
    try:
        inst.start()
        a, b = "sb_%s_a" % tag, "sb_%s_b" % tag
        inst.ctl("create", cfg["template"], a)
        inst.ctl("exec", a, "--", "python3", WORKLOAD, "start", "--path", marker_path,
                 "--nonce", nonce, "--bytes", RAM_BYTES)
        inst.ctl("capture", a, cp["0"], "--durability", "local")
        out_events.ack("0")
        acked.append("0")
        if unauthorized:
            # Only generation 0 is Lamp's; generation 1 comes from a producer
            # this store never enrolled, and must not be published.
            inst.ctl("release", a)
            notes["unauthorized"] = rogue_import(cfg, inst, nonce, marker_path, t, adir)
            vol.mark("crash")
            raise Done()
        # A second durable capture of the same source, deleted later.
        inst.ctl("capture", a, cp["x"], "--durability", "local")
        # The fork: restored from generation 0's image while the source runs.
        inst.ctl("create", cp["0"], b)
        inst.ctl("release", a)
        inst.ctl("exec", b, "--", "python3", WORKLOAD, "call", "--path", marker_path,
                 "--request", json.dumps({"op": "write", "generation": 1}))
        if point != "none" or omit:
            armed = inst.state("fault", point if point != "none" else "", side, omit or "")
            if not armed.get("ok"):
                raise Fail("arming the fault failed: %s" % armed)
        r = inst.ctl("capture", b, cp["1"], "--durability", "local", check=False)
        if r.returncode == 0:
            out_events.ack("1")
            acked.append("1")
        if r.returncode == 0 and (point, side) != ("ack", "after") and not inst.exited():
            for op in (("delete", cp["x"]), ("gc",), ("compact", cp["1"])):
                reply = inst.state(*op)
                notes.setdefault("maintenance", []).append({"op": op[0], "reply": reply})
                if inst.exited():
                    break
        if point != "none":
            code = inst.wait_exit(30)
            if code is None:
                raise Fail("no crash at %s/%s" % (point, side))
            crash_exit = code
        # Power cut: nothing written after this mark exists on the disk.
        vol.mark("crash")
    except Done:
        pass
    finally:
        release_store(cfg, inst, mnt)
        vol.remove()
    seed = int(hashlib.sha256(nonce.encode()).hexdigest()[:16], 16)
    replay, summary = power_cut_image(cfg, vol, fault, seed, "crash", adir / "barrier-trace.txt.gz")
    vol.drop()
    notes["barrier"] = summary
    # Recovery: a new supervisor on the power-cut image.
    mount(cfg, replay, mnt)
    rec = Instance(cfg, "main", mnt, t, adir)
    objects = {}
    try:
        rec.start()
        listed = rec.state("list")
        notes["recovered_list"] = listed
        captures = {c["capture_id"]: c for c in listed.get("captures", [])}
        for g in ("0", "1"):
            c = captures.get(cp[g])
            if not c or c.get("state") != "committed":
                continue
            sb = "sb_%s_r%s" % (tag, g)
            restored = rec.ctl("create", cp[g], sb, check=False)
            if restored.returncode:
                notes.setdefault("restore_failures", {})[g] = restored.stderr.strip()[-300:]
                continue
            st = guest_state(rec, sb, marker_path)
            rec.ctl("release", sb)
            parent = c.get("parent")
            objects[g] = {
                "nonce": st.get("nonce"), "generation": st.get("generation"),
                "parent": next((k for k, v in cp.items() if v == parent), parent),
                "producer": "authorized" if c.get("producer_key_id") == authorized_key(rec) else
                            "unauthorized:" + str(c.get("producer_key_id")),
                "memory_sha256": st.get("memory_sha256"), "disk_sha256": st.get("disk_sha256"),
                "ticks": st.get("ticks"),
            }
        # A capture by any other producer that survived is reported as such.
        for cid, c in captures.items():
            if c.get("state") == "committed" and c.get("producer_key_id") != authorized_key(rec):
                objects.setdefault("1", {"producer": "unauthorized:" + str(c.get("producer_key_id")),
                                         "capture_id": cid})
    finally:
        release_store(cfg, rec, mnt)
        blkdiscard("/dev/mapper/e04-%s-replay" % cfg["lane"])
        dm_remove("e04-%s-replay" % cfg["lane"])
        thin_delete(cfg, REPLAY)
        t.close()
    value = {"objects": objects, "qualification": "real-filesystem-block-barrier",
             "filesystem": cfg["lane"], "disk_backend": cfg["backend_name"]}
    if unauthorized:
        value["unauthorized_capture_rejected"] = (
            notes["unauthorized"].get("code") == "HOST_NOT_TRUSTED" and "1" not in objects)
    value["evidence"] = evidence(adir, notes, acked, crash_exit)
    return crash_exit, value


_AUTH = {}
WATCH = []


def authorized_key(inst):
    """The enrolled producer of this store: the key its status reports."""
    if "id" not in _AUTH:
        s = inst.state("status")
        _AUTH["id"] = s.get("producer_key_id")
    return _AUTH["id"]


def rogue_import(cfg, inst, nonce, marker_path, t, adir):
    """A second, unenrolled producer makes a validly hashed generation 1 of
    the same workload; the main store must refuse its bundle."""
    rmnt = Path(cfg["rogue_mnt"])
    vol = Volume(cfg, "rogue", ROGUE, ROGUE_LOG)
    dev = vol.create()
    mount(cfg, dev, rmnt, second=True)
    os.remove(rmnt / "keys/producer.key")      # a fresh, unenrolled key
    rogue = Instance(cfg, "rogue", rmnt, t, adir)
    bundle = Path(cfg["ram"]) / ("bundle-" + nonce[:12])
    shutil.rmtree(bundle, ignore_errors=True)
    try:
        rogue.start()
        sb, cid = "sb_%s_rogue" % nonce[:12], "cp_%s_1" % nonce[:12]
        rogue.ctl("create", cfg["template"], sb)
        rogue.ctl("exec", sb, "--", "python3", WORKLOAD, "start", "--path", marker_path,
                  "--nonce", nonce, "--bytes", RAM_BYTES)
        rogue.ctl("exec", sb, "--", "python3", WORKLOAD, "call", "--path", marker_path,
                  "--request", json.dumps({"op": "write", "generation": 1}))
        rogue.ctl("capture", sb, cid, "--durability", "local")
        rogue.ctl("release", sb)
        exported = rogue.state("export", cid, str(bundle))
        if not exported.get("ok"):
            raise Fail("rogue export failed: %s" % exported)
        rogue_key = rogue.state("status").get("producer_key_id")
    finally:
        release_store(cfg, rogue, rmnt)
        vol.remove()
        vol.drop()
    reply = inst.state("import", str(bundle))
    shutil.rmtree(bundle, ignore_errors=True)
    return {"code": reply.get("code"), "ok": reply.get("ok"), "message": reply.get("message"),
            "rogue_producer_key_id": rogue_key}


def evidence(adir, notes, acked, crash_exit):
    """Digests of the kept traces; the harness reads and checks them."""
    (adir / "notes.json").write_text(json.dumps(notes, indent=1))
    digests = {}
    adir.chmod(0o755)
    for p in sorted(adir.iterdir()):
        if p.is_file():
            p.chmod(0o644)
            digests[p.name] = hashlib.sha256(p.read_bytes()).hexdigest()
    return {"artifact_dir": str(adir), "files": digests, "acknowledged": acked,
            "crash_exit": crash_exit, "barrier": notes.get("barrier")}


class Events:
    def __init__(self, request):
        self.request = request

    def ack(self, ident):
        print(json.dumps({"event": "ack", "value": {
            "id": ident, "parent": None if ident == "0" else "0", "nonce": self.request["nonce"],
            "generation": int(ident), "memory_bytes": RAM_BYTES, "disk_bytes": 16384}}), flush=True)
        line = sys.stdin.readline()
        if json.loads(line) != {"acknowledged": ident}:
            raise Fail("external history did not acknowledge " + ident)


def run(args):
    cfg = json.loads(Path(args.config).read_text())
    request = json.loads(args.request)
    lock = open(Path(cfg["work"]) / "lock", "w")
    import fcntl
    fcntl.flock(lock, fcntl.LOCK_EX)
    tripped = Path(cfg["work"]) / "memory-guard-tripped"
    if tripped.exists():
        raise Fail("memory guard tripped earlier in this run: " + tripped.read_text().strip())
    if available() < FLOOR + BUDGET:
        raise Fail("memory guard: %d MiB available, need %d" % (available() >> 20, (FLOOR + BUDGET) >> 20))
    WATCH.append(Watchdog(cfg["work"]))
    crash_exit, value = scenario(cfg, args.root, request, Events(request))
    print(json.dumps({"event": "recovered", "crash_exit": crash_exit, "value": value}), flush=True)
    return 0


def main():
    p = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    sub = p.add_subparsers(dest="cmd", required=True)
    pr = sub.add_parser("prepare")
    pr.add_argument("--lane", choices=sorted(LANES), required=True)
    pr.add_argument("--work", required=True)
    pr.add_argument("--binary", required=True)
    pr.add_argument("--template-store", default="/var/lib/lamp")
    pr.add_argument("--template", default="bench")
    pr.add_argument("--artifacts", required=True)
    pr.add_argument("--uid-base", type=int, required=True)
    pr.add_argument("--ram-gib", type=int, default=3)
    rn = sub.add_parser("run")
    rn.add_argument("--config", required=True)
    rn.add_argument("--root", required=True)
    rn.add_argument("--request", required=True)
    td = sub.add_parser("teardown")
    td.add_argument("--config", required=True)
    args = p.parse_args()
    if os.geteuid() != 0:
        raise SystemExit("the E04 fixture runs as root (sudo -n)")
    try:
        return {"prepare": prepare, "run": run, "teardown": teardown}[args.cmd](args) or 0
    except BaseException as e:
        # The harness keeps no fixture stderr: every failure is also kept
        # in the lane's failures.log, next to the run's artifacts.
        import traceback
        text = "".join(traceback.format_exception(e))
        if args.cmd == "run":
            try:
                work = json.loads(Path(args.config).read_text())["work"]
                with open(Path(work) / "failures.log", "a") as f:
                    f.write("%s request=%s\n%s\n" % (time.ctime(), args.request, text))
            except (OSError, ValueError, KeyError):
                pass
        print("e04 fixture: %s" % (e if isinstance(e, Fail) else text), file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
