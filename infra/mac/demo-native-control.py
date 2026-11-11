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
    p.add_argument("--worker-config", type=Path)
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
    worker=json.loads(a.worker_config.read_text()) if a.worker_config else None

    def start():
        config.write_text(json.dumps(configuration))
        config.chmod(0o600)
        command=[str(a.daemon), "--socket", str(api), "--state-dir", str(root / "state"),
                 "--identity-file", str(identity), "--native-fallback-file", str(config)]
        if worker:
            for key in ["worker-socket","worker-id","worker-boot-id","worker-uid","worker-pid","grant-id"]:
                command.extend(["--"+key,str(worker[key])])
            command.extend(["--worker-timeout","60s"])
        process = subprocess.Popen(command,
                                   stdout=log, stderr=log)
        deadline = time.monotonic() + 30
        while not api.exists():
            if process.poll() is not None or time.monotonic() >= deadline:
                raise RuntimeError("native control did not start")
            time.sleep(0.05)
        return process

    def request(method, path, bearer, body=None,key=None,extra=None):
        connection = http.client.HTTPConnection("localhost", timeout=65)
        connection.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        connection.sock.settimeout(65)
        connection.sock.connect(str(api))
        headers={"Authorization": "Bearer " + bearer, "Content-Type": "application/json",
                 "Idempotency-Key": key or secrets.token_hex(16)}
        headers.update(extra or {})
        connection.request(method, path, None if body is None else json.dumps(body),headers)
        response = connection.getresponse()
        value = {"status": response.status, "body": json.loads(response.read()),
                 "isolation": response.getheader("X-Lamp-Isolation")}
        connection.close()
        return value

    def restart():
        nonlocal daemon
        daemon.terminate()
        daemon.wait(timeout=15)
        if api.exists():api.unlink()
        daemon=start()

    def clean(label):
        directories=list((root/"state").glob("native-task-*"))
        result[label]=len(directories)
        if directories:raise RuntimeError("native request retained its task bundle")

    try:
        daemon = start()
        expires=(datetime.datetime.now(datetime.timezone.utc)+datetime.timedelta(hours=1)).strftime("%Y-%m-%dT%H:%M:%SZ")
        enrolled = request("POST", "/v1/keys", token, {"expires_at":expires})
        if enrolled["status"] != 200:
            result["enrollment_error"]=enrolled
            raise RuntimeError("owner could not enroll scoped job key: "+json.dumps(enrolled))
        ci_token = enrolled["body"]["result"]["secret"]
        scoped=request("POST","/v1/keys",token,{"expires_at":expires})
        if scoped["status"]!=200:raise RuntimeError("could not enroll owner scoped key")
        scoped_token=scoped["body"]["result"]["secret"]
        configuration["jobs"][hashlib.sha256(ci_token.encode()).hexdigest()] = {
            "job_id": "jb_public_fixture", "repository_visibility": "public"}
        restart()
        result["valid_job_credential"] = request("GET", "/v1/fleets/fl_local", ci_token)
        task = {"argv": ["/usr/bin/sqlite3", ":memory:", "select 6*7;"], "memory_mib": 256, "timeout_seconds": 10}
        result["public_ci_forged_origin"] = request("POST", "/internal/native-tasks", ci_token,
                                                   dict(task, origin="owner_task", repository_visibility="private"))
        result["public_ci_ordinary_create"] = request("POST", "/v1/sandboxes", ci_token, {})
        result["owner_scoped_ordinary_create"] = request("POST", "/v1/sandboxes", scoped_token, {})
        result["owner_native_task"] = request("POST", "/internal/native-tasks", token, task)
        clean("bundles_after_success")
        result["enrolled_job"] = {"job_id": "jb_public_fixture", "repository_visibility": "public",
                                  "source": "custodian-owned startup configuration", "caller_mutable": False}
        if result["valid_job_credential"]["status"] != 200:
            raise RuntimeError("job credential was not authenticated")
        if result["public_ci_forged_origin"]["status"]!=403 or result["public_ci_forged_origin"]["body"]["error"]["code"]!="VM_REQUIRED":
            raise RuntimeError("public CI native task was not refused")
        for name in ["public_ci_ordinary_create","owner_scoped_ordinary_create"]:
            if result[name]["body"].get("error",{}).get("code")!="INVALID_ARGUMENT":
                raise RuntimeError("VM request did not reach normal argument validation")
        if result["owner_native_task"]["status"] != 200:
            raise RuntimeError("native control execution did not complete")
        for field in ["packager","launcher"]:
            installed=configuration[field]
            configuration[field]=str(root/("missing-"+field))
            restart()
            key="retry-after-"+field+"-failure"
            failed=request("POST","/internal/native-tasks",token,task,key=key)
            result[field+"_failure"]=failed
            clean("bundles_after_"+field+"_failure")
            if failed["body"].get("error",{}).get("code")!="HOST_UNAVAILABLE":raise RuntimeError("expected pre-execution failure")
            configuration[field]=installed
            restart()
            retried=request("POST","/internal/native-tasks",token,task,key=key)
            result[field+"_retry"]=retried
            clean("bundles_after_"+field+"_retry")
            if retried["status"]!=200:raise RuntimeError("idempotency key remained consumed before execution")
        if worker:
            for label,bearer in [("public_ci_vm",ci_token),("owner_scoped_vm",scoped_token)]:
                created=request("POST","/v1/sandboxes",bearer,{"template_id":worker["template_id"],"resources":worker["resources"],"wait_ms":60000})
                result[label]=created
                if created["status"]!=200 or created["body"].get("operation",{}).get("state")!="succeeded":raise RuntimeError("VM create did not succeed: "+json.dumps(created))
                sid=created["body"]["result"]["sandbox_id"]
                current=request("GET","/v1/sandboxes/"+sid,bearer)
                removed=request("DELETE","/v1/sandboxes/"+sid,bearer,{"wait_ms":60000},extra={"If-Match":str(current["body"]["data"]["revision"])})
                result[label+"_release"]=removed
                if removed["status"]!=200:raise RuntimeError("VM release failed")
    finally:
        if daemon is not None and daemon.poll() is None:
            daemon.terminate()
            daemon.wait(timeout=15)
        log.close()
        (a.output / "result.json").write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps(result, indent=2))


if __name__ == "__main__":
    main()
