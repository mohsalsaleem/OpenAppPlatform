#!/usr/bin/env python3
"""Headless M2.5 acceptance against the isolated standalone Docker platform.
Preview authority is explicit; agent bootstrap is a separate M2.6 gate.
"""
import json
import os
import subprocess
import socket
import urllib.request
from pathlib import Path

binary = '/opt/oap/oap'
base = os.environ['OAP_BASE_URL']
report = {'schemaVersion': 1, 'authority': 'isolated-preview', 'operatorRequired': False}

def cli(*args, body=None, code=0):
    result = subprocess.run([binary, '--url', base, *args], input=None if body is None else json.dumps(body), text=True, capture_output=True, timeout=240)
    value = json.loads(result.stdout)
    if result.returncode != code:
        raise RuntimeError('CLI acceptance failed: ' + value.get('error', {}).get('code', 'unexpected_exit'))
    return value

cli('schema')
with socket.socket() as listener:
    listener.bind(('127.0.0.1', 0))
    host_port = listener.getsockname()[1]
manifest = {'name': 'cli-acceptance', 'targetId': 'test-docker', 'environment': 'staging', 'components': [{'name': 'web', 'kind': 'web', 'image': os.environ['OAP_TEST_DOCKER_IMAGE'], 'port': 8080, 'hostPort': host_port, 'instances': 1, 'strategy': 'standard', 'env': {'OAP_TEST_MESSAGE': 'headless-cli'}}]}
app = cli('--target', 'test-docker', '--environment', 'staging', '--file', '-', 'create', body=manifest)['data']
app_id = app['id']
context = ['--app', app_id, '--target', 'test-docker', '--environment', 'staging']
assert cli('--app', app_id, 'instances')['data'][0]['status'] == 'not deployed'
assert cli('--app', app_id, 'capabilities')['data']['actions']['web']['deploy']['allowed']
request = {'expectedVersion': app['version']}
release = cli(*context, '--key', 'cli-release-1', '--file', '-', 'deploy', body=request)
assert cli(*context, '--key', 'cli-release-1', '--file', '-', 'deploy', body=request)['operationId'] == release['operationId']
cli('--operation', release['operationId'], '--timeout', '60s', 'wait')
proof = cli('--app', app_id, 'verify')['data']
assert proof['coverage'] == 'current_instance_health' and not proof['applicationBehaviorVerified']
url = proof['instances'][0]['resource']['url']
with urllib.request.urlopen(url, timeout=5) as response:
    assert json.load(response)['message'] == 'headless-cli'
manifest['components'][0]['env']['OAP_TEST_MESSAGE'] = 'updated-headless-cli'
updated = cli(*context, '--file', '-', 'update', body={'manifest': manifest, 'expectedVersion': app['version']})['data']
# Stale saves and context mistakes do not dispatch a replacement.
cli(*context, '--file', '-', 'update', body={'manifest': manifest, 'expectedVersion': app['version']}, code=4)
cli('--app', app_id, '--target', 'wrong-target', '--key', 'cli-release-2', '--file', '-', 'deploy', body={'expectedVersion': updated['version']}, code=2)
with urllib.request.urlopen(url, timeout=5) as response:
    assert json.load(response)['message'] == 'headless-cli'
second = cli(*context, '--key', 'cli-release-2', '--file', '-', 'deploy', body={'expectedVersion': updated['version']})
cli('--operation', second['operationId'], '--timeout', '60s', 'wait')
proof = cli('--app', app_id, 'verify')['data']
with urllib.request.urlopen(proof['instances'][0]['resource']['url'], timeout=5) as response:
    assert json.load(response)['message'] == 'updated-headless-cli'
# A deliberate missing image is still admission, followed by a stopped operation.
failed = cli(*context, '--key', 'cli-release-fail', '--file', '-', 'deploy', body={'expectedVersion': updated['version'], 'images': {'web': 'oap-cli-missing@sha256:' + 'a' * 64}})
# Preparation errors use ten bounded controller retries before attention.
cli('--operation', failed['operationId'], '--timeout', '3m', 'wait', code=6)
report.update(status='passed', applicationId=app_id, deploymentId=release['operationId'], updateOperationId=second['operationId'], failedOperationId=failed['operationId'], checks=['create_without_deploy', 'scoped_capabilities', 'stable_key', 'wait_success', 'live_fixture_behavior', 'save_without_deploy', 'stale_save_denied', 'context_mismatch_denied', 'replacement_verified', 'failed_operation_nonzero'])
Path('/report/cli-acceptance.json').write_text(json.dumps(report, indent=2) + '\n')
print('Standalone JSON CLI acceptance passed (isolated preview authority)')
