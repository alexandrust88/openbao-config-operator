"""Contract checks for the disposable Authentik provider and service identities."""
import io
import json
import os
import unittest
import urllib.error
from unittest.mock import Mock, patch

import authentik


class FakeAPI:
    def __init__(self):
        self.calls = []
        self.counter = 0

    def one(self, path, field, value):
        return {'pk': value}

    def request(self, method, path, body=None):
        self.calls.append((method, path, body))
        self.counter += 1
        return {'pk': str(self.counter)}


class AuthentikTests(unittest.TestCase):
    def test_human_matrix_and_machine_identities_are_distinct(self):
        state = authentik.state_from_credentials(authentik.new_credentials())
        self.assertEqual(len(state['users']), 9)
        self.assertEqual(len(state['machine_users']), 9)
        self.assertEqual({(u['namespace'], u['role']) for u in state['users']},
                         {(u['namespace'], u['role']) for u in state['machine_users']})
        self.assertFalse({u['username'] for u in state['users']} & {u['username'] for u in state['machine_users']})
        self.assertEqual(state['oidc_scopes'], ['profile', 'email', 'groups'])
        self.assertEqual(state['issuer'], authentik.ISSUER)

    def test_provider_is_confidential_signed_and_groups_in_id_tokens(self):
        api = FakeAPI()
        authentik.configure(api, authentik.new_credentials())
        provider = next(body for _, path, body in api.calls if path == '/api/v3/providers/oauth2/')
        self.assertEqual(provider['client_type'], 'confidential')
        self.assertEqual(provider['grant_types'], ['authorization_code', 'client_credentials'])
        self.assertTrue(provider['signing_key'])
        self.assertTrue(provider['include_claims_in_id_token'])
        self.assertEqual(provider['redirect_uris'], [{'matching_mode': 'strict', 'url': authentik.lab.CALLBACK}])
        users = [body for _, path, body in api.calls if path == '/api/v3/core/users/']
        machines = [user for user in users if user.get('type') == 'service_account']
        self.assertEqual(len(users), 18)
        self.assertEqual(len(machines), 9)
        self.assertTrue(all(len(user['groups']) == 1 for user in users))
        groups = [body for _, path, body in api.calls if path == '/api/v3/core/groups/']
        self.assertTrue(all(not group['is_superuser'] for group in groups))
        tokens = [body for _, path, body in api.calls if path == '/api/v3/core/tokens/']
        self.assertEqual({token['user'] for token in tokens},
                         {str(index + 1) for index, (_, path, body) in enumerate(api.calls)
                          if path == '/api/v3/core/users/' and body.get('type') == 'service_account'})
        self.assertTrue(all(token['intent'] == 'app_password' for token in tokens))

    def test_install_uses_pinned_helm_or_explicit_override(self):
        for configured, expected in (('', str(authentik.identity.ROOT / 'bin/helm')), ('/fixture/helm', '/fixture/helm')):
            with patch.dict(os.environ, {'HELM': configured}), patch.object(authentik.identity, 'kube', return_value=''), \
                 patch.object(authentik.identity, 'apply'), patch.object(authentik.subprocess, 'run', return_value=Mock(returncode=1)) as run:
                with self.assertRaisesRegex(RuntimeError, 'Authentik Helm installation failed'):
                    authentik.up()
                command = run.call_args.args[0]
                self.assertEqual(command[0], expected)
                self.assertEqual(command[command.index('--values') + 1], str(authentik.VALUES_FILE))
                self.assertNotIn('input', run.call_args.kwargs)
                self.assertIn('--kube-context', command)
                self.assertIn('--version', command)
                self.assertNotIn('bootstrap-token', ' '.join(command))

    def test_api_error_is_sanitized_and_never_retries_mutations(self):
        api = authentik.API(1234, 'private-token')
        api.opener = Mock()
        response = io.BytesIO(b'private-user-password')
        api.opener.open.side_effect = urllib.error.HTTPError('http://localhost', 400, 'private', {}, response)
        with self.assertRaisesRegex(RuntimeError, '^Authentik API POST failed \\(HTTP 400\\)$'):
            api.request('POST', '/api/v3/core/users/', {'password': 'private'})
        self.assertTrue(response.closed)
        self.assertEqual(api.opener.open.call_count, 1)

    def test_machine_subject_must_differ_from_human(self):
        def token(subject):
            return 'header.' + authentik.base64.urlsafe_b64encode(json.dumps({'sub': subject}).encode()).decode().rstrip('=') + '.signature'
        authentik.distinct_subjects(token('human'), token('machine'))
        with self.assertRaisesRegex(RuntimeError, 'reused a human'):
            authentik.distinct_subjects(token('human'), token('human'))
        with self.assertRaisesRegex(RuntimeError, 'missing or malformed'):
            authentik.distinct_subjects('invalid', token('machine'))

    def test_blueprints_must_settle_before_provisioning(self):
        api = Mock()
        api.one.side_effect = [RuntimeError('not ready')] + [{'pk': 'ready'}] * 6
        with patch.object(authentik.time, 'sleep') as sleep:
            authentik.wait_defaults(api)
        sleep.assert_called_once_with(3)
        api.request.assert_not_called()

    def test_blueprint_wait_has_a_deadline(self):
        api = Mock()
        api.one.side_effect = RuntimeError('not ready')
        with patch.object(authentik.time, 'monotonic', side_effect=[0, 181]):
            with self.assertRaisesRegex(RuntimeError, 'blueprints did not become ready'):
                authentik.wait_defaults(api)
        api.request.assert_not_called()

    def test_existing_namespace_is_never_reprovisioned(self):
        with patch.object(authentik.identity, 'kube', return_value='namespace/obco-lab-authentik'), patch.object(authentik.identity, 'apply') as apply:
            with self.assertRaisesRegex(RuntimeError, 'already exists'):
                authentik.up()
            apply.assert_not_called()


if __name__ == '__main__':
    unittest.main()
