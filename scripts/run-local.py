#!/usr/bin/env python3
"""Run a command with local secrets without placing values in shell arguments."""
import os
import sys
from pathlib import Path
root = Path(__file__).resolve().parent.parent
env = os.environ.copy()
path = root / '.env.local'
if path.exists():
    for line in path.read_text().splitlines():
        if line and not line.startswith('#'):
            key, value = line.split('=', 1)
            env.setdefault(key, value)
# Workspace-contained Go caches keep builds reproducible in restricted environments.
env.setdefault('GOCACHE', str(root / '.local/go-build'))
env.setdefault('GOPATH', str(root / '.local/go'))
env.setdefault('TEST_DATABASE_URL', env.get('DATABASE_URL', ''))
os.chdir(root)
if len(sys.argv) < 2:
    raise SystemExit('Usage: python3 scripts/run-local.py COMMAND [ARGS...]')
os.execvpe(sys.argv[1], sys.argv[1:], env)
