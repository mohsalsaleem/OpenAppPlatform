#!/usr/bin/env python3
"""Owner-reviewed staging route membership over OAP bindings and native Traefik."""
import argparse
import hashlib
import http.cookiejar
import importlib.util
import json
from pathlib import Path
import re
import shlex
import subprocess
import urllib.error
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('route_render', ROOT / 'render-coolify-route.py')
renderer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(renderer)


def digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(',', ':')).encode()).hexdigest()


def snapshot(app, target, instances, releases, principal, component):
    if principal.get('role') != 'owner' or principal.get('kind') != 'session':
        raise ValueError('Route publication requires an existing human owner session')
    manifest = app['manifest']
    if target['id'] != manifest['targetId'] or target['operator'] != 'coolify' or target['environment'] != 'staging' or manifest['environment'] != 'staging':
        raise ValueError('This pilot requires a connected Coolify staging application')
    if any(item['state'] in ('queued', 'running', 'attention') or
           (item['state'] == 'abandoned' and not item.get('control', {}).get('resolvedAt')) for item in releases):
        raise ValueError('Finish or reconcile application work before reviewing routing')
    selected = next((item for item in manifest['components'] if item['name'] == component), None)
    if not selected or selected.get('management') == 'observe' or selected.get('resourceId'):
        raise ValueError('This pilot requires an OAP-owned managed image component')
    expected_owner = 'OpenAppPlatform:' + app['id'] + ':' + component
    known, members = set(), []
    for item in instances:
        if item['component'] != component:
            continue
        resource = item.get('resource') or {}
        endpoint = resource.get('url', '').rstrip('/')
        if resource.get('description') != expected_owner or resource.get('id') != item.get('resourceId') or resource.get('artifactKind') != 'image' or not endpoint:
            raise ValueError('Every recorded component binding must retain native ownership and an endpoint')
        known.add(endpoint)
        if item.get('retired'):
            continue
        if item['status'] != 'running:healthy' or resource.get('status') != 'running:healthy':
            raise ValueError('Every desired instance must be healthy before route publication')
        members.append({'ordinal': item['ordinal'], 'resourceId': item['resourceId'], 'url': endpoint})
    members.sort(key=lambda item: item['ordinal'])
    if [item['ordinal'] for item in members] != list(range(1, selected['instances'] + 1)) or len({item['url'] for item in members}) != len(members):
        raise ValueError('Healthy binding set does not match the desired replica definition')
    return {'version': app['version'], 'application': app['id'], 'component': component,
            'manifestHash': digest(manifest), 'targetId': target['id'], 'targetHash': digest(target),
            'owner': {'userId': principal['userId'], 'workspaceId': principal['workspaceId']},
            'members': members}, known


def plan_for(footprint, known, native, name, domain):
    if not native['peer']['fileProvider'] or not native['peer']['image'].startswith(('traefik:', 'traefik@')):
        raise ValueError('Native proxy must use the supported Traefik file-provider layout')
    try:
        current = json.loads(native['config'])
        service = current['http']['services'][name]['loadBalancer']
        old_urls = [item['url'].rstrip('/') for item in service['servers']]
        health = service['healthCheck']['path']
    except (KeyError, TypeError, json.JSONDecodeError):
        raise ValueError('Only an existing isolated JSON route from the OAP renderer is supported') from None
    if not set(old_urls).issubset(known) or current != renderer.render(name, domain, old_urls, health):
        raise ValueError('Existing route has foreign backends or custom configuration; retain native ownership')
    new_urls = [item['url'] for item in footprint['members']]
    rendered = json.dumps(renderer.render(name, domain, new_urls, health), indent=2) + '\n'
    return {'format': 1, 'footprint': footprint, 'name': name, 'domain': domain,
            'peer': native['peer'], 'previousHash': native['sha256'], 'config': rendered,
            'desiredHash': hashlib.sha256(rendered.encode()).hexdigest(),
            'added': sorted(set(new_urls) - set(old_urls)), 'removed': sorted(set(old_urls) - set(new_urls))}


def verify_plan(plan, footprint, known, native):
    if footprint != plan['footprint'] or native['peer'] != plan['peer']:
        raise ValueError('Application, target, owner or native proxy changed; review a fresh plan')
    regenerated = plan_for(footprint, known, native, plan['name'], plan['domain'])
    if native['sha256'] == plan['desiredHash'] and regenerated['config'] == plan['config']:
        return True
    if regenerated != {key: value for key, value in plan.items() if key not in ('baseURL', 'sshHost')}:
        raise ValueError('Native file or reviewed plan changed; review a fresh plan')
    return False


class OwnerAPI:
    def __init__(self, base, credentials_file):
        parsed = urllib.parse.urlsplit(base)
        if parsed.scheme != 'https' or not parsed.hostname or parsed.username is not None or parsed.path not in ('', '/') or parsed.query or parsed.fragment:
            raise ValueError('Use the explicit trusted HTTPS OAP origin')
        self.base = base.rstrip('/')
        credentials = json.loads(Path(credentials_file).read_text())
        if credentials['url'].rstrip('/') != self.base:
            raise ValueError('Credentials are scoped to a different OAP origin')
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        self.call('/api/v1/auth/login', {'email': credentials['email'], 'password': credentials['password']})

    def call(self, path, body=None):
        request = urllib.request.Request(self.base + path, data=json.dumps(body).encode() if body is not None else None,
                                         headers={'Content-Type': 'application/json', 'X-OAP-CSRF': '1'})
        try:
            with self.opener.open(request, timeout=20) as response:
                return json.load(response)
        except urllib.error.HTTPError as error:
            raise ValueError('OAP request rejected with HTTP ' + str(error.code)) from None

    def footprint(self, app_id, component):
        if not re.fullmatch('[a-f0-9]{32}', app_id) or not re.fullmatch('[a-z][a-z0-9-]{0,47}', component):
            raise ValueError('Invalid application/component identity')
        path = '/api/v1/applications/' + app_id
        app = self.call(path)
        target = next(item for item in self.call('/api/v1/targets') if item['id'] == app['manifest']['targetId'])
        return snapshot(app, target, self.call(path + '/instances'), self.call(path + '/deployments'), self.call('/api/v1/auth/me'), component)


def remote(host, request):
    if not re.fullmatch('[A-Za-z0-9][A-Za-z0-9_.@-]*', host):
        raise ValueError('Use a trusted configured SSH host alias')
    # This is our fixed source, not code from the plan or remote file.
    code = (ROOT / 'native_route_file.py').read_text()
    result = subprocess.run(['ssh', host, 'python3', '-c', shlex.quote(code)], input=json.dumps(request).encode(), capture_output=True, timeout=40)
    if result.returncode:
        raise ValueError('Native route identity/drift check rejected the operation')
    return json.loads(result.stdout)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('mode', choices=['plan', 'apply'])
    parser.add_argument('--base-url', required=True)
    parser.add_argument('--credentials-file', required=True)
    parser.add_argument('--ssh-host', required=True)
    parser.add_argument('--application')
    parser.add_argument('--component')
    parser.add_argument('--name')
    parser.add_argument('--domain')
    parser.add_argument('--plan-file', required=True)
    parser.add_argument('--acknowledge-routing-change', action='store_true')
    args = parser.parse_args()
    try:
        api = OwnerAPI(args.base_url, args.credentials_file)
        if args.mode == 'plan':
            if not all((args.application, args.component, args.name, args.domain)):
                raise ValueError('Application, component, route name and domain are required')
            footprint, known = api.footprint(args.application, args.component)
            native = remote(args.ssh_host, {'mode': 'read', 'name': args.name})
            plan = plan_for(footprint, known, native, args.name, args.domain)
            plan.update({'baseURL': api.base, 'sshHost': args.ssh_host})
            path = Path(args.plan_file)
            path.write_text(json.dumps(plan, indent=2) + '\n'); path.chmod(0o600)
            print(json.dumps({key: plan[key] for key in ('name', 'domain', 'added', 'removed', 'previousHash', 'desiredHash')}, indent=2))
        else:
            if not args.acknowledge_routing_change:
                raise ValueError('Review the plan and acknowledge the routing change')
            plan = json.loads(Path(args.plan_file).read_text())
            if plan['baseURL'] != api.base or plan['sshHost'] != args.ssh_host:
                raise ValueError('Plan belongs to a different OAP origin or SSH host')
            footprint, known = api.footprint(plan['footprint']['application'], plan['footprint']['component'])
            native = remote(args.ssh_host, {'mode': 'read', 'name': plan['name']})
            if verify_plan(plan, footprint, known, native):
                print(json.dumps({'status': 'alreadyApplied', 'sha256': native['sha256']})); return
            # Recheck lifecycle inputs after the native inspection round trip.
            if api.footprint(footprint['application'], footprint['component'])[0] != footprint:
                raise ValueError('Application changed before publication; review a fresh plan')
            result = remote(args.ssh_host, {'mode': 'publish', 'name': plan['name'], 'peer': plan['peer'], 'sha256': plan['previousHash'], 'config': plan['config']})
            print(json.dumps(result))
    except (ValueError, KeyError, StopIteration) as error:
        parser.error(str(error) or 'Reviewed route input is incomplete')


if __name__ == '__main__':
    main()
