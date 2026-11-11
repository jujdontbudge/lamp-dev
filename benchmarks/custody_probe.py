import argparse
import base64
import errno
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import subprocess
import sys

from benchmarks.evidence import Evidence


BLOCKERS = [
    "official subscription sign-in and useful inference not exercised",
    "same-person live memory fork and suspend/resume subscription continuation not exercised",
    "vendor-approved refresh and account eligibility not qualified",
    "per-sandbox vendor-only guard policy not integrated",
    "bounded native harness task bridge and independent exfiltration sinks not qualified",
    "public template/export/share fresh-harness handoff not integrated",
    "custody graph is not yet persisted or enforced by the public control/session path",
]


def canary_variants(nonce):
    if not re.fullmatch("[0-9a-f]{32}", nonce):
        raise ValueError("synthetic nonce must be 32 lowercase hex characters")
    literal = ("lamp-e07-canary-" + nonce).encode()
    return {
        "literal": literal,
        "base64": base64.b64encode(literal),
        "base64_unpadded": base64.b64encode(literal).rstrip(b"="),
        "hex": literal.hex().encode(),
        "hex_upper": literal.hex().upper().encode(),
        "percent": b"".join(("%%%02X" % byte).encode() for byte in literal),
        "utf16le": literal.decode().encode("utf-16le"),
    }


def scan_ranges(fd, size, overlap, sparse):
    ranges = []
    offset = 0
    if not sparse or not hasattr(os, "SEEK_DATA"):
        return [(0, size)]
    while offset < size:
        try:
            data = os.lseek(fd, offset, os.SEEK_DATA)
            end = os.lseek(fd, data, os.SEEK_HOLE)
        except OSError as error:
            if error.errno == errno.ENXIO:
                break
            if error.errno not in (errno.EINVAL, errno.ENOTSUP):
                raise
            return [(0, size)]
        first, last = max(0, data - overlap), min(size, end + overlap)
        if ranges and first <= ranges[-1][1]:
            ranges[-1] = (ranges[-1][0], last)
        else:
            ranges.append((first, last))
        offset = end
    return ranges


def scan_file(path, variants, chunk_size=4 << 20):
    if not variants or not all(variants.values()) or chunk_size < 1:
        raise ValueError("scanner requires nonempty canaries and a positive chunk")
    matches = {name: 0 for name in variants}
    overlap = max(map(len, variants.values())) - 1
    scanned = 0
    result_hash = hashlib.sha256()
    with Path(path).open("rb", buffering=0) as source:
        size = os.fstat(source.fileno()).st_size
        ranges = scan_ranges(source.fileno(), size, overlap, all(any(value) for value in variants.values()))
        for offset, end in ranges:
            tail = b""
            source.seek(offset)
            while offset < end:
                chunk = source.read(min(chunk_size, end - offset))
                if not chunk:
                    raise OSError("image truncated during scan")
                result_hash.update(offset.to_bytes(8, "big"))
                result_hash.update(len(chunk).to_bytes(8, "big"))
                result_hash.update(chunk)
                combined = tail + chunk
                for name, value in variants.items():
                    start = max(0, len(tail) - len(value) + 1)
                    while (found := combined.find(value, start)) >= 0:
                        matches[name] += 1
                        start = found + 1
                tail = combined[-overlap:] if overlap else b""
                scanned += len(chunk)
                offset += len(chunk)
    return {
        "logical_bytes": size, "scanned_bytes": scanned, "matches": matches,
        "scanned_extents_sha256": result_hash.hexdigest(),
    }


def probe(fixture, seed, evidence, timeout):
    nonce = secrets.token_hex(16)
    root = evidence.root.resolve() / "fixture"
    completed = subprocess.run(
        [str(Path(fixture).resolve()), str(root), str(Path(seed).resolve()), nonce],
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=timeout, check=False,
    )
    if completed.returncode != 0:
        evidence.observe("fixture_exit", {"returncode": completed.returncode})
        phase_path = root / "phase.json"
        phase = json.loads(phase_path.read_text()) if phase_path.exists() else None
        phases = {
            "open", "import_seed", "build_source", "build_clean", "allocate_clean",
            "restore_clean", "useful_command", "release_clean", "complete",
        }
        evidence.observe("fixture_phase", {"phase": phase if phase in phases else "unclassified"})
        cleanup_path = root / "memory-unmounted.json"
        cleanup = json.loads(cleanup_path.read_text()) if cleanup_path.exists() else None
        evidence.observe("fixture_cleanup", {"memory_unmounted": cleanup is True})
        return {"status": "fail", "reason": "disposable fixture did not complete"}
    if len(completed.stdout) > 4096:
        raise ValueError("fixture receipt exceeds bound")
    receipt = json.loads(completed.stdout)
    if set(receipt) != {
        "qualification", "source_template_id", "clean_template_id", "clean_manifest_digest",
        "definition_digest", "unsafe_transfers_denied", "seed_ref", "fresh_tool_verified",
    } or receipt["qualification"] != "diagnostic-engine-api-only":
        raise ValueError("unexpected fixture receipt")
    for key in ("source_template_id", "clean_template_id"):
        if not isinstance(receipt[key], str) or not re.fullmatch("tp_[0-9a-f]{26}", receipt[key]):
            raise ValueError("invalid template reference")
    for key in ("clean_manifest_digest", "definition_digest"):
        if not isinstance(receipt[key], str) or not re.fullmatch("[0-9a-f]{64}", receipt[key]):
            raise ValueError("invalid digest")
    if not isinstance(receipt["seed_ref"], str) or not re.fullmatch(
        "lamp-seed/[a-z0-9.-]+@sha256:[0-9a-f]{64}", receipt["seed_ref"]
    ) or receipt["unsafe_transfers_denied"] != 3:
        raise ValueError("invalid seed or negative control count")
    if receipt["fresh_tool_verified"] is not True:
        raise ValueError("fresh reconstruction useful command missing")
    if not (root / "lamp-disposable-custody-fixture").is_file():
        raise ValueError("disposable fixture marker absent")
    unmounted = json.loads((root / "memory-unmounted.json").read_text())
    evidence.observe("fixture_cleanup", {"memory_unmounted": unmounted is True})
    if unmounted is not True:
        raise ValueError("disposable memory mount cleanup failed")
    evidence.observe("disposable_fixture", receipt)
    variants = canary_variants(nonce)
    scans = {}
    for label in ("source", "clean"):
        directory = root / "store/templates" / receipt[label + "_template_id"]
        if not directory.resolve().is_relative_to(root.resolve()):
            raise ValueError("template outside disposable fixture")
        paths = sorted(path for path in directory.rglob("*") if path.is_file())
        if not paths:
            raise ValueError("template has no output bytes")
        scans[label] = {}
        for path in paths:
            if path.is_symlink():
                raise ValueError("symlink in output artifact")
            value = scan_file(path, variants)
            relative = str(path.relative_to(directory))
            scans[label][relative] = value
            evidence.observe("independent_canary_scan", {"label": label, "file": relative, **value})
    source_found = any(any(value["matches"].values()) for value in scans["source"].values())
    clean_found = any(any(value["matches"].values()) for value in scans["clean"].values())
    required = {"mem", "rootfs.ext4", "root.ext4", "vmstate", "manifest.json", "provenance.json"}
    complete = required.issubset(scans["clean"])
    return {
        "status": "pass" if source_found and not clean_found and complete else "fail",
        "source_canary_positive": source_found,
        "clean_canary_matches": sum(sum(value["matches"].values()) for value in scans["clean"].values()),
        "complete_output_scan": complete,
        "qualification": "diagnostic-engine-api-only",
    }


def main():
    parser = argparse.ArgumentParser(description="Diagnostic clean-builder probe, never E07 qualification")
    parser.add_argument("--fixture", required=True)
    parser.add_argument("--seed", required=True)
    parser.add_argument("--metadata", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--timeout", type=float, default=600)
    args = parser.parse_args()
    evidence = Evidence(
        args.output, json.loads(Path(args.metadata).read_text()),
        {"suite": "E07-clean-builder-diagnostic", "fixture": args.fixture, "seed": args.seed},
    )
    results = {"E07": {"status": "incomplete", "blockers": BLOCKERS}}
    try:
        results["clean_builder_control"] = probe(args.fixture, args.seed, evidence, args.timeout)
    except Exception as error:
        evidence.observe("probe_failure", {"type": type(error).__name__})
        results["clean_builder_control"] = {"status": "fail", "error": type(error).__name__}
    summary = evidence.finish(results, diagnostic=True)
    print(json.dumps({"status": summary["status"], "evidence": str(evidence.root.resolve())}))
    return 1


if __name__ == "__main__":
    sys.exit(main())
