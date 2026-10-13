"""M2.6 shipping install and named-agent journey: no browser or operator account."""
import importlib.util
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import time
import urllib.request

ROOT = Path('/workspace')
spec = importlib.util.spec_from_file_location('installer', ROOT / 'scripts/install.py')
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)


def run():
    socket_path = os.environ.get('OAP_TEST_DOCKER_SOCKET')
    if socket_path != '/run/oap-test/docker.sock':
        raise RuntimeError('Headless acceptance requires the isolated daemon')
    os.environ['DOCKER_HOST'] = 'unix://' + socket_path
    os.environ['DOCKER_BUILDKIT'] = '0'
    os.environ.pop('DOCKER_CONTEXT', None)
    folder = Path('/report/headless-install')
    os.environ.pop('OAP_CREDENTIAL_FILE', None)
    os.environ.pop('OAP_SESSION_FILE', None)
    clean = {key: value for key, value in os.environ.items() if key != 'OAP_API_TOKEN'}

    def native(args, data=None):
        result = subprocess.run(['docker', *args], input=data, capture_output=True, timeout=90)
        if result.returncode:
            raise RuntimeError('Headless native command failed: ' + args[0])
        return result.stdout

    def free_port():
        with socket.socket() as listener:
            listener.bind(('127.0.0.1', 0))
            return listener.getsockname()[1]

    def install(action, *args, body=None, code=0):
        result = subprocess.run(['python3', 'scripts/install.py', action, '--json', '--directory', str(folder), *args], input=None if body is None else json.dumps(body), env=clean, text=True, capture_output=True, timeout=180)
        value = json.loads(result.stdout)
        if result.returncode != code:
            raise RuntimeError('Headless installation failed: ' + action + '/' + value.get('error', {}).get('code', 'unexpected_exit'))
        return value

    base = 'http://127.0.0.1:' + str(free_port())
    owner = folder / 'owner.json'
    agent = folder / 'agent.json'

    def cli(identity, *args, body=None, code=0):
        argv = ['/opt/oap/oap', '--url', base]
        if identity:
            argv += ['--credential-file', str(identity)]
        result = subprocess.run([*argv, *args], input=None if body is None else json.dumps(body), env=clean, text=True, capture_output=True, timeout=90)
        value = json.loads(result.stdout)
        if result.returncode != code:
            raise RuntimeError('Headless CLI failed: ' + args[-1] + '/' + value.get('error', {}).get('code', 'unexpected_exit'))
        if '"token":' in result.stdout or '"session":' in result.stdout:
            raise RuntimeError('Credential leaked to stdout')
        return value

    project = None
    try:
        runtime = json.loads(native(['image', 'inspect', os.environ['OAP_TEST_PLATFORM_IMAGE']]))[0]['Id']
        postgres = json.loads(native(['image', 'inspect', os.environ['OAP_TEST_DATABASE_IMAGE']]))[0]['Id']
        install('init', '--image', runtime, '--docker-socket', socket_path, '--port', base.rsplit(':', 1)[1])
        values = dict(line.split('=', 1) for line in (folder / '.env').read_text().splitlines() if line)
        values['OAP_POSTGRES_IMAGE'] = postgres
        installer.rewrite_values(folder, values)
        project = values['COMPOSE_PROJECT_NAME']
        # Add one inaccessible sibling target to prove scoped enumeration.
        targets = json.loads((folder / 'targets.json').read_text())
        targets.append(dict(targets[0], id='other-local', name='Sibling local target'))
        (folder / 'targets.json').write_text(json.dumps(targets))
        base_tag = 'oap-headless-base:' + project
        candidate_tag = 'oap-headless-candidate:' + project
        native(['tag', runtime, base_tag])
        dockerfile = ('FROM ' + base_tag + '\nLABEL io.openappplatform.test-cache=true io.openappplatform.headless-variant=' + project + '\n').encode()
        native(['build', '--network=none', '--pull=false', '-t', candidate_tag, '-'], dockerfile)
        candidate = json.loads(native(['image', 'inspect', candidate_tag]))[0]['Id']
        assert candidate != runtime
        install('start')
        assert install('check')['healthy']
        install('owner-setup', '--cli-bin', '/opt/oap/oap', '--out', str(owner), body={'email': 'headless@example.invalid', 'name': 'Owner', 'password': secrets.token_urlsafe(24)})
        issued = cli(owner, '--out', str(agent), '--file', '-', 'grant', body={'name': 'Standalone lifecycle agent', 'targetId': 'docker-local', 'environment': 'staging', 'maxApplications': 1, 'actions': ['deploy', 'restart', 'configure', 'recover']})['data']
        assert [target['id'] for target in cli(agent, 'targets')['data']] == ['docker-local']
        cli(agent, '--target', 'other-local', 'resources', code=3)
        image_id = json.loads(native(['image', 'inspect', os.environ['OAP_TEST_DOCKER_IMAGE']]))[0]['Id']
        manifest = {'name': 'headless-owned', 'environment': 'staging', 'targetId': 'docker-local', 'components': [{'name': 'web', 'kind': 'web', 'image': os.environ['OAP_TEST_DOCKER_IMAGE'], 'port': 8080, 'hostPort': free_port(), 'instances': 1, 'strategy': 'standard', 'env': {'OAP_TEST_MESSAGE': 'headless-agent'}}]}
        app = cli(agent, '--key', 'headless-create', '--file', '-', 'create', body=manifest)['data']
        assert cli(agent, '--key', 'headless-create', '--file', '-', 'create', body=manifest)['data']['id'] == app['id']
        # Known image content IDs are valid baseline deployments; plans require
        # registry digest references, so the exact-review path is separately
        # covered by the owner-mode system integration test with a fake provider.
        release = cli(agent, '--app', app['id'], '--key', 'headless-release', '--file', '-', 'deploy', body={'expectedVersion': app['version']})
        cli(agent, '--operation', release['operationId'], '--timeout', '60s', 'wait')
        proof = cli(agent, '--app', app['id'], 'verify')['data']
        url = proof['instances'][0]['resource']['url']
        with urllib.request.urlopen(url, timeout=5) as response:
            original_behavior = json.load(response)
            assert original_behavior['message'] == 'headless-agent'
        before = [instance['resourceId'] for instance in proof['instances']]
        def boot_identity():
            return [(item['Id'], item['State']['StartedAt']) for item in json.loads(native(['inspect', *before]))]
        native_identity = boot_identity()
        install('update', '--image', candidate)
        assert boot_identity() == native_identity
        updated_values = dict(line.split('=', 1) for line in (folder / '.env').read_text().splitlines() if line)
        assert updated_values['OAP_IMAGE'] == candidate and updated_values['OAP_PREVIOUS_IMAGE'] == runtime
        assert [instance['resourceId'] for instance in cli(agent, '--app', app['id'], 'verify')['data']['instances']] == before
        # Host access, not an OAP token, authorizes this external controller rescue.
        installer.compose(folder, ['stop', 'platform'])
        assert not install('check', code=4)['healthy']
        install('recover')
        assert boot_identity() == native_identity
        assert [instance['resourceId'] for instance in cli(agent, '--app', app['id'], 'verify')['data']['instances']] == before
        with urllib.request.urlopen(url, timeout=5) as response:
            assert json.load(response) == original_behavior
        control = cli(owner, 'agent-control')['data']
        cli(owner, '--file', '-', 'set-agent-control', body={'expectedVersion': control['version'], 'enabled': False, 'reason': 'Headless acceptance pause'})
        cli(agent, 'apps', code=3)
        cli(owner, '--file', '-', 'set-agent-control', body={'expectedVersion': control['version'] + 1, 'enabled': True, 'reason': 'Resume acceptance'})
        cli(owner, '--credential', issued['credentialId'], 'revoke')
        cli(agent, 'apps', code=3)
        report = {'schemaVersion': 1, 'status': 'passed', 'authority': 'named-agent-owner-mode', 'browserRequired': False, 'operatorRequired': False, 'applicationId': app['id'], 'operationId': release['operationId'], 'checks': ['private_headless_owner_setup', 'bounded_agent_grant', 'target_scope', 'idempotent_app_creation', 'agent_deployment', 'live_http_behavior', 'compatible_update', 'external_controller_rescue', 'unchanged_workload_identity', 'pause_resume', 'revocation'], 'reviewedPlans': 'owner-mode system suite', 'imageContentId': image_id}
        Path('/report/headless-acceptance.json').write_text(json.dumps(report, indent=2) + '\n')
        print('Headless standalone owner and named-agent acceptance passed', flush=True)
    finally:
        if project:
            # Only this isolated installation and synthetic app containers are removed.
            result = subprocess.run(['docker', 'ps', '-aq', '--filter', 'label=io.openappplatform.owner=OpenAppPlatform:' + app['id'] + ':web' if 'app' in locals() else 'label=io.openappplatform.owner=not-created'], capture_output=True)
            ids = result.stdout.decode().split()
            if ids:
                subprocess.run(['docker', 'rm', '-f', *ids], capture_output=True)
            installer.compose(folder, ['down', '--volumes', '--remove-orphans'])


if __name__ == '__main__':
    run()
