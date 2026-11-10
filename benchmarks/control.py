"""Independent E03 caller-to-command-start upper bound; no worker/private imports.

Inputs name 32 already prepared, private sandboxes and environment variable names
for their credentials. Provisioning, output validation and replay checks stay
outside the measured endpoint. No service, template or Docker mutation occurs.
"""
import base64
from concurrent.futures import ThreadPoolExecutor
import hashlib
import json
import os
from pathlib import Path
import threading
import time
import urllib.error
import uuid

from benchmarks.protocol import CONTROL_CALLERS, CONTROL_SAMPLES, CONTROL_P99_MS, percentile
from benchmarks.public import PublicLamp


def control_suite(url, cli, callers, evidence, integrated_e01=None, smoke=False):
    if len(callers) != CONTROL_CALLERS:
        raise ValueError("E03 requires exactly 32 credentialed callers")
    for field in ("principal_id", "sandbox_id", "token_env"):
        if len({c[field] for c in callers}) != CONTROL_CALLERS:
            raise ValueError("E03 requires distinct " + field)
    clients = []
    for caller in callers:
        if set(caller) != {"principal_id", "sandbox_id", "token_env"}:
            raise ValueError("caller manifest contains unknown fields; never store bearer values")
        token = os.environ.get(caller["token_env"])
        if not token or cli:
            raise ValueError("E03 requires API credentials in environment, never CLI argv")
        client = PublicLamp(url=url, timeout=60, token=token)
        resource = client.get(caller["sandbox_id"])
        if resource["owner_id"] != caller["principal_id"] or resource["state"] != "ready":
            raise ValueError("caller does not own a ready sandbox")
        clients.append(client)
    count = 2 if smoke else CONTROL_SAMPLES
    barrier = threading.Barrier(CONTROL_CALLERS, timeout=60)
    rows = []
    lock = threading.Lock()

    def run(index):
        caller, client = callers[index], clients[index]
        for iteration in range(count):
            marker = {"principal_id": caller["principal_id"], "nonce": uuid.uuid4().hex}
            fields = {"sandbox_id": caller["sandbox_id"], "argv": ["/bin/echo", json.dumps(marker)],
                      "idempotency_key": uuid.uuid4().hex, "wait_ms": 60000}
            row = {"caller": index, "iteration": iteration, "error": None}
            try:
                barrier.wait()
                started = time.monotonic_ns()
                try:
                    accepted = client.call("exec", "create", fields)
                finally:
                    row["elapsed_ms"] = (time.monotonic_ns() - started) / 1e6
                if accepted.get("operation", {}).get("state") != "succeeded":
                    raise ValueError("no terminal command-start evidence")
                identity = accepted["result"]["exec_id"]
                row.update(operation_id=accepted["operation_id"], exec_id=identity)
                replay = client.call("exec", "create", fields)
                if not replay.get("replayed") or replay["operation_id"] != accepted["operation_id"] or replay["result"]["exec_id"] != identity:
                    raise ValueError("replay created new work")
                output = bytearray()
                cursor = None
                deadline = time.monotonic() + 60
                while True:
                    page = client.call("exec", "watch", {"exec_id": identity, "wait_ms": 1000, **({"cursor": cursor} if cursor else {})})
                    for frame in page["items"]:
                        if frame["channel"] == "stdout":
                            output.extend(base64.b64decode(frame["data_base64"], validate=True))
                    if len(output) > 65536:
                        raise ValueError("unbounded command output")
                    cursor = page.get("next_cursor")
                    resource = client.call("exec", "get", {"exec_id": identity})["data"]
                    if resource["owner_id"] != caller["principal_id"]:
                        raise ValueError("cross-owner effect")
                    if not cursor and resource["state"] == "exited":
                        if resource.get("exit_code") != 0 or json.loads(output) != marker:
                            raise ValueError("lost or incorrect command effect")
                        break
                    if resource["state"] in ("failed", "outcome_unknown") or time.monotonic() >= deadline:
                        raise ValueError("missing command completion")
                try:
                    clients[(index + 1) % CONTROL_CALLERS].call("exec", "get", {"exec_id": identity})
                except urllib.error.HTTPError as error:
                    if error.code != 404:
                        raise ValueError("foreign resource did not return NOT_FOUND") from error
                else:
                    raise ValueError("cross-owner resource read")
            except Exception as error:
                row["error"] = type(error).__name__ + ": " + str(error)
                barrier.abort()
            with lock:
                rows.append(row)
    with ThreadPoolExecutor(max_workers=CONTROL_CALLERS) as pool:
        list(pool.map(run, range(CONTROL_CALLERS)))
    for row in sorted(rows, key=lambda r: (r["iteration"], r["caller"])):
        evidence.observe("control_trial", row)
    for index, client in enumerate(clients):
        for call in client.calls:
            evidence.observe("public_call", {"caller": index, **call})
    samples = [r["elapsed_ms"] for r in rows if "elapsed_ms" in r]
    failures = sum(r["error"] is not None for r in rows)
    e01_status = "incomplete"
    if integrated_e01:
        path = Path(integrated_e01)
        raw = path.read_bytes()
        value = json.loads(raw)
        e01_status = value.get("results", {}).get("E01", {}).get("status", "incomplete")
        manifest_path = path.with_name("manifest.json")
        matched = False
        manifest_hash = None
        if manifest_path.is_file():
            manifest_raw = manifest_path.read_bytes()
            manifest_hash = hashlib.sha256(manifest_raw).hexdigest()
            old_metadata = json.loads(manifest_raw).get("metadata", {})
            matched = all(old_metadata.get(k) == evidence.metadata.get(k) and old_metadata.get(k)
                          for k in ("source_hashes", "dependency_hashes", "artifacts", "compiled_features",
                                    "policy", "resources", "durability", "lane", "topology", "controls"))
        if e01_status == "pass" and (value.get("status") != "pass" or not matched):
            e01_status = "incomplete"
        evidence.observe("integrated_e01", {"sha256": hashlib.sha256(raw).hexdigest(), "status": e01_status,
                                            "manifest_sha256": manifest_hash, "same_build_and_policy": matched})
    p99 = percentile(samples, .99)
    boundary_status = "pass" if len(samples) == CONTROL_CALLERS * count and not failures and p99 <= CONTROL_P99_MS else "fail"
    status = "fail" if boundary_status == "fail" or e01_status == "fail" else "incomplete" if smoke or e01_status != "pass" else "pass"
    return {"status": status, "control_status": boundary_status, "integrated_e01_status": e01_status,
            "callers": CONTROL_CALLERS, "trials": len(rows), "failures": failures,
            "p50_ms": percentile(samples, .5), "p95_ms": percentile(samples, .95), "p99_ms": p99,
            "boundary_ms": CONTROL_P99_MS,
            "endpoint": "caller send through durable admission and worker command_started receipt; output/replay/isolation checked outside timer"}
