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


class BackupReportingTest(unittest.TestCase):
    def test_blocked_backup_records_safe_failure_and_preserves_archive(self):
        with tempfile.TemporaryDirectory() as tmp:
            archive = Path(tmp) / 'secret-path.dump'
            with patch.object(installer, 'compose', return_value=b'running-container\n'):
                with self.assertRaises(ValueError):
                    installer.backup(tmp, archive)
            reports = list((Path(tmp) / 'backup-reports').glob('*.json'))
            self.assertEqual(len(reports), 1)
            import json
            value = json.loads(reports[0].read_text())
            self.assertEqual(value['status'], 'failed')
            self.assertEqual(value['errorCode'], 'controller-running')
            self.assertNotIn('secret-path', reports[0].read_text())
            self.assertFalse(archive.exists())
            self.assertEqual(reports[0].stat().st_mode & 0o777, 0o644)

    def test_private_errors_and_unavailable_reporting_fail_closed(self):
        with tempfile.TemporaryDirectory() as tmp:
            archive = Path(tmp) / 'metadata.dump'
            with patch.object(installer, '_backup', side_effect=RuntimeError('password=private-value')):
                with self.assertRaises(RuntimeError):
                    installer.backup(tmp, archive)
            report = next((Path(tmp) / 'backup-reports').glob('*.json')).read_text()
            self.assertNotIn('private-value', report)
            self.assertIn('operation-failed', report)
            with patch.object(installer, 'write_report', side_effect=OSError('unavailable')):
                with patch.object(installer, '_backup') as operation:
                    with self.assertRaises(OSError):
                        installer.backup(tmp, archive)
                    operation.assert_not_called()

class LifecycleSafetyTest(unittest.TestCase):
    def test_incompatible_candidate_preserves_private_configuration(self):
        with tempfile.TemporaryDirectory() as tmp:
            folder = Path(tmp) / 'install'
            installer.init(folder, IMAGE, operator_only=True)
            before = (folder / '.env').read_bytes()
            with patch.object(installer, 'preflight_image', side_effect=ValueError('incompatible')):
                with self.assertRaises(ValueError):
                    installer.lifecycle(folder, 'update', 'sha256:' + 'b' * 64)
            self.assertEqual((folder / '.env').read_bytes(), before)
            # Advisory lock is released; a failed command does not leave a stale lock.
            with patch.object(installer, 'compose', return_value=b'[]'):
                self.assertFalse(installer.lifecycle(folder, 'check')['healthy'])

    def test_update_requires_immutable_candidate_before_dispatch(self):
        with tempfile.TemporaryDirectory() as tmp:
            folder = Path(tmp) / 'install'
            installer.init(folder, IMAGE, operator_only=True)
            with patch.object(installer, 'preflight_image') as preflight:
                with self.assertRaises(ValueError):
                    installer.lifecycle(folder, 'update', 'oap:latest')
                preflight.assert_not_called()


class IngressInstallationSafetyTest(unittest.TestCase):
    def test_provider_diagnostics_are_opt_in_and_private(self):
        import json
        with tempfile.TemporaryDirectory() as tmp:
            folder = Path(tmp) / 'install'
            installer.init(folder, IMAGE, operator_only=True)
            error = installer.ComposeError('synthetic-password=private-test-value')
            self.assertIsNone(installer.save_diagnostics(error))
            with patch.object(installer, 'DIAGNOSTICS_DIRECTORY', str(folder)):
                name = installer.save_diagnostics(error)
            self.assertEqual((folder / name).stat().st_mode & 0o777, 0o600)
            self.assertEqual(json.loads((folder / name).read_text())['detail'], error.detail)

    def test_unsupported_inputs_fail_before_creating_installation(self):
        cases = [dict(ingress_domain='*.example.com'), dict(ingress_domain='127.0.0.1'),
                 dict(ingress_domain='app.example.com', ingress_tls='arbitrary'),
                 dict(ingress_domain='app.example.com', ingress_image='caddy:latest'),
                 dict(ingress_domain='app.example.com', ingress_http_port=443),
                 dict(ingress_domain='app.example.com', ingress_bind='not-an-address')]
        for options in cases:
            with self.subTest(options=options), tempfile.TemporaryDirectory() as tmp:
                folder = Path(tmp) / 'install'
                with self.assertRaises(ValueError):
                    installer.init(folder, IMAGE, operator_only=True, **options)
                self.assertFalse(folder.exists())

    def test_initial_proxy_claim_and_repeated_start_preserve_authority(self):
        import json
        import socket
        import subprocess
        with tempfile.TemporaryDirectory() as tmp, socket.socket(socket.AF_UNIX) as listener:
            endpoint = Path(tmp) / 'docker.sock'
            listener.bind(str(endpoint))
            folder = Path(tmp) / 'install'
            with patch.object(installer.subprocess, 'run', return_value=subprocess.CompletedProcess([], 0, b'999', b'')):
                installer.init(folder, IMAGE, endpoint, ingress_domain='app.example.com',
                               ingress_tls='letsencrypt', ingress_bind='0.0.0.0', environment='production')
            targets = json.loads((folder / 'targets.json').read_text())
            self.assertEqual(targets[0]['environment'], 'production')
            self.assertNotIn('ingress', targets[0]['settings'])
            directory, values = installer.installation_values(folder)
            peer, commitment = 'b' * 64, 'c' * 64
            with patch.object(installer, 'compose', side_effect=[b'', b'', peer.encode(), json.dumps({'ok': True, 'bootstrapHash': commitment}).encode()]):
                installer.provision_ingress(directory, values)
            before = (folder / 'targets.json').read_bytes()
            authority = json.loads(before)[0]['settings']['ingress']
            self.assertEqual(authority['publicAcme'], 'letsencrypt')
            self.assertFalse(authority['internalCa'])
            self.assertEqual(authority['bootstrapHash'], commitment)
            with patch.object(installer, 'compose', side_effect=[peer.encode(), b'', peer.encode()]) as commands:
                installer.provision_ingress(directory, values)
                self.assertEqual(commands.call_count, 3)  # no new bootstrap observation/claim
            self.assertEqual((folder / 'targets.json').read_bytes(), before)
            with patch.object(installer, 'compose', side_effect=[('d' * 64).encode()]):
                with self.assertRaisesRegex(ValueError, 'handoff'):
                    installer.provision_ingress(directory, values)
            self.assertEqual((folder / 'targets.json').read_bytes(), before)
