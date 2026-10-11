import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('route_render', Path(__file__).resolve().parents[2] / 'scripts/render-coolify-route.py')
route = importlib.util.module_from_spec(spec)
spec.loader.exec_module(route)


class NativeRouteTests(unittest.TestCase):
    def test_health_and_native_tls_without_replacing_host_routes(self):
        result = route.render('oap-demo', 'demo.example.test', ['https://one.example.test', 'https://two.example.test'])
        service = result['http']['services']['oap-demo']['loadBalancer']
        self.assertFalse(service['passHostHeader'])
        self.assertEqual(service['healthCheck']['path'], '/health/ready')
        self.assertEqual(len(service['servers']), 2)
        self.assertEqual(result['http']['routers']['oap-demo-https']['tls']['certResolver'], 'letsencrypt')

    def test_rejects_route_injection_credentials_duplicates_and_self_loop(self):
        for domain, upstreams in [('bad`) || Host(`other.test', ['https://one.test']),
                                  ('demo.test', ['https://user:secret@one.test']),
                                  ('demo.test', ['https://demo.test']),
                                  ('demo.test', ['https://one.test', 'https://one.test']),
                                  ('demo.test', ['https://one.test/path']),
                                  ('demo.test', ['https://one.test:bad'])]:
            with self.subTest(domain=domain, upstreams=upstreams), self.assertRaises(ValueError):
                route.render('oap-demo', domain, upstreams)
