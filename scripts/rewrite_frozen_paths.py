#!/usr/bin/env python3
"""Reject rewrite commits that touch the frozen old tree.

Guardrail 4 of docs/architecture/rewrite-plan.md: commits on the rewrite branch
may not change the old tree, except security fixes the owner approved, marked
with an `Owner-Approved: security` line in the commit message. Retiring the
old tree (R5.5) is the one other exception: a commit with an
`Owner-Approved: retire` line may delete frozen files, and only delete them
(owner decision, 2026-10-28). Commits that come from the base branch
(normally main) are not checked.
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
RETIRE = 'Owner-Approved: retire'


def git(*args, cwd=None):
    return subprocess.run(['git', *args], cwd=cwd, check=True, capture_output=True, text=True).stdout


def frozen(path):
    return path.startswith(FROZEN)


def violations(base, head='HEAD', cwd=None):
    out = []
    for commit in git('rev-list', '--no-merges', f'{base}..{head}', cwd=cwd).split():
        message = git('log', '-1', '--format=%B', commit, cwd=cwd)
        lines = [line.strip() for line in message.splitlines()]
        if APPROVAL in lines:
            continue
        retire = RETIRE in lines
        changes = git('diff-tree', '--no-commit-id', '--name-status', '--no-renames', '-r', '--root', commit, cwd=cwd).split('\n')
        touched = sorted(path for status, _, path in (c.partition('\t') for c in changes if c)
                         if frozen(path) and not (retire and status == 'D'))
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
        print(f'Frozen old tree changed. Revert, or get owner approval and add "{APPROVAL}"'
              f' ("{RETIRE}" allows only deletions).', file=sys.stderr)
        return 1
    print('frozen paths: ok')
    return 0


if __name__ == '__main__':
    sys.exit(main())
