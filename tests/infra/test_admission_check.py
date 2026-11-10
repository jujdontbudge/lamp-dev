import contextlib
import importlib.util
import io
from pathlib import Path
from types import SimpleNamespace
import unittest
from unittest import mock

path = Path(__file__).resolve().parents[2] / 'infra/linux/admission-check.py'
spec = importlib.util.spec_from_file_location('admission_check', path)
guard = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guard)


class AllocationGuard(unittest.TestCase):
    def test_fixed_memory_boundary_and_two_sandbox_ceiling(self):
        for available, allocated, expected in ((12288, 0, 0), (12287, 0, 1), (20000, 2, 1)):
            groups = [SimpleNamespace(name=f'sb-{i}', is_dir=lambda: True) for i in range(allocated)]
            raw = f'              total used free shared buff/cache available\nMem: 31140 100 100 0 100 {available}\n'
            with mock.patch.object(guard.subprocess, 'check_output', return_value=raw) as free, mock.patch.object(guard.Path, 'rglob', return_value=groups), mock.patch.object(guard.Path, 'glob', return_value=[]), contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
                self.assertEqual(guard.main(), expected)
                free.assert_called_once_with(['free', '-m'], text=True)
