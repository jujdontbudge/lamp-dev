import argparse
import datetime
import hashlib
import http.client
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import tempfile
import time


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--daemon", type=Path, required=True)
    p.add_argument("--launcher", type=Path, required=True)
    p.add_argument("--helper", type=Path, required=True)
    p.add_argument("--output", type=Path, required=True)
    a = p.parse_args()
    a.output.mkdir(parents=True, exist_ok=False)
    root = Path(tempfile.mkdtemp(prefix="lamp-native-control-", dir="/private/var/tmp"))
    token = secrets.token_urlsafe(32)
    identity = root / "identity.json"
    identity.write_text(json.dumps({"owner_id": "pr_native_fixture", "bootstrap_sha256": hashlib.sha256(token.encode()).hexdigest()}))
    identity.chmod(0o600)
    configuration = {"launcher": str(a.launcher.resolve()), "helper_binary": str(a.helper.resolve()),
                     "packager": str(Path(__file__).resolve().with_name("package-native.sh")), "jobs": {}}
    config = root / "native.json"
    api = root / "api.sock"
    log = (a.output / "control.log").open("w")
    result = {}
    daemon = None

    def start():
        config.write_text(json.dumps(configuration))
        config.chmod(0o600)
        process = subprocess.Popen([str(a.daemon), "--socket", str(api), "--state-dir", str(root / "state"),
                                    "--identity-file", str(identity), "--native-fallback-file", str(config)],
                                   stdout=log, stderr=log)
        deadline = time.monotonic() + 30
        while not api.exists():
            if process.poll() is not None or time.monotonic() >= deadline:
                raise RuntimeError("native control did not start")
            time.sleep(0.05)
        return process

    def request(method, path, bearer, body=None):
        connection = http.client.HTTPConnection("localhost", timeout=30)
        connection.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        connection.sock.settimeout(30)
        connection.sock.connect(str(api))
        connection.request(method, path, None if body is None else json.dumps(body),
                           {"Authorization": "Bearer " + bearer, "Content-Type": "application/json",
                            "Idempotency-Key": secrets.token_hex(16)})
        response = connection.getresponse()
        value = {"status": response.status, "body": json.loads(response.read()),
                 "isolation": response.getheader("X-Lamp-Isolation")}
        connection.close()
        return value

    try:
        daemon = start()
        expires=(datetime.datetime.now(datetime.timezone.utc)+datetime.timedelta(hours=1)).strftime("%Y-%m-%dT%H:%M:%SZ")
        enrolled = request("POST", "/v1/keys", token, {"expires_at":expires})
        if enrolled["status"] != 200:
            result["enrollment_error"]=enrolled
            raise RuntimeError("owner could not enroll scoped job key: "+json.dumps(enrolled))
        ci_token = enrolled["body"]["result"]["secret"]
        configuration["jobs"][hashlib.sha256(ci_token.encode()).hexdigest()] = {
            "job_id": "jb_public_fixture", "repository_visibility": "public"}
        daemon.terminate()
        daemon.wait(timeout=15)
        if api.exists():
            api.unlink()
        daemon = start()
        result["valid_job_credential"] = request("GET", "/v1/fleets/fl_local", ci_token)
        task = {"argv": ["/usr/bin/sqlite3", ":memory:", "select 6*7;"], "memory_mib": 256, "timeout_seconds": 10}
        result["public_ci_forged_origin"] = request("POST", "/internal/native-tasks", ci_token,
                                                   dict(task, origin="owner_task", repository_visibility="private"))
        result["public_ci_ordinary_create"] = request("POST", "/v1/sandboxes", ci_token, {})
        result["owner_native_task"] = request("POST", "/internal/native-tasks", token, task)
        result["enrolled_job"] = {"job_id": "jb_public_fixture", "repository_visibility": "public",
                                  "source": "custodian-owned startup configuration", "caller_mutable": False}
        if result["valid_job_credential"]["status"] != 200:
            raise RuntimeError("job credential was not authenticated")
        for name in ["public_ci_forged_origin", "public_ci_ordinary_create"]:
            if result[name]["status"] != 403 or result[name]["body"]["error"]["code"] != "VM_REQUIRED":
                raise RuntimeError("public CI was not refused by trusted control authority")
        if result["owner_native_task"]["status"] != 200:
            raise RuntimeError("native control execution did not complete")
        journal = json.loads((root / "state/journal.json").read_text())
        result["public_ci_sandboxes_created"] = len(journal["sandboxes"])
        if result["public_ci_sandboxes_created"]:
            raise RuntimeError("refused job created a sandbox")
    finally:
        if daemon is not None and daemon.poll() is None:
            daemon.terminate()
            daemon.wait(timeout=15)
        log.close()
        (a.output / "result.json").write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps(result, indent=2))


if __name__ == "__main__":
    main()
