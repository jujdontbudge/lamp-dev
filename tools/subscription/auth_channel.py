"""Trusted owner-only sign-in channel. Challenge/code contents never go to stdout."""
import argparse
import json
import os
from pathlib import Path
import re
import socket
import stat


def exchange(root, sandbox, request):
    if not re.fullmatch(r"sb_[a-f0-9]{32}", sandbox):
        raise ValueError("expected a public sandbox identifier")
    path = root / "store/jail/firecracker" / sandbox.replace("_", "-") / "root/v.sock"
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as connection:
        connection.settimeout(10)
        connection.connect(str(path))
        connection.sendall(b"CONNECT 5501\n")
        stream = connection.makefile("rb")
        if not stream.readline(128).startswith(b"OK "):
            raise ValueError("guest authentication channel refused")
        connection.sendall(json.dumps(request, separators=(",", ":")).encode() + b"\n")
        result = stream.readline(8193)
        if len(result) > 8192:
            raise ValueError("oversized authentication response")
        return json.loads(result)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=Path, required=True)
    parser.add_argument("--sandbox", required=True)
    parser.add_argument("--challenge-file", type=Path)
    parser.add_argument("--code-file", type=Path)
    parser.add_argument("--credential-file", type=Path)
    parser.add_argument("--harness", choices=["claude_code", "codex"])
    args = parser.parse_args()
    if os.geteuid() != 0 or sum(path is not None for path in (args.challenge_file, args.code_file, args.credential_file)) != 1:
        raise ValueError("owner root and exactly one private delivery file required")
    request = {"method": "challenge"}
    if args.code_file:
        code = args.code_file.read_text().strip()
        if len(code) > 4096 or "\n" in code:
            raise ValueError("invalid native authorization code")
        request = {"method": "code", "code": code}
    if args.credential_file:
        if args.harness is None:
            raise ValueError("credential delivery requires its enrolled harness")
        descriptor = os.open(args.credential_file, os.O_RDONLY | os.O_NOFOLLOW)
        with os.fdopen(descriptor, "r") as source:
            info = os.fstat(source.fileno())
            if info.st_uid != os.geteuid() or info.st_mode & 0o077 or not stat.S_ISREG(info.st_mode):
                raise ValueError("credential source must be owner-private")
            credential = source.read(32769).strip()
        if not credential or len(credential.encode()) > 32768:
            raise ValueError("invalid native credential size")
        request = {"method": "credential", "harness": args.harness, "credential": credential}
    result = exchange(args.root, args.sandbox, request)
    if args.challenge_file:
        if result.get("type") not in ("chatgptDeviceCode", "claude_code"):
            raise ValueError("native sign-in challenge is not available")
        descriptor = os.open(args.challenge_file, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
        with os.fdopen(descriptor, "w") as output:
            json.dump(result, output)
            output.flush()
            os.fsync(output.fileno())
    elif result != {"accepted": True}:
        raise ValueError("native authorization code was not accepted")
    print(json.dumps({"delivered": True}))


if __name__ == "__main__":
    main()
