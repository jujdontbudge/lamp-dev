"""Live harness catalogue run through lampd's public session API on one Mac.

For each official harness: create a session on enrolled host location hs_a,
give it a codeword, teleport it with Lamp's continuation artifact to hs_b
(a different private root, so a different workspace path), and ask for the
codeword there. Claude Code is also run with its own sub-agent feature so the
sub-agent shows up as a separate child session. Model traffic uses Lamp's
broker with an OpenRouter zero-price route; no owner project or account is used.
"""

import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import secrets
import shutil
import socket
import subprocess
import sys
import threading
import time
from datetime import datetime, timezone

from broker_demo import request
from sessions_demo import LocalHTTP

SOURCE = Path(__file__).resolve().parents[1]
ROUTES = {"claude": "anthropic", "codex": "responses"}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=Path, required=True)
    parser.add_argument("--bin-dir", type=Path, required=True, help="directory with lampd and lamp-broker")
    parser.add_argument("--public-repository", type=Path, required=True)
    parser.add_argument("--binary", action="append", default=[], help="kind=/absolute/official/binary")
    parser.add_argument("--path", default=os.environ["PATH"])
    parser.add_argument("--subagent-kind", action="append", default=[], help="also enroll this kind with native_subagents and show its children (default: claude when given)")
    parser.add_argument("--skip-teleport", action="store_true", help="only run the native sub-agent step")
    args = parser.parse_args()
    os.umask(0o077)
    root = args.root.resolve()
    root.mkdir(mode=0o700)
    binaries = dict(item.split("=", 1) for item in args.binary)
    transcript = []
    processes = []
    owner = secrets.token_urlsafe(32)
    api = root / "api.sock"

    def record(step, **value):
        transcript.append(dict(step=step, at=datetime.now(timezone.utc).isoformat(), **value))
        (root / "evidence.json").write_text(json.dumps(transcript, indent=2) + "\n")
        print(json.dumps(transcript[-1])[:600], flush=True)

    def start(argv, log):
        stream = (root / log).open("ab")
        process = subprocess.Popen(argv, stdin=subprocess.DEVNULL, stdout=stream, stderr=stream)
        processes.append(process)
        return process

    def call(method, path, value=None, revision=None, expected=200):
        connection = LocalHTTP(api)
        headers = {"Authorization": "Bearer " + owner}
        if method != "GET":
            headers.update({"Content-Type": "application/json", "Idempotency-Key": secrets.token_hex(16)})
        if revision is not None:
            headers["If-Match"] = str(revision)
        connection.request(method, path, json.dumps(value) if value is not None else None, headers)
        response = connection.getresponse()
        reply = json.loads(response.read())
        connection.close()
        if response.status != expected and not (expected == 200 and response.status == 202):
            raise RuntimeError(method + " " + path + ": " + str(response.status) + " " + json.dumps(reply.get("error", {}))[:300])
        return reply

    def session(identity):
        return call("GET", "/v1/sessions/" + identity)["data"]

    def wait(condition, timeout=60):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if value := condition():
                return value
            time.sleep(.2)
        raise RuntimeError("live observation did not arrive")

    def frames(identity):
        return call("GET", "/v1/sessions/" + identity + "/output?limit=1000")["items"]

    def stdout(identity, after=0):
        return b"".join(base64.b64decode(f["data_base64"]) for f in frames(identity)[after:] if f["channel"] == "stdout").decode(errors="replace")

    def completed(identity, input_id):
        for f in frames(identity):
            if f["channel"] == "control":
                event = json.loads(base64.b64decode(f["data_base64"]))
                if event.get("type") == "input_completed" and event.get("input_id", "").startswith(input_id):
                    return event
        return None

    def say(identity, text, timeout=300):
        current = session(identity)
        lease = call("POST", "/v1/leases", {"type": "input", "resource_id": identity, "expires_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(time.time() + 280))})["result"]["lease_id"]
        call("POST", "/v1/sessions/" + identity + ":attach", {"lease_id": lease}, revision=current["revision"])
        before = len(frames(identity))
        current = session(identity)
        call("POST", "/v1/sessions/" + identity + ":input", {"lease_id": lease, "location_epoch": current["location_epoch"], "sequence": 0, "data_base64": base64.b64encode(text.encode()).decode()}, revision=current["revision"])
        event = wait(lambda: completed(identity, lease), timeout)
        lease_now = call("GET", "/v1/leases/" + lease)["data"]
        if lease_now.get("state") == "active":
            call("POST", "/v1/leases/" + lease + ":release", {}, revision=lease_now["revision"])
        return event, stdout(identity, before)

    # Broker with three native wire routes; grants are renewed in the background.
    reader = "import json,pathlib; s=json.loads(pathlib.Path.home().joinpath('.pi/agent/auth.json').read_text())['openrouter']; print(next(s[k] for k in ('key','apiKey','api_key') if isinstance(s.get(k),str)))"
    sys.argv = [sys.argv[0]]
    import harness_catalogue_demo as demo
    fields = demo.connection_fields()
    connections = {name: {"kind": "model", "access": "full", "endpoint": endpoint, "credential_command": [sys.executable, "-c", reader], "models": ["openrouter/free"], "free_only": True, "operations": {operation: {"write": True, "fields": fields[operation]}}} for name, operation, endpoint in [("anthropic", "messages", "https://openrouter.ai/api/v1/messages"), ("responses", "responses", "https://openrouter.ai/api/v1/responses"), ("chat", "chat_completions", "https://openrouter.ai/api/v1/chat/completions")]}
    (root / "broker.json").write_text(json.dumps({"connections": connections, "settings": {}}))
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
    start([str(args.bin_dir / "lamp-broker"), "--config", str(root / "broker.json"), "--owner-socket", str(root / "owner.sock"), "--task-listen", "127.0.0.1:" + str(port), "--audit", str(root / "broker-audit.jsonl")], "broker.log")
    wait(lambda: (root / "owner.sock").exists(), 15)
    token = root / "transport.token"
    stop = threading.Event()

    def renew():
        while True:
            status, grant = request("http://localhost/owner/grants", {"sandbox_id": "sb_t25_live", "generation": 1, "peer_ip": "127.0.0.1", "ttl_seconds": 300, "connections": {"anthropic": ["messages"], "responses": ["responses"], "chat": ["chat_completions"]}, "settings": []}, unix=str(root / "owner.sock"))
            if status == 200:
                (root / "transport.next").write_text(grant["token"])
                os.replace(root / "transport.next", token)
            if stop.wait(200):
                return
    threading.Thread(target=renew, daemon=True).start()
    wait(lambda: token.exists(), 15)

    bundle = root / "hello.bundle"
    subprocess.run(["git", "-C", str(args.public_repository), "bundle", "create", str(bundle), "--all"], check=True, capture_output=True)
    repository = "bundle:" + base64.b64encode(bundle.read_bytes()).decode()
    harnesses = {}
    for kind, binary in binaries.items():
        connection = ROUTES.get(kind, "chat")
        endpoint = "http://127.0.0.1:" + str(port) + "/v1/models/" + connection + ("/native" if connection == "chat" else "")
        native = {"kind": kind, "binary": binary, "model": "openrouter/free", "endpoint": endpoint, "credential_file": str(token)}
        harnesses["hr_" + kind] = {"owner_id": "pr_alice", "argv": ["python3"], "cwd": "{session_dir}/work", "env": {"HOME": "{session_dir}/home", "PATH": args.path, "LANG": "en_US.UTF-8"}, "auth_modes": ["api_key"], "repository": repository, "native": native}
    subagent_kinds = args.subagent_kind or (["claude"] if "claude" in binaries else [])
    for kind in subagent_kinds:
        harnesses["hr_" + kind + "_subagents"] = json.loads(json.dumps(harnesses["hr_" + kind]))
        harnesses["hr_" + kind + "_subagents"]["native"]["native_subagents"] = True
    hosts = {name: {"owner_id": "pr_alice", "command": [sys.executable], "root": str(root / name)} for name in ["hs_a", "hs_b"]}
    (root / "sessions.json").write_text(json.dumps({"harnesses": harnesses, "hosts": hosts}))
    (root / "identity.json").write_text(json.dumps({"owner_id": "pr_alice", "bootstrap_sha256": hashlib.sha256(owner.encode()).hexdigest(), "subjects": []}))
    start([str(args.bin_dir / "lampd"), "--socket", str(api), "--state-dir", str(root / "state"), "--identity-file", str(root / "identity.json"), "--sessions-config", str(root / "sessions.json"), "--worker-socket", str(root / "unused.sock"), "--worker-id", "unused-t25", "--worker-boot-id", "unused-t25", "--worker-uid", str(os.getuid()), "--worker-pid", str(os.getpid()), "--grant-id", "unused-t25"], "lampd.log")
    try:
        wait(lambda: api.exists(), 20)
        catalogue = call("GET", "/v1/harnesses?limit=100")["items"]
        record("harness-catalogue", count=len(catalogue), harnesses=[{"id": h["id"], "catalogue_id": h.get("plugin_id"), "state": h["state"], "auth_modes": h.get("auth_modes"), "binary_digest": h.get("binary_digest", ""), "continuation": h["labels"].get("lamp.continuation", "")} for h in catalogue])
        for kind in [] if args.skip_teleport else binaries:
            created = call("POST", "/v1/sessions", {"sandbox_id": "", "harness_id": "hr_" + kind, "auth_mode": "api_key", "location": {"kind": "host", "host_id": "hs_a"}})["result"]["session_id"]
            wait(lambda: session(created)["state"] == "active")
            codeword = "LAMP" + secrets.token_hex(3).upper()
            first, out1 = say(created, "Remember this codeword for later: " + codeword + ". Do not use any tools. Reply with only the word READY.")
            before = session(created)
            call("POST", "/v1/sessions/" + created + ":teleport", {"location": {"kind": "host", "host_id": "hs_b"}}, revision=before["revision"])
            moved = wait(lambda: (s := session(created))["state"] == "active" and s["location"].get("host_id") == "hs_b" and s, 120)
            # The source location's private root no longer holds this session.
            source_dirs = [p.name for p in (root / "hs_a").iterdir()] if (root / "hs_a").exists() else []
            second, out2 = say(created, "What was the codeword I gave you earlier? Do not use any tools. Reply with only the codeword.")
            record("teleported-continuation", kind=kind, session_id=created, epoch_before=before["location_epoch"], epoch_after=moved["location_epoch"], location_before=before["location"], location_after=moved["location"], turn1=out1.strip()[-300:], turn1_success=first.get("success"), turn2=out2.strip()[-300:], turn2_success=second.get("success"), continuation_before=first.get("continuation_ref"), continuation_after=second.get("continuation_ref"), native_fork_of=second.get("forked_from"), codeword_recalled=codeword in out2, source_root_entries=source_dirs)
            current = session(created)
            call("POST", "/v1/sessions/" + created + ":end", {}, revision=current["revision"])
        for kind in subagent_kinds:
            parent = call("POST", "/v1/sessions", {"sandbox_id": "", "harness_id": "hr_" + kind + "_subagents", "auth_mode": "api_key", "location": {"kind": "host", "host_id": "hs_a"}})["result"]["session_id"]
            wait(lambda: session(parent)["state"] == "active")
            tool = "Task tool" if kind == "claude" else "built-in sub-agent (task) tool"
            event, out = say(parent, "Use the " + tool + " exactly once to launch a general-purpose sub-agent with the prompt 'Reply with exactly LAMP_SUBAGENT_DONE and nothing else.' Then reply with the sub-agent's answer.", 400)
            children = [s for s in call("GET", "/v1/sessions?limit=100")["items"] if s.get("parent_session_id") == parent]
            record("native-subagent-children", kind=kind, harness_id="hr_" + kind + "_subagents", parent=parent, parent_native_session=event.get("continuation_ref"), parent_output=out.strip()[-400:], success=event.get("success"), children=[{"id": c["id"], "state": c["state"], "status": c.get("status"), "continuation_ref": c.get("continuation_ref"), "labels": c.get("labels"), "output": stdout(c["id"]).strip()[-300:]} for c in children])
            current = session(parent)
            call("POST", "/v1/sessions/" + parent + ":end", {}, revision=current["revision"])
        record("sessions-listed", sessions=[{"id": s["id"], "harness_id": s["harness_id"], "parent_session_id": s.get("parent_session_id"), "state": s["state"], "location": s["location"].get("host_id")} for s in call("GET", "/v1/sessions?limit=100")["items"]])
    finally:
        stop.set()
        for process in reversed(processes):
            process.terminate()
            try:
                process.wait(timeout=15)
            except subprocess.TimeoutExpired:
                process.kill()


if __name__ == "__main__":
    main()
