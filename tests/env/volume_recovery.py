"""Disposable native Docker volume recovery with explicit writer cutover."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import time

ROOT = Path(__file__).resolve().parents[2]


def run_drill(host, project, image, report_dir):
    if not host.startswith('unix:///') or not project.startswith('oap-test-') or not image.startswith('openappplatform-test-fixture:'):
        raise ValueError('Volume recovery requires local disposable test fixtures')
    prefix = ['docker', '--host', host, 'compose', '--env-file', 'tests/env/images.env',
              '-f', 'tests/env/compose.yaml', '-p', project, 'exec', '-T', 'docker',
              'docker', '--host', 'unix:///run/oap-test/docker.sock']

    def native(args, input_bytes=None, expected_success=True):
        result = subprocess.run(prefix + args, cwd=ROOT, input=input_bytes,
                                capture_output=True, timeout=30)
        if (result.returncode == 0) != expected_success:
            raise RuntimeError('Unexpected native Docker volume recovery result: ' + args[0])
        return result.stdout

    volumes = {kind: project + '-recovery-' + kind for kind in ('source', 'backup', 'restored')}
    containers, created = [], []

    def helper(script, mounts, user='0', expected_success=True):
        args = ['run', '--rm', '--network', 'none', '--user', user, '--entrypoint', '/bin/sh']
        for kind, destination, readonly in mounts:
            args += ['--mount', 'type=volume,src=' + volumes[kind] + ',dst=' + destination + (',readonly' if readonly else '')]
        return native(args + [image, '-c', script], expected_success=expected_success)

    def read(kind, filename):
        return helper('cat /data/' + filename, [(kind, '/data', True)], user='10001')

    def start_writer(kind):
        name = project + '-writer-' + kind
        native(['run', '-d', '--name', name, '--network', 'none', '--user', '10001',
                '--mount', 'type=volume,src=' + volumes[kind] + ',dst=/data',
                '--entrypoint', '/bin/sh', image, '-c',
                'while :; do echo write >> /data/events; sleep 0.1; done'])
        containers.append(name)
        return name

    def stop_writer(name):
        native(['stop', '-t', '1', name])
        if native(['inspect', '--format', '{{.State.Running}}', name]).strip() != b'false':
            raise RuntimeError('Writer is still running')

    def wait_for_write(kind, previous):
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline:
            current = read(kind, 'events')
            if len(current) > len(previous):
                return current
            time.sleep(0.05)
        raise RuntimeError('Writer did not append to restored data')

    report_dir = Path(report_dir)
    try:
        # Use only the archive built by the current local test run. The runner's
        # later image load is idempotent and uses the same archive.
        native(['load'], (report_dir / 'fixtures.tar').read_bytes())
        for volume in volumes.values():
            native(['volume', 'create', '--label', 'io.openappplatform.test-recovery=' + project, volume])
            created.append(volume)
        helper('chown 10001:10001 /data; chmod 700 /data; printf asset-before-backup > /data/upload.bin; : > /data/events; chown 10001:10001 /data/*', [('source', '/data', False)])
        source_writer = start_writer('source')
        wait_for_write('source', b'')
        stop_writer(source_writer)
        before = read('source', 'events')
        time.sleep(0.2)
        if read('source', 'events') != before:
            raise RuntimeError('Quiesced writer changed data')
        helper('tar -cf /backup/uploads.tar -C /data .', [('source', '/data', True), ('backup', '/backup', False)])
        archive = helper('cat /backup/uploads.tar', [('backup', '/backup', True)])
        archive_path = report_dir / 'application-uploads.tar'
        archive_path.write_bytes(archive)
        archive_path.chmod(0o600)
        # Demonstrate changes made after the backup without destroying the source.
        native(['start', source_writer])
        wait_for_write('source', before)
        stop_writer(source_writer)
        after_backup = read('source', 'events')
        helper('printf asset-after-backup > /data/upload.bin', [('source', '/data', False)], user='10001')
        helper('printf corrupt > /backup/bad.tar; tar -xf /backup/bad.tar -C /data', [('backup', '/backup', False), ('restored', '/data', False)], expected_success=False)
        helper('tar -xf /backup/uploads.tar -C /data', [('backup', '/backup', True), ('restored', '/data', False)])
        if read('restored', 'upload.bin') != b'asset-before-backup' or read('restored', 'events') != before:
            raise RuntimeError('Restored volume does not match the quiesced snapshot')
        if read('source', 'upload.bin') != b'asset-after-backup' or read('source', 'events') != after_backup:
            raise RuntimeError('Restoration changed the retained source')
        # Native cutover: only the restored writer is resumed. Its non-root
        # access also checks that archive restoration retained usable ownership.
        restored_writer = start_writer('restored')
        wait_for_write('restored', before)
        if native(['inspect', '--format', '{{.State.Running}}', source_writer]).strip() != b'false':
            raise RuntimeError('Source writer resumed during cutover')
        stop_writer(restored_writer)
        if read('source', 'events') != after_backup:
            raise RuntimeError('Retained source changed during cutover')
        receipt = {'status': 'passed', 'backup': archive_path.name,
                   'sha256': hashlib.sha256(archive).hexdigest(),
                   'writerQuiescenceVerified': True, 'sourcePreserved': True,
                   'corruptArchiveRejected': True, 'postBackupChangesExcluded': True,
                   'restoredNonRootWriteVerified': True, 'singleWriterCutoverVerified': True,
                   'scope': 'native Docker synthetic file writer; no OAP automatic volume restore'}
        (report_dir / 'volume-recovery.json').write_text(json.dumps(receipt, indent=2) + '\n')
        print('Native volume backup/restore, writer quiescence and cutover drill passed')
    finally:
        for name in reversed(containers):
            native(['rm', '-f', name])
        for volume in reversed(created):
            native(['volume', 'rm', volume])


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--docker-host', required=True)
    parser.add_argument('--project', required=True)
    parser.add_argument('--image', required=True)
    parser.add_argument('--report-dir', required=True)
    args = parser.parse_args()
    run_drill(args.docker_host, args.project, args.image, args.report_dir)
