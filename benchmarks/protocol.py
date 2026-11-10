import hashlib
import json
import math
import random
import statistics


PROTOCOL = "lamp.benchmark/v1"
WARMUPS = 20
CREATES = 200
FORKS = 100
PAIRS = 10
STORAGE_FORKS = 1000
CONTROL_CALLERS = 32
CONTROL_SAMPLES = 200
CONTROL_P99_MS = 35
RESOURCES = {"vcpus": 2, "memory_mib": 4096, "disk_gib": 20, "pids_max": 4096}
DIRTY_MIB = (0, 128, 1024)
DURABILITIES = ("volatile", "local", "replicated")
COMMIT_POINTS = (
    "reserve", "capture", "seal", "object_flush", "directory_flush",
    "heads", "switch", "journal_flush", "publish", "ack", "delete", "gc",
    "compact_objects", "compact_redirect", "compact_gc",
)
MEASURES = {
    "compile": "rate", "compression": "rate", "crypto": "rate",
    "seq_read": "rate", "seq_write": "rate",
    "random_read": "rate", "random_write": "rate",
    "tree": "rate", "fsync": "latency", "cow_first_write": "latency",
    "tcp_goodput": "rate", "udp_goodput": "rate",
    "tcp_packets": "rate", "udp_packets": "rate",
    "request_latency": "latency", "small_flows": "rate", "proxy": "latency",
}


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":"), allow_nan=False).encode()


def digest(value):
    return hashlib.sha256(canonical(value)).hexdigest()


def percentile(values, fraction):
    if not values:
        return None
    ordered = sorted(values)
    return ordered[max(0, math.ceil(fraction * len(ordered)) - 1)]


def timing(rows, required, boundary, every=False):
    measured = [r for r in rows if not r["warmup"]]
    values = [r["elapsed_ms"] for r in measured]
    successes = sum(r["error"] is None for r in measured)
    complete = len(measured) >= required
    limit = max(values, default=math.inf) if every else percentile(values, .5)
    return {
        "trials": len(measured), "successes": successes,
        "failures": len(measured) - successes, "required": required,
        "p50_ms": percentile(values, .5), "p95_ms": percentile(values, .95),
        "p99_ms": percentile(values, .99), "max_ms": max(values, default=None),
        "status": "pass" if complete and successes == len(measured) and limit < boundary else "fail",
    }


def paired_summary(pairs, kind):
    if not pairs:
        return {"status": "incomplete"}
    ratios = []
    for pair in pairs:
        native, secured = pair["native"], pair["secured"]
        if not all(isinstance(v, (int, float)) and math.isfinite(v) and v > 0 for v in (native, secured)):
            raise ValueError("nonpositive or nonfinite measurement")
        ratios.append(secured / native if kind == "rate" else native / secured)
    logs = [math.log(r) for r in ratios]
    point = math.exp(statistics.mean(logs))
    rng = random.Random(2202)
    bootstrap = sorted(math.exp(statistics.mean(rng.choices(logs, k=len(logs)))) for _ in range(10000))
    lower = bootstrap[499]
    return {
        "pairs": len(pairs), "ratios": ratios, "point_ratio": point,
        "one_sided_95_lower": lower, "method": "paired geometric mean; percentile bootstrap, seed 2202, n=10000",
        "statistical_evidence": "provisional" if lower < .97 else "objective_met",
        "status": "pass" if len(pairs) >= PAIRS and point >= .97 else "fail",
    }


def plan(extra_rows=()):
    rows = []
    for durability in DURABILITIES:
        for dirty in DIRTY_MIB:
            rows.append({
                "id": f"b1-{durability}-{dirty}", "resources": dict(RESOURCES),
                "dirty_mib": dirty, "durability": durability,
                "case": "prepared_template", "hard_timing": True,
            })
    for row in extra_rows:
        if row["id"] in {r["id"] for r in rows}:
            raise ValueError("cannot replace a binding row")
        rows.append({**row, "hard_timing": False})
    return rows
