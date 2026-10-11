"""Native Docker file protocol acceptance; HTTP routing is verified in staging."""
import argparse
import hashlib
import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('native_route', ROOT / 'scripts/native_route_file.py')
native = importlib.util.module_from_spec(spec); spec.loader.exec_module(native)


def run_drill(host, project, image, report_dir):
    if not host.startswith('unix:///') or not project.startswith('oap-test-') or not image.startswith('openappplatform-test-fixture:'):
        raise ValueError('Native route test requires disposable local fixtures')
    docker = ['docker', '--host', host, 'compose', '--env-file', 'tests/env/images.env', '-f', 'tests/env/compose.yaml',
              '-p', project, 'exec', '-T', 'docker', 'docker', '--host', 'unix:///run/oap-test/docker.sock']
    initial_locks = tempfile.TemporaryDirectory(prefix='oap-route-lock-')
    proxy = native.NativeProxy(docker, initial_locks.name)
    container = None
    try:
        container = proxy.run(['run', '-d', '--name', 'coolify-proxy', '--network', 'none', '--user', '0',
                               '--label', 'io.openappplatform.test-route=' + project,
                               '--entrypoint', '/bin/sh', image, '-c', 'mkdir -p /traefik/dynamic; exec sleep 3600']).decode().strip()
        name, path = 'oap-test-route', '/traefik/dynamic/oap-test-route.yaml'
        old, new = b'{"revision":1}\n', b'{"revision":2}\n'
        def write(value):
            proxy.run(['exec', '-i', container, 'sh', '-c', 'cat > "$1"', 'fixture', path], value)
        write(old)
        before = proxy.read(name)
        write(b'{"external":true}\n')
        try:
            proxy.publish(name, before['peer'], before['sha256'], new.decode())
            raise AssertionError('External native drift was overwritten')
        except RuntimeError:
            assert proxy.read(name)['config'] == '{"external":true}\n'
        write(old)
        with tempfile.TemporaryDirectory(prefix='oap-route-lock-') as locks:
            proxy.lock_dir = Path(locks)
            result = proxy.publish(name, before['peer'], before['sha256'], new.decode())
            assert result['status'] == 'applied'
            identity = proxy.run(['exec', container, 'stat', '-c', '%i:%Y', path])
            result = proxy.publish(name, before['peer'], before['sha256'], new.decode())
            assert result['status'] == 'alreadyApplied'
            assert proxy.run(['exec', container, 'stat', '-c', '%i:%Y', path]) == identity
            changed_peer = dict(before['peer'], proxyId='different-proxy')
            try:
                proxy.publish(name, changed_peer, before['sha256'], old.decode())
                raise AssertionError('Changed proxy identity was accepted')
            except RuntimeError:
                assert proxy.read(name)['config'] == new.decode()
        proxy.run(['exec', container, 'sh', '-c', 'ln -s "$1" /traefik/dynamic/oap-symlink.yaml', 'fixture', path])
        try:
            proxy.read('oap-symlink')
            raise AssertionError('Symlink route was accepted')
        except RuntimeError:
            pass
        receipt = {'status': 'passed', 'externalDriftPreserved': True, 'proxyIdentityChecked': True,
                   'atomicReplacementVerified': True, 'repeatApplyPreservesInode': True,
                   'symlinkRejected': True, 'sha256': hashlib.sha256(new).hexdigest(),
                   'scope': 'native Docker file protocol; fixture is not an HTTP Traefik proxy'}
        (Path(report_dir) / 'route-membership-native.json').write_text(json.dumps(receipt, indent=2) + '\n')
        print('Native route file protocol: drift rejection, replacement and idempotent retry passed')
    finally:
        try:
            if container:
                proxy.run(['rm', '-f', container])
        finally:
            initial_locks.cleanup()


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--docker-host', required=True)
    parser.add_argument('--project', required=True)
    parser.add_argument('--image', required=True)
    parser.add_argument('--report-dir', required=True)
    args = parser.parse_args()
    run_drill(args.docker_host, args.project, args.image, args.report_dir)
