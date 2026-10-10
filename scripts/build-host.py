#!/usr/bin/env python3
"""Optional private, allowlisted host service for the command builder adapter."""
import argparse
import base64
import hashlib
import hmac
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

spec = importlib.util.spec_from_file_location('ssh_builder', Path(__file__).with_name('build-on-server.py'))
helper = importlib.util.module_from_spec(spec)
spec.loader.exec_module(helper)


def allowed(request, policy):
    helper.validate(request)
    if set(request) != {'buildId', 'repository', 'repositoryId', 'commit', 'component'}:
        raise ValueError('Unknown request fields')
    binding = {k: request[k] for k in ('repository', 'repositoryId', 'component')}
    if binding not in policy['bindings']:
        raise ValueError('Build request is outside configured policy')
    return request


def build(request, policy):
    allowed(request, policy)
    root = Path.home()/'.cache/openappplatform-builds'/request['buildId']
    fingerprint = hashlib.sha256(json.dumps(request, sort_keys=True).encode()).hexdigest()
    if not (root/'intent.json').exists():
        # Only owner-allowlisted public repositories are supported here. No Git
        # tokens, controller credentials or mounted socket reach repository code.
        checkout = Path.home()/'.cache/openappplatform-sources'/hashlib.sha256(request['repository'].encode()).hexdigest()
        checkout.parent.mkdir(parents=True, exist_ok=True)
        if not checkout.exists():
            subprocess.run(['git', 'init', '--bare', str(checkout)], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        subprocess.run(['git', '-C', str(checkout), 'fetch', '--no-tags', '--depth=1', 'https://github.com/'+request['repository']+'.git', request['commit']], check=True, timeout=90, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        context = request['component']['context']
        tree = request['commit'] if context == '.' else request['commit']+':'+context
        destination = root/'contexts'/fingerprint
        destination.mkdir(parents=True, exist_ok=True)
        archive = subprocess.Popen(['git', '-C', str(checkout), 'archive', '--format=tar', tree], stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
        try:
            subprocess.run(['tar', '-xf', '-', '-C', str(destination)], stdin=archive.stdout, check=True, timeout=60, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        finally:
            archive.stdout.close()
        if archive.wait() != 0:
            raise RuntimeError('Exact source archive failed')
    encoded = base64.b64encode(json.dumps(request).encode()).decode()
    result = subprocess.run(['/usr/bin/python3', '-c', helper.REMOTE, encoded], check=True, timeout=600, capture_output=True)
    return json.loads(result.stdout)


def serve(policy):
    if len(policy.get('token', '')) < 32 or not policy.get('bindings'):
        raise ValueError('Private credential and explicit bindings required')
    gate = threading.Lock()

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass  # Never log credentials, request bodies or build output.

        def reply(self, status, body):
            raw = json.dumps(body).encode()
            self.send_response(status)
            self.send_header('Content-Type', 'application/json')
            self.send_header('Content-Length', str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)

        def do_POST(self):
            if self.path != '/build' or not hmac.compare_digest(self.headers.get('Authorization', ''), 'Bearer '+policy['token']):
                self.reply(401, {'message': 'Build authentication required'})
                return
            try:
                size = int(self.headers.get('Content-Length', '0'))
                if size < 1 or size > 8192 or self.headers.get('Transfer-Encoding'):
                    raise ValueError('Bounded request required')
                self.connection.settimeout(15)
                request = allowed(json.loads(self.rfile.read(size)), policy)
            except Exception:
                self.reply(400, {'message': 'Invalid or unapproved build request'})
                return
            if not gate.acquire(blocking=False):
                self.reply(409, {'message': 'Build host busy; inspect before retrying'})
                return
            try:
                self.connection.settimeout(620)
                self.reply(200, build(request, policy))
            except Exception:
                self.reply(409, {'message': 'Build outcome uncertain; inspect the stable build ID'})
            finally:
                gate.release()

    ThreadingHTTPServer((policy['bind'], policy['port']), Handler).serve_forever()


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--policy', required=True)
    args = parser.parse_args()
    path = Path(args.policy)
    if path.stat().st_mode & 0o077:
        raise SystemExit('Policy file must be private (mode 0600)')
    serve(json.loads(path.read_text()))
