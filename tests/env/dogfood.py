"""M2.9 fresh headless standalone install-to-fix and measured 10-app profile."""
import copy
import importlib.util
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import time
import urllib.request

from footprint import Sampler, SocketProxy, docker_json

ROOT = Path('/workspace')
spec = importlib.util.spec_from_file_location('installer', ROOT/'scripts/install.py')
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)


def run():
    endpoint = os.environ.get('OAP_TEST_DOCKER_SOCKET')
    if endpoint != '/run/oap-test/docker.sock':
        raise RuntimeError('Dogfood requires the isolated test Engine')
    os.environ['DOCKER_HOST'] = 'unix://' + endpoint
    os.environ['DOCKER_BUILDKIT'] = '0'
    os.environ.pop('DOCKER_CONTEXT', None)
    clean = {k: v for k, v in os.environ.items() if k not in ('OAP_API_TOKEN', 'OAP_CREDENTIAL_FILE', 'OAP_SESSION_FILE', 'OAP_URL')}
    folder = Path('/report/dogfood-install')
    owner, agent = folder/'owner.json', folder/'agent.json'
    apps, project, restored = [], None, None
    proxy = SocketProxy('/run/oap-test/dogfood-proxy.sock', endpoint)
    phases, operations, latency = [], [], {}

    def native(args, data=None):
        result = subprocess.run(['docker', *args], input=data, capture_output=True, timeout=90)
        if result.returncode:
            raise RuntimeError('Dogfood native command failed: ' + args[0])
        return result.stdout

    def inspect(identifier):
        return json.loads(native(['inspect', identifier]))[0]

    def free_port():
        with socket.socket() as listener:
            listener.bind(('127.0.0.1', 0))
            return listener.getsockname()[1]

    base = 'http://127.0.0.1:' + str(free_port())

    def install(action, *args, body=None, code=0):
        result = subprocess.run(['python3', 'scripts/install.py', action, '--json', '--directory', str(folder), *args],
            input=None if body is None else json.dumps(body), env=clean, text=True, capture_output=True, timeout=180)
        value = json.loads(result.stdout)
        if result.returncode != code:
            raise RuntimeError('Dogfood installation failed: ' + action + '/' + value.get('error', {}).get('code', 'unexpected_exit'))
        return value

    def cli(identity, *args, body=None, code=0):
        result = subprocess.run(['/opt/oap/oap', '--url', base, '--credential-file', str(identity), *args],
            input=None if body is None else json.dumps(body), env=clean, text=True, capture_output=True, timeout=100)
        value = json.loads(result.stdout)
        if result.returncode != code:
            raise RuntimeError('Dogfood CLI failed: ' + args[-1] + '/' + value.get('error', {}).get('code', 'unexpected_exit'))
        if '"token":' in result.stdout or '"session":' in result.stdout:
            raise RuntimeError('Credential leaked to output')
        return value

    def mcp(name, arguments):
        request = {'jsonrpc': '2.0', 'id': 1, 'method': 'tools/call', 'params': {'name': name, 'arguments': arguments}}
        result = subprocess.run(['/opt/oap/oap-mcp', '--url', base, '--credential-file', str(agent), '--operate'],
            input=json.dumps(request)+'\n', text=True, capture_output=True, env=clean, timeout=90)
        reply = json.loads(result.stdout)
        if result.returncode or reply.get('error') or reply['result'].get('isError'):
            raise RuntimeError('Dogfood scoped MCP tool failed: ' + name)
        return json.loads(reply['result']['content'][0]['text'])

    def plan(request):
        return cli(agent, '--file', '-', 'action-plan', body=request)['data']

    def execute(value, review=False):
        if review:
            cli(agent, '--plan', value['id'], '--file', '-', 'execute-action', body={}, code=4)
            cli(agent, '--plan', value['id'], '--file', '-', 'approve-action', body={'planHash': value['planHash']}, code=3)
            reviewed = cli(owner, '--plan', value['id'], 'review-action-input')['data']
            assert reviewed['planHash'] == value['planHash'] and reviewed['secretValuesResolved'] is False
            cli(owner, '--plan', value['id'], '--file', '-', 'approve-action', body={'planHash': value['planHash']})
        receipt = mcp('execute_action_plan', {'planId': value['id']})['result']
        repeated = cli(agent, '--plan', value['id'], '--file', '-', 'execute-action', body={})['data']['result']
        assert receipt == repeated
        return receipt

    def wait(operation, code=0):
        return cli(agent, '--operation', operation, '--timeout', '60s', 'wait', code=code)

    def app_spec(name, publish=False):
        components = [
            {'name': 'frontend', 'kind': 'web', 'image': web_image, 'port': 8080, 'instances': 1,
             'services': {'UPSTREAM_URL': 'api'}, 'dependsOn': ['api'], 'readiness': {'requireHealthy': True, 'timeoutSeconds': 30}},
            {'name': 'api', 'kind': 'web', 'image': web_image, 'port': 8080, 'instances': 1,
             'env': {'OAP_TEST_MESSAGE': 'dogfood-api'},
             'healthCheck': {'mode': 'http', 'path': '/health/custom', 'intervalSeconds': 1, 'timeoutSeconds': 1, 'retries': 1},
             'readiness': {'requireHealthy': True, 'timeoutSeconds': 30}},
            {'name': 'worker', 'kind': 'worker', 'image': worker_image, 'instances': 1,
             'readiness': {'requireHealthy': True, 'timeoutSeconds': 30}},
            {'name': 'helper-a', 'kind': 'web', 'image': web_image, 'port': 8080, 'instances': 1},
            {'name': 'helper-b', 'kind': 'web', 'image': web_image, 'port': 8080, 'instances': 1}]
        if publish:
            components[0]['hostPort'] = free_port()
        return {'apiVersion': 'oap.dev/v1alpha1', 'kind': 'Application',
                'spec': {'name': name, 'targetId': 'docker-local', 'environment': 'staging', 'components': components}}

    def behavior():
        instances = cli(agent, '--app', primary['id'], 'verify')['data']['instances']
        frontend = next(i for i in instances if i['component'] == 'frontend')
        with urllib.request.urlopen(frontend['resource']['url']+'/upstream', timeout=5) as response:
            value = json.load(response)
            assert response.status == 200 and value['version'] == 'v1' and value['message'] == 'dogfood-api' and value['boot']
        evidence = cli(agent, '--app', primary['id'], '--component', 'worker', 'incident')['data']
        assert evidence['logs']['status'] == 'sampled' and 'worker active' in evidence['logs']['text']
        return value

    def workload_identity():
        result = {}
        for app in apps:
            for component in app['manifest']['components']:
                rows = native(['ps', '-q', '--filter', 'label=io.openappplatform.owner=OpenAppPlatform:'+app['id']+':'+component['name']]).decode().split()
                assert len(rows) == 1
                info = inspect(rows[0])
                result[app['id']+':'+component['name']] = (info['Id'], info['State']['StartedAt'])
        return result

    def ids():
        return {service: installer.compose(folder, ['ps', '-q', service]).decode().strip() for service in ('platform', 'postgres')}

    def metadata():
        # Fixed owner/host measurement, never an agent SQL interface.
        return json.loads(installer.sql(folder, 'oap', "SELECT json_build_object('databaseBytes',pg_database_size(current_database()),'connections',(SELECT count(*) FROM pg_stat_activity WHERE datname=current_database()),'applications',(SELECT count(*) FROM oap_applications),'operations',(SELECT count(*) FROM oap_deployments),'plans',(SELECT count(*) FROM oap_action_plans));"))

    def observation(index):
        if index % 2 == 0:
            view = cli(agent, '--app', apps[index % len(apps)]['id'], 'incident')['data']
            assert view['state'] == ('inconclusive' if proxy.disabled else 'observed_healthy')

    try:
        runtime_info = json.loads(native(['image', 'inspect', os.environ['OAP_TEST_PLATFORM_IMAGE']]))[0]
        runtime = runtime_info['Id']
        postgres = json.loads(native(['image', 'inspect', os.environ['OAP_TEST_DATABASE_IMAGE']]))[0]['Id']
        web_image = json.loads(native(['image', 'inspect', os.environ['OAP_TEST_DOCKER_IMAGE']]))[0]['Id']
        worker_image = json.loads(native(['image', 'inspect', os.environ['OAP_TEST_WORKER_IMAGE']]))[0]['Id']
        install('init', '--image', runtime, '--docker-socket', proxy.path, '--port', base.rsplit(':', 1)[1])
        values = dict(line.split('=', 1) for line in (folder/'.env').read_text().splitlines() if line)
        values['OAP_POSTGRES_IMAGE'] = postgres
        installer.rewrite_values(folder, values)
        project = values['COMPOSE_PROJECT_NAME']
        started = time.monotonic(); install('start'); latency['startupSeconds'] = time.monotonic()-started
        install('owner-setup', '--cli-bin', '/opt/oap/oap', '--out', str(owner), body={'email': 'dogfood@example.invalid', 'name': 'Owner', 'password': secrets.token_urlsafe(24)})
        grant = cli(owner, '--out', str(agent), '--file', '-', 'grant', body={'name': 'Dogfood lifecycle agent', 'targetId': 'docker-local', 'environment': 'staging', 'maxApplications': 10, 'actions': ['configure', 'deploy', 'restart', 'recover']})['data']
        assert [t['id'] for t in cli(agent, 'targets')['data']] == ['docker-local']
        specification = app_spec('dogfood-primary', publish=True)
        cli(agent, '--file', '-', 'validate-spec', body={'specification': specification})
        created = execute(plan({'action': 'create', 'specification': specification}))
        primary = cli(agent, '--app', created['applicationId'], 'get')['data']; apps.append(primary)
        assert cli(agent, '--app', primary['id'], 'instances')['data'][0]['status'] == 'not deployed'
        first = execute(plan({'action': 'deploy', 'applicationId': primary['id'], 'expectedVersion': primary['version'], 'requireReview': True}), review=True)
        wait(first['operationId']); operations.append(first['operationId']); behavior()
        initial_identity = workload_identity()
        broken_spec = copy.deepcopy(specification)
        broken_spec['spec']['components'][1]['healthCheck']['path'] = '/health/fail'
        saved = execute(plan({'action': 'configure', 'applicationId': primary['id'], 'expectedVersion': primary['version'], 'specification': broken_spec}))
        assert saved['state'] == 'saved' and 'operationId' not in saved and workload_identity() == initial_identity
        primary = cli(agent, '--app', primary['id'], 'get')['data']; apps[0] = primary
        failed = execute(plan({'action': 'deploy', 'applicationId': primary['id'], 'expectedVersion': primary['version'], 'components': ['api']}))
        wait(failed['operationId'], code=6); operations.append(failed['operationId'])
        incident = mcp('get_incident_context', {'applicationId': primary['id'], 'component': 'api'})
        assert incident['state'] == 'issue' and incident['untrustedEvidence'] and incident['applicationBehaviorVerified'] is False
        assert any(c['field'] == 'native health check' and c['component'] == 'api' for c in incident['comparison']['changes'])
        assert any(a['action'] == 'configure' and a['allowed'] for a in incident['nextActions'])
        cli(agent, '--file', '-', 'action-plan', body={'action': 'configure', 'applicationId': primary['id'], 'expectedVersion': 1, 'specification': specification}, code=4)
        fixed = execute(plan({'action': 'configure', 'applicationId': primary['id'], 'expectedVersion': incident['definitionVersion'], 'specification': specification}))
        primary = cli(agent, '--app', primary['id'], 'get')['data']; apps[0] = primary
        correction = execute(plan({'action': 'deploy', 'applicationId': primary['id'], 'expectedVersion': fixed['definitionVersion'], 'components': ['api']}))
        wait(correction['operationId']); operations.append(correction['operationId']); behavior()
        assert cli(agent, '--app', primary['id'], 'incident')['data']['state'] == 'observed_healthy'
        for index in range(1, 10):
            definition = app_spec('profile-'+str(index))['spec']
            app = cli(agent, '--key', 'profile-create-'+str(index), '--file', '-', 'create', body=definition)['data']; apps.append(app)
            release = cli(agent, '--app', app['id'], '--key', 'profile-initial-'+str(index), '--file', '-', 'deploy', body={'expectedVersion': app['version']})
            operations.append(release['operationId'])
        for operation in operations[3:]: wait(operation)
        assert sum(len(cli(agent, '--app', app['id'], 'verify')['data']['instances']) for app in apps) == 50
        cli(agent, '--key', 'profile-over-capacity', '--file', '-', 'create', body=app_spec('profile-denied')['spec'], code=4)
        sampler = Sampler(endpoint, native, ids, metadata, proxy)
        print('Dogfood install, named-agent multi-component release, diagnosis/correction and 10-app profile passed', flush=True)
        phases.append(sampler.phase('idle', 60))
        phases.append(sampler.phase('agent_observation', 30, tick=observation))
        burst = []
        def deploy_burst():
            for index, app in enumerate(apps):
                release = cli(agent, '--app', app['id'], '--key', 'profile-burst-'+str(index), '--file', '-', 'deploy', body={'expectedVersion': app['version']})
                burst.append(release['operationId'])
        phases.append(sampler.phase('deployment_burst', 30, action=deploy_burst))
        for operation in burst: wait(operation)
        # Simulated target failure holds only this platform's proxy, not Engine/workloads.
        before = workload_identity()
        proxy.disabled = True
        try:
            phases.append(sampler.phase('target_outage', 30, tick=observation))
            with urllib.request.urlopen('http://127.0.0.1:'+str(specification['spec']['components'][0]['hostPort'])+'/upstream', timeout=5) as response:
                assert json.load(response)['message'] == 'dogfood-api'
        finally:
            proxy.disabled = False
        assert workload_identity() == before
        # Compatible image update is deliberately a label-only variant of the same build.
        base_tag, candidate_tag = 'oap-dogfood-base:'+project, 'oap-dogfood-candidate:'+project
        native(['tag', runtime, base_tag])
        native(['build', '--network=none', '--pull=false', '-t', candidate_tag, '-'], ('FROM '+base_tag+'\nLABEL io.openappplatform.dogfood-variant='+project+'\n').encode())
        candidate = json.loads(native(['image', 'inspect', candidate_tag]))[0]['Id']; assert candidate != runtime
        started=time.monotonic();install('update', '--image', candidate);latency['compatibleUpdateSeconds']=time.monotonic()-started
        assert workload_identity() == before; behavior()
        current = ids()['platform'];native(['update', '--restart=no', current]);native(['kill', '--signal', 'KILL', current])
        assert not install('check', code=4)['healthy']
        with urllib.request.urlopen('http://127.0.0.1:'+str(specification['spec']['components'][0]['hostPort'])+'/upstream', timeout=5) as response: assert json.load(response)['message']=='dogfood-api'
        started=time.monotonic();install('recover');latency['externalRescueSeconds']=time.monotonic()-started
        assert workload_identity()==before;behavior()
        phases.append(sampler.phase('controller_recovery', 30, tick=observation))
        # Quiesced metadata restore into a separate DB, then one active restored controller.
        installer.compose(folder, ['stop', 'platform'])
        archive=folder/'metadata.dump';install('backup','--archive',str(archive))
        restored_db='oap_restore_dogfood';restore=install('restore-check','--archive',str(archive),'--database',restored_db)
        assert restore['metadataVerified'] and not restore['controllerStarted']
        restored=project+'-restored-controller'
        password=values['POSTGRES_PASSWORD']
        installer.compose(folder,['run','-d','--no-deps','--service-ports','--name',restored,'-e','DATABASE_URL=postgres://oap:'+password+'@postgres:5432/'+restored_db+'?sslmode=disable','platform'])
        for _ in range(80):
            try:
                with urllib.request.urlopen(base+'/healthz',timeout=1) as response:
                    if response.status==200:break
            except Exception:pass
            time.sleep(.25)
        else:raise RuntimeError('Restored controller did not become ready')
        assert workload_identity()==before;behavior()
        assert len(cli(agent,'apps')['data'])==10
        control=cli(owner,'agent-control')['data'];cli(owner,'--file','-','set-agent-control',body={'expectedVersion':control['version'],'enabled':False,'reason':'Dogfood final pause verification'})
        cli(agent,'apps',code=3)
        cli(owner,'--file','-','set-agent-control',body={'expectedVersion':control['version']+1,'enabled':True,'reason':'Dogfood final resume verification'})
        cli(owner,'--credential',grant['credentialId'],'revoke');cli(agent,'apps',code=3)
        info=docker_json(endpoint,'/info')
        idle=phases[0]['services']; service=idle['platform']; pg=idle['postgres']
        complete=all(p['services'][name]['cpuIntervals']>0 and p['services'][name]['rssPeakBytes'] is not None for p in phases for name in ('platform','postgres'))
        if not complete:raise RuntimeError('Required footprint measurements missing; never treat missing as zero')
        measured={'schemaVersion':1,'status':'measured','profile':{'applications':10,'components':50,'instances':50,'observationIntervalSeconds':10,'idleSeconds':60},
            'host':{k:info.get(k) for k in ('NCPU','MemTotal','Architecture','OperatingSystem','ServerVersion')},'platformImageBytes':runtime_info['Size'],
            'latency':latency,'phases':phases,'goals':{'serviceIdleRssUnder256MiB':service['rssPeakBytes']<=256*1024**2,'combinedIdleSummedRssUnder512MiB':service['rssPeakBytes']+pg['rssPeakBytes']<=512*1024**2,'combinedIdleCpuUnderOneCorePercent':service['cpuMeanOneCorePercent']+pg['cpuMeanOneCorePercent']<=1},
            'definitions':{'platformRss':'VmRSS of /proc/1/status','postgresRss':'sum of postgres process VmRSS; shared pages may be counted repeatedly','workingSet':'Docker memory usage minus inactive file cache','cpu':'Docker CPU counter delta / system delta * online CPUs * 100; one-core percentage','runtimeRequests':'Only platform adapter requests through the test proxy; sampler/host commands bypass it'},
            'limits':['Short isolated Docker Desktop/Linux profile, not production capacity or sustained-memory proof','Workloads, Engine, browser/test runner and builds excluded from OAP+PostgreSQL accounting','Proxy instrumentation adds overhead; no monitoring service is part of the product','Label-only update tests image replacement/schema compatibility, not a historical release migration','Metadata restore is not application-data restore']}
        Path('/report/footprint.json').write_text(json.dumps(measured,indent=2)+'\n')
        acceptance={'schemaVersion':1,'status':'passed','browserRequired':False,'operatorRequired':False,'authority':{'trust':'owner session','applicationLifecycle':'named bounded agent','hostDrills':'explicit isolated host authority'},'applications':10,'components':50,'primaryApplicationId':primary['id'],'failedOperationId':failed['operationId'],'correctionOperationId':correction['operationId'],'checks':['headless_install_trust','portable_create_save','exact_owner_review','agent_mcp_execution','immutable_multi_component_release','declared_frontend_api_connection','worker_heartbeat','injected_failure_diagnosis','stale_correction_denial','authorized_fix_http_verification','target_outage_without_workload_restart','compatible_update','controller_kill_external_rescue','unchanged_native_boot_identity','isolated_metadata_restore_agent_access','pause_resume_revoke','measured_footprint'],'applicationDataRestored':False,'footprintReport':'footprint.json'}
        Path('/report/dogfood-acceptance.json').write_text(json.dumps(acceptance,indent=2)+'\n')
        print('Headless dogfood, measured footprint and recovery/restore acceptance passed',flush=True)
    finally:
        if restored:native(['rm','-f',restored])
        for app in apps:
            for component in app['manifest']['components']:
                rows=native(['ps','-aq','--filter','label=io.openappplatform.owner=OpenAppPlatform:'+app['id']+':'+component['name']]).decode().split()
                if rows:native(['rm','-f',*rows])
        if project:installer.compose(folder,['down','--volumes','--remove-orphans'])
        proxy.close()


if __name__=='__main__':run()
