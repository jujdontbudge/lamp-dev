"""Two real turns per official harness through Lamp's credential broker.

Turn one hands the harness a fresh codeword; turn two asks for it back through
the harness's own resume mechanism. The codeword appears only in turn one's
prompt, so a correct answer shows a continued native session, not a new prompt
with a transcript.
"""

import argparse
import json
import os
from pathlib import Path
import secrets
import select
import signal
import socket
import subprocess
import sys
import time

from broker_demo import request

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "internal/control"))
from session_artifact import artifact_path, artifact_restore, artifact_snapshot  # noqa: E402

WRAPPER = Path(__file__).resolve().parents[1] / "internal/control/native_harness.py"
# kind -> broker connection whose native wire protocol it speaks.
ROUTES = {"claude": "anthropic", "codex": "responses", "opencode": "chat", "pi": "chat", "goose": "chat", "copilot": "chat", "droid": "chat"}


def connection_fields():
    """Native wire fields each broker operation accepts (names, never values)."""
    shared = {"model": "string", "stream": "boolean?", "tools": "array?", "temperature": "number?", "top_p": "number?", "metadata": "object?"}
    messages = dict(shared, messages="array", max_tokens="number?", system="array?", tool_choice="object?", thinking="object?", output_config="object?", context_management="object?", stop_sequences="array?", top_k="number?")
    responses = dict(shared, input="array", instructions="string?", store="boolean?", parallel_tool_calls="boolean?", include="array?", reasoning="object?", text="object?", service_tier="string?", prompt_cache_key="string?", max_output_tokens="number?", tool_choice="string?", truncation="string?", previous_response_id="string?", stream_options="object?", safety_identifier="string?", client_metadata="object?")
    chat = dict(shared, messages="array", max_tokens="number?", max_completion_tokens="number?", stream_options="object?", tool_choice="string_or_object?", parallel_tool_calls="boolean?", reasoning_effort="string?", response_format="object?", stop="array?", seed="number?", n="number?", user="string?", frequency_penalty="number?", presence_penalty="number?", reasoning="object?", store="boolean?", prompt_cache_key="string?", usage="object?", provider="object?")
    return {"messages": messages, "responses": responses, "chat_completions": chat}


def turn(process, identity, text, timeout):
    process.stdin.write(json.dumps({"input_id": identity, "text": text}).encode() + b"\n")
    process.stdin.flush()
    output = bytearray()
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        ready, _, _ = select.select([process.stdout], [], [], 1)
        if not ready:
            if process.poll() is not None:
                break
            continue
        line = process.stdout.readline()
        if not line:
            break
        if line.startswith(b"\x1eLAMP "):
            event = json.loads(line[6:])
            if event["type"] == "input_completed":
                return event, output.decode(errors="replace")
        else:
            output.extend(line)
    return {"success": False, "continuation_ref": ""}, output.decode(errors="replace")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=Path, required=True)
    parser.add_argument("--broker", type=Path, required=True)
    parser.add_argument("--public-repository", type=Path, required=True)
    parser.add_argument("--binary", action="append", default=[], help="kind=/absolute/official/binary")
    parser.add_argument("--auth", action="append", default=[], help="kind=api_key|subscription:/credential/file (default: broker API route)")
    parser.add_argument("--move", action="store_true", help="move the native session to a new location between turns")
    parser.add_argument("--model", action="append", default=[], help="kind=model for non-broker routes")
    args = parser.parse_args()
    os.umask(0o077)
    root = args.root.resolve()
    root.mkdir(mode=0o700, exist_ok=True)
    binaries = dict(item.split("=", 1) for item in args.binary)
    auths = dict(item.split("=", 1) for item in args.auth)
    models = dict(item.split("=", 1) for item in args.model)
    reader = "import json,pathlib; s=json.loads(pathlib.Path.home().joinpath('.pi/agent/auth.json').read_text())['openrouter']; print(next(s[k] for k in ('key','apiKey','api_key') if isinstance(s.get(k),str)))"
    fields = connection_fields()
    messages, responses, chat = fields["messages"], fields["responses"], fields["chat_completions"]
    connections = {}
    for name, operation, endpoint, fields in [("anthropic", "messages", "https://openrouter.ai/api/v1/messages", messages), ("responses", "responses", "https://openrouter.ai/api/v1/responses", responses), ("chat", "chat_completions", "https://openrouter.ai/api/v1/chat/completions", chat)]:
        connections[name] = {"kind": "model", "access": "full", "endpoint": endpoint, "credential_command": [sys.executable, "-c", reader], "models": ["openrouter/free"], "free_only": True, "operations": {operation: {"write": True, "fields": fields}}}
    config = root / "broker.json"
    config.write_text(json.dumps({"connections": connections, "settings": {}}))
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
    log = (root / "broker.log").open("wb")
    broker = subprocess.Popen([str(args.broker), "--config", str(config), "--owner-socket", str(root / "owner.sock"), "--task-listen", "127.0.0.1:" + str(port), "--audit", str(root / "audit.jsonl")], stdin=subprocess.DEVNULL, stdout=log, stderr=log)
    evidence = []
    try:
        deadline = time.monotonic() + 15
        while not (root / "owner.sock").exists():
            if broker.poll() is not None or time.monotonic() >= deadline:
                raise RuntimeError("broker startup failed")
            time.sleep(.05)
        token_file = root / "transport.token"
        for kind, binary in binaries.items():
            status, grant = request("http://localhost/owner/grants", {"sandbox_id": "sb_t25_catalogue_demo", "generation": 1, "peer_ip": "127.0.0.1", "ttl_seconds": 300, "connections": {"anthropic": ["messages"], "responses": ["responses"], "chat": ["chat_completions"]}, "settings": []}, unix=str(root / "owner.sock"))
            if status != 200:
                raise RuntimeError("scoped grant failed")
            token_file.write_text(grant["token"])
            directory = root / kind
            directory.mkdir(mode=0o700)
            work = directory / "work"
            subprocess.run(["git", "-c", "core.hooksPath=/dev/null", "clone", "--quiet", "--", str(args.public_repository), str(work)], check=True)
            native = {"kind": kind, "binary": binary, "auth": "api_key", "model": "openrouter/free", "session_id": "se_t25_" + kind, "trace": True}
            if kind in auths:
                mode, _, credential = auths[kind].partition(":")
                native.update({"auth": mode, "credential_file": credential, "model": models.get(kind, "")})
                route = "official " + mode + " route held on this custody host"
            else:
                connection = ROUTES[kind]
                suffix = "/native" if connection == "chat" else ""
                native.update({"endpoint": "http://127.0.0.1:" + str(port) + "/v1/models/" + connection + suffix, "credential_file": str(token_file)})
                route = "official API route via Lamp broker (" + connection + ")"
            (directory / "native.json").write_text(json.dumps(native))
            def start(where):
                environment = {"HOME": str(where / "home"), "PATH": os.environ["PATH"], "TERM": "xterm-256color", "LANG": "en_US.UTF-8"}
                return subprocess.Popen([sys.executable, "-u", str(WRAPPER), str(directory / "native.json")], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, env=environment, cwd=where / "work")
            process = start(directory)
            codeword = "LAMP" + secrets.token_hex(3).upper()
            first, out1 = turn(process, "t25.1", "Remember this codeword for later: " + codeword + ". Do not use any tools. Reply with only the word READY.", 240)
            moved = None
            if args.move:
                # Package with Lamp's continuation artifact code and restore
                # into a different session directory and workspace path.
                process.stdin.close()
                process.wait(timeout=30)
                (directory / "launch.json").write_text(json.dumps({"cwd": str(work), "env": {"HOME": str(directory / "home")}, "native": native}))
                transfer = {"transfer_id": "tr_t25_" + kind, "include_native": True}
                archive = artifact_snapshot(directory, transfer, True)
                data = archive.read_bytes()
                target = root / (kind + "-moved")
                target.mkdir(mode=0o700)
                artifact_path(target, transfer).with_suffix(".upload").write_bytes(__import__("base64").b64encode(data))
                import hashlib
                artifact_restore(target, dict(transfer, artifact_sha256=hashlib.sha256(data).hexdigest(), cwd="{session_dir}/work", env={"HOME": "{session_dir}/home"}))
                # The original workspace and harness home are deleted, so the
                # second turn can only use what the artifact carried.
                import shutil
                shutil.rmtree(work)
                shutil.rmtree(directory / "home")
                moved = {"from": str(work), "to": str(target / "work"), "artifact_bytes": len(data), "original_deleted": True}
                process = start(target)
            second, out2 = turn(process, "t25.2", "What was the codeword I gave you earlier? Do not use any tools. Reply with only the codeword.", 240)
            process.stdin.close()
            try:
                process.wait(timeout=20)
            except subprocess.TimeoutExpired:
                process.kill()
            record = {"kind": kind, "official_binary": binary, "version": subprocess.run([binary, "--version"], capture_output=True, text=True, timeout=30).stdout.strip().splitlines()[:1], "route": route, "model": native["model"], "public_repository": "octocat/Hello-World", "turn1": {"success": first["success"], "observed": out1[-2000:], "continuation_ref": first.get("continuation_ref")}, "turn2": {"success": second["success"], "observed": out2[-2000:], "continuation_ref": second.get("continuation_ref")}, "codeword_recalled": codeword in out2, "moved": moved, "same_native_session": bool(first.get("continuation_ref")) and first.get("continuation_ref") == second.get("continuation_ref")}
            record["continued"] = record["turn1"]["success"] and record["turn2"]["success"] and record["codeword_recalled"] and record["same_native_session"]
            evidence.append(record)
            (root / "evidence.json").write_text(json.dumps(evidence, indent=2) + "\n")
            print(json.dumps({k: record[k] for k in ("kind", "route", "continued", "codeword_recalled", "same_native_session")} | {"ref": record["turn2"]["continuation_ref"]}), flush=True)
    finally:
        broker.terminate()
        broker.wait(timeout=10)
        log.close()


if __name__ == "__main__":
    signal.signal(signal.SIGTERM, lambda *_: sys.exit(1))
    main()
