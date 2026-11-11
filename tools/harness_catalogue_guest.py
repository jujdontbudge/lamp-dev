"""Host -> Firecracker sandbox -> host continuation for catalogue harnesses.

Runs as root in a disposable Linux root. It builds a guest seed from an
existing verified Lamp seed plus the official Linux harness binaries, starts
its own worker, broker and lampd, allocates one sandbox, and moves each native
session host -> sandbox -> host with Lamp's teleport. Each hop asks for a
codeword given only before the hop. Model traffic uses the broker with an
OpenRouter zero-price route; the provider key arrives over an SSH-forwarded
socket and stays in the broker.
"""

import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import sys
import tempfile
import time

sys.path.insert(0, str(Path(__file__).resolve().parent))
sys.path.insert(0, str(Path(__file__).resolve().parent / "subscription"))
from broker_demo import request  # noqa: E402
from harness_catalogue_demo import connection_fields  # noqa: E402
from prepare_seed import object_digest  # noqa: E402
from sessions_demo import LocalHTTP  # noqa: E402

BRIDGE_IP, BRIDGE_PORT, HOST_ID = "10.219.25.1", 17780, "hs_t25"


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=Path, required=True)
    parser.add_argument("--base-seed", type=Path, required=True)
    parser.add_argument("--engine-bin", type=Path, required=True, help="lamp-supervisor and lamp-guard matching the base seed")
    parser.add_argument("--supervisor-name", default="lamp-supervisor-next")
    parser.add_argument("--guard", type=Path, help="host network guard binary (default: lamp-guard-next from --engine-bin)")
    parser.add_argument("--lampd", type=Path, required=True)
    parser.add_argument("--broker", type=Path, required=True)
    parser.add_argument("--harness-bin", type=Path, required=True, help="official Linux binaries named by kind")
    parser.add_argument("--kinds", default="opencode,droid")
    parser.add_argument("--credential-socket", required=True)
    parser.add_argument("--public-repository", type=Path, required=True)
    args = parser.parse_args()
    if os.geteuid() != 0:
        raise RuntimeError("run as root in the ticket's disposable root")
    os.umask(0o077)
    root = args.root
    root.mkdir(mode=0o700)
    kinds = args.kinds.split(",")
    events, processes = [], []
    sandbox = None
    runtime = root / "r"
    runtime.mkdir(mode=0o755)
    binaries = root / "bin"
    binaries.mkdir()
    for source, name in [(args.engine_bin / args.supervisor_name, "lamp-supervisor"), (args.guard or args.engine_bin / "lamp-guard-next", "lamp-guard"), (args.lampd, "lampd"), (args.broker, "lamp-broker")]:
        shutil.copy2(source, binaries / name)
    for kind in kinds:
        shutil.copy2(args.harness_bin / kind, binaries / kind)
    supervisor = str(binaries / "lamp-supervisor")
    common = ["--store", str(root / "store"), "--mem-dir", str(runtime / "m"), "--netns-dir", str(runtime / "n"), "--uid-base", "1950000000", "--host-id", HOST_ID]
    api = runtime / "public/api.sock"

    def record(step, **value):
        events.append(dict(step=step, at=time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()), **value))
        (root / "evidence.json").write_text(json.dumps(events, indent=2) + "\n")
        print(json.dumps(events[-1])[:800], flush=True)

    def run(argv, name):
        with (root / (name + ".log")).open("wb") as output:
            subprocess.run(argv, stdin=subprocess.DEVNULL, stdout=output, stderr=output, check=True)
        return (root / (name + ".log")).read_text()

    def start(argv, name):
        with (root / (name + ".log")).open("ab") as output:
            process = subprocess.Popen(argv, stdin=subprocess.DEVNULL, stdout=output, stderr=output, start_new_session=True)
        processes.append(process)
        return process

    def wait(condition, seconds=120):
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            if result := condition():
                return result
            time.sleep(.2)
        raise RuntimeError("observation did not complete")

    def call(method, path, value=None, revision=None):
        headers = {}
        if method != "GET":
            headers = {"Content-Type": "application/json", "Idempotency-Key": secrets.token_hex(16)}
        if revision is not None:
            headers["If-Match"] = str(revision)
        connection = LocalHTTP(api)
        connection.request(method, path, json.dumps(value) if value is not None else None, headers)
        response = connection.getresponse()
        reply = json.loads(response.read())
        connection.close()
        if response.status not in (200, 202):
            raise RuntimeError(method + " " + path + " " + str(response.status) + " " + json.dumps(reply.get("error", {}))[:300])
        return reply

    def session(identity):
        return call("GET", "/v1/sessions/" + identity)["data"]

    def frames(identity):
        return call("GET", "/v1/sessions/" + identity + "/output?limit=1000")["items"]

    def completed(identity, lease):
        for f in frames(identity):
            if f["channel"] == "control":
                event = json.loads(base64.b64decode(f["data_base64"]))
                if event.get("type") == "input_completed" and event.get("input_id", "").startswith(lease):
                    return event
        return None

    def say(identity, text):
        current = session(identity)
        lease = call("POST", "/v1/leases", {"type": "input", "resource_id": identity, "expires_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(time.time() + 280))})["result"]["lease_id"]
        call("POST", "/v1/sessions/" + identity + ":attach", {"lease_id": lease}, revision=current["revision"])
        before = len(frames(identity))
        current = session(identity)
        call("POST", "/v1/sessions/" + identity + ":input", {"lease_id": lease, "location_epoch": current["location_epoch"], "sequence": 0, "data_base64": base64.b64encode(text.encode()).decode()}, revision=current["revision"])
        event = wait(lambda: completed(identity, lease), 280)
        out = b"".join(base64.b64decode(f["data_base64"]) for f in frames(identity)[before:] if f["channel"] == "stdout").decode(errors="replace")
        lease_now = call("GET", "/v1/leases/" + lease)["data"]
        if lease_now.get("state") == "active":
            call("POST", "/v1/leases/" + lease + ":release", {}, revision=lease_now["revision"])
        return event, out

    def guest_exec(argv):
        reply = call("POST", "/v1/execs", {"sandbox_id": sandbox, "argv": argv, "wait_ms": 60000})
        identity = reply["result"]["exec_id"]
        value = wait(lambda: (e := call("GET", "/v1/execs/" + identity)["data"])["state"] in ("exited", "failed", "outcome_unknown") and e, 90)
        if value.get("exit_code") != 0:
            raise RuntimeError("guest exec failed")

    def grant(subject):
        status, value = request("http://localhost/owner/grants", {"sandbox_id": subject, "generation": 1, "peer_ip": BRIDGE_IP, "ttl_seconds": 300, "connections": {"chat": ["chat_completions"]}, "settings": []}, unix=str(model / "owner.sock"))
        if status != 200:
            raise RuntimeError("scoped grant failed")
        return value["token"]

    def refresh():
        # A short-lived scoped Lamp grant (never the provider key) for each side.
        (model / "host.token.next").write_text(grant("sb_host_" + HOST_ID))
        os.replace(model / "host.token.next", model / "host.token")
        if sandbox:
            program = "import base64,os,sys; os.umask(0o077); open('/tmp/lamp-model.token','w').write(sys.argv[1]); open('/tmp/lamp-model-ca.pem','wb').write(base64.b64decode(sys.argv[2]))"
            guest_exec(["python3", "-c", program, grant(sandbox), base64.b64encode((model / "ca.pem").read_bytes()).decode()])

    try:
        # Guest seed: the verified base plus official harness binaries.
        seed = root / "seed"
        seed.mkdir(mode=0o700)
        base = json.loads((args.base_seed / "seed.json").read_text())
        for name in ["vmlinux", "rootfs.ext4", "packages.txt"]:
            subprocess.run(["cp", "--reflink=auto", "--", str(args.base_seed / name), str(seed / name)], check=True)
            os.chmod(seed / name, 0o600)
        with tempfile.TemporaryDirectory() as temporary:
            batch = Path(temporary) / "debugfs"
            commands = []
            for kind in kinds:
                commands += [f"rm /opt/lamp/bin/{kind}", f"write {binaries / kind} /opt/lamp/bin/{kind}", f"set_inode_field /opt/lamp/bin/{kind} mode 0100755"]
            batch.write_text("\n".join(commands) + "\n")
            subprocess.run(["debugfs", "-w", "-f", str(batch), str(seed / "rootfs.ext4")], check=True, capture_output=True)
        base["name"] = "lamp-harness-catalogue-t25"
        base["rootfs"]["digest"] = object_digest(seed / "rootfs.ext4")
        (seed / "seed.json").write_text(json.dumps(base, sort_keys=True, separators=(",", ":")) + "\n")
        provenance = {kind: {"binary_sha256": hashlib.sha256((binaries / kind).read_bytes()).hexdigest(), "version": subprocess.run([str(binaries / kind), "--version"], capture_output=True, text=True, timeout=60).stdout.strip()[:80]} for kind in kinds}
        record("guest-seed", base=str(args.base_seed), harnesses=provenance)
        run([supervisor, "seed-import", str(seed)] + common, "seed")
        seed_ref = "lamp-seed/lamp-harness-catalogue-t25@sha256:" + hashlib.sha256((seed / "seed.json").read_bytes()).hexdigest()
        (root / "task.toml").write_text('format="lamp.template/v1"\nname="t25-harness-catalogue"\nguest_os="linux"\narch="x86_64"\nseed_ref="' + seed_ref + '"\n')
        template = next(json.loads(line)["template_id"] for line in reversed(run([supervisor, "template-build", str(root / "task.toml")] + common, "template").splitlines()) if line.startswith("{"))
        worker_grant = json.loads(run([supervisor, "grant-install", "--store", str(root / "store"), "--caller-uid", "0", "--ttl-s", "3600"], "grant"))["grant_id"]

        # Broker on a ticket-only address the guest may reach, with a
        # CA:FALSE serverAuth leaf signed by a disposable CA.
        subprocess.run(["ip", "link", "add", "lamp-t25br", "type", "dummy"], check=True)
        subprocess.run(["ip", "addr", "add", BRIDGE_IP + "/32", "dev", "lamp-t25br"], check=True)
        subprocess.run(["ip", "link", "set", "lamp-t25br", "up"], check=True)
        model = root / "model"
        model.mkdir(mode=0o700)
        quiet = {"check": True, "stdout": subprocess.DEVNULL, "stderr": subprocess.DEVNULL}
        subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", str(model / "ca.key"), "-out", str(model / "ca.pem"), "-days", "2", "-subj", "/CN=lamp-t25-disposable-ca", "-addext", "basicConstraints=critical,CA:TRUE"], **quiet)
        subprocess.run(["openssl", "req", "-newkey", "rsa:2048", "-nodes", "-keyout", str(model / "tls.key"), "-out", str(model / "tls.csr"), "-subj", "/CN=" + BRIDGE_IP], **quiet)
        (model / "tls.ext").write_text("basicConstraints=critical,CA:FALSE\nextendedKeyUsage=serverAuth\nsubjectAltName=IP:" + BRIDGE_IP + "\n")
        subprocess.run(["openssl", "x509", "-req", "-in", str(model / "tls.csr"), "-CA", str(model / "ca.pem"), "-CAkey", str(model / "ca.key"), "-CAcreateserial", "-out", str(model / "tls.pem"), "-days", "2", "-extfile", str(model / "tls.ext")], **quiet)
        fields = connection_fields()["chat_completions"]
        reader = ["/usr/bin/python3", str(Path(__file__).resolve().parent / "broker_demo.py"), "credential", args.credential_socket]
        (model / "broker.json").write_text(json.dumps({"connections": {"chat": {"kind": "model", "access": "full", "endpoint": "https://openrouter.ai/api/v1/chat/completions", "credential_command": reader, "models": ["openrouter/free"], "free_only": True, "operations": {"chat_completions": {"write": True, "fields": fields}}}}, "settings": {}}))
        start([str(binaries / "lamp-broker"), "--config", str(model / "broker.json"), "--owner-socket", str(model / "owner.sock"), "--task-listen", BRIDGE_IP + ":" + str(BRIDGE_PORT), "--tls-cert", str(model / "tls.pem"), "--tls-key", str(model / "tls.key"), "--audit", str(model / "audit.jsonl")], "broker")
        policy = root / "network.json"
        policy.write_text(json.dumps({"guard": str(binaries / "lamp-guard"), "pasta": "/usr/bin/pasta", "audit_dir": str(root / "audit"), "revision": 1, "principal_id": "pr_local", "exceptions": [{"address": BRIDGE_IP, "transport": "tcp", "port": BRIDGE_PORT}]}))
        worker = start([supervisor, "serve"] + common + ["--socket", str(runtime / "w.sock"), "--network-policy", str(policy)], "worker")
        wait(lambda: (runtime / "w.sock").exists() and (model / "owner.sock").exists(), 60)

        endpoint = "https://" + BRIDGE_IP + ":" + str(BRIDGE_PORT) + "/v1/models/chat/native"
        bundle = root / "public.bundle"
        subprocess.run(["git", "-C", str(args.public_repository), "bundle", "create", str(bundle), "--all"], check=True, capture_output=True)
        harnesses = {}
        for kind in kinds:
            inside = {"binary": "/opt/lamp/bin/" + kind, "credential_file": "/tmp/lamp-model.token", "ca_file": "/tmp/lamp-model-ca.pem"}
            host = {"binary": str(binaries / kind), "credential_file": str(model / "host.token"), "ca_file": str(model / "ca.pem")}
            harnesses["hr_" + kind] = {"owner_id": "pr_local", "argv": ["python3"], "cwd": "{session_dir}/work", "env": {"HOME": "{session_dir}/home", "PATH": "/opt/lamp/bin:/usr/local/bin:/usr/bin:/bin", "LANG": "C.UTF-8"}, "auth_modes": ["api_key"], "repository": "bundle:" + base64.b64encode(bundle.read_bytes()).decode(), "native": {"kind": kind, "binary": "/opt/lamp/bin/" + kind, "model": "openrouter/free", "endpoint": endpoint, "profiles": {"sandbox:" + HOST_ID: inside, "sandbox:hs_local": inside, "host:hs_lx": host}}}
        (root / "sessions.json").write_text(json.dumps({"harnesses": harnesses, "hosts": {"hs_lx": {"owner_id": "pr_local", "command": ["/usr/bin/python3"], "root": str(root / "host-sessions")}}}))
        start([str(binaries / "lampd"), "--state-dir", str(root / "control"), "--socket", str(api), "--sessions-config", str(root / "sessions.json"), "--worker-socket", str(runtime / "w.sock"), "--worker-id", "lamp-supervisor@" + HOST_ID, "--worker-boot-id", Path("/proc/sys/kernel/random/boot_id").read_text().strip(), "--worker-uid", "0", "--worker-pid", str(worker.pid), "--grant-id", worker_grant, "--worker-timeout", "1m"], "lampd")
        wait(lambda: api.exists(), 60)

        # Let the seed and template writes reach disk first, so the guard's
        # 1 s durable-audit deadline measures the sandbox, not this setup.
        subprocess.run(["sync"], check=True)
        live = int(subprocess.run(["pgrep", "-c", "firecracker"], capture_output=True, text=True).stdout.strip() or "0")
        available = int(next(line for line in subprocess.check_output(["free", "-m"], text=True).splitlines() if line.startswith("Mem:")).split()[-1])
        record("allocation-admission", live_firecracker=live, available_mib=available)
        if live >= 2 or available < 12288:
            raise RuntimeError("owner allocation condition not met")
        sandbox = call("POST", "/v1/sandboxes", {"template_id": template, "wait_ms": 60000})["result"]["sandbox_id"]
        wait(lambda: call("GET", "/v1/sandboxes/" + sandbox)["data"]["state"] == "ready", 120)
        record("sandbox", sandbox_id=sandbox, boundary=call("GET", "/v1/sandboxes/" + sandbox)["data"].get("boundary"))
        placed = call("GET", "/v1/sandboxes/" + sandbox)["data"].get("host_id") or "hs_local"
        hops = [{"kind": "sandbox", "host_id": placed, "sandbox_id": sandbox}, {"kind": "host", "host_id": "hs_lx"}]
        for kind in kinds:
            refresh()
            identity = call("POST", "/v1/sessions", {"sandbox_id": "", "harness_id": "hr_" + kind, "auth_mode": "api_key", "location": {"kind": "host", "host_id": "hs_lx"}})["result"]["session_id"]
            wait(lambda: session(identity)["state"] == "active", 60)
            word = "LAMP" + secrets.token_hex(3).upper()
            first, out = say(identity, "Remember this codeword: " + word + ". Do not use any tools. Reply with only READY.")
            steps = [{"location": session(identity)["location"], "epoch": session(identity)["location_epoch"], "output": out.strip()[-200:], "success": first.get("success"), "continuation_ref": first.get("continuation_ref")}]
            for hop in hops:
                refresh()
                current = session(identity)
                moving = call("POST", "/v1/sessions/" + identity + ":teleport", {"location": hop}, revision=current["revision"])
                try:
                    wait(lambda: (s := session(identity))["state"] == "active" and s["location"].get("kind") == hop["kind"] and s["location_epoch"] > current["location_epoch"], 180)
                except RuntimeError:
                    operation = moving.get("operation_id") or moving.get("result", {}).get("operation_id", "")
                    record("teleport-incomplete", kind=kind, hop=hop, session=session(identity), operation=call("GET", "/v1/operations/" + operation)["data"] if operation else moving)
                    raise
                previous, word = word, "LAMP" + secrets.token_hex(3).upper()
                event, out = say(identity, "What was the codeword I gave you most recently? Reply with only that codeword on the first line, then remember this new codeword: " + word + ". Do not use any tools.")
                steps.append({"location": session(identity)["location"], "epoch": session(identity)["location_epoch"], "asked_for": previous, "recalled": previous in out, "output": out.strip()[-200:], "success": event.get("success"), "continuation_ref": event.get("continuation_ref"), "forked_from": event.get("forked_from")})
            record("host-sandbox-host", kind=kind, session_id=identity, steps=steps, continued=all(s.get("recalled", True) and s["success"] for s in steps) and len({s["continuation_ref"] for s in steps}) == 1)
            current = session(identity)
            call("POST", "/v1/sessions/" + identity + ":end", {}, revision=current["revision"])
    finally:
        if sandbox:
            try:
                current = call("GET", "/v1/sandboxes/" + sandbox)["data"]
                call("DELETE", "/v1/sandboxes/" + sandbox, {"wait_ms": 60000}, current["revision"])
                wait(lambda: call("GET", "/v1/sandboxes/" + sandbox)["data"]["state"] in ("deleted", "released"), 120)
                record("sandbox-deleted", sandbox_id=sandbox)
            except Exception as error:  # noqa: BLE001 - report, then still stop services
                record("sandbox-delete-failed", error=str(error)[:300])
        for process in reversed(processes):
            process.terminate()
            try:
                process.wait(timeout=20)
            except subprocess.TimeoutExpired:
                process.kill()
        subprocess.run(["ip", "link", "del", "lamp-t25br"], stderr=subprocess.DEVNULL)


if __name__ == "__main__":
    main()
