"""Flux dependency, privilege and revision regression checks."""
import json
from pathlib import Path
import tempfile
import types
import unittest
from unittest.mock import patch

import flux


class FluxTests(unittest.TestCase):
    def setUp(self):
        self.h = types.SimpleNamespace(namespace='obco-gitops', operator_values={'image': {'tag': 'test'}})

    def test_config_waits_for_both_infrastructure_layers_with_current_generation(self):
        roots, release = flux.manifests(self.h)
        config = roots[2]['spec']
        self.assertEqual(config['dependsOn'], [{'name': 'openbao'}, {'name': 'operator'}])
        self.assertTrue(config['wait'])
        self.assertTrue(config['prune'])
        for check in config['healthCheckExprs']:
            self.assertIn('observedGeneration == metadata.generation', check['current'])
            self.assertEqual("c.type == 'Synced'" in check['current'], not check['apiVersion'].startswith('core.'))
            self.assertIn("c.reason == 'ReconcileError'", check['failed'])
        self.assertEqual(release['spec']['chart']['spec']['reconcileStrategy'], 'Revision')
        self.assertEqual(release['spec']['driftDetection']['mode'], 'enabled')
        self.assertEqual(release['spec']['values'], self.h.operator_values)

    def test_install_explicitly_lists_json_resources_for_kustomize(self):
        with tempfile.TemporaryDirectory() as directory:
            self.h.context = 'kind-fixture'
            self.h.repo = types.SimpleNamespace(path=Path(directory), url='http://git.fixture/repo.git', commit=lambda message: None)
            self.h.apply = lambda objects: None
            self.h.kubectl = lambda *args: None
            with patch.object(flux.common, 'run') as run:
                flux.install(self.h)
                self.assertIn('image.tag=v' + flux.OPERATOR_VERSION + '@' + flux.OPERATOR_DIGEST, run.call_args.args[0])
            for name, filename in [('root', 'resources.json'), ('operator', 'release.json')]:
                manifest = json.loads((Path(directory) / 'flux' / name / 'kustomization.yaml').read_text())
                self.assertEqual(manifest['resources'], [filename])

    def test_configuration_identity_has_no_secret_or_cluster_admin_access(self):
        values = flux.permissions(self.h)
        roles = [v for v in values if v['kind'] in ('Role', 'ClusterRole')]
        self.assertEqual({v['kind'] for v in roles}, {'Role', 'ClusterRole'})
        for value in roles:
            for rule in value['rules']:
                self.assertNotIn('secrets', rule['resources'])
        cluster = next(v for v in roles if v['kind'] == 'ClusterRole')
        self.assertEqual(cluster['rules'][0]['resources'], ['openbaoservers'])
        for value in values:
            if value['kind'].endswith('Binding') and value['roleRef']['name'] == 'cluster-admin':
                self.assertEqual(value['subjects'][0]['name'], 'platform-reconciler')

    def test_ready_rejects_stale_generation(self):
        value = {'metadata': {'generation': 2}, 'status': {'conditions': [
            {'type': 'Ready', 'status': 'True', 'observedGeneration': 1}]}}
        self.assertFalse(flux.ready(value))
        value['status']['conditions'][0]['observedGeneration'] = 2
        self.assertTrue(flux.ready(value))

    def test_unhealthy_requires_attempted_revision_and_classified_health_failure(self):
        value = {'metadata': {'generation': 2}, 'status': {'lastAttemptedRevision': 'main@sha1:new',
            'conditions': [{'type': 'Ready', 'status': 'False', 'reason': 'HealthCheckFailed', 'observedGeneration': 2}]}}
        self.h.get = lambda *args: value
        def evaluate(check, label, timeout):
            self.assertTrue(check())
            value['status']['lastAttemptedRevision'] = 'main@sha1:old'
            self.assertFalse(check())
            value['status']['lastAttemptedRevision'] = 'main@sha1:new'
            value['status']['conditions'][0]['reason'] = 'ArtifactFailed'
            self.assertFalse(check())
            value['status']['conditions'][0]['reason'] = 'HealthCheckFailed'
            value['status']['conditions'][0]['observedGeneration'] = 1
            self.assertFalse(check())
        with patch.object(flux.common, 'eventually', evaluate):
            flux.assert_unhealthy(self.h, 'new')

    def test_wait_requires_helm_chart_and_release_consuming_current_git_revision(self):
        healthy = lambda: {'metadata': {'generation': 1}, 'status': {'conditions': [
            {'type': 'Ready', 'status': 'True', 'observedGeneration': 1}]}}
        values = {kind: healthy() for kind in ('gitrepositories', 'kustomizations', 'helmreleases', 'helmcharts')}
        values['gitrepositories']['status']['artifact'] = {'revision': 'main@sha1:new'}
        values['kustomizations']['status']['lastAppliedRevision'] = 'main@sha1:new'
        values['helmcharts']['status'].update(observedSourceArtifactRevision='main@sha1:new', artifact={'revision': 'chart-new'})
        values['helmreleases']['status']['lastAttemptedRevision'] = 'chart-old'
        self.h.get = lambda kind, *args, **kwargs: values[kind.split('.')[0]]
        def evaluate(check, label, timeout):
            self.assertFalse(check())
            values['helmreleases']['status']['lastAttemptedRevision'] = 'chart-new'
            self.assertTrue(check())
            values['helmcharts']['status']['observedSourceArtifactRevision'] = 'main@sha1:old'
            self.assertFalse(check())
        with patch.object(flux.common, 'eventually', evaluate):
            flux.wait(self.h, 'new')

    def test_wait_rejects_previous_revision_even_if_healthy(self):
        healthy = {'metadata': {'generation': 1}, 'status': {'conditions': [
            {'type': 'Ready', 'status': 'True', 'observedGeneration': 1}],
            'artifact': {'revision': 'main@sha1:old'}}}
        self.h.get = lambda *args, **kwargs: healthy
        def evaluate(check, label, timeout):
            self.assertFalse(check())
        with patch.object(flux.common, 'eventually', evaluate):
            flux.wait(self.h, 'new')


if __name__ == '__main__':
    unittest.main()
