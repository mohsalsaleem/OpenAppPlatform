#!/usr/bin/env python3
"""Reproducible local verification, isolated from developer secrets and workloads."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parent.parent
STATE = ROOT / '.local' / 'test-env'
LATEST = STATE / 'last-pass.json'

def source_names():
    return sorted(set(filter(None, subprocess.check_output(['git', 'ls-files', '-z', '--cached', '--others', '--exclude-standard'], cwd=ROOT).decode().split('\0'))))

def source_fingerprint():
    names = source_names()
    digest = hashlib.sha256()
    for name in sorted(set(filter(None, names))):
        path = ROOT / name
        if not path.exists() and not path.is_symlink():
            continue
        if path.is_symlink():
            data = os.readlink(path).encode()
        elif path.is_file():
            data = path.read_bytes()
        else:
            continue
        digest.update(name.encode() + b'\0')
        digest.update(str(path.lstat().st_mode & 0o111).encode() + b'\0')
        digest.update(hashlib.sha256(data).digest())
    return digest.hexdigest()

def command(args, env=None, **kwargs):
    return subprocess.run(args, cwd=ROOT, env=env, check=True, **kwargs)

def pins():
    result = {}
    for line in (ROOT / 'tests/env/images.env').read_text().splitlines():
        if line and not line.startswith('#'):
            key, value = line.split('=', 1)
            result[key] = value
    return result

def docker_client():
    host = os.environ.get('DOCKER_HOST')
    if not host:
        host = subprocess.check_output(['docker', 'context', 'inspect', '--format', '{{.Endpoints.docker.Host}}'], cwd=ROOT, text=True).strip()
    if not host.startswith('unix:///'):
        raise RuntimeError('The local test harness requires a local Unix Docker endpoint')
    original = Path(os.environ.get('DOCKER_CONFIG', str(Path.home() / '.docker')))
    client_dir = STATE / 'docker-client'
    client_dir.mkdir(parents=True, exist_ok=True)
    (client_dir / 'config.json').write_text(json.dumps({'auths': {}, 'cliPluginsExtraDirs': [str(original / 'cli-plugins')]}))
    env = os.environ.copy()
    env.pop('DOCKER_CONTEXT', None)
    for key in list(env):
        if key.startswith(('COOLIFY_', 'OAP_')) or key in ('DATABASE_URL', 'TEST_DATABASE_URL'):
            env.pop(key, None)
    env['DOCKER_CONFIG'] = str(client_dir)
    env['DOCKER_BUILDKIT'] = '0'
    return ['docker', '--host', host], env

def verify():
    if subprocess.check_output(['git', 'status', '--porcelain'], cwd=ROOT, text=True).strip():
        raise RuntimeError('Commit the tested changes first; the working tree must be clean before pushing')
    if not LATEST.exists():
        raise RuntimeError('No successful isolated local run. Run make test-local before pushing')
    record = json.loads(LATEST.read_text())
    if record.get('status') != 'passed' or record.get('fingerprint') != source_fingerprint():
        raise RuntimeError('The source tree differs from the last passing local run. Run make test-local again')
    print('Local pre-push verification passed:', record['runId'])

def run(keep=False):
    STATE.mkdir(parents=True, exist_ok=True)
    LATEST.unlink(missing_ok=True)
    command([sys.executable, '-m', 'unittest', 'discover', '-s', 'tests/env', '-p', '*_test.py'])
    run_id = time.strftime('%Y%m%d-%H%M%S') + '-' + secrets.token_hex(4)
    run_dir = STATE / 'runs' / run_id
    run_dir.mkdir(parents=True)
    fingerprint = source_fingerprint()
    project = 'oap-test-' + secrets.token_hex(6)
    docker, env = docker_client()
    versions = pins()
    runner = 'openappplatform-test:' + fingerprint[:16]
    context = run_dir / 'source'
    context.mkdir()
    for name in source_names():
        original = ROOT / name
        if original.is_file() or original.is_symlink():
            copied = context / name
            copied.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(original, copied, follow_symlinks=False)
    env.update(versions)
    env['OAP_TEST_RUNNER_IMAGE'] = runner
    env['OAP_TEST_RUN_DIR'] = str(run_dir)
    env['OAP_TEST_FIXTURE_V1'] = 'openappplatform-test-fixture:' + project + '-v1'
    env['OAP_TEST_FIXTURE_V2'] = 'openappplatform-test-fixture:' + project + '-v2'
    compose = docker + ['compose', '--env-file', 'tests/env/images.env', '-f', 'tests/env/compose.yaml', '-p', project]
    record = {'status': 'running', 'runId': run_id, 'fingerprint': fingerprint, 'project': project, 'images': versions}
    container = None
    try:
        build = docker + ['build', '--pull=false', '-f', 'tests/env/Dockerfile', '-t', runner]
        build += ['--build-arg', 'GO_IMAGE=' + versions['OAP_TEST_GO_IMAGE'], '--build-arg', 'BROWSER_IMAGE=' + versions['OAP_TEST_BROWSER_IMAGE'], str(context)]
        command(build, env)
        extraction = run_dir / 'fixture-build'
        extraction.mkdir()
        container = subprocess.check_output(docker + ['create', runner], env=env, text=True).strip()
        for version in ['v1', 'v2']:
            command(docker + ['cp', container + ':/opt/oap/fixture-' + version, str(extraction / ('fixture-' + version))], env)
        command(docker + ['rm', container], env)
        container = None
        shutil.copyfile(ROOT / 'tests/env/fixture/Dockerfile', extraction / 'Dockerfile')
        fixture_tags = []
        for version, port in [('v1', '8080'), ('v2', '8025')]:
            tag = env['OAP_TEST_FIXTURE_' + version.upper()]
            fixture_tags.append(tag)
            command(docker + ['build', '--pull=false', '--build-arg', 'ALPINE_IMAGE=' + versions['OAP_TEST_ALPINE_IMAGE'], '--build-arg', 'BINARY=fixture-' + version, '--build-arg', 'PORT=' + port, '-t', tag, str(extraction)], env)
        with (run_dir / 'fixtures.tar').open('wb') as archive:
            command(docker + ['save'] + fixture_tags, env, stdout=archive)
        command(compose + ['up', '-d', '--wait', '--wait-timeout', '90', 'postgres', 'docker'], env)
        command(compose + ['run', '--rm', '--no-deps', 'runner'], env)
        suite = json.loads((run_dir / 'suite.json').read_text())
        if suite.get('status') != 'passed':
            raise RuntimeError('Runner did not produce a passing suite report')
        if fingerprint != source_fingerprint():
            raise RuntimeError('Source files changed during verification; rerun the suite')
        record.update({'status': 'passed', 'suite': suite, 'finishedAt': time.time()})
    except Exception as error:
        record.update({'status': 'failed', 'error': str(error), 'finishedAt': time.time()})
        raise
    finally:
        if container:
            subprocess.run(docker + ['rm', container], env=env, cwd=ROOT)
        logs = subprocess.run(compose + ['logs', '--no-color'], env=env, cwd=ROOT, text=True, capture_output=True)
        (run_dir / 'services.log').write_text(logs.stdout + logs.stderr)
        if not keep:
            cleanup = subprocess.run(compose + ['down', '--volumes', '--remove-orphans'], env=env, cwd=ROOT)
            if cleanup.returncode:
                record.update({'status': 'failed', 'error': 'Test environment cleanup failed'})
        else:
            print('Retained test environment:', project)
        (run_dir / 'result.json').write_text(json.dumps(record, indent=2) + '\n')
        if record['status'] == 'passed':
            LATEST.write_text(json.dumps(record, indent=2) + '\n')
        print('Local verification report:', run_dir / 'result.json')
    if record['status'] != 'passed':
        raise RuntimeError(record.get('error', 'Local test suite failed'))

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('action', choices=['run', 'verify', 'install-hook'])
    parser.add_argument('--keep', action='store_true', help='retain only this run\'s environment for diagnosis')
    args = parser.parse_args()
    if args.action == 'verify':
        verify()
    elif args.action == 'install-hook':
        command(['git', 'config', '--local', 'core.hooksPath', '.githooks'])
        print('Installed repository-local pre-push verification hook')
    else:
        run(args.keep)

if __name__ == '__main__':
    try:
        main()
    except (RuntimeError, subprocess.CalledProcessError) as error:
        print('Local verification failed:', error, file=sys.stderr)
        raise SystemExit(1)
