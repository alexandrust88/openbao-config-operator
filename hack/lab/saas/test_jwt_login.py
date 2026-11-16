"""OAuth correlation and credential transport contracts without live providers."""
import base64
import json
import io
import time
import unittest
from types import SimpleNamespace
from unittest.mock import Mock, patch
from urllib.parse import parse_qs, urlsplit

import jwt_login as login


def token(nonce='nonce', **changes):
    claims = {'iss': 'https://example.auth0.com/', 'aud': 'client', 'sub': 'user',
              'nonce': nonce, 'exp': time.time() + 60}
    claims.update(changes)
    encode = lambda value: base64.urlsafe_b64encode(json.dumps(value).encode()).rstrip(b'=').decode()
    return encode({'alg': 'RS256'}) + '.' + encode(claims) + '.signature'


class LoginTests(unittest.TestCase):
    def setUp(self):
        self.state = {'provider': 'auth0', 'issuer': 'https://example.auth0.com/',
                      'oidc_client_id': 'client', 'oidc_client_secret': 'fake-client-secret',
                      'connection_name': 'owned-users'}
        self.metadata = {key: 'https://example.auth0.com/' + path for key, path in
                         [('authorization_endpoint', 'authorize'), ('token_endpoint', 'oauth/token'),
                          ('jwks_uri', '.well-known/jwks.json')]}

    def test_loopback_transport_ignores_environment_proxies_only_locally(self):
        for local_only in (True, False):
            opener = Mock()
            opener.open.return_value.__enter__ = Mock(return_value=io.BytesIO(b'{}'))
            opener.open.return_value.__exit__ = Mock(return_value=False)
            with patch.object(login.urllib.request, 'build_opener', return_value=opener) as build:
                login.request_json('http://127.0.0.1:12345/token' if local_only else 'https://issuer.test/token', local_only=local_only)
            proxies = [handler for handler in build.call_args.args if isinstance(handler, login.urllib.request.ProxyHandler)]
            self.assertEqual(len(proxies), 1 if local_only else 0)
            if local_only:
                self.assertEqual(proxies[0].proxies, {})

    def test_local_provider_requests_use_only_loopback_and_preserve_host(self):
        from operator_lab import LOCAL_ISSUERS
        for provider, issuer in LOCAL_ISSUERS.items():
            state = {**self.state, "provider": provider, "issuer": issuer, "browser_route_port": 12345}
            endpoint = issuer.rstrip('/') + '/token'
            with patch.object(login, 'request_json', return_value={}) as request:
                login.provider_request(state, endpoint, b'fixture-body', {'Authorization': 'Basic fixture'})
                url, body, headers = request.call_args.args
                self.assertTrue(request.call_args.kwargs['local_only'])
                self.assertEqual(urlsplit(url).netloc, '127.0.0.1:12345')
                self.assertEqual(headers['Host'], urlsplit(issuer).netloc)
                self.assertEqual(headers['Authorization'], 'Basic fixture')
                self.assertEqual(body, b'fixture-body')
                for bad in ('http://other.test/token', 'https://other.test/token'):
                    with self.assertRaises(login.LoginError):
                        login.provider_request(state, bad)
            for bad_port in (0, 65536, True, '12345'):
                with self.assertRaises(login.LoginError):
                    login.provider_request({**state, 'browser_route_port': bad_port}, endpoint)
            with self.assertRaises(login.LoginError):
                login.trusted_endpoint(issuer, endpoint)  # HTTP is unavailable without exact local state.

    def test_local_code_exchange_passes_relay_and_basic_auth(self):
        from operator_lab import LOCAL_ISSUERS
        for provider in ('authentik', 'zitadel'):
            issuer = LOCAL_ISSUERS[provider]
            state = {**self.state, 'provider': provider, 'issuer': issuer, 'browser_route_host': '127.0.0.1',
                     'browser_route_port': 12345, 'token_endpoint_auth_method': 'client_secret_basic'}
            metadata = {key: issuer.rstrip('/') + '/' + name for key, name in
                        [('authorization_endpoint', 'authorize'), ('token_endpoint', 'token'), ('jwks_uri', 'keys')]}
            captured = {}
            def browser(*args, **kwargs):
                config = json.loads(kwargs['input'])
                self.assertEqual(config['route_port'], 12345)
                captured.update(parse_qs(urlsplit(config['url']).query))
                return SimpleNamespace(returncode=0, stderr='', stdout=json.dumps({'state': config['state'], 'code': 'fixture'}))
            def request(url, body, headers, *, local_only=False):
                self.assertTrue(local_only)
                self.assertEqual(urlsplit(url).netloc, '127.0.0.1:12345')
                self.assertTrue(headers['Authorization'].startswith('Basic '))
                self.assertNotIn('client_id', parse_qs(body.decode()))
                return {'id_token': token(nonce=captured['nonce'][0], iss=issuer)}
            with patch.object(login.subprocess, 'run', side_effect=browser), patch.object(login, 'request_json', side_effect=request):
                login.acquire_id_token(state, {'username': 'fixture', 'password': 'fixture'}, ['node'], metadata)

    def test_reject_endpoint_exfiltration(self):
        for url in ('http://example.auth0.com/token', 'https://attacker.test/token',
                    'https://user@example.auth0.com/token', 'https://example.auth0.com/token?redirect=x'):
            with self.subTest(url=url), self.assertRaises(login.LoginError):
                login.trusted_endpoint(self.state['issuer'], url)

    def test_nonce_audience_expiration_issuer(self):
        login.validate_id_token(token(), self.state, 'nonce')
        for changes in ({'nonce': 'other'}, {'aud': 'other'}, {'exp': 0}, {'iss': 'https://other.test'}, {'sub': ''}):
            with self.subTest(changes=changes), self.assertRaises(login.LoginError):
                login.validate_id_token(token(**changes), self.state, 'nonce')
        for malformed in ('', 'secret-response', 'a.b.c'):
            with self.assertRaises(login.LoginError):
                login.validate_id_token(malformed, self.state, 'nonce')

    def test_provider_authentication_pkce_and_connection(self):
        for provider in ('auth0', 'okta'):
            with self.subTest(provider=provider):
                self.state['provider'] = provider
                captured = {}
                def browser(*args, **kwargs):
                    config = json.loads(kwargs['input'])
                    captured.update(parse_qs(urlsplit(config['url']).query))
                    return SimpleNamespace(returncode=0, stderr='', stdout=json.dumps({'code': 'fake-code', 'state': config['state']}))
                def endpoint(url, body, headers):
                    payload = parse_qs(body.decode())
                    self.assertIn('code_verifier', payload)
                    self.assertEqual(captured['code_challenge_method'], ['S256'])
                    if provider == 'auth0':
                        self.assertEqual(captured['connection'], ['owned-users'])
                        self.assertEqual(payload['client_id'], ['client'])
                        self.assertIn('client_secret', payload)
                        self.assertNotIn('Authorization', headers)
                    else:
                        self.assertNotIn('client_id', payload)
                        self.assertNotIn('client_secret', payload)
                        self.assertTrue(headers['Authorization'].startswith('Basic '))
                    return {'id_token': token(nonce=captured['nonce'][0])}
                with patch.object(login.subprocess, 'run', side_effect=browser), patch.object(login, 'request_json', side_effect=endpoint):
                    login.acquire_id_token(self.state, {'username': 'fake-user', 'password': 'fake-password'}, ['node'], self.metadata)

    def test_wrong_callback_state_never_exchanges_code(self):
        result = SimpleNamespace(returncode=0, stderr='', stdout=json.dumps({'state': 'wrong', 'code': 'fake'}))
        with patch.object(login.subprocess, 'run', return_value=result), patch.object(login, 'request_json') as request:
            with self.assertRaises(login.LoginError):
                login.acquire_id_token(self.state, {'username': 'fake-user', 'password': 'fake-password'}, ['node'], self.metadata)
            request.assert_not_called()

    def test_redirect_does_not_forward_credentials(self):
        with self.assertRaises(login.LoginError):
            login.NoRedirect().redirect_request(None, None, 302, 'redirect', {}, 'https://other.test')

    def test_browser_errors_do_not_expose_stderr(self):
        result = SimpleNamespace(returncode=1, stderr='sensitive-provider-response', stdout='')
        with patch.object(login.subprocess, 'run', return_value=result):
            with self.assertRaises(login.LoginError) as caught:
                login.acquire_id_token(self.state, {'username': 'fake-user', 'password': 'fake-password'}, ['node'], self.metadata)
            self.assertNotIn('sensitive-provider-response', str(caught.exception))

    def test_discovery_mismatch_fails(self):
        with patch.object(login, 'request_json', return_value={**self.metadata, 'issuer': 'https://other.test'}):
            with self.assertRaises(login.LoginError):
                login.discover(self.state)


if __name__ == '__main__':
    unittest.main()
