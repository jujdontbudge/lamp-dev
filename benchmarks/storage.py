import gzip
import hashlib
import json
import os
from pathlib import Path
import selectors
import signal
import subprocess
import tempfile
import time
import uuid

from benchmarks.oracles import check_recovery
from benchmarks.protocol import COMMIT_POINTS, STORAGE_FORKS


def scenario(command, request, evidence, timeout=30):
    history = []
    recovered = None
    with tempfile.TemporaryDirectory(prefix="lamp-disposable-") as directory:
        root = Path(directory)
        (root / "lamp-disposable-fixture").write_text("disposable fixture; no live block devices\n")
        error_output = tempfile.TemporaryFile()
        process = subprocess.Popen(
            [*command, "--root", str(root), "--request", json.dumps(request)],
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=error_output,
            bufsize=0, start_new_session=True,
        )
        try:
            deadline = time.monotonic() + timeout
            with selectors.DefaultSelector() as selector:
                selector.register(process.stdout, selectors.EVENT_READ)
                pending = bytearray()
                while True:
                    if not selector.select(max(0, deadline - time.monotonic())):
                        raise TimeoutError("storage fixture")
                    chunk = os.read(process.stdout.fileno(), 65536)
                    if not chunk:
                        if pending:
                            raise ValueError("truncated fixture event")
                        break
                    pending.extend(chunk)
                    if len(pending) > 4 * 1024 * 1024:
                        raise ValueError("oversized fixture event")
                    while b"\n" in pending:
                        line, _, remaining = pending.partition(b"\n")
                        pending = bytearray(remaining)
                        event = json.loads(line)
                        if event["event"] == "ack":
                            ack = event["value"]
                            if ack["id"] not in ("0", "1") or ack["nonce"] != request["nonce"] or ack["generation"] != int(ack["id"]):
                                raise ValueError("unexpected acknowledgement")
                            if any(previous["id"] == ack["id"] for previous in history):
                                raise ValueError("duplicate acknowledgement")
                            expected_parent = None if ack["id"] == "0" else "0"
                            if ack["parent"] != expected_parent or ack["memory_bytes"] != 8192 or ack["disk_bytes"] != 16384:
                                raise ValueError("acknowledgement does not match externally chosen workload")
                            evidence.observe("external_ack", {"scenario": request, "ack": ack})
                            history.append(ack)
                            process.stdin.write((json.dumps({"acknowledged": ack["id"]}) + "\n").encode())
                            process.stdin.flush()
                        elif event["event"] == "recovered":
                            if recovered is not None:
                                raise ValueError("duplicate recovery")
                            recovered = event["value"]
                            expected_crash = 0 if request["point"] == "none" else 79
                            if event.get("crash_exit") != expected_crash:
                                raise ValueError("missing abrupt process termination")
                        else:
                            raise ValueError("unknown fixture event")
                    if time.monotonic() > deadline:
                        raise TimeoutError("storage fixture")
            process.wait(timeout=max(.001, deadline - time.monotonic()))
            if process.returncode or recovered is None:
                raise RuntimeError("fixture failed or omitted recovery")
            after_ack = request["point"] == "none" or COMMIT_POINTS.index(request["point"]) > COMMIT_POINTS.index("ack") or (request["point"] == "ack" and request["side"] == "after")
            expected_history = ["0", "1"] if after_ack else ["0"]
            if request.get("unauthorized") and recovered.get("unauthorized_capture_rejected") is True:
                expected_history = ["0"]
            if [ack["id"] for ack in history] != expected_history:
                raise ValueError("missing external acknowledgement")
            written = [
                {"id": str(generation), "parent": None if generation == 0 else "0",
                 "nonce": request["nonce"], "generation": generation, "memory_bytes": 8192, "disk_bytes": 16384}
                for generation in (0, 1)
            ]
            errors = check_recovery(recovered, history, {"authorized"}, written=written)
            evidence.observe("storage_recovery", {"scenario": request, "written": written, "observed": recovered, "oracle_errors": errors})
            return errors, recovered
        finally:
            if process.poll() is None:
                os.killpg(process.pid, signal.SIGKILL)
            process.wait()
            for stream in (process.stdin, process.stdout, error_output):
                stream.close()


QUALIFIED = "real-filesystem-block-barrier"
TRACE_FILES = {"public_call_transcript": "transcript.jsonl", "injected_fault_barrier_trace": "barrier-trace.txt.gz"}


def qualification_evidence(observed):
    """Names of the qualification traces this scenario failed to provide.

    A real fixture names the files it kept; the harness reads them itself,
    checks their digests and that they hold actual public calls and a block
    trace with the power-cut mark. A claim alone is not evidence."""
    missing = []
    if not isinstance(observed, dict) or observed.get("qualification") != QUALIFIED:
        return sorted(TRACE_FILES)
    kept = observed.get("evidence") if isinstance(observed.get("evidence"), dict) else {}
    directory = kept.get("artifact_dir")
    files = kept.get("files") if isinstance(kept.get("files"), dict) else {}
    for name, filename in TRACE_FILES.items():
        try:
            data = (Path(directory) / filename).read_bytes()
            if hashlib.sha256(data).hexdigest() != files.get(filename):
                raise ValueError("digest")
            if filename.endswith(".gz"):
                lines = gzip.decompress(data).decode().splitlines()
                if not any(line.endswith(" crash") for line in lines[1:]):
                    raise ValueError("no power-cut mark")
            else:
                calls = [json.loads(line) for line in data.decode().splitlines()]
                if not any(call.get("kind") in ("ctl", "admin") for call in calls):
                    raise ValueError("no public calls")
        except Exception:
            missing.append(name)
    return missing


def storage_suite(command, evidence, count=STORAGE_FORKS, model=False):
    rows, failures = [], []
    missing_evidence = set()
    cases = [(phase, side) for phase in COMMIT_POINTS for side in ("before", "after")]
    for index in range(count):
        point, side = cases[index % len(cases)]
        request = {"nonce": uuid.uuid4().hex, "point": point, "side": side, "phases": COMMIT_POINTS,
                   "fault": "reorder" if index % 2 else "discard", "iteration": index}
        observed = None
        try:
            errors, observed = scenario(command, request, evidence)
        except Exception as error:
            errors = [type(error).__name__ + ": " + str(error)]
        if not model:
            missing_evidence.update(qualification_evidence(observed))
        if errors:
            failures.append({"iteration": index, "errors": errors})
        rows.append({"point": point, "side": side, "errors": errors})
    controls = []
    for omitted in ("object_flush", "directory_flush", "journal_flush"):
        request = {"nonce": uuid.uuid4().hex, "point": "ack", "side": "after", "phases": COMMIT_POINTS, "omit": omitted}
        errors, observed = scenario(command, request, evidence)
        if not model:
            missing_evidence.update(qualification_evidence(observed))
        caught = any("lost acknowledged object 1" in error for error in errors)
        controls.append({"omitted": omitted, "caught": caught, "errors": errors})
    request = {"nonce": uuid.uuid4().hex, "point": "none", "side": "after", "phases": COMMIT_POINTS, "unauthorized": True}
    errors, unauthorized_observation = scenario(command, request, evidence)
    if not model:
        missing_evidence.update(qualification_evidence(unauthorized_observation))
    unauthorized_caught = any("unauthorized" in error for error in errors)
    unauthorized_rejected = (
        unauthorized_observation.get("unauthorized_capture_rejected") is True and
        not errors and "1" not in unauthorized_observation.get("objects", {})
    )
    passed = not failures and all(control["caught"] for control in controls) and (unauthorized_caught if model else unauthorized_rejected)
    qualified = not model and not missing_evidence and count >= STORAGE_FORKS
    return {
        "status": "fail" if not passed else "pass" if qualified else "incomplete",
        "forks": count, "required": STORAGE_FORKS, "failures": failures,
        "crash_coverage": sorted({row["point"] + ":" + row["side"] for row in rows}),
        "negative_controls": controls, "unauthorized_capture_caught": unauthorized_caught,
        "unauthorized_capture_rejected": unauthorized_rejected,
        "qualification": "disposable-model-only" if model else QUALIFIED if qualified else "unqualified-external-fixture",
        "missing_qualification_evidence": sorted(TRACE_FILES) if model else sorted(missing_evidence),
    }
