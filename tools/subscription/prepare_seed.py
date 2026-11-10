"""Prepare a new credential-free seed from an existing verified seed, offline."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile


CHUNK = 4 << 20


def object_digest(path):
    digest = hashlib.sha256()
    zero = hashlib.sha256(b"\0" * CHUNK).digest()
    with path.open("rb", buffering=0) as source:
        size = path.stat().st_size
        for offset in range(0, size, CHUNK):
            length = min(CHUNK, size - offset)
            try:
                data = os.lseek(source.fileno(), offset, os.SEEK_DATA)
            except OSError:
                data = size
            if data >= offset + length and length == CHUNK:
                digest.update(zero)
            else:
                source.seek(offset)
                digest.update(hashlib.sha256(source.read(length)).digest())
    return digest.hexdigest()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--base", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--binaries", type=Path, required=True)
    parser.add_argument("--downloads", type=Path, required=True)
    args = parser.parse_args()
    if os.geteuid() != 0 or args.output.exists():
        raise ValueError("root and a new disposable output directory required")
    base = json.loads((args.base / "seed.json").read_text())
    for filename, item in [("vmlinux", base["kernel"]), ("rootfs.ext4", base["rootfs"]), ("packages.txt", base["packages"])]:
        if (args.base / filename).stat().st_size != item["size"] or object_digest(args.base / filename) != item["digest"]:
            raise ValueError("base seed digest mismatch")
    args.output.mkdir(mode=0o700)
    for filename in ["vmlinux", "rootfs.ext4", "packages.txt"]:
        subprocess.run(["cp", "--reflink=always", "--", str(args.base / filename), str(args.output / filename)], check=True)
        os.chmod(args.output / filename, 0o600)
    image = args.output / "rootfs.ext4"
    sources = Path(__file__).resolve().parent
    with tempfile.TemporaryDirectory(prefix="lamp-t9-seed-") as temporary:
        root = Path(temporary)
        commands = []
        for directory in ["/opt/lamp", "/opt/lamp/bin", "/home/lamp-private", "/home/lamp-private/work"]:
            commands.append(f"mkdir {directory}")
        files = {
            "/usr/sbin/lamp-init": args.binaries / "lamp-init",
            "/usr/sbin/lamp-guest-helper": args.binaries / "lamp-guest-helper",
            "/usr/sbin/lamp-guard": args.binaries / "lamp-guard",
            "/opt/lamp/bin/claude": args.downloads / "claude",
            "/opt/lamp/bin/codex": args.downloads / "codex",
            "/usr/local/bin/lamp-subscription": sources / "guest.py",
            "/usr/local/bin/lamp-auth-browser": sources / "capture_browser.py",
            "/opt/lamp/mcp_bridge.py": sources / "mcp_bridge.py",
            "/opt/lamp/relay.py": sources / "relay.py",
        }
        for destination, source in files.items():
            commands.extend([f"rm {destination}", f"write {source} {destination}",
                             f"set_inode_field {destination} mode 0100755"])
        for directory in ["/home/lamp-private", "/home/lamp-private/work"]:
            commands.extend([f"set_inode_field {directory} uid 65537", f"set_inode_field {directory} gid 65537",
                             f"set_inode_field {directory} mode 040700"])
        batch = root / "debugfs.commands"
        batch.write_text("\n".join(commands) + "\n")
        log = subprocess.run(["debugfs", "-w", "-f", str(batch), str(image)], stdout=subprocess.PIPE, stderr=subprocess.STDOUT, check=True)
        (args.output / "prepare.log").write_bytes(log.stdout)
    for name in ["lamp-init", "lamp-guest-helper", "lamp-guard"]:
        base["init"][name] = hashlib.sha256((args.binaries / name).read_bytes()).hexdigest()
    base["name"] = "lamp-subscription-t9"
    base["rootfs"]["digest"] = object_digest(image)
    (args.output / "seed.json").write_text(json.dumps(base, sort_keys=True, separators=(",", ":")) + "\n")
    provenance = json.loads((args.downloads / "vendor-provenance.json").read_text())
    for name, path in [("claude_code", args.downloads / "claude"), ("codex", args.downloads / "codex")]:
        if hashlib.sha256(path.read_bytes()).hexdigest() != provenance[name]["binary_sha256"]:
            raise ValueError("vendor binary provenance mismatch")
    (args.output / "vendor-provenance.json").write_text(json.dumps(provenance, indent=2) + "\n")
    print(json.dumps({"seed_ref": "lamp-seed/lamp-subscription-t9@sha256:" + hashlib.sha256((args.output / "seed.json").read_bytes()).hexdigest()}))


if __name__ == "__main__":
    main()
