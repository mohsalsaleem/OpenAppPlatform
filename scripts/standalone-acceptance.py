#!/usr/bin/env python3
"""Repeatable user-facing CLI deploy/HTTPS acceptance; no private APIs or SQL."""
import argparse
import concurrent.futures
import hashlib
import http.client
import json
import os
import re
from pathlib import Path
import socket
import secrets
import ssl
import stat
import subprocess
import sys
import urllib.parse

ROOT = Path(__file__).resolve().parent.parent


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['deploy', 'verify'])
    parser.add_argument('--directory', required=True)
    parser.add_argument('--spec', required=True)
    parser.add_argument('--owner-input')
    parser.add_argument('--cli-bin', default=str(ROOT / 'bin/oap'))
    parser.add_argument('--url', default='http://127.0.0.1:8878')
    parser.add_argument('--origin')
    parser.add_argument('--source-root')
    parser.add_argument('--ca-file')
    parser.add_argument('--connect-host', help='Explicit connection address when testing DNS before cutover')
    args = parser.parse_args()
    folder = Path(args.directory).resolve()
    if folder.stat().st_mode & 0o077:
        raise ValueError('Use the private installer directory')
    specification = json.loads(Path(args.spec).read_text())
    fingerprint = hashlib.sha256(json.dumps(specification, sort_keys=True).encode()).hexdigest()
    state_file = folder / 'acceptance-state.json'
    state = json.loads(state_file.read_text()) if state_file.exists() else {'specificationHash': fingerprint}
    if state.get('specificationHash') != fingerprint:
        raise ValueError('Acceptance specification changed; retain the existing operation and inspect it before proceeding')

    def save():
        temporary = folder / ('acceptance-state.' + secrets.token_hex(8) + '.tmp')
        fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, 'w') as stream:
            json.dump(state, stream, indent=2)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, state_file)

    clean = {k: v for k, v in os.environ.items() if k not in ('OAP_API_TOKEN', 'OAP_SESSION_FILE', 'OAP_CREDENTIAL_FILE', 'OAP_URL')}

    def cli(identity, command, flags=(), body=None):
        commandline = [args.cli_bin, '--url', args.url, '--credential-file', str(folder / (identity + '.json')), *flags]
        if body is not None:
            commandline += ['--file', '-']
        result = subprocess.run([*commandline, command], input=None if body is None else json.dumps(body),
                                capture_output=True, text=True, env=clean, timeout=150)
        value = json.loads(result.stdout)
        if result.returncode:
            code = value.get('error', {}).get('code', 'unavailable')
            if not isinstance(code, str) or not re.fullmatch(r'[a-z0-9_]{1,80}', code):
                code = 'unavailable'
            raise ValueError(command + ' rejected: ' + code)
        return value

    if args.action == 'deploy':
        if not (folder / 'owner.json').exists():
            if not args.owner_input:
                raise ValueError('First deployment requires a private owner-input file')
            p = Path(args.owner_input)
            info = p.lstat()
            if not stat.S_ISREG(info.st_mode) or info.st_mode & 0o077:
                raise ValueError('Owner input must be a private regular file')
            result = subprocess.run([sys.executable, str(ROOT / 'scripts/install.py'), 'owner-setup', '--json',
                '--directory', str(folder), '--cli-bin', args.cli_bin, '--out', str(folder / 'owner.json')],
                input=p.read_bytes(), capture_output=True, env=clean, timeout=45)
            if result.returncode:
                raise ValueError('Documented owner setup failed; inspect setup status before retrying')
        if not (folder / 'agent.json').exists():
            manifest = specification['spec']
            cli('owner', 'grant', ['--out', str(folder / 'agent.json')], {
                'name': 'Standalone acceptance agent', 'targetId': manifest['targetId'],
                'environment': manifest['environment'], 'maxApplications': 1,
                'actions': ['configure', 'deploy', 'restart', 'recover']})
        cli('agent', 'validate-spec', body={'specification': specification})
        if 'createPlan' not in state:
            state['createPlan'] = cli('agent', 'action-plan', body={'action': 'create', 'specification': specification})['data']
            save()
        if 'application' not in state:
            state['application'] = cli('agent', 'execute-action', ['--plan', state['createPlan']['id']], {})['data']['result']
            save()
        app = state['application']
        if 'deployPlan' not in state:
            state['deployPlan'] = cli('agent', 'action-plan', body={'action': 'deploy',
                'applicationId': app['applicationId'], 'expectedVersion': app['definitionVersion']})['data']
            save()
        if 'deployment' not in state:
            state['deployment'] = cli('agent', 'execute-action', ['--plan', state['deployPlan']['id']], {})['data']['result']
            save()
        operation = state['deployment']['operationId']
        cli('agent', 'wait', ['--operation', operation, '--timeout', '120s'])
        cli('agent', 'verify', ['--app', app['applicationId']])
        replay = cli('agent', 'execute-action', ['--plan', state['deployPlan']['id']], {})['data']['result']
        if replay['operationId'] != operation:
            raise ValueError('Operation replay differed')
        print(json.dumps({'schemaVersion': 1, 'ok': True, 'action': 'deploy',
                          'applicationId': app['applicationId'], 'operationId': operation,
                          'replaySameOperation': True, 'HTTPSVerified': False}))
        return

    origin = urllib.parse.urlsplit(args.origin or '')
    domain = specification['spec'].get('ingress', {}).get('domain')
    if origin.scheme != 'https' or origin.hostname != domain or origin.username or origin.password or origin.query or origin.fragment or origin.path not in ('', '/'):
        raise ValueError('Verification origin must be the specification HTTPS hostname')
    context = ssl.create_default_context(cafile=args.ca_file)

    def fetch(path):
        connection = http.client.HTTPSConnection(origin.hostname, origin.port or 443, context=context, timeout=10)
        raw = socket.create_connection((args.connect_host or origin.hostname, origin.port or 443), timeout=10)
        connection.sock = context.wrap_socket(raw, server_hostname=origin.hostname)
        certificate = connection.sock.getpeercert()
        leaf = hashlib.sha256(connection.sock.getpeercert(binary_form=True)).hexdigest()
        try:
            connection.request('GET', path)
            response = connection.getresponse()
            return response.status, response.read(), leaf, certificate['notAfter']
        finally:
            connection.close()

    status, body, leaf, expiration = fetch('/')
    if status != 200:
        raise ValueError('HTTPS homepage did not return success')
    checked = 1
    if args.source_root:
        source = Path(args.source_root).resolve()
        files = [p for p in source.rglob('*') if p.is_file()]
        if not files:
            raise ValueError('Source root contains no files')
        def check(p):
            try:
                p.resolve().relative_to(source)
            except ValueError:
                raise ValueError('Source files must remain under the supplied root')
            status, body, _, _ = fetch('/' + urllib.parse.quote(p.relative_to(source).as_posix(), safe='/'))
            return status == 200 and hashlib.sha256(body).digest() == hashlib.sha256(p.read_bytes()).digest()
        with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
            if not all(pool.map(check, files)):
                raise ValueError('Public site differs from retained source')
        checked = len(files)
    receipt = {'schemaVersion': 1, 'ok': True, 'action': 'verify', 'HTTPSHostnameAndChainVerified': True,
               'filesChecked': checked, 'contentMismatches': 0, 'certificateSHA256': leaf,
               'certificateExpires': expiration, 'customCATrust': bool(args.ca_file), 'scheduledRenewalVerified': False}
    state['HTTPSVerification'] = receipt
    save()
    print(json.dumps(receipt))


if __name__ == '__main__':
    try:
        main()
    except (ValueError, OSError, KeyError, subprocess.SubprocessError) as error:
        print(json.dumps({'schemaVersion': 1, 'ok': False, 'error': 'acceptance_failed',
                          'reason': str(error) if isinstance(error, ValueError) else type(error).__name__,
                          'next': 'Inspect installer status and retained plan/operation before retrying; do not edit control metadata'}))
        sys.exit(1)
