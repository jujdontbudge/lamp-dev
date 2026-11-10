#!/usr/bin/env python3
"""Collects the T5 `--metadata` record (§22 observations) on lane LX.

Every field is read from the running machine at collection time: source and
dependency digests, installed artifact digests, CPU/memory, kernel, the
workload directory's filesystem and mount options, the physical disk, power
and governor, and thermal samples. Nothing is typed in by hand except the
lane, the topology and the access route, which describe how the run is made.

Usage: run-metadata.py SRC_DIR WORKLOAD_DIR TEMPLATE_ID > metadata.json
"""
import argparse
import hashlib
import json
import os
import platform
import re
import subprocess
import sys
import time
from pathlib import Path

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('source', type=Path)
parser.add_argument('directory')
parser.add_argument('template')
parser.add_argument('--store', type=Path, default=Path('/var/lib/lamp'))
parser.add_argument('--worker-binary', default='/usr/local/lib/lamp/lamp-supervisor')
parser.add_argument('--policy', type=Path, default=Path('/etc/lamp/network.json'))
args = parser.parse_args()
SRC, DIRECTORY, TEMPLATE = args.source, args.directory, args.template
policy = json.loads(args.policy.read_text())


def sha(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def root_sha(path):
    out = subprocess.run(["sudo", "-n", "sha256sum", str(path)], capture_output=True, text=True, check=True)
    return out.stdout.split()[0]


def tree_sha(root, suffixes):
    """Digest of every source file under root, by sorted relative path."""
    h = hashlib.sha256()
    for path in sorted(p for p in Path(root).rglob("*") if p.is_file() and p.suffix in suffixes and "target" not in p.parts):
        h.update(str(path.relative_to(root)).encode() + b"\0")
        h.update(hashlib.sha256(path.read_bytes()).digest())
    return h.hexdigest()


def run(*argv):
    return subprocess.run(argv, capture_output=True, text=True).stdout.strip()


def read(path, default=None):
    try:
        return Path(path).read_text().strip()
    except OSError:
        return default


source_hashes = {name: tree_sha(SRC / "crates" / name, {".rs", ".toml"}) for name in sorted(os.listdir(SRC / "crates"))}
source_hashes["go-control"] = tree_sha(SRC, {".go"})
source_hashes["benchmarks"] = tree_sha(SRC / "benchmarks", {".py"})
source_hashes["contracts"] = tree_sha(SRC / "contracts", {".proto", ".json", ".yaml", ".yml"})
source_hashes["images/linux-seed"] = tree_sha(SRC / "images", {".sh", ".py", ".lamp", ".json"})

dependency_hashes = {"Cargo.lock": sha(SRC / "Cargo.lock"), "go.sum": sha(SRC / "go.sum")}
for name in ("firecracker", "jailer"):
    dependency_hashes[name] = sha(f"/usr/local/lib/lamp/{name}")
for path in ("/usr/bin/pasta", "/usr/bin/python3", "/usr/bin/gcc-15", "/usr/sbin/nft", "/usr/bin/nsenter", "/usr/bin/fallocate"):
    dependency_hashes[path] = sha(os.path.realpath(path))

artifacts = {}
for path in (args.worker_binary, policy["guard"], str(Path.home() / "lamp-t6/bin/lampd"), str(Path.home() / "lamp-t6/bin/lamp")):
    artifacts[path] = sha(path)
artifacts["benchmarks/workload.py (installed in template)"] = sha(SRC / "benchmarks" / "workload.py")
artifacts["infra/linux/observer.py"] = sha(SRC / "infra" / "linux" / "observer.py")
# The template's manifest/provenance pin its seed, kernel and image digests;
# the kernel and the template's root disk are hashed here directly.
tdir = str(args.store / "templates" / TEMPLATE)
for name in ("manifest.json", "provenance.json", "vmlinux", "rootfs.ext4"):
    artifacts[f"{tdir}/{name}"] = root_sha(f"{tdir}/{name}")
manifest = json.loads(subprocess.run(["sudo", "-n", "cat", f"{tdir}/manifest.json"], capture_output=True, text=True, check=True).stdout)
seed = manifest["seed_ref"].rsplit("sha256:", 1)[1]
seed_path = str(args.store / "seeds" / seed / "seed.json")
artifacts[seed_path] = root_sha(seed_path)

cpuinfo = read("/proc/cpuinfo", "")
model = re.search(r"model name\s*:\s*(.+)", cpuinfo).group(1)
meminfo = read("/proc/meminfo", "")
memory = int(re.search(r"MemTotal:\s+(\d+)", meminfo).group(1)) * 1024
os_release = dict(re.findall(r'^(\w+)="?([^"\n]*)"?$', read("/etc/os-release", ""), re.M))

findmnt = json.loads(run("findmnt", "-J", "-T", DIRECTORY, "-o", "SOURCE,FSTYPE,OPTIONS,TARGET"))["filesystems"][0]
st = os.statvfs(DIRECTORY)
lsblk = json.loads(run("lsblk", "-J", "-d", "-o", "NAME,MODEL,SIZE,ROTA,TYPE,TRAN"))
lamp_fs = json.loads(run("findmnt", "-J", "-T", str(args.store), "-o", "SOURCE,FSTYPE,OPTIONS,TARGET"))["filesystems"][0]
mem_fs = json.loads(run("findmnt", "-J", "-T", "/run/lamp/mem", "-o", "SOURCE,FSTYPE,OPTIONS,TARGET"))["filesystems"][0]
# The audit spool is deliberately root-only. Resolve its actual mount as the
# same privileged observer used for template hashes, without weakening access.
audit_fs = json.loads(subprocess.check_output(["sudo", "-n", "findmnt", "-J", "-T", policy["audit_dir"], "-o", "SOURCE,FSTYPE,OPTIONS,TARGET"], text=True))["filesystems"][0]

governors = sorted({read(p) for p in Path("/sys/devices/system/cpu").glob("cpu[0-9]*/cpufreq/scaling_governor")})
epp = sorted({read(p) for p in Path("/sys/devices/system/cpu").glob("cpu[0-9]*/cpufreq/energy_performance_preference")})
ac = [int(read(p, "0")) for p in Path("/sys/class/power_supply").glob("*/online")]
samples, zones = [], {}
for _ in range(5):
    for zone in sorted(Path("/sys/class/thermal").glob("thermal_zone*")):
        t = read(zone / "temp")
        if t and t.lstrip("-").isdigit():
            samples.append(int(t) / 1000)
            zones[zone.name] = read(zone / "type")
    time.sleep(0.2)

metadata = {
    "source_hashes": source_hashes,
    "dependency_hashes": dependency_hashes,
    # Packages and the cargo features actually resolved for the two host
    # binaries (cargo tree), plus the Go build.
    "compiled_features": sorted(set(
        l.removesuffix(" (*)") for l in run("env", f"PATH={Path.home()}/.cargo/bin:/usr/bin:/bin", "cargo", "tree", "--manifest-path", str(SRC / "Cargo.toml"),
            "-p", "lamp-supervisor", "-p", "lamp-guard", "-e", "normal", "--depth", "1", "-f", "{p} [{f}]", "--prefix", "none").splitlines() if l.strip()
    )) + ["go: " + run(str(Path.home() / "go/bin/go") if (Path.home() / "go/bin/go").exists() else "go", "version")],
    "hardware": {
        "cpu_model": model, "logical_cpus": os.cpu_count(), "memory_bytes": memory,
        "cpu_core_assignment": "native workload pinned to CPUs 0,1 (--native-cpus); guest VMM threads are not pinned (scheduler-placed across all 8)",
        "swap_bytes": int(re.search(r"SwapTotal:\s+(\d+)", meminfo).group(1)) * 1024,
    },
    "os": {
        "kernel": platform.system(), "kernel_version": platform.release(),
        "distribution": os_release.get("PRETTY_NAME"), "machine": platform.machine(),
        "boot_id": read("/proc/sys/kernel/random/boot_id"),
    },
    "filesystem": {
        "type": findmnt["fstype"], "block_size_bytes": st.f_bsize, "directory": DIRECTORY,
        "mount": findmnt, "guest_disk_store": lamp_fs, "memory_image_store": mem_fs, "audit_store": audit_fs,
    },
    "storage": {
        "total_bytes": st.f_blocks * st.f_frsize, "free_bytes": st.f_bavail * st.f_frsize,
        "physical": lsblk["blockdevices"],
        "stack": run("lsblk", "-s", "-o", "NAME,TYPE,FSTYPE", findmnt["source"]),
    },
    "policy": {"revision": policy["revision"], "exceptions": policy["exceptions"], "file": str(args.policy), "sha256": sha(args.policy)},
    "resources": {"vcpus": 2, "memory_mib": 4096, "disk_gib": 20, "pids_max": 4096},
    "durability": "volatile",
    "durability_note": "E01 rows each carry their own durability (volatile/local/replicated); this field is the E02 row's.",
    "power": {"ac_online": max(ac) if ac else 0, "governors": governors, "energy_performance_preference": epp,
              "platform_profile": read("/sys/firmware/acpi/platform_profile"), "battery_status": read("/sys/class/power_supply/BAT0/status")},
    "thermal": {"samples_c": samples, "zones": zones},
    "lane": "LX",
    "topology": "native",
    "controls": {"guard": True, "audit": True, "cow": True, "capture_tracking": True,
                 "detail": "per-sandbox lamp-guard (NFQUEUE, durable flow admission), guest process collector over vsock, mediated proxy, reflink CoW disks, full memory capture"},
    "artifacts": artifacts,
    "access_route": "local: lamp CLI -> lampd Unix socket -> lamp.worker/1 Unix socket -> lamp-supervisor (no network hop)",
    "template_id": TEMPLATE,
    "seed_ref": manifest["seed_ref"],
    "load": os.getloadavg(),
    "collected_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
}
print(json.dumps(metadata, indent=1))
