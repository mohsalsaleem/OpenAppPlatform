#!/usr/bin/env python3
"""Reproduce committed database/API code, or reject generation drift."""
import argparse
import os
from pathlib import Path
import shutil
import subprocess

ROOT = Path(__file__).resolve().parent.parent
SQLC_VERSION = 'v1.30.0'


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--check', action='store_true')
    args = parser.parse_args()
    sqlc = os.environ.get('OAP_SQLC') or shutil.which('sqlc') or str(ROOT / '.local/go/bin/sqlc')
    if not Path(sqlc).is_file():
        raise SystemExit('Install sqlc v1.30.0: python3 scripts/run-local.py go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.30.0')
    version = subprocess.check_output([sqlc, 'version'], text=True).strip()
    if version != SQLC_VERSION:
        raise SystemExit(f'Expected sqlc {SQLC_VERSION}; got {version}')
    subprocess.run([sqlc, 'diff' if args.check else 'generate'], cwd=ROOT, check=True)
    command = ['node', 'web/node_modules/openapi-typescript/bin/cli.js', 'docs/openapi.json', '-o', 'web/src/generated/api.ts']
    if args.check:
        command.append('--check')
    subprocess.run(command, cwd=ROOT, check=True)


if __name__ == '__main__':
    main()
