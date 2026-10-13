#!/usr/bin/env python3
"""Small local installation and quiesced metadata backup helpers; no env loading."""
import argparse
import fcntl
from datetime import datetime, timezone
import hashlib
import json
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


def private_write(path, text):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as stream:
        stream.write(text)


def init(directory, image, socket=None, operator_only=False, port=8878):
    if not IMAGE.fullmatch(image) or not 1024 <= port <= 65535:
        raise ValueError('Use an immutable OAP image digest/content ID and port 1024–65535')
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
        'OAP_POSTGRES_IMAGE': POSTGRES,
        'POSTGRES_PASSWORD': secrets.token_urlsafe(32),
        'OAP_SETUP_TOKEN': secrets.token_urlsafe(32),
        'OAP_COOKIE_SECURE': 'false',
        'OAP_BIND_ADDR': '127.0.0.1',
        'OAP_HTTP_PORT': str(port),
        'OAP_BACKUP_REPORTS_DIR': '/app/backup-reports',
    }
    if not operator_only:
        env.update(OAP_DOCKER_SOCKET=str(socket), OAP_DOCKER_GID=str(gid))
    private_write(directory / '.env', ''.join(f'{key}={value}\n' for key, value in env.items()))
    for name in ('compose.yaml', 'compose.docker.yaml'):
        shutil.copyfile(ROOT / 'deploy' / name, directory / name)
    targets = [] if operator_only else [{
        'id': 'docker-local', 'name': 'Local Docker', 'operator': 'docker',
        'url': 'unix:///var/run/docker.sock', 'environment': 'staging',
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
            return {'schemaVersion': PROTOCOL_VERSION, 'action': action, 'services': services,
                    'healthy': all(any(row['service'] == name and row['state'] == 'running' and row['health'] == 'healthy' for row in services) for name in ('platform', 'postgres'))}
        if action == 'start':
            compose(directory, ['up', '-d', '--wait', '--wait-timeout', '90'])
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
    global JSON_OUTPUT
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['init', 'start', 'check', 'update', 'recover', 'owner-setup', 'backup', 'restore-check'])
    parser.add_argument('--directory', default='.local/install')
    parser.add_argument('--image')
    parser.add_argument('--docker-socket')
    parser.add_argument('--operator-only', action='store_true')
    parser.add_argument('--port', type=int, default=8878)
    parser.add_argument('--archive')
    parser.add_argument('--database')
    parser.add_argument('--json', action='store_true')
    parser.add_argument('--protocol-version', type=int, default=1)
    parser.add_argument('--cli-bin', default=str(ROOT / 'bin/oap'))
    parser.add_argument('--out')
    args = parser.parse_args()
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
        path = init(args.directory, args.image, args.docker_socket, args.operator_only, args.port)
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
    try:
        main()
    except ValueError as error:
        if JSON_OUTPUT:
            print(json.dumps({'schemaVersion': 1, 'ok': False, 'error': {'code': 'installation_rejected', 'message': str(error)}}))
        else:
            print(str(error), file=sys.stderr)
        sys.exit(1)
    except (RuntimeError, OSError, subprocess.SubprocessError):
        if JSON_OUTPUT:
            print(json.dumps({'schemaVersion': 1, 'ok': False, 'error': {'code': 'installation_unavailable', 'message': 'Inspect installation, controller state and the retained configuration before retrying'}}))
        else:
            print('Installation operation failed. Check paths, socket permissions, image identity and controller state; existing data is retained.', file=sys.stderr)
        sys.exit(1)
