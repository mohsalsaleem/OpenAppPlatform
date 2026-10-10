#!/usr/bin/env python3
"""Opt-in HTTP E2E: reuse one OAP-owned staging fixture on a real operator.

Uses owner credentials from a private JSON file (url/email/password). Retains the
fixture; no deletion, source trigger changes, DB edits, or production operations.
"""
import argparse
import http.cookiejar
import json
from pathlib import Path
import re
import secrets
import sys
import time
import urllib.error
import urllib.parse
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--credentials-file', required=True)
    parser.add_argument('--target-id', required=True)
    parser.add_argument('--image', required=True)
    parser.add_argument('--port', required=True, type=int)
    parser.add_argument('--path', default='/')
    parser.add_argument('--name', default='native-health-check-staging')
    parser.add_argument('--receipt', required=True)
    args = parser.parse_args()
    if not re.fullmatch(r'[A-Za-z0-9/._:-]+@sha256:[a-f0-9]{64}', args.image) or not 1 <= args.port <= 65535:
        raise SystemExit('Use a pinned image and valid internal port')
    credentials = json.loads(Path(args.credentials_file).read_text())
    base = credentials['url'].rstrip('/')
    parsed = urllib.parse.urlparse(base)
    if parsed.scheme != 'https' or parsed.username or parsed.password or parsed.path:
        raise SystemExit('A trusted HTTPS staging URL is required')
    client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    def call(path, method='GET', body=None, key=None):
        headers = {'Content-Type': 'application/json', 'X-OAP-CSRF': '1', 'Origin': base}
        if key:
            headers['Idempotency-Key'] = key
        req = urllib.request.Request(base + '/api/v1' + path, method=method,
                                     data=json.dumps(body).encode() if body is not None else None, headers=headers)
        try:
            with client.open(req, timeout=30) as response:
                return json.load(response)
        except urllib.error.HTTPError as error:
            raise RuntimeError(f'{method} {path}: HTTP {error.code}') from None
    call('/auth/login', 'POST', {key: credentials[key] for key in ('email', 'password')})
    try:
        targets = call('/targets')
        target = next((t for t in targets if t['id'] == args.target_id), None)
        if not target or target['environment'] != 'staging':
            raise RuntimeError('Explicit staging target is required')
        apps = [a for a in call('/applications') if a['manifest']['name'] == args.name and a['manifest']['targetId'] == args.target_id]
        if len(apps) > 1:
            raise RuntimeError('Fixture name is ambiguous; reconcile it before running')
        desired = {'mode': 'http', 'path': args.path, 'intervalSeconds': 2,
                   'timeoutSeconds': 2, 'retries': 3, 'startPeriodSeconds': 2}
        if apps:
            app = apps[0]
            manifest = app['manifest']
            if manifest['environment'] != 'staging' or len(manifest['components']) != 1:
                raise RuntimeError('Existing fixture topology differs')
            comp = manifest['components'][0]
            if comp.get('resourceId') or comp.get('management') or comp['image'] != args.image or comp['port'] != args.port or comp['instances'] != 1 or any(comp.get(k) for k in ('env', 'services', 'serviceEndpoints', 'hostPort')):
                raise RuntimeError('Existing fixture runtime/authority differs')
            comp['healthCheck'] = desired
            app = call('/applications/' + app['id'], 'PUT', {'manifest': manifest, 'expectedVersion': app['version']})
        else:
            app = call('/applications', 'POST', {'name': args.name, 'environment': 'staging', 'targetId': args.target_id,
                       'components': [{'name': 'web', 'kind': 'web', 'image': args.image, 'port': args.port,
                                       'instances': 1, 'strategy': 'standard', 'healthCheck': desired}]})
        run_id = secrets.token_hex(16)
        def deploy(phase):
            release = call('/applications/' + app['id'] + '/deployments', 'POST',
                           {'expectedVersion': app['version']}, 'health-e2e:' + run_id + ':' + phase)
            deadline = time.monotonic() + 240
            last = ''
            while time.monotonic() < deadline:
                release = call('/deployments/' + release['id'])
                if release['state'] != last:
                    print(phase + ': ' + release['state'], flush=True)
                    last = release['state']
                if release['state'] in ('failed', 'attention', 'cancelled', 'abandoned'):
                    raise RuntimeError('Fixture requires operator inspection; no automatic reissue')
                if release['state'] == 'succeeded':
                    instances = call('/applications/' + app['id'] + '/instances')
                    if len(instances) != 1 or instances[0]['status'] != 'running:healthy':
                        raise RuntimeError('Fixture image must have a healthy native image check for this E2E')
                    return {'releaseId': release['id'], 'resourceId': instances[0]['resourceId'], 'state': release['state']}
                time.sleep(2)
            raise RuntimeError('Fixture deadline exceeded; inspect the recorded release')
        checked = deploy('http')
        app['manifest']['components'][0]['healthCheck'] = {'mode': 'image'}
        app = call('/applications/' + app['id'], 'PUT', {'manifest': app['manifest'], 'expectedVersion': app['version']})
        inherited = deploy('image')
        if checked['resourceId'] != inherited['resourceId']:
            raise RuntimeError('Stable resource binding changed')
        receipt = {'applicationId': app['id'], 'targetId': args.target_id, 'image': args.image,
                   'http': checked, 'imageCheck': inherited, 'retained': True}
        Path(args.receipt).write_text(json.dumps(receipt, indent=2) + '\n')
        print('Native HTTP/image health E2E passed; staging fixture retained.')
    finally:
        call('/auth/logout', 'POST', {})


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
