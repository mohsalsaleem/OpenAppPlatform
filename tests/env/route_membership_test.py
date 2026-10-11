import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('membership', ROOT / 'scripts/coolify-route-membership.py')
routes = importlib.util.module_from_spec(spec); spec.loader.exec_module(routes)


class MembershipTests(unittest.TestCase):
    def setUp(self):
        self.app = {'id': 'a'*32, 'version': 2, 'manifest': {'targetId': 'staging', 'environment': 'staging', 'components': [{'name': 'api', 'instances': 3}]}}
        self.target = {'id': 'staging', 'operator': 'coolify', 'environment': 'staging', 'serverId': 'server'}
        self.owner = {'role': 'owner', 'kind': 'session', 'userId': 'owner', 'workspaceId': 'default'}
        self.instances = [{'component': 'api', 'ordinal': ordinal, 'resourceId': str(ordinal), 'status': 'running:healthy', 'resource': {'id': str(ordinal), 'description': 'OpenAppPlatform:'+'a'*32+':api', 'status': 'running:healthy', 'artifactKind': 'image', 'url': 'https://api'+str(ordinal)+'.test'}} for ordinal in range(1, 4)]
        self.peer = {'engineId': 'engine', 'proxyId': 'proxy', 'image': 'traefik:v3.7', 'fileProvider': True}
        self.name, self.domain = 'oap-example', 'shared.test'

    def native(self, urls):
        config = json.dumps(routes.renderer.render(self.name, self.domain, urls), indent=2)+'\n'
        return {'config': config, 'sha256': hashlib.sha256(config.encode()).hexdigest(), 'peer': self.peer}

    def footprint(self):
        return routes.snapshot(self.app, self.target, self.instances, [], self.owner, 'api')

    def test_adds_new_binding_then_removes_retained_retirement(self):
        footprint, known = self.footprint()
        plan = routes.plan_for(footprint, known, self.native(['https://api1.test','https://api2.test']), self.name, self.domain)
        self.assertEqual(plan['added'], ['https://api3.test'])
        self.assertFalse(routes.verify_plan(plan, footprint, known, self.native(['https://api1.test','https://api2.test'])))
        self.assertTrue(routes.verify_plan(plan, footprint, known, self.native(['https://api1.test','https://api2.test','https://api3.test'])))
        self.app['manifest']['components'][0]['instances'] = 2
        self.app['version'] += 1
        self.instances[2].update({'retired': True, 'status': 'exited'})
        self.instances[2]['resource']['status'] = 'exited'
        footprint, known = self.footprint()
        down = routes.plan_for(footprint, known, self.native(['https://api1.test','https://api2.test','https://api3.test']), self.name, self.domain)
        self.assertEqual(down['removed'], ['https://api3.test'])

    def test_rejects_stale_definition_target_proxy_and_modified_plan(self):
        footprint, known = self.footprint()
        native = self.native(['https://api1.test','https://api2.test'])
        plan = routes.plan_for(footprint, known, native, self.name, self.domain)
        for key in ('version','targetHash','manifestHash','owner'):
            changed = copy.deepcopy(footprint); changed[key] = 'changed'
            with self.subTest(key=key), self.assertRaises(ValueError): routes.verify_plan(plan, changed, known, native)
        changed_native = copy.deepcopy(native); changed_native['peer']['proxyId'] = 'replacement'
        with self.assertRaises(ValueError): routes.verify_plan(plan, footprint, known, changed_native)
        altered = copy.deepcopy(plan); altered['config'] = '{}'
        with self.assertRaises(ValueError): routes.verify_plan(altered, footprint, known, native)
        # Even formatting-only external edits invalidate the reviewed native hash.
        changed_native = copy.deepcopy(native); changed_native['sha256'] = 'b'*64
        with self.assertRaises(ValueError): routes.verify_plan(plan, footprint, known, changed_native)

    def test_preserves_custom_native_routes_and_foreign_backends(self):
        footprint, known = self.footprint()
        foreign = self.native(['https://foreign.test'])
        with self.assertRaises(ValueError): routes.plan_for(footprint, known, foreign, self.name, self.domain)
        native = self.native(['https://api1.test','https://api2.test'])
        config = json.loads(native['config']); config['http']['routers']['another-app'] = {}
        native['config'] = json.dumps(config)
        with self.assertRaises(ValueError): routes.plan_for(footprint, known, native, self.name, self.domain)

    def test_rejects_agents_unhealthy_or_foreign_binding_and_uncertain_work(self):
        self.owner['kind'] = 'agent'
        with self.assertRaises(ValueError): self.footprint()
        self.owner['kind'] = 'session'
        self.instances[2]['status'] = 'not deployed'
        with self.assertRaises(ValueError): self.footprint()
        self.instances[2]['status'] = 'running:healthy'
        self.instances[2]['resource']['description'] = 'different-owner'
        with self.assertRaises(ValueError): self.footprint()
        self.instances[2]['resource']['description'] = 'OpenAppPlatform:'+'a'*32+':api'
        for release in ({'state':'running'}, {'state':'attention'}, {'state':'abandoned','control':{'mode':'abandon'}}):
            with self.subTest(release=release), self.assertRaises(ValueError):
                routes.snapshot(self.app, self.target, self.instances, [release], self.owner, 'api')
