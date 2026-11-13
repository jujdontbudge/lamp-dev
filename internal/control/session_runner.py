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
import subprocess
import sys
import tempfile
import termios
import time
import tty

if "artifact_snapshot" not in globals():
    from session_artifact import artifact_apply, artifact_info, artifact_open, artifact_path, artifact_restore, artifact_snapshot, artifact_upload, session_git


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
    state = {"state": "active", "inputs": {}, "size": 0, "epoch": request["epoch"], "events": [], "outstanding": []}
    native = bool(request.get("native"))
    buffered = bytearray()
    pending_write = None

    def capture(chunk):
        nonlocal buffered
        if not native:
            output.write(chunk)
            state["size"] += len(chunk)
            return
        buffered.extend(chunk)
        while b"\n" in buffered:
            line, _, buffered = buffered.partition(b"\n")
            if line.startswith(b"\x1eLAMP "):
                event = json.loads(line[6:])
                if event.get("type") == "input_completed" and event["input_id"] in state["outstanding"]:
                    state["outstanding"].remove(event["input_id"])
                state["events"].append(event)
            else:
                output.write(line + b"\n")
                state["size"] += len(line) + 1
    pid, terminal = pty.fork()
    if pid == 0:
        os.chdir(request["cwd"])
        environment = {"PATH": os.defpath, "TERM": "xterm-256color", "LANG": "C.UTF-8"}
        environment.update(request.get("env", {}))
        os.execvpe(request["argv"][0], request["argv"], environment)
    if native:
        tty.setraw(terminal, termios.TCSANOW)
    os.set_blocking(terminal, False)
    output = (directory / "output").open("ab", buffering=0)
    os.chmod(directory / "output", 0o600)
    listener = socket.socket(socket.AF_UNIX)
    listener.bind(endpoint)
    os.chmod(endpoint, 0o600)
    listener.listen(8)
    save(state_path, state)
    try:
        while True:
            ready, writable, _ = select.select([listener, terminal] if terminal >= 0 else [listener], [terminal] if pending_write and terminal >= 0 else [], [], .1)
            if terminal in writable:
                key, sequence, view = pending_write
                try:
                    view = view[os.write(terminal, view[:16384]):]
                except BlockingIOError:
                    pass
                if view:
                    pending_write = (key, sequence, view)
                else:
                    state["inputs"][key]["state"] = "delivered"
                    pending_write = None
                    save(state_path, state)
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
                        capture(chunk)
                        os.fsync(output.fileno())
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
                            if previous["state"] != "delivered" and not (pending_write and pending_write[:2] == (key, sequence)):
                                raise ValueError("OUTCOME_UNKNOWN")
                        else:
                            if pending_write:
                                raise ValueError("RESOURCE_IN_USE")
                            if sequence != (previous["sequence"] + 1 if previous else 0):
                                raise ValueError("REVISION_CONFLICT")
                            state["inputs"][key] = {"sequence": sequence, "digest": digest, "state": "pending"}
                            if native:
                                input_id = key + "." + str(sequence)
                                state["outstanding"].append(input_id)
                                data = json.dumps({"input_id": input_id, "text": data.decode("utf-8")}).encode() + b"\n"
                            save(state_path, state)
                            pending_write = (key, sequence, memoryview(data))
                    elif action == "cancel":
                        if not native or terminal < 0:
                            raise ValueError("RESOURCE_IN_USE")
                        if state.get("last_cancel") != operation.get("id"):
                            os.kill(pid, signal.SIGUSR1)
                            state["last_cancel"] = operation.get("id")
                            save(state_path, state)
                    elif action == "resize":
                        if terminal < 0:
                            raise ValueError("RESOURCE_IN_USE")
                        fcntl.ioctl(terminal, termios.TIOCSWINSZ, struct.pack("HHHH", operation["rows"], operation["columns"], 0, 0))
                    elif action in ("end", "snapshot"):
                        if action == "snapshot" and operation.get("include_native") and state["outstanding"]:
                            operation["wait"] = True
                        elif action == "snapshot":
                            artifact_snapshot(directory, operation, native)
                        if not operation.get("wait") and terminal >= 0 and (action == "end" or operation.get("fence")):
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
                                    capture(chunk)
                            os.fsync(output.fileno())
                            os.close(terminal)
                            terminal = -1
                            state["state"] = "ended" if action == "end" else "fenced"
                            save(state_path, state)
                    elif action == "git_apply":
                        if state["outstanding"]:
                            operation["wait"] = True
                        else:
                            operation["git_result"] = artifact_apply(directory, operation)
                    elif action != "read":
                        raise ValueError("INVALID_ARGUMENT")
                    offset = operation.get("offset", 0)
                    if not isinstance(offset, int) or not 0 <= offset <= state["size"]:
                        raise ValueError("CURSOR_EXPIRED")
                    with (directory / "output").open("rb") as stream:
                        stream.seek(offset)
                        data = stream.read(32768)
                    event_offset = operation.get("event_offset", 0)
                    reply = {"state": state["state"], "data": base64.b64encode(data).decode(), "offset": offset + len(data), "size": state["size"], "events": state["events"][event_offset:], "event_offset": len(state["events"]), "outstanding": len(state["outstanding"])}
                    if action == "input":
                        reply["input_delivery"] = state["inputs"][operation["lease"]]["state"]
                    if action == "snapshot" and not operation.get("wait"):
                        reply.update(artifact_info(directory, operation))
                        reply["inputs"] = state["inputs"]
                    if operation.get("wait"):
                        reply["wait"] = True
                    if "git_result" in operation:
                        reply.update(operation["git_result"])
                except Exception as error:
                    reply = {"error": str(error) if str(error) in {"REVISION_CONFLICT", "IDEMPOTENCY_CONFLICT", "OUTCOME_UNKNOWN", "RESOURCE_IN_USE", "CURSOR_EXPIRED", "INVALID_ARGUMENT"} else "DATA_UNAVAILABLE", "error_type": type(error).__name__, "error_errno": getattr(error, "errno", None)}
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
    # The guest collector traces forks. A vfork child can otherwise hold the
    # relay thread needed to complete its traced launch.
    subprocess._USE_VFORK = False
    if request["action"] == "exec":
        with tempfile.TemporaryFile() as output, tempfile.TemporaryFile() as error:
            try:
                process = subprocess.run(request["argv"], stdin=subprocess.DEVNULL, stdout=output, stderr=error, cwd="/", timeout=request["timeout_ms"] / 1000)
            except subprocess.TimeoutExpired:
                return {"error": "OUTCOME_UNKNOWN"}
            output.seek(0)
            error.seek(0)
            stdout, stderr = output.read(65537), error.read(65537)
            return {"exit_code": process.returncode, "stdout_base64": base64.b64encode(stdout).decode(), "stderr_base64": base64.b64encode(stderr).decode(), "truncated": len(stdout) > 65536 or len(stderr) > 65536}
    root = Path(request["root"])
    private(root)
    identity = request["id"]
    if not identity or len(identity) > 128 or any(c not in "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-" for c in identity):
        raise ValueError("invalid session identity")
    directory = root / (identity + ".e" + str(request["epoch"]))
    legacy = root / identity
    if (legacy / "state.json").exists() and json.loads((legacy / "state.json").read_text()).get("epoch") == request["epoch"]:
        directory = legacy
    private(directory)
    endpoint = str(directory / "io.sock")
    if len(endpoint.encode()) > 100:
        raise ValueError("runner path too long")
    with (directory / "lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        if request["action"] == "export":
            path = artifact_path(directory, request)
            offset = request.get("artifact_offset", 0)
            with path.open("rb") as source:
                source.seek(offset)
                chunk = source.read(24576)
            return {"artifact_data": base64.b64encode(chunk).decode(), "artifact_offset": offset + len(chunk), "artifact_size": path.stat().st_size}
        if request["action"] == "upload":
            return artifact_upload(directory, request)
        if request["action"] == "restore":
            return dict(artifact_restore(directory, request), restored=True)
        if request["action"] == "start" and not (directory / "state.json").exists():
            request["cwd"] = request["cwd"].replace("{session_dir}", str(directory))
            request["env"] = {key: value.replace("{session_dir}", str(directory)) for key, value in request.get("env", {}).items()}
            if str(directory) + "/" in request["cwd"]:
                Path(request["cwd"]).mkdir(parents=True, mode=0o700, exist_ok=True)
            home = request["env"].get("HOME", "")
            if home.startswith(str(directory) + "/"):
                Path(home).mkdir(parents=True, mode=0o700, exist_ok=True)
            if repository := request.get("repository"):
                cwd = Path(request["cwd"])
                if not (cwd / ".git").exists():
                    source = repository
                    if repository.startswith("bundle:"):
                        bundle = directory / "initial.bundle"
                        bundle.write_bytes(base64.b64decode(repository[7:], validate=True))
                        source = str(bundle)
                    subprocess.run(["git", "-c", "core.hooksPath=/dev/null", "clone", "--quiet", *([] if repository.startswith("bundle:") else ["--depth", "1"]), "--", source, str(cwd)], check=True, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            if shared := request.get("shared_session_id"):
                if not shared or any(c not in "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-." for c in shared):
                    raise ValueError("invalid shared session")
                launch = json.loads((Path(request["root"]) / (shared + ".e" + str(request["shared_epoch"])) / "launch.json").read_text())
                request["cwd"] = launch["cwd"]
            cwd = Path(request["cwd"])
            if (cwd / ".git").is_dir() and not (cwd / ".git/lamp-base").exists():
                (cwd / ".git/lamp-base").write_bytes(session_git(cwd, "rev-parse", "HEAD"))
            if (cwd / ".git").is_dir() and not request.get("shared_session_id"):
                session_git(cwd, "checkout", "-B", "lamp/" + request["id"])
            if native_config := request.get("native"):
                native_config = {key: value.replace("{session_dir}", str(directory)) if isinstance(value, str) else value for key, value in native_config.items()}
                (directory / "native.py").write_text(request["native_source"])
                (directory / "native.json").write_text(json.dumps(native_config))
                request["argv"] = [sys.executable, str(directory / "native.py"), str(directory / "native.json")]
            (directory / "launch.json").write_text(json.dumps(request))
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
        if request["action"] == "snapshot" and state["state"] == "fenced":
            return dict(artifact_info(directory, request), state="fenced", size=state["size"], event_offset=len(state.get("events", [])), inputs=state["inputs"])
        if not Path(endpoint).exists() and state["state"] != "active":
            if request["action"] not in {"read", "end"}:
                return {"error": "RESOURCE_IN_USE"}
            offset = request.get("offset", 0)
            if not isinstance(offset, int) or not 0 <= offset <= state["size"]:
                return {"error": "CURSOR_EXPIRED"}
            with (directory / "output").open("rb") as stream:
                stream.seek(offset)
                data = stream.read(32768)
            return {"state": state["state"], "data": base64.b64encode(data).decode(), "offset": offset + len(data), "size": state["size"], "events": state.get("events", [])[request.get("event_offset", 0):], "event_offset": len(state.get("events", [])), "outstanding": len(state.get("outstanding", []))}
        with socket.socket(socket.AF_UNIX) as connection:
            connection.settimeout(8)
            connection.connect(endpoint)
            connection.sendall(json.dumps(request).encode() + b"\n")
            return receive(connection)


if __name__ == "__main__":
    try:
        os.umask(0o077)
        if sys.argv[1] == "--serve":
            endpoint, root = sys.argv[2:4]
            private(Path(endpoint).parent)
            with socket.socket(socket.AF_UNIX) as listener:
                listener.bind(endpoint)
                listener.listen(8)
                while True:
                    connection, _ = listener.accept()
                    with connection:
                        connection.settimeout(30)
                        try:
                            request = receive(connection)
                            request["root"] = root
                            result = main(request)
                        except Exception:
                            result = {"error": "DATA_UNAVAILABLE"}
                        try:
                            connection.sendall(json.dumps(result).encode() + b"\n")
                        except OSError:
                            # A caller may time out while its command finishes.
                            # Its lost reply must not terminate the shared bridge.
                            pass
        else:
            request = json.loads(base64.b64decode(sys.argv[1], validate=True))
            print(json.dumps(main(request)))
    except Exception:
        print(json.dumps({"error": "DATA_UNAVAILABLE"}))
