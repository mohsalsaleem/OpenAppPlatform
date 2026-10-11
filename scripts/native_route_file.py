"""Bounded native file transport. Only cooperating OAP publishers share its lock."""
import fcntl
import hashlib
import json
from pathlib import Path
import re
import subprocess


class NativeProxy:
    def __init__(self, docker=None, lock_dir=None):
        self.docker = docker or ['docker']
        self.lock_dir = Path(lock_dir or Path.home() / '.local/state/openappplatform/route-locks')

    def run(self, args, data=None):
        result = subprocess.run(self.docker + args, input=data, capture_output=True, timeout=25)
        if result.returncode:
            raise RuntimeError('Native route operation rejected; inspect route identity/drift')
        return result.stdout

    def peer(self):
        value = json.loads(self.run(['inspect', 'coolify-proxy']))[0]
        return {'engineId': self.run(['info', '--format', '{{.ID}}']).decode().strip(),
                'proxyId': value['Id'], 'image': value['Config']['Image'],
                'fileProvider': '--providers.file.directory=/traefik/dynamic/' in value['Config'].get('Cmd', [])}

    def path(self, name):
        if not re.fullmatch(r'oap-[a-z0-9-]{1,48}', name):
            raise ValueError('Only an explicit OAP route filename is supported')
        return '/traefik/dynamic/' + name + '.yaml'

    def read(self, name):
        path = self.path(name)
        peer = self.peer()
        value = self.run(['exec', peer['proxyId'], 'sh', '-c',
                          'test ! -L "$1" && test -f "$1" && head -c 65537 "$1"', 'route-read', path])
        if len(value) > 65536:
            raise ValueError('Native route exceeds the bounded configuration size')
        return {'peer': peer, 'sha256': hashlib.sha256(value).hexdigest(), 'config': value.decode()}

    def publish(self, name, expected_peer, expected_hash, config):
        self.path(name)
        data = config.encode()
        if len(data) > 65536 or not re.fullmatch(r'[a-f0-9]{64}', expected_hash):
            raise ValueError('Invalid bounded route update')
        self.lock_dir.mkdir(parents=True, exist_ok=True, mode=0o700)
        with (self.lock_dir / (name + '.lock')).open('a') as lock:
            fcntl.flock(lock, fcntl.LOCK_EX)
            current = self.read(name)
            if current['peer'] != expected_peer:
                raise RuntimeError('Native proxy identity changed; review a fresh plan')
            desired_hash = hashlib.sha256(data).hexdigest()
            if current['sha256'] == desired_hash:
                return {'status': 'alreadyApplied', 'sha256': desired_hash}
            if current['sha256'] != expected_hash:
                raise RuntimeError('Native route changed; review a fresh plan')
            # Recheck immediately before atomic replacement. Native UI editors do
            # not share our lock; this is not CAS across every external writer.
            script = '''set -eu
test ! -L "$1" && test -f "$1"
test "$(sha256sum "$1" | cut -d ' ' -f 1)" = "$2"
temporary=$(mktemp /traefik/dynamic/.oap-route-XXXXXX)
trap 'rm -f "$temporary"' EXIT
cat > "$temporary"
test "$(sha256sum "$temporary" | cut -d ' ' -f 1)" = "$3"
chmod 644 "$temporary"
test "$(sha256sum "$1" | cut -d ' ' -f 1)" = "$2"
mv "$temporary" "$1"
'''
            self.run(['exec', '-i', expected_peer['proxyId'], 'sh', '-c', script,
                      'route-update', self.path(name), expected_hash, desired_hash], data)
            if self.read(name)['sha256'] != desired_hash:
                raise RuntimeError('Route publication outcome is uncertain; inspect before retrying')
            return {'status': 'applied', 'sha256': desired_hash}


if __name__ == '__main__':
    import sys
    request = json.load(sys.stdin)
    proxy = NativeProxy()
    if request['mode'] == 'read':
        result = proxy.read(request['name'])
    elif request['mode'] == 'publish':
        result = proxy.publish(request['name'], request['peer'], request['sha256'], request['config'])
    else:
        raise ValueError('Unknown native route operation')
    print(json.dumps(result))
