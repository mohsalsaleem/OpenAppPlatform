"""Test-only Docker request counter and metadata-only footprint measurements."""
import http.client
import http.server
import json
import os
from pathlib import Path
import socket
import socketserver
import threading
import time
from collections import Counter


class UnixHTTP(http.client.HTTPConnection):
    def __init__(self, endpoint, timeout=10):
        super().__init__('docker', timeout=timeout)
        self.endpoint = endpoint

    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(self.timeout)
        self.sock.connect(self.endpoint)


def docker_json(endpoint, path):
    connection = UnixHTTP(endpoint)
    try:
        connection.request('GET', '/v1.45' + path)
        response = connection.getresponse()
        raw = response.read((8 << 20) + 1)
        if response.status != 200 or len(raw) > 8 << 20:
            raise RuntimeError('Bounded Docker measurement unavailable')
        return json.loads(raw)
    finally:
        connection.close()


class SocketProxy:
    """Only the isolated fixture platform uses this proxy. No headers/bodies persist."""
    def __init__(self, path, upstream):
        self.path, self.upstream = str(path), str(upstream)
        self.disabled = False
        self.counts = Counter()
        self.lock = threading.Lock()
        proxy = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def forward(self):
                # Do not retain URLs, identifiers, bodies, credentials or headers.
                parts = self.path.split('?', 1)[0].split('/')
                category = parts[2] if len(parts) > 2 else 'other'
                if category not in ('containers', 'images', 'networks', 'version', 'info'):
                    category = 'other'
                with proxy.lock:
                    proxy.counts[self.command + ':' + category] += 1
                try:
                    length = int(self.headers.get('Content-Length', '0'))
                except ValueError:
                    self.send_error(400)
                    return
                if length < 0 or length > 8 << 20 or self.headers.get('Transfer-Encoding'):
                    self.send_error(413)
                    return
                if proxy.disabled:
                    self.send_error(503, 'Test target outage')
                    return
                body = self.rfile.read(length) if length else None
                connection = UnixHTTP(proxy.upstream)
                try:
                    headers = {k: v for k, v in self.headers.items() if k.lower() not in ('host', 'connection', 'transfer-encoding')}
                    connection.request(self.command, self.path, body=body, headers=headers)
                    response = connection.getresponse()
                    data = response.read((8 << 20) + 1)
                    if len(data) > 8 << 20:
                        self.send_error(502)
                        return
                    self.send_response(response.status)
                    self.send_header('Content-Type', response.getheader('Content-Type', 'application/json'))
                    self.send_header('Content-Length', str(len(data)))
                    self.end_headers()
                    if data and self.command != 'HEAD':
                        self.wfile.write(data)
                except (BrokenPipeError, ConnectionResetError):
                    # A cancelled/empty-body caller has already closed its socket.
                    return
                except (OSError, http.client.HTTPException):
                    try:
                        self.send_error(503, 'Test proxy unavailable')
                    except (BrokenPipeError, ConnectionResetError):
                        pass
                finally:
                    connection.close()

            do_GET = do_POST = do_DELETE = do_PUT = do_PATCH = do_HEAD = forward

        class Server(socketserver.ThreadingMixIn, socketserver.UnixStreamServer):
            daemon_threads = True

        self.server = Server(self.path, Handler)
        os.chmod(self.path, 0o660)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    def snapshot(self):
        with self.lock:
            return dict(self.counts)

    def close(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=2)
        Path(self.path).unlink(missing_ok=True)


def cpu_percent(previous, current):
    """Docker's one-core percentage, using two samples and online CPU count."""
    try:
        cpu_delta = current['cpu_stats']['cpu_usage']['total_usage'] - previous['cpu_stats']['cpu_usage']['total_usage']
        system_delta = current['cpu_stats']['system_cpu_usage'] - previous['cpu_stats']['system_cpu_usage']
        cores = current['cpu_stats']['online_cpus']
        if cpu_delta < 0 or system_delta <= 0 or cores <= 0:
            return None
        return cpu_delta / system_delta * cores * 100
    except (KeyError, TypeError):
        return None


def working_set(stats):
    memory = stats.get('memory_stats', {})
    if 'usage' not in memory:
        return None
    detail = memory.get('stats', {})
    inactive = detail.get('total_inactive_file', detail.get('inactive_file', 0))
    return memory['usage'] - inactive if inactive < memory['usage'] else memory['usage']


class Sampler:
    def __init__(self, endpoint, native, identifiers, metadata, proxy, interval=5):
        self.endpoint, self.native = endpoint, native
        self.identifiers, self.metadata = identifiers, metadata
        self.proxy, self.interval = proxy, interval

    def rss(self, identifier, service):
        if service == 'platform':
            raw = self.native(['exec', identifier, 'awk', r'/^VmRSS:/{printf "%.0f\n", $2*1024}', '/proc/1/status'])
        else:
            # Sum PostgreSQL process VmRSS. Shared buffers can be counted by
            # more than one process; also publish cgroup working set separately.
            command = r'''awk '/^Name:/{pg=($2=="postgres")} /^VmRSS:/{if(pg) total+=$2*1024} END{printf "%.0f\n", total+0}' /proc/[0-9]*/status 2>/dev/null'''
            raw = self.native(['exec', identifier, '/bin/sh', '-c', command])
        return int(raw.strip())

    def collect(self):
        result = {'at': time.monotonic(), 'services': {}}
        for service, identifier in self.identifiers().items():
            try:
                stats = docker_json(self.endpoint, '/containers/' + identifier + '/stats?stream=false&one-shot=true')
                result['services'][service] = {'id': identifier, 'stats': stats, 'rssBytes': self.rss(identifier, service), 'workingSetBytes': working_set(stats)}
            except (RuntimeError, OSError, ValueError, http.client.HTTPException):
                result['services'][service] = {'id': identifier, 'missing': True}
        return result

    def phase(self, name, duration, action=None, tick=None):
        counts = self.proxy.snapshot()
        before_db = self.metadata()
        started = time.monotonic()
        samples = [self.collect()]
        if action:
            action()
        next_tick = started + self.interval
        while time.monotonic() - started < duration:
            time.sleep(max(0, next_tick - time.monotonic()))
            if tick:
                tick(len(samples))
            samples.append(self.collect())
            next_tick += self.interval
        elapsed = time.monotonic() - started
        end_counts = self.proxy.snapshot()
        report = {'name': name, 'elapsedSeconds': round(elapsed, 3), 'sampleIntervalSeconds': self.interval,
                  'samples': len(samples), 'services': {}, 'metadataBefore': before_db, 'metadataAfter': self.metadata(),
                  'runtimeRequests': {k: end_counts.get(k, 0) - counts.get(k, 0) for k in end_counts}}
        report['runtimeRequestsPerSecond'] = round(sum(report['runtimeRequests'].values()) / elapsed, 4)
        for service in ('platform', 'postgres'):
            observed = [s['services'].get(service, {}) for s in samples]
            resident = [s['rssBytes'] for s in observed if 'rssBytes' in s]
            working = [s['workingSetBytes'] for s in observed if s.get('workingSetBytes') is not None]
            cpu = []
            for a, b in zip(observed, observed[1:]):
                if 'stats' in a and 'stats' in b and a.get('id') == b.get('id'):
                    value = cpu_percent(a['stats'], b['stats'])
                    if value is not None:
                        cpu.append(value)
            report['services'][service] = {'rssPeakBytes': max(resident) if resident else None,
                'rssMeanBytes': sum(resident) / len(resident) if resident else None,
                'workingSetPeakBytes': max(working) if working else None,
                'cpuMeanOneCorePercent': sum(cpu) / len(cpu) if cpu else None,
                'cpuIntervals': len(cpu), 'missingSamples': sum(1 for s in observed if s.get('missing') or not s)}
        return report
