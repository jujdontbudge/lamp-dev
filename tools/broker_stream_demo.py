"""One long streamed chat reply through a Lamp broker, with budget accounting.

A small accountability stub records what the broker reserves and settles.
The client measures when bytes arrive and whether the stream ended whole
([DONE] with a finish_reason). Run it against two broker binaries to compare.
Model traffic uses OpenRouter's zero-price route.
"""

import argparse
import http.client
import json
import os
from pathlib import Path
import socket
import subprocess
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from broker_demo import request
from harness_catalogue_demo import connection_fields


class UnixServer(ThreadingHTTPServer):
    address_family = socket.AF_UNIX

    def server_bind(self):
        self.socket.bind(self.server_address)

    def get_request(self):
        connection, _ = self.socket.accept()
        return connection, ("unix", 0)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--broker", type=Path, required=True)
    parser.add_argument("--root", type=Path, required=True)
    parser.add_argument("--limit-key", default="max_completion_tokens", choices=["max_tokens", "max_completion_tokens"])
    args = parser.parse_args()
    os.umask(0o077)
    root = args.root.resolve()
    root.mkdir(mode=0o700)
    ledger = []

    class Accounting(BaseHTTPRequestHandler):
        def do_POST(self):
            body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            operation = self.path.rsplit("/", 1)[-1]
            if operation != "check":
                ledger.append({"operation": operation, **{k: body.get(k) for k in ("tokens", "actual_tokens", "duration_ms", "type")}})
            reply = json.dumps({"reservation_id": "rs_demo"} if operation == "reserve" else {}).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(reply)))
            self.end_headers()
            self.wfile.write(reply)

        def log_message(self, *_):
            pass

    accounting = UnixServer(str(root / "accounting.sock"), Accounting)
    threading.Thread(target=accounting.serve_forever, daemon=True).start()
    reader = "import json,pathlib; s=json.loads(pathlib.Path.home().joinpath('.pi/agent/auth.json').read_text())['openrouter']; print(next(s[k] for k in ('key','apiKey','api_key') if isinstance(s.get(k),str)))"
    config = {"accountability_socket": str(root / "accounting.sock"), "connections": {"chat": {"kind": "model", "access": "full", "endpoint": "https://openrouter.ai/api/v1/chat/completions", "credential_command": [sys.executable, "-c", reader], "models": ["openrouter/free"], "free_only": True, "operations": {"chat_completions": {"write": True, "fields": connection_fields()["chat_completions"]}}}}, "settings": {}}
    (root / "broker.json").write_text(json.dumps(config))
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
    log = (root / "broker.log").open("wb")
    broker = subprocess.Popen([str(args.broker), "--config", str(root / "broker.json"), "--owner-socket", str(root / "owner.sock"), "--task-listen", "127.0.0.1:" + str(port), "--audit", str(root / "audit.jsonl")], stdout=log, stderr=log)
    try:
        while not (root / "owner.sock").exists():
            time.sleep(.05)
        status, grant = request("http://localhost/owner/grants", {"sandbox_id": "sb_t25_stream", "generation": 1, "peer_ip": "127.0.0.1", "ttl_seconds": 300, "connections": {"chat": ["chat_completions"]}, "settings": []}, unix=str(root / "owner.sock"))
        assert status == 200
        body = {"model": "openrouter/free", "stream": True, args.limit_key: 4096, "messages": [{"role": "user", "content": "Write a detailed, numbered, 40-step tutorial on building a small HTTP server in Python, with a code sample for every step. Be thorough and long."}]}
        connection = http.client.HTTPConnection("127.0.0.1", port, timeout=600)
        started = time.monotonic()
        connection.request("POST", "/v1/models/chat/native/chat/completions", json.dumps(body), {"Content-Type": "application/json", "Authorization": "Bearer " + grant["token"]})
        response = connection.getresponse()
        first, size, chunks, done, finish, text = None, 0, 0, False, None, []
        while True:
            try:
                line = response.readline()
            except (http.client.IncompleteRead, ConnectionError, OSError):
                break
            if not line:
                break
            if first is None:
                first = time.monotonic() - started
            size += len(line)
            line = line.strip()
            if line == b"data: [DONE]":
                done = True
            elif line.startswith(b"data: "):
                try:
                    event = json.loads(line[6:])
                except ValueError:
                    continue
                chunks += 1
                for choice in event.get("choices", []):
                    text.append((choice.get("delta", {}).get("content") or "") + (choice.get("delta", {}).get("reasoning") or ""))
                    finish = choice.get("finish_reason") or finish
        total = time.monotonic() - started
        time.sleep(1)
        result = {"broker": str(args.broker), "limit_key": args.limit_key, "http_status": response.status, "first_byte_s": round(first, 2) if first is not None else None, "total_s": round(total, 2), "bytes": size, "chunks": chunks, "reply_chars": len("".join(text)), "ended_with_done": done, "finish_reason": finish, "ledger": ledger, "broker_log": (root / "broker.log").read_text().strip().splitlines()[-2:]}
        (root / "result.json").write_text(json.dumps(result, indent=2) + "\n")
        print(json.dumps(result))
    finally:
        broker.terminate()
        broker.wait(timeout=10)
        accounting.shutdown()


if __name__ == "__main__":
    main()
