import unittest
from unittest import mock
from benchmarks.control import control_suite
from benchmarks.protocol import CONTROL_CALLERS, CONTROL_SAMPLES, CONTROL_P99_MS


class ControlProtocolTests(unittest.TestCase):
    def test_fixed_allocation_and_invalid_preparation(self):
        self.assertEqual((CONTROL_CALLERS, CONTROL_SAMPLES, CONTROL_P99_MS), (32, 200, 35))
        with self.assertRaisesRegex(ValueError, "exactly 32"):
            control_suite("http://localhost", None, [], None)
        callers = [dict(principal_id="pr", sandbox_id="sb", token_env="TOKEN")] * 32
        with self.assertRaisesRegex(ValueError, "distinct principal_id"):
            control_suite("http://localhost", None, callers, None)

    def test_bearers_cannot_be_loaded_from_a_manifest(self):
        callers = [dict(principal_id=f"p{i}", sandbox_id=f"s{i}", token_env=f"T{i}", token="not-allowed") for i in range(32)]
        with self.assertRaisesRegex(ValueError, "unknown fields"):
            control_suite("http://localhost", None, callers, None)
        for c in callers:
            del c["token"]
        with mock.patch.dict("os.environ", {}, clear=True):
            with self.assertRaisesRegex(ValueError, "environment"):
                control_suite("http://localhost", None, callers, None)
