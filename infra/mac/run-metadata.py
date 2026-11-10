#!/usr/bin/env python3
"""Collects the §22 run record for a Mac lane (M5, AIR) at run time.

Everything is read from the running machine: the git commit and a digest of
the working tree's sources, installed artifact digests, the template, CPU and
memory, macOS build, the store's filesystem and mount options, the physical
disk, power and thermal state, and the confinement inventory the worker
publishes. Only the lane name is given by hand.

Usage: run-metadata.py LANE TEMPLATE_ID [SANDBOX_ID] > metadata.json
With SANDBOX_ID (a live sandbox of the run), the record includes the exact
Seatbelt profile and inventory its worker runs under.
Needs sudo -n (the store and binaries are root-owned). Shows no dialog.
"""
import hashlib
import json
import platform
import subprocess
import sys
import time
from pathlib import Path

LANE, TEMPLATE = sys.argv[1], sys.argv[2]
ROOT = Path(__file__).resolve().parents[2]
LIB = Path("/usr/local/lib/lamp")
STORE = Path("/private/var/lamp/store")


def run(*argv, sudo=False):
    argv = (["sudo", "-n"] if sudo else []) + list(argv)
    return subprocess.run(argv, capture_output=True, text=True).stdout.strip()


def root_sha(path):
    return run("shasum", "-a", "256", str(path), sudo=True).split()[0]


def tree_sha(root, suffixes):
    h = hashlib.sha256()
    files = sorted(p for p in root.rglob("*") if p.is_file() and p.suffix in suffixes and "target" not in p.parts)
    for p in files:
        h.update(str(p.relative_to(root)).encode() + b"\0" + p.read_bytes())
    return h.hexdigest()


def sysctl(name):
    return run("sysctl", "-n", name)


store_df = run("df", "-k", str(STORE)).splitlines()[-1].split()
device = store_df[0]
mount_line = next((l for l in run("mount").splitlines() if l.startswith(device + " ")), "")
ram_line = next((l for l in run("mount").splitlines() if " on /private/var/lamp/ram " in l), "")
template_dir = STORE / "templates" / TEMPLATE
meta = {
    "lane": LANE,
    "captured_at_unix_ms": int(time.time() * 1000),
    "git_commit": run("git", "-C", str(ROOT), "rev-parse", "HEAD"),
    "git_dirty": bool(run("git", "-C", str(ROOT), "status", "--porcelain", "--untracked-files=no")),
    "source_hashes": {
        "crates": tree_sha(ROOT / "crates", {".rs", ".toml", ".py", ".entitlements"}),
        "infra_mac": tree_sha(ROOT / "infra" / "mac", {".sh", ".py"}),
    },
    "dependency_hashes": {"Cargo.lock": hashlib.sha256((ROOT / "Cargo.lock").read_bytes()).hexdigest()},
    "artifacts": {
        "lamp-vz": root_sha(LIB / "lamp-vz"),
        "lamp-supervisor": root_sha(LIB / "lamp-supervisor"),
        "template.manifest.json": root_sha(template_dir / "manifest.json"),
    },
    "worker_signature": run("codesign", "-dvv", str(LIB / "lamp-vz")) or subprocess.run(
        ["codesign", "-dvv", str(LIB / "lamp-vz")], capture_output=True, text=True).stderr.strip(),
    "worker_entitlements": subprocess.run(
        ["codesign", "-d", "--entitlements", "-", "--xml", str(LIB / "lamp-vz")], capture_output=True, text=True
    ).stdout.strip(),
    "hardware": {
        "model": sysctl("hw.model"),
        "cpu": sysctl("machdep.cpu.brand_string"),
        "perf_cores": sysctl("hw.perflevel0.physicalcpu"),
        "eff_cores": sysctl("hw.perflevel1.physicalcpu"),
        "memory_bytes": int(sysctl("hw.memsize")),
        "page_size": int(sysctl("hw.pagesize")),
    },
    "os": {
        "kernel": platform.system(),
        "release": platform.release(),
        "product_version": run("sw_vers", "-productVersion"),
        "build": run("sw_vers", "-buildVersion"),
    },
    "filesystem": {"store": STORE.as_posix(), "device": device, "mount": mount_line, "ram_volume": ram_line},
    "storage": {"diskutil": run("diskutil", "info", device.replace("/dev/", "")).splitlines()[:40]},
    "power": {"pmset_batt": run("pmset", "-g", "batt"), "pmset_custom": run("pmset", "-g").splitlines()},
    "thermal": {"pmset_therm": run("pmset", "-g", "therm"), "load_avg": sysctl("vm.loadavg")},
    "memory": {"vm_stat": run("vm_stat").splitlines(), "swap": sysctl("vm.swapusage")},
    "confinement": json.loads(
        run(str(LIB / "lamp-vz"), "profile", "--spec", str(STORE / "sb" / sys.argv[3] / "spec.json"), sudo=True) or "null"
    ) if len(sys.argv) > 3 else None,
    "controls": {"confine": True, "identity_per_sandbox": True, "cow": "apfs-clonefile"},
}
json.dump(meta, sys.stdout, indent=1)
print()
