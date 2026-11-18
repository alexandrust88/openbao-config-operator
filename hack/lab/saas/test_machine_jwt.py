"""Machine JWT mounts preserve human resources and enforce configured audiences."""
import unittest
from unittest.mock import Mock, patch

import jwt_lab as jwt
from test_operator import fixture


def state():
    return {**fixture(), 'jwks_url': 'https://tenant.example.test/keys'}


class MachineJWTTests(unittest.TestCase):
    def test_machine_resources_do_not_overlap_human_and_keep_identity_policies(self):
        human = state()
        machine = {**human, 'jwt_mount_suffix': '-machine', 'oidc_client_id': 'project-audience'}
        human_names = {(obj['kind'], obj['metadata']['name']) for obj in jwt.render(human)}
        objects = jwt.render(machine)
        self.assertFalse(human_names & {(obj['kind'], obj['metadata']['name']) for obj in objects})
        for obj in objects:
            if obj['kind'] == 'JWTAuthRole' and obj['spec']['name'] != 'wrong-audience':
                self.assertEqual(obj['spec']['boundAudiences'], ['project-audience'])
                self.assertNotIn('tokenPolicies', obj['spec'])
            if obj['kind'] == 'IdentityGroup':
                self.assertTrue(obj['spec']['policies'][0].startswith('saas-auth0-'))
        with self.assertRaises(ValueError):
            jwt.render({**human, 'jwt_mount_suffix': '/outside'})

    def test_machine_matrix_requires_complete_tokens_and_explicit_audience(self):
        tokens = {(t, r): 'fixture-jwt' for t in jwt.lab.TENANTS for r in jwt.lab.ROLES}
        for values, audience in [({}, 'project'), (tokens, ''), (tokens, None)]:
            with self.assertRaises(ValueError):
                jwt.run_machine(Mock(), state(), Mock(), values, audience)
        with self.assertRaisesRegex(ValueError, 'human tokens'):
            jwt.run_machine(Mock(), state(), Mock(), tokens, 'different-audience')

    def test_distinct_audience_checks_both_directions_all_namespaces_modes(self):
        tokens = {(t, r): 'machine-' + t + r for t in jwt.lab.TENANTS for r in jwt.lab.ROLES}
        human_tokens = {pair: 'human-' + value for pair, value in tokens.items()}
        kube = Mock()
        with patch('jwt_login.discover', return_value={'jwks_uri': state()['jwks_url']}), patch.object(jwt, 'check') as check, patch.object(jwt, 'rejected') as rejected, patch('builtins.print'):
            jwt.run_machine(Mock(), state(), kube, tokens, 'project-audience', human_tokens=human_tokens)
        self.assertEqual(check.call_args.args[1]['jwt_mount_suffix'], '-machine')
        self.assertEqual(rejected.call_count, 12)
        for call in rejected.call_args_list:
            self.assertEqual(call.args[-1], 'audience')
        machine_calls = [call for call in rejected.call_args_list if call.args[3].endswith('-machine')]
        self.assertEqual(len(machine_calls), 6)
        self.assertTrue(all(call.args[-2].startswith('human-') for call in machine_calls))
        kube.apply.assert_called_once()
        kube.wait.assert_called_once()
