#!/usr/bin/env python3
"""Durable fork and suspend/resume of the standard sandbox on ordinary ext4.

Usage: ext4-durable-demo.py OUTPUT.jsonl   (store started by ext4-store.sh)

Uses only the supervisor's public worker API (`lamp-supervisor ctl`) on the
ext4 store. The guest runs the frozen workload's resident process, holding
RAM_BYTES of pattern memory and a 16 KiB fsynced disk file. It checks that:

1. A durable (`local`) fork of the running source is useful, keeps the
   resident (same PID and memory/disk digests), and later writes in the child
   do not reach the source.
2. Suspend (durable capture, then release) followed by a supervisor restart
   and a resume (create from the capture) brings back the same process with
   the same RAM and disk.
3. The durable capture survives the restart and is listed by the store.

Every call, its timing and the result go to OUTPUT.jsonl. Exits 1 on any
mismatch.
"""
import json
import os
from pathlib import Path
import subprocess
import sys
import time
import uuid

HERE = Path(__file__).resolve().parent
BIN = os.environ.get("BIN", str(Path.home() / "t7/bin/lamp-supervisor-plain"))
RUN = Path("/run/lamp-t7/ext4")
TEMPLATE = os.environ.get("TEMPLATE", "bench")
WORKLOAD = "/opt/lamp/bench-workload.py"
RAM_BYTES = 256 << 20
tag = uuid.uuid4().hex[:8]
nonce = uuid.uuid4().hex
out = open(sys.argv[1], "x")
failed = []


def log(kind, **value):
    value["kind"] = kind
    value["at"] = time.time()
    out.write(json.dumps(value) + "\n")
    out.flush()
    print(json.dumps(value)[:400], flush=True)


def ctl(*args, durability=None):
    argv = [BIN, "ctl", "--socket", str(RUN / "worker.sock")]
    if durability:
        argv += ["--durability", durability]
    argv += list(map(str, args))
    t = time.monotonic()
    r = subprocess.run(argv, capture_output=True, text=True, timeout=300)
    log("ctl", argv=argv[4:], exit=r.returncode, stdout=r.stdout[-2000:], stderr=r.stderr[-2000:],
        ms=(time.monotonic() - t) * 1000)
    if r.returncode:
        raise SystemExit("ctl %s failed: %s" % (args[0], r.stderr.strip()[-300:]))
    return r.stdout


def call(sb, request):
    return json.loads(ctl("exec", sb, "--", "python3", WORKLOAD, "call", "--path", path,
                          "--request", json.dumps(request)))


def check(name, ok, **detail):
    log("check", name=name, ok=bool(ok), **detail)
    if not ok:
        failed.append(name)


def store(cmd):
    r = subprocess.run([str(HERE / "ext4-store.sh"), cmd], capture_output=True, text=True, timeout=120)
    log("store", cmd=cmd, exit=r.returncode, stdout=r.stdout, stderr=r.stderr)
    if r.returncode:
        raise SystemExit("ext4-store.sh %s failed" % cmd)


path = "/var/tmp/demo-%s" % tag
a, b, c = "sb_t7d_%s_a" % tag, "sb_t7d_%s_b" % tag, "sb_t7d_%s_c" % tag
cp = "cp_t7d_%s" % tag
log("start", nonce=nonce, ram_bytes=RAM_BYTES, template=TEMPLATE,
    filesystem=subprocess.run(["findmnt", "-T", "/var/lib/lamp-t7/ext4-store", "-J"],
                              capture_output=True, text=True).stdout)
ctl("create", TEMPLATE, a)
ctl("exec", a, "--", "python3", WORKLOAD, "start", "--path", path, "--nonce", nonce,
    "--bytes", RAM_BYTES)
s1 = call(a, {"op": "write", "generation": 1})

# 1. Durable fork of the running source.
forked = json.loads(ctl("fork", a, b, durability="local"))
log("fork", ms=forked["fork_ms"], probe_exits=forked["probe_exits"])
fa, fb = call(a, {"op": "state"}), call(b, {"op": "state"})
same = ("pid", "nonce", "generation", "memory_sha256", "disk_sha256")
check("fork keeps the resident and its RAM/disk", all(fb[k] == s1[k] == fa[k] for k in same), source=fa, child=fb)
wb = call(b, {"op": "write", "generation": 2})
fa2 = call(a, {"op": "state"})
check("child writes do not reach the source",
      fa2["generation"] == 1 and fa2["memory_sha256"] == s1["memory_sha256"] and
      fa2["disk_sha256"] == s1["disk_sha256"] and wb["memory_sha256"] != s1["memory_sha256"],
      source=fa2, child=wb)
ctl("release", b)

# 2. Suspend, restart the supervisor, resume.
before = call(a, {"op": "state"})
suspended = json.loads(ctl("suspend", a, cp))
log("suspend", ms=suspended["capture_ms"], detail=suspended["detail"])
store("stop")
store("start")
resumed = json.loads(ctl("create", cp, c))
log("resume", ms=resumed["create_to_command_ms"])
after = call(c, {"op": "state"})
check("resume after restart keeps the process, RAM and disk",
      all(after[k] == before[k] for k in same) and after["ticks"] == before["ticks"] + 1,
      before=before, after=after)
ctl("release", c)
listed = subprocess.run(["sudo", "-n", BIN, "state", "--admin-socket", str(RUN / "admin.sock"), "list"],
                        capture_output=True, text=True)
log("state-list", stdout=listed.stdout[-4000:], exit=listed.returncode)
check("durable capture listed after restart", cp in listed.stdout)
log("result", ok=not failed, failed=failed)
sys.exit(1 if failed else 0)
