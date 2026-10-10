#!/usr/bin/env python3
"""Trusted SSH image builder: one JSON request on stdin, one result on stdout."""
import argparse
import hashlib
import json
import re
import shlex
import subprocess
import sys
from pathlib import Path
from urllib.parse import urlparse


def validate(request):
    if not re.fullmatch(r'[a-f0-9]{32}', request.get('buildId', '')):
        raise ValueError('A 32-character build ID is required')
    if not re.fullmatch(r'[a-f0-9]{40}', request.get('commit', '')):
        raise ValueError('An exact commit SHA is required')
    component = request['component']
    context = component.get('context', '.')
    if context.startswith('/') or '..' in context.split('/') or '\\' in context:
        raise ValueError('Context must stay inside the repository')
    if not re.fullmatch(r'[a-z0-9/._:-]+', component.get('imageRepository', '')):
        raise ValueError('Invalid image repository')
    if ':' in component['imageRepository'].split('/')[-1]:
        raise ValueError('Image repository must not contain a tag')
    return request


# Fixed remote program. Request fields are decoded as data, never shell source.
REMOTE = r'''
import base64,fcntl,hashlib,json,os,pathlib,subprocess,sys
request=json.loads(base64.b64decode(sys.argv[1]))
root=pathlib.Path.home()/'.cache/openappplatform-builds'/request['buildId']
root.mkdir(parents=True,exist_ok=True)
lock=open(root/'lock','a');fcntl.flock(lock,fcntl.LOCK_EX)
fingerprint=hashlib.sha256(json.dumps(request,sort_keys=True).encode()).hexdigest()
intent=root/'intent.json';receipt=root/'receipt.json'
if intent.exists() and json.loads(intent.read_text())['fingerprint']!=fingerprint:
    raise SystemExit('Build ID is already associated with a different request')
if receipt.exists():
    print(receipt.read_text());raise SystemExit(0)
image=request['component']['imageRepository']
tag=image+':oap-'+request['buildId']
if intent.exists():
    # An interrupted operation needs explicit owner review; never blindly rebuild.
    raise SystemExit('Uncertain prior build; inspect this build ID before retrying')
intent.write_text(json.dumps({'fingerprint':fingerprint,'commit':request['commit'],'tag':tag}))
def run(argv):
    subprocess.run(argv,check=True,stdout=sys.stderr,stderr=sys.stderr)
run(['docker','build','--label','org.opencontainers.image.revision='+request['commit'],
     '--label','org.opencontainers.image.source=https://github.com/'+request['repository'],
     '--label','io.openappplatform.build-id='+request['buildId'],'--tag',tag,str(root/'contexts'/fingerprint)])
run(['docker','push',tag])
inspection=json.loads(subprocess.check_output(['docker','image','inspect',tag]))[0]
labels=inspection.get('Config',{}).get('Labels',{})
if labels.get('org.opencontainers.image.revision')!=request['commit']:
    raise SystemExit('Built image revision differs')
digests=[d for d in inspection.get('RepoDigests',[]) if d.startswith(image+'@sha256:')]
if len(digests)!=1:raise SystemExit('Registry digest unavailable or ambiguous')
result={'commit':request['commit'],'image':digests[0]}
temporary=root/'receipt.tmp';temporary.write_text(json.dumps(result));os.replace(temporary,receipt)
print(json.dumps(result))
'''


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--ssh-host', required=True)
    parser.add_argument('--repository-root', required=True)
    args = parser.parse_args()
    if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9_.@-]*', args.ssh_host):
        raise ValueError('Use a configured SSH host alias')
    request = validate(json.load(sys.stdin))
    root = Path(args.repository_root).resolve()
    origin = subprocess.check_output(['git', '-C', str(root), 'remote', 'get-url', 'origin'], text=True).strip()
    if origin.startswith('git@github.com:'):
        repository = origin.removeprefix('git@github.com:').removesuffix('.git')
    else:
        parsed = urlparse(origin)
        if parsed.hostname != 'github.com':
            raise ValueError('This builder requires an approved GitHub repository')
        repository = parsed.path.strip('/').removesuffix('.git')
    if repository.lower() != request['repository'].lower():
        raise ValueError('Repository checkout does not match the binding')
    subprocess.run(['git', '-C', str(root), 'cat-file', '-e', request['commit'] + '^{commit}'], check=True)
    context = request['component'].get('context', '.')
    tree = request['commit'] if context == '.' else request['commit'] + ':' + context
    fingerprint = hashlib.sha256(json.dumps(request, sort_keys=True).encode()).hexdigest()
    remote_context = '.cache/openappplatform-builds/' + request['buildId'] + '/contexts/' + fingerprint
    archive = subprocess.Popen(['git', '-C', str(root), 'archive', '--format=tar', tree], stdout=subprocess.PIPE)
    upload = subprocess.run(['ssh', '-o', 'BatchMode=yes', args.ssh_host,
                             'mkdir -p ' + shlex.quote(remote_context) + ' && tar -xf - -C ' + shlex.quote(remote_context)], stdin=archive.stdout)
    archive.stdout.close()
    if archive.wait() != 0 or upload.returncode != 0:
        raise RuntimeError('Source archive upload failed')
    import base64
    encoded_request = base64.b64encode(json.dumps(request).encode()).decode()
    command = 'python3 -c ' + shlex.quote(REMOTE) + ' ' + shlex.quote(encoded_request)
    subprocess.run(['ssh', '-o', 'BatchMode=yes', args.ssh_host, command], check=True)


if __name__ == '__main__':
    try:
        main()
    except Exception:
        print('Server image build did not complete; inspect its build ID and host logs.', file=sys.stderr)
        sys.exit(1)
