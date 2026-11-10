#!/usr/bin/env python3
"""Resource observer for the T5 harness (`--observer`) on lane LX.

Reads one JSON object on stdin ({"operation", "sandbox_id",
"source_sandbox_id"}) and prints {"rss_bytes", "disk_bytes", "pressure",
"source_pause_ms"?}. Values come from kernel state, never from Lamp's API:

- rss_bytes: VmRSS of every process in the sandbox's jail cgroup (its VMM).
- disk_bytes: allocated bytes (st_blocks * 512) of the jail's root disk. The
  disk is a reflink copy, so this counts extents it shares with its template.
  On a dm-thin store (ordinary ext4) the root disk is a block device; then it
  is the thin volume's mapped bytes from `dmsetup status`, which likewise
  counts blocks shared with its origin.
- pressure: PSI "some avg10" of the jail cgroup (cpu/memory/io) and of the
  host (/proc/pressure).
- source_pause_ms (forks only): NOT independently witnessed. It is the
  supervisor's own `source_pause_us` for the newest capture of the source,
  read from the systemd journal, and labelled `source_pause_origin:
  "engine_journal"` so the evidence says so.

Run it as the harness user; it uses `sudo -n` only to read the root-owned
jail disk metadata and the system journal.

LAMP_STORE (default /var/lib/lamp) names the store whose jails are observed.
LAMP_SUPERVISOR_LOG names a supervisor log file to read instead of the
`lamp-supervisor` unit's journal, for a supervisor started outside systemd
(infra/linux/ext4-store.sh).
"""
import json
import os
import re
import subprocess
import sys

CGROUP = "/sys/fs/cgroup/lamp"
JAIL = os.path.join(os.environ.get("LAMP_STORE", "/var/lib/lamp"), "jail", "firecracker")
LOG = os.environ.get("LAMP_SUPERVISOR_LOG")


def jail(sandbox):
    return "sb-" + sandbox.removeprefix("sb_")


def avg10(path):
    try:
        with open(path) as f:
            line = f.readline()
        return float(re.search(r"avg10=([0-9.]+)", line).group(1))
    except (OSError, AttributeError):
        return None


def rss(sandbox):
    try:
        with open(os.path.join(CGROUP, jail(sandbox), "cgroup.procs")) as f:
            pids = f.read().split()
    except OSError:
        return None
    total = 0
    for pid in pids:
        try:
            with open(f"/proc/{pid}/status") as f:
                for line in f:
                    if line.startswith("VmRSS:"):
                        total += int(line.split()[1]) * 1024
        except OSError:
            pass
    return total if pids else None


def disk(sandbox):
    path = os.path.join(JAIL, jail(sandbox), "root", "rootfs.ext4")
    out = subprocess.run(["sudo", "-n", "stat", "-c", "%F %b %B %t %T", path], capture_output=True, text=True)
    if out.returncode != 0:
        return None
    *kind, blocks, size, major, minor = out.stdout.split()
    if " ".join(kind) != "block special file":
        return int(blocks) * int(size)
    # Thin volume: "0 <sectors> thin <mapped sectors> <highest mapped>".
    out = subprocess.run(["sudo", "-n", "dmsetup", "status", "-j", str(int(major, 16)), "-m", str(int(minor, 16))],
                         capture_output=True, text=True)
    fields = out.stdout.split()
    if out.returncode != 0 or len(fields) < 4 or fields[2] != "thin":
        return None
    return int(fields[3]) * 512


def pressure(sandbox):
    base = os.path.join(CGROUP, jail(sandbox))
    value = {f"sandbox_{k}_some_avg10": avg10(os.path.join(base, f"{k}.pressure")) for k in ("cpu", "memory", "io")}
    value.update({f"host_{k}_some_avg10": avg10(f"/proc/pressure/{k}") for k in ("cpu", "memory", "io")})
    return value if value["sandbox_cpu_some_avg10"] is not None else None


def source_pause_ms(source):
    if LOG:
        argv = ["sudo", "-n", "tail", "-c", "4000000", LOG]
    else:
        argv = ["sudo", "-n", "journalctl", "-u", "lamp-supervisor", "--since", "-120s", "-o", "cat", "--no-pager"]
    out = subprocess.run(argv, capture_output=True, text=True)
    pause = None
    for line in out.stdout.splitlines():
        if ".capture sandbox " + source in line:
            match = re.search(r"source_pause_us=(\d+)", line)
            if match:
                pause = int(match.group(1)) / 1000
    return pause


def main():
    request = json.load(sys.stdin)
    sandbox = request.get("sandbox_id")
    result = {"rss_bytes": None, "disk_bytes": None, "pressure": None}
    if sandbox:
        result.update(rss_bytes=rss(sandbox), disk_bytes=disk(sandbox), pressure=pressure(sandbox))
    if request.get("operation") == "fork":
        source = request.get("source_sandbox_id")
        result["source_pause_ms"] = source_pause_ms(source) if source else None
        result["source_pause_origin"] = "engine_log" if LOG else "engine_journal"
    print(json.dumps(result))


if __name__ == "__main__":
    main()
