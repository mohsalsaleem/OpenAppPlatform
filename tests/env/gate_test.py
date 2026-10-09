import contextlib
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

module_path = Path(__file__).resolve().parents[2] / 'scripts/test-env.py'
spec = importlib.util.spec_from_file_location('oap_test_env', module_path)
harness = importlib.util.module_from_spec(spec)
spec.loader.exec_module(harness)

class PushGateTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.stamp = self.root / '.local/last-pass.json'
        self.root_patch = patch.object(harness, 'ROOT', self.root)
        self.stamp_patch = patch.object(harness, 'LATEST', self.stamp)
        self.root_patch.start(); self.stamp_patch.start()
        self.addCleanup(self.root_patch.stop); self.addCleanup(self.stamp_patch.stop)
        self.git('init', '-q')
        (self.root / '.gitignore').write_text('.local/\n')
        (self.root / 'source.txt').write_text('tested source\n')
        self.commit()

    def git(self, *args):
        subprocess.run(['git', '-c', 'user.name=Local Test', '-c', 'user.email=test@example.invalid', *args], cwd=self.root, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

    def commit(self):
        self.git('add', '.')
        self.git('commit', '-qm', 'Test snapshot')

    def passing_stamp(self):
        self.stamp.parent.mkdir(exist_ok=True)
        self.stamp.write_text(json.dumps({'status':'passed','runId':'test','fingerprint':harness.source_fingerprint()}))

    def test_committing_tested_content_does_not_change_fingerprint(self):
        (self.root / 'source.txt').write_text('new tested source\n')
        fingerprint = harness.source_fingerprint()
        self.commit()
        self.assertEqual(fingerprint, harness.source_fingerprint())
        self.passing_stamp()
        with contextlib.redirect_stdout(io.StringIO()): harness.verify()

    def test_untested_committed_source_is_rejected(self):
        self.passing_stamp()
        (self.root / 'source.txt').write_text('untested source\n')
        self.commit()
        with self.assertRaisesRegex(RuntimeError, 'differs'): harness.verify()

    def test_dirty_working_tree_is_rejected(self):
        self.passing_stamp()
        (self.root / 'source.txt').write_text('dirty source\n')
        with self.assertRaisesRegex(RuntimeError, 'clean'): harness.verify()

    def test_ignored_artifacts_do_not_change_fingerprint(self):
        before = harness.source_fingerprint()
        self.stamp.parent.mkdir()
        (self.stamp.parent / 'artifact.log').write_text('generated data')
        self.assertEqual(before, harness.source_fingerprint())

    def test_missing_or_failed_pass_cannot_authorize_push(self):
        with self.assertRaisesRegex(RuntimeError, 'No successful'): harness.verify()
        self.passing_stamp()
        record=json.loads(self.stamp.read_text());record['status']='failed';self.stamp.write_text(json.dumps(record))
        with self.assertRaisesRegex(RuntimeError, 'differs'): harness.verify()

    def test_a_failed_run_invalidates_previous_pass_before_checks(self):
        self.passing_stamp()
        with patch.object(harness, 'STATE', self.stamp.parent), patch.object(harness, 'command', side_effect=RuntimeError('guard checks failed')):
            with self.assertRaisesRegex(RuntimeError, 'guard checks failed'): harness.run()
        self.assertFalse(self.stamp.exists())

    def test_executable_bit_is_part_of_fingerprint(self):
        before=harness.source_fingerprint()
        (self.root/'source.txt').chmod(0o755)
        self.assertNotEqual(before,harness.source_fingerprint())

if __name__ == '__main__': unittest.main()
