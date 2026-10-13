"""Exercise the shipping Compose package in the disposable Engine only."""
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import secrets
import shutil
import socket
import subprocess
import time
import urllib.request

ROOT = Path('/workspace')
spec = importlib.util.spec_from_file_location('installation', ROOT / 'scripts/install.py')
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)


def run():
    endpoint = os.environ.get('OAP_TEST_DOCKER_SOCKET', '')
    if endpoint != '/run/oap-test/docker.sock' or not Path('/report/fixtures.tar').exists():
        raise RuntimeError('Installation drill requires the disposable test daemon/report')
    os.environ['DOCKER_HOST'] = 'unix://' + endpoint
    os.environ.pop('DOCKER_CONTEXT', None)
    folder = Path('/report/installation')
    project = None
    restored_container = None
    app = None
    external_db = None

    def native(args, data=None):
        result = subprocess.run(['docker'] + args, input=data, capture_output=True, timeout=90)
        if result.returncode:
            raise RuntimeError('Installation fixture native command failed: ' + args[0])
        return result.stdout

    def inspect(ref):
        return json.loads(native(['inspect', ref]))[0]

    def free_port():
        with socket.socket() as listener:
            listener.bind(('127.0.0.1', 0))
            return listener.getsockname()[1]

    def values():
        return dict(line.split('=', 1) for line in (folder / '.env').read_text().splitlines() if line and not line.startswith('#'))

    def set_env(key, value):
        lines = [line for line in (folder / '.env').read_text().splitlines() if not line.startswith(key + '=')]
        (folder / '.env').write_text('\n'.join(lines + [key + '=' + value]) + '\n')
        (folder / '.env').chmod(0o600)

    def compose(args, data=None):
        return installer.compose(folder, args, data)

    def ready():
        for _ in range(80):
            try:
                with urllib.request.urlopen(base + '/healthz', timeout=1) as response:
                    if response.status == 200:
                        return
            except Exception:
                pass
            time.sleep(.25)
        raise RuntimeError('Packaged controller did not become ready')

    def browser(phase):
        env = os.environ.copy()
        env.update(OAP_BASE_URL=base, OAP_INSTALL_PHASE=phase,
                   OAP_SETUP_TOKEN=values()['OAP_SETUP_TOKEN'],
                   OAP_INSTALL_RECEIPT=str(folder / 'application.json'),
                   OAP_INSTALL_DATABASE_ID=external_db,
                   OAP_INSTALL_FRONTEND_PORT=str(frontend_port), OAP_INSTALL_REPORT=str(folder))
        result = subprocess.run(['node', 'node_modules/@playwright/test/cli.js', 'test', 'tests/installation.spec.ts'],
                                cwd=ROOT / 'web', env=env, capture_output=True, timeout=110)
        (folder / ('browser-' + phase + '.log')).write_bytes(result.stdout + result.stderr)
        if result.returncode:
            raise RuntimeError('Packaged owner browser phase failed: ' + phase)
        print('Packaged owner browser phase passed:', phase, flush=True)

    def workload_ids():
        out = {}
        for component in ('frontend', 'api', 'worker'):
            ids = native(['ps', '-q', '--filter', 'label=io.openappplatform.owner=OpenAppPlatform:' + app['id'] + ':' + component]).decode().split()
            if len(ids) != 1:
                raise RuntimeError('Expected one running owned workload per component')
            out[component] = ids[0]
        return out

    def same_workloads(before):
        if workload_ids() != before:
            raise RuntimeError('Controller update/recovery changed native workload identity')

    def preflight(database='oap'):
        password = values()['POSTGRES_PASSWORD']
        compose(['run', '--rm', '--no-deps', '-T', '-e', f'DATABASE_URL=postgres://oap:{password}@postgres:5432/{database}?sslmode=disable',
                 '--entrypoint', '/app/oap-admin', 'platform', 'check-schema'])

    try:
        runtime = json.loads(native(['image', 'inspect', os.environ['OAP_TEST_PLATFORM_IMAGE']]))[0]['Id']
        postgres = json.loads(native(['image', 'inspect', os.environ['OAP_TEST_DATABASE_IMAGE']]))[0]['Id']
        frontend_port, platform_port = free_port(), free_port()
        base = 'http://127.0.0.1:' + str(platform_port)
        installer.init(folder, runtime, endpoint, port=platform_port)
        set_env('OAP_POSTGRES_IMAGE', postgres)
        project = values()['COMPOSE_PROJECT_NAME']
        password = secrets.token_hex(16)
        external_db = native(['run', '-d', '--name', project + '-existing-database',
                              '-e', 'POSTGRES_USER=fixture_admin', '-e', 'POSTGRES_DB=example', '-e', 'POSTGRES_PASSWORD=' + password,
                              '--health-cmd', 'pg_isready -U fixture_admin -d example', '--health-interval', '1s', '--health-retries', '30',
                              os.environ['OAP_TEST_DATABASE_IMAGE']]).decode().strip()
        for _ in range(80):
            if inspect(external_db)['State'].get('Health', {}).get('Status') == 'healthy':
                break
            time.sleep(.25)
        else:
            raise RuntimeError('Existing database fixture did not become healthy')
        reader_password = secrets.token_hex(16)
        seed = "CREATE TABLE example_marker(value text); INSERT INTO example_marker VALUES('unchanged'); CREATE ROLE fixture_reader LOGIN PASSWORD '" + reader_password + "'; GRANT CONNECT ON DATABASE example TO fixture_reader; ALTER ROLE fixture_reader SET default_transaction_read_only=on;"
        native(['exec', '-i', external_db, 'psql', '-X', '-v', 'ON_ERROR_STOP=1', '-U', 'fixture_admin', '-d', 'example'], seed.encode())
        targets = json.loads((folder / 'targets.json').read_text())
        targets[0]['settings']['observeContainers'] = [external_db]
        (folder / 'targets.json').write_text(json.dumps(targets))
        compose(['up', '-d', '--wait', '--wait-timeout', '90'])
        ready()
        platform_id = compose(['ps', '-q', 'platform']).decode().strip()
        info = inspect(platform_id)
        if info['Config']['User'] != 'platform' or not info['HostConfig']['ReadonlyRootfs']:
            raise RuntimeError('Installation did not retain non-root/read-only runtime')
        browser('bootstrap')
        app = json.loads((folder / 'application.json').read_text())
        secret_dir = folder / 'secret-store'
        secret_dir.mkdir(mode=0o700)
        secret_dir.chmod(0o700)
        native(['run','--rm','--network','none','--user','0','--mount','type=bind,src='+str(secret_dir)+',dst=/secrets','--entrypoint','/bin/chown',runtime,'10001:10001','/secrets'])
        secret_cli=['run','--rm','-i','--network','none','--mount','type=bind,src='+str(secret_dir)+',dst=/secrets','-e','OAP_SECRET_STORE_FILE=/secrets/store.json','-e','OAP_SECRET_KEY_FILE=/secrets/key','--entrypoint','/app/oap-admin',runtime]
        native(secret_cli+['secret-store-init'])
        secret_record={'reference':'stored-proof-v1','targetId':'docker-local','applicationId':app['id'],'component':'worker','variable':'PROOF_TOKEN','value':'installation-private-stored-token'}
        native(secret_cli+['secret-put'],json.dumps(secret_record).encode())
        if b'installation-private-stored-token' in (secret_dir/'store.json').read_bytes():
            raise RuntimeError('Stored secret was not encrypted')
        shutil.copyfile('/workspace/deploy/compose.secrets.yaml',folder/'compose.secrets.yaml')
        set_env('COMPOSE_FILE',values()['COMPOSE_FILE']+':compose.secrets.yaml')
        network = 'oap-docker-local-' + app['id'][:12]
        # Native owner prepares reachability; database linking itself never joins networks.
        native(['network', 'create', '--label', 'io.openappplatform.target=docker-local',
                '--label', 'io.openappplatform.application=' + app['id'], '--label', 'io.openappplatform.environment=staging', network])
        native(['network', 'connect', '--alias', 'database', network, external_db])
        baseline_db = hashlib.sha256(json.dumps(inspect(external_db)['Config'], sort_keys=True).encode()).hexdigest()
        grants = [{'reference': 'install-database-v1', 'targetId': 'docker-local', 'applicationId': app['id'],
                   'component': name, 'variable': 'DATABASE_URL', 'valueEnv': 'OAP_SECRET_INSTALL_DATABASE'} for name in ('api', 'worker')]
        set_env('OAP_RUNTIME_INPUTS', "'" + json.dumps(grants) + "'")
        set_env('OAP_SECRET_INSTALL_DATABASE', f'postgres://fixture_reader:{reader_password}@database:5432/example?sslmode=disable')
        compose(['up', '-d', '--no-deps', '--force-recreate', '--wait', '--wait-timeout', '90', 'platform'])
        ready()
        browser('deploy')
        before = workload_ids()
        platform_id = compose(['ps', '-q', 'platform']).decode().strip()
        # Signal the Go process through exec; Docker's administrative kill can
        # suppress restart policy and does not model a process crash.
        fault = subprocess.run(['docker','exec',platform_id,'/bin/kill','-QUIT','1'], capture_output=True, timeout=15)
        if fault.returncode not in (0,137,143):
            raise RuntimeError('Could not inject the controller process fault')
        for _ in range(80):
            state = inspect(platform_id)
            if state['State']['Running'] and state['RestartCount'] >= 1:
                break
            time.sleep(.25)
        else:
            raise RuntimeError('Controller restart policy did not recover a process crash')
        ready()
        same_workloads(before)
        browser('verify')
        compose(['stop', 'platform'])
        archive = folder / 'metadata.dump'
        installer.backup(folder, archive)
        corrupt = folder / 'corrupt.dump'
        corrupt.write_bytes(archive.read_bytes()[:20])
        corrupt.with_suffix('.dump.json').write_bytes(archive.with_suffix('.dump.json').read_bytes())
        try:
            installer.restore_check(folder, corrupt, 'oap_restore_corrupt')
        except ValueError:
            pass
        else:
            raise RuntimeError('Corrupt metadata archive accepted')
        restored = installer.restore_check(folder, archive, 'oap_restore_verified')
        preflight('oap_restore_verified')
        # Same binary/schema, different immutable image: a compatible image replacement.
        candidate = native(['create', runtime]).decode().strip()
        upgrade = native(['commit', '--change', 'LABEL io.openappplatform.install-fixture=upgrade', candidate]).decode().strip()
        native(['rm', candidate])
        set_env('OAP_IMAGE', upgrade)
        preflight()
        compose(['up', '-d', '--no-deps', '--wait', '--wait-timeout', '90', 'platform'])
        ready()
        same_workloads(before)
        compose(['stop', 'platform'])
        set_env('OAP_IMAGE', runtime)
        preflight()
        compose(['up', '-d', '--no-deps', '--wait', '--wait-timeout', '90', 'platform'])
        ready()
        same_workloads(before)
        browser('verify')
        browser('backup-reports')
        compose(['stop', 'platform'])
        restored_container = project + '-restored-controller'
        password = values()['POSTGRES_PASSWORD']
        compose(['run', '-d', '--no-deps', '--service-ports', '--name', restored_container,
                 '-e', f'DATABASE_URL=postgres://oap:{password}@postgres:5432/oap_restore_verified?sslmode=disable', 'platform'])
        ready()
        same_workloads(before)
        browser('verify')
        native(['stop', restored_container])
        current_db = hashlib.sha256(json.dumps(inspect(external_db)['Config'], sort_keys=True).encode()).hexdigest()
        marker = native(['exec', external_db, 'psql', '-X', '-U', 'fixture_admin', '-d', 'example', '-Atc', 'SELECT value FROM example_marker']).decode().strip()
        if current_db != baseline_db or marker != 'unchanged':
            raise RuntimeError('Existing application database changed')
        receipt = {'status': 'passed', 'freshOwnerSetup': True, 'nonRootSocketAccess': True,
                   'fourComponents': True, 'healthLogsRestart': True, 'processCrashRecovered': True,
                   'compatibleImageReplacementAndRollback': True, 'workloadIDsPreserved': True,
                   'corruptArchiveRejected': True, 'metadataRestoreAndOwnerLogin': True,
                   'existingApplicationDataUnchanged': True, 'restore': restored}
        (folder / 'acceptance.json').write_text(json.dumps(receipt, indent=2) + '\n')
        print('Packaged installation, controller crash, compatible update/rollback and isolated metadata restore passed.', flush=True)
    except Exception as error:
        details = getattr(error, 'detail', str(error))
        if project:
            command = ['docker','compose','--project-directory',str(folder),'logs','--no-color','--tail','50']
            logs = subprocess.run(command,capture_output=True,timeout=20,cwd=folder)
            details += "\n" + (logs.stdout+logs.stderr).decode(errors='replace')
        if folder.exists() and (folder/'.env').exists():
            for key,value in values().items():
                if 'PASSWORD' in key or 'TOKEN' in key or key.startswith('OAP_SECRET_'):
                    details = details.replace(value.strip("'"), '[REDACTED]')
            (folder/'failure.log').write_text(details)
            (folder/'failure.log').chmod(0o600)
        raise
    finally:
        if restored_container:
            native(['rm', '-f', restored_container])
        if project:
            compose(['down', '--volumes', '--remove-orphans'])
        if app:
            for component in ('frontend', 'api', 'worker'):
                ids = native(['ps', '-aq', '--filter', 'label=io.openappplatform.owner=OpenAppPlatform:' + app['id'] + ':' + component]).decode().split()
                if ids:
                    native(['rm', '-f'] + ids)
        if external_db:
            native(['rm', '-f', '-v', external_db])


if __name__ == '__main__':
    run()
