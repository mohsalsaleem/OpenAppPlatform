"""Bounded test-only accounting for the packaged OAP/PostgreSQL/Caddy stack."""
import json
from pathlib import Path

from footprint import Sampler, docker_json


SERVICES = ('platform', 'postgres', 'ingress')


def goals(idle):
    def metric(service, field):
        value = idle.get(service, {})
        if value.get('missingSamples', 0) or (field == 'cpuMeanOneCorePercent' and not value.get('cpuIntervals')):
            return None
        return value.get(field)

    def combined(field):
        values = [metric(service, field) for service in SERVICES]
        return None if any(value is None for value in values) else sum(values)

    def result(value, threshold):
        return {'status': 'unavailable' if value is None else 'met' if value <= threshold else 'missed',
                'measured': value, 'threshold': threshold}

    return {'platformIdleRssUnder256MiB': result(metric('platform', 'rssPeakBytes'), 256 * 1024**2),
            'stackIdleSummedRssUnder512MiB': result(combined('rssPeakBytes'), 512 * 1024**2),
            'stackIdleCpuUnderOneCorePercent': result(combined('cpuMeanOneCorePercent'), 1)}


def measure(endpoint, native, compose, request, runtime_commit='unrecorded'):
    identifiers = {service: compose('ps', '-q', service).decode().strip() for service in SERVICES}
    if any(not identifier for identifier in identifiers.values()):
        raise RuntimeError('Ingress footprint requires all three packaged services')
    sampler = Sampler(endpoint, native, lambda: identifiers, lambda: {}, None)
    phases = [sampler.phase('idle', 30)]
    requests = 0

    def traffic(_):
        nonlocal requests
        # Five fresh TLS connections every five seconds: light bursts, not load
        # capacity or sustained throughput. Persist no bodies, headers or certs.
        for _ in range(5):
            if request()[0] != 200:
                raise RuntimeError('Measured ingress request failed')
            requests += 1

    phases.append(sampler.phase('light_https', 30, tick=traffic))
    for phase in phases:
        for service in SERVICES:
            value = phase['services'][service]
            if value['missingSamples'] or value['rssPeakBytes'] is None or value['workingSetPeakBytes'] is None or value['cpuIntervals'] < 1:
                raise RuntimeError('Required ingress footprint counters unavailable; never infer zero')
    info = docker_json(endpoint, '/info')
    images = {}
    for service, identifier in identifiers.items():
        container = docker_json(endpoint, '/containers/' + identifier + '/json')
        images[service] = docker_json(endpoint, '/images/' + container['Image'] + '/json')['Size']
    report = {'schemaVersion': 1, 'status': 'measured', 'runtimeCommit': runtime_commit,
              'profile': {'applications': 1, 'components': 1, 'instances': 1, 'tls': 'local_ca',
                          'idleSeconds': 30, 'trafficSeconds': 30, 'requests': requests,
                          'trafficPattern': 'five fresh verified HTTPS connections every five seconds'},
              'host': {key: info.get(key) for key in ('NCPU', 'MemTotal', 'Architecture', 'OperatingSystem', 'ServerVersion')},
              'imageBytes': images, 'phases': phases, 'goals': goals(phases[0]['services']),
              'definitions': {'platformAndIngressRss': 'VmRSS of /proc/1/status',
                              'postgresRss': 'sum of PostgreSQL process VmRSS; shared pages can be counted repeatedly',
                              'stackRss': 'conservative sum of separate service peaks, not unique physical memory',
                              'workingSet': 'Docker memory usage minus inactive file cache',
                              'cpu': 'Docker counter delta / system delta * online CPUs * 100; percentage of one core',
                              'runtimeRequests': 'not instrumented in this profile; null is unavailable, not zero'},
              'limits': ['One-app Docker Desktop/Linux fixture; no capacity or public ACME/renewal claim',
                         'Workloads, Docker Engine, builds and browser/test runner excluded from stack accounting',
                         'Sampler exec and ordinary health checks can contribute CPU; short samples can miss peaks',
                         'Footprint goals are reported separately from functional acceptance; misses remain open for M3.7']}
    Path('/report/ingress-footprint.json').write_text(json.dumps(report, indent=2) + '\n')
    return {'status': 'measured', 'services': list(SERVICES), 'report': 'ingress-footprint.json',
            'goals': report['goals'], 'requests': requests}
