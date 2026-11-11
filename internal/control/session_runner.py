import base64
import errno
import fcntl
import hashlib
import json
import os
from pathlib import Path
import pty
import select
import signal
import socket
import struct
import sys
import termios
import time


def save(path, value):
    temporary = path.with_suffix(".next")
    with temporary.open("w") as stream:
        os.chmod(temporary, 0o600)
        json.dump(value, stream)
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temporary, path)
    fd = os.open(path.parent, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def private(path):
    path.mkdir(mode=0o700, parents=True, exist_ok=True)
    st = path.lstat()
    if not path.is_dir() or path.is_symlink() or st.st_uid != os.getuid() or st.st_mode & 0o077:
        raise ValueError("runner directory must be private")


def receive(connection):
    data = bytearray()
    while not data.endswith(b"\n"):
        chunk = connection.recv(65536)
        if not chunk:
            raise ValueError("incomplete runner request")
        data.extend(chunk)
        if len(data) > 131072:
            raise ValueError("runner request too large")
    return json.loads(data)


def serve(directory, endpoint, request):
    state_path = directory / "state.json"
    state = {"state": "active", "inputs": {}, "size": 0, "epoch": request["epoch"]}
    pid, terminal = pty.fork()
    if pid == 0:
        os.chdir(request["cwd"])
        environment = {"PATH": os.defpath, "TERM": "xterm-256color", "LANG": "C.UTF-8"}
        environment.update(request.get("env", {}))
        os.execvpe(request["argv"][0], request["argv"], environment)
    output = (directory / "output").open("ab", buffering=0)
    os.chmod(directory / "output", 0o600)
    listener = socket.socket(socket.AF_UNIX)
    listener.bind(endpoint)
    os.chmod(endpoint, 0o600)
    listener.listen(8)
    save(state_path, state)
    try:
        while True:
            ready, _, _ = select.select([listener, terminal] if terminal >= 0 else [listener], [], [], .1)
            if terminal in ready:
                try:
                    chunk = os.read(terminal, 16384)
                except OSError as error:
                    if error.errno != errno.EIO:
                        raise
                    chunk = b""
                if chunk:
                    if state["size"] + len(chunk) > 16 * 1024 * 1024:
                        os.killpg(pid, signal.SIGKILL)
                        state["state"] = "failed"
                        state["error"] = "CONTROL_QUOTA_EXCEEDED"
                    else:
                        output.write(chunk)
                        os.fsync(output.fileno())
                        state["size"] += len(chunk)
                    save(state_path, state)
                else:
                    os.close(terminal)
                    terminal = -1
                    _, status = os.waitpid(pid, 0)
                    if state["state"] != "failed":
                        state["state"] = "ended"
                    state["exit_code"] = os.waitstatus_to_exitcode(status)
                    save(state_path, state)
            if listener not in ready:
                continue
            connection, _ = listener.accept()
            with connection:
                connection.settimeout(5)
                operation = {}
                try:
                    operation = receive(connection)
                    if operation["epoch"] != state["epoch"]:
                        raise ValueError("REVISION_CONFLICT")
                    action = operation["action"]
                    if action == "input":
                        if state["state"] != "active":
                            raise ValueError("RESOURCE_IN_USE")
                        data = base64.b64decode(operation["data"], validate=True)
                        if not data or len(data) > 32768:
                            raise ValueError("INVALID_ARGUMENT")
                        key = operation["lease"]
                        sequence = operation["sequence"]
                        digest = hashlib.sha256(data).hexdigest()
                        previous = state["inputs"].get(key)
                        if previous and previous["sequence"] == sequence:
                            if previous["digest"] != digest:
                                raise ValueError("IDEMPOTENCY_CONFLICT")
                            if previous["state"] != "delivered":
                                raise ValueError("OUTCOME_UNKNOWN")
                        else:
                            if sequence != (previous["sequence"] + 1 if previous else 0):
                                raise ValueError("REVISION_CONFLICT")
                            state["inputs"][key] = {"sequence": sequence, "digest": digest, "state": "pending"}
                            save(state_path, state)
                            view = memoryview(data)
                            while view:
                                view = view[os.write(terminal, view):]
                            state["inputs"][key]["state"] = "delivered"
                            save(state_path, state)
                    elif action == "resize":
                        if terminal < 0:
                            raise ValueError("RESOURCE_IN_USE")
                        fcntl.ioctl(terminal, termios.TIOCSWINSZ, struct.pack("HHHH", operation["rows"], operation["columns"], 0, 0))
                    elif action == "end":
                        if terminal >= 0:
                            os.killpg(pid, signal.SIGTERM)
                            deadline = time.monotonic() + 2
                            while time.monotonic() < deadline:
                                found, _ = os.waitpid(pid, os.WNOHANG)
                                if found:
                                    break
                                time.sleep(.01)
                            else:
                                os.killpg(pid, signal.SIGKILL)
                                os.waitpid(pid, 0)
                            while select.select([terminal], [], [], 0)[0]:
                                try:
                                    chunk = os.read(terminal, 16384)
                                except OSError as error:
                                    if error.errno != errno.EIO:
                                        raise
                                    break
                                if not chunk:
                                    break
                                if state["size"] + len(chunk) <= 16 * 1024 * 1024:
                                    output.write(chunk)
                                    state["size"] += len(chunk)
                            os.fsync(output.fileno())
                            os.close(terminal)
                            terminal = -1
                        state["state"] = "ended"
                        save(state_path, state)
                    elif action != "read":
                        raise ValueError("INVALID_ARGUMENT")
                    offset = operation.get("offset", 0)
                    if not isinstance(offset, int) or not 0 <= offset <= state["size"]:
                        raise ValueError("CURSOR_EXPIRED")
                    with (directory / "output").open("rb") as stream:
                        stream.seek(offset)
                        data = stream.read(32768)
                    reply = {"state": state["state"], "data": base64.b64encode(data).decode(), "offset": offset + len(data), "size": state["size"]}
                except Exception as error:
                    reply = {"error": str(error) if str(error) in {"REVISION_CONFLICT", "IDEMPOTENCY_CONFLICT", "OUTCOME_UNKNOWN", "RESOURCE_IN_USE", "CURSOR_EXPIRED", "INVALID_ARGUMENT"} else "DATA_UNAVAILABLE"}
                connection.sendall(json.dumps(reply).encode() + b"\n")
                if state["state"] != "active" and reply.get("offset") == state["size"]:
                    return
    finally:
        if terminal >= 0:
            try:
                os.killpg(pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
        output.close()
        listener.close()
        os.unlink(endpoint)


def main(request):
    root = Path(request["root"])
    private(root)
    identity = request["id"]
    if not identity or len(identity) > 128 or any(c not in "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-" for c in identity):
        raise ValueError("invalid session identity")
    directory = root / identity
    private(directory)
    endpoint = str(directory / "io.sock")
    if len(endpoint.encode()) > 100:
        raise ValueError("runner path too long")
    with (directory / "lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        if request["action"] == "start" and not (directory / "state.json").exists():
            if os.fork() == 0:
                os.setsid()
                if os.fork() != 0:
                    os._exit(0)
                null = os.open(os.devnull, os.O_RDWR)
                for fd in (0, 1, 2):
                    os.dup2(null, fd)
                os.close(lock.fileno())
                try:
                    serve(directory, endpoint, request)
                finally:
                    os._exit(0)
            os.wait()
            deadline = time.monotonic() + 5
            while not (directory / "state.json").exists():
                if time.monotonic() > deadline:
                    raise ValueError("runner did not become ready")
                time.sleep(.01)
        request["action"] = "read" if request["action"] == "start" else request["action"]
        state = json.loads((directory / "state.json").read_text())
        if state["epoch"] != request["epoch"]:
            return {"error": "REVISION_CONFLICT"}
        if not Path(endpoint).exists() and state["state"] != "active":
            if request["action"] not in {"read", "end"}:
                return {"error": "RESOURCE_IN_USE"}
            offset = request.get("offset", 0)
            if not isinstance(offset, int) or not 0 <= offset <= state["size"]:
                return {"error": "CURSOR_EXPIRED"}
            with (directory / "output").open("rb") as stream:
                stream.seek(offset)
                data = stream.read(32768)
            return {"state": state["state"], "data": base64.b64encode(data).decode(), "offset": offset + len(data), "size": state["size"]}
        with socket.socket(socket.AF_UNIX) as connection:
            connection.settimeout(8)
            connection.connect(endpoint)
            connection.sendall(json.dumps(request).encode() + b"\n")
            return receive(connection)


if __name__ == "__main__":
    try:
        os.umask(0o077)
        request = json.loads(base64.b64decode(sys.argv[1], validate=True))
        print(json.dumps(main(request)))
    except Exception:
        print(json.dumps({"error": "DATA_UNAVAILABLE"}))
