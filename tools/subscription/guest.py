#!/usr/bin/python3
import base64
import hashlib
import json
import os
from pathlib import Path
import queue
import re
import signal
import socket
import subprocess
import sys
import threading
import time


HOME = Path("/home/lamp-private")
CONFIG = Path("/opt/lamp/subscription.json")
CONTROL = HOME / "control.sock"
CHALLENGE = HOME / "challenge.json"
MARKER = HOME / "canary"
MAX_CONTROL = 4096


def private_write(path, data):
    descriptor = os.open(path, os.O_CREAT | os.O_TRUNC | os.O_WRONLY, 0o600)
    with os.fdopen(descriptor, "wb") as output:
        output.write(data)
        output.flush()
        os.fsync(output.fileno())


def report(ok=False, **values):
    return dict(ok=ok, refreshed=values.get("refreshed", False),
                signed_in=values.get("signed_in", False), pid=values.get("pid", 0),
                quiesced=values.get("quiesced", False),
                turns=values.get("turns", 0),
                heap_canary_present=values.get("heap_canary_present", False),
                disk_canary_present=values.get("disk_canary_present", False))


class Native:
    def __init__(self, kind, canary):
        for directory in ["codex", "claude", "work"]:
            (HOME / directory).mkdir(mode=0o700, exist_ok=True)
        self.kind = kind
        self.process = None
        self.started = False
        self.identifier = 0
        self.thread = None
        self.messages = queue.Queue()
        self.events = []
        self.canary = canary
        self.login_pty = None
        self.stopped = []
        self.env = {
            "HOME": str(HOME), "PATH": "/usr/local/bin:/usr/bin:/bin",
            "CODEX_HOME": str(HOME / "codex"), "CLAUDE_CONFIG_DIR": str(HOME / "claude"),
            "HTTPS_PROXY": "http://198.18.240.1:3128", "HTTP_PROXY": "http://198.18.240.1:3128",
            "NO_PROXY": "localhost,127.0.0.1", "BROWSER": "/usr/local/bin/lamp-auth-browser",
            "DISABLE_AUTOUPDATER": "1", "DISABLE_TELEMETRY": "1", "DISABLE_ERROR_REPORTING": "1",
            "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "ENABLE_TOOL_SEARCH": "false",
        }
        if canary:
            self.env["LAMP_E07_CANARY"] = canary.decode()

    def credential_fingerprint(self):
        path = HOME / ("codex/auth.json" if self.kind == "codex" else "claude-plan-token")
        return hashlib.sha256(path.read_bytes()).digest() if path.exists() else None

    def start(self):
        if self.process is not None:
            if self.process.poll() is not None:
                raise RuntimeError("native process exited; automatic restart is forbidden")
            return
        if self.started:
            raise RuntimeError("native process restart is forbidden")
        self.started = True
        if self.kind == "codex":
            argv = ["/opt/lamp/bin/codex"]
            for feature in ["shell_tool", "unified_exec", "multi_agent", "apps", "plugins", "hooks",
                            "browser_use", "computer_use", "image_generation", "daemon_auto_start"]:
                argv += ["--disable", feature]
            argv += ["-c", 'cli_auth_credentials_store="file"', "-c", 'web_search="disabled"',
                     "-c", 'sandbox_mode="read-only"', "-c", 'approval_policy="never"',
                     "-c", 'mcp_servers.lamp={command="/usr/bin/python3",args=["/opt/lamp/mcp_bridge.py"],enabled_tools=["next_step"],default_tools_approval_mode="approve",supports_parallel_tool_calls=false,tool_timeout_sec=180}',
                     "app-server"]
        else:
            token = HOME / "claude-plan-token"
            if token.exists():
                self.env["CLAUDE_CODE_OAUTH_TOKEN"] = token.read_text().strip()
            argv = ["/opt/lamp/bin/claude", "-p", "--input-format", "stream-json",
                    "--output-format", "stream-json", "--verbose", "--tools", "",
                    "--allowedTools", "mcp__lamp__next_step", "--permission-mode", "dontAsk",
                    "--strict-mcp-config", "--mcp-config", "/opt/lamp/claude-mcp.json",
                    "--setting-sources", "", "--disable-slash-commands", "--no-chrome"]
        self.stderr = open(HOME / "native.stderr", "ab", buffering=0)
        self.process = subprocess.Popen(argv, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                        stderr=self.stderr, env=self.env, cwd=HOME / "work")
        threading.Thread(target=self.read, daemon=True).start()
        if self.kind == "codex":
            self.rpc("initialize", {"clientInfo": {"name": "lamp_custody", "title": "Lamp custody proof", "version": "1.0.0"}})
            self.send({"method": "initialized"})

    def resume(self):
        for pid in self.stopped:
            try:
                os.kill(pid, signal.SIGCONT)
            except ProcessLookupError:
                pass
        self.stopped = []

    def quiesce(self):
        if self.process is None or self.process.poll() is not None:
            return
        descendants = {self.process.pid}
        # Stop the parent before walking children so it cannot create more.
        os.kill(self.process.pid, signal.SIGSTOP)
        self.wait_stopped(self.process.pid)
        for _ in range(8):
            added = set()
            for path in Path("/proc").glob("[0-9]*/status"):
                try:
                    fields = dict(line.split(":", 1) for line in path.read_text().splitlines() if ":" in line)
                    if int(fields.get("PPid", -1)) in descendants:
                        pid = int(path.parent.name)
                        if pid not in descendants:
                            os.kill(pid, signal.SIGSTOP)
                            self.wait_stopped(pid)
                            added.add(pid)
                except (OSError, ValueError):
                    continue
            descendants.update(added)
            if not added:
                self.stopped = sorted(descendants)
                return
        raise RuntimeError("native process tree did not quiesce")

    @staticmethod
    def wait_stopped(pid):
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline:
            try:
                status = Path(f"/proc/{pid}/status").read_text()
            except FileNotFoundError:
                return
            state = next(line.split()[1] for line in status.splitlines() if line.startswith("State:"))
            if state in ("T", "t", "Z"):
                return
            time.sleep(0.005)
        raise RuntimeError("native process did not acknowledge quiescence")

    def memory_contains_canary(self):
        if self.process is None or not self.canary:
            return False
        pid = self.process.pid
        scanned = 0
        with open(f"/proc/{pid}/mem", "rb", buffering=0) as memory:
            for line in Path(f"/proc/{pid}/maps").read_text().splitlines():
                fields = line.split()
                if not fields[1].startswith("rw") or fields[-1] in ("[stack]", "[vvar]", "[vsyscall]"):
                    continue
                start, end = [int(value, 16) for value in fields[0].split("-")]
                carry = b""
                for offset in range(start, end, 1 << 20):
                    if scanned >= 256 << 20:
                        return False
                    try:
                        block = os.pread(memory.fileno(), min(end - offset, 1 << 20), offset)
                    except OSError:
                        break
                    scanned += len(block)
                    if self.canary in carry + block:
                        return True
                    carry = block[-len(self.canary):]
        return False

    def read(self):
        try:
            for line in self.process.stdout:
                if len(line) > 8 << 20:
                    break
                self.messages.put(json.loads(line))
        except (ValueError, OSError):
            pass
        self.messages.put({"closed": True})

    def send(self, value):
        self.process.stdin.write(json.dumps(value, separators=(",", ":")).encode() + b"\n")
        self.process.stdin.flush()

    def next(self, timeout=180):
        message = self.messages.get(timeout=timeout)
        if message.get("closed"):
            raise RuntimeError("native process closed")
        if self.kind == "codex" and "id" in message and "method" in message:
            self.send({"id": message["id"], "error": {"code": -32601, "message": "client callbacks are not granted"}})
            return self.next(timeout)
        return message

    def rpc(self, method, params):
        self.identifier += 1
        identifier = self.identifier
        self.send({"id": identifier, "method": method, "params": params})
        while True:
            message = self.next()
            if message.get("id") == identifier:
                if "error" in message:
                    raise RuntimeError("official native RPC failed")
                return message.get("result", {})
            self.events.append(message)

    def notification(self, method, timeout=600):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            for index, event in enumerate(self.events):
                if event.get("method") == method:
                    return self.events.pop(index)["params"]
            event = self.next(max(1, deadline - time.monotonic()))
            if event.get("method") == method:
                return event["params"]
            self.events.append(event)
        raise TimeoutError("native notification did not complete")

    def signed_in(self):
        if self.kind == "codex":
            account = self.rpc("account/read", {"refreshToken": False}).get("account")
            return bool(account and account.get("type") == "chatgpt" and account.get("planType") not in (None, "free", "unknown"))
        return (HOME / "claude-plan-token").is_file()

    def sign_in(self):
        before = self.credential_fingerprint()
        if self.kind == "codex":
            self.start()
            challenge = self.rpc("account/login/start", {"type": "chatgptDeviceCode"})
            if challenge.get("type") != "chatgptDeviceCode":
                raise RuntimeError("managed subscription device sign-in required")
            private_write(CHALLENGE, json.dumps(challenge).encode())
            result = self.notification("account/login/completed")
            if not result.get("success"):
                raise RuntimeError("native subscription sign-in failed")
            return self.signed_in(), before != self.credential_fingerprint()
        master, slave = os.openpty()
        process = subprocess.Popen(["/opt/lamp/bin/claude", "setup-token"], stdin=slave,
                                   stdout=slave, stderr=slave, env=self.env, cwd=HOME / "work")
        os.close(slave)
        self.login_pty = master
        captured = bytearray()
        try:
            while True:
                try:
                    block = os.read(master, 4096)
                except OSError:
                    break
                if not block:
                    break
                captured.extend(block)
                if len(captured) > 1 << 20:
                    raise ValueError("native sign-in output limit")
            process.wait(timeout=5)
            tokens = re.findall(rb"sk-ant-oat01-[A-Za-z0-9_-]{20,}", captured)
            if process.returncode != 0 or not tokens:
                raise RuntimeError("native subscription token setup failed")
            private_write(HOME / "claude-plan-token", tokens[-1] + b"\n")
        finally:
            self.login_pty = None
            os.close(master)
            if process.poll() is None:
                process.terminate()
        return True, before != self.credential_fingerprint()

    def turn(self):
        self.start()
        if not self.signed_in():
            raise RuntimeError("subscription sign-in required")
        expected = "LAMP_SUBSCRIPTION_FORK_OK"
        prompt = "Reply with exactly LAMP_SUBSCRIPTION_FORK_OK. Do not use any tools."
        if self.kind == "codex":
            if self.thread is None:
                result = self.rpc("thread/start", {"cwd": str(HOME / "work"), "approvalPolicy": "never", "sandbox": "read-only"})
                self.thread = result["thread"]["id"]
            started = self.rpc("turn/start", {"threadId": self.thread, "input": [{"type": "text", "text": prompt}]})
            identifier = started["turn"]["id"]
            while True:
                completed = self.notification("turn/completed")
                if completed["turn"]["id"] == identifier:
                    responses = [
                        event.get("params", {}).get("item", {}).get("text", "")
                        for event in self.events
                        if event.get("method") == "item/completed"
                        and event.get("params", {}).get("turnId") == identifier
                        and event.get("params", {}).get("item", {}).get("type") == "agentMessage"
                    ]
                    return completed["turn"]["status"] == "completed" and any(
                        response.strip() == expected for response in responses
                    )
        self.send({"type": "user", "message": {"role": "user", "content": prompt}})
        while True:
            message = self.next(600)
            if message.get("type") == "system" and message.get("subtype") == "init":
                if set(message.get("tools", [])) - {"mcp__lamp__next_step"}:
                    raise RuntimeError("native tools exceed installed bridge")
            if message.get("type") == "result":
                return not message.get("is_error", True) and message.get("result", "").strip() == expected


class Daemon:
    def __init__(self):
        self.kind = json.loads(CONFIG.read_text())["harness"]
        self.canary = MARKER.read_bytes() if MARKER.exists() else b""
        self.native = Native(self.kind, self.canary)
        self.turns = 0
        self.authenticated = False
        self.lock = threading.Lock()

    def proof(self, ok=True, refreshed=False):
        process = self.native.process
        pid = process.pid if process is not None and process.poll() is None else 0
        heap = False
        if pid and self.canary:
            heap = self.native.memory_contains_canary()
        quiesced = not pid or bool(self.native.stopped)
        return report(ok, refreshed=refreshed, signed_in=self.authenticated, pid=pid,
                      quiesced=quiesced,
                      turns=self.turns, heap_canary_present=heap,
                      disk_canary_present=bool(self.canary and MARKER.read_bytes() == self.canary))

    def handle(self, request):
        with self.lock:
            mode = request.get("mode")
            before = self.native.credential_fingerprint()
            running = mode in ("sign-in", "start", "turn", "refresh", "stop")
            if running:
                self.native.resume()
            if mode == "init":
                nonce = request.get("nonce", "")
                if not re.fullmatch("[0-9a-f]{32}", nonce) or self.native.started:
                    raise ValueError("canary must be initialized before the native process starts")
                self.canary = b"lamp-e07-canary-" + nonce.encode()
                for name, value in [("canary", self.canary), ("cache/canary", base64.b64encode(self.canary)), ("journal/canary", self.canary.hex().encode())]:
                    path = HOME / name
                    path.parent.mkdir(mode=0o700, exist_ok=True)
                    private_write(path, value)
                self.native = Native(self.kind, self.canary)
            elif mode == "sign-in":
                success, refreshed = self.native.sign_in()
                self.authenticated = success
                self.native.quiesce()
                return self.proof(success, refreshed)
            elif mode == "start":
                self.native.start()
            elif mode == "turn":
                if not self.native.turn():
                    raise RuntimeError("official inference failed")
                self.turns += 1
            elif mode == "refresh":
                if self.kind != "codex":
                    raise ValueError("native long-lived plan tokens are renewed only through setup-token")
                self.native.rpc("account/read", {"refreshToken": True})
                if before == self.native.credential_fingerprint():
                    raise RuntimeError("native proactive refresh did not change managed credentials")
            elif mode == "stop":
                if self.native.process is not None:
                    self.native.process.terminate()
                    self.native.process.wait(timeout=10)
            elif mode not in ("status", "proof"):
                raise ValueError("unknown installed operation")
            if mode in ("start", "turn", "refresh"):
                self.authenticated = self.native.signed_in()
            if running:
                self.native.quiesce()
            return self.proof(refreshed=before != self.native.credential_fingerprint())

    def auth_channel(self):
        listener = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        listener.bind(str(HOME / "auth.sock"))
        os.chmod(HOME / "auth.sock", 0o600)
        listener.listen(2)
        while True:
            connection, _ = listener.accept()
            with connection:
                connection.settimeout(5)
                try:
                    request = json.loads(connection.makefile("rb").readline(8193))
                    if request == {"method": "challenge"}:
                        data = CHALLENGE.read_bytes() if CHALLENGE.exists() else b"{}"
                        connection.sendall(data + b"\n")
                    elif set(request) == {"method", "code"} and request["method"] == "code":
                        code = request["code"]
                        if isinstance(code, str) and len(code) <= 4096 and "\n" not in code and self.native.login_pty is not None:
                            os.write(self.native.login_pty, code.encode() + b"\n")
                            connection.sendall(b'{"accepted":true}\n')
                except (OSError, ValueError, TypeError):
                    continue

    def run(self):
        threading.Thread(target=self.auth_channel, daemon=True).start()
        listener = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        listener.bind(str(CONTROL))
        os.chmod(CONTROL, 0o600)
        listener.listen(4)
        private_write(HOME / "daemon-started", b"1")
        while True:
            connection, _ = listener.accept()
            with connection:
                connection.settimeout(650)
                try:
                    line = connection.makefile("rb").readline(MAX_CONTROL + 1)
                    if len(line) > MAX_CONTROL:
                        raise ValueError("control limit")
                    value = self.handle(json.loads(line))
                except Exception:
                    self.native.quiesce()
                    value = report()
                connection.sendall(json.dumps(value, separators=(",", ":")).encode() + b"\n")


def main():
    os.umask(0o077)
    if len(sys.argv) == 2 and sys.argv[1] == "daemon":
        Daemon().run()
        return 0
    mode = sys.argv[1] if len(sys.argv) >= 2 else ""
    request = {"mode": mode}
    if mode == "init" and len(sys.argv) == 3:
        request["nonce"] = sys.argv[2]
    if not CONTROL.exists():
        if (HOME / "daemon-started").exists():
            raise RuntimeError("persistent native daemon was lost")
        with open(HOME / "daemon.stderr", "ab", buffering=0) as stderr:
            subprocess.Popen(["/usr/bin/python3", __file__, "daemon"], stdin=subprocess.DEVNULL,
                             stdout=subprocess.DEVNULL, stderr=stderr, start_new_session=True)
        deadline = time.monotonic() + 10
        while not CONTROL.exists() and time.monotonic() < deadline:
            time.sleep(0.01)
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as connection:
        connection.settimeout(650)
        connection.connect(str(CONTROL))
        connection.sendall(json.dumps(request).encode() + b"\n")
        value = json.loads(connection.makefile("rb").readline(MAX_CONTROL))
        print(json.dumps(value, separators=(",", ":")))
        return 0 if value.get("ok") else 1


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception:
        print(json.dumps(report(), separators=(",", ":")))
        raise SystemExit(1)
