#!/usr/bin/env python3
"""APFS durable-suspend crash test for the Mac engine (T8, E05 storage row).

Each case runs a separate supervisor on a disposable store, puts the
externally chosen T7 pattern into a Linux guest (8192 RAM bytes held by a
live process, 16384 disk bytes fsync'd to the guest disk), asks for a
durable suspend, and ends the supervisor abruptly: either inside the commit
at an injected point (`LAMP_TEST_CRASH_AT`, exit 79) or with SIGKILL right
after the acknowledgement. A fresh supervisor then recovers the store.

Oracle (benchmarks.oracles.check_state, hashes derived here from the nonce):
an acknowledged suspend must come back, resume with the same RAM and disk
bytes and a continuing process; an unacknowledged one may come back only
whole, or be absent. Anything else (torn state, a sandbox that cannot
resume, a sandbox resumed cold) is a failure.

This exercises process crashes on the real APFS store with F_FULLFSYNC
barriers. It is not a power-loss or block-reordering experiment.

Usage: apfs-crash-test.py --sup BIN --worker BIN --seed DIR --cases N --out DIR
Needs sudo -n. Never touches the main store or the main supervisor.
"""
import argparse
import json
import os
import subprocess
import sys
import time
import uuid
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
from benchmarks.oracles import check_state, pattern_hash  # noqa: E402

POINTS = ["none", "suspend.paused", "suspend.saved", "suspend.synced", "suspend.meta_written",
          "suspend.meta_renamed", "suspend.committed", "suspend.torn_down"]
STORE = "/private/var/lamp/crash-store"
SOCK = "/private/var/run/lamp/crash.sock"
GUEST = r'''
import hashlib, os, socket, sys, threading, time, json
nonce = sys.argv[1]
def pattern(size, domain):
    out = bytearray()
    prefix = f"{nonce}:0:{domain}:".encode()
    for off in range(0, size, 4096):
        out += (hashlib.sha256(prefix + str(off // 4096).encode()).digest() * 128)[:min(4096, size - off)]
    return out
ram = pattern(8192, "ram")
fd = os.open("/root/pattern.disk", os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
os.write(fd, bytes(pattern(16384, "disk"))); os.fsync(fd); os.close(fd)
ticks = 0
def tick():
    global ticks
    while True:
        ticks += 1; time.sleep(0.01)
threading.Thread(target=tick, daemon=True).start()
s = socket.socket(socket.AF_UNIX); s.bind("/run/pattern.sock"); s.listen(4)
open("/run/pattern.ready", "w").write("1")
while True:
    c, _ = s.accept(); c.recv(16)
    open("/proc/sys/vm/drop_caches", "w").write("1")
    disk = open("/root/pattern.disk", "rb").read()
    c.sendall(json.dumps({"nonce": nonce, "generation": 0, "ticks": ticks,
        "memory_sha256": hashlib.sha256(ram).hexdigest(),
        "disk_sha256": hashlib.sha256(disk).hexdigest()}).encode()); c.close()
'''
ASK = "import socket;s=socket.socket(socket.AF_UNIX);s.connect('/run/pattern.sock');s.send(b'r');print(s.recv(4096).decode())"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--sup", required=True)
    ap.add_argument("--worker", required=True)
    ap.add_argument("--seed", required=True)
    ap.add_argument("--cases", type=int, default=1000)
    ap.add_argument("--out", required=True)
    ap.add_argument("--identities", default="13-16")
    a = ap.parse_args()
    out = Path(a.out)
    out.mkdir(parents=True, exist_ok=False)
    base = ["--store", STORE, "--worker", a.worker, "--ram-dir", "", "--identities", a.identities]

    def sup(*args, env=None, **kw):
        return subprocess.run(["sudo", "-n", "env", *(env or []), a.sup, *args], capture_output=True, text=True, **kw)

    def serve(crash):
        env = [f"LAMP_TEST_CRASH_AT={crash}"] if crash != "none" else []
        subprocess.run(["sudo", "-n", "rm", "-f", SOCK], check=True)
        p = subprocess.Popen(["sudo", "-n", "env", *env, a.sup, "serve", "--socket", SOCK, *base],
                             stdout=subprocess.DEVNULL, stderr=open(out / "supervisor.log", "a"))
        for _ in range(600):
            if os.path.exists(SOCK):
                return p
            time.sleep(0.05)
        raise RuntimeError("supervisor did not start")

    def ctl(gen, *args):
        return sup("ctl", "--socket", SOCK, "--generation", str(gen), *args, env=[f"LAMP_GRANT_ID={grants[gen]}"])

    def kill(p):
        subprocess.run(["sudo", "-n", "pkill", "-KILL", "-f", f"lamp-supervisor serve --socket {SOCK}"])
        return p.wait()

    subprocess.run(["sudo", "-n", "pkill", "-KILL", "-f", f"lamp-supervisor serve --socket {SOCK}"])
    subprocess.run(["sudo", "-n", "rm", "-rf", STORE], check=True)
    built = sup("template-prepare", a.seed, "crash-linux", *base)
    if built.returncode != 0:
        sys.exit("template: " + built.stderr)
    grants = {}
    for g in (1, 2):
        r = sup("grant-install", "--store", STORE, "--caller-uid", "0", "--generation", str(g))
        grants[g] = json.loads(r.stdout)["grant_id"]
    log = open(out / "cases.jsonl", "w")
    failures = 0
    counts = {}
    for i in range(a.cases):
        point = POINTS[i % len(POINTS)]
        nonce = uuid.uuid4().hex
        sid = f"sb_cr_{i}_{nonce[:6]}"
        case = {"case": i, "point": point, "nonce": nonce, "sandbox": sid}
        errors = []
        p = serve(point)
        r = ctl(1, "create", "crash-linux", sid)
        r = ctl(1, "exec", sid, "--", "sh", "-c",
                f"cat > /root/p.py <<'EOF'\n{GUEST}\nEOF\nsetsid python3 /root/p.py {nonce} >/root/p.log 2>&1 & "
                "for i in $(seq 200); do [ -f /run/pattern.ready ] && exit 0; sleep 0.05; done; exit 1")
        if r.returncode != 0:
            errors.append("guest pattern did not start")
        before = json.loads(ctl(1, "exec", sid, "--", "python3", "-c", ASK).stdout or "{}")
        errors += ["before: " + e for e in check_state(before, nonce, 0, 8192)]
        t = time.monotonic()
        s = ctl(1, "suspend", sid)
        acked = s.returncode == 0 and '"suspended"' in s.stdout
        case["suspend_ms"] = (time.monotonic() - t) * 1000
        case["acked"] = acked
        case["exit"] = p.wait(timeout=30) if point != "none" and not acked else kill(p)
        if point != "none" and not acked and case["exit"] != 79:
            errors.append(f"supervisor did not crash at {point} (exit {case['exit']})")
        p = serve("none")
        insp = ctl(1, "inspect", sid)
        state = json.loads(insp.stdout)["state"] if insp.returncode == 0 else "absent"
        case["recovered_state"] = state
        if state == "suspended":
            res = ctl(2, "resume", sid, "2")
            if res.returncode != 0:
                errors.append("recovered suspend did not resume: " + res.stderr.strip())
            else:
                after = json.loads(ctl(2, "exec", sid, "--", "python3", "-c", ASK).stdout or "{}")
                case["observed"] = after
                errors += check_state(after, nonce, 0, 8192)
                if after.get("ticks", 0) <= before.get("ticks", 0):
                    errors.append("process did not continue from its suspended RAM")
                expect = (pattern_hash(nonce, 0, 8192, "ram"), pattern_hash(nonce, 0, 16384, "disk"))
                case["expected"] = expect
            ctl(2, "release", sid)
        elif acked:
            errors.append(f"lost acknowledged suspend (recovered as {state})")
        elif state != "absent":
            errors.append(f"unacknowledged suspend recovered as {state}")
            ctl(1, "release", sid)
        case["errors"] = errors
        failures += bool(errors)
        counts.setdefault(point, {"cases": 0, "recovered": 0, "failures": 0})
        counts[point]["cases"] += 1
        counts[point]["recovered"] += state == "suspended"
        counts[point]["failures"] += bool(errors)
        log.write(json.dumps(case) + "\n")
        log.flush()
        os.fsync(log.fileno())
        subprocess.run(["sudo", "-n", "pkill", "-TERM", "-f", f"lamp-supervisor serve --socket {SOCK}"])
        p.wait(timeout=60)
    summary = {"cases": a.cases, "failures": failures, "by_point": counts,
               "qualification": "process-crash on APFS with F_FULLFSYNC; not power-loss"}
    (out / "summary.json").write_text(json.dumps(summary, indent=1) + "\n")
    print(json.dumps(summary))
    sys.exit(1 if failures else 0)


if __name__ == "__main__":
    main()
