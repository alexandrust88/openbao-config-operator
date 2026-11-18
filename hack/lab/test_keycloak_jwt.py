import base64
import io
import json
import unittest
import urllib.error
from unittest import mock

import keycloak_jwt
import jwt_lab as jwt


def state():
    return {"provider": "auth0", "status": "complete", "connection_name": "fake-lab", "issuer": "https://issuer.example.invalid",
            "jwks_url": "https://issuer.example.invalid/keys", "groups_claim": "groups", "oidc_client_id": "openbao", "oidc_client_secret": "fake-secret",
            "users": [{"namespace": tenant, "role": role, "group": tenant + "-" + role + "s"}
                      for tenant in jwt.lab.TENANTS for role in jwt.lab.ROLES]}


class JWTTests(unittest.TestCase):
    def test_modes_have_exclusive_config_and_independent_alias_groups(self):
        objects = jwt.render(state())
        self.assertEqual(12, sum(o['kind'] == 'JWTAuthConfig' for o in objects))
        self.assertEqual(30, sum(o['kind'] == 'JWTAuthRole' for o in objects))
        self.assertEqual(18, sum(o['kind'] == 'IdentityGroup' for o in objects))
        names = [(o['kind'], o['metadata']['name']) for o in objects]
        self.assertEqual(len(names), len(set(names)))
        aliases = [o for o in objects if o['kind'] == 'IdentityGroupAlias']
        self.assertEqual(18, len(aliases))
        self.assertEqual(18, len({o['spec']['groupRef']['name'] for o in aliases}))
        for o in objects:
            spec = o['spec']
            if o['kind'] == 'JWTAuthConfig':
                self.assertEqual(1, sum(k in spec for k in ('oidcDiscoveryURL', 'jwksURL')))
                self.assertNotIn('oidcClientID', spec)
                self.assertNotIn('oidcClientSecretSecretRef', spec)
                self.assertEqual(['RS256'], spec['jwtSupportedAlgs'])
            if o['kind'] == 'JWTAuthRole':
                self.assertEqual('jwt', spec['roleType'])
                self.assertTrue(spec['tokenNoDefaultPolicy'])
                self.assertNotIn('tokenPolicies', spec)
                self.assertNotIn('allowedRedirectURIs', spec)
            if o['kind'] == 'IdentityGroupMembership':
                self.assertTrue(spec['groupRef']['name'].endswith('-session'))
                self.assertIn('-jwt-', spec['memberGroupRef']['name'])

    def test_all_providers_keep_claims_issuer_and_client_binding(self):
        for provider in ('auth0', 'okta', 'keycloak'):
            example = state()
            example.update(provider=provider, complete=True,
                           groups_claim='https://claims.example.invalid/groups' if provider == 'auth0' else 'groups')
            if provider == 'keycloak':
                example['issuer'] = jwt.lab.KEYCLOAK_ISSUER
                example['jwks_url'] = example['issuer'] + '/protocol/openid-connect/certs'
            objects = jwt.render(example, 'custom-kube-namespace', 'custom-connection')
            for obj in objects:
                self.assertEqual('custom-kube-namespace', obj['metadata']['namespace'])
                self.assertEqual({'name': 'custom-connection'}, obj['spec']['connectionRef'])
                if obj['kind'] == 'JWTAuthRole':
                    self.assertEqual(example['groups_claim'], obj['spec']['groupsClaim'])
                    self.assertIn(example['groups_claim'], obj['spec']['boundClaims'])
                    if obj['spec']['name'] != 'wrong-audience':
                        self.assertEqual([example['oidc_client_id']], obj['spec']['boundAudiences'])
                if obj['kind'] == 'JWTAuthConfig' and not obj['metadata']['name'].endswith('-bad-issuer'):
                    self.assertEqual(example['issuer'], obj['spec']['boundIssuer'])

    def test_signature_changes_bytes_not_claims(self):
        token = 'header.claims.' + base64.urlsafe_b64encode(b'signature').decode().rstrip('=')
        changed = jwt.corrupt_signature(token)
        self.assertEqual(token.split('.')[:2], changed.split('.')[:2])
        self.assertNotEqual(token, changed)
        with self.assertRaises(RuntimeError):
            jwt.corrupt_signature('not-a-jwt')

    def test_negative_rejects_generic_error_and_accidental_login(self):
        bao = mock.Mock()
        for result in [(400, {'rejection': None}), (500, {'rejection': 'issuer'}),
                       (200, {'auth': {'client_token': 'fake-token'}})]:
            with self.subTest(result=result), mock.patch.object(jwt, 'login', return_value=result):
                with self.assertRaises(RuntimeError):
                    jwt.rejected(bao, state(), 'root', 'mount', 'read', 'fake-jwt', 'issuer')
        bao.request.assert_called_once_with('root', 'POST', 'auth/token/revoke-self', token='fake-token')

    def test_error_classification_omits_provider_body_and_closes_error(self):
        for reason, marker in jwt.ERROR_MARKERS.items():
            body = io.BytesIO(json.dumps({'errors': [marker + ' private detail']}).encode())
            error = urllib.error.HTTPError('https://example.invalid', 400, 'failed', {}, body)
            bao = mock.Mock(address='https://example.invalid')
            bao.opener.open.side_effect = error
            status, result = jwt.login(bao, state(), 'alpha', 'mount', 'read', 'fake-jwt')
            self.assertEqual((400, {'rejection': reason}), (status, result))
            self.assertTrue(body.closed)
            request = bao.opener.open.call_args.args[0]
            self.assertEqual('saas-auth0-alpha', request.get_header('X-vault-namespace'))

    def test_malformed_error_cannot_satisfy_negative_case(self):
        for payload in ({'errors': 'invalid issuer (iss) claim'}, ['invalid issuer (iss) claim']):
            error = urllib.error.HTTPError('https://example.invalid', 400, 'failed', {},
                                           io.BytesIO(json.dumps(payload).encode()))
            bao = mock.Mock(address='https://example.invalid')
            bao.opener.open.side_effect = error
            self.assertEqual((400, {'rejection': None}),
                             jwt.login(bao, state(), 'root', 'mount', 'read', 'fake-jwt'))
        bao.opener.open.side_effect = urllib.error.HTTPError('https://example.invalid', 400, 'failed', {},
                                                            io.BytesIO(b'private invalid response'))
        with self.assertRaisesRegex(RuntimeError, '^JWT rejection response was not valid JSON; response omitted$'):
            jwt.login(bao, state(), 'root', 'mount', 'read', 'fake-jwt')

    def test_hosted_runner_mints_once_per_user_and_reuses_discovery(self):
        import jwt_login
        example = state()
        kube, bao = mock.Mock(), mock.Mock()
        metadata = {'jwks_uri': example['jwks_url']}
        with mock.patch.object(jwt_login, 'discover', return_value=metadata) as discovery, \
             mock.patch.object(jwt_login, 'acquire_id_token', return_value='private-jwt') as acquire, \
             mock.patch.object(jwt, 'check') as check:
            jwt.run(bao, example, kube, 'custom-namespace', 'custom-connection')
        discovery.assert_called_once_with(example)
        self.assertEqual(9, acquire.call_count)
        self.assertTrue(all(call.kwargs['metadata'] is metadata for call in acquire.call_args_list))
        objects = kube.apply.call_args.args[0]
        kube.wait.assert_called_once_with(objects)
        self.assertTrue(all(obj['metadata']['namespace'] == 'custom-namespace' for obj in objects))
        self.assertTrue(all(obj['spec']['connectionRef']['name'] == 'custom-connection' for obj in objects))
        self.assertEqual(9, len(check.call_args.args[2]))
        self.assertNotIn('tokens', example)

    def test_matrix_checks_all_positive_and_classified_negatives(self):
        tokens = {(t, r): f'{t}.{r}.c2lnbmF0dXJl' for t in jwt.lab.TENANTS for r in jwt.lab.ROLES}
        with mock.patch.object(jwt, 'login', return_value=(200, {'auth': {'client_token': 'fake'}})) as login, \
             mock.patch.object(jwt.lab, 'test_access') as access, \
             mock.patch.object(jwt, 'rejected') as rejected, mock.patch('builtins.print'):
            jwt.check(mock.Mock(), state(), tokens)
        self.assertEqual(18, login.call_count)
        self.assertEqual(18, access.call_count)
        self.assertEqual(30, rejected.call_count)
        self.assertEqual({'group', 'audience', 'issuer', 'signature'}, {call.args[-1] for call in rejected.call_args_list})
        self.assertEqual(6, sum(call.args[-1] == 'signature' for call in rejected.call_args_list))
        for call in rejected.call_args_list:
            self.assertNotEqual('', call.args[-2])


if __name__ == '__main__':
    unittest.main()
