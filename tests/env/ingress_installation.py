"""Fresh packaged ingress installation through the documented public CLI flow."""
import hashlib
import http.client
import json
import os
from pathlib import Path
import secrets
import socket
import ssl
import subprocess
import time

from ingress_footprint import measure


def run():
    endpoint = os.environ.get('OAP_TEST_DOCKER_SOCKET')
    if endpoint != '/run/oap-test/docker.sock':
        raise RuntimeError('Ingress installation acceptance requires the isolated Engine')
    os.environ['DOCKER_HOST'] = 'unix://' + endpoint
    os.environ.pop('DOCKER_CONTEXT', None)
    clean = {k: v for k, v in os.environ.items() if k not in ('OAP_API_TOKEN', 'OAP_CREDENTIAL_FILE', 'OAP_SESSION_FILE', 'OAP_URL')}
    folder = Path('/report/ingress-installation')
    project = None
    app = None
    workload = None

    def native(args):
        result = subprocess.run(['docker', *args], capture_output=True, timeout=120)
        if result.returncode:
            raise RuntimeError('Ingress fixture native command failed: ' + args[0])
        return result.stdout

    def port():
        with socket.socket() as s:
            s.bind(('127.0.0.1', 0))
            return s.getsockname()[1]

    def install(action, *args, body=None):
        result = subprocess.run(['python3', 'scripts/install.py', action, '--json', '--diagnostics', '--directory', str(folder), *args],
                                input=None if body is None else json.dumps(body), text=True,
                                capture_output=True, env=clean, timeout=180)
        value = json.loads(result.stdout)
        if result.returncode or not value.get('ok'):
            if folder.exists():
                debug = subprocess.run(['docker','compose','--project-directory',str(folder),'logs','--no-color','ingress-init','ingress','platform','postgres'],capture_output=True,timeout=30)
                diagnostic=folder/'ingress-fixture-private.log'
                status = subprocess.run(['docker','compose','--project-directory',str(folder),'ps','--all'],capture_output=True,timeout=30)
                diagnostic.write_bytes(debug.stdout+debug.stderr+status.stdout+status.stderr);diagnostic.chmod(0o600)
            raise RuntimeError('Packaged ingress installer failed: ' + action + '/' + value.get('error', {}).get('code', 'failed'))
        return value

    api_port, public_http_port, public_https_port = port(), port(), port()
    base = 'http://127.0.0.1:' + str(api_port)
    hostname = 'installed.example.test'
    owner, agent = folder / 'owner.json', folder / 'agent.json'

    def cli(identity, command, *flags, body=None):
        args = ['/opt/oap/oap', '--url', base, '--credential-file', str(identity), *flags]
        if body is not None:
            args += ['--file', '-']
        result = subprocess.run([*args, command], input=None if body is None else json.dumps(body), text=True,
                                capture_output=True, env=clean, timeout=130)
        value = json.loads(result.stdout)
        if result.returncode:
            raise RuntimeError('Ingress installation CLI failed: ' + command + '/' + value.get('error', {}).get('code', 'failed'))
        if '"token":' in result.stdout or '"session":' in result.stdout:
            raise RuntimeError('Credential printed by public CLI')
        return value

    def compose(*args):
        return native(['compose', '--project-directory', str(folder), *args])

    def plan(request):
        return cli(agent, 'action-plan', body=request)['data']

    def execute(value):
        return cli(agent, 'execute-action', '--plan', value['id'], body={})['data']['result']

    try:
        runtime = native(['image', 'inspect', os.environ['OAP_TEST_PLATFORM_IMAGE'], '--format', '{{.Id}}']).decode().strip()
        database = native(['image', 'inspect', os.environ['OAP_TEST_DATABASE_IMAGE'], '--format', '{{.Id}}']).decode().strip()
        caddy = native(['image', 'inspect', os.environ['OAP_TEST_CADDY_IMAGE'], '--format', '{{.Id}}']).decode().strip()
        image = native(['image', 'inspect', os.environ['OAP_TEST_DOCKER_IMAGE'], '--format', '{{.Id}}']).decode().strip()
        install('init', '--image', runtime, '--docker-socket', endpoint, '--port', str(api_port),
                '--postgres-image', database, '--ingress-image', caddy, '--ingress-domain', hostname,
                '--ingress-tls', 'internal', '--ingress-http-port', str(public_http_port), '--ingress-https-port', str(public_https_port))
        values = dict(line.split('=', 1) for line in (folder / '.env').read_text().splitlines() if line)
        project = values['COMPOSE_PROJECT_NAME']
        assert 'ingress' not in json.loads((folder / 'targets.json').read_text())[0]['settings']
        install('start')
        assert install('check')['healthy']
        targets = json.loads((folder / 'targets.json').read_text())
        authority = targets[0]['settings']['ingress']
        peer = authority['containerId']
        original_authority = (folder / 'targets.json').read_bytes()
        install('start')
        assert (folder / 'targets.json').read_bytes() == original_authority
        assert compose('ps', '-q', 'ingress').decode().strip() == peer
        info = json.loads(native(['inspect', peer]))[0]
        assert info['Config']['User'] == '10001:10001' and info['HostConfig']['ReadonlyRootfs']
        assert not info['HostConfig']['Privileged'] and '2019/tcp' not in info['HostConfig']['PortBindings']
        assert all(m['Destination'] != '/var/run/docker.sock' for m in info['Mounts'])
        database_peer=json.loads(native(['inspect',compose('ps','-q','postgres').decode().strip()]))[0]
        assert not set(info['NetworkSettings']['Networks']).intersection(database_peer['NetworkSettings']['Networks'])
        spec = {'apiVersion': 'oap.dev/v1alpha1', 'kind': 'Application', 'spec': {
            'name': 'installed-ingress', 'environment': 'staging', 'targetId': 'docker-local',
            'components': [{'name': 'web', 'kind': 'web', 'image': image, 'port': 8080, 'instances': 1,
                            'readiness': {'requireHealthy': True, 'timeoutSeconds': 60}}],
            'ingress': {'domain': hostname, 'https': True, 'routes': [{'path': '/', 'component': 'web'}]}}}
        spec_file = folder / 'application.json'
        spec_file.write_text(json.dumps(spec))
        owner_input = folder / 'private-owner.json'
        owner_input.write_text(json.dumps({'email':'ingress-owner@example.invalid','name':'Owner','password':secrets.token_urlsafe(32)}))
        owner_input.chmod(0o600)
        # Human-owner fixture establishes trust through product primitives; the
        # agent runner receives only the resulting scoped credential.
        install('owner-setup', '--cli-bin', '/opt/oap/oap', '--out', str(owner), body=json.loads(owner_input.read_text()))
        cli(owner, 'grant', '--out', str(agent), body={
            'name': 'Standalone acceptance agent', 'targetId': 'docker-local',
            'environment': 'staging', 'maxApplications': 1,
            'actions': ['configure', 'deploy', 'restart', 'recover']})
        acceptance = ['python3','scripts/standalone-acceptance.py']
        agent_work = folder / 'agent-work'
        agent_work.mkdir(mode=0o700)
        flags = ['--directory',str(agent_work),'--spec',str(spec_file),'--cli-bin','/opt/oap/oap','--url',base]
        deployed = subprocess.run([*acceptance,'deploy',*flags,'--credential-file',str(agent)],capture_output=True,text=True,env=clean,timeout=180)
        if deployed.returncode: raise RuntimeError('Documented CLI acceptance deployment failed: '+deployed.stdout)
        result=json.loads(deployed.stdout)
        app={'applicationId':result['applicationId']}
        assert result['replaySameOperation']
        root = folder / 'fixture-root.crt'
        compose('cp', 'ingress:/data/caddy/pki/authorities/local/root.crt', str(root))
        context = ssl.create_default_context(cafile=str(root))

        def request(host=hostname):
            connection = http.client.HTTPSConnection(host, public_https_port, context=context, timeout=5)
            raw = socket.create_connection(('127.0.0.1', public_https_port), timeout=5)
            connection.sock = context.wrap_socket(raw, server_hostname=hostname)
            certificate = hashlib.sha256(connection.sock.getpeercert(binary_form=True)).hexdigest()
            try:
                connection.request('GET', '/', headers={'Host': host})
                response = connection.getresponse()
                body = response.read()
                return response.status, body, certificate
            finally:
                connection.close()

        for _ in range(60):
            try:
                status, body, certificate = request()
                if status == 200:
                    break
            except (OSError, ssl.SSLError):
                pass
            time.sleep(.5)
        else:
            raise RuntimeError('Installed trusted HTTPS did not become ready')
        checked=subprocess.run([*acceptance,'verify',*flags,'--origin','https://'+hostname+':'+str(public_https_port),'--ca-file',str(root),'--connect-host','127.0.0.1'],capture_output=True,text=True,env=clean,timeout=60)
        if checked.returncode: raise RuntimeError('Documented trusted HTTPS acceptance failed: '+checked.stdout)
        assert json.loads(body)['version'] == 'v1'
        assert request('foreign.example.test')[0] == 404
        workload = native(['ps', '-q', '--filter', 'label=io.openappplatform.owner=OpenAppPlatform:' + app['applicationId'] + ':web']).decode().strip()
        def identity():
            return native(['inspect', workload, '--format', '{{.Id}} {{.State.StartedAt}}']).decode().strip()
        initial = identity()
        footprint = measure(endpoint, native, compose, request, os.environ.get('OAP_TEST_RUNTIME_COMMIT', 'unrecorded'))
        print('Packaged ingress footprint measured: ' + json.dumps(footprint), flush=True)
        compose('stop', 'platform')
        for _ in range(20):
            assert request()[0] == 200
        install('recover')
        assert initial == identity()
        compose('restart', 'ingress')
        for _ in range(60):
            try:
                status, body, after_certificate = request()
                if status == 200:
                    break
            except (OSError, ssl.SSLError):
                pass
            time.sleep(.5)
        else:
            raise RuntimeError('Installed proxy resume failed')
        assert after_certificate == certificate
        assert compose('ps', '-q', 'ingress').decode().strip() == peer
        install('start')
        assert (folder / 'targets.json').read_bytes() == original_authority
        cli(agent, 'verify', '--app', app['applicationId'])
        receipt = {'status': 'passed', 'freshPackagedInstallation': True, 'scopedAgentPlanApply': True,
                   'verifiedLocalHTTPS': True, 'privateProxyAdministration': True, 'controllerOutageRequests': 20,
                   'controllerOutageFailures': 0, 'workloadIdentityUnchanged': True, 'proxyResume': True,
                   'certificatePersisted': True, 'proxyAuthorityUnchanged': True, 'publicACMEVerified': False,
                   'footprintReport': footprint['report']}
        Path('/report/ingress-installation.json').write_text(json.dumps(receipt, indent=2) + '\n')
        print(json.dumps(receipt), flush=True)
    finally:
        if project:
            if workload: subprocess.run(['docker','rm','-f',workload],capture_output=True,timeout=30)
            compose('down', '--volumes', '--remove-orphans')
            if app: subprocess.run(['docker','network','rm','oap-docker-local-'+app['applicationId'][:12]],capture_output=True,timeout=30)


if __name__ == '__main__':
    run()
