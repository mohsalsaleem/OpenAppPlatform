#!/usr/bin/env python3
"""Small local installation and quiesced metadata backup helpers; no env loading."""
import argparse
import fcntl
from datetime import datetime, timezone
import hashlib
import json
import ipaddress
import os
from pathlib import Path
import re
import secrets
import shutil
import stat
import subprocess
import sys

ROOT = Path(__file__).resolve().parent.parent
IMAGE = re.compile(r'^(?:sha256:[a-f0-9]{64}|[A-Za-z0-9/._:-]+@sha256:[a-f0-9]{64})$')
POSTGRES = 'postgres:17-alpine@sha256:742f40ea20b9ff2ff31db5458d127452988a2164df9e17441e191f3b72252193'
CADDY = 'caddy:2.10.2-alpine@sha256:4c6e91c6ed0e2fa03efd5b44747b625fec79bc9cd06ac5235a779726618e530d'
TLS_MODES = ('internal', 'letsencrypt-staging', 'letsencrypt')


def ingress_options(domain, tls, bind, http_port, https_port, image):
    if not domain:
        return None
    if isinstance(domain, list):
        if any(not host for host in domain) or len(domain) > 32 or len(set(domain)) != len(domain):
            raise ValueError('Use at most 32 unique owner ingress domains')
        options = [ingress_options(host, tls, bind, http_port, https_port, image) for host in domain]
        result = options[0]
        result['domains'] = sorted(domain[1:])
        return result
    labels = domain.split('.')
    if (len(domain) > 253 or len(labels) < 2 or
        any(not re.fullmatch(r'[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?', label) for label in labels)):
        raise ValueError('Ingress requires one lowercase DNS hostname; wildcards and IP addresses are unsupported')
    try:
        ipaddress.ip_address(domain)
    except ValueError:
        pass
    else:
        raise ValueError('Ingress requires a DNS hostname, not an IP address')
    if tls not in TLS_MODES or not IMAGE.fullmatch(image):
        raise ValueError('Use an explicit supported ingress TLS mode and immutable Caddy image')
    if not (1 <= http_port <= 65535 and 1 <= https_port <= 65535) or http_port == https_port:
        raise ValueError('Ingress requires distinct valid host ports')
    try:
        address = ipaddress.ip_address(bind)
    except ValueError:
        raise ValueError('Ingress bind must be an IPv4 address')
    if address.version != 4:
        raise ValueError('Initial ingress installation requires an IPv4 bind address')
    return {'domain': domain, 'tls': tls, 'bind': bind,
            'httpPort': http_port, 'httpsPort': https_port, 'image': image}


def private_write(path, text):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as stream:
        stream.write(text)


def init(directory, image, socket=None, operator_only=False, port=8878,
         environment='staging', ingress_domain=None, ingress_tls='internal',
         ingress_bind='127.0.0.1', ingress_http_port=80, ingress_https_port=443,
         ingress_image=CADDY, postgres_image=POSTGRES):
    if not IMAGE.fullmatch(image) or not 1024 <= port <= 65535:
        raise ValueError('Use an immutable OAP image digest/content ID and port 1024–65535')
    if not re.fullmatch(r'[a-z][a-z0-9-]{0,47}', environment) or not IMAGE.fullmatch(postgres_image):
        raise ValueError('Use a lowercase environment slug and immutable PostgreSQL image')
    ingress = ingress_options(ingress_domain, ingress_tls, ingress_bind, ingress_http_port, ingress_https_port, ingress_image)
    if ingress and (operator_only or port in (ingress_http_port, ingress_https_port)):
        raise ValueError('Ingress requires standalone Docker and ports separate from the control plane')
    directory = Path(directory).resolve()
    if directory.exists() and any(directory.iterdir()):
        raise ValueError('Installation directory must be empty; existing configuration is never overwritten')
    gid = 0
    if not operator_only:
        if socket is None:
            endpoint = subprocess.check_output(['docker', 'context', 'inspect', '--format', '{{.Endpoints.docker.Host}}'], text=True).strip()
            if not endpoint.startswith('unix:///'):
                raise ValueError('Standalone Docker requires a local Unix socket')
            socket = endpoint[len('unix://'):]
        socket = Path(socket)
        if not socket.is_absolute() or not stat.S_ISSOCK(socket.stat().st_mode):
            raise ValueError('Docker socket must be an existing absolute Unix socket path')
        result = subprocess.run(["docker", "run", "--rm", "--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--user", "10001", "--mount", "type=bind,src=" + str(socket) + ",dst=/probe.sock,readonly", "--entrypoint", "/bin/stat", image, "-c", "%g", "/probe.sock"], capture_output=True, timeout=30)
        if result.returncode or not result.stdout.strip().isdigit():
            raise ValueError("Cannot inspect the socket group as seen by the container; preload the pinned OAP image")
        gid = int(result.stdout.strip())
    directory.mkdir(parents=True, exist_ok=True, mode=0o700)
    directory.chmod(0o700)
    (directory / 'backup-reports').mkdir(mode=0o755, exist_ok=True)
    (directory / 'backup-reports').chmod(0o755)
    files = 'compose.yaml' if operator_only else 'compose.yaml:compose.docker.yaml'
    env = {
        'COMPOSE_PROJECT_NAME': 'oap-' + secrets.token_hex(4),
        'COMPOSE_FILE': files,
        'OAP_IMAGE': image,
        'OAP_POSTGRES_IMAGE': postgres_image,
        'POSTGRES_PASSWORD': secrets.token_urlsafe(32),
        'OAP_SETUP_TOKEN': secrets.token_urlsafe(32),
        'OAP_COOKIE_SECURE': 'false',
        'OAP_BIND_ADDR': '127.0.0.1',
        'OAP_HTTP_PORT': str(port),
        'OAP_BACKUP_REPORTS_DIR': '/app/backup-reports',
    }
    if not operator_only:
        env.update(OAP_DOCKER_SOCKET=str(socket), OAP_DOCKER_GID=str(gid))
    if ingress:
        env['COMPOSE_FILE'] += ':compose.ingress.yaml'
        env.update(OAP_CADDY_IMAGE=ingress['image'], OAP_INGRESS_DOMAIN=ingress['domain'],
                   OAP_INGRESS_TLS=ingress['tls'], OAP_INGRESS_BIND_ADDR=ingress['bind'],
                   OAP_INGRESS_HTTP_PORT=str(ingress['httpPort']), OAP_INGRESS_HTTPS_PORT=str(ingress['httpsPort']))
        if ingress.get('domains'):
            env['OAP_INGRESS_DOMAINS'] = json.dumps(ingress['domains'], separators=(',', ':'))
        bootstrap = {'admin': {'listen': 'unix//run/oap-ingress/admin.sock|0600', 'config': {'persist': True}},
                     'apps': {'http': {'http_port': 80, 'https_port': 443, 'servers': {'bootstrap': {
                         'listen': [':80'], 'automatic_https': {'disable': True},
                         'routes': [{'handle': [{'handler': 'static_response', 'status_code': 404}]}]}}}}}
        (directory / 'ingress-bootstrap.json').write_text(json.dumps(bootstrap) + '\n')
        (directory / 'ingress-bootstrap.json').chmod(0o644)
        shutil.copyfile(ROOT / 'deploy/compose.ingress.yaml', directory / 'compose.ingress.yaml')
    private_write(directory / '.env', ''.join(f'{key}={value}\n' for key, value in env.items()))
    for name in ('compose.yaml', 'compose.docker.yaml'):
        shutil.copyfile(ROOT / 'deploy' / name, directory / name)
    targets = [] if operator_only else [{
        'id': 'docker-local', 'name': 'Local Docker', 'operator': 'docker',
        'url': 'unix:///var/run/docker.sock', 'environment': environment,
        'settings': {'pullPolicy': 'never', 'hostBindIP': '127.0.0.1'},
    }]
    # Parent is private; the mounted reference-only file must be readable by UID 10001.
    (directory / 'targets.json').write_text(json.dumps(targets, indent=2) + '\n')
    (directory / 'targets.json').chmod(0o644)
    (directory / '.gitignore').write_text('*\n')
    return directory


class ComposeError(RuntimeError):
    def __init__(self, detail):
        super().__init__("Installation Compose command failed; inspect the installation locally (secret values are not printed)")
        self.detail = detail


def compose(directory, args, input_bytes=None):
    result = subprocess.run(['docker', 'compose', '--project-directory', str(directory)] + args,
                            input=input_bytes, capture_output=True, timeout=180, cwd=directory)
    if result.returncode:
        raise ComposeError(result.stderr.decode(errors='replace')[:16000])
    return result.stdout


def stopped(directory):
    if compose(directory, ['ps', '--status', 'running', '-q', 'platform']).strip():
        raise ValueError('Stop the platform before metadata backup/restore; native workloads can keep running')


def sql(directory, database, query):
    return compose(directory, ['exec', '-T', 'postgres', 'psql', '-X', '-U', 'oap', '-d', database,
                               '-v', 'ON_ERROR_STOP=1', '-At'], query.encode()).decode().strip()


def metadata(directory, database):
    # Aggregate identities, not credential contents. Table counts include durable job/claim ledgers.
    return json.loads(sql(directory, database, """
SELECT json_build_object('migrations',(SELECT json_agg(json_build_object('version',version,'checksum',checksum) ORDER BY version) FROM oap_schema_migrations),
'applications',(SELECT count(*) FROM oap_applications),
'owners',(SELECT count(*) FROM oap_users WHERE role='owner'),
'bindings',(SELECT count(*) FROM oap_bindings),
'deployments',(SELECT count(*) FROM oap_deployments));
"""))


def _backup(directory, archive):
    directory, archive = Path(directory).resolve(), Path(archive).resolve()
    stopped(directory)
    if archive.exists() or archive.with_suffix(archive.suffix + '.json').exists():
        raise ValueError('Backup destination already exists')
    before = metadata(directory, 'oap')
    data = compose(directory, ['exec', '-T', 'postgres', 'pg_dump', '-U', 'oap', '-d', 'oap', '-Fc'])
    if not data.startswith(b'PGDMP'):
        raise RuntimeError('PostgreSQL did not produce a custom archive')
    fd = os.open(archive, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'wb') as stream:
        stream.write(data)
    receipt = {'sha256': hashlib.sha256(data).hexdigest(), 'metadata': before,
               'postgresMajor': 17, 'controllersStopped': True}
    private_write(archive.with_suffix(archive.suffix + '.json'), json.dumps(receipt, indent=2) + '\n')
    return receipt


def _restore_check(directory, archive, database):
    directory, archive = Path(directory).resolve(), Path(archive).resolve()
    if not re.fullmatch(r'oap_restore_[a-z0-9_]{1,40}', database):
        raise ValueError('Restore only into a new oap_restore_<name> database; existing databases are never overwritten')
    stopped(directory)
    data = archive.read_bytes()
    receipt = json.loads(archive.with_suffix(archive.suffix + '.json').read_text())
    if hashlib.sha256(data).hexdigest() != receipt['sha256'] or not data.startswith(b'PGDMP'):
        raise ValueError('Backup checksum or archive format is invalid')
    compose(directory, ['exec', '-T', 'postgres', 'pg_restore', '--list'], data)
    compose(directory, ['exec', '-T', 'postgres', 'createdb', '-U', 'oap', database])
    compose(directory, ['exec', '-T', 'postgres', 'pg_restore', '-U', 'oap', '-d', database,
                        '--exit-on-error', '--no-owner', '--no-privileges'], data)
    if metadata(directory, database) != receipt['metadata']:
        raise RuntimeError('Restored metadata identity counts or migration ledger differ')
    return {'database': database, 'metadataVerified': True, 'controllerStarted': False}


def report_time():
    return datetime.now(timezone.utc).isoformat().replace('+00:00', 'Z')


def write_report(directory, report):
    # Summary only: archive paths, passwords, configuration values and raw errors stay private.
    directory = Path(directory).resolve()
    directory.mkdir(mode=0o755, parents=True, exist_ok=True)
    directory.chmod(0o755)
    temporary = directory / ('.' + report['id'] + '.' + secrets.token_hex(4) + '.tmp')
    fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o644)
    os.fchmod(fd, 0o644)
    try:
        with os.fdopen(fd, 'w') as stream:
            json.dump(report, stream)
            stream.write('\n')
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, directory / (report['id'] + '.json'))
    finally:
        temporary.unlink(missing_ok=True)


def report_error(error):
    if isinstance(error, KeyboardInterrupt):
        return 'interrupted'
    text = str(error)
    if text.startswith('Stop the platform'): return 'controller-running'
    if text.startswith('Backup destination already exists'): return 'destination-exists'
    if text.startswith('Backup checksum or archive format is invalid'): return 'checksum-mismatch'
    if text.startswith('Restore only into a new'): return 'invalid-restore-target'
    return 'operation-failed'


def reported(action, directory, archive, callback):
    reports = Path(directory).resolve() / 'backup-reports'
    now = report_time()
    report = {'version': 1, 'id': secrets.token_hex(16), 'action': action,
              'status': 'running', 'source': 'host-helper', 'coverage': 'metadata-only',
              'startedAt': now, 'updatedAt': now, 'sha256': '', 'sizeBytes': 0, 'errorCode': ''}
    write_report(reports, report)  # If the intent cannot be recorded, do not start the backup.
    try:
        result = callback()
        data = Path(archive).read_bytes()
        report.update(status='succeeded', sha256=hashlib.sha256(data).hexdigest(), sizeBytes=len(data))
        report['updatedAt'] = report_time()
        write_report(reports, report)
        return result
    except BaseException as error:
        report.update(status='failed', errorCode=report_error(error), sha256='', sizeBytes=0, updatedAt=report_time())
        write_report(reports, report)
        raise


def backup(directory, archive):
    return reported('backup', directory, archive, lambda: _backup(directory, archive))


def restore_check(directory, archive, database):
    return reported('restore-check', directory, archive, lambda: _restore_check(directory, archive, database))



PROTOCOL_VERSION = 1
JSON_OUTPUT = False
DIAGNOSTICS_DIRECTORY = None


def save_diagnostics(error):
    """Opt-in owner-local provider detail, never stdout or an uploaded report."""
    if DIAGNOSTICS_DIRECTORY is None or not isinstance(error, ComposeError):
        return None
    try:
        directory, _ = installation_values(DIAGNOSTICS_DIRECTORY)
        name = 'installation-failure-' + secrets.token_hex(8) + '.json'
        private_write(directory / name, json.dumps({'schemaVersion': 1, 'provider': 'compose',
                      'detail': error.detail}) + '\n')
        return name
    except (ValueError, OSError):
        return None


def installation_values(directory):
    directory = Path(directory).absolute()
    info = directory.lstat()
    config = directory / '.env'
    metadata = config.lstat()
    if not stat.S_ISDIR(info.st_mode) or info.st_mode & 0o077 or not stat.S_ISREG(metadata.st_mode) or metadata.st_mode & 0o077 or metadata.st_size > 65536:
        raise ValueError('Installation directory/configuration must be private regular files')
    result = dict(line.split('=', 1) for line in config.read_text().splitlines() if line and not line.startswith('#'))
    if not IMAGE.fullmatch(result.get('OAP_IMAGE', '')) or not re.fullmatch(r'oap-[a-f0-9]{8}', result.get('COMPOSE_PROJECT_NAME', '')):
        raise ValueError('Installation identity or immutable image is invalid')
    return directory, result


def rewrite_values(directory, values):
    temporary = directory / ('.env.' + secrets.token_hex(6))
    try:
        private_write(temporary, ''.join(key + '=' + value + '\n' for key, value in values.items()))
        with temporary.open('rb') as stream:
            os.fsync(stream.fileno())
        os.replace(temporary, directory / '.env')
        descriptor = os.open(directory, os.O_RDONLY)
        try:
            os.fsync(descriptor)
        finally:
            os.close(descriptor)
    finally:
        temporary.unlink(missing_ok=True)


def provision_ingress(directory, values):
    """Establish initial authority once; never rebind a recorded proxy on restart."""
    if 'OAP_INGRESS_DOMAIN' not in values:
        return
    options = ingress_options(values['OAP_INGRESS_DOMAIN'], values['OAP_INGRESS_TLS'],
                              values['OAP_INGRESS_BIND_ADDR'], int(values['OAP_INGRESS_HTTP_PORT']),
                              int(values['OAP_INGRESS_HTTPS_PORT']), values['OAP_CADDY_IMAGE'])
    domains = json.loads(values.get('OAP_INGRESS_DOMAINS', '[]'))
    if domains:
        options = ingress_options([options['domain'], *domains], options['tls'], options['bind'],
                                  options['httpPort'], options['httpsPort'], options['image'])
    target_file = directory / 'targets.json'
    if target_file.is_symlink():
        raise ValueError('Ingress authority file must be a regular owner-managed file')
    targets = json.loads(target_file.read_text())
    eligible = [t for t in targets if t.get('id') == 'docker-local' and t.get('operator') == 'docker']
    if len(eligible) != 1:
        raise ValueError('Packaged ingress requires exactly one docker-local target')
    target = eligible[0]
    recorded = target['settings'].get('ingress')
    if recorded and not re.fullmatch(r'[a-f0-9]{64}', recorded.get('bootstrapHash', '')):
        raise ValueError('Recorded ingress authority is incomplete; inspect installation before proceeding')
    if recorded:
        existing = compose(directory, ['ps', '--all', '-q', 'ingress']).decode().strip()
        if existing != recorded.get('containerId'):
            raise ValueError('Installed ingress identity changed; explicit handoff is required')
    else:
        compose(directory, ['run', '--rm', '--no-deps', '-T', 'ingress-init'])
    # --no-recreate keeps native identity stable even if someone changes Compose.
    compose(directory, ['up', '-d', '--no-recreate', '--wait', '--wait-timeout', '90', 'ingress'])
    peer = compose(directory, ['ps', '-q', 'ingress']).decode().strip()
    if not re.fullmatch(r'[a-f0-9]{64}', peer):
        raise ValueError('Cannot establish exact installed proxy identity')
    authority = {'containerId': peer, 'adminSocket': '/run/oap-ingress/admin.sock',
                 'proxySocket': '/run/oap-ingress/admin.sock', 'domain': options['domain'],
                 'httpPort': 80, 'httpsPort': 443, 'internalCa': options['tls'] == 'internal'}
    if options.get('domains'):
        authority['domains'] = options['domains']
    if options['tls'] != 'internal':
        authority['publicAcme'] = options['tls']
    if recorded:
        if any(recorded.get(key) != value for key, value in authority.items()) or recorded.get('publicAcme', '') != authority.get('publicAcme', '') or recorded.get('domains', []) != authority.get('domains', []):
            raise ValueError('Installed ingress identity/authority changed; explicit handoff is required')
        return
    raw = compose(directory, ['run', '--rm', '--no-deps', '-T', '--entrypoint', '/app/oap-admin',
                              'platform', 'ingress-authority'])
    observation = json.loads(raw)
    commitment = observation.get('bootstrapHash', '')
    if not observation.get('ok') or not re.fullmatch(r'[a-f0-9]{64}', commitment):
        raise ValueError('Cannot observe installed ingress bootstrap commitment')
    authority['bootstrapHash'] = commitment
    target['settings']['ingress'] = authority
    temporary = directory / ('targets.' + secrets.token_hex(6) + '.tmp')
    try:
        private_write(temporary, json.dumps(targets, indent=2) + '\n')
        temporary.chmod(0o644)
        with temporary.open('rb') as stream:
            os.fsync(stream.fileno())
        os.replace(temporary, target_file)
    finally:
        temporary.unlink(missing_ok=True)


def preflight_image(directory, values, image):
    # Compose resolves the candidate image from this transient environment; the
    # private installed config is unchanged until the read-only preflight passes.
    env = os.environ.copy()
    env['OAP_IMAGE'] = image
    result = subprocess.run(['docker', 'compose', '--project-directory', str(directory),
                             'run', '--rm', '--no-deps', '-T', '--entrypoint', '/app/oap-admin',
                             'platform', 'check-schema'], cwd=directory, env=env,
                            capture_output=True, timeout=90)
    if result.returncode:
        raise ValueError('Candidate image/schema preflight failed; installation configuration remains unchanged')


def lifecycle(directory, action, image=None):
    directory, values = installation_values(directory)
    lock = directory / '.lifecycle.lock'
    fd = os.open(lock, os.O_WRONLY | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    try:
        fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except BlockingIOError:
        os.close(fd)
        raise ValueError('Another installation operation is running')
    try:
        if action == 'check':
            raw = compose(directory, ['ps', '--format', 'json'])
            if len(raw) > 65536:
                raise ValueError('Installation status exceeds bounds')
            rows = json.loads(raw) if raw.lstrip().startswith(b'[') else [json.loads(line) for line in raw.splitlines() if line.strip()]
            services = [{'service': row.get('Service', ''), 'state': row.get('State', ''), 'health': row.get('Health', '')} for row in rows]
            required = ('platform', 'postgres', 'ingress') if 'OAP_INGRESS_DOMAIN' in values else ('platform', 'postgres')
            return {'schemaVersion': PROTOCOL_VERSION, 'action': action, 'services': services,
                    'healthy': all(any(row['service'] == name and row['state'] == 'running' and row['health'] == 'healthy' for row in services) for name in required)}
        if action == 'start':
            provision_ingress(directory, values)
            services = ['platform', 'postgres', 'ingress'] if 'OAP_INGRESS_DOMAIN' in values else []
            compose(directory, ['up', '-d', '--no-recreate', '--wait', '--wait-timeout', '90', *services])
        else:
            candidate = image or values['OAP_IMAGE']
            if not IMAGE.fullmatch(candidate):
                raise ValueError('Use an immutable OAP image digest/content ID')
            preflight_image(directory, values, candidate)
            if candidate != values['OAP_IMAGE']:
                values['OAP_PREVIOUS_IMAGE'] = values['OAP_IMAGE']
                values['OAP_IMAGE'] = candidate
                rewrite_values(directory, values)
            compose(directory, ['up', '-d', '--no-deps', '--force-recreate', '--wait', '--wait-timeout', '90', 'platform'])
        return {'schemaVersion': PROTOCOL_VERSION, 'action': action, 'image': values['OAP_IMAGE'],
                'runtimeVerified': True, 'applicationDataRestored': False}
    finally:
        os.close(fd)
        # The advisory lock is released even after a crash; keep the inode so
        # another process cannot accidentally lock a replacement file.


def owner_setup(directory, binary, output):
    directory, values = installation_values(directory)
    raw = sys.stdin.buffer.read(65537)
    if len(raw) > 65536:
        raise ValueError('Owner setup input exceeds 64 KiB')
    json.loads(raw)
    port = values.get('OAP_HTTP_PORT', '')
    if not port.isdigit() or not 1024 <= int(port) <= 65535 or values.get('OAP_BIND_ADDR') != '127.0.0.1':
        raise ValueError('Owner setup helper requires the private loopback installation')
    env = {key: value for key, value in os.environ.items() if key not in ('OAP_API_TOKEN', 'OAP_CREDENTIAL_FILE', 'OAP_SESSION_FILE', 'OAP_URL')}
    env['OAP_SETUP_TOKEN'] = values['OAP_SETUP_TOKEN']
    result = subprocess.run([binary, '--url', 'http://127.0.0.1:' + port, '--out', output, '--file', '-', 'setup'], input=raw, env=env, capture_output=True, timeout=30)
    if result.returncode:
        raise ValueError('Owner setup failed; inspect setup status and use login for an existing owner')
    return {'schemaVersion': PROTOCOL_VERSION, 'ok': True, 'action': 'owner-setup', 'ownerEstablished': True, 'credentialSaved': True}

def main():
    global JSON_OUTPUT, DIAGNOSTICS_DIRECTORY
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['init', 'start', 'check', 'update', 'recover', 'owner-setup', 'backup', 'restore-check'])
    parser.add_argument('--directory', default='.local/install')
    parser.add_argument('--image')
    parser.add_argument('--docker-socket')
    parser.add_argument('--operator-only', action='store_true')
    parser.add_argument('--port', type=int, default=8878)
    parser.add_argument('--environment', default='staging')
    parser.add_argument('--postgres-image', default=POSTGRES)
    parser.add_argument('--ingress-domain', action='append', help='Owner-authorized hostname; repeat for up to 32 applications')
    parser.add_argument('--ingress-tls', choices=TLS_MODES, default='internal')
    parser.add_argument('--ingress-bind', default='127.0.0.1')
    parser.add_argument('--ingress-http-port', type=int, default=80)
    parser.add_argument('--ingress-https-port', type=int, default=443)
    parser.add_argument('--ingress-image', default=CADDY)
    parser.add_argument('--archive')
    parser.add_argument('--database')
    parser.add_argument('--json', action='store_true')
    parser.add_argument('--diagnostics', action='store_true', help='Save bounded raw provider failure locally in a private file; may contain configuration')
    parser.add_argument('--protocol-version', type=int, default=1)
    parser.add_argument('--cli-bin', default=str(ROOT / 'bin/oap'))
    parser.add_argument('--out')
    args = parser.parse_args()
    DIAGNOSTICS_DIRECTORY = args.directory if args.diagnostics else None
    JSON_OUTPUT = args.json or args.action in ('start', 'check', 'update', 'recover', 'owner-setup')
    if args.protocol_version != PROTOCOL_VERSION:
        raise ValueError('Unsupported installation protocol version')
    if args.action in ('start', 'check', 'update', 'recover'):
        if args.action == 'update' and not args.image:
            raise ValueError('Update requires an exact candidate image')
        result = lifecycle(args.directory, args.action, args.image)
        result['ok'] = result.get('healthy', True)
        print(json.dumps(result))
        if args.action == 'check' and not result['healthy']:
            sys.exit(4)
    elif args.action == 'owner-setup':
        if not args.out:
            raise ValueError('Owner setup requires --out private credential file')
        print(json.dumps(owner_setup(args.directory, args.cli_bin, args.out)))
    elif args.action == 'init':
        if not args.image:
            parser.error('--image is required')
        path = init(args.directory, args.image, args.docker_socket, args.operator_only, args.port,
                    args.environment, args.ingress_domain, args.ingress_tls, args.ingress_bind,
                    args.ingress_http_port, args.ingress_https_port, args.ingress_image, args.postgres_image)
        if args.json:
            print(json.dumps({'schemaVersion': PROTOCOL_VERSION, 'ok': True, 'action': 'init', 'prepared': True, 'started': False}))
        else:
            print('Prepared owner-mode installation:', path)
            print('Private setup secret is in .env. Run docker compose up -d --wait from that directory.')
    elif args.action == 'backup':
        if not args.archive:
            parser.error('--archive is required')
        backup(args.directory, args.archive)
        if args.json:
            print(json.dumps({'schemaVersion': PROTOCOL_VERSION, 'ok': True, 'action': 'backup', 'archiveSaved': True, 'applicationDataIncluded': False}))
        else:
            print('Quiesced metadata archive and checksum receipt saved privately.')
    else:
        if not args.archive or not args.database:
            parser.error('--archive and --database are required')
        print(json.dumps(restore_check(args.directory, args.archive, args.database)))


if __name__ == '__main__':
    if sys.version_info < (3, 8):
        sys.exit('OAP installation requires Python 3.8 or newer; keep system Python unchanged and use a supported interpreter')
    try:
        main()
    except ValueError as error:
        if JSON_OUTPUT:
            print(json.dumps({'schemaVersion': 1, 'ok': False, 'error': {'code': 'installation_rejected', 'message': str(error)}}))
        else:
            print(str(error), file=sys.stderr)
        sys.exit(1)
    except (RuntimeError, OSError, subprocess.SubprocessError) as error:
        diagnostic = save_diagnostics(error)
        if JSON_OUTPUT:
            print(json.dumps({'schemaVersion': 1, 'ok': False, 'diagnosticsFile': diagnostic, 'error': {'code': 'installation_unavailable', 'message': 'Inspect installation, controller state and the retained configuration before retrying'}}))
        else:
            print('Installation operation failed. Check paths, socket permissions, image identity and controller state; existing data is retained.', file=sys.stderr)
        sys.exit(1)
