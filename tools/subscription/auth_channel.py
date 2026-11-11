"""Trusted owner-only sign-in channel. Challenge/code contents never go to stdout."""
import argparse
import json
import os
from pathlib import Path
import re
import socket


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
    args = parser.parse_args()
    if os.geteuid() != 0 or (args.challenge_file is None) == (args.code_file is None):
        raise ValueError("owner root and exactly one private delivery file required")
    request = {"method": "challenge"}
    if args.code_file:
        code = args.code_file.read_text().strip()
        if len(code) > 4096 or "\n" in code:
            raise ValueError("invalid native authorization code")
        request = {"method": "code", "code": code}
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
