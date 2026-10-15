#!/usr/bin/env python3
"""Continuous HTTP probe for the v0.1 VPS release journey.

Sends requests to one URL for a fixed duration and records status codes,
failures and the distinct backend instances that answered (from the JSON
"instance" field returned by the hello-api fixture). Writes one JSON summary.
Application traffic only: it never calls OAP or Docker.
"""
import argparse
import json
import signal
import ssl
import sys
import threading
import time
import urllib.error
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--url', required=True)
    parser.add_argument('--duration', type=float, default=30.0, help='seconds')
    parser.add_argument('--concurrency', type=int, default=4)
    parser.add_argument('--interval', type=float, default=0.1, help='seconds between requests per worker')
    parser.add_argument('--timeout', type=float, default=5.0)
    parser.add_argument('--cafile', help='trust this CA (internal-CA installs); default system trust')
    parser.add_argument('--out', help='write the JSON summary here as well as stdout')
    parser.add_argument('--expect-instances', type=int, help='fail unless this many distinct instances answered')
    parser.add_argument('--max-failures', type=int, help='fail if more requests than this failed')
    args = parser.parse_args()

    context = ssl.create_default_context(cafile=args.cafile) if args.cafile else ssl.create_default_context()
    lock = threading.Lock()
    summary = {'url': args.url, 'requests': 0, 'failures': 0, 'statuses': {}, 'instances': {},
               'versions': {}, 'errors': {}, 'startedAt': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())}
    deadline = time.monotonic() + args.duration
    stop = threading.Event()
    # The operator can end a probe after the observed action completes without
    # losing its measurements. The duration remains an upper bound.
    def finish(_signum, _frame):
        stop.set()
    signal.signal(signal.SIGTERM, finish)
    signal.signal(signal.SIGINT, finish)

    def record(status, instance=None, version=None, error=None):
        with lock:
            summary['requests'] += 1
            summary['statuses'][str(status)] = summary['statuses'].get(str(status), 0) + 1
            if error is not None or not (200 <= status < 300):
                summary['failures'] += 1
            if error is not None:
                summary['errors'][error] = summary['errors'].get(error, 0) + 1
            if instance:
                summary['instances'][instance] = summary['instances'].get(instance, 0) + 1
            if version:
                summary['versions'][version] = summary['versions'].get(version, 0) + 1

    def worker():
        while not stop.is_set() and time.monotonic() < deadline:
            request = urllib.request.Request(args.url, headers={'Cache-Control': 'no-store'})
            try:
                with urllib.request.urlopen(request, timeout=args.timeout, context=context) as response:
                    body = response.read(65536)
                    instance = version = None
                    try:
                        data = json.loads(body)
                        if isinstance(data, dict):
                            instance, version = data.get('instance'), data.get('version')
                    except ValueError:
                        pass
                    record(response.status, instance, version)
            except urllib.error.HTTPError as e:
                record(e.code)
            except Exception as e:  # connection reset, timeout, TLS, DNS
                record(0, error=type(e).__name__)
            stop.wait(args.interval)

    threads = [threading.Thread(target=worker, daemon=True) for _ in range(args.concurrency)]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join()

    summary['finishedAt'] = time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())
    summary['stoppedBySignal'] = stop.is_set()
    summary['distinctInstances'] = len(summary['instances'])
    ok = True
    if args.expect_instances is not None and summary['distinctInstances'] < args.expect_instances:
        ok = False
    if args.max_failures is not None and summary['failures'] > args.max_failures:
        ok = False
    summary['ok'] = ok
    text = json.dumps(summary, indent=2, sort_keys=True)
    if args.out:
        with open(args.out, 'w') as handle:
            handle.write(text + '\n')
    print(text)
    return 0 if ok else 1


if __name__ == '__main__':
    sys.exit(main())
