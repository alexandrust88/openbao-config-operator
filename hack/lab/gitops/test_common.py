"""Offline contracts for the shared GitOps fixture and lifecycle assertions."""
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('gitops_common', Path(__file__).with_name('common.py'))
common = importlib.util.module_from_spec(spec)
spec.loader.exec_module(common)


class FixtureTests(unittest.TestCase):
    def test_namespace_and_deletion_contract(self):
        objects = common.fixtures()
        self.assertEqual(len(objects), 17)
        for tenant in ('root', 'child'):
            selected = [o for o in objects if o['metadata']['name'].startswith(tenant + '-')]
            for obj in selected:
                self.assertEqual(obj['spec']['driftInterval'], '30s')
                self.assertEqual(obj['spec'].get('namespace'), 'gitops-team' if tenant == 'child' else None)
                self.assertEqual(obj['spec']['deletionPolicy'], 'Delete' if obj['metadata']['name'] == tenant + '-delete' else 'Orphan')
        self.assertFalse(any(o['kind'] == 'Secret' for o in objects))

    def test_git_update_changes_only_reader_policy(self):
        before, after = common.fixtures(1), common.fixtures(2)
        changed = [old['metadata']['name'] for old, new in zip(before, after) if old != new]
        self.assertEqual(changed, ['root-reader', 'child-reader'])

    def test_policy_update_grants_metadata_list_without_data_write(self):
        self.assertNotIn('metadata', common.policy(1))
        self.assertIn('path "gitops-kv/metadata/app/*" { capabilities = ["list"] }', common.policy(2))
        self.assertNotIn('"update"', common.policy(2))

    def test_prune_preserves_mounts_connections_and_namespace(self):
        with tempfile.TemporaryDirectory() as tmp:
            h = object.__new__(common.Harness)
            h.repo = type('Repository', (), {'path': Path(tmp)})()
            h.write_fixtures(2, prune=True)
            objects = json.loads((Path(tmp) / 'config/objects.json').read_text())['items']
            self.assertEqual(len(objects), 13)
            self.assertEqual(sum(obj['kind'] == 'SecretsMount' for obj in objects), 4)
            self.assertNotIn('root-orphan', [o['metadata']['name'] for o in objects])

    def test_no_shared_context(self):
        with patch.dict(os.environ, {'LAB_CONTEXT': 'production'}):
            with self.assertRaises(ValueError):
                common.Harness()

    def test_repeated_lab_uses_new_checkout_and_preserves_previous_evidence(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / 'hack/e2e').mkdir(parents=True)
            (root / 'hack/e2e/openbao.yaml').write_text('apiVersion: v1\nkind: Namespace\nmetadata: {name: openbao}\n')
            (root / 'charts/openbao-config-operator').mkdir(parents=True)
            paths = []
            class Repository:
                def __init__(self, path, url):
                    self.path = path
                    path.mkdir(exist_ok=False)
                    paths.append(path)
                def commit(self, message): pass
            h = object.__new__(common.Harness)
            h.context = 'kind-repeat'
            h.operator_values = {}
            h.get = lambda *args: {'data': {'ca.crt': 'public'}}
            with patch.object(common, 'ROOT', root), patch.object(common, 'Repository', Repository):
                h.prepare_repository(1234)
                (paths[0] / 'evidence').write_text('first run')
                h.prepare_repository(5678)
            self.assertNotEqual(paths[0], paths[1])
            self.assertEqual((paths[0] / 'evidence').read_text(), 'first run')
            self.assertTrue((paths[1] / 'config/objects.json').exists())

    def test_credentials_not_written_to_git(self):
        with tempfile.TemporaryDirectory() as tmp:
            h = object.__new__(common.Harness)
            h.context = 'kind-test'
            h.operator_values = {'image': {'tag': 'immutable'}}
            h.get = lambda *args: {'data': {'ca.crt': 'public-certificate'}}
            class Repository:
                def __init__(self, path, url):
                    self.path = Path(tmp) / 'repository'
                    self.path.mkdir()
                def commit(self, message): pass
            with patch.object(common, 'Repository', Repository):
                h.prepare_repository(1234)
            source = (h.repo.path / 'openbao/openbao.yaml').read_text()
            self.assertNotIn('\nkind: Secret\n', source)
            self.assertNotIn('e2e-admin-token-not-real', source)
            config = json.loads((h.repo.path / 'config/connection.json').read_text())
            self.assertNotIn('Secret', [o['kind'] for o in config['items']])
            self.assertEqual(config['items'][-1]['spec']['auth']['token']['secretRef']['name'], 'gitops-bootstrap')


class LiveAssertionTests(unittest.TestCase):
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

    def test_invalid_update_requires_current_synced_error_despite_previous_ready(self):
        current = {'metadata': {'generation': 3}, 'spec': {'policy': 'path "unterminated {'},
                   'status': {'conditions': [
                       {'type': 'Ready', 'status': 'True', 'reason': 'Available', 'observedGeneration': 2},
                       {'type': 'Synced', 'status': 'False', 'reason': 'ReconcileError', 'observedGeneration': 3}]}}
        self.assertTrue(common.invalid_policy_rejected(current))
        failure = current['status']['conditions'][1]
        failure['observedGeneration'] = 2
        self.assertFalse(common.invalid_policy_rejected(current))
        failure['observedGeneration'] = 3
        failure['reason'] = 'ReferenceNotReady'
        self.assertFalse(common.invalid_policy_rejected(current))
        failure['reason'] = 'ReconcileError'
        failure['status'] = 'True'
        self.assertFalse(common.invalid_policy_rejected(current))
        failure['status'] = 'False'
        current['spec']['policy'] = common.policy(2)
        self.assertFalse(common.invalid_policy_rejected(current))

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
                if method == 'GET' and path == 'gitops-kv/data/app/probe':
                    return 200, {'data': {'data': {'value': 'gitops-public-probe'}}}
                return 403, {}
        bao = Bao()
        with self.assertRaisesRegex(RuntimeError, 'crossed the namespace boundary'):
            common.assert_role_access(bao, 'root')
        self.assertEqual(bao.calls[-1], ('root', 'POST', 'auth/token/revoke-self', 'fixture'))


if __name__ == '__main__':
    unittest.main()
