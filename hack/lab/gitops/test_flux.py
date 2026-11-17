"""Validate the checked-in public Flux delivery contract."""
import types
import unittest
from unittest.mock import patch

import flux


class FluxTests(unittest.TestCase):
    def test_public_source_default_and_ci_commit_override(self):
        h = types.SimpleNamespace(public_url='https://github.com/alexandrust88/openbao-config-operator', revision='main')
        source = flux.bootstrap(h)[1]
        self.assertEqual(source['spec']['ref'], {'branch': 'main'})
        h.revision = 'a' * 40
        source = flux.bootstrap(h)[1]
        self.assertEqual(source['spec']['ref'], {'commit': h.revision})
        self.assertNotIn('branch', source['spec']['ref'])
        h.revision = 'feature-not-immutable'
        with self.assertRaises(ValueError):
            flux.bootstrap(h)

    def test_published_chart_sources_have_no_git_chart_or_image_override(self):
        operator = flux.load('operator/release.yaml')['spec']
        source = flux.load('operator/source.yaml')['spec']
        self.assertEqual(operator['chartRef'], {'kind': 'OCIRepository', 'name': 'operator-chart'})
        self.assertEqual(source['url'], 'oci://ghcr.io/alexandrust88/charts/openbao-config-operator')
        self.assertTrue(source['ref']['digest'].startswith('sha256:'))
        self.assertNotIn('image', operator.get('values', {}))
        self.assertEqual(flux.load('openbao/source.yaml')['spec']['url'], 'https://openbao.github.io/openbao-helm')
        self.assertEqual(flux.load('openbao/release.yaml')['spec']['chart']['spec']['version'], '0.30.7')

    def test_dependency_and_health_contract(self):
        roots = flux.load('bootstrap/kustomizations.yaml')['items']
        config = next(item for item in roots if item['metadata']['name'] == 'config')['spec']
        self.assertEqual(config['dependsOn'], [{'name': 'openbao'}, {'name': 'operator'}])
        self.assertEqual(config['path'], './gitops/config')
        self.assertTrue(config['wait'])
        for health in flux.health_expressions():
            self.assertIn('observedGeneration == metadata.generation', health['current'])
            self.assertEqual("c.type == 'Synced'" in health['current'], not health['apiVersion'].startswith('core.'))

    def test_revision_gate_never_accepts_other_sha(self):
        sha = 'a' * 40
        self.assertTrue(flux.matches_revision('main@sha1:' + sha, sha))
        self.assertFalse(flux.matches_revision('main@sha1:' + 'b' * 40, sha))
        self.assertFalse(flux.matches_revision('', 'main'))

    def test_ready_rejects_stale_generation(self):
        resource = {'metadata': {'generation': 2}, 'status': {'conditions': [
            {'type': 'Ready', 'status': 'True', 'observedGeneration': 1}]}}
        self.assertFalse(flux.ready(resource))

    def test_install_consumes_static_instance_and_pins_bootstrap_image(self):
        h = types.SimpleNamespace(context='kind-fixture', public_url='https://github.com/example/repo', revision='main',
                                  apply=lambda obj: None, kubectl=lambda *args: None)
        original = flux.common.run
        with patch.object(flux.common, 'run', wraps=original) as run:
            run.side_effect = lambda args, **kwargs: '' if args[0].endswith('/helm') else original(args, **kwargs)
            flux.install(h)
            self.assertTrue(any('image.tag=v' + flux.OPERATOR_VERSION + '@' + flux.OPERATOR_DIGEST in call.args[0]
                                for call in run.call_args_list))


if __name__ == '__main__':
    unittest.main()
