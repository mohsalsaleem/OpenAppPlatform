"""A previous host OOM must not be attributed to a new release journey."""
import contextlib
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('journey_monitor', Path(__file__).with_name('monitor.py'))
monitor = importlib.util.module_from_spec(spec)
spec.loader.exec_module(monitor)


class KernelWindowTests(unittest.TestCase):
    def summary(self, responses):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'monitor.jsonl'
            path.write_text('\n'.join(json.dumps({'t': t, 'memAvailableMiB': 300}) for t in
                                      ('2026-10-15T10:00:00Z', '2026-10-15T10:01:00Z')))
            output = io.StringIO()
            with patch.object(monitor.subprocess, 'run', side_effect=responses) as calls:
                with contextlib.redirect_stdout(output):
                    self.assertEqual(monitor.summary(SimpleNamespace(file=str(path), kernel_log=True)), 0)
            return json.loads(output.getvalue()), calls.call_args_list

    def test_empty_readable_window_is_zero_and_both_bounds_are_requested(self):
        result, calls = self.summary([subprocess.CompletedProcess([], 0, stdout='')])
        self.assertEqual(result['oomKills'], 0)
        self.assertTrue(result['kernelLogAvailable'])
        command = calls[0].args[0]
        self.assertIn('2026-10-15 10:00:00 UTC', command)
        self.assertIn('2026-10-15 10:01:00 UTC', command)

    def test_current_window_oom_is_reported(self):
        result, _ = self.summary([subprocess.CompletedProcess([], 0, stdout='kernel: oom-kill: process\n')])
        self.assertEqual(result['oomKills'], 1)

    def test_unreadable_logs_are_unknown_not_zero(self):
        result, calls = self.summary([subprocess.CompletedProcess([], 1, stdout=''),
                                      subprocess.CompletedProcess([], 1, stdout='')])
        self.assertIsNone(result['oomKills'])
        self.assertFalse(result['kernelLogAvailable'])
        self.assertEqual(len(calls), 2)


if __name__ == '__main__':
    unittest.main()
