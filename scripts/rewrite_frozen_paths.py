#!/usr/bin/env python3
"""Reject rewrite commits that touch the frozen old tree.

Guardrail 4 of docs/architecture/rewrite-plan.md: commits on the rewrite branch
may not change the old tree, except security fixes the owner approved, marked
with an `Owner-Approved: security` line in the commit message. Commits that
come from the base branch (normally main) are not checked.
"""
import argparse
import subprocess
import sys

FROZEN = (
    'internal/access/', 'internal/backupreports/', 'internal/cli/', 'internal/config/',
    'internal/controller/', 'internal/domain/', 'internal/httpapi/', 'internal/ingress/',
    'internal/mcp/', 'internal/operator/', 'internal/operators/', 'internal/routing/',
    'internal/runtimeinputs/', 'internal/secretstore/', 'internal/source/', 'internal/store/',
    'cmd/platform/', 'cmd/oap/', 'cmd/oap-mcp/', 'cmd/oap-admin/', 'cmd/oap-build-client/',
    'web/',
)
APPROVAL = 'Owner-Approved: security'


def git(*args, cwd=None):
    return subprocess.run(['git', *args], cwd=cwd, check=True, capture_output=True, text=True).stdout


def frozen(path):
    return path.startswith(FROZEN)


def violations(base, head='HEAD', cwd=None):
    out = []
    for commit in git('rev-list', '--no-merges', f'{base}..{head}', cwd=cwd).split():
        message = git('log', '-1', '--format=%B', commit, cwd=cwd)
        if any(line.strip() == APPROVAL for line in message.splitlines()):
            continue
        files = git('diff-tree', '--no-commit-id', '--name-only', '-r', '--root', commit, cwd=cwd).split('\n')
        touched = sorted(f for f in files if f and frozen(f))
        if touched:
            subject = message.splitlines()[0] if message else ''
            out.append((commit[:12], subject, touched))
    return out


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--base', default='origin/main', help='base branch the rewrite started from')
    parser.add_argument('--head', default='HEAD')
    args = parser.parse_args()
    found = violations(args.base, args.head)
    for commit, subject, touched in found:
        print(f'{commit} {subject!r} touches frozen paths:', file=sys.stderr)
        for path in touched:
            print(f'  {path}', file=sys.stderr)
    if found:
        print(f'Frozen old tree changed. Revert, or get owner approval and add "{APPROVAL}".', file=sys.stderr)
        return 1
    print('frozen paths: ok')
    return 0


if __name__ == '__main__':
    sys.exit(main())
