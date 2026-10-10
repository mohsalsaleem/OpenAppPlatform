import importlib.util
import pathlib
import unittest
spec = importlib.util.spec_from_file_location('builder', pathlib.Path(__file__).with_name('build-on-server.py'))
builder = importlib.util.module_from_spec(spec)
spec.loader.exec_module(builder)
class Validation(unittest.TestCase):
    def request(self):
        return {'buildId':'a'*32,'commit':'b'*40,'component':{'context':'.','imageRepository':'127.0.0.1:5001/oap/staging'}}
    def test_rejects_unpinned_commit_and_escaping_context(self):
        for value in ['../outside','/tmp/context','a/../../b']:
            request=self.request();request['component']['context']=value
            with self.assertRaises(ValueError):builder.validate(request)
        request=self.request();request['commit']='main'
        with self.assertRaises(ValueError):builder.validate(request)
    def test_rejects_tags_and_shell_payloads(self):
        for value in ['repo:latest','repo; touch /tmp/x','repo$(whoami)']:
            request=self.request();request['component']['imageRepository']=value
            with self.assertRaises(ValueError):builder.validate(request)
    def test_allows_local_registry_with_exact_commit(self):
        self.assertEqual(builder.validate(self.request())['commit'],'b'*40)
if __name__=='__main__':unittest.main()
