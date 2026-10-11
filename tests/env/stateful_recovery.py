"""Real pg_dump/pg_restore drill restricted to disposable local test projects."""
import argparse
import json
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[2]


def run_drill(host, project, report_dir):
    if not host.startswith('unix:///') or not project.startswith('oap-test-'):
        raise ValueError('Recovery drill requires a local disposable oap-test project')
    compose = ['docker', '--host', host, 'compose', '--env-file', 'tests/env/images.env',
               '-f', 'tests/env/compose.yaml', '-p', project, 'exec', '-T', 'postgres']

    def native(args, input_bytes=None, expected_success=True):
        result = subprocess.run(compose + args, cwd=ROOT, input=input_bytes,
                                capture_output=True, timeout=30)
        if (result.returncode == 0) != expected_success:
            raise RuntimeError('Unexpected native PostgreSQL recovery result: ' + args[0])
        return result.stdout

    def sql(database, text, expected_success=True):
        return native(['psql', '-X', '-U', 'oap', '-d', database, '-v', 'ON_ERROR_STOP=1',
                       '-At'], text.encode(), expected_success).decode().strip()

    source, restored = 'application_recovery_source', 'application_recovery_restored'
    report_dir = Path(report_dir)
    report_dir.mkdir(parents=True, exist_ok=True)
    created = []
    try:
        for database in (source, restored):
            native(['createdb', '-U', 'oap', database])
            created.append(database)
        sql(source, (ROOT / 'examples/stateful-recovery/schema-v1.sql').read_text())
        backup = native(['pg_dump', '-U', 'oap', '-Fc', '--no-owner', '--no-acl', source])
        if not backup.startswith(b'PGDMP'):
            raise RuntimeError('Native backup did not produce a custom-format archive')
        backup_path = report_dir / 'application-data.dump'
        backup_path.write_bytes(backup)
        backup_path.chmod(0o600)
        sql(source, (ROOT / 'examples/stateful-recovery/schema-v2.sql').read_text())
        # The earlier application contract fails after the incompatible migration.
        sql(source, 'SELECT email FROM customers WHERE id=1;', expected_success=False)
        if sql(source, 'SELECT version FROM schema_version; SELECT count(*) FROM customers;') != '2\n2':
            raise RuntimeError('Migration/write fixture was not applied')
        # Corrupt archives must fail, not yield a claimed successful recovery.
        native(['pg_restore', '-U', 'oap', '--exit-on-error', '--no-owner', '--no-acl',
                '-d', restored], b'not a PostgreSQL backup', expected_success=False)
        native(['pg_restore', '-U', 'oap', '--exit-on-error', '--no-owner', '--no-acl',
                '-d', restored], backup_path.read_bytes())
        if sql(restored, 'SELECT version FROM schema_version; SELECT email FROM customers WHERE id=1; SELECT count(*) FROM customers;') != '1\nbefore-backup@example.test\n1':
            raise RuntimeError('Restored application contract or data did not match backup')
        if sql(source, 'SELECT version FROM schema_version; SELECT count(*) FROM customers;') != '2\n2':
            raise RuntimeError('Isolated restore changed the source database')
        # Validate a write on the recovered schema before claiming it usable.
        sql(restored, "INSERT INTO customers VALUES (3, 'after-restore@example.test');")
        if sql(restored, 'SELECT count(*) FROM customers;') != '2':
            raise RuntimeError('Recovered schema did not accept application writes')
        receipt = {'status': 'passed', 'backup': backup_path.name,
                   'backupSchemaVersion': 1, 'sourceSchemaVersion': 2,
                   'restoredSchemaVersion': 1, 'postBackupRowsExcluded': True,
                   'sourcePreserved': True, 'corruptArchiveRejected': True,
                   'restoredReadWriteVerified': True,
                   'scope': 'isolated native PostgreSQL application-data procedure; no OAP automatic restore'}
        (report_dir / 'stateful-recovery.json').write_text(json.dumps(receipt, indent=2) + '\n')
        print('Native application-data backup/restore and schema compatibility drill passed')
    finally:
        for database in reversed(created):
            native(['dropdb', '-U', 'oap', database])


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--docker-host', required=True)
    parser.add_argument('--project', required=True)
    parser.add_argument('--report-dir', required=True)
    args = parser.parse_args()
    run_drill(args.docker_host, args.project, args.report_dir)
