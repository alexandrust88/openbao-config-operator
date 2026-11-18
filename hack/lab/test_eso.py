"""Generated credential ownership and projection regression contracts."""
import base64
import copy
import unittest
from unittest.mock import patch

import eso


class GeneratedCredentialTests(unittest.TestCase):
    def test_recipe_has_one_destination_owner_and_tenant_scoped_source(self):
        for tenant in eso.TENANTS:
            policy, generated, projection = eso.generated_resources(tenant)
            self.assertEqual(policy['spec']['openbaoNamespace'], 'lab-' + tenant)
            self.assertEqual(generated['spec']['openbaoNamespace'], 'lab-' + tenant)
            self.assertNotIn('writeSecretRef', generated['spec'])
            self.assertEqual(generated['spec']['passwordPolicyName'], policy['spec']['name'])
            self.assertEqual(projection['metadata']['namespace'], 'obco-lab-' + tenant)
            self.assertEqual(projection['spec']['target']['creationPolicy'], 'Owner')
            self.assertEqual(projection['spec']['data'], [{'secretKey': 'password', 'remoteRef': {
                'key': generated['spec']['path'], 'property': generated['spec']['key']}}])
            self.assertNotIn('dataFrom', projection['spec'])

    def test_projection_rejects_extra_keys_even_when_password_matches(self):
        encoded = base64.b64encode(b'fixture').decode()
        self.assertTrue(eso.projection_matches({'data': {'password': encoded}}, 'fixture'))
        self.assertFalse(eso.projection_matches({'data': {'password': encoded, 'excluded': encoded}}, 'fixture'))
        self.assertFalse(eso.projection_matches({'data': {'password': encoded}}, 'changed'))
        self.assertFalse(eso.projection_matches({}, 'fixture'))

    def test_generation_change_must_preserve_value_and_kv_version(self):
        original = {'data': {'password': 'fixture'}, 'metadata': {'version': 1}}
        stable = {'data': {'password': 'fixture', 'excluded': 'extra'}, 'metadata': {'version': 2}}
        changed = copy.deepcopy(stable)
        changed['metadata']['version'] = 3
        with patch.object(eso, 'read_generated', side_effect=[original, stable, changed]), \
             patch.object(eso, 'wait_generated'), patch.object(eso.lab, 'wait_ready'), \
             patch.object(eso.lab, 'read_secret', return_value={'token': 'fixture'}), \
             patch.object(eso.lab, 'apply'):
            from unittest.mock import Mock
            bao = Mock()
            bao.request.return_value = (200, {})
            with self.assertRaisesRegex(RuntimeError, 'unexpectedly changed'):
                eso.test_generated(bao, 'alpha', 'fixture')

    def run_scenario(self, reads):
        from unittest.mock import Mock
        bao = Mock()
        bao.request.side_effect = [(200, {}), (403, {}), (403, {})]
        with patch.object(eso, 'read_generated', side_effect=reads), \
             patch.object(eso, 'wait_generated') as projected, \
             patch.object(eso, 'wait_absent'), \
             patch.object(eso.lab, 'wait_ready'), \
             patch.object(eso.lab, 'read_secret', return_value={'token': 'fixture'}), \
             patch.object(eso.lab, 'apply') as applied, \
             patch.object(eso.lab, 'kube') as kube, \
             patch.object(eso.secrets, 'token_urlsafe', return_value='extra'), \
             patch.object(eso.secrets, 'token_hex', return_value='next'), \
             patch('builtins.print'):
            eso.test_generated(bao, 'alpha', 'reader')
        return bao, projected, applied, kube

    def test_full_scenario_rechecks_recovery_and_real_denials(self):
        original = {'data': {'password': 'fixture'}, 'metadata': {'version': 1}}
        stable = {'data': {'password': 'fixture', 'excluded': 'extra'}, 'metadata': {'version': 2}}
        rotated = {'data': {'password': 'replacement', 'excluded': 'extra'}, 'metadata': {'version': 3}}
        bao, projected, applied, kube = self.run_scenario([original, stable, stable, rotated, rotated, rotated, rotated])
        self.assertEqual(projected.call_count, 5)
        self.assertEqual(applied.call_count, 4)
        self.assertEqual(kube.call_count, 3)
        kube.assert_any_call('-n', 'obco-lab-alpha', 'delete', 'secret', 'generated-application')
        kube.assert_any_call('-n', 'obco-lab-alpha', 'delete', 'externalsecret', 'generated-application')
        self.assertEqual(bao.request.call_args_list[-2].args[:3], ('beta', 'GET', 'kv/data/app/generated'))
        self.assertEqual(bao.request.call_args_list[-1].args[:3], ('alpha', 'POST', 'kv/data/app/generated'))

    def test_nonce_rotation_cannot_pass_if_old_password_is_returned(self):
        original = {'data': {'password': 'fixture'}, 'metadata': {'version': 1}}
        stable = {'data': {'password': 'fixture', 'excluded': 'extra'}, 'metadata': {'version': 2}}
        with self.assertRaisesRegex(RuntimeError, 'Explicit credential rotation failed'):
            self.run_scenario([original, stable, stable, stable])

    def test_projection_recovery_cannot_regenerate_upstream_value(self):
        original = {'data': {'password': 'fixture'}, 'metadata': {'version': 1}}
        stable = {'data': {'password': 'fixture', 'excluded': 'extra'}, 'metadata': {'version': 2}}
        rotated = {'data': {'password': 'replacement', 'excluded': 'extra'}, 'metadata': {'version': 3}}
        rewritten = copy.deepcopy(rotated)
        rewritten['metadata']['version'] = 4
        with self.assertRaisesRegex(RuntimeError, 'Projection recovery changed'):
            self.run_scenario([original, stable, stable, rotated, rewritten])

    def test_re_adoption_does_not_rewrite_retained_value(self):
        original = {'data': {'password': 'fixture'}, 'metadata': {'version': 1}}
        stable = {'data': {'password': 'fixture', 'excluded': 'extra'}, 'metadata': {'version': 2}}
        rotated = {'data': {'password': 'replacement', 'excluded': 'extra'}, 'metadata': {'version': 3}}
        rewritten = copy.deepcopy(rotated)
        rewritten['metadata']['version'] = 4
        with self.assertRaisesRegex(RuntimeError, 'Re-adoption changed'):
            self.run_scenario([original, stable, stable, rotated, rotated, rotated, rewritten])
