"""Independent E03 caller-to-command-start upper bound; no worker/private imports.

Inputs name prepared, owner-private sandboxes and 32 environment variable names
for distinct credentials across at least two principals. Provisioning, output validation and replay checks stay
outside the measured endpoint. No service, template or Docker mutation occurs.
"""
import base64
from concurrent.futures import ThreadPoolExecutor
import hashlib
import json
import multiprocessing
import queue
import sys
import os
import subprocess
from pathlib import Path
import threading
import time
import urllib.error
import uuid

from benchmarks.protocol import CONTROL_CALLERS, CONTROL_SAMPLES, CONTROL_P99_MS, percentile
from benchmarks.public import PublicLamp


def control_suite(url, cli, callers, evidence, integrated_e01=None, smoke=False, socket_path=None, allocation_observer=None, cleanup_prepared=False, process_callers=False):
    if len(callers) != CONTROL_CALLERS:
        raise ValueError("E03 requires exactly 32 credentialed callers")
    if len({c["principal_id"] for c in callers}) < 2:
        raise ValueError("E03 requires at least two distinct principals for owner isolation")
    if len({c["token_env"] for c in callers}) != CONTROL_CALLERS:
        raise ValueError("E03 requires distinct token_env")
    owners = {}
    for caller in callers:
        if owners.setdefault(caller["sandbox_id"], caller["principal_id"]) != caller["principal_id"]:
            raise ValueError("a sandbox cannot be assigned across owners")
    clients = []
    for caller in callers:
        if set(caller) != {"principal_id", "sandbox_id", "token_env"}:
            raise ValueError("caller manifest contains unknown fields; never store bearer values")
        token = os.environ.get(caller["token_env"])
        if not token or cli:
            raise ValueError("E03 requires API credentials in environment, never CLI argv")
        client = PublicLamp(url=url, timeout=60, token=token, socket_path=socket_path)
        resource = client.get(caller["sandbox_id"])
        if resource["owner_id"] != caller["principal_id"] or resource["state"] != "ready":
            raise ValueError("caller does not own a ready sandbox")
        clients.append(client)
    if len({hashlib.sha256(c.token.encode()).digest() for c in clients}) != CONTROL_CALLERS:
        raise ValueError("E03 requires 32 distinct credentials")
    count = 2 if smoke else CONTROL_SAMPLES
    if process_callers and sys.platform != "linux":
        raise ValueError("process callers require the native Linux fork runtime")
    runtime = multiprocessing.get_context("fork") if process_callers else None
    barrier = runtime.Barrier(CONTROL_CALLERS, timeout=60) if runtime else threading.Barrier(CONTROL_CALLERS, timeout=60)
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
                        row.update(exit_code=resource.get("exit_code"), signal=resource.get("signal"), output_sha256=hashlib.sha256(output).hexdigest())
                        if resource.get("exit_code") != 0 or json.loads(output) != marker:
                            raise ValueError("lost or incorrect command effect")
                        break
                    if resource["state"] in ("failed", "outcome_unknown") or time.monotonic() >= deadline:
                        raise ValueError("missing command completion")
                try:
                    next(c for i, c in enumerate(clients) if callers[i]["principal_id"] != caller["principal_id"]).call("exec", "get", {"exec_id": identity})
                except urllib.error.HTTPError as error:
                    error.close()
                    if error.code != 404:
                        raise ValueError("foreign resource did not return NOT_FOUND") from error
                else:
                    raise ValueError("cross-owner resource read")
            except Exception as error:
                row["error"] = type(error).__name__ + ": " + str(error)
            with lock:
                rows.append(row)
    if runtime:
        results = runtime.Queue()
        def child(index):
            try:
                run(index)
                results.put({"index": index, "rows": rows,
                             "calls": [(i, c.calls) for i, c in enumerate(clients) if c.calls]})
            except BaseException as error:
                results.put({"index": index, "fatal": type(error).__name__ + ": " + str(error)})
        processes = [runtime.Process(target=child, args=(i,)) for i in range(CONTROL_CALLERS)]
        try:
            for process in processes:
                process.start()
            received = set()
            deadline = time.monotonic() + count * 61 + 60
            while len(received) < CONTROL_CALLERS:
                try:
                    result = results.get(timeout=1)
                except queue.Empty:
                    if time.monotonic() > deadline or any(p.exitcode not in (None, 0) for p in processes) or all(p.exitcode is not None for p in processes):
                        raise RuntimeError("caller process failed to return its transcript")
                    continue
                if result["index"] in received or "fatal" in result:
                    raise RuntimeError("invalid caller process transcript: " + result.get("fatal", "duplicate caller"))
                received.add(result["index"])
                rows.extend(result["rows"])
                for index, calls in result["calls"]:
                    clients[index].calls.extend(calls)
            for process in processes:
                process.join(timeout=10)
                if process.exitcode != 0:
                    raise RuntimeError("caller process did not exit cleanly")
            evidence.observe("caller_runtime", {"mode": "32 Linux processes", "pids": [p.pid for p in processes]})
        finally:
            for process in processes:
                if process.is_alive():
                    process.kill()
                process.join(timeout=10)
            results.close()
    else:
        with ThreadPoolExecutor(max_workers=CONTROL_CALLERS) as pool:
            list(pool.map(run, range(CONTROL_CALLERS)))
    # The owned LX lane permits at most two live sandboxes. Retire the other
    # owner after the fixed latency plan, before live-command quota contention.
    retirement_errors = []
    if allocation_observer and len(owners) == 2:
        other = next(i for i, c in enumerate(callers) if c["principal_id"] != callers[0]["principal_id"])
        try:
            clients[other].delete(callers[other]["sandbox_id"])
            evidence.observe("quota_preparation", {"retired_sandbox_id": callers[other]["sandbox_id"]})
        except Exception as error:
            retirement_errors.append(type(error).__name__ + ": " + str(error))
    quota = quota_contention(clients[0], callers[0], evidence, url, socket_path, allocation_observer)
    cleanup_errors = retirement_errors
    if cleanup_prepared:
        for sandbox in owners:
            client = clients[next(i for i, c in enumerate(callers) if c["sandbox_id"] == sandbox)]
            try:
                if client.get(sandbox)["state"] != "deleted":
                    client.delete(sandbox)
            except Exception as error:
                cleanup_errors.append(type(error).__name__ + ": " + str(error))
        evidence.observe("prepared_cleanup", {"errors": cleanup_errors})
    for row in sorted(rows, key=lambda r: (r["iteration"], r["caller"])):
        evidence.observe("control_trial", row)
    for index, client in enumerate(clients):
        for call in client.calls:
            evidence.observe("public_call", {"caller": index, **call})
    samples = [r["elapsed_ms"] for r in rows if "elapsed_ms" in r]
    failures = sum(r["error"] is not None for r in rows) + len(cleanup_errors)
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
    boundary_status = "pass" if quota["status"] != "fail" and len(samples) == CONTROL_CALLERS * count and not failures and p99 <= CONTROL_P99_MS else "fail"
    status = "fail" if boundary_status == "fail" or e01_status == "fail" else "incomplete" if smoke or e01_status != "pass" or quota["status"] != "pass" else "pass"
    return {"status": status, "control_status": boundary_status, "integrated_e01_status": e01_status,
            "callers": CONTROL_CALLERS, "principals": len({c["principal_id"] for c in callers}), "prepared_sandboxes": len(owners), "trials": len(rows), "failures": failures,
            "p50_ms": percentile(samples, .5), "p95_ms": percentile(samples, .95), "p99_ms": p99,
            "boundary_ms": CONTROL_P99_MS, "quota": quota,
            "endpoint": "caller send through durable admission and worker command_started receipt; output/replay/isolation checked outside timer"}


def quota_contention(parent, caller, evidence, url, socket_path, observer):
    """Hold one live command while 32 distinct callers contend for a limit of 1.

    Independently selected guest marker files witness actual effects/processes;
    kernel snapshots detect unexpected VM allocations. No additional sandbox is
    created. This phase is outside the unchanged 6,400-command latency plan.
    """
    if not observer:
        return {"status": "incomplete", "reason": "missing independent allocation observer"}
    samples, witnesses, errors, accepted = [], [], [], []
    stop = threading.Event()
    key_id, client, thread = None, None, None
    directory = "/tmp/lamp-e03-quota-" + uuid.uuid4().hex
    markers = [uuid.uuid4().hex for _ in range(CONTROL_CALLERS)]
    sandbox = caller["sandbox_id"]
    setup = "import os,json,sys; os.mkdir(sys.argv[1]); print(json.dumps(True))"
    command = "import os,json,sys,time\nroot,marker=sys.argv[1:]\nwith open(root+'/'+marker,'x') as f: f.write(str(os.getpid()))\nwhile not os.path.exists(root+'/release'): time.sleep(.01)\nprint(json.dumps({'marker':marker}))"
    inspect = "import os,json,sys; root=sys.argv[1]; print(json.dumps({n:int(open(root+'/'+n).read()) for n in os.listdir(root) if n!='release'}))"
    release = "import json,sys; open(sys.argv[1]+'/release','a').close(); print(json.dumps(True))"
    cleanup = "import shutil,json,sys; shutil.rmtree(sys.argv[1],ignore_errors=True); print(json.dumps(True))"

    def snapshot():
        reply = subprocess.run(observer, input=json.dumps({"operation": "allocations"}),
                               text=True, capture_output=True, timeout=10, check=True)
        value = json.loads(reply.stdout)
        ids = value["sandbox_ids"]
        if not isinstance(ids, list) or len(set(ids)) != len(ids) or any(not isinstance(i, str) for i in ids):
            raise ValueError("invalid independent allocation sample")
        samples.append(set(ids))
        witnesses.append(value)

    def observe():
        while not stop.wait(.005):
            try:
                snapshot()
            except Exception as error:
                errors.append("allocation witness: " + str(error))
                return

    try:
        parent.execute(sandbox, ["python3", "-c", setup, directory])
        snapshot()
        before = samples[0]
        if sandbox not in before:
            raise ValueError("independent observer did not witness prepared allocation")
        reply = parent.call("key", "create", {"principal_id": caller["principal_id"],
            "scope": {"limits": {"concurrency": 1}},
            "expires_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(time.time() + 3600)),
            "idempotency_key": uuid.uuid4().hex})
        key_id = reply["result"]["key_id"]
        client = PublicLamp(url=url, socket_path=socket_path, token=reply["result"]["secret"], timeout=60)
        barrier = threading.Barrier(CONTROL_CALLERS, timeout=60)
        thread = threading.Thread(target=observe)
        thread.start()

        def create(index):
            barrier.wait()
            try:
                result = client.mutation("exec", "create", {"sandbox_id": sandbox,
                    "argv": ["python3", "-c", command, directory, markers[index]]})
                return {"exec_id": result["exec_id"], "marker": markers[index]}
            except urllib.error.HTTPError as error:
                with error:
                    value = json.load(error)
                if value.get("error", {}).get("code") != "SCOPE_DENIED":
                    raise ValueError("unexpected quota denial") from error
                return None

        with ThreadPoolExecutor(max_workers=CONTROL_CALLERS) as pool:
            futures = [pool.submit(create, i) for i in range(CONTROL_CALLERS)]
            for future in futures:
                try:
                    result = future.result()
                    if result:
                        accepted.append(result)
                except Exception as error:
                    errors.append(str(error))
        observed = {}
        until = time.monotonic() + 10
        while time.monotonic() < until:
            observed = parent.execute(sandbox, ["python3", "-c", inspect, directory])
            if len(observed) >= len(accepted):
                break
            time.sleep(.01)
        evidence.observe("independent_quota_effects", {"marker_pids": observed})
        if len(accepted) != 1 or set(observed) != {a["marker"] for a in accepted} or any(type(p) is not int or p <= 0 for p in observed.values()):
            errors.append("quota overspend or lost independent command allocation/effect")
        snapshot()
        if any(sample != before for sample in samples):
            errors.append("unexpected independent sandbox allocation")
    except Exception as error:
        errors.append(type(error).__name__ + ": " + str(error))
    finally:
        stop.set()
        if thread:
            thread.join(timeout=15)
        try:
            parent.execute(sandbox, ["python3", "-c", release, directory])
            for result in accepted:
                if parent.wait_exec(result["exec_id"]) != {"marker": result["marker"]}:
                    errors.append("incorrect quota command completion")
            parent.execute(sandbox, ["python3", "-c", cleanup, directory])
            if key_id:
                key = parent.call("key", "get", {"key_id": key_id})["data"]
                parent.call("key", "delete", {"key_id": key_id, "expected_revision": key["revision"], "idempotency_key": uuid.uuid4().hex})
        except Exception as error:
            errors.append("quota cleanup: " + str(error))
    if client:
        for call in client.calls:
            evidence.observe("quota_public_call", call)
    for index, sample in enumerate(samples):
        evidence.observe("independent_allocations", {"sample": index, **witnesses[index]})
    result = {"status": "fail" if errors else "pass", "limit": 1, "preexisting_live_commands": 0,
              "contenders": CONTROL_CALLERS, "accepted": accepted, "errors": errors, "allocation_samples": len(samples)}
    evidence.observe("quota_contention", result)
    return result
