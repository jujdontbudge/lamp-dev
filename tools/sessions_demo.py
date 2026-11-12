import argparse
import base64
import hashlib
import http.client
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import sys
import time
from datetime import datetime, timezone


class LocalHTTP(http.client.HTTPConnection):
    def __init__(self, path):
        super().__init__("localhost", timeout=30)
        self.path = str(path)

    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX)
        self.sock.connect(self.path)


def main():
    parser = argparse.ArgumentParser(description="Disposable live local sessions and stock SSH demonstration; no VM or model-provider claims.")
    parser.add_argument("--root", required=True)
    parser.add_argument("--bin-dir", required=True)
    args = parser.parse_args()
    os.umask(0o077)
    root = Path(args.root).resolve()
    root.mkdir(mode=0o700)
    binary = Path(args.bin_dir).resolve()
    transcript = []
    processes = []
    session_ids = []
    daemon = None
    client_socket = root / "api.sock"
    owner_token = secrets.token_urlsafe(32)
    bea_token = secrets.token_urlsafe(32)

    def record(step, **value):
        transcript.append(dict(step=step, **value))
        (root / "evidence.json").write_text(json.dumps(transcript, indent=2) + "\n")
        print(json.dumps(transcript[-1]), flush=True)

    def write(name, value):
        path = root / name
        path.write_text(json.dumps(value) if not isinstance(value, str) else value)
        path.chmod(0o600)
        return path

    def start(argv, log):
        stream = (root / log).open("ab")
        process = subprocess.Popen(argv, stdin=subprocess.DEVNULL, stdout=stream, stderr=stream)
        stream.close()
        processes.append(process)
        return process

    def call(method, path, value=None, token=owner_token, revision=None, key=None, expected=200):
        connection = LocalHTTP(client_socket)
        headers = {"Authorization": "Bearer " + token}
        if method != "GET":
            headers.update({"Content-Type": "application/json", "Idempotency-Key": key or secrets.token_hex(16)})
        if revision is not None:
            headers["If-Match"] = str(revision)
        connection.request(method, path, json.dumps(value) if value is not None else None, headers)
        response = connection.getresponse()
        reply = json.loads(response.read())
        connection.close()
        if response.status != expected:
            raise RuntimeError("unexpected API status for " + path + ": " + str(response.status) + " " + str(reply.get("error", {}).get("code")))
        return reply

    def resource(identity):
        return call("GET", "/v1/sessions/" + identity)["data"]

    def wait_for(condition):
        deadline = time.monotonic() + 20
        while time.monotonic() < deadline:
            value = condition()
            if value:
                return value
            time.sleep(.05)
        raise RuntimeError("live session observation did not arrive")

    def daemon_start():
        return start([str(binary / "lampd"), "--socket", str(client_socket), "--state-dir", str(root / "state"), "--identity-file", str(root / "identity.json"), "--sessions-config", str(root / "sessions.json"), "--worker-socket", str(root / "unused.sock"), "--worker-id", "unused-native-demo", "--worker-boot-id", "unused-native-demo", "--worker-uid", str(os.getuid()), "--worker-pid", str(os.getpid()), "--grant-id", "unused-native-demo"], "daemon.log")

    def text(identity, token=owner_token):
        reply = call("GET", "/v1/sessions/" + identity + "/output", token=token)
        return b"".join(base64.b64decode(frame["data_base64"]) for frame in reply["items"]).decode(errors="replace")

    def mutation(identity, suffix, value, token=owner_token):
        return call("POST", "/v1/sessions/" + identity + suffix, value, token=token, revision=resource(identity)["revision"])

    try:
        source = Path(__file__).resolve().parents[1]
        fingerprint = hashlib.sha256()
        for name in ["internal/control/sessions.go", "internal/control/session_runner.py", "internal/control/authority.go", "internal/control/core.go", "internal/control/store.go", "internal/access/console.go", "tools/sessions_demo.py"]:
            fingerprint.update(name.encode() + b"\0" + (source / name).read_bytes())
        record("scope", observed_at=datetime.now(timezone.utc).isoformat(), source_sha256=fingerprint.hexdigest(), description="Live native PTY sessions through the public Lamp API and stock SSH; partial T21, not a complete ticket demonstration")
        subprocess.run(["git", "clone", "--quiet", "--depth", "1", "https://github.com/octocat/Hello-World.git", str(root / "work")], check=True, stdin=subprocess.DEVNULL)
        (root / "home").mkdir(mode=0o700)
        write("identity.json", {"owner_id": "pr_alice", "bootstrap_sha256": hashlib.sha256(owner_token.encode()).hexdigest(), "subjects": [{"issuer": "sessions-demo", "subject": "bea", "sha256": hashlib.sha256(bea_token.encode()).hexdigest()}]})
        write("sessions.json", {"harnesses": {"hr_shell": {"owner_id": "pr_alice", "argv": ["/bin/sh", "-i"], "cwd": str(root / "work"), "env": {"HOME": str(root / "home"), "PATH": os.defpath}, "auth_modes": ["none"]}}, "hosts": {"hs_local": {"owner_id": "pr_alice", "command": [sys.executable], "root": str(root / "r")}}})
        daemon = daemon_start()
        wait_for(lambda: client_socket.exists())
        call("PATCH", "/v1/fleets/fl_local", {"mode": "team"}, revision=call("GET", "/v1/fleets/fl_local")["data"]["revision"])
        bea = call("POST", "/v1/principals", {"issuer": "sessions-demo", "subject": "bea", "display_name": "Disposable Bea"})["result"]["principal_id"]
        call("POST", "/v1/memberships", {"principal_id": bea, "role": "member"})
        created = call("POST", "/v1/sessions", {"sandbox_id": "", "harness_id": "hr_shell", "location": {"kind": "host", "host_id": "hs_local"}})
        identity = created["result"]["session_id"]
        session_ids.append(identity)
        wait_for(lambda: resource(identity)["state"] == "active")
        child = call("POST", "/v1/sessions", {"sandbox_id": "", "harness_id": "hr_shell", "parent_session_id": identity, "location": {"kind": "host", "host_id": "hs_local"}})["result"]["session_id"]
        session_ids.append(child)
        wait_for(lambda: resource(child)["state"] == "active")
        record("separate-live-sessions", sessions=session_ids, parent=resource(child)["parent_session_id"], project="octocat/Hello-World", location="native M5 PTYs, not sandboxes")
        hidden = call("GET", "/v1/sessions", token=bea_token)["items"]
        if hidden:
            raise RuntimeError("unshared sessions visible")
        call("GET", "/v1/sessions/" + identity, token=bea_token, expected=404)
        record("unshared-private", recipient_sessions=len(hidden))
        share = call("POST", "/v1/shares", {"resource_kind": "session", "resource_id": identity, "subject_id": bea, "actions": ["session.get", "session.watch"]})["result"]["share_id"]
        call("GET", "/v1/sessions/" + identity, token=bea_token)
        call("POST", "/v1/leases", {"type": "input", "resource_id": identity, "expires_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(time.time() + 240))}, token=bea_token, expected=403)
        record("explicit-watch-only", visible_sessions=len(call("GET", "/v1/sessions", token=bea_token)["items"]), input_denied=True)
        write("alice.token", owner_token)
        write("bea.token", bea_token)
        for name in ["host-ca", "user-ca"]:
            subprocess.run([str(binary / "lamp-access"), "init-ca", "--out", str(root / name)], check=True, stdout=subprocess.DEVNULL)
        subprocess.run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", str(root / "ssh-key")], check=True, stdin=subprocess.DEVNULL)
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            port = listener.getsockname()[1]
        write("access.json", {"state_dir": str(root / "access"), "api_socket": str(client_socket), "ssh_listen": "127.0.0.1:" + str(port), "host_ca_file": str(root / "host-ca"), "user_ca_file": str(root / "user-ca"), "hostnames": ["127.0.0.1"], "users": [{"name": name, "token_file": str(root / (name + ".token")), "authorized_keys": [(root / "ssh-key.pub").read_text().strip()]} for name in ["alice", "bea"]]})
        edge = start([str(binary / "lamp-access"), "serve", "--config", str(root / "access.json")], "access.log")
        known = write("known-hosts", "@cert-authority [127.0.0.1]:" + str(port) + " " + (root / "host-ca.pub").read_text())
        ssh = ["ssh", "-F", "/dev/null", "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile=" + str(known), "-i", str(root / "ssh-key"), "-p", str(port)]
        def ssh_ready():
            with socket.socket() as connection:
                return connection.connect_ex(("127.0.0.1", port)) == 0
        wait_for(ssh_ready)
        listed = json.loads(subprocess.check_output(ssh + ["alice@127.0.0.1", "sessions"], stderr=subprocess.PIPE))
        if len(listed["items"]) != 2:
            raise RuntimeError("SSH did not list both sessions")
        record("stock-ssh", fleet_endpoint="disposable loopback SSH with pinned CA", sessions=len(listed["items"]))
        subprocess.run(ssh + ["alice@127.0.0.1", "attach", child], input=b"printf 'ssh-attached-child\\n'\n", stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, check=True, timeout=20)
        wait_for(lambda: "ssh-attached-child" in text(child))
        record("stock-ssh-attach", child_input_acknowledged=True)
        watcher = subprocess.Popen(ssh + ["bea@127.0.0.1", "watch", identity], stdout=(root / "ssh-watch.txt").open("wb"), stderr=subprocess.PIPE)
        processes.append(watcher)
        lease = call("POST", "/v1/leases", {"type": "input", "resource_id": identity, "expires_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(time.time() + 240))})["result"]["lease_id"]
        mutation(identity, ":attach", {"lease_id": lease})
        marker = "live-session-" + secrets.token_hex(8)
        command = "stty -echo; printf '%s\\n' '" + marker + "'; cat README\n"
        revision = resource(identity)["revision"]
        key = secrets.token_hex(16)
        request = {"lease_id": lease, "location_epoch": resource(identity)["location_epoch"], "sequence": 0, "data_base64": base64.b64encode(command.encode()).decode()}
        original = call("POST", "/v1/sessions/" + identity + ":input", request, revision=revision, key=key)
        replay = call("POST", "/v1/sessions/" + identity + ":input", request, revision=revision, key=key)
        if not replay["replayed"] or replay["operation_id"] != original["operation_id"]:
            raise RuntimeError("input retry did not return its original durable receipt")
        wait_for(lambda: marker in text(identity, bea_token) and "Hello World!" in text(identity, bea_token))
        wait_for(lambda: marker in (root / "ssh-watch.txt").read_text(errors="replace"))
        record("live-input-and-watch", project_readme=True, ssh_watch_received_input_result=True, identical_input_retry_replayed=True)
        current = call("GET", "/v1/shares/" + share)["data"]
        call("POST", "/v1/shares/" + share + ":revoke", {}, revision=current["revision"])
        call("GET", "/v1/sessions/" + identity, token=bea_token, expected=404)
        call("POST", "/v1/shares", {"resource_kind": "session", "resource_id": identity, "subject_id": bea, "actions": ["session.get", "session.watch", "session.input"]})
        call("POST", "/v1/leases", {"type": "input", "resource_id": identity, "expires_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(time.time() + 240))}, token=bea_token, expected=409)
        current = call("GET", "/v1/leases/" + lease)["data"]
        call("POST", "/v1/leases/" + lease + ":release", {}, revision=current["revision"])
        bea_lease = call("POST", "/v1/leases", {"type": "input", "resource_id": identity, "expires_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(time.time() + 240))}, token=bea_token)["result"]["lease_id"]
        command = "printf 'bea-granted-input\\n'\n"
        mutation(identity, ":input", {"lease_id": bea_lease, "location_epoch": resource(identity)["location_epoch"], "sequence": 0, "data_base64": base64.b64encode(command.encode()).decode()}, token=bea_token)
        wait_for(lambda: "bea-granted-input" in text(identity))
        record("explicit-input-and-exclusive-writer", competing_lease_denied=True, recipient_input_observed=True)
        daemon.terminate()
        daemon.wait(timeout=10)
        if client_socket.exists():
            client_socket.unlink()
        daemon = daemon_start()
        wait_for(lambda: client_socket.exists())
        wait_for(lambda: resource(identity)["observation_state"] == "fresh")
        if "bea-granted-input" not in text(identity):
            raise RuntimeError("durable output missing after restart")
        command = "printf 'same-process-after-restart\\n'\n"
        mutation(identity, ":input", {"lease_id": bea_lease, "location_epoch": resource(identity)["location_epoch"], "sequence": 1, "data_base64": base64.b64encode(command.encode()).decode()}, token=bea_token)
        wait_for(lambda: "same-process-after-restart" in text(identity))
        record("daemon-restart", durable_output_retained=True, existing_pty_accepts_next_sequence=True)
        for identity in session_ids:
            mutation(identity, ":end", {"reason": "disposable demonstration complete"})
            wait_for(lambda: resource(identity)["state"] == "ended")
        record("cleanup", session_states=[resource(identity)["state"] for identity in session_ids])
        record("not-demonstrated", features=["sandbox PTY execution", "Claude Code/Codex API or subscription turns", "T3 Code/Herdr public console bridges", "git change-set return", "laptop/sandbox continuation transfer"])
    finally:
        if daemon and daemon.poll() is None:
            for identity in session_ids:
                try:
                    if resource(identity)["state"] != "ended":
                        mutation(identity, ":end", {"reason": "demonstration cleanup"})
                        wait_for(lambda: resource(identity)["state"] == "ended")
                except Exception:
                    pass
        for process in reversed(processes):
            if process.poll() is None:
                process.terminate()
        for process in processes:
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()


if __name__ == "__main__":
    main()
