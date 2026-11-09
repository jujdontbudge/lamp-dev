import datetime
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import subprocess
import time

from benchmarks.protocol import PROTOCOL, canonical, digest


REQUIRED_METADATA = (
    "source_hashes", "dependency_hashes", "compiled_features", "hardware", "os",
    "filesystem", "storage", "policy", "resources", "durability", "power",
    "thermal", "lane", "topology", "controls", "artifacts", "access_route",
)


def metadata_errors(metadata):
    def available(value):
        if isinstance(value, str):
            return value.strip().lower() not in ("", "unavailable", "placeholder", "unknown", "n/a")
        if isinstance(value, dict):
            return any(available(item) for key, item in value.items() if key.lower() not in ("unavailable", "placeholder"))
        if isinstance(value, list):
            return any(available(item) for item in value)
        return value is not None

    errors = [key + " missing" for key in REQUIRED_METADATA if not available(metadata.get(key))]
    for key in ("guard", "audit", "cow", "capture_tracking"):
        controls = metadata.get("controls", {})
        if not isinstance(controls, dict) or controls.get(key) is not True:
            errors.append(key + " is not enabled")
    for key in ("source_hashes", "dependency_hashes", "artifacts"):
        values = metadata.get(key)
        if not isinstance(values, dict) or not values or any(not isinstance(value, str) or not re.fullmatch("[0-9a-f]{64}", value) for value in values.values()):
            errors.append(key + " must pin SHA-256 digests")
    for key in ("hardware", "os", "filesystem", "storage", "policy", "resources", "power", "thermal"):
        if not isinstance(metadata.get(key), dict):
            errors.append(key + " must contain structured observations")
    os_metadata = metadata.get("os", {})
    if not isinstance(os_metadata, dict) or os_metadata.get("kernel") not in ("Linux", "Darwin"):
        errors.append("OS kernel unavailable")
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
                           "forks_per_row": 100, "native_pairs_per_measure": 10, "storage_forks": 1000},
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
