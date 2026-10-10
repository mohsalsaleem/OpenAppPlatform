#!/usr/bin/env python3
"""Opt-in GET-only Coolify acceptance with a disposable PostgreSQL database."""
import argparse
import json
import os
from pathlib import Path
import secrets
import subprocess
import tempfile
import time

ROOT = Path(__file__).resolve().parent.parent

def run(args, **kwargs):
    return subprocess.run(args, check=True, **kwargs)

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image-resource', required=True)
    parser.add_argument('--source-resource', required=True)
    args = parser.parse_args()
    required = ['COOLIFY_URL', 'COOLIFY_TOKEN', 'COOLIFY_PROJECT_ID', 'COOLIFY_SERVER_ID', 'COOLIFY_ENVIRONMENT']
    if any(not os.environ.get(key) for key in required) or os.environ['COOLIFY_ENVIRONMENT'] != 'staging':
        raise SystemExit('Configured Coolify staging scope and token required; use scripts/run-local.py')
    # Reuse an exact-tree passing image. Never download an unknown test image.
    import importlib.util
    spec = importlib.util.spec_from_file_location('test_env', ROOT / 'scripts/test-env.py')
    harness = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(harness)
    harness.verify()
    record = json.loads((ROOT / '.local/test-env/last-pass.json').read_text())
    runner = 'openappplatform-test:' + record['fingerprint'][:16]
    name = 'oap-acceptance-' + secrets.token_hex(6)
    network, postgres = name + '-network', name + '-postgres'
    created_network = created_postgres = False
    try:
        run(['docker', 'network', 'create', network], stdout=subprocess.DEVNULL)
        created_network = True
        run(['docker', 'run', '--pull=never', '-d', '--name', postgres, '--network', network,
             '--tmpfs', '/var/lib/postgresql/data:rw,size=268435456',
             '-e', 'POSTGRES_DB=oap_test', '-e', 'POSTGRES_USER=oap', '-e', 'POSTGRES_PASSWORD=oap-test-only',
             record['images']['OAP_TEST_POSTGRES_IMAGE']], stdout=subprocess.DEVNULL)
        created_postgres = True
        for _ in range(30):
            ready = subprocess.run(['docker', 'exec', postgres, 'pg_isready', '-U', 'oap', '-d', 'oap_test'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            if ready.returncode == 0:
                break
            time.sleep(1)
        else:
            raise RuntimeError('Disposable PostgreSQL did not become ready')
        with tempfile.TemporaryDirectory(prefix='oap-acceptance-') as directory:
            env_file = Path(directory) / 'environment'
            values = {key: os.environ[key] for key in required}
            values.update(OAP_LIVE_COOLIFY='1', OAP_ACCEPTANCE_IMAGE_RESOURCE=args.image_resource,
                          OAP_ACCEPTANCE_SOURCE_RESOURCE=args.source_resource,
                          TEST_DATABASE_URL=f'postgres://oap:oap-test-only@{postgres}:5432/oap_test?sslmode=disable')
            if any('\n' in value or '\r' in value for value in values.values()):
                raise RuntimeError('Invalid multiline acceptance configuration')
            env_file.write_text(''.join(f'{key}={value}\n' for key, value in values.items()))
            env_file.chmod(0o600)
            # No Docker socket, host source files or durable database are exposed.
            run(['docker', 'run', '--pull=never', '--rm', '--network', network, '--env-file', str(env_file),
                 '--entrypoint', 'node', runner, '-e',
                 "const fs=require('fs'),cp=require('child_process');const p=fs.readFileSync('/opt/oap/packages.txt','utf8').trim().split('\\n');const i=p.indexOf('github.com/mohsalsaleem/OpenAppPlatform/tests/system');if(i<0)throw Error('System test binary missing');cp.execFileSync('/opt/oap/tests/'+i+'.test',['-test.v','-test.run=^TestLiveCoolifyObserveOnlyAssembly$','-test.timeout=3m'],{stdio:'inherit'});"])
    finally:
        if created_postgres:
            run(['docker', 'rm', '-f', postgres], stdout=subprocess.DEVNULL)
        if created_network:
            run(['docker', 'network', 'rm', network], stdout=subprocess.DEVNULL)

if __name__ == '__main__':
    main()
