import os
from pathlib import Path
import subprocess
import tempfile
import unittest

import rewrite_frozen_paths as frozen


class FrozenPathsTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.repo = self.tmp.name
        self.git('init', '-q', '-b', 'main')
        self.commit('internal/controller/a.go', 'base')
        self.git('checkout', '-q', '-b', 'rewrite')

    def tearDown(self):
        self.tmp.cleanup()

    def git(self, *args):
        env = dict(os.environ, GIT_AUTHOR_NAME='t', GIT_AUTHOR_EMAIL='t@example.com',
                   GIT_COMMITTER_NAME='t', GIT_COMMITTER_EMAIL='t@example.com')
        subprocess.run(['git', *args], cwd=self.repo, check=True, capture_output=True, env=env)

    def commit(self, path, message, content=None):
        file = Path(self.repo, path)
        file.parent.mkdir(parents=True, exist_ok=True)
        file.write_text(content or message)
        self.git('add', '-A')
        self.git('commit', '-q', '-m', message)

    def check(self):
        return frozen.violations('main', cwd=self.repo)

    def test_new_tree_changes_pass(self):
        self.commit('internal/core/plan.go', 'core')
        self.commit('cmd/oapd/main.go', 'oapd')
        self.commit('docs/x.md', 'docs')
        self.assertEqual(self.check(), [])

    def test_frozen_change_fails(self):
        self.commit('internal/core/plan.go', 'core')
        self.commit('internal/controller/a.go', 'tweak controller', content='changed')
        found = self.check()
        self.assertEqual(len(found), 1)
        self.assertEqual(found[0][1], 'tweak controller')
        self.assertEqual(found[0][2], ['internal/controller/a.go'])

    def test_similar_names_are_not_frozen(self):
        self.commit('cmd/oapd/main.go', 'oapd')
        self.commit('cmd/oapctl/main.go', 'oapctl')
        self.commit('internal/core/store/x.go', 'nested store name')
        self.assertEqual(self.check(), [])

    def test_web_and_commands_are_frozen(self):
        self.commit('web/src/app.tsx', 'web')
        self.commit('cmd/oap/main.go', 'old cli')
        self.assertEqual([v[1] for v in self.check()], ['old cli', 'web'])

    def test_owner_approved_security_fix_passes(self):
        self.commit('internal/store/s.go', 'Fix token check\n\nOwner-Approved: security')
        self.assertEqual(self.check(), [])

    def test_approval_must_be_its_own_line(self):
        self.commit('internal/store/s.go', 'Not Owner-Approved: security really')
        self.assertEqual(len(self.check()), 1)

    def test_commits_from_base_are_not_checked(self):
        self.git('checkout', '-q', 'main')
        self.commit('internal/controller/b.go', 'main work')
        self.git('checkout', '-q', 'rewrite')
        self.git('merge', '-q', '--no-edit', 'main')
        self.assertEqual(self.check(), [])

    def test_deletion_counts(self):
        self.git('rm', '-q', 'internal/controller/a.go')
        self.git('commit', '-q', '-m', 'delete old controller')
        self.assertEqual(len(self.check()), 1)


if __name__ == '__main__':
    unittest.main()
