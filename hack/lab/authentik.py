#!/usr/bin/env python3
"""Provision disposable Authentik and exercise operator-managed OIDC/JWT access."""
import argparse
import base64
import contextlib
import hashlib
import json
import os
import platform
import secrets
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

import identity
from saas import operator_lab as lab

sys.path.insert(0, str(Path(__file__).resolve().parent / 'saas'))
import jwt_lab
import jwt_login

NAMESPACE = 'obco-lab-authentik'
ORIGIN = 'http://authentik.obco-lab-authentik.svc:9000'
ISSUER = ORIGIN + '/application/o/openbao/'
# renovate: datasource=helm depName=authentik registryUrl=https://charts.goauthentik.io
CHART_VERSION = '2026.8.5'
VALUES_FILE = identity.ROOT / 'hack/lab/authentik/values.yaml'
# renovate: datasource=docker depName=docker.io/library/postgres
POSTGRES = 'docker.io/library/postgres:16.16-alpine@sha256:81e77a4833487b8132fe4b00bedd39dc3f74b8641e387f19312a48f7de218c2e'


def credentials():
    return identity.read_secret('authentik-lab-users', NAMESPACE)


def new_credentials():
    values = {key: secrets.token_urlsafe(32) for key in ('admin-password', 'bootstrap-token', 'secret-key', 'postgres-password', 'client-secret')}
    for tenant in lab.TENANTS:
        for role in lab.ROLES:
            values[f'{tenant}-{role}'] = secrets.token_urlsafe(24)
            values[f'machine-{tenant}-{role}'] = secrets.token_urlsafe(32)
    return values


def state_from_credentials(values):
    return {'provider': 'authentik', 'complete': True, 'issuer': ISSUER, 'groups_claim': 'groups',
            'callback_uri': lab.CALLBACK, 'oidc_client_id': 'openbao', 'oidc_client_secret': values['client-secret'],
            'token_endpoint_auth_method': 'client_secret_basic', 'oidc_scopes': ['profile', 'email', 'groups'],
            'machine_users': [{'namespace': tenant, 'role': role, 'username': f'machine-{tenant}-{role}'}
                              for tenant in lab.TENANTS for role in lab.ROLES],
            'users': [{'namespace': tenant, 'role': role, 'username': f'{tenant}-{role}',
                       'password': values[f'{tenant}-{role}'], 'group': f'{tenant}-{role}s'}
                      for tenant in lab.TENANTS for role in lab.ROLES]}


def password_hash(password):
    salt = secrets.token_hex(12)
    digest = hashlib.pbkdf2_hmac('sha256', password.encode(), salt.encode(), 1000000)
    return 'pbkdf2_sha256$1000000$' + salt + '$' + base64.b64encode(digest).decode()


def deployment_objects(values):
    obj = lambda kind, name, **fields: identity.obj(kind, name, NAMESPACE, **fields)
    env = {'AUTHENTIK_SECRET_KEY': values['secret-key'], 'AUTHENTIK_POSTGRESQL__HOST': 'authentik-postgres',
           'AUTHENTIK_POSTGRESQL__NAME': 'authentik', 'AUTHENTIK_POSTGRESQL__USER': 'authentik',
           'AUTHENTIK_POSTGRESQL__PASSWORD': values['postgres-password'],
           'AUTHENTIK_BOOTSTRAP_PASSWORD_HASH': password_hash(values['admin-password']),
           'AUTHENTIK_BOOTSTRAP_TOKEN': values['bootstrap-token'], 'AUTHENTIK_ERROR_REPORTING__ENABLED': 'false',
           'AUTHENTIK_DISABLE_STARTUP_ANALYTICS': 'true'}
    postgres = {'name': 'postgres', 'image': POSTGRES, 'ports': [{'containerPort': 5432}],
                'env': [{'name': 'POSTGRES_DB', 'value': 'authentik'}, {'name': 'POSTGRES_USER', 'value': 'authentik'},
                        {'name': 'POSTGRES_PASSWORD', 'valueFrom': {'secretKeyRef': {'name': 'authentik-lab-users', 'key': 'postgres-password'}}}],
                'readinessProbe': {'exec': {'command': ['pg_isready', '-U', 'authentik']}, 'periodSeconds': 3},
                'resources': {'requests': {'cpu': '100m', 'memory': '128Mi'}, 'limits': {'memory': '512Mi'}},
                'volumeMounts': [{'name': 'data', 'mountPath': '/var/lib/postgresql/data'}]}
    return [identity.secret('authentik-config', NAMESPACE, env),
            obj('Deployment', 'authentik-postgres', api='apps/v1', spec={'replicas': 1,
                'selector': {'matchLabels': {'app': 'authentik-postgres'}},
                'template': {'metadata': {'labels': {'app': 'authentik-postgres'}}, 'spec': {
                    'automountServiceAccountToken': False, 'containers': [postgres], 'volumes': [{'name': 'data', 'emptyDir': {}}]}}}),
            obj('Service', 'authentik-postgres', spec={'selector': {'app': 'authentik-postgres'}, 'ports': [{'port': 5432}]}),
            obj('Service', 'authentik', spec={'selector': {'app.kubernetes.io/name': 'authentik',
                'app.kubernetes.io/instance': 'authentik', 'app.kubernetes.io/component': 'server'},
                'ports': [{'port': 9000, 'targetPort': 9000}]})]



class API:
    def __init__(self, port, token):
        self.address, self.token = f'http://127.0.0.1:{port}', token
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), lab.NoRedirect())

    def request(self, method, path, body=None):
        if not path.startswith('/api/v3/'):
            raise RuntimeError('Invalid Authentik API path')
        request = urllib.request.Request(self.address + path,
            json.dumps(body).encode() if body is not None else None,
            {'Host': urllib.parse.urlsplit(ORIGIN).netloc, 'Authorization': 'Bearer ' + self.token, 'Content-Type': 'application/json'}, method=method)
        try:
            with self.opener.open(request, timeout=30) as response:
                raw = response.read(1048576)
                return json.loads(raw) if raw else {}
        except urllib.error.HTTPError as error:
            error.close()
            raise RuntimeError(f'Authentik API {method} failed (HTTP {error.code})') from None
        except (urllib.error.URLError, ValueError, TimeoutError):
            raise RuntimeError('Authentik API request failed') from None

    def one(self, path, field, value):
        result = self.request('GET', '/api/v3/' + path + '/?' + urllib.parse.urlencode({field: value, 'page_size': 100}))
        matches = [item for item in result['results'] if item.get(field) == value]
        if len(matches) != 1:
            raise RuntimeError('Expected one Authentik ' + path + ' configuration')
        return matches[0]


def wait_defaults(api, timeout=180):
    deadline = time.monotonic() + timeout
    while True:
        try:
            for slug in ('default-authentication-flow', 'default-provider-authorization-implicit-consent', 'default-provider-invalidation-flow'):
                api.one('flows/instances', 'slug', slug)
            for scope in ('openid', 'email', 'profile'):
                api.one('propertymappings/provider/scope', 'managed', 'goauthentik.io/providers/oauth2/scope-' + scope)
            return
        except RuntimeError:
            if time.monotonic() >= deadline:
                raise RuntimeError('Authentik default blueprints did not become ready') from None
            time.sleep(3)


def configure(api, values):
    flow = lambda slug: api.one('flows/instances', 'slug', slug)['pk']
    auth_flow = flow('default-authentication-flow')
    authorize = flow('default-provider-authorization-implicit-consent')
    invalidate = flow('default-provider-invalidation-flow')
    mapping_ids = [api.one('propertymappings/provider/scope', 'managed', 'goauthentik.io/providers/oauth2/scope-' + scope)['pk']
                   for scope in ('openid', 'email', 'profile')]
    mapping = api.request('POST', '/api/v3/propertymappings/provider/scope/', {
        'name': 'openbao-lab-groups', 'scope_name': 'groups',
        'expression': 'return {"groups": list(request.user.ak_groups.values_list("name", flat=True))}'})
    key = api.request('POST', '/api/v3/crypto/certificatekeypairs/generate/', {'common_name': 'openbao-lab', 'validity_days': 7, 'alg': 'rsa'})
    provider = api.request('POST', '/api/v3/providers/oauth2/', {
        'name': 'openbao-lab', 'authentication_flow': auth_flow, 'authorization_flow': authorize,
        'invalidation_flow': invalidate, 'client_type': 'confidential', 'client_id': 'openbao',
        'client_secret': values['client-secret'], 'grant_types': ['authorization_code', 'client_credentials'],
        'redirect_uris': [{'matching_mode': 'strict', 'url': lab.CALLBACK}],
        'property_mappings': mapping_ids + [mapping['pk']], 'signing_key': key['pk'],
        'include_claims_in_id_token': True, 'issuer_mode': 'per_provider', 'access_token_validity': 'minutes=10'})
    api.request('POST', '/api/v3/core/applications/', {'name': 'OpenBao lab', 'slug': 'openbao', 'provider': provider['pk']})
    for tenant in lab.TENANTS:
        for role in lab.ROLES:
            username = f'{tenant}-{role}'
            group = api.request('POST', '/api/v3/core/groups/', {'name': username + 's', 'is_superuser': False})
            user = api.request('POST', '/api/v3/core/users/', {'username': username, 'name': username,
                'is_active': True, 'groups': [group['pk']], 'email': username + '@example.invalid', 'path': 'openbao-lab'})
            api.request('POST', f'/api/v3/core/users/{user["pk"]}/set_password/', {'password': values[username]})
            machine_name = 'machine-' + username
            machine = api.request('POST', '/api/v3/core/users/', {'username': machine_name, 'name': machine_name,
                'is_active': True, 'type': 'service_account', 'groups': [group['pk']], 'path': 'openbao-lab'})
            api.request('POST', '/api/v3/core/tokens/', {'identifier': machine_name, 'intent': 'app_password', 'user': machine['pk'], 'expiring': False})
            api.request('POST', f'/api/v3/core/tokens/{machine_name}/set_key/', {'key': values[machine_name]})


def up():
    existing = identity.kube('get', 'namespace', NAMESPACE, '--ignore-not-found', '-o', 'name').strip()
    if existing:
        raise RuntimeError('Authentik namespace already exists; reuse test or delete the disposable cluster before provisioning again')
    identity.apply([identity.obj('Namespace', NAMESPACE, None)])
    values = new_credentials()
    identity.apply([identity.secret('authentik-lab-users', NAMESPACE, values)] + deployment_objects(values))
    identity.kube('-n', NAMESPACE, 'rollout', 'status', 'deployment/authentik-postgres', '--timeout=180s')
    result = subprocess.run([os.environ.get('HELM') or str(identity.ROOT / 'bin/helm'), 'upgrade', '--install', 'authentik', 'authentik', '--repo', 'https://charts.goauthentik.io',
        '--version', CHART_VERSION, '--kube-context', identity.CONTEXT, '-n', NAMESPACE, '--values', str(VALUES_FILE), '--wait', '--timeout', '10m'],
        text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=660)
    if result.returncode:
        raise RuntimeError('Authentik Helm installation failed; inspect pod readiness')
    with identity.forward(NAMESPACE, 'authentik', 9000) as port:
        api = API(port, values['bootstrap-token'])
        wait_defaults(api)
        configure(api, values)
    print('Authentik ready: nine human users and nine scoped service identities')


def apply():
    state = state_from_credentials(credentials())
    kube = lab.Kube(identity.CONTEXT)
    objects = lab.render(state, identity.NAMESPACE, identity.CONNECTION)
    namespaces = [item for item in objects if item['kind'] == 'BaoNamespace']
    kube.apply(namespaces)
    kube.wait(namespaces)
    kube.apply([item for item in objects if item['kind'] != 'BaoNamespace'])
    kube.wait(objects)
    print('Authentik operator OIDC CRDs Ready')


@contextlib.contextmanager
def connect():
    state = state_from_credentials(credentials())
    with identity.forward(NAMESPACE, 'authentik', 9000) as port, lab.connect(lab.Kube(identity.CONTEXT), state) as bao:
        state['browser_route_host'] = '127.0.0.1' if platform.system() == 'Linux' or os.environ.get('LAB_BROWSER_RUNNER') == 'node' else 'host.docker.internal'
        state['browser_route_port'] = port
        state['local_forward_port'] = port
        yield bao, state


def distinct_subjects(human, machine):
    def subject(token):
        try:
            encoded = token.split('.')[1]
            value = json.loads(base64.urlsafe_b64decode(encoded + '=' * (-len(encoded) % 4)))['sub']
            if not isinstance(value, str) or not value:
                raise ValueError()
            return value
        except (IndexError, KeyError, ValueError, TypeError):
            raise RuntimeError('Authentik token subject missing or malformed') from None
    # This checks identity separation; the OpenBao JWT logins verify signatures.
    if subject(human) == subject(machine):
        raise RuntimeError('Authentik service token reused a human identity')


def test():
    with connect() as (bao, state):
        kube = lab.Kube(identity.CONTEXT)
        lab.showcase(bao, state, kube, identity.NAMESPACE)
        human_tokens = jwt_lab.run(bao, state, kube, identity.NAMESPACE, identity.CONNECTION)
        values = credentials()
        metadata = jwt_login.discover(state)
        tokens = {}
        for user in state['users']:
            name = 'machine-' + user['username']
            payload = urllib.parse.urlencode({'grant_type': 'client_credentials', 'client_id': 'openbao',
                'username': name, 'password': values[name], 'scope': 'openid profile email groups'}).encode()
            result = jwt_login.provider_request(state, metadata['token_endpoint'], payload, {'Content-Type': 'application/x-www-form-urlencoded'})
            key = user['namespace'], user['role']
            tokens[key] = result['access_token']
            for human_token in human_tokens.values():
                distinct_subjects(human_token, tokens[key])
        jwt_lab.run_machine(bao, state, kube, tokens, state['oidc_client_id'],
                            identity.NAMESPACE, identity.CONNECTION, human_tokens=human_tokens)
    print('Authentik: human OIDC, human JWT discovery/JWKS and service-account JWT ACL regressions passed')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=('up', 'apply', 'test'))
    args = parser.parse_args()
    try:
        {'up': up, 'apply': apply, 'test': test}[args.command]()
    except RuntimeError as error:
        raise SystemExit(str(error)) from None
    except (ValueError, KeyError, subprocess.TimeoutExpired):
        raise SystemExit('Authentik lab failed; inspect pod readiness and safe browser diagnostics') from None
