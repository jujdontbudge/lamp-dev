import hashlib
import json
from pathlib import Path
import random
import re
import subprocess
import sys
import tempfile
import time
import urllib.parse
import urllib.request
import uuid

from benchmarks.oracles import check_fork, check_state
from benchmarks.protocol import CREATES, FORKS, MEASURES, PAIRS, WARMUPS, paired_summary, plan, timing


class EngineHarness:
    def __init__(self, lamp, evidence, template, workload, python="python3", native_python=sys.executable, directory=None, fixture=None, proxy=None, observer=None, native_cpus=None):
        self.lamp, self.evidence, self.template = lamp, evidence, template
        self.workload, self.python, self.native_python = workload, python, native_python
        self.directory, self.fixture, self.proxy = directory, fixture, proxy
        self.observer = observer
        self.native_cpus = native_cpus
        self.cleanup_errors = []
        self.incomplete_observations = []

    def observe_resources(self, operation, identity, source=None):
        observation = {"source_pause_ms": None, "rss_bytes": None, "disk_bytes": None, "pressure": None}
        if self.observer:
            try:
                observation = json.loads(subprocess.check_output(
                    self.observer, input=json.dumps({"operation": operation, "sandbox_id": identity, "source_sandbox_id": source}).encode(),
                    timeout=self.lamp.timeout,
                ))
            except Exception as error:
                observation["error"] = type(error).__name__
        required = ("rss_bytes", "disk_bytes", "pressure") + (("source_pause_ms",) if operation == "fork" else ())
        if any(observation.get(key) is None for key in required):
            self.incomplete_observations.append({"operation": operation, "sandbox_id": identity})
        return observation

    def verify_configuration(self, identity, row):
        observed = self.lamp.get(identity)
        self.evidence.observe("sandbox_configuration", observed)
        if observed.get("resources") != row["resources"] or observed.get("durability") != row["durability"]:
            raise ValueError("public sandbox resources/durability differ from requested row")
        if observed.get("boundary") != "vm" or observed.get("warm_pool_id"):
            raise ValueError("binding prepared-template row requires VM with no warm assignment")

    def command(self, sandbox, command, **kwargs):
        argv = [self.python, self.workload, command]
        for key, value in kwargs.items():
            if value is not None:
                argv.extend(["--" + key.replace("_", "-"), json.dumps(value) if isinstance(value, dict) else str(value)])
        return self.lamp.execute(sandbox, argv)

    def create(self, row):
        return self.lamp.mutation("sandbox", "create", {
            "template_id": self.template, "resources": row["resources"], "durability": row["durability"],
        })["sandbox_id"]

    def delete(self, identity):
        if identity:
            try:
                self.lamp.delete(identity)
            except Exception as error:
                item = {"sandbox_id": identity, "error": type(error).__name__ + ": " + str(error)}
                self.cleanup_errors.append(item)
                self.evidence.observe("cleanup_failure", item)

    def useful(self, identity):
        nonce = uuid.uuid4().hex
        answer = self.command(identity, "useful", nonce=nonce)
        if answer != {"nonce": nonce, "answer": hashlib.sha256(nonce.encode()).hexdigest()}:
            raise ValueError("useful command witness mismatch")

    def verify_artifact(self, identity):
        observed = self.command(identity, "identity")
        expected = hashlib.sha256(Path(__file__).with_name("workload.py").read_bytes()).hexdigest()
        if observed["sha256"] != expected:
            raise ValueError("guest workload digest differs from frozen suite")
        self.evidence.observe("workload_identity", observed)
        return observed

    def create_trial(self, row, warmup):
        identity, error = None, None
        started = time.monotonic_ns()
        try:
            identity = self.create(row)
            self.useful(identity)
        except Exception as caught:
            error = type(caught).__name__ + ": " + str(caught)
        elapsed = (time.monotonic_ns() - started) / 1e6
        if identity and error is None:
            try:
                self.verify_configuration(identity, row)
            except Exception as caught:
                error = type(caught).__name__ + ": " + str(caught)
        value = {"warmup": warmup, "elapsed_ms": elapsed, "error": error, "sandbox_id": identity, "row": row}
        value["resources"] = self.observe_resources("create", identity)
        self.evidence.observe("create_trial", value)
        self.delete(identity)
        return value

    def fork_trial(self, source, row, path, nonce, warmup):
        identity, error, state_errors = None, None, []
        before = self.command(source, "call", path=path, request={"op": "ping"})
        revision = self.lamp.get(source)["revision"]
        started = time.monotonic_ns()
        try:
            identity = self.lamp.mutation("sandbox", "fork", {
                "sandbox_id": source, "expected_revision": revision,
                "fork_mode": "memory", "durability": row["durability"],
            })["sandbox_id"]
            parent = self.command(source, "call", path=path, request={"op": "ping"})
            child = self.command(identity, "call", path=path, request={"op": "ping"})
            for value in (parent, child):
                if value.get("nonce") != nonce or value.get("generation") != 0:
                    raise ValueError("captured RAM marker discontinuity")
            if parent["ticks"] <= before["ticks"]:
                raise ValueError("source did not continue")
            if child.get("ticks", 0) <= before["ticks"]:
                raise ValueError("child did not continue")
        except Exception as caught:
            error = type(caught).__name__ + ": " + str(caught)
        elapsed = (time.monotonic_ns() - started) / 1e6
        if identity and error is None:
            try:
                self.verify_configuration(identity, row)
                size = row["dirty_mib"] * 1024 * 1024
                for sandbox in (source, identity):
                    captured = self.command(sandbox, "call", path=path, request={"op": "state"})
                    state_errors += check_state(captured, nonce, 0, size)
                self.command(source, "call", path=path, request={"op": "write", "generation": 1})
                self.command(identity, "call", path=path, request={"op": "write", "generation": 2})
                parent = self.command(source, "call", path=path, request={"op": "state"})
                child = self.command(identity, "call", path=path, request={"op": "state"})
                state_errors += check_fork(before, parent, child, nonce, size)
                self.evidence.observe("fork_oracle", {"parent": parent, "child": child, "errors": state_errors})
            except Exception as caught:
                state_errors.append(type(caught).__name__ + ": " + str(caught))
            finally:
                try:
                    self.command(source, "call", path=path, request={"op": "write", "generation": 0})
                except Exception as caught:
                    state_errors.append("source reset: " + type(caught).__name__)
        if state_errors:
            error = "; ".join(state_errors)
        value = {"warmup": warmup, "elapsed_ms": elapsed, "error": error, "state_errors": state_errors,
                 "sandbox_id": identity, "source_sandbox_id": source, "row": row}
        value["resources"] = self.observe_resources("fork", identity, source)
        self.evidence.observe("fork_trial", value)
        self.delete(identity)
        return value

    def e01(self, extra_rows=(), smoke=False):
        results = {}
        rows = plan(extra_rows)
        failed_warmups = False
        for row in rows:
            creates = [self.create_trial(row, index < WARMUPS and not smoke) for index in range(2 if smoke else WARMUPS + CREATES)]
            failed_warmups |= any(event["error"] for event in creates if event["warmup"])
            results[row["id"] + "-create"] = timing(creates, CREATES, 500)
            source = None
            forks = []
            try:
                source = self.create(row)
                self.verify_artifact(source)
                nonce = uuid.uuid4().hex
                path = str(Path(self.directory or "/tmp") / ("lamp-marker-" + nonce))
                self.command(source, "start", path=path, nonce=nonce, bytes=row["dirty_mib"] * 1024 * 1024)
                for index in range(2 if smoke else WARMUPS + FORKS):
                    try:
                        forks.append(self.fork_trial(source, row, path, nonce, index < WARMUPS and not smoke))
                    except Exception as caught:
                        value = {"warmup": index < WARMUPS and not smoke, "elapsed_ms": self.lamp.timeout * 1000,
                                 "error": type(caught).__name__ + ": " + str(caught), "row": row}
                        forks.append(value)
                        self.evidence.observe("fork_trial", value)
            except Exception as error:
                self.evidence.observe("row_setup_failure", {"row": row, "error": type(error).__name__ + ": " + str(error)})
            finally:
                self.delete(source)
            results[row["id"] + "-fork"] = timing(forks, FORKS, 1000, every=True)
            failed_warmups |= any(event["error"] for event in forks if event["warmup"])
            if not row["hard_timing"]:
                for suffix in ("-create", "-fork"):
                    result = results[row["id"] + suffix]
                    result["timing_binding"] = False
                    result["status"] = "pass" if result["trials"] >= result["required"] and result["failures"] == 0 else "fail"
        failed = any(r["status"] != "pass" for r in results.values()) or bool(self.cleanup_errors) or failed_warmups
        return {"status": "fail" if failed else "incomplete" if self.incomplete_observations else "pass",
                "rows": results, "cleanup_errors": self.cleanup_errors, "missing_observations": self.incomplete_observations}

    def native(self, command, **kwargs):
        argv = [self.native_python, str(Path(__file__).with_name("workload.py")), command]
        if self.native_cpus:
            argv.extend(["--cpus", ",".join(map(str, self.native_cpus))])
        for key, value in kwargs.items():
            if value is not None:
                argv.extend(["--" + key.replace("_", "-"), json.dumps(value) if isinstance(value, dict) else str(value)])
        return json.loads(subprocess.check_output(argv, timeout=self.lamp.timeout))

    def e02(self, row, smoke=False):
        if not self.directory or not self.fixture or not self.proxy:
            raise ValueError("E02 needs an explicit private disk directory, remote network fixture and secured proxy path")
        proxy = urllib.parse.urlsplit(self.proxy)
        if (proxy.scheme not in ("http", "https") or not proxy.hostname or
                proxy.username is not None or proxy.password is not None or
                proxy.query or proxy.fragment or proxy.path not in ("", "/") or
                any(character.isspace() for character in self.proxy) or
                (proxy.port is not None and not 0 < proxy.port < 65536)):
            raise ValueError("E02 secured side requires a valid HTTP(S) proxy endpoint")
        if not smoke and (not self.native_cpus or len(self.native_cpus) != row["resources"]["vcpus"]):
            raise ValueError("E02 needs matched native CPU affinity")
        source = self.create(row)
        results, pairs = {}, {}
        rng = random.Random(2201)
        try:
            guest_identity = self.verify_artifact(source)
            self.verify_configuration(source, row)
            native_identity = self.native("identity")
            self.evidence.observe("native_workload_identity", native_identity)
            if native_identity != guest_identity:
                raise ValueError("native and secured interpreter/compiler/workload identities differ")
            for measure, kind in MEASURES.items():
                pairs[measure] = []
                for index in range(1 if smoke else PAIRS):
                    order = ["native", "secured"]
                    rng.shuffle(order)
                    pair = {"measure": measure, "index": index, "order": order}
                    child = None
                    try:
                        for side in order:
                            if measure == "cow_first_write":
                                path = str(Path(self.directory) / ("lamp-cow-" + uuid.uuid4().hex))
                                if side == "native":
                                    with tempfile.TemporaryDirectory(dir=self.directory, prefix="lamp-native-cow-") as root:
                                        path = str(Path(root) / "head")
                                        self.native("cow-prepare", path=path)
                                        value = self.native("cow-write", path=path)
                                else:
                                    self.command(source, "cow-prepare", path=path)
                                    revision = self.lamp.get(source)["revision"]
                                    child = self.lamp.mutation("sandbox", "fork", {
                                        "sandbox_id": source, "fork_mode": "memory", "durability": row["durability"], "expected_revision": revision,
                                    })["sandbox_id"]
                                    value = self.command(child, "cow-write", path=path)
                                    self.command(source, "cow-check", path=path)
                                    self.command(source, "cow-remove", path=path)
                            else:
                                arguments = {"measure": measure, "directory": self.directory, "fixture": self.fixture,
                                             "proxy": self.proxy if side == "secured" else "direct"}
                                if measure == "proxy":
                                    arguments["fixture"] = {**self.fixture, "run_id": uuid.uuid4().hex}
                                value = self.native("measure", **arguments) if side == "native" else self.command(source, "measure", **arguments)
                                if measure == "proxy":
                                    self.verify_proxy(value, arguments["fixture"]["run_id"])
                            pair[side] = value["value"]
                            pair[side + "_raw"] = value
                        pairs[measure].append(pair)
                    except Exception as error:
                        pair["error"] = type(error).__name__ + ": " + str(error)
                    finally:
                        pair["resources"] = self.observe_resources("measure", source)
                        self.delete(child)
                        self.evidence.observe("native_pair", pair)
                result = paired_summary(pairs[measure], kind)
                if len(pairs[measure]) < (1 if smoke else PAIRS):
                    result["status"] = "fail"
                results[measure] = result
        finally:
            self.delete(source)
        passed = len(results) == len(MEASURES) and all(r["status"] == "pass" for r in results.values()) and not self.cleanup_errors
        return {"status": "fail" if not passed else "incomplete" if self.incomplete_observations else "pass",
                "measures": results, "cleanup_errors": self.cleanup_errors, "missing_observations": self.incomplete_observations}

    def verify_proxy(self, value, run_id):
        received = value.get("origin_receipts")
        if (value.get("origin_run_id") != run_id or not isinstance(received, dict) or
                set(received) != {str(index) for index in range(64)} or
                any(not isinstance(token, str) or not re.fullmatch("[0-9a-f]{32}", token) for token in received.values()) or
                len(set(received.values())) != 64):
            raise ValueError("proxy response receipt mismatch")
        target = urllib.parse.urlsplit(self.fixture["http_url"])
        query = urllib.parse.parse_qsl(target.query) + [("lamp_run", run_id)]
        url = urllib.parse.urlunsplit(target._replace(query=urllib.parse.urlencode(query)))
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        with opener.open(url, timeout=self.lamp.timeout) as response:
            ledger = json.load(response)
        self.evidence.observe("proxy_origin_ledger", {"run_id": run_id, "ledger": ledger})
        payload = hashlib.sha256(b"lamp-network-v1").digest()
        expected = {
            index: {"receipt": token, "body_sha256": hashlib.sha256(payload).hexdigest(), "body_bytes": len(payload)}
            for index, token in received.items()
        }
        if ledger != {"run_id": run_id, "requests": expected}:
            raise ValueError("proxy origin receipt mismatch")
        value["origin_requests"] = len(received)
