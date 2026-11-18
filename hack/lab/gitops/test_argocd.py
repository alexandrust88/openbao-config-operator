"""Argo CD hierarchy, permission boundary and convergence regression contracts."""
import json
import re
import subprocess
import types
import textwrap
import unittest
from unittest.mock import patch

import argocd


class ArgoTests(unittest.TestCase):
    def test_checked_in_applications_reference_public_charts_and_git(self):
        rendered = subprocess.run(['kubectl', 'kustomize', str(argocd.EXAMPLES / 'applications')],
                                  text=True, capture_output=True, check=True).stdout
        for value in ('https://openbao.github.io/openbao-helm',
                      'https://docs.obco.dev/charts',
                      'chart: openbao-config-operator', 'targetRevision: 1.0.0',
                      'path: gitops/config', 'path: gitops/openbao/bootstrap'):
            self.assertIn(value, rendered)
        self.assertNotIn('obco-gitops.svc', rendered)
        self.assertNotIn('example.com', rendered)

    def test_checked_in_controller_image_digests_are_valid(self):
        values = (argocd.EXAMPLES / 'values/argocd.yaml').read_text()
        digests = re.findall(r'sha256:([a-f0-9]+)', values)
        self.assertEqual(len(digests), 2)
        self.assertTrue(all(len(value) == 64 for value in digests))
        self.assertIn('server:\n  enabled: true', values)

    def test_config_project_cannot_manage_workloads_or_secrets(self):
        project = argocd.read_object(argocd.EXAMPLES / 'projects/gitops-config.yaml')['spec']
        allowed = {item['kind'] for item in project['namespaceResourceWhitelist']}
        self.assertFalse({'Secret', 'Deployment', 'RoleBinding', 'Application', '*'} & allowed)
        self.assertEqual(project['destinations'], [{'server': 'https://kubernetes.default.svc', 'namespace': 'obco-gitops'}])
        self.assertEqual(project['clusterResourceWhitelist'], [{'group': 'core.obco.dev', 'kind': 'BaoServer'}])

    def test_exact_revision_override_preserves_chart_versions(self):
        base = {'spec': {'source': {'path': 'gitops/argocd/applications'}}}
        with patch.object(argocd, 'read_object', return_value=base):
            root = argocd.root_application('https://github.com/owner/repo.git', 'a' * 40)
        source = root['spec']['source']
        self.assertEqual(source['targetRevision'], 'a' * 40)
        patches = source['kustomize']['patches']
        self.assertEqual(len(patches), 3)
        for entry in patches:
            for operation in json.loads(entry['patch']):
                self.assertNotIn('/sources/0/', operation['path'])
                self.assertIn(operation['value'], ('a' * 40, 'https://github.com/owner/repo.git'))

    def test_multisource_requires_every_git_revision_and_chart_version(self):
        app = {'spec': {'sources': [{'chart': 'openbao', 'targetRevision': '0.30.7'},
                                   {'ref': 'values'}, {'path': 'bootstrap'}]},
               'status': {'sync': {'status': 'Synced', 'revisions': ['0.30.7', 'sha', 'sha']},
                          'health': {'status': 'Healthy'}}}
        self.assertTrue(argocd.synced(app, 'sha'))
        app['status']['sync']['revisions'][2] = 'old'
        self.assertFalse(argocd.synced(app, 'sha'))
        app['status']['sync']['revisions'] = ['0.30.7', 'sha']
        self.assertFalse(argocd.synced(app, 'sha'))

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

    def test_child_application_health_requires_both_sync_and_health(self):
        values = argocd.common.load_yaml(argocd.EXAMPLES / 'values/argocd.yaml')
        source = values['configs']['cm']['resource.customizations.health.argoproj.io_Application']
        cases = [('{}', 'Progressing'),
                 ('{status={sync={status="Synced"},health={}}}', 'Progressing'),
                 ('{status={sync={status="OutOfSync"},health={status="Healthy"}}}', 'Progressing'),
                 ('{status={sync={status="Synced"},health={status="Progressing"}}}', 'Progressing'),
                 ('{status={sync={status="Synced"},health={status="Healthy"}}}', 'Healthy'),
                 ('{status={operationState={phase="Failed"}}}', 'Degraded')]
        for value, expected in cases:
            with self.subTest(value=value):
                script = 'obj=' + value + '\nlocal function health()\n' + source + '\nend\nprint(health().status)'
                result = subprocess.run(['lua', '-'], input=script, text=True, capture_output=True, check=True)
                self.assertEqual(result.stdout.strip(), expected)

    def test_operator_health_reports_current_failure_before_stale_ready(self):
        text = (argocd.common.ROOT / 'docs/guides/argocd-cm-health.yaml').read_text()
        canonical = textwrap.dedent(text.split('      health.lua: |\n', 1)[1])
        values = argocd.common.load_yaml(argocd.EXAMPLES / 'values/argocd.yaml')
        source = textwrap.dedent(values['configs']['cm']['resource.customizations'].split('health.lua: |\n', 1)[1])
        self.assertEqual(source.strip(), canonical.strip())
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
