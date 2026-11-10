import datetime
import hashlib
import json
import math
import os
from pathlib import Path
import platform
import re
import subprocess
import time

from benchmarks.protocol import PROTOCOL, canonical, digest


REQUIRED_METADATA = {
    "source_hashes": dict, "dependency_hashes": dict, "compiled_features": list,
    "hardware": dict, "os": dict, "filesystem": dict, "storage": dict,
    "policy": dict, "resources": dict, "durability": str, "power": dict,
    "thermal": dict, "lane": str, "topology": str, "controls": dict,
    "artifacts": dict, "access_route": str,
}

OBSERVATION_FIELDS = {
    "hardware": {"cpu_model": str, "logical_cpus": int, "memory_bytes": int},
    "os": {"kernel_version": str},
    "filesystem": {"type": str, "block_size_bytes": int},
    "storage": {"total_bytes": int, "free_bytes": int},
    "policy": {"revision": int},
    "resources": {"vcpus": int, "memory_mib": int, "disk_gib": int, "pids_max": int},
    "power": {"ac_online": int},
    "thermal": {"samples_c": list},
}


def metadata_errors(metadata):
    def unavailable(value):
        if isinstance(value, dict):
            fields = {key.lower(): item for key, item in value.items()}
            return bool({"unavailable", "placeholder"} & fields.keys()) or fields.get("sampled") is False or any(unavailable(item) for item in fields.values())
        if isinstance(value, list):
            return any(unavailable(item) for item in value)
        return isinstance(value, str) and value.strip().lower() in ("unavailable", "placeholder", "unknown", "n/a")

    def typed(value, kind):
        if type(value) is not kind:
            return False
        if kind is list:
            return bool(value) and all(type(sample) in (int, float) and math.isfinite(sample) for sample in value)
        return kind is not str or bool(value.strip())

    errors = []
    for key, kind in REQUIRED_METADATA.items():
        value = metadata.get(key)
        if type(value) is not kind or not value or (kind is str and not value.strip()) or unavailable(value):
            errors.append(key + " missing")
    features = metadata.get("compiled_features")
    if isinstance(features, list) and any(not typed(feature, str) for feature in features):
        errors.append("compiled_features missing")
    for key, choices in (
        ("durability", ("volatile", "local", "replicated", "backup")),
        ("lane", ("LX", "LX22", "D12", "F40", "L510", "LA", "M5", "AIR", "NV")),
        ("topology", ("native", "nested")),
    ):
        if metadata.get(key) not in choices:
            errors.append(key + " invalid")
    for key, fields in OBSERVATION_FIELDS.items():
        record = metadata.get(key)
        if not isinstance(record, dict) or any(not typed(record.get(field), kind) for field, kind in fields.items()):
            errors.append(key + " missing")
    for key in ("guard", "audit", "cow", "capture_tracking"):
        controls = metadata.get("controls", {})
        if not isinstance(controls, dict) or controls.get(key) is not True:
            errors.append(key + " is not enabled")
    for key in ("source_hashes", "dependency_hashes", "artifacts"):
        values = metadata.get(key)
        if not isinstance(values, dict) or not values or any(not typed(name, str) or not isinstance(value, str) or not re.fullmatch("[0-9a-f]{64}", value) for name, value in values.items()):
            errors.append(key + " must pin SHA-256 digests")
    return errors


def local_observation():
    def command(argv):
        try:
            return subprocess.check_output(argv, stderr=subprocess.STDOUT, timeout=5).decode().strip()
        except (OSError, subprocess.SubprocessError) as error:
            return {"unavailable": type(error).__name__}
    observation = {
        "platform": platform.platform(), "machine": platform.machine(),
        "cpu_count": os.cpu_count(), "load": os.getloadavg(),
        "disk": command(["df", "-k", "."]),
        "power": command(["pmset", "-g", "batt"]) if platform.system() == "Darwin" else command(["sh", "-c", "cat /sys/devices/system/cpu/cpu0/cpufreq/scaling_governor"]),
        "pressure": {},
    }
    for name in ("cpu", "memory", "io"):
        path = Path("/proc/pressure") / name
        observation["pressure"][name] = path.read_text() if path.exists() else "unavailable"
    return observation


class Evidence:
    def __init__(self, root, metadata, configuration):
        self.root = Path(root)
        self.root.mkdir(parents=True, exist_ok=False)
        self.raw = (self.root / "observations.jsonl").open("x")
        self.previous = "0" * 64
        self.metadata = metadata
        self.configuration = configuration
        sources = {}
        for path in sorted(Path(__file__).parent.glob("*.py")):
            sources[path.name] = hashlib.sha256(path.read_bytes()).hexdigest()
        self.manifest = {
            "protocol": PROTOCOL, "started_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
            "metadata": metadata, "configuration": configuration,
            "harness_sources": sources, "suite_digest": digest(sources),
            "metadata_errors": metadata_errors(metadata), "caller": local_observation(),
            "thresholds": {"create_p50_ms_exclusive": 500, "fork_max_ms_exclusive": 1000,
                           "native_ratio_inclusive": .97, "warmups": 20, "creates": 200,
                           "forks_per_row": 100, "native_pairs_per_measure": 10, "storage_forks": 1000,
                           "control_callers": 32, "control_samples_per_caller": 200, "control_p99_ms_inclusive": 35},
        }
        self.write("manifest.json", self.manifest)

    def write(self, name, value):
        with (self.root / name).open("x") as output:
            json.dump(value, output, sort_keys=True, indent=2, allow_nan=False)
            output.write("\n")
            output.flush()
            os.fsync(output.fileno())

    def observe(self, kind, value):
        item = {"kind": kind, "value": value, "monotonic_ns": time.monotonic_ns(), "previous": self.previous}
        item["sha256"] = digest(item)
        self.previous = item["sha256"]
        self.raw.write(canonical(item).decode() + "\n")
        self.raw.flush()
        os.fsync(self.raw.fileno())

    def finish(self, results, diagnostic=False):
        incomplete = bool(self.manifest["metadata_errors"]) or diagnostic
        failed = any(result["status"] == "fail" for result in results.values())
        status = "fail" if failed else "incomplete" if incomplete or any(r["status"] != "pass" for r in results.values()) else "pass"
        summary = {
            "status": status, "diagnostic": diagnostic, "results": results,
            "last_observation_hash": self.previous, "caller_after": local_observation(),
            "completed_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        }
        self.write("summary.json", summary)
        self.raw.close()
        return summary
