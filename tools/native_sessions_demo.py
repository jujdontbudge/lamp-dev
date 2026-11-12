import argparse
import json
import os
from pathlib import Path
import select
import shutil
import signal
import socket
import subprocess
import sys
import time

from broker_demo import request


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=Path, required=True)
    parser.add_argument("--broker", type=Path, required=True)
    parser.add_argument("--public-repository", type=Path, required=True)
    parser.add_argument("--serve", action="store_true")
    parser.add_argument("--kind", choices=["claude", "codex"])
    args = parser.parse_args()
    os.umask(0o077)
    root = args.root.resolve()
    root.mkdir(mode=0o700)
    reader = "import json,pathlib; s=json.loads(pathlib.Path.home().joinpath('.pi/agent/auth.json').read_text())['openrouter']; print(next(s[k] for k in ('key','apiKey','api_key') if isinstance(s.get(k),str)))"
    shared = {"model": "string", "stream": "boolean?", "tools": "array?", "temperature": "number?", "top_p": "number?", "metadata": "object?"}
    messages = dict(shared, messages="array", max_tokens="number?", system="array?", tool_choice="object?", thinking="object?", output_config="object?", context_management="object?", stop_sequences="array?", top_k="number?")
    responses = dict(shared, input="array", instructions="string?", store="boolean?", parallel_tool_calls="boolean?", include="array?", reasoning="object?", text="object?", service_tier="string?", prompt_cache_key="string?", max_output_tokens="number?", tool_choice="string?", truncation="string?", previous_response_id="string?", stream_options="object?", safety_identifier="string?", client_metadata="object?")
    connections = {}
    for name, operation, endpoint, fields in [("claude", "messages", "https://openrouter.ai/api/v1/messages", messages), ("codex", "responses", "https://openrouter.ai/api/v1/responses", responses)]:
        connections[name] = {"kind": "model", "access": "full", "endpoint": endpoint, "credential_command": [sys.executable, "-c", reader], "models": ["openrouter/free"], "free_only": True, "operations": {operation: {"write": True, "fields": fields}}}
    config = root / "broker.json"
    config.write_text(json.dumps({"connections": connections, "settings": {}}))
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
    log = (root / "broker.log").open("wb")
    broker = subprocess.Popen([str(args.broker), "--config", str(config), "--owner-socket", str(root / "owner.sock"), "--task-listen", "127.0.0.1:" + str(port), "--audit", str(root / "audit.jsonl")], stdin=subprocess.DEVNULL, stdout=log, stderr=log)
    children = []
    evidence = []
    try:
        deadline = time.monotonic() + 15
        while not (root / "owner.sock").exists():
            if broker.poll() is not None or time.monotonic() >= deadline:
                raise RuntimeError("native broker startup failed")
            time.sleep(.05)
        status, grant = request("http://localhost/owner/grants", {"sandbox_id": "sb_t21_native_demo", "generation": 1, "peer_ip": "127.0.0.1", "ttl_seconds": 240, "connections": {"claude": ["messages"], "codex": ["responses"]}, "settings": []}, unix=str(root / "owner.sock"))
        if status != 200:
            raise RuntimeError("scoped native-model grant failed")
        token_file = root / "transport.token"
        token_file.write_text(grant["token"])
        (root / "runtime.json").write_text(json.dumps({"port": port, "pid": broker.pid, "token_file": str(token_file), "owner_socket": str(root / "owner.sock")}))
        for kind in ([args.kind] if args.kind else ["claude", "codex"]):
            directory = root / kind
            directory.mkdir(mode=0o700)
            home = directory / "home"
            home.mkdir()
            work = directory / "work"
            subprocess.run(["git", "-c", "core.hooksPath=/dev/null", "clone", "--quiet", "--", str(args.public_repository), str(work)], check=True)
            native = {"kind": kind, "binary": shutil.which(kind), "auth": "api_key", "model": "openrouter/free", "endpoint": "http://127.0.0.1:" + str(port) + "/v1/models/" + kind, "credential_file": str(token_file)}
            path = directory / "native.json"
            path.write_text(json.dumps(native))
            environment = {"HOME": str(home), "PATH": os.environ["PATH"], "TERM": "xterm-256color"}
            process = subprocess.Popen([sys.executable, "-u", str(Path(__file__).resolve().parents[1] / "internal/control/native_harness.py"), str(path)], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=environment, cwd=work)
            children.append(process)
            marker = "LAMP_" + kind.upper() + "_API_OK"
            process.stdin.write(json.dumps({"input_id": "demo.0", "text": "Reply with exactly " + marker + ". Do not use any tools."}).encode() + b"\n")
            process.stdin.flush()
            output = bytearray()
            deadline = time.monotonic() + 120
            success = False
            while time.monotonic() < deadline:
                ready, _, _ = select.select([process.stdout], [], [], min(1, deadline - time.monotonic()))
                if not ready:
                    if process.poll() is not None:
                        raise RuntimeError("official native wrapper exited")
                    continue
                line = process.stdout.readline()
                if not line:
                    raise RuntimeError("official native wrapper closed output")
                if line.startswith(b"\x1eLAMP "):
                    event = json.loads(line[6:])
                    if event["type"] == "input_completed":
                        success = event["success"]
                        evidence.append({"kind": kind, "authentication": "official API gateway route", "public_repository": "octocat/Hello-World", "official_binary": native["binary"], "model": native["model"], "free_only": True, "success": success, "observed": output.decode(errors="replace"), "continuation_ref": event.get("continuation_ref")})
                        break
                else:
                    output.extend(line)
            (root / "evidence.json").write_text(json.dumps(evidence, indent=2) + "\n")
            print(json.dumps(evidence[-1] if evidence else {"kind": kind, "success": False}), flush=True)
            if not success or marker.encode() not in output:
                raise RuntimeError("official native API turn did not produce its marker")
            process.stdin.close()
            process.wait(timeout=10)
        if args.serve:
            print(json.dumps({"ready": str(root / "runtime.json")}), flush=True)
            signal.pause()
    finally:
        for process in children:
            if process.poll() is None:
                process.terminate()
                process.wait(timeout=10)
        broker.terminate()
        broker.wait(timeout=10)
        log.close()


if __name__ == "__main__":
    main()
