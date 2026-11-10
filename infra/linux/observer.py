#!/usr/bin/env python3
"""Resource observer for the T5 harness (`--observer`) on lane LX.

Reads one JSON object on stdin ({"operation", "sandbox_id",
"source_sandbox_id"}) and prints {"rss_bytes", "disk_bytes", "pressure",
"source_pause_ms"?}. Values come from kernel state, never from Lamp's API:

- rss_bytes: VmRSS of every process in the sandbox's jail cgroup (its VMM).
- disk_bytes: allocated bytes (st_blocks * 512) of the jail's root disk. The
  disk is a reflink copy, so this counts extents it shares with its template.
- pressure: PSI "some avg10" of the jail cgroup (cpu/memory/io) and of the
  host (/proc/pressure).
- source_pause_ms (forks only): NOT independently witnessed. It is the
  supervisor's own `source_pause_us` for the newest capture of the source,
  read from the systemd journal, and labelled `source_pause_origin:
  "engine_journal"` so the evidence says so.

Run it as the harness user; it uses `sudo -n` only to read the root-owned
jail disk metadata and the system journal.
"""
import json
import os
import re
import subprocess
import sys

CGROUP = "/sys/fs/cgroup/lamp"
JAIL = "/var/lib/lamp/jail/firecracker"


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
    out = subprocess.run(["sudo", "-n", "stat", "-c", "%b %B", path], capture_output=True, text=True)
    if out.returncode != 0:
        return None
    blocks, size = out.stdout.split()
    return int(blocks) * int(size)


def pressure(sandbox):
    base = os.path.join(CGROUP, jail(sandbox))
    value = {f"sandbox_{k}_some_avg10": avg10(os.path.join(base, f"{k}.pressure")) for k in ("cpu", "memory", "io")}
    value.update({f"host_{k}_some_avg10": avg10(f"/proc/pressure/{k}") for k in ("cpu", "memory", "io")})
    return value if value["sandbox_cpu_some_avg10"] is not None else None


def source_pause_ms(source):
    out = subprocess.run(
        ["sudo", "-n", "journalctl", "-u", "lamp-supervisor", "--since", "-120s", "-o", "cat", "--no-pager"],
        capture_output=True, text=True,
    )
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
        result["source_pause_origin"] = "engine_journal"
    print(json.dumps(result))


if __name__ == "__main__":
    main()
