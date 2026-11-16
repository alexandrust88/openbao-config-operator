"""Acquire human ID tokens through hosted login; OpenBao verifies their signatures."""
import base64
import hashlib
import hmac
import json
import secrets
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request

CALLBACK = 'http://127.0.0.1:8250/oidc/callback'


class LoginError(RuntimeError):
    """Safe diagnostic without credentials, callback codes or provider responses."""


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise LoginError('OAuth endpoint redirected; credentials were not forwarded')


def request_json(url, body=None, headers=None, *, local_only=False):
    request = urllib.request.Request(url, body, headers or {})
    try:
        with urllib.request.build_opener(*([urllib.request.ProxyHandler({})] if local_only else []), NoRedirect()).open(request, timeout=30) as response:
            data = response.read(1048577)
            if len(data) > 1048576:
                raise LoginError('OAuth response exceeded size limit')
            value = json.loads(data)
            if not isinstance(value, dict):
                raise LoginError('OAuth response was not an object')
            return value
    except urllib.error.HTTPError as error:
        error.close()
        raise LoginError(f'OAuth endpoint failed (HTTP {error.code})') from None
    except (urllib.error.URLError, TimeoutError, ValueError):
        raise LoginError('OAuth request failed') from None


def trusted_endpoint(issuer, endpoint, state=None):
    base, parsed = urllib.parse.urlsplit(issuer), urllib.parse.urlsplit(endpoint)
    from operator_lab import local_provider
    local = state is not None and local_provider(state) and issuer.rstrip('/') == state['issuer'].rstrip('/')
    if ((base.scheme != 'https' and not local) or not base.hostname or base.username or base.password or
            parsed.scheme != base.scheme or parsed.netloc != base.netloc or parsed.username or
            parsed.password or parsed.fragment or parsed.query):
        raise LoginError('Untrusted OAuth endpoint')
    return endpoint


def provider_request(state, endpoint, body=None, headers=None):
    """Use a loopback port-forward only for a registered disposable provider."""
    from operator_lab import local_provider
    trusted_endpoint(state['issuer'], endpoint, state)
    if not local_provider(state):
        return request_json(endpoint, body, headers)
    port = state.get('browser_route_port')
    if type(port) is not int or not 1 <= port <= 65535:
        raise LoginError('Invalid local provider port-forward')
    parsed = urllib.parse.urlsplit(endpoint)
    headers = {**(headers or {}), 'Host': parsed.netloc}
    endpoint = urllib.parse.urlunsplit((parsed.scheme, f'127.0.0.1:{port}', parsed.path, parsed.query, ''))
    return request_json(endpoint, body, headers, local_only=True)


def discover(state):
    issuer = state['issuer'].rstrip('/')
    trusted_endpoint(issuer, issuer, state)
    metadata = provider_request(state, issuer + '/.well-known/openid-configuration')
    if metadata.get('issuer', '').rstrip('/') != issuer:
        raise LoginError('OAuth discovery issuer mismatch')
    for field in ('authorization_endpoint', 'token_endpoint', 'jwks_uri'):
        trusted_endpoint(issuer, metadata.get(field, ''), state)
    return metadata


def validate_id_token(token, state, nonce):
    # These correlation checks are not signature verification. The JWT login
    # under test must verify the signature against discovery/JWKS independently.
    try:
        header, payload, signature = token.split('.')
        decode = lambda value: json.loads(base64.urlsafe_b64decode(value + '=' * (-len(value) % 4)))
        header, claims = decode(header), decode(payload)
        audience = claims.get('aud')
        audiences = audience if isinstance(audience, list) else [audience]
        if (header.get('alg') != 'RS256' or not signature or
                claims.get('iss', '').rstrip('/') != state['issuer'].rstrip('/') or
                state['oidc_client_id'] not in audiences or
                not isinstance(claims.get('nonce'), str) or
                not hmac.compare_digest(claims['nonce'], nonce) or
                not isinstance(claims.get('exp'), (int, float)) or claims['exp'] <= time.time() or
                not isinstance(claims.get('sub'), str) or not claims['sub']):
            raise ValueError()
    except (ValueError, TypeError, AttributeError, UnicodeError):
        raise LoginError('ID token correlation checks failed') from None
    return token


def acquire_id_token(state, user, command=None, metadata=None):
    from operator_lab import authorization_url, browser_command, browser_failure, local_provider
    metadata = metadata or discover(state)
    for field in ('authorization_endpoint', 'token_endpoint', 'jwks_uri'):
        trusted_endpoint(state['issuer'], metadata.get(field, ''), state)
    nonce, correlation, verifier = (secrets.token_urlsafe(32) for _ in range(3))
    challenge = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).rstrip(b'=').decode()
    query = {'response_type': 'code', 'client_id': state['oidc_client_id'], 'redirect_uri': CALLBACK,
             'scope': ' '.join(dict.fromkeys(['openid', *state.get('oidc_scopes', ['profile', 'email'])])), 'state': correlation, 'nonce': nonce,
             'code_challenge': challenge, 'code_challenge_method': 'S256'}
    url = authorization_url(state, metadata['authorization_endpoint'] + '?' + urllib.parse.urlencode(query))
    try:
        config = {'provider': state['provider'], 'issuer': state['issuer'], 'url': url,
                  'state': correlation, 'username': user['username'], 'password': user['password']}
        if local_provider(state):
            config.update(route_host=state['browser_route_host'], route_port=state['browser_route_port'])
        result = subprocess.run(command or browser_command(local_provider(state)), input=json.dumps(config),
            text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=210)
    except (subprocess.TimeoutExpired, OSError):
        raise LoginError('Hosted JWT login browser failed') from None
    if result.returncode:
        raise LoginError(browser_failure(result.stderr))
    try:
        callback = json.loads(result.stdout)
        if (not isinstance(callback, dict) or callback.get('state') != correlation or
                not isinstance(callback.get('code'), str) or not callback['code']):
            raise ValueError()
    except (ValueError, TypeError):
        raise LoginError('OAuth callback correlation failed') from None
    body = {'grant_type': 'authorization_code', 'code': callback['code'], 'redirect_uri': CALLBACK,
            'code_verifier': verifier}
    headers = {'Content-Type': 'application/x-www-form-urlencoded'}
    method = state.get('token_endpoint_auth_method', 'client_secret_basic') if local_provider(state) else {'okta': 'client_secret_basic', 'auth0': 'client_secret_post'}.get(state['provider'])
    if method == 'client_secret_basic':
        quote = urllib.parse.quote_plus
        credential = quote(state['oidc_client_id']) + ':' + quote(state['oidc_client_secret'])
        headers['Authorization'] = 'Basic ' + base64.b64encode(credential.encode()).decode()
    elif method == 'client_secret_post':
        body['client_id'] = state['oidc_client_id']
        body['client_secret'] = state['oidc_client_secret']
    else:
        raise LoginError('Unsupported JWT provider')
    tokens = provider_request(state, metadata['token_endpoint'], urllib.parse.urlencode(body).encode(), headers)
    return validate_id_token(tokens.get('id_token', ''), state, nonce)
