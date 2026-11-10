#!/usr/bin/env python3
"""One disposable T17 journey, not a benchmark or qualification suite.

The model and MCP vendors are explicit authenticated TLS fixtures, not claims
of OpenRouter/third-party MCP compatibility. gh, when installed, is the real
signed-in CLI against the public cli/cli repository. No account writes occur.
"""
import argparse
import hashlib
import http.client
import json
import os
from pathlib import Path
import secrets
import shutil
import socket
import sqlite3
import ssl
import subprocess
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


def credential(path):
    with socket.socket(socket.AF_UNIX) as s:
        s.connect(path)
        data = s.recv(16384)
    sys.stdout.buffer.write(data)


def request(url, value, token=None, unix=None, ca=None, method="POST"):
    from urllib.parse import urlsplit
    u = urlsplit(url)
    if unix:
        c = http.client.HTTPConnection("localhost", timeout=50)
        c.sock = socket.socket(socket.AF_UNIX)
        c.sock.settimeout(50)
        c.sock.connect(str(unix))
    elif u.scheme == "https":
        c = http.client.HTTPSConnection(u.hostname, u.port, timeout=50,
                                       context=ssl.create_default_context(cafile=str(ca) if ca else None))
    else:
        c = http.client.HTTPConnection(u.hostname, u.port, timeout=50)
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = "Bearer " + token
    c.request(method, u.path, json.dumps(value) if value is not None else None, headers)
    r = c.getresponse()
    data = json.loads(r.read())
    c.close()
    return r.status, data


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--binary", required=True)
    p.add_argument("--root", required=True)
    p.add_argument("--task-host", default="127.0.0.1")
    p.add_argument("--task-port", type=int, default=17778)
    p.add_argument("--serve", action="store_true", help="keep fixtures/broker ready for one external guest journey")
    args = p.parse_args()
    root = Path(args.root).resolve()
    root.mkdir(mode=0o700, parents=True, exist_ok=False)
    private = root / "private"
    private.mkdir(mode=0o700)
    transcript = (root / "transcript.jsonl").open("x")
    key = secrets.token_urlsafe(40).encode()  # host-memory-only canary
    def record(step, value):
        raw = json.dumps({"step": step, "value": value}, sort_keys=True)
        if key in raw.encode():
            raise RuntimeError("refusing secret-bearing evidence")
        transcript.write(raw + "\n")
        transcript.flush()
    credential_socket = socket.socket(socket.AF_UNIX)
    credential_socket.bind(str(private / "credential.sock"))
    credential_socket.listen(8)
    def credentials():
        while True:
            try:
                c, _ = credential_socket.accept()
                with c:
                    c.sendall(key)
            except OSError:
                return
    threading.Thread(target=credentials, daemon=True).start()
    db = private / "service.sqlite"
    with sqlite3.connect(db) as conn:
        conn.execute("CREATE TABLE notes (body TEXT)")
        conn.execute("INSERT INTO notes VALUES ('initial')")
    class Upstream(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass
        def do_GET(self):
            self.do_POST()
        def do_POST(self):
            if self.headers.get("Authorization") != "Bearer " + key.decode():
                self.send_response(401)
                self.end_headers()
                return
            raw = self.rfile.read(int(self.headers.get("Content-Length", "0")))
            data = json.loads(raw) if raw else {}
            result = None
            if self.path == "/chat":
                # A useful runnable-code shape; explicitly a vendor fixture.
                result = {"id": "fixture", "choices": [{"message": {"role": "assistant", "content": "print(sum(range(1, 11)))"}}]}
            elif self.path == "/mcp":
                if data.get("method") == "initialize":
                    result = {"jsonrpc": "2.0", "id": data["id"], "result": {"protocolVersion": "2025-06-18", "capabilities": {"tools": {}}, "serverInfo": {"name": "authenticated-notes", "version": "1"}}}
                elif data.get("method") == "notifications/initialized":
                    self.send_response(202)
                    self.end_headers()
                    return
                elif data.get("method") == "tools/call" and data["params"]["name"] == "read_notes":
                    with sqlite3.connect(db) as conn:
                        rows = [r[0] for r in conn.execute("SELECT body FROM notes")]
                    result = {"jsonrpc": "2.0", "id": data["id"], "result": {"content": [{"type": "text", "text": json.dumps(rows)}]}}
            elif self.path == "/notes" and self.command == "GET":
                with sqlite3.connect(db) as conn:
                    result = {"notes": [r[0] for r in conn.execute("SELECT body FROM notes")]}
            elif self.path == "/notes" and self.command == "POST":
                with sqlite3.connect(db) as conn:
                    conn.execute("INSERT INTO notes VALUES (?)", (data["body"],))
                    result = {"count": conn.execute("SELECT COUNT(*) FROM notes").fetchone()[0]}
            elif self.path == "/reflect":
                # Deliberately split the canary over transport writes.
                payload = json.dumps({"reflected": key.decode()}).encode()
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                for chunk in (payload[:19], payload[19:]):
                    self.wfile.write(chunk)
                    self.wfile.flush()
                return
            if result is None:
                self.send_response(404)
                self.end_headers()
                return
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps(result).encode())
    cert, tlskey = private / "cert.pem", private / "tls-key.pem"
    subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
                    "-keyout", str(tlskey), "-out", str(cert), "-subj", "/CN=lamp-disposable-broker",
                    "-addext", "subjectAltName=IP:127.0.0.1,IP:" + args.task_host], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    server = ThreadingHTTPServer(("127.0.0.1", 0), Upstream)
    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    ctx.load_cert_chain(cert, tlskey)
    server.socket = ctx.wrap_socket(server.socket, server_side=True)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    upstream = "https://127.0.0.1:" + str(server.server_port)
    reader = [sys.executable, str(Path(__file__).resolve()), "credential", str(private / "credential.sock")]
    settings = private / "settings"
    settings.mkdir()
    (settings / "AGENTS.md").write_text("Original instructions\n")
    (settings / "harness.json").write_text('{"theme":"dark"}\n')
    signing_key = private / "git-signing-key"
    subprocess.run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", str(signing_key)], check=True)
    connections = {
        "model": {"kind": "model", "access": "full", "endpoint": upstream + "/chat", "credential_command": reader, "models": ["fixture-code"], "operations": {"chat": {"write": True, "fields": {"model": "string", "messages": "array", "max_tokens": "number"}}}},
        "mcp": {"kind": "mcp", "access": "read_only", "endpoint": upstream + "/mcp", "credential_command": reader, "operations": {"read_notes": {"write": False}}},
        "notes": {"kind": "service", "access": "off", "endpoint": upstream, "credential_command": reader, "operations": {"read": {"method": "GET", "path": "/notes", "write": False, "semantic_read": True}, "append": {"method": "POST", "path": "/notes", "write": True, "fields": {"body": "string"}}, "reflect": {"method": "GET", "path": "/reflect", "write": False, "semantic_read": True}}},
        "git": {"kind": "git_sign", "access": "ask_first", "binary": shutil.which("ssh-keygen"), "key_file": str(signing_key), "resource": "fixture/disposable", "operations": {"sign_commit": {"write": True, "fields": {"commit": "string"}}}},
    }
    if shutil.which("gh"):
        connections["github"] = {"kind": "cli", "access": "read_only", "adapter": "gh", "binary": shutil.which("gh"), "resource": "cli/cli", "operations": {"repo_view": {"write": False}}}
    config = {"upstream_ca_files": [str(cert)], "connections": connections, "settings": {"owner": {"root": str(settings), "paths": ["AGENTS.md", "harness.json"]}}}
    config_path = private / "config.json"
    config_path.write_text(json.dumps(config))
    config_path.chmod(0o600)
    owner = private / "owner.sock"
    log = (root / "broker.log").open("x")
    broker = subprocess.Popen([args.binary, "--config", str(config_path), "--owner-socket", str(owner), "--task-listen", args.task_host + ":" + str(args.task_port), "--tls-cert", str(cert), "--tls-key", str(tlskey), "--audit", str(root / "audit.jsonl")], stdout=log, stderr=log)
    def admin(path, value=None, method="POST"):
        status, reply = request("http://localhost" + path, value, unix=owner, method=method)
        if status != 200:
            raise RuntimeError("owner operation refused: " + path)
        return reply
    def issue(sandbox="sb_t17_demo"):
        return admin("/owner/grants", {"sandbox_id": sandbox, "generation": 1, "peer_ip": args.task_host, "ttl_seconds": 300, "connections": {id: list(c["operations"]) for id, c in connections.items()}, "settings": ["owner"]})
    try:
        deadline = time.monotonic() + 10
        while not owner.exists():
            if broker.poll() is not None or time.monotonic() > deadline:
                raise RuntimeError("broker did not start")
            time.sleep(.02)
        record("artifacts", {"broker_sha256": hashlib.sha256(Path(args.binary).read_bytes()).hexdigest(), "machine": os.uname().sysname, "vendor_boundary": "authenticated TLS fixtures; GitHub is real when available"})
        if args.serve:
            manifest = {"url": "https://" + args.task_host + ":" + str(args.task_port), "ca": str(cert), "owner_socket": str(owner), "settings_root": str(settings)}
            (private / "manifest.json").write_text(json.dumps(manifest))
            print("Disposable broker ready", flush=True)
            sys.stdin.read()
            return
        grant = issue()
        token = grant["token"]
        base = "https://" + args.task_host + ":" + str(args.task_port)
        def task(path, value):
            return request(base + path, value, token, ca=cert)
        def invoke(connection, operation, arguments=None, approval=None):
            body = {"connection_id": connection, "operation": operation, "arguments": arguments or {}}
            if approval:
                body["approval_id"] = approval
            return task("/v1/invoke", body)
        chat = {"model": "fixture-code", "messages": [{"role": "user", "content": "Write Python that sums 1 through 10"}], "max_tokens": 128}
        status, reply = task("/v1/models/model/chat/completions", chat)
        assert status == 200, reply
        code = reply["choices"][0]["message"]["content"]
        useful = subprocess.check_output([sys.executable, "-c", code], text=True).strip()
        assert useful == "55"
        record("model fixture useful code", {"status": status, "result": useful})
        status, reply = invoke("mcp", "read_notes")
        assert status == 200, reply
        record("authenticated MCP gateway", reply)
        assert invoke("mcp", "export_token")[0] == 403
        status, copy = task("/v1/settings/owner/copy", {})
        assert status == 200
        status, proposed = task("/v1/settings/owner/propose", {"base_revision": copy["base_revision"], "files": {"AGENTS.md": "Approved instructions\n", "harness.json": '{"theme":"light"}\n'}})
        assert status == 200
        assert (settings / "AGENTS.md").read_text() == "Original instructions\n"
        admin("/owner/settings/" + proposed["settings_change_id"], {"base_revision": copy["base_revision"], "selected_paths": ["AGENTS.md"]})
        assert (settings / "AGENTS.md").read_text() == "Approved instructions\n"
        assert (settings / "harness.json").read_text() == '{"theme":"dark"}\n'
        record("selected settings return", {"selected_applied": True, "unselected_unchanged": True, "no_write_before_approval": True})
        _, copy = task("/v1/settings/owner/copy", {})
        _, proposed = task("/v1/settings/owner/propose", {"base_revision": copy["base_revision"], "files": {"AGENTS.md": "Stale edit\n"}})
        (settings / "harness.json").write_text('{"theme":"concurrently changed"}\n')
        status, reply = request("http://localhost/owner/settings/" + proposed["settings_change_id"], {"base_revision": copy["base_revision"], "selected_paths": ["AGENTS.md"]}, unix=owner)
        assert status == 409
        record("stale settings refused", reply)
        (settings / "AGENTS.md").unlink()
        (settings / "AGENTS.md").symlink_to(cert)
        assert task("/v1/settings/owner/copy", {})[0] == 403
        (settings / "AGENTS.md").unlink()
        (settings / "AGENTS.md").write_text("Approved instructions\n")
        assert invoke("notes", "read")[0] == 403
        admin("/owner/access/notes", {"access": "read_only"})
        assert invoke("notes", "read")[0] == 200
        assert invoke("notes", "append", {"body": "denied"})[0] == 403
        admin("/owner/access/notes", {"access": "ask_first"})
        status, pending = invoke("notes", "append", {"body": "approved"})
        assert status == 403 and pending["error"]["code"] == "APPROVAL_REQUIRED"
        with sqlite3.connect(db) as conn:
            assert conn.execute("SELECT COUNT(*) FROM notes").fetchone()[0] == 1
        admin("/owner/approvals/" + pending["proposal_id"], {"digest": pending["digest"]})
        assert invoke("notes", "append", {"body": "changed"}, pending["proposal_id"])[0] == 403
        assert invoke("notes", "append", {"body": "approved"}, pending["proposal_id"])[0] == 200
        assert invoke("notes", "append", {"body": "approved"}, pending["proposal_id"])[0] == 403
        admin("/owner/access/notes", {"access": "full"})
        assert invoke("notes", "append", {"body": "full"})[0] == 200
        with sqlite3.connect(db) as conn:
            bodies = [r[0] for r in conn.execute("SELECT body FROM notes")]
        assert bodies == ["initial", "approved", "full"]
        record("four service modes", {"database_rows": bodies, "off_denied": True, "read_only_write_denied": True, "ask_first_changed_and_replay_denied": True})
        status, reply = invoke("notes", "reflect")
        assert status == 502 and reply["error"]["code"] == "OUTPUT_BLOCKED"
        record("split secret response blocked", reply)
        commit = "tree " + "0" * 40 + "\nauthor Demo <demo@example.invalid> 1 +0000\ncommitter Demo <demo@example.invalid> 1 +0000\n\nDisposable commit\n"
        status, pending = invoke("git", "sign_commit", {"commit": commit})
        assert status == 403
        admin("/owner/approvals/" + pending["proposal_id"], {"digest": pending["digest"]})
        status, signed = invoke("git", "sign_commit", {"commit": commit}, pending["proposal_id"])
        assert status == 200, signed
        sig = private / "signature"
        sig.write_text(signed["data"]["signature"])
        allowed = private / "allowed-signers"
        allowed.write_text("demo " + signing_key.with_suffix(".pub").read_text())
        verified = subprocess.run(["ssh-keygen", "-Y", "verify", "-f", str(allowed), "-I", "demo", "-n", "git", "-s", str(sig)], input=commit.encode(), capture_output=True)
        assert verified.returncode == 0
        record("host-only approved git signing", {"verified": True, "private_key_not_copied": True})
        if "github" in connections:
            status, reply = invoke("github", "repo_view")
            record("real signed-in gh", {"status": status, "result": reply})
            assert status == 200 and reply["data"]["full_name"] == "cli/cli", reply
            assert invoke("github", "auth_token")[0] == 403
        else:
            record("real gh", {"state": "blocked", "reason": "binary unavailable on this machine"})
        admin("/owner/revoke", {"grant_id": grant["grant_id"]})
        assert invoke("notes", "read")[0] == 403
        exposed = [root / "transcript.jsonl", root / "broker.log", root / "audit.jsonl"]
        assert all(key not in p.read_bytes() for p in exposed)
        record("canary scan", {"provider_key_occurrences_in_output_and_logs": 0, "guest_and_capture": "not demonstrated by this host-only journey"})
        record("remaining account evidence", {"OpenRouter": "credential access blocked", "Stripe": "test-account sign-in not available", "AWS": "scoped test-resource sign-in not available", "external_MCP": "not demonstrated"})
    finally:
        broker.terminate()
        broker.wait(timeout=50)
        server.shutdown()
        credential_socket.close()
        log.close()
        transcript.close()
        # Host-only fixture TLS/signing material and task authority are disposable.
        shutil.rmtree(private)
    print(str(root / "transcript.jsonl"))


if __name__ == "__main__":
    if len(sys.argv) == 3 and sys.argv[1] == "credential":
        credential(sys.argv[2])
    else:
        main()
