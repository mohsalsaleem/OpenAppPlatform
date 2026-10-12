import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('installer', Path(__file__).resolve().parents[2] / 'scripts/install.py')
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)
IMAGE = 'sha256:' + 'a' * 64


class InstallationSafetyTest(unittest.TestCase):
    def test_operator_only_generates_private_owner_configuration_without_socket_access(self):
        with tempfile.TemporaryDirectory() as tmp:
            folder = Path(tmp) / 'install'
            with patch.object(installer.subprocess, 'run', side_effect=AssertionError('unexpected daemon access')):
                installer.init(folder, IMAGE, operator_only=True)
            env = (folder / '.env').read_text()
            self.assertIn('COMPOSE_FILE=compose.yaml\n', env)
            self.assertNotIn('OAP_DOCKER_SOCKET=', env)
            self.assertEqual((folder / '.env').stat().st_mode & 0o777, 0o600)
            self.assertEqual(folder.stat().st_mode & 0o777, 0o700)
            self.assertEqual((folder / 'targets.json').read_text().strip(), '[]')
            self.assertGreaterEqual(len(next(line.split('=', 1)[1] for line in env.splitlines() if line.startswith('OAP_SETUP_TOKEN='))), 24)
            before = (folder / '.env').read_bytes()
            with self.assertRaises(ValueError):
                installer.init(folder, IMAGE, operator_only=True)
            self.assertEqual((folder / '.env').read_bytes(), before)

    def test_mutable_image_is_rejected_before_writing_configuration(self):
        with tempfile.TemporaryDirectory() as tmp:
            folder = Path(tmp) / 'install'
            with self.assertRaises(ValueError):
                installer.init(folder, 'openappplatform:latest', operator_only=True)
            self.assertFalse(folder.exists())

    def test_running_controller_blocks_backup_and_restore(self):
        with tempfile.TemporaryDirectory() as tmp:
            archive = Path(tmp) / 'metadata.dump'
            with patch.object(installer, 'compose', return_value=b'running-container\n'):
                with self.assertRaises(ValueError):
                    installer.backup(tmp, archive)
            self.assertFalse(archive.exists())
            with self.assertRaises(ValueError):
                installer.restore_check(tmp, archive, 'oap')
