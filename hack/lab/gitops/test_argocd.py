"""Argo CD hierarchy, permission boundary and convergence regression contracts."""
import hashlib
import json
import subprocess
import types
import unittest
from unittest.mock import patch

import argocd


class ArgoTests(unittest.TestCase):
    def setUp(self):
        self.h = types.SimpleNamespace(repo=types.SimpleNamespace(url='http://git.obco-gitops.svc:8080/repo.git'),
                                      namespace='obco-gitops', operator_values={'image': {'tag': 'immutable-build'}})

    def test_root_children_are_real_git_sources_and_helm_chart(self):
        children, root = argocd.manifests(self.h)
        self.assertEqual(root['spec']['source']['path'], 'argocd/root')
        self.assertEqual([a['spec']['source']['path'] for a in children], ['openbao', 'operator', 'config'])
        self.assertEqual([a['metadata']['annotations']['argocd.argoproj.io/sync-wave'] for a in children], ['0', '10', '20'])
        self.assertEqual(children[1]['spec']['source']['helm']['valuesObject'], self.h.operator_values)
        for app in [root, *children]:
            self.assertEqual(app['spec']['source']['repoURL'], self.h.repo.url)
            self.assertTrue(app['spec']['syncPolicy']['automated']['selfHeal'])
            self.assertTrue(app['spec']['syncPolicy']['automated']['prune'])

    def test_config_project_cannot_manage_workloads_or_secrets(self):
        projects = {p['metadata']['name']: p['spec'] for p in argocd.projects(self.h)}
        for spec in projects.values():
            self.assertEqual(spec['sourceRepos'], [self.h.repo.url])
            self.assertEqual(len(spec['destinations']), 1)
            self.assertFalse(any(item['kind'] == '*' for item in spec['namespaceResourceWhitelist']))
        config = projects['gitops-config']
        self.assertEqual(config['destinations'][0]['namespace'], self.h.namespace)
        self.assertEqual(config['clusterResourceWhitelist'], [{'group': 'core.openbaoconfig.io', 'kind': 'OpenBaoServer'}])
        self.assertFalse({'Secret', 'Application', 'Deployment', 'RoleBinding'} & {x['kind'] for x in config['namespaceResourceWhitelist']})
        self.assertEqual(projects['gitops-root']['namespaceResourceWhitelist'], [{'group': 'argoproj.io', 'kind': 'Application'}])

    def test_wrong_revision_or_unhealthy_cannot_pass(self):
        app = {'status': {'sync': {'status': 'Synced', 'revision': 'expected'}, 'health': {'status': 'Healthy'}}}
        self.assertTrue(argocd.synced(app, 'expected'))
        self.assertFalse(argocd.synced(app, 'old'))
        app['status']['health']['status'] = 'Progressing'
        self.assertFalse(argocd.synced(app, 'expected'))
        self.assertFalse(argocd.synced({}, 'expected'))

    def test_reversed_or_missing_dependencies_fail(self):
        app = lambda timestamp: {'metadata': {'creationTimestamp': timestamp}}
        with self.assertRaisesRegex(RuntimeError, 'before its dependency'):
            argocd.verify_order({'gitops-config': app('2026-01-01T00:00:00Z')})
        with self.assertRaisesRegex(RuntimeError, 'creation order'):
            argocd.verify_order({'gitops-openbao': app('2026-01-01T00:00:02Z'), 'gitops-operator': app('2026-01-01T00:00:01Z')})

    def test_bootstrap_error_names_stage_without_response_or_secret(self):
        h = types.SimpleNamespace(context='kind-disposable')
        result = types.SimpleNamespace(returncode=1, stderr='Error from server (Conflict): private-value', stdout='private-value')
        with patch.object(argocd.subprocess, 'run', return_value=result):
            with self.assertRaisesRegex(RuntimeError, 'bootstrap install.*Conflict') as error:
                argocd.bootstrap_command(h, 'install', 'apply', '-f', '-', data='private-input')
        self.assertNotIn('private', str(error.exception))
        result.stderr = 'unrecognised private-value'
        with patch.object(argocd.subprocess, 'run', return_value=result):
            with self.assertRaisesRegex(RuntimeError, 'unclassified') as error:
                argocd.bootstrap_command(h, 'establish CRD', 'wait')
        self.assertNotIn('private', str(error.exception))
        with patch.object(argocd.subprocess, 'run', side_effect=subprocess.TimeoutExpired(['private'], 360)):
            with self.assertRaisesRegex(RuntimeError, 'command timeout') as error:
                argocd.bootstrap_command(h, 'rollout', 'rollout', 'status')
        self.assertNotIn('private', str(error.exception))

    def test_install_checksum_and_image_inventory_fail_closed(self):
        with self.assertRaisesRegex(RuntimeError, 'checksum'):
            argocd.pinned_manifest(b'changed release')
        raw = b'  image: unexpected/image:latest\n'
        with patch.object(argocd, 'MANIFEST_SHA256', hashlib.sha256(raw).hexdigest()):
            with self.assertRaisesRegex(RuntimeError, 'inventory'):
                argocd.pinned_manifest(raw)
        raw = ''.join('  image: ' + image + '\n' for image in argocd.IMAGES).encode()
        with patch.object(argocd, 'MANIFEST_SHA256', hashlib.sha256(raw).hexdigest()):
            rendered = argocd.pinned_manifest(raw)
        self.assertEqual(rendered.count('@sha256:'), len(argocd.IMAGES))

    def test_child_application_health_requires_both_sync_and_health(self):
        cases = [('{}', 'Progressing'),
                 ('{status={sync={status="Synced"},health={}}}', 'Progressing'),
                 ('{status={sync={status="OutOfSync"},health={status="Healthy"}}}', 'Progressing'),
                 ('{status={sync={status="Synced"},health={status="Progressing"}}}', 'Progressing'),
                 ('{status={sync={status="Synced"},health={status="Healthy"}}}', 'Healthy'),
                 ('{status={operationState={phase="Failed"}}}', 'Degraded')]
        for value, expected in cases:
            with self.subTest(value=value):
                script = 'obj=' + value + '\nlocal function health()\n' + argocd.APPLICATION_HEALTH + '\nend\nprint(health().status)'
                result = subprocess.run(['lua', '-'], input=script, text=True, capture_output=True, check=True)
                self.assertEqual(result.stdout.strip(), expected)

    def test_operator_health_reports_current_failure_before_stale_ready(self):
        text = (argocd.common.ROOT / 'docs/guides/argocd-cm-health.yaml').read_text()
        source = argocd.textwrap.dedent(text.split('      health.lua: |\n', 1)[1])
        cases = [
            ('{metadata={generation=2},status={observedGeneration=1,conditions={{type="Ready",status="True",observedGeneration=1},{type="Synced",status="False",reason="ReconcileError",observedGeneration=2}}}}', 'Degraded'),
            ('{metadata={generation=3},status={observedGeneration=1,conditions={{type="Ready",status="True",observedGeneration=1},{type="Synced",status="False",reason="ReconcileError",observedGeneration=2}}}}', 'Progressing'),
            ('{metadata={generation=2},status={observedGeneration=1,conditions={{type="Ready",status="True",observedGeneration=1},{type="Synced",status="False",reason="ConnectionNotReady",observedGeneration=2}}}}', 'Progressing'),
            ('{metadata={generation=2},status={observedGeneration=2,conditions={{type="Ready",status="True",observedGeneration=2},{type="Synced",status="True",observedGeneration=2}}}}', 'Healthy'),
        ]
        cases += [
            ('{metadata={generation=1},status={conditions={{type="Ready",status="True"}}}}', 'Progressing'),
            ('{metadata={generation=2},status={observedGeneration=2,conditions={{type="Ready",status="True",observedGeneration=2},{type="Synced",status="False",reason="ConnectionNotReady",observedGeneration=2}}}}', 'Progressing'),
            ('{metadata={generation=2},status={observedGeneration=2,conditions={{type="Ready",status="True",observedGeneration=2}}}}', 'Healthy'),
            ('{metadata={generation=2},status={observedGeneration=2,conditions={{type="Ready",status="False",reason="Paused",observedGeneration=2}}}}', 'Suspended'),
        ]
        for value, expected in cases:
            with self.subTest(expected=expected):
                result = subprocess.run(['lua', '-'], input='obj=' + value + '\nlocal function health()\n' + source + '\nend\nprint(health().status)', text=True, capture_output=True, check=True)
                self.assertEqual(result.stdout.strip(), expected)


if __name__ == '__main__':
    unittest.main()
