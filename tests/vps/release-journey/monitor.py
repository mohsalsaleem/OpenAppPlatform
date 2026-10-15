#!/usr/bin/env python3
"""Bounded host monitor for the release journey on a small VPS.

run      Append one JSON line every --interval seconds to --out: available and
         total memory, swap, load average, and (every --docker-interval
         seconds) per-container memory and CPU from `docker stats`. Stops after
         --duration seconds, at --max-lines, or on SIGTERM/SIGINT. Uses only the
         standard library and /proc; a missing docker CLI is recorded, not fatal.
summary  Read a monitor file and print peaks: lowest available memory, highest
         load, swap use, and peak memory per container. With --kernel-log it
         also counts out-of-memory kills reported by the kernel.

Start it in the background before Phase A3 and keep it running through Phase X:
  nohup python3 -I monitor.py run --out $OUT/monitor.jsonl --duration 14400 &
"""
import argparse
import json
import signal
import subprocess
import sys
import time

stop = False


def handle(_signum, _frame):
    global stop
    stop = True


def meminfo():
    values = {}
    with open('/proc/meminfo') as handle:
        for line in handle:
            key, rest = line.split(':', 1)
            values[key] = int(rest.split()[0]) // 1024  # MiB
    return {'memTotalMiB': values.get('MemTotal'), 'memAvailableMiB': values.get('MemAvailable'),
            'swapTotalMiB': values.get('SwapTotal'), 'swapUsedMiB': values.get('SwapTotal', 0) - values.get('SwapFree', 0)}


def loadavg():
    with open('/proc/loadavg') as handle:
        one, five, fifteen = handle.read().split()[:3]
    return {'load1': float(one), 'load5': float(five), 'load15': float(fifteen)}


def docker_stats():
    try:
        result = subprocess.run(['docker', 'stats', '--no-stream', '--format', '{{json .}}'],
                                capture_output=True, text=True, timeout=20)
    except (OSError, subprocess.TimeoutExpired) as e:
        return {'error': type(e).__name__}
    containers = {}
    for line in result.stdout.splitlines()[:64]:
        try:
            row = json.loads(line)
        except ValueError:
            continue
        containers[row.get('Name', '?')] = {'mem': row.get('MemUsage'), 'cpu': row.get('CPUPerc')}
    return {'containers': containers}


def to_mib(text):
    """Parse the used part of docker's MemUsage, e.g. '21.4MiB / 64MiB'."""
    try:
        value = text.split('/')[0].strip()
        for unit, factor in (('GiB', 1024), ('MiB', 1), ('KiB', 1 / 1024), ('kB', 1 / 1024), ('B', 1 / 1048576)):
            if value.endswith(unit):
                return float(value[:-len(unit)]) * factor
    except (AttributeError, ValueError):
        pass
    return None


def run(args):
    signal.signal(signal.SIGTERM, handle)
    signal.signal(signal.SIGINT, handle)
    deadline = time.monotonic() + args.duration
    next_docker = 0.0
    lines = 0
    with open(args.out, 'a', buffering=1) as out:
        while not stop and time.monotonic() < deadline and lines < args.max_lines:
            sample = {'t': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())}
            sample.update(meminfo())
            sample.update(loadavg())
            if time.monotonic() >= next_docker:
                sample.update(docker_stats())
                next_docker = time.monotonic() + args.docker_interval
            out.write(json.dumps(sample) + '\n')
            lines += 1
            time.sleep(args.interval)
    return 0


def summary(args):
    rows = []
    with open(args.file) as handle:
        for line in handle:
            try:
                rows.append(json.loads(line))
            except ValueError:
                continue
    if not rows:
        print(json.dumps({'ok': False, 'error': 'no samples'}))
        return 1
    available = [r['memAvailableMiB'] for r in rows if r.get('memAvailableMiB') is not None]
    peaks = {}
    for row in rows:
        for name, stats in row.get('containers', {}).items():
            mib = to_mib(stats.get('mem'))
            if mib is not None:
                peaks[name] = round(max(peaks.get(name, 0.0), mib), 1)
    result = {'ok': True, 'samples': len(rows), 'from': rows[0]['t'], 'to': rows[-1]['t'],
              'memTotalMiB': rows[0].get('memTotalMiB'),
              'minMemAvailableMiB': min(available) if available else None,
              'maxSwapUsedMiB': max((r.get('swapUsedMiB') or 0) for r in rows),
              'maxLoad1': max(r.get('load1', 0) for r in rows),
              'containerPeakMiB': dict(sorted(peaks.items()))}
    if args.kernel_log:
        try:
            log = subprocess.run(['dmesg'], capture_output=True, text=True, timeout=20).stdout
        except (OSError, subprocess.TimeoutExpired):
            log = ''
        if not log:
            try:
                log = subprocess.run(['journalctl', '-k', '--no-pager', '-q'], capture_output=True, text=True, timeout=30).stdout
            except (OSError, subprocess.TimeoutExpired):
                log = None
        result['oomKills'] = None if log is None else sum(1 for line in log.splitlines()
                                                           if 'Out of memory' in line or 'oom-kill' in line)
    print(json.dumps(result, indent=2))
    return 0


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest='action', required=True)
    r = sub.add_parser('run')
    r.add_argument('--out', required=True)
    r.add_argument('--interval', type=float, default=5.0)
    r.add_argument('--docker-interval', type=float, default=30.0)
    r.add_argument('--duration', type=float, default=14400.0)
    r.add_argument('--max-lines', type=int, default=5000)
    s = sub.add_parser('summary')
    s.add_argument('--file', required=True)
    s.add_argument('--kernel-log', action='store_true', help='count kernel OOM kills (may need root)')
    args = parser.parse_args()
    return run(args) if args.action == 'run' else summary(args)


if __name__ == '__main__':
    sys.exit(main())
