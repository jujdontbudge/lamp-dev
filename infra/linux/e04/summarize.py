#!/usr/bin/env python3
"""Summarizes one E04 run directory for the evidence docs.

Usage: summarize.py RUN_DIR > summary.json

Reads the harness's chained observations (it does not judge them; the
harness summary does) and counts scenarios per crash point and fault, the
recovered/acknowledged generations, and the block-trace fates recorded by
the fixture.
"""
import collections
import json
from pathlib import Path
import sys

run = Path(sys.argv[1])
summary = json.loads((run / "summary.json").read_text())
points = collections.Counter()
faults = collections.Counter()
recovered = collections.Counter()
trace = collections.Counter()
backends = collections.Counter()
count = 0
first = last = None
with open(run / "observations.jsonl") as f:
    for line in f:
        o = json.loads(line)
        if o["kind"] != "storage_recovery":
            continue
        v = o["value"]
        req, obs = v.get("request") or v.get("scenario") or {}, v.get("observed") or {}
        count += 1
        first = first or o["monotonic_ns"]
        last = o["monotonic_ns"]
        kind = "unauthorized" if req.get("unauthorized") else "omit:" + req["omit"] if req.get("omit") else "scenario"
        points["%s %s:%s" % (kind, req.get("point"), req.get("side"))] += 1
        faults["%s %s" % (kind, req.get("fault", "discard"))] += 1
        recovered["%s %s" % (kind, ",".join(sorted(obs.get("objects", {}))) or "-")] += 1
        backends[(obs.get("filesystem"), obs.get("disk_backend"))] += 1
        for k, n in (obs.get("evidence", {}).get("barrier") or {}).items():
            trace[k] += n
e04 = summary["results"].get("E04", {})
json.dump({
    "run": run.name, "status": summary["status"], "completed_at": summary.get("completed_at"),
    "e04": {k: v for k, v in e04.items() if k != "crash_coverage"},
    "crash_coverage": e04.get("crash_coverage"),
    "recoveries_observed": count,
    "wall_s": round((last - first) / 1e9, 1) if first else None,
    "filesystem_backend": {"%s/%s" % k: n for k, n in backends.items()},
    "per_point": dict(sorted(points.items())), "per_fault": dict(sorted(faults.items())),
    "recovered_generations": dict(sorted(recovered.items())),
    "block_trace_totals": dict(trace),
    "last_observation_hash": summary.get("last_observation_hash"),
}, sys.stdout, indent=1)
print()
