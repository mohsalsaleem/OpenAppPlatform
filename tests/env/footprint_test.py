import http.server
import json
from pathlib import Path
import socketserver
import tempfile
import threading
import unittest

from footprint import SocketProxy, UnixHTTP, cpu_percent, working_set


class FootprintTests(unittest.TestCase):
    def test_cpu_missing_reset_and_working_set(self):
        old = {'cpu_stats': {'cpu_usage': {'total_usage': 100}, 'system_cpu_usage': 1000, 'online_cpus': 4}}
        new = {'cpu_stats': {'cpu_usage': {'total_usage': 110}, 'system_cpu_usage': 2000, 'online_cpus': 4}}
        self.assertEqual(cpu_percent(old, new), 4)
        self.assertIsNone(cpu_percent(new, old))
        self.assertIsNone(cpu_percent({}, new))
        self.assertEqual(working_set({'memory_stats': {'usage': 100, 'stats': {'inactive_file': 20}}}), 80)
        self.assertIsNone(working_set({}))
        self.assertEqual(working_set({'memory_stats': {'usage': 100, 'stats': {'total_inactive_file': 30, 'inactive_file': 10}}}), 70)
        self.assertEqual(working_set({'memory_stats': {'usage': 100, 'stats': {'inactive_file': 200}}}), 100)

    def test_proxy_is_transparent_counts_no_private_fields_and_can_hold_target(self):
        with tempfile.TemporaryDirectory(dir='/tmp', prefix='oap-proxy-') as folder:
            captured = []
            class Handler(http.server.BaseHTTPRequestHandler):
                def log_message(self, *_): pass
                def do_POST(self):
                    captured.append(self.rfile.read(int(self.headers['Content-Length'])))
                    self.send_response(201); self.send_header('Content-Length', '11'); self.end_headers(); self.wfile.write(b'{"ok":true}')
            upstream = socketserver.UnixStreamServer(str(Path(folder)/'up.sock'), Handler)
            thread = threading.Thread(target=upstream.serve_forever, daemon=True); thread.start()
            proxy = SocketProxy(Path(folder)/'proxy.sock', Path(folder)/'up.sock')
            try:
                connection = UnixHTTP(proxy.path)
                connection.request('POST', '/v1.45/containers/create?private=not-retained', body=b'private-body')
                response = connection.getresponse(); self.assertEqual(response.status, 201); response.read(); connection.close()
                self.assertEqual(captured, [b'private-body'])
                self.assertEqual(proxy.snapshot(), {'POST:containers': 1})
                self.assertNotIn('private', json.dumps(proxy.snapshot()))
                proxy.disabled = True
                connection = UnixHTTP(proxy.path); connection.request('GET', '/v1.45/containers/secret-id/json'); response = connection.getresponse()
                self.assertEqual(response.status, 503); response.read(); connection.close()
                self.assertEqual(len(captured), 1)
            finally:
                proxy.close(); upstream.shutdown(); upstream.server_close(); thread.join(timeout=2)


if __name__ == '__main__': unittest.main()
