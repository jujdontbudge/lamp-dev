"""Owned LX fixture setup and public-API observations; never emits auth material."""
import argparse
import base64
import hashlib
import http.client
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import time
import uuid


class LocalHTTP(http.client.HTTPConnection):
    def __init__(self, path):
        super().__init__("localhost", timeout=70)
        self.path = str(path)

    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(self.timeout)
        self.sock.connect(self.path)


class Fixture:
    def __init__(self, root, runtime):
        self.root = root
        self.runtime = runtime

    def record(self, event, value):
        with (self.root / "observations.jsonl").open("a") as output:
            output.write(json.dumps({"event": event, "value": value, "observed_unix_ns": time.time_ns()}, sort_keys=True) + "\n")
            output.flush()
            os.fsync(output.fileno())

    def api(self, method, path, body=None, revision=None):
        headers = {}
        if body is not None:
            headers = {"Content-Type": "application/json", "Idempotency-Key": uuid.uuid4().hex}
        if revision is not None:
            headers["If-Match"] = str(revision)
        connection = LocalHTTP(self.runtime / "public/api.sock")
        connection.request(method, path, json.dumps(body) if body is not None else None, headers)
        response = connection.getresponse()
        value = json.loads(response.read())
        connection.close()
        if response.status >= 400:
            self.record("public_error", {"status": response.status, "path": path, "error": value.get("error", {}).get("code")})
            raise RuntimeError("public request failed: " + value.get("error", {}).get("code", "unknown"))
        return value

    def mutate(self, path, body, revision=None):
        envelope = self.api("POST", path, body, revision)
        identifier = envelope.get("operation", {}).get("id") or envelope.get("data", {}).get("id")
        if not identifier:
            raise ValueError("public operation ID absent")
        deadline = time.monotonic() + 650
        while time.monotonic() < deadline:
            operation = self.api("GET", "/v1/operations/" + identifier)["data"]
            if operation["state"] == "succeeded":
                result = operation["outcome"]["result"]
                self.record("public_completion", {"path": path, "operation_id": identifier, "result": result})
                return result
            if operation["state"] in ("failed", "outcome_unknown", "cancelled"):
                self.record("public_failure", {"path": path, "operation_id": identifier, "error": operation.get("error", {})})
                raise RuntimeError("public operation failed")
            time.sleep(0.05)
        raise TimeoutError("public operation not completed")

    def exec_start(self, sandbox, argv):
        return self.mutate("/v1/execs", {"sandbox_id": sandbox, "argv": argv, "cwd": "/"})["exec_id"]

    def exec_done(self, identifier, timeout=60):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            value = self.api("GET", "/v1/execs/" + identifier)["data"]
            if value["state"] in ("exited", "failed", "outcome_unknown"):
                frames = self.api("GET", f"/v1/execs/{identifier}/output")["items"]
                self.record("exec_completion", {"exec_id": identifier, "state": value["state"], "exit_code": value.get("exit_code")})
                return value, frames
            time.sleep(0.1)
        raise TimeoutError("guest command not completed")

    def command(self, sandbox, mode, nonce=None):
        argv = ["/usr/local/bin/lamp-subscription", mode] + ([nonce] if nonce else [])
        identifier = self.exec_start(sandbox, argv)
        done, output = self.exec_done(identifier, 650)
        self.record("harness_receipt", {"sandbox": sandbox, "mode": mode, "output": output})
        return done, output


def setup(args):
    if os.geteuid() != 0 or args.root.exists() or args.runtime.exists():
        raise ValueError("root and new disposable store/runtime paths required")
    args.root.mkdir(mode=0o700)
    args.runtime.mkdir(mode=0o755)
    os.chmod(args.runtime, 0o755)
    fixture = Fixture(args.root, args.runtime)
    binaries = args.root / "bin"
    binaries.mkdir()
    for name in ["lamp-supervisor", "lamp-guard"]:
        shutil.copyfile(args.binaries / name, binaries / name)
        os.chmod(binaries / name, 0o755)
    shutil.copyfile(args.lampd, binaries / "lampd")
    os.chmod(binaries / "lampd", 0o755)
    supervisor = str(binaries / "lamp-supervisor")
    common = ["--store", str(args.root / "store"), "--mem-dir", str(args.runtime / "m"),
              "--netns-dir", str(args.runtime / "n"), "--uid-base", "2080000000", "--host-id", "hs_t9"]

    def run(command, name):
        with (args.root / (name + ".log")).open("wb") as log:
            subprocess.run(command, stdout=log, stderr=subprocess.STDOUT, check=True)

    run([supervisor, "seed-import", str(args.seed)] + common, "import")
    seed = "lamp-seed/lamp-subscription-t9@sha256:" + hashlib.sha256((args.seed / "seed.json").read_bytes()).hexdigest()
    templates = {}
    definitions = {}
    kinds = ["task", args.harness] if args.harness else ["task", "claude_code", "codex"]
    for kind in kinds:
        script = (
            "import json,pathlib,subprocess\n"
            "root=pathlib.Path('/opt/lamp')\n"
            + (
                "root.joinpath('subscription.json').write_text(" + repr(json.dumps({"harness": kind})) + ")\n"
                "root.joinpath('claude-mcp.json').write_text(" + repr(json.dumps({"mcpServers": {"lamp": {"command": "/usr/bin/python3", "args": ["/opt/lamp/mcp_bridge.py"]}}})) + ")\n"
                if kind != "task" else "assert not root.joinpath('subscription.json').exists()\n"
            )
        )
        if kind != "task":
            installed = {
                "/usr/local/bin/lamp-subscription": "guest.py",
                "/usr/local/bin/lamp-auth-browser": "capture_browser.py",
                "/opt/lamp/mcp_bridge.py": "mcp_bridge.py",
                "/opt/lamp/relay.py": "relay.py",
            }
            prefix = "import base64,pathlib\n"
            for destination, filename in installed.items():
                encoded = base64.b64encode(Path(__file__).with_name(filename).read_bytes()).decode()
                prefix += f"pathlib.Path({destination!r}).write_bytes(base64.b64decode({encoded!r}))\n"
            script = prefix + script
        name = kind.replace("_", "-")
        text = f'format="lamp.template/v1"\nname="t9-{name}"\nguest_os="linux"\narch="x86_64"\nseed_ref="{seed}"\n[[steps]]\nargv=' + json.dumps(["/usr/bin/python3", "-c", script]) + "\n"
        definition = args.root / f"{kind}.toml"
        definition.write_text(text)
        run([supervisor, "template-build", str(definition)] + common, "build-" + kind)
        lines = (args.root / ("build-" + kind + ".log")).read_text().splitlines()
        report = next(json.loads(line) for line in reversed(lines) if line.startswith("{"))
        templates[kind] = report["template_id"]
        definitions[kind] = text
        fixture.record("template_built", {"harness": kind, "report": report})
    policy = {"owner_id": "pr_local", "custody_host_ids": ["hs_t9"], "templates": {},
              "task_template": templates["task"], "steps": []}
    for kind in kinds[1:]:
        policy["templates"][templates[kind]] = {
            "harness": kind, "definition": definitions[kind],
            "approved_nonsecret_digest": hashlib.sha256(definitions[kind].encode()).hexdigest(),
        }
    build = "test ! -e /home/lamp-private/canary; test ! -e /home/lamp-private/codex/auth.json; test ! -e /home/lamp-private/claude-plan-token; test -z \"${LAMP_E07_CANARY:-}\"; cd /tmp; printf '#include <stdio.h>\\nint main(void){puts(\"lamp-token-free-build\");return 0;}\\n' > lamp.c; cc lamp.c -o lamp-task && ./lamp-task; python3 -c 'import urllib.request,hashlib; r=urllib.request.urlopen(\"https://raw.githubusercontent.com/openai/codex/main/README.md\",timeout=20).read(1048576); print(\"download_sha256=\"+hashlib.sha256(r).hexdigest())'; git ls-remote https://github.com/openai/codex.git HEAD"
    for _ in range(8):
        policy["steps"].append({"argv": ["/bin/sh", "-ec", build], "cwd": "/", "timeout_s": 30})
    (args.root / "subscription.json").write_text(json.dumps(policy))
    network = {"guard": str(binaries / "lamp-guard"), "pasta": "/usr/bin/pasta",
               "audit_dir": str(args.root / "audit"), "revision": 1, "principal_id": "pr_local"}
    (args.root / "network.json").write_text(json.dumps(network))
    run([supervisor, "grant-install", "--store", str(args.root / "store"), "--caller-uid", "0"], "grant")
    grant = json.loads((args.root / "grant.log").read_text())
    with (args.root / "supervisor.log").open("wb") as log:
        worker = subprocess.Popen([supervisor, "serve"] + common + [
            "--socket", str(args.runtime / "w.sock"), "--network-policy", str(args.root / "network.json"),
            "--subscription-policy", str(args.root / "subscription.json"), "--exports-dir", str(args.root / "exports")],
            stdin=subprocess.DEVNULL, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
    deadline = time.monotonic() + 180
    while not (args.runtime / "w.sock").exists() and worker.poll() is None and time.monotonic() < deadline:
        time.sleep(0.05)
    if not (args.runtime / "w.sock").exists():
        raise RuntimeError("isolated supervisor failed")
    boot = Path("/proc/sys/kernel/random/boot_id").read_text().strip()
    with (args.root / "control.log").open("wb") as log:
        control = subprocess.Popen([str(binaries / "lampd"), "--socket", str(args.runtime / "public/api.sock"),
            "--state-dir", str(args.root / "control"), "--worker-socket", str(args.runtime / "w.sock"),
            "--worker-id", "lamp-supervisor@hs_t9", "--worker-boot-id", boot, "--worker-uid", "0",
            "--worker-pid", str(worker.pid), "--grant-id", grant["grant_id"], "--worker-timeout", "1m",
            "--exports-dir", str(args.root / "exports"), "--share-reader", "65534:pr_reader"],
            stdin=subprocess.DEVNULL, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
    deadline = time.monotonic() + 60
    while not (args.runtime / "public/api.sock").exists() and control.poll() is None and time.monotonic() < deadline:
        time.sleep(0.05)
    if not (args.runtime / "public/api.sock").exists():
        raise RuntimeError("isolated public control failed")
    result = {"templates": templates, "worker_pid": worker.pid, "control_pid": control.pid, "root": str(args.root), "runtime": str(args.runtime)}
    (args.root / "fixture.json").write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps(result))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=Path, required=True)
    parser.add_argument("--runtime", type=Path, required=True)
    parser.add_argument("--seed", type=Path)
    parser.add_argument("--binaries", type=Path)
    parser.add_argument("--lampd", type=Path)
    parser.add_argument("mode", choices=["setup", "create", "command", "begin-sign-in"])
    parser.add_argument("--harness", choices=["claude_code", "codex"])
    parser.add_argument("--sandbox")
    parser.add_argument("--operation")
    args = parser.parse_args()
    os.umask(0o077)
    if args.mode == "setup":
        setup(args)
        return
    fixture = Fixture(args.root, args.runtime)
    if args.mode == "create":
        info = json.loads((args.root / "fixture.json").read_text())
        result = fixture.mutate("/v1/sandboxes", {"template_id": info["templates"][args.harness]})
        print(json.dumps(result))
    elif args.mode == "command":
        print(json.dumps(fixture.command(args.sandbox, args.operation)))
    elif args.mode == "begin-sign-in":
        identifier = fixture.exec_start(args.sandbox, ["/usr/local/bin/lamp-subscription", "sign-in"])
        print(json.dumps({"exec_id": identifier}))


if __name__ == "__main__":
    main()
