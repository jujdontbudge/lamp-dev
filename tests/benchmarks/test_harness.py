import base64
import copy
import gzip
import hashlib
import http.client
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
from unittest import mock
import urllib.parse
import urllib.request

from benchmarks.engine import EngineHarness
from benchmarks.evidence import Evidence, metadata_errors
from benchmarks.oracles import check_fork, check_recovery, check_state, pattern_hash
from benchmarks.protocol import COMMIT_POINTS, MEASURES, paired_summary, plan, timing
from benchmarks.public import PublicLamp
from benchmarks.storage import scenario, storage_suite
from benchmarks.workload import blocks, measure, network_measure


ROOT = Path(__file__).resolve().parents[2]


def valid_metadata():
    metadata = json.loads((ROOT / "tests/benchmarks/evidence/model-metadata.json").read_text())
    metadata["controls"] = {key: True for key in metadata["controls"]}
    metadata.update({
        "hardware": {"cpu_model": "fixture CPU", "logical_cpus": 2, "memory_bytes": 4294967296},
        "os": {"kernel": "Linux", "kernel_version": "6.8.0"},
        "filesystem": {"type": "ext4", "block_size_bytes": 4096},
        "storage": {"total_bytes": 21474836480, "free_bytes": 10737418240},
        "policy": {"revision": 1},
        "resources": {"vcpus": 2, "memory_mib": 4096, "disk_gib": 20, "pids_max": 4096},
        "power": {"ac_online": 1}, "thermal": {"samples_c": [0, 42.5]},
    })
    return metadata


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
        self.headers = []

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
                fixture.headers.append(dict(self.headers))
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

    def test_rest_mutation_preconditions_are_headers_not_body(self):
        fixture = PublicFixture()
        client = self.server(fixture)
        client.call("sandbox", "create", {
            "template_id": "tp_bench", "resources": plan()[0]["resources"],
            "durability": "local", "idempotency_key": "create-key",
        })
        client.call("sandbox", "fork", {
            "sandbox_id": "sb_0", "fork_mode": "memory", "durability": "local",
            "idempotency_key": "fork-key", "expected_revision": 18446744073709551615,
        })
        client.call("sandbox", "delete", {
            "sandbox_id": "sb_1", "idempotency_key": "delete-key", "expected_revision": 0,
        })
        for index, (key, revision) in enumerate((
            ("create-key", None), ("fork-key", "18446744073709551615"), ("delete-key", "0"),
        )):
            with self.subTest(mutation=index):
                headers = {name.lower(): value for name, value in fixture.headers[index].items()}
                self.assertEqual(headers.get("idempotency-key"), key)
                self.assertEqual(headers.get("if-match"), revision)
                fields = fixture.paths[index][2]
                self.assertNotIn("idempotency_key", fields)
                self.assertNotIn("expected_revision", fields)
        self.assertEqual(fixture.paths[0][2]["template_id"], "tp_bench")
        self.assertEqual(fixture.paths[1][2]["fork_mode"], "memory")

    def test_execute_drains_output_after_exit_and_deduplicates_final_pages(self):
        first = {"stream_id": "out", "sequence": 0, "channel": "stdout",
                 "data_base64": base64.b64encode(b'{"value":').decode()}
        last = {"stream_id": "out", "sequence": 1, "channel": "stdout",
                "data_base64": base64.b64encode(b'"complete"}').decode()}
        for initial in ([], [first]):
            with self.subTest(initial=initial):
                client = PublicLamp(url="http://127.0.0.1", timeout=1)
                responses = [
                    {"result": {"exec_id": "ex_1"}},
                    {"items": initial},
                    {"data": {"state": "exited", "exit_code": 0}},
                    {"items": [first], "next_cursor": "tail"},
                    {"items": [first, last]},
                ]
                with mock.patch.object(client, "call", side_effect=responses) as call:
                    self.assertEqual(client.execute("sb_1", ["useful"]), {"value": "complete"})
                self.assertEqual(call.call_args, mock.call("exec", "watch", {"exec_id": "ex_1", "cursor": "tail"}))

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

    def test_fork_trial_rejects_one_tick_child_rollback(self):
        class StaleChild(PublicFixture):
            def handle(self, method, path, fields):
                result = super().handle(method, path, fields)
                if path.endswith(":fork"):
                    self.sandboxes[result["result"]["sandbox_id"]]["state"]["ticks"] -= 1
                return result

        fixture = StaleChild()
        client = self.server(fixture)
        evidence = RecordingEvidence()
        harness = EngineHarness(client, evidence, "tp_bench", str(ROOT / "benchmarks/workload.py"), sys.executable)
        row = plan()[0]
        source = harness.create(row)
        fixture.sandboxes[source]["state"] = {
            "nonce": "marker", "generation": 0, "ticks": 100, "size": 0,
            "memory_sha256": hashlib.sha256(b"").hexdigest(),
            "disk_sha256": hashlib.sha256(b"".join(blocks("marker", 0, 16384, "disk"))).hexdigest(),
        }
        result = harness.fork_trial(source, row, "/tmp/fixture", "marker", False)
        self.assertIn("child did not continue", result["error"] or "")
        self.assertEqual(result["state_errors"], [])
        self.assertFalse(any(kind == "fork_oracle" for kind, _ in evidence.events))

    def test_check_fork_rejects_one_tick_child_rollback(self):
        parent = {
            "nonce": "marker", "generation": 1, "ticks": 101,
            "memory_sha256": hashlib.sha256(b"").hexdigest(),
            "disk_sha256": hashlib.sha256(b"".join(blocks("marker", 1, 16384, "disk"))).hexdigest(),
        }
        child = {
            "nonce": "marker", "generation": 2,
            "memory_sha256": hashlib.sha256(b"").hexdigest(),
            "disk_sha256": hashlib.sha256(b"".join(blocks("marker", 2, 16384, "disk"))).hexdigest(),
        }
        self.assertIn("child did not continue", check_fork({"ticks": 100}, parent, {**child, "ticks": 100}, "marker", 0))
        self.assertEqual(check_fork({"ticks": 100}, parent, {**child, "ticks": 101}, "marker", 0), [])

    def test_recovery_validates_unacknowledged_objects_against_external_writes(self):
        written = [
            {"id": str(generation), "parent": None if generation == 0 else "0",
             "nonce": "outside", "generation": generation, "memory_bytes": 8192, "disk_bytes": 16384}
            for generation in (0, 1)
        ]
        objects = {
            item["id"]: {
                "parent": item["parent"], "producer": "authorized", "ticks": 1,
                "nonce": item["nonce"], "generation": item["generation"],
                "memory_sha256": hashlib.sha256(b"".join(blocks("outside", item["generation"], 8192, "ram"))).hexdigest(),
                "disk_sha256": hashlib.sha256(b"".join(blocks("outside", item["generation"], 16384, "disk"))).hexdigest(),
            } for item in written
        }
        mixed = copy.deepcopy(objects)
        mixed["1"]["memory_sha256"] = objects["0"]["memory_sha256"]
        self.assertTrue(check_recovery({"objects": mixed}, written[:1], {"authorized"}))
        self.assertEqual(check_recovery({"objects": objects}, written[:1], {"authorized"}, written=written), [])
        for field, value in (
            ("memory_sha256", objects["0"]["memory_sha256"]),
            ("disk_sha256", objects["0"]["disk_sha256"]),
            ("nonce", "self-reported"), ("generation", 77),
        ):
            with self.subTest(field=field):
                corrupted = copy.deepcopy(objects)
                corrupted["1"][field] = value
                errors = check_recovery({"objects": corrupted}, written[:1], {"authorized"}, written=written)
                self.assertIn("1: " + field + " mismatch", errors)
        self.assertEqual(check_recovery({"objects": {"0": objects["0"]}}, written[:1], {"authorized"}, written=written), [])

    def test_placeholder_metadata_cannot_produce_passing_evidence(self):
        metadata = valid_metadata()
        self.assertEqual(metadata_errors(metadata), [])
        for key in ("hardware", "filesystem", "storage", "policy", "resources", "power", "thermal"):
            for value in ({}, {"unavailable": True}, {"unavailable": "sensor offline"},
                          {"status": "unavailable"}, {"status": "placeholder"}, {"samples": []}):
                with self.subTest(field=key, value=value):
                    self.assertIn(key + " missing", metadata_errors({**metadata, key: value}))
        for value in ("placeholder", ["placeholder"], ["unavailable"], [], "unavailable"):
            with self.subTest(features=value):
                self.assertIn("compiled_features missing", metadata_errors({**metadata, "compiled_features": value}))
        with tempfile.TemporaryDirectory() as root:
            evidence = Evidence(Path(root) / "missing", {**metadata, "thermal": {"unavailable": True}}, {})
            self.assertEqual(evidence.finish({"E01": {"status": "pass"}})["status"], "incomplete")

    def test_unavailable_or_unsampled_metadata_cannot_pass(self):
        metadata = valid_metadata()
        self.assertEqual(metadata_errors(metadata), [])
        records = (
            {"unavailable": True, "reason": "sensor offline"},
            {"sampled": False},
            {"unavailable": False, "samples_c": [42]},
            {"placeholder": False, "samples_c": [42]},
            {"sampled": False, "samples_c": [42]},
            {"status": "placeholder", "samples_c": [42]},
            {"reason": "sensor offline"}, {"description": "sensor offline"},
            {"present": False},
        )
        for key in ("hardware", "filesystem", "storage", "policy", "resources", "power", "thermal"):
            for record in records:
                with self.subTest(field=key, record=record):
                    self.assertIn(key + " missing", metadata_errors({**metadata, key: record}))
        for record in records[:2]:
            with self.subTest(evidence=record), tempfile.TemporaryDirectory() as root:
                evidence = Evidence(Path(root) / "missing", {**metadata, "thermal": record}, {})
                self.assertEqual(evidence.finish({"E01": {"status": "pass"}})["status"], "incomplete")

    def test_metadata_requires_typed_measurements_not_free_text(self):
        metadata = valid_metadata()
        self.assertEqual(metadata_errors(metadata), [])
        for record in ({"samples_c": ["sensor offline"]}, {"value": "sensor offline"}):
            with self.subTest(record=record), tempfile.TemporaryDirectory() as root:
                evidence = Evidence(Path(root) / "invalid", {**metadata, "thermal": record}, {})
                self.assertEqual(evidence.finish({"E01": {"status": "pass"}})["status"], "incomplete")
        for section in ("hardware", "os", "filesystem", "storage", "policy", "resources", "power", "thermal"):
            for field in metadata[section]:
                if section == "os" and field == "kernel":
                    continue
                for value in (None, [], True, "sensor offline"):
                    if value == "sensor offline" and field in ("cpu_model", "kernel_version", "type"):
                        continue
                    invalid = copy.deepcopy(metadata)
                    invalid[section][field] = value
                    self.assertTrue(metadata_errors(invalid), (section, field, value))
        for samples in ([], [True], ["42"], [float("inf")], [float("nan")]):
            self.assertTrue(metadata_errors({**metadata, "thermal": {"samples_c": samples}}))
        with tempfile.TemporaryDirectory() as root:
            evidence = Evidence(Path(root) / "valid", metadata, {})
            self.assertEqual(evidence.finish({"E01": {"status": "pass"}})["status"], "pass")

    def test_real_e04_self_attestation_without_harness_traces_is_incomplete(self):
        def self_attested(command, request, evidence):
            observed = {
                "qualification": "real-filesystem-block-barrier",
                "public_call_transcript": ["fixture claims a public call"],
                "injected_fault_barrier_trace": ["fixture claims a barrier"],
            }
            if request.get("omit"):
                return ["lost acknowledged object 1"], observed
            if request.get("unauthorized"):
                observed.update({"unauthorized_capture_rejected": True, "objects": {"0": {}}})
            return [], observed

        with mock.patch("benchmarks.storage.scenario", side_effect=self_attested):
            result = storage_suite(["self-attesting-fixture"], RecordingEvidence(), count=1000)
        self.assertEqual(result["failures"], [])
        self.assertEqual(result["status"], "incomplete")
        self.assertEqual(set(result["missing_qualification_evidence"]), {"public_call_transcript", "injected_fault_barrier_trace"})

    def test_e02_rejects_direct_and_invalid_secured_proxy_endpoints(self):
        harness = EngineHarness(
            mock.Mock(), RecordingEvidence(), "tp_bench", "/workload.py",
            directory="/private", fixture={"host": "fixture"}, native_cpus=[0, 1],
        )
        harness.create = mock.Mock(side_effect=AssertionError("sandbox admission reached"))
        for proxy in ("direct", "relative", "ftp://proxy", "http://", "http:///proxy",
                      "http://proxy:bad", "http://proxy:0", "http://proxy:65536",
                      "http://user:password@proxy", "http://proxy?bypass=true", "http://proxy/#bypass"):
            with self.subTest(proxy=proxy):
                harness.proxy = proxy
                with self.assertRaises(ValueError):
                    harness.e02(plan()[3])
                harness.create.assert_not_called()
        for proxy in ("http://127.0.0.1:8080", "https://proxy.example"):
            with self.subTest(valid_proxy=proxy):
                harness.proxy = proxy
                with self.assertRaisesRegex(AssertionError, "sandbox admission reached"):
                    harness.e02(plan()[3])

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
            self.assertGreater(network_measure("proxy", fixture, "direct")["value"], 0)
            with self.assertRaises(ValueError):
                network_measure("proxy", fixture, fixture["http_url"])
            with self.assertRaises(ValueError):
                network_measure("proxy", fixture, None)
        finally:
            process.terminate()
            process.communicate(timeout=5)

    def network_fixture(self):
        process = subprocess.Popen(
            [sys.executable, str(ROOT / "benchmarks/workload.py"), "serve-network"],
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
        )
        def stop():
            process.terminate()
            process.communicate(timeout=5)
        self.addCleanup(stop)
        ports = json.loads(process.stdout.readline())
        return {"host": "127.0.0.1", "tcp_port": ports["tcp_port"], "udp_port": ports["udp_port"],
                "http_url": "http://127.0.0.1:" + str(ports["http_port"])}

    def relay(self, mode):
        class Relay(BaseHTTPRequestHandler):
            def do_POST(self):
                body = self.rfile.read(int(self.headers["Content-Length"]))
                self.server.received.append(dict(self.headers))
                original = body
                if mode != "echo" and not (mode == "partial" and len(self.server.received) == 64):
                    target = urllib.parse.urlsplit(self.path)
                    connection = http.client.HTTPConnection(target.hostname, target.port, timeout=5)
                    try:
                        headers = {key: value for key, value in self.headers.items() if key.lower().startswith("x-lamp-")}
                        sent = b"" if mode.startswith("empty") else b"x" * len(body) if mode == "altered" else body
                        connection.request("POST", target.path or "/", sent, headers)
                        response = connection.getresponse()
                        self.send_response(response.status)
                        body = response.read()
                        if mode == "empty_echo":
                            body = original
                        if mode == "forged" and len(self.server.received) == 1:
                            body = json.dumps({"receipt": "a" * 32}).encode()
                    finally:
                        connection.close()
                else:
                    self.send_response(200)
                    if mode == "partial":
                        body = json.dumps({"receipt": "a" * 32}).encode()
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, *args):
                pass

        server = ThreadingHTTPServer(("127.0.0.1", 0), Relay)
        server.received = []
        threading.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        return "http://127.0.0.1:" + str(server.server_port), server

    def test_proxy_receipts_bind_origin_response_and_request_body(self):
        fixture = {**self.network_fixture(), "run_id": "host-selected-run"}
        harness = EngineHarness(mock.Mock(timeout=5), RecordingEvidence(), "tp", "/workload", fixture=fixture)
        for mode in ("forward", "echo", "empty_echo", "empty", "partial", "altered", "forged"):
            with self.subTest(mode=mode):
                proxy, server = self.relay(mode)
                if mode == "forward":
                    result = network_measure("proxy", fixture, proxy)
                    harness.verify_proxy(result, fixture["run_id"])
                    self.assertEqual(len(set(result["origin_receipts"].values())), 64)
                    self.assertEqual(len(server.received), 64)
                else:
                    with self.assertRaises(ValueError):
                        result = network_measure("proxy", fixture, proxy)
                        harness.verify_proxy(result, fixture["run_id"])

    def test_proxy_only_guest_leaves_ledger_checks_to_host_for_both_sides(self):
        fixture = self.network_fixture()
        proxy, _ = self.relay("forward")
        evidence = RecordingEvidence()
        harness = EngineHarness(mock.Mock(timeout=5), evidence, "tp", "/workload", directory="/private",
                                fixture=fixture, proxy=proxy, native_cpus=[0, 1])
        harness.create = mock.Mock(return_value="sb")
        harness.delete = mock.Mock()
        harness.verify_configuration = mock.Mock()
        harness.verify_artifact = mock.Mock(return_value={"identity": "same"})
        harness.observe_resources = mock.Mock(return_value={})
        def native(command, **kwargs):
            if command == "identity":
                return {"identity": "same"}
            opener = urllib.request.build_opener
            def post_only(*args):
                client = opener(*args)
                original_open = client.open
                def open_request(request, **options):
                    self.assertEqual(request.get_method(), "POST")
                    return original_open(request, **options)
                client.open = open_request
                return client
            with mock.patch("benchmarks.workload.urllib.request.build_opener", side_effect=post_only):
                return network_measure("proxy", kwargs["fixture"], kwargs["proxy"])
        def secured(sandbox, command, **kwargs):
            with mock.patch("benchmarks.workload.urllib.request.build_opener", side_effect=AssertionError("guest direct HTTP forbidden")):
                return network_measure("proxy", kwargs["fixture"], kwargs["proxy"])
        harness.native, harness.command = native, secured
        with mock.patch("benchmarks.engine.MEASURES", {"proxy": "latency"}), mock.patch.object(harness, "verify_proxy", wraps=harness.verify_proxy) as verify:
            result = harness.e02(plan()[3], smoke=True)
        self.assertEqual(result["measures"]["proxy"]["pairs"], 1)
        self.assertEqual(verify.call_count, 2)
        pairs = [value for kind, value in evidence.events if kind == "native_pair"]
        self.assertNotIn("error", pairs[0])
        for side in ("native", "secured"):
            self.assertEqual(pairs[0][side + "_raw"]["origin_requests"], 64)

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
