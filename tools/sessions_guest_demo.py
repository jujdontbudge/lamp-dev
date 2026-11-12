import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import time

from sessions_demo import LocalHTTP


def capacity():
    count = subprocess.run(["pgrep", "-c", "firecracker"], capture_output=True, text=True)
    live = int(count.stdout.strip() or "0")
    memory = subprocess.check_output(["free", "-m"], text=True)
    available = int(next(line for line in memory.splitlines() if line.startswith("Mem:")).split()[-1])
    if live >= 2 or available < 12288:
        raise RuntimeError("owner allocation condition not met")
    return {"live_firecracker": live, "available_mib": available}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=Path, required=True)
    parser.add_argument("--seed", type=Path, required=True)
    parser.add_argument("--engine-bin", type=Path, required=True)
    parser.add_argument("--lampd", type=Path, required=True)
    parser.add_argument("--keep", action="store_true")
    parser.add_argument("--resume-setup", action="store_true")
    parser.add_argument("--prepare-only", action="store_true")
    args = parser.parse_args()
    if os.geteuid() != 0:
        raise RuntimeError("use an isolated Linux root invocation")
    os.umask(0o077)
    root = args.root
    root.mkdir(mode=0o700, exist_ok=args.resume_setup)
    runtime = root / "r"
    runtime.mkdir(mode=0o755, exist_ok=args.resume_setup)
    runtime.chmod(0o755)
    binaries = root / "bin"
    binaries.mkdir(exist_ok=args.resume_setup)
    for name in ["lamp-supervisor", "lamp-guard"]:
        shutil.copy2(args.engine_bin / name, binaries / name)
    shutil.copy2(args.lampd, binaries / "lampd")
    supervisor = str(binaries / "lamp-supervisor")
    common = ["--store", str(root / "store"), "--mem-dir", str(runtime / "m"), "--netns-dir", str(runtime / "n"), "--uid-base", "1940000000", "--host-id", "hs_t21"]
    events = []
    processes = []
    sandbox = None
    session = None
    api = runtime / "public/api.sock"

    def record(step, **value):
        events.append(dict(step=step, **value))
        (root / "evidence.json").write_text(json.dumps(events, indent=2) + "\n")
        print(json.dumps(events[-1]), flush=True)

    def run(argv, name):
        with (root / (name + ".log")).open("wb") as output:
            subprocess.run(argv, stdin=subprocess.DEVNULL, stdout=output, stderr=output, check=True)

    def start(argv, name):
        with (root / (name + ".log")).open("ab") as output:
            process = subprocess.Popen(argv, stdin=subprocess.DEVNULL, stdout=output, stderr=output, start_new_session=True)
        processes.append(process)
        return process

    def wait(condition, seconds=120):
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            result = condition()
            if result:
                return result
            time.sleep(.1)
        raise RuntimeError("guest observation did not complete")

    def call(method, path, value=None, revision=None):
        headers = {}
        if method != "GET":
            headers = {"Content-Type": "application/json", "Idempotency-Key": os.urandom(16).hex()}
        if revision is not None:
            headers["If-Match"] = str(revision)
        connection = LocalHTTP(api)
        connection.request(method, path, json.dumps(value) if value is not None else None, headers)
        response = connection.getresponse()
        reply = json.loads(response.read())
        connection.close()
        if response.status not in (200, 202):
            raise RuntimeError("public API " + str(response.status) + " " + str(reply.get("error", {}).get("code")))
        return reply

    def resource(kind, identity):
        return call("GET", "/v1/" + kind + "/" + identity)["data"]

    def mutate(kind, identity, action, value):
        return call("POST", "/v1/" + kind + "/" + identity + action, value, resource(kind, identity)["revision"])

    def output():
        reply = call("GET", "/v1/sessions/" + session + "/output")
        return b"".join(base64.b64decode(item["data_base64"]) for item in reply["items"]).decode(errors="replace")

    try:
        if not args.resume_setup:
            record("template-allocation-admission", **capacity())
            run([supervisor, "seed-import", str(args.seed)] + common, "seed")
            seed_ref = "lamp-seed/lamp-subscription-t9@sha256:" + hashlib.sha256((args.seed / "seed.json").read_bytes()).hexdigest()
            definition = root / "task.toml"
            definition.write_text('format="lamp.template/v1"\nname="t21-public-task"\nguest_os="linux"\narch="x86_64"\nseed_ref="' + seed_ref + '"\n')
            run([supervisor, "template-build", str(definition)] + common, "template")
        template = next(json.loads(line)["template_id"] for line in reversed((root / "template.log").read_text().splitlines()) if line.startswith("{"))
        run([supervisor, "grant-install", "--store", str(root / "store"), "--caller-uid", "0", "--ttl-s", "3600"], "grant")
        grant = json.loads((root / "grant.log").read_text())["grant_id"]
        policy = root / "network.json"
        policy.write_text(json.dumps({"guard": str(binaries / "lamp-guard"), "pasta": "/usr/bin/pasta", "audit_dir": str(root / "audit"), "revision": 1, "principal_id": "pr_local"}))
        worker = start([supervisor, "serve"] + common + ["--socket", str(runtime / "w.sock"), "--network-policy", str(policy)], "worker")
        wait(lambda: (runtime / "w.sock").exists())
        config = root / "sessions.json"
        config.write_text(json.dumps({"harnesses": {"hr_guest_shell": {"owner_id": "pr_local", "argv": ["/bin/sh", "-i"], "cwd": "/tmp", "env": {"HOME": "/tmp", "PATH": "/opt/lamp/bin:/usr/local/bin:/usr/bin:/bin"}, "auth_modes": ["none"]}}, "hosts": {}}))
        daemon = start([str(binaries / "lampd"), "--state-dir", str(root / "control"), "--socket", str(api), "--sessions-config", str(config), "--worker-socket", str(runtime / "w.sock"), "--worker-id", "lamp-supervisor@hs_t21", "--worker-boot-id", Path("/proc/sys/kernel/random/boot_id").read_text().strip(), "--worker-uid", "0", "--worker-pid", str(worker.pid), "--grant-id", grant, "--worker-timeout", "1m"], "control")
        wait(api.exists)
        if args.prepare_only:
            args.keep = True
            manifest = {"root": str(root), "runtime": str(runtime), "template_id": template, "worker_pid": worker.pid, "control_pid": daemon.pid, "common": common}
            (root / "manifest.json").write_text(json.dumps(manifest, indent=2))
            record("prepared-without-sandbox", manifest=str(root / "manifest.json"))
            return
        record("sandbox-allocation-admission", **capacity())
        sandbox = call("POST", "/v1/sandboxes", {"template_id": template, "wait_ms": 60000})["result"]["sandbox_id"]
        record("sandbox", id=sandbox, state=resource("sandboxes", sandbox)["state"], boundary=resource("sandboxes", sandbox)["boundary"])
        session = call("POST", "/v1/sessions", {"sandbox_id": sandbox, "harness_id": "hr_guest_shell", "agent_mode": "inside"})["result"]["session_id"]
        wait(lambda: resource("sessions", session)["state"] in ("active", "failed"))
        if resource("sessions", session)["state"] != "active":
            raise RuntimeError("guest PTY session failed")
        lease = call("POST", "/v1/leases", {"type": "input", "resource_id": session, "expires_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(time.time() + 240))})["result"]["lease_id"]
        command = "stty -echo; printf 'GUEST_TTY='; test -t 0 && echo yes; uname -s; printf 'GUEST_HOSTNAME='; hostname; git clone -q --depth 1 https://github.com/octocat/Hello-World.git /tmp/t21-public && cat /tmp/t21-public/README; printf '\\nT21_GUEST_DONE\\n'\n"
        mutate("sessions", session, ":input", {"lease_id": lease, "location_epoch": resource("sessions", session)["location_epoch"], "sequence": 0, "data_base64": base64.b64encode(command.encode()).decode()})
        wait(lambda: "\nT21_GUEST_DONE\r\n" in output())
        observed = output()
        if "GUEST_TTY=yes" not in observed or "Hello World!" not in observed:
            raise RuntimeError("real guest PTY/project observation missing")
        record("real-guest-pty", session_id=session, sandbox_id=sandbox, output=observed, public_repository="octocat/Hello-World")
        if args.keep:
            manifest = {"root": str(root), "runtime": str(runtime), "sandbox_id": sandbox, "session_id": session, "template_id": template, "worker_pid": worker.pid, "control_pid": daemon.pid, "common": common}
            (root / "manifest.json").write_text(json.dumps(manifest, indent=2))
            record("retained-for-ticket", manifest=str(root / "manifest.json"))
            return
    finally:
        if not args.keep or not (root / "manifest.json").exists():
            if session:
                try:
                    mutate("sessions", session, ":end", {"reason": "guest demonstration complete"})
                    wait(lambda: resource("sessions", session)["state"] == "ended")
                except Exception as error:
                    record("session-cleanup-error", error=type(error).__name__)
            if sandbox:
                current = resource("sandboxes", sandbox)
                call("DELETE", "/v1/sandboxes/" + sandbox, {"wait_ms": 60000}, current["revision"])
                wait(lambda: resource("sandboxes", sandbox)["state"] == "deleted")
                record("sandbox-released", sandbox_id=sandbox)
            for process in reversed(processes):
                process.terminate()
                process.wait(timeout=30)


if __name__ == "__main__":
    main()
