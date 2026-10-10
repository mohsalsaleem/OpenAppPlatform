import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('host', Path(__file__).with_name('build-host.py'))
host = importlib.util.module_from_spec(spec)
spec.loader.exec_module(host)

class PolicyTests(unittest.TestCase):
    def test_binding_rejects_repository_context_and_destination_changes(self):
        request = {'buildId': 'a'*32, 'commit': 'b'*40, 'repository': 'owner/repo', 'repositoryId': 123, 'component': {'name': 'web', 'context': '.', 'imageRepository': '127.0.0.1:5001/staging'}}
        policy = {'bindings': [{k: request[k] for k in ('repository', 'repositoryId', 'component')}]}
        self.assertEqual(host.allowed(request, policy), request)
        for field, value in [('repository', 'other/repo'), ('repositoryId', 999), ('component', dict(request['component'], context='../')), ('component', dict(request['component'], imageRepository='registry.test/other'))]:
            with self.subTest(field=field, value=value), self.assertRaises(ValueError):
                host.allowed(dict(request, **{field: value}), policy)
        with self.assertRaises(ValueError):
            host.allowed(dict(request, command='arbitrary'), policy)

if __name__ == '__main__': unittest.main()
