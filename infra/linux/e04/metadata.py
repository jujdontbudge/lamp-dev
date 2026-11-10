#!/usr/bin/env python3
"""Extends the lane's `run-metadata.py` record with what an E04 run uses.

Usage: metadata.py BASE_METADATA.json LANE_CONFIG.json > e04-metadata.json

Adds the digests of the fault-injection supervisor and of this fixture, its
actual feature set, and the observed disposable volume stack of the lane:
the filesystem type, mount options and backend as configured by `fixture.py
prepare`. Durability is `local`: every capture in E04 is a durable one.
"""
import hashlib
import json
from pathlib import Path
import subprocess
import sys

HERE = Path(__file__).resolve().parent


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def main():
    base = json.loads(Path(sys.argv[1]).read_text())
    cfg = json.loads(Path(sys.argv[2]).read_text())
    sys.path.insert(0, str(HERE))
    sys.dont_write_bytecode = True
    from fixture import LANES
    base["artifacts"][cfg["binary"] + " (E04 fault-injection build)"] = sha(cfg["binary"])
    for name in ("fixture.py", "logwrites.py"):
        base["artifacts"]["infra/linux/e04/" + name] = sha(HERE / name)
    base["compiled_features"].append("lamp-supervisor E04 build: --features fault-injection")
    base["durability"] = "local"
    base["durability_note"] = "Every E04 capture is a durable local capture."
    kernel = subprocess.run(["uname", "-r"], capture_output=True, text=True).stdout.strip()
    base["e04_volume"] = {
        "lane_filesystem": cfg["lane"], "mkfs": LANES[cfg["lane"]]["mkfs"],
        "mount_options": LANES[cfg["lane"]]["mount"], "lamp_disk_backend": cfg["backend_name"],
        "stack": ["tmpfs file", "loop", "dm-thin pool " + cfg["pool"], "thin snapshot of golden",
                  "dm-log-writes", cfg["lane"]],
        "kernel": kernel,
    }
    json.dump(base, sys.stdout, indent=1)


if __name__ == "__main__":
    main()
