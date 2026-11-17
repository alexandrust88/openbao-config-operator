"""Check public-source trust boundaries and real static configuration contracts."""
import importlib.util
import os
import subprocess
from pathlib import Path
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('gitops_common', Path(__file__).with_name('common.py'))
common = importlib.util.module_from_spec(spec)
spec.loader.exec_module(common)


class FixtureTests(unittest.TestCase):
    def test_namespace_and_credential_contract(self):
        objects = common.fixtures()
        self.assertEqual(len(objects), 13)
        for obj in objects:
            self.assertEqual(obj['spec'].get('namespace'), 'gitops-team' if obj['metadata']['name'].startswith('child-') else None)
            self.assertNotEqual(obj['kind'], 'Secret')
            self.assertEqual(obj['spec']['deletionPolicy'], 'Orphan')
        self.assertIn('gitops-kv/metadata/app/*', common.policy())
        self.assertNotIn('"update"', common.policy())

    def test_only_first_raft_pod_loads_initialization(self):
        values = common.load_yaml(common.ROOT / 'gitops/openbao/values.yaml')['server']
        self.assertNotIn('initialize', values['ha']['raft']['config'])
        init = common.load_yaml(common.ROOT / 'gitops/openbao/bootstrap/init.yaml')
        self.assertIn('initialize "lab"', init['data']['init.hcl'])
        for ordinal, expected in ((0, '1'), (1, '0'), (2, '0')):
            result = subprocess.check_output(['sh', '-c', 'set -- ' + values['extraArgs'] + '; printf "%s" "$#"'],
                                             env={**os.environ, 'HOSTNAME': 'openbao-' + str(ordinal)}, text=True)
            self.assertEqual(result, expected)

    def test_no_shared_context(self):
        with patch.dict(os.environ, {'LAB_CONTEXT': 'production'}):
            with self.assertRaises(ValueError): common.Harness()

    def test_public_github_repository_only(self):
        for url in ('http://github.com/a/b.git', 'https://token@github.com/a/b.git', 'https://example.com/a/b.git',
                    'https://github.com/a/b.git?token=private'):
            with patch.dict(os.environ, {'LAB_CONTEXT': 'kind-test', 'GITOPS_REPOSITORY': url}):
                with self.assertRaises(ValueError): common.Harness()

    def test_main_resolves_to_exact_public_commit(self):
        sha = 'a' * 40
        env = {'LAB_CONTEXT': 'kind-test', 'GITOPS_REPOSITORY': 'https://github.com/owner/repo.git', 'GITOPS_REVISION': 'main'}
        with patch.dict(os.environ, env), patch.object(common, 'run', return_value=sha + '\trefs/heads/main\n') as run:
            h = common.Harness()
        self.assertEqual(h.revision, sha)
        self.assertIn('credential.helper=', run.call_args.args[0])
        self.assertIn('http.extraHeader=', run.call_args.args[0])
        with patch.dict(os.environ, env), patch.object(common, 'run', return_value=''):
            with self.assertRaises(RuntimeError): common.Harness()

    def test_sha_does_not_require_git_lookup(self):
        with patch.dict(os.environ, {'LAB_CONTEXT': 'kind-test', 'GITOPS_REVISION': 'b' * 40}), patch.object(common, 'run') as run:
            self.assertEqual(common.Harness().revision, 'b' * 40)
            run.assert_not_called()

    def test_current_ready_without_synced_is_not_success(self):
        h = object.__new__(common.Harness)
        current = {'metadata': {'generation': 2}, 'status': {'conditions': [
            {'type': 'Ready', 'status': 'True', 'observedGeneration': 2},
            {'type': 'Synced', 'status': 'True', 'observedGeneration': 1}]}}
        h.get = lambda *args, **kwargs: current
        with patch.object(common, 'fixtures', return_value=[common.fixtures()[0]]), patch.object(common, 'eventually') as poll:
            h.wait_resources()
            check = poll.call_args.args[0]
            self.assertFalse(check())
            current['status']['conditions'][1]['observedGeneration'] = 2
            self.assertTrue(check())

    def test_failed_namespace_denial_revokes_login_and_fails(self):
        class Bao:
            calls = []
            def request(self, tenant, method, path, payload=None, token=None):
                self.calls.append((tenant, method, path, token))
                if path.endswith('/role-id'): return 200, {'data': {'role_id': 'fixture'}}
                if path.endswith('/secret-id'): return 200, {'data': {'secret_id': 'fixture'}}
                if path.endswith('/login'): return 200, {'auth': {'client_token': 'fixture'}}
                if path == 'auth/token/revoke-self': return 204, {}
                if token is None: return 200, {}
                if method == 'LIST': return 200, {'data': {'keys': ['probe']}}
                if method == 'GET' and path == 'gitops-kv/data/app/probe':
                    return 200, {'data': {'data': {'value': 'gitops-public-probe'}}}
                return 403, {}
        bao = Bao()
        with self.assertRaisesRegex(RuntimeError, 'crossed the namespace boundary'):
            common.assert_role_access(bao, 'root')
        self.assertEqual(bao.calls[-1], ('root', 'POST', 'auth/token/revoke-self', 'fixture'))
