"""A bounded traffic probe retains observations when its action finishes early."""
import http.server
import json
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import threading
import unittest


class ProbeTests(unittest.TestCase):
    def test_signal_finishes_successful_probe_and_writes_summary(self):
        observed = threading.Event()

        class Handler(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                body = b'{"instance":"fixture","version":"v1"}'
                self.send_response(200)
                self.send_header('Content-Length', str(len(body)))
                self.end_headers()
                self.wfile.write(body)
                observed.set()

            def log_message(self, *args):
                pass

        server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        child = None
        try:
            with tempfile.TemporaryDirectory() as folder:
                output = Path(folder) / 'probe.json'
                child = subprocess.Popen([sys.executable, '-I', str(Path(__file__).with_name('probe.py')),
                                          '--url', 'http://127.0.0.1:' + str(server.server_port),
                                          '--duration', '30', '--concurrency', '1', '--out', str(output)],
                                         stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
                self.assertTrue(observed.wait(10), 'probe never reached the fixture')
                child.send_signal(signal.SIGTERM)
                stdout, stderr = child.communicate(timeout=10)
                self.assertEqual(child.returncode, 0, stderr)
                summary = json.loads(output.read_text())
                self.assertEqual(summary, json.loads(stdout))
                self.assertTrue(summary['stoppedBySignal'])
                self.assertGreater(summary['requests'], 0)
                self.assertEqual(summary['failures'], 0)
                self.assertEqual(summary['distinctInstances'], 1)
        finally:
            if child is not None and child.poll() is None:
                child.kill()
                child.communicate()
            server.shutdown()
            server.server_close()
            thread.join()


if __name__ == '__main__':
    unittest.main()
