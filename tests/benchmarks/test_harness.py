import base64
import copy
import gzip
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import math
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import unittest
import urllib.parse

from benchmarks.engine import EngineHarness
from benchmarks.evidence import Evidence
from benchmarks.oracles import check_recovery, check_state, pattern_hash
from benchmarks.protocol import COMMIT_POINTS, MEASURES, paired_summary, plan, timing
from benchmarks.public import PublicLamp
from benchmarks.storage import scenario, storage_suite
from benchmarks.workload import blocks, measure, network_measure


ROOT = Path(__file__).resolve().parents[2]


class RecordingEvidence:
    def __init__(self):
        self.events = []

    def observe(self, kind, value):
        self.events.append((kind, copy.deepcopy(value)))


class PublicFixture:
    def __init__(self, shared=False, asynchronous=False):
        self.sandboxes, self.execs = {}, {}
        self.shared, self.asynchronous = shared, asynchronous
        self.operations = {}
        self.paths = []

    def handle(self, method, path, fields):
        self.paths.append((method, path, fields))
        if path == "/v1/sandboxes":
            identity = "sb_" + str(len(self.sandboxes))
            self.sandboxes[identity] = {"revision": 1, "state": None, "resources": fields["resources"], "durability": fields["durability"]}
            return {"result": {"sandbox_id": identity, "session_ids": []}, "request_id": "rq"}
        if path.startswith("/v1/operations/"):
            return {"data": {"state": "succeeded", "outcome": {"result": self.operations[path.split("/")[-1]]}}}
        if path.startswith("/v1/sandboxes/"):
            identity = path.split("/")[-1].split(":")[0]
            if path.endswith(":fork"):
                child = "sb_" + str(len(self.sandboxes))
                self.sandboxes[child] = self.sandboxes[identity] if self.shared else copy.deepcopy(self.sandboxes[identity])
                return {"result": {"sandbox_id": child, "session_ids": []}}
            if method == "DELETE":
                self.sandboxes[identity]["deleted"] = True
                return {"result": {}}
            sandbox = self.sandboxes[identity]
            return {"data": {"id": identity, "revision": sandbox["revision"], "resources": sandbox["resources"],
                             "durability": sandbox["durability"], "boundary": "vm"}}
        if path == "/v1/execs":
            identity = "ex_" + str(len(self.execs))
            argv = fields["argv"]
            sandbox = self.sandboxes[fields["sandbox_id"]]
            command = argv[2]
            flags = dict(zip(argv[3::2], argv[4::2]))
            if command == "call":
                state = sandbox["state"]
                request = json.loads(flags["--request"])
                state["ticks"] += 1
                if request["op"] == "write":
                    generation = request["generation"]
                    state["generation"] = generation
                    for domain, key, size in (("ram", "memory_sha256", state["size"]), ("disk", "disk_sha256", 16384)):
                        raw = b"".join(blocks(state["nonce"], generation, size, domain))
                        state[key] = hashlib.sha256(raw).hexdigest()
                output = state
            else:
                output = json.loads(subprocess.check_output(argv))
            self.execs[identity] = json.dumps(output).encode()
            result = {"exec_id": identity}
            if self.asynchronous:
                operation = "op_" + identity
                self.operations[operation] = result
                return {"operation_id": operation, "operation": {"state": "running"}}
            return {"result": result}
        if path.startswith("/v1/execs/"):
            identity = path.split("/")[3]
            if path.endswith("/output"):
                return {"items": [{"stream_id": identity, "sequence": 0, "channel": "stdout",
                                   "data_base64": base64.b64encode(self.execs[identity]).decode(),
                                   "byte_offset": "0", "location_epoch": 1}]}
            return {"data": {"state": "exited", "exit_code": 0}}
        raise AssertionError((method, path, fields))


class HarnessTests(unittest.TestCase):
    def server(self, fixture):
        class Handler(BaseHTTPRequestHandler):
            def respond(self):
                raw = self.rfile.read(int(self.headers.get("Content-Length", 0)))
                result = fixture.handle(self.command, urllib.parse.urlsplit(self.path).path, json.loads(raw) if raw else {})
                body = json.dumps(result).encode()
                self.send_response(200)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            do_GET = do_POST = do_DELETE = respond

            def log_message(self, *args):
                pass

        server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        return PublicLamp(url=f"http://127.0.0.1:{server.server_port}", timeout=5)

    def test_frozen_rows_and_thresholds(self):
        self.assertEqual(len(plan()), 9)
        self.assertEqual({r["dirty_mib"] for r in plan()}, {0, 128, 1024})
        rows = [{"warmup": False, "elapsed_ms": 1000, "error": None}] * 100
        self.assertEqual(timing(rows, 100, 1000, every=True)["status"], "fail")
        rows = [{"warmup": False, "elapsed_ms": 500, "error": None}] * 200
        self.assertEqual(timing(rows, 200, 500)["status"], "fail")
        rows[0] = {**rows[0], "elapsed_ms": 2, "error": "timeout"}
        self.assertEqual(timing(rows, 200, 500)["failures"], 1)

    def test_pair_directions_boundary_and_uncertainty(self):
        rates = [{"native": 100, "secured": 97}] * 10
        self.assertEqual(paired_summary(rates, "rate")["status"], "pass")
        latency = [{"native": 97, "secured": 100}] * 10
        self.assertEqual(paired_summary(latency, "latency")["status"], "pass")
        self.assertEqual(paired_summary(rates[:9], "rate")["status"], "fail")
        variable = [{"native": 1, "secured": v} for v in [.8, 1.3] * 5]
        self.assertEqual(paired_summary(variable, "rate")["statistical_evidence"], "provisional")
        for value in (0, math.nan, math.inf):
            with self.assertRaises(ValueError):
                paired_summary([{"native": 1, "secured": value}], "rate")

    def test_independent_generation_checksum(self):
        for size in (0, 1, 4096, 8193):
            raw = b"".join(blocks("external-nonce", 27, size, "ram"))
            self.assertEqual(pattern_hash("external-nonce", 27, size, "ram"), hashlib.sha256(raw).hexdigest())
        bad = {"nonce": "x", "generation": 0, "ticks": 2, "memory_sha256": "validly serialized", "disk_sha256": "validly serialized"}
        self.assertEqual(len(check_state(bad, "x", 0, 1)), 2)

    def test_api_real_useful_work_and_async_operations(self):
        fixture = PublicFixture(asynchronous=True)
        client = self.server(fixture)
        evidence = RecordingEvidence()
        harness = EngineHarness(client, evidence, "tp_bench", str(ROOT / "benchmarks/workload.py"), sys.executable)
        result = harness.create_trial(plan()[0], False)
        self.assertIsNone(result["error"])
        self.assertTrue(fixture.sandboxes["sb_0"]["deleted"])
        self.assertTrue(any(path.startswith("/v1/operations/") for _, path, _ in fixture.paths))
        self.assertTrue(any(path.endswith("/output") for _, path, _ in fixture.paths))
        self.assertFalse(any("worker" in path for _, path, _ in fixture.paths))

    def test_fork_oracle_detects_shared_writes_through_public_api(self):
        for shared in (False, True):
            with self.subTest(shared=shared):
                fixture = PublicFixture(shared=shared)
                client = self.server(fixture)
                harness = EngineHarness(client, RecordingEvidence(), "tp_bench", str(ROOT / "benchmarks/workload.py"), sys.executable)
                row = {**plan()[0], "dirty_mib": 0}
                source = harness.create(row)
                nonce = "memory-marker-not-from-snapshot"
                state = {"nonce": nonce, "generation": 0, "ticks": 1, "size": 0}
                state["memory_sha256"] = hashlib.sha256(b"").hexdigest()
                state["disk_sha256"] = hashlib.sha256(b"".join(blocks(nonce, 0, 16384, "disk"))).hexdigest()
                fixture.sandboxes[source]["state"] = state
                result = harness.fork_trial(source, row, "/tmp/fixture", nonce, False)
                self.assertEqual(result["error"] is not None, shared)
                if shared:
                    self.assertTrue(any("generation mismatch" in e for e in result["state_errors"]))

    def test_cli_adapter_uses_public_flags_and_real_workload(self):
        with tempfile.TemporaryDirectory() as root:
            executable = Path(root) / "lamp fixture"
            executable.write_text(
                "#!" + sys.executable + "\n"
                "import json,sys\n"
                "from pathlib import Path\n"
                "Path(__file__).with_suffix('.args').write_text(json.dumps(sys.argv[1:]))\n"
                "print(json.dumps({'result': {'sandbox_id': 'sb_cli'}}))\n"
            )
            executable.chmod(0o700)
            client = PublicLamp(cli=str(executable))
            result = client.mutation("sandbox", "fork", {"sandbox_id": "sb_source", "expected_revision": 18446744073709551615, "fork_mode": "memory", "durability": "local"})
            self.assertEqual(result["sandbox_id"], "sb_cli")
            args = json.loads(executable.with_suffix(".args").read_text())
            self.assertEqual(args[:3], ["sandbox", "fork", "sb_source"])
            self.assertIn("18446744073709551615", args)
            self.assertIn("--idempotency-key", args)
            self.assertIn("--json", args)

    def test_real_workload_compute_disk(self):
        with tempfile.TemporaryDirectory() as root:
            for name in ("compile", "compression", "crypto", "seq_read", "seq_write", "random_read", "random_write", "tree", "fsync"):
                with self.subTest(name=name):
                    self.assertGreater(measure(name, root)["value"], 0)
        self.assertIn("proxy", MEASURES)

    def test_real_network_fixture_every_measure_and_required_proxy(self):
        process = subprocess.Popen(
            [sys.executable, str(ROOT / "benchmarks/workload.py"), "serve-network"],
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
        )
        try:
            ports = json.loads(process.stdout.readline())
            fixture = {"host": "127.0.0.1", "tcp_port": ports["tcp_port"], "udp_port": ports["udp_port"],
                       "http_url": "http://127.0.0.1:" + str(ports["http_port"])}
            for name in ("tcp_goodput", "udp_goodput", "tcp_packets", "udp_packets", "request_latency", "small_flows"):
                with self.subTest(name=name):
                    self.assertGreater(network_measure(name, fixture, None)["value"], 0)
            for proxy in ("direct", fixture["http_url"]):
                self.assertGreater(network_measure("proxy", fixture, proxy)["value"], 0)
            with self.assertRaises(ValueError):
                network_measure("proxy", fixture, None)
        finally:
            process.terminate()
            process.communicate(timeout=5)

    def test_real_resident_memory_and_disk_generations(self):
        with tempfile.TemporaryDirectory(prefix="lamp-ram-") as root:
            path = str(Path(root) / "resident")
            process = subprocess.Popen(
                [sys.executable, str(ROOT / "benchmarks/workload.py"), "resident", "--path", path,
                 "--nonce", "outside-history", "--bytes", "8193"], stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            )
            try:
                from benchmarks.workload import call_resident
                import time
                deadline = time.monotonic() + 5
                while not Path(path).exists():
                    self.assertLess(time.monotonic(), deadline)
                    time.sleep(.01)
                before = call_resident(path, {"op": "state"})
                self.assertEqual(check_state(before, "outside-history", 0, 8193), [])
                after = call_resident(path, {"op": "write", "generation": 43})
                self.assertEqual(check_state(after, "outside-history", 43, 8193), [])
                self.assertGreater(after["ticks"], before["ticks"])
                with open(path + ".disk", "r+b") as disk:
                    disk.write(b"corrupt-but-validly-hashed")
                corrupted = call_resident(path, {"op": "state"})
                self.assertEqual(check_state(corrupted, "outside-history", 43, 8193), ["disk_sha256 mismatch"])
            finally:
                process.terminate()
                process.communicate(timeout=5)

    def test_fixture_partial_line_timeout_cannot_hang_runner(self):
        command = [sys.executable, "-c", "import sys,time;sys.stdout.write('{');sys.stdout.flush();time.sleep(10)"]
        request = {"nonce": "n", "point": "ack", "side": "after", "phases": COMMIT_POINTS}
        with self.assertRaises(TimeoutError):
            scenario(command, request, RecordingEvidence(), timeout=.1)

    def test_runner_process_writes_incomplete_model_evidence(self):
        with tempfile.TemporaryDirectory() as root:
            metadata = Path(root) / "metadata.json"
            metadata.write_text("{}")
            output = Path(root) / "evidence"
            process = subprocess.run(
                [sys.executable, "-m", "benchmarks.run", "--suite", "E04", "--model", "--smoke",
                 "--metadata", str(metadata), "--output", str(output)], cwd=ROOT, capture_output=True, text=True,
            )
            self.assertEqual(process.returncode, 1, process.stderr)
            summary = json.loads((output / "summary.json").read_text())
            self.assertEqual(summary["status"], "incomplete")
            self.assertEqual(summary["results"]["E04"]["failures"], [])
            raw = [json.loads(line) for line in (output / "observations.jsonl").read_text().splitlines()]
            self.assertTrue(any(item["kind"] == "external_ack" for item in raw))

    def test_external_history_crashes_and_omitted_flush_controls(self):
        evidence = RecordingEvidence()
        command = [sys.executable, str(ROOT / "benchmarks/block_fixture.py")]
        result = storage_suite(command, evidence, count=30, model=True)
        self.assertEqual(result["status"], "incomplete")
        self.assertEqual(result["failures"], [])
        self.assertEqual(len(result["crash_coverage"]), len(COMMIT_POINTS) * 2)
        self.assertTrue(all(row["caught"] for row in result["negative_controls"]))
        self.assertTrue(result["unauthorized_capture_caught"])
        self.assertTrue(any(kind == "external_ack" for kind, _ in evidence.events))

    def test_acknowledged_loss_ancestor_and_unauthorized_are_errors(self):
        ack = {"id": "1", "parent": "0", "nonce": "n", "generation": 1, "memory_bytes": 0, "disk_bytes": 0}
        state = {"nonce": "n", "generation": 1, "parent": "0", "producer": "bad", "ticks": 1,
                 "memory_sha256": hashlib.sha256(b"").hexdigest(), "disk_sha256": hashlib.sha256(b"").hexdigest()}
        errors = check_recovery({"objects": {"1": state}}, [ack], {"authorized"})
        self.assertTrue(any("lost ancestor" in e for e in errors))
        self.assertTrue(any("unauthorized" in e for e in errors))
        self.assertEqual(check_recovery({}, [ack], {"authorized"}), ["lost acknowledged object 1"])

    def test_evidence_preserves_failed_runs_and_rejects_overwrite(self):
        with tempfile.TemporaryDirectory() as root:
            path = Path(root) / "failed"
            evidence = Evidence(path, {}, {})
            evidence.observe("timeout", {"elapsed_ms": 1000})
            summary = evidence.finish({"E01": {"status": "fail"}})
            self.assertEqual(summary["status"], "fail")
            self.assertTrue(json.loads((path / "manifest.json").read_text())["metadata_errors"])
            with self.assertRaises(FileExistsError):
                Evidence(path, {}, {})
            observation = json.loads((path / "observations.jsonl").read_text())
            self.assertEqual(summary["last_observation_hash"], observation["sha256"])

    def test_checked_in_run_evidence_hash_chains_and_honest_status(self):
        root = ROOT / "tests/benchmarks/evidence"
        for name in ("model-1000", "unimplemented-cli"):
            with self.subTest(run=name):
                directory = root / name
                summary = json.loads((directory / "summary.json").read_text())
                manifest = json.loads((directory / "manifest.json").read_text())
                raw = directory / "observations.jsonl"
                content = raw.read_bytes() if raw.exists() else gzip.decompress(raw.with_suffix(".jsonl.gz").read_bytes())
                previous = "0" * 64
                observations = []
                for line in content.splitlines():
                    item = json.loads(line)
                    checksum = item.pop("sha256")
                    self.assertEqual(item["previous"], previous)
                    encoded = json.dumps(item, sort_keys=True, separators=(",", ":"), allow_nan=False).encode()
                    self.assertEqual(hashlib.sha256(encoded).hexdigest(), checksum)
                    previous = checksum
                    observations.append(item)
                self.assertEqual(summary["last_observation_hash"], previous)
                self.assertEqual(manifest["protocol"], "lamp.benchmark/v1")
                self.assertEqual(manifest["thresholds"]["fork_max_ms_exclusive"], 1000)
                if name == "model-1000":
                    self.assertEqual(summary["status"], "incomplete")
                    result = summary["results"]["E04"]
                    self.assertEqual(result["forks"], 1000)
                    self.assertEqual(result["failures"], [])
                    self.assertTrue(all(row["caught"] for row in result["negative_controls"]))
                    self.assertEqual(sum(item["kind"] == "storage_recovery" for item in observations), 1004)
                else:
                    self.assertEqual(summary["status"], "fail")
                    creates = [item for item in observations if item["kind"] == "create_trial"]
                    self.assertEqual(len(creates), 18)
                    self.assertTrue(all(item["value"]["error"] for item in creates))


if __name__ == "__main__":
    unittest.main()
