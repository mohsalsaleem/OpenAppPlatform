#!/usr/bin/env python3
import argparse
import json
import os
import urllib.request
import uuid
from pathlib import Path
parser = argparse.ArgumentParser()
parser.add_argument('manifest', type=Path)
parser.add_argument('--deploy', action='store_true')
args = parser.parse_args()
base = 'http://' + os.getenv('OAP_ADDR', '127.0.0.1:8787') + '/api/v1'
def call(path, data, key=None):
    headers = {'Authorization': 'Bearer ' + os.environ['OAP_API_TOKEN'], 'Content-Type': 'application/json'}
    if key: headers['Idempotency-Key'] = key
    req = urllib.request.Request(base + path, data=json.dumps(data).encode(), headers=headers, method='POST')
    with urllib.request.urlopen(req) as response: return json.load(response)
app = call('/applications', json.loads(args.manifest.read_text()))
print('Application:', app['id'])
if args.deploy:
    release = call('/applications/' + app['id'] + '/deployments', {}, str(uuid.uuid4()))
    print('Deployment:', release['id'], release['state'])
