"""Trusted seed-installed relay; task seccomp continues to deny AF_VSOCK."""
import json
import os
from pathlib import Path
import socket
import threading


TASK = "/run/lamp/subscription-task.sock"
AUTH = "/home/lamp-private/auth.sock"


def task_relay():
    server = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    server.bind(TASK)
    os.chown(TASK, 65537, 65537)
    os.chmod(TASK, 0o600)
    server.listen(2)
    while True:
        connection, _ = server.accept()
        with connection:
            connection.settimeout(180)
            try:
                frame = connection.makefile("rb").read(6)
                if frame != b"NEXT\n":
                    continue
                with socket.socket(socket.AF_VSOCK, socket.SOCK_STREAM) as host:
                    host.settimeout(180)
                    host.connect((socket.VMADDR_CID_HOST, 5500))
                    host.sendall(b"NEXT\n")
                    host.shutdown(socket.SHUT_WR)
                    reply = host.makefile("rb").read(32769)
                    if len(reply) <= 32768:
                        connection.sendall(reply)
            except OSError:
                continue


def auth_relay():
    server = socket.socket(socket.AF_VSOCK, socket.SOCK_STREAM)
    server.bind((socket.VMADDR_CID_ANY, 5501))
    server.listen(2)
    while True:
        connection, peer = server.accept()
        with connection:
            if peer[0] != socket.VMADDR_CID_HOST:
                continue
            connection.settimeout(5)
            try:
                line = connection.makefile("rb").readline(8193)
                if len(line) > 8192:
                    continue
                request = json.loads(line)
                allowed = request == {"method": "challenge"} or (
                    isinstance(request, dict) and set(request) == {"method", "code"}
                    and request["method"] == "code" and isinstance(request["code"], str)
                    and len(request["code"]) <= 4096 and "\n" not in request["code"]
                )
                if not allowed:
                    continue
                with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as daemon:
                    daemon.settimeout(5)
                    daemon.connect(AUTH)
                    daemon.sendall(line)
                    reply = daemon.makefile("rb").readline(8193)
                    if len(reply) <= 8192:
                        connection.sendall(reply)
            except (OSError, ValueError, TypeError):
                continue


if __name__ == "__main__":
    if os.geteuid() != 0 or not Path("/opt/lamp/subscription.json").is_file():
        raise SystemExit(1)
    threading.Thread(target=task_relay, daemon=True).start()
    auth_relay()
