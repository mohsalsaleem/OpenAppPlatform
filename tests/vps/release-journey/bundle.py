#!/usr/bin/env python3
"""Build the release-journey candidate off-host and load it on a small VPS.

prepare  (build machine): build the OAP image, the oap and oap-mcp binaries and
         every fixture image for the VPS platform, then write a bundle directory:
           images.tar.gz   all images (docker save)
           bin/oap, bin/oap-mcp
           manifest.json   commit, platform, image IDs, sha256 of every file
           journey.env     shell exports (OAP_IMAGE, JOB, WEB, API1, ...)
load     (VPS): verify every checksum, `docker load` the images and confirm
         each tag has exactly the image ID recorded in the manifest.

Nothing is compiled on the VPS. Image IDs survive save/load, so the IDs in the
manifest are the ones the spec and installer use. Prints one JSON summary;
non-zero exit on any mismatch. --dry-run prints the commands instead.
"""
import argparse
import gzip
import hashlib
import json
import os
import shutil
import subprocess
import sys
import time
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[2]

# tag -> (dockerfile or None for <context>/Dockerfile, context, build args)
FIXTURES = [
    ('oap-journey-api:base', None, 'examples/hello-api', {}),
    ('oap-journey-notes:v1', None, 'examples/persistent-web', {}),
    ('oap-journey-notes:v2', 'tests/vps/release-journey/notes-variant.Dockerfile', 'tests/vps/release-journey',
     {'BASE': 'oap-journey-notes:v1'}),
    ('oap-journey-worker:v1', None, 'examples/worker', {}),
    ('oap-journey-job:v1', 'tests/vps/release-journey/job.Dockerfile', 'tests/vps/release-journey', {}),
    ('oap-journey-api:web-v1', 'tests/vps/release-journey/api-variant.Dockerfile', 'tests/vps/release-journey',
     {'BASE': 'oap-journey-api:base', 'VERSION': 'web-v1'}),
    ('oap-journey-api:v1', 'tests/vps/release-journey/api-variant.Dockerfile', 'tests/vps/release-journey',
     {'BASE': 'oap-journey-api:base', 'VERSION': 'v1'}),
    ('oap-journey-api:v2', 'tests/vps/release-journey/api-variant.Dockerfile', 'tests/vps/release-journey',
     {'BASE': 'oap-journey-api:base', 'VERSION': 'v2'}),
    ('oap-journey-api:broken', 'tests/vps/release-journey/api-broken.Dockerfile', 'tests/vps/release-journey',
     {'BASE': 'oap-journey-api:base'}),
]
OAP_TAG = 'openappplatform:rc'
# exported variable name -> tag
EXPORTS = {'OAP_IMAGE': OAP_TAG, 'JOB': 'oap-journey-job:v1', 'WEB': 'oap-journey-api:web-v1',
           'API1': 'oap-journey-api:v1', 'API2': 'oap-journey-api:v2', 'APIBAD': 'oap-journey-api:broken',
           'NOTES': 'oap-journey-notes:v1', 'NOTES2': 'oap-journey-notes:v2',
           'WORKER': 'oap-journey-worker:v1'}


class Runner:
    def __init__(self, dry_run):
        self.dry_run = dry_run
        self.commands = []

    def run(self, command, env=None, capture=False):
        self.commands.append(' '.join(command))
        if self.dry_run:
            return 'sha256:' + '0' * 64 if capture else ''
        result = subprocess.run(command, cwd=REPO, env=env, check=True, text=True,
                                stdout=subprocess.PIPE if capture else None)
        return result.stdout.strip() if capture else ''


def sha256(path):
    digest = hashlib.sha256()
    with open(path, 'rb') as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b''):
            digest.update(chunk)
    return digest.hexdigest()


def image_id(runner, tag):
    return runner.run(['docker', 'image', 'inspect', '--format', '{{.Id}}', tag], capture=True)


def prepare(args):
    runner = Runner(args.dry_run)
    out = Path(args.out).resolve()
    started = time.time()
    platform = args.platform
    goos, goarch = platform.split('/')[:2]
    if not args.dry_run:
        if out.exists() and any(out.iterdir()):
            raise SystemExit(f'{out} is not empty; choose a new bundle directory')
        (out / 'bin').mkdir(parents=True, exist_ok=True)
    commit = runner.run(['git', 'rev-parse', 'HEAD'], capture=True) if not args.dry_run else 'DRY-RUN'
    dirty = bool(runner.run(['git', 'status', '--porcelain'], capture=True)) if not args.dry_run else False

    runner.run(['docker', 'build', '--platform', platform,
                '--label', f'org.opencontainers.image.revision={commit}', '-t', OAP_TAG, '.'])
    for tag, dockerfile, context, build_args in FIXTURES:
        command = ['docker', 'build', '--platform', platform, '-t', tag]
        if dockerfile:
            command += ['-f', dockerfile]
        for key, value in build_args.items():
            command += ['--build-arg', f'{key}={value}']
        runner.run(command + [context])

    env = dict(os.environ, GOOS=goos, GOARCH=goarch, CGO_ENABLED='0')
    for name in ('oap', 'oap-mcp'):
        runner.run(['go', 'build', '-trimpath', '-o', str(out / 'bin' / name), f'./cmd/{name}'], env=env)

    tags = [OAP_TAG] + [tag for tag, *_ in FIXTURES]
    ids = {tag: image_id(runner, tag) for tag in tags}
    tar_path, archive = out / 'images.tar', out / 'images.tar.gz'
    runner.run(['docker', 'save', '-o', str(tar_path)] + tags)
    if not args.dry_run:
        with open(tar_path, 'rb') as source, gzip.open(archive, 'wb', compresslevel=6) as target:
            shutil.copyfileobj(source, target, 1 << 20)
        tar_path.unlink()

    files = {}
    if not args.dry_run:
        for path in [archive, out / 'bin' / 'oap', out / 'bin' / 'oap-mcp']:
            files[str(path.relative_to(out))] = {'sha256': sha256(path), 'bytes': path.stat().st_size}
    manifest = {'schemaVersion': 1, 'commit': commit, 'dirtyTree': dirty, 'platform': platform,
                'createdAt': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()),
                'prepareSeconds': round(time.time() - started, 1), 'images': ids, 'files': files}
    if not args.dry_run:
        (out / 'manifest.json').write_text(json.dumps(manifest, indent=2, sort_keys=True) + '\n')
        (out / 'journey.env').write_text(''.join(f'export {k}={ids[v]}\n' for k, v in EXPORTS.items()))
    summary = {'ok': not dirty or args.allow_dirty, 'action': 'prepare', 'bundle': str(out), 'manifest': manifest}
    if args.dry_run:
        summary['commands'] = runner.commands
    if dirty and not args.allow_dirty:
        summary['error'] = 'working tree has uncommitted changes; commit them or pass --allow-dirty'
    print(json.dumps(summary, indent=2))
    return 0 if summary['ok'] else 1


def load(args):
    runner = Runner(args.dry_run)
    bundle = Path(args.bundle).resolve()
    started = time.time()
    manifest = json.loads((bundle / 'manifest.json').read_text())
    problems = []
    for name, expected in manifest['files'].items():
        actual = sha256(bundle / name)
        if actual != expected['sha256']:
            problems.append(f'checksum mismatch: {name}')
    if problems:
        print(json.dumps({'ok': False, 'action': 'load', 'problems': problems}, indent=2))
        return 1
    runner.run(['docker', 'load', '-i', str(bundle / 'images.tar.gz')])
    loaded = {}
    for tag, expected in manifest['images'].items():
        actual = image_id(runner, tag)
        loaded[tag] = actual
        if not args.dry_run and actual != expected:
            problems.append(f'image ID mismatch: {tag}')
    for name in ('oap', 'oap-mcp'):
        path = bundle / 'bin' / name
        if path.exists():
            path.chmod(0o755)
    summary = {'ok': not problems, 'action': 'load', 'commit': manifest['commit'],
               'platform': manifest['platform'], 'loadSeconds': round(time.time() - started, 1),
               'images': loaded, 'problems': problems}
    if args.dry_run:
        summary['commands'] = runner.commands
    print(json.dumps(summary, indent=2))
    return 0 if summary['ok'] else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest='action', required=True)
    p = sub.add_parser('prepare', help='build machine: build and package the candidate')
    p.add_argument('--out', required=True, help='new, empty bundle directory')
    p.add_argument('--platform', default='linux/amd64', help='VPS platform, e.g. linux/amd64 or linux/arm64')
    p.add_argument('--allow-dirty', action='store_true', help='permit uncommitted changes (recorded in manifest)')
    p.add_argument('--dry-run', action='store_true')
    l = sub.add_parser('load', help='VPS: verify and load the bundle')
    l.add_argument('--bundle', required=True)
    l.add_argument('--dry-run', action='store_true')
    args = parser.parse_args()
    return prepare(args) if args.action == 'prepare' else load(args)


if __name__ == '__main__':
    sys.exit(main())
