import base64
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

from benchmarks.custody_probe import canary_variants, main, probe, scan_file
from benchmarks.protocol import digest


class Observations:
    def __init__(self, root):
        self.root = Path(root)
        self.events = []

    def observe(self, kind, value):
        self.events.append((kind, value))


class CustodyScannerTests(unittest.TestCase):
    def test_literal_and_encoded_canaries_across_every_chunk_boundary(self):
        variants = canary_variants("1" * 32)
        self.assertIn(base64.b64encode(b"lamp-e07-canary-" + b"1" * 32), variants.values())
        with tempfile.TemporaryDirectory() as root:
            path = Path(root) / "artifact"
            for name, value in variants.items():
                for split in range(1, len(value)):
                    path.write_bytes(b"x" * (64 - split) + value + b"x" * 64)
                    result = scan_file(path, variants, chunk_size=64)
                    self.assertGreater(result["matches"][name], 0)

    def test_clean_bytes_and_sparse_holes_are_scanned_without_false_matches(self):
        with tempfile.TemporaryDirectory() as root:
            path = Path(root) / "memory"
            with path.open("wb") as output:
                output.seek(1 << 20)
                output.write(b"clean")
            result = scan_file(path, canary_variants("2" * 32), chunk_size=4096)
            self.assertFalse(any(result["matches"].values()))
            self.assertEqual(result["logical_bytes"], (1 << 20) + 5)

    def test_a_matching_prefix_is_not_counted_twice_in_the_overlap(self):
        variants = {"literal": b"synthetic-canary"}
        with tempfile.TemporaryDirectory() as root:
            path = Path(root) / "memory"
            path.write_bytes(variants["literal"] + b"x" * 128)
            self.assertEqual(scan_file(path, variants, chunk_size=32)["matches"]["literal"], 1)

    def test_nonce_cannot_be_an_arbitrary_credential(self):
        with self.assertRaises(ValueError):
            canary_variants("not-a-synthetic-nonce")

    def test_utf16_canary_can_end_in_a_sparse_hole(self):
        with tempfile.TemporaryDirectory() as root:
            path = Path(root) / "memory"
            with path.open("wb") as output:
                output.write(b"x" * 4093 + b"A\0B")
                output.truncate(1 << 20)
            result = scan_file(path, {"utf16": b"A\0B\0"}, chunk_size=1024)
            self.assertEqual(result["matches"]["utf16"], 1)

    def test_empty_canaries_and_invalid_chunk_size_are_rejected(self):
        for variants, size in [({}, 1), ({"bad": b""}, 1), ({"good": b"canary"}, 0)]:
            with self.assertRaises(ValueError):
                scan_file("not-opened", variants, chunk_size=size)


class CleanBuilderOracleTests(unittest.TestCase):
    def fixture(self, root, source_positive=True, contaminate=False, omit_memory=False):
        path = Path(root) / "fixture.py"
        path.write_text(
            "#!/usr/bin/env python3\n"
            "import json, sys\n"
            "from pathlib import Path\n"
            "root = Path(sys.argv[1]); root.mkdir()\n"
            "(root / 'lamp-disposable-custody-fixture').touch()\n"
            "(root / 'memory-unmounted.json').write_text('true')\n"
            "token = ('lamp-e07-canary-' + sys.argv[3]).encode()\n"
            f"for label, ident in [('source', 'a'), ('clean', 'b')]:\n"
            " directory = root / 'store/templates' / ('tp_' + ident * 26)\n"
            " directory.mkdir(parents=True)\n"
            " for name in ['mem', 'rootfs.ext4', 'root.ext4', 'vmstate', 'manifest.json', 'provenance.json']:\n"
            f"  if {omit_memory!r} and label == 'clean' and name == 'mem': continue\n"
            f"  tainted = (label == 'source' and {source_positive!r}) or (label == 'clean' and {contaminate!r})\n"
            "  (directory / name).write_bytes(token if tainted else b'clean')\n"
            "print(json.dumps({'qualification': 'diagnostic-engine-api-only',\n"
            " 'source_template_id': 'tp_' + 'a' * 26, 'clean_template_id': 'tp_' + 'b' * 26,\n"
            " 'clean_manifest_digest': '1' * 64, 'definition_digest': '2' * 64,\n"
            " 'unsafe_transfers_denied': 3, 'fresh_tool_verified': True,\n"
            " 'seed_ref': 'lamp-seed/test@sha256:' + '3' * 64}))\n"
        )
        path.chmod(0o700)
        return path

    def test_positive_control_and_complete_clean_outputs_are_required(self):
        for source, contaminated, omit, expected in [
            (True, False, False, "pass"),
            (False, False, False, "fail"),
            (True, True, False, "fail"),
            (True, False, True, "fail"),
        ]:
            with tempfile.TemporaryDirectory() as root:
                fixture = self.fixture(root, source, contaminated, omit)
                result = probe(fixture, root, Observations(root), 10)
                self.assertEqual(result["status"], expected)

    def test_successful_diagnostic_cannot_pass_e07_or_exit_successfully(self):
        with tempfile.TemporaryDirectory() as root:
            metadata = Path(root) / "metadata.json"
            metadata.write_text("{}")
            output = Path(root) / "run"
            argv = [
                "probe", "--fixture", "/unused", "--seed", "/unused",
                "--metadata", str(metadata), "--output", str(output),
            ]
            with mock.patch("sys.argv", argv), mock.patch(
                "benchmarks.custody_probe.probe", return_value={"status": "pass"}
            ), mock.patch("builtins.print"):
                self.assertEqual(main(), 1)
            summary = json.loads((output / "summary.json").read_text())
            self.assertEqual(summary["status"], "incomplete")
            self.assertEqual(summary["results"]["E07"]["status"], "incomplete")
            self.assertTrue(summary["diagnostic"])

    def test_real_recorded_runs_preserve_failures_and_do_not_qualify_e07(self):
        root = Path(__file__).resolve().parents[2] / "docs/development/evidence/t9/runs"
        runs = sorted(root.iterdir())
        self.assertEqual(len(runs), 4)
        for index, run in enumerate(runs):
            summary = json.loads((run / "summary.json").read_text())
            self.assertEqual(summary["status"], "fail" if index < 3 else "incomplete")
            self.assertTrue(summary["diagnostic"])
            self.assertEqual(summary["results"]["E07"]["status"], "incomplete")
            previous = "0" * 64
            for line in (run / "observations.jsonl").read_text().splitlines():
                observation = json.loads(line)
                recorded = observation.pop("sha256")
                self.assertEqual(observation["previous"], previous)
                self.assertEqual(digest(observation), recorded)
                previous = recorded
            self.assertEqual(summary["last_observation_hash"], previous)
        result = json.loads((runs[-1] / "summary.json").read_text())["results"]["clean_builder_control"]
        self.assertEqual(result["status"], "pass")
        self.assertTrue(result["source_canary_positive"])
        self.assertTrue(result["complete_output_scan"])
        self.assertEqual(result["clean_canary_matches"], 0)


if __name__ == "__main__":
    unittest.main()
