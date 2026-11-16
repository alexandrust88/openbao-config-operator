#!/usr/bin/env python3
"""Provision the disposable ZITADEL kind fixture using its supported management APIs."""
import argparse
import contextlib
import platform
import base64
import datetime
import hashlib
import json
import os
from pathlib import Path
import secrets
import string
import subprocess
import sys
import tempfile
import urllib.error
import urllib.request
import urllib.parse

import identity
sys.path.insert(0, str(Path(__file__).resolve().parent / "saas"))
import operator_lab as lab

NAMESPACE = 'obco-lab-zitadel'
ISSUER = 'http://zitadel.obco-lab-zitadel.svc:8080'
# Update chart version and verified archive checksum together.
CHART_VERSION = '10.5.0'
CHART_SHA256 = 'e216ca1a9b19e9e2982e76a2cf49c446eeb3a02cdf167834977978acb880f019'
# renovate: datasource=docker depName=docker.io/library/postgres
POSTGRES_IMAGE = 'docker.io/library/postgres:16.16-alpine@sha256:81e77a4833487b8132fe4b00bedd39dc3f74b8641e387f19312a48f7de218c2e'


def password():
    alphabet = string.ascii_letters + string.digits + '!_-'
    while True:
        value = ''.join(secrets.choice(alphabet) for _ in range(36))
        if all(any(c in group for c in value) for group in (string.ascii_lowercase, string.ascii_uppercase, string.digits, '!_-')):
            return value


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise RuntimeError('ZITADEL API redirect refused')


class API:
    def __init__(self, port, token):
        self.address = f'http://127.0.0.1:{port}'
        self.token = token.strip()
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())

    def call(self, method, path, data=None):
        if not path.startswith(('/management/v1/', '/v2/', '/auth/v1/', '/admin/v1/')):
            raise ValueError('Unexpected ZITADEL API path')
        request = urllib.request.Request(self.address + path, method=method,
            data=None if data is None else json.dumps(data).encode(), headers={
                'Host': 'zitadel.obco-lab-zitadel.svc:8080', 'Authorization': 'Bearer ' + self.token,
                'Content-Type': 'application/json'})
        try:
            with self.opener.open(request, timeout=30) as response:
                return json.load(response)
        except urllib.error.HTTPError as error:
            error.close()
            raise RuntimeError(f'ZITADEL management API failed (HTTP {error.code}); response omitted') from None
        except (urllib.error.URLError, ValueError, TimeoutError):
            raise RuntimeError('ZITADEL management request failed; response omitted') from None


def action_script(project, client=None, name='obcoGroups'):
    guard = '' if client is None else 'if (ctx.v1.application.getClientId() !== ' + json.dumps(client) + ') return;'
    return '''function %s(ctx, api) {
  %s
  var groups = [];
  if (ctx.v1.user.grants && ctx.v1.user.grants.grants) {
    ctx.v1.user.grants.grants.forEach(function(grant) {
      if (grant.projectId !== %s) return;
      grant.roles.forEach(function(role) {
        if (/^(root|alpha|beta)-(admin|read|ops)s$/.test(role) && groups.indexOf(role) < 0) groups.push(role);
      });
    });
  }
  api.v1.claims.setClaim('groups', groups);
}''' % (name, guard, json.dumps(project))


def configure_password_only_fixture(api):
    # This API is bound to the fixed, disposable local instance, never a SaaS account.
    policy = api.call('GET', '/admin/v1/policies/login').get('policy')
    if not isinstance(policy, dict) or policy.get('allowUsernamePassword') is not True:
        raise RuntimeError('ZITADEL fixture requires an existing password-enabled login policy')
    if policy.get('forceMfa') or policy.get('forceMfaLocalOnly'):
        raise RuntimeError('Refusing to weaken a ZITADEL policy that requires MFA')
    for factor in ('SECOND_FACTOR_TYPE_OTP', 'SECOND_FACTOR_TYPE_U2F'):
        api.call('DELETE', '/admin/v1/policies/login/second_factors/' + factor)
    api.call('DELETE', '/admin/v1/policies/login/multi_factors/MULTI_FACTOR_TYPE_U2F_WITH_VERIFICATION')


def provision(api):
    configure_password_only_fixture(api)
    project = api.call('POST', '/management/v1/projects', {
        'name': 'obco-lab', 'projectRoleAssertion': True, 'projectRoleCheck': True})['id']
    machine_project = api.call('POST', '/management/v1/projects', {
        'name': 'obco-machines', 'projectRoleAssertion': True, 'projectRoleCheck': True})['id']
    app = api.call('POST', f'/management/v1/projects/{project}/apps/oidc', {
        'name': 'OpenBao lab', 'redirectUris': [lab.CALLBACK],
        'responseTypes': ['OIDC_RESPONSE_TYPE_CODE'], 'grantTypes': ['OIDC_GRANT_TYPE_AUTHORIZATION_CODE'],
        'appType': 'OIDC_APP_TYPE_WEB', 'authMethodType': 'OIDC_AUTH_METHOD_TYPE_BASIC',
        'devMode': True, 'idTokenRoleAssertion': True, 'accessTokenRoleAssertion': True,
        'accessTokenType': 'OIDC_TOKEN_TYPE_JWT'})
    state = {'provider': 'zitadel', 'complete': True, 'issuer': ISSUER, 'groups_claim': 'groups',
             'oidc_client_id': app['clientId'], 'oidc_client_secret': app['clientSecret'],
             'callback_uri': lab.CALLBACK, 'project_id': project, 'machine_project_id': machine_project, 'users': [], 'machine_users': []}
    for tenant in lab.TENANTS:
        for role in lab.ROLES:
            group = f'{tenant}-{role}s'
            for target_project in (project, machine_project):
                api.call('POST', f'/management/v1/projects/{target_project}/roles', {'roleKey': group, 'displayName': group})
            username = f'{tenant}-{role}@example.test'
            secret = password()
            user = api.call('POST', '/v2/users/human', {'username': username,
                'profile': {'givenName': tenant, 'familyName': role, 'preferredLanguage': 'en'},
                'email': {'email': username, 'isVerified': True},
                'password': {'password': secret, 'changeRequired': False}})
            api.call('POST', f'/management/v1/users/{user["userId"]}/grants', {'projectId': project, 'roleKeys': [group]})
            state['users'].append({'namespace': tenant, 'role': role, 'username': username, 'password': secret, 'group': group, 'subject': user['userId']})
            machine = api.call('POST', '/management/v1/users/machine', {'userName': f'obco-{tenant}-{role}',
                'name': f'OpenBao {tenant} {role} machine', 'accessTokenType': 'ACCESS_TOKEN_TYPE_JWT'})
            api.call('POST', f'/management/v1/users/{machine["userId"]}/grants', {'projectId': machine_project, 'roleKeys': [group]})
            credential = api.call('PUT', f'/management/v1/users/{machine["userId"]}/secret', {})
            state['machine_users'].append({'namespace': tenant, 'role': role, 'group': group,
                'client_id': credential['clientId'], 'client_secret': credential['clientSecret'],
                'subject': machine['userId']})
    for trigger, client in (('4', app['clientId']), ('5', None)):
        name = 'obcoIDGroups' if client else 'obcoAccessGroups'
        action = api.call('POST', '/management/v1/actions', {'name': name,
            'script': action_script(project if client else machine_project, client, name), 'timeout': '5s', 'allowedToFail': False})
        api.call('POST', f'/management/v1/flows/2/trigger/{trigger}', {'actionIds': [action['id']]})
    return state


def resources(values):
    labels = {'app': 'zitadel-postgres'}
    return [identity.obj('Namespace', NAMESPACE, namespace=None),
        identity.secret('zitadel-masterkey', NAMESPACE, {'masterkey': values['masterkey']}),
        identity.secret('zitadel-postgres', NAMESPACE, {'password': values['database']}),
        identity.secret('zitadel-config', NAMESPACE, {'config.yaml': json.dumps({'Database': {'Postgres': {
            'User': {'Password': values['database']}, 'Admin': {'Password': values['database']}}}})}),
        identity.obj('Service', 'zitadel-postgres', namespace=NAMESPACE,
                     spec={'selector': labels, 'ports': [{'port': 5432, 'targetPort': 5432}]}),
        identity.obj('Deployment', 'zitadel-postgres', namespace=NAMESPACE, api='apps/v1', spec={
            'replicas': 1, 'selector': {'matchLabels': labels}, 'template': {'metadata': {'labels': labels}, 'spec': {
                'containers': [{'name': 'postgres', 'image': POSTGRES_IMAGE,
                    'env': [{'name': 'POSTGRES_PASSWORD', 'valueFrom': {'secretKeyRef': {'name': 'zitadel-postgres', 'key': 'password'}}}],
                    'readinessProbe': {'exec': {'command': ['pg_isready', '-U', 'postgres']}, 'initialDelaySeconds': 3},
                    'resources': {'requests': {'cpu': '50m', 'memory': '64Mi'}, 'limits': {'memory': '256Mi'}},
                    'volumeMounts': [{'name': 'data', 'mountPath': '/var/lib/postgresql/data'}]}],
                'volumes': [{'name': 'data', 'emptyDir': {}}]}}})]


def up():
    existing = json.loads(identity.kube('get', 'namespace', NAMESPACE, '--ignore-not-found', '-o', 'json') or '{}')
    if existing:
        # Never overwrite a partial fixture and lose track of credentials or provider IDs.
        saved = identity.read_secret('zitadel-lab-state', NAMESPACE)
        if not json.loads(saved['state.json']).get('complete'):
            raise RuntimeError('Incomplete ZITADEL fixture; delete only its disposable namespace before rebuilding')
        print('ZITADEL fixture already provisioned')
        return
    values = {'masterkey': secrets.token_hex(16), 'database': password()}
    identity.apply(resources(values))
    identity.kube('-n', NAMESPACE, 'rollout', 'status', 'deployment/zitadel-postgres', '--timeout=180s')
    url = f'https://github.com/zitadel/zitadel-charts/releases/download/zitadel-{CHART_VERSION}/zitadel-{CHART_VERSION}.tgz'
    archive = urllib.request.urlopen(url, timeout=30).read()
    if hashlib.sha256(archive).hexdigest() != CHART_SHA256:
        raise RuntimeError('ZITADEL chart digest does not match the pinned release')
    with tempfile.TemporaryDirectory(prefix='obco-zitadel-') as directory:
        chart = Path(directory) / 'chart.tgz'
        chart.write_bytes(archive)
        expiry = (datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(days=1)).strftime('%Y-%m-%dT%H:%M:%SZ')
        override = Path(directory) / 'expiry.json'
        override.write_text(json.dumps({'zitadel': {'configmapConfig': {'FirstInstance': {'Org': {
            'Machine': {'Pat': {'ExpirationDate': expiry}}}}}}}))
        result = subprocess.run([str(identity.ROOT / 'bin/helm'), 'upgrade', '--install', 'zitadel', str(chart),
            '--kube-context', identity.CONTEXT, '-n', NAMESPACE, '-f', str(identity.ROOT / 'hack/lab/zitadel/values.yaml'),
            '-f', str(override), '--wait', '--timeout', '8m'], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, timeout=540)
        if result.returncode:
            raise RuntimeError('ZITADEL chart install failed; inspect fixture pod status (Helm output omitted)')
    token = identity.read_secret('obco-bootstrap-pat', NAMESPACE)['pat']
    with identity.forward(NAMESPACE, 'zitadel', 8080) as port:
        state = provision(API(port, token))
    identity.apply([identity.secret('zitadel-lab-state', NAMESPACE, {'state.json': json.dumps(state)})])
    print('ZITADEL provisioned: nine humans, nine service users, scoped role claims')


def state():
    return json.loads(identity.read_secret('zitadel-lab-state', NAMESPACE)['state.json'])


@contextlib.contextmanager
def connect():
    current = state()
    kube = lab.Kube(identity.CONTEXT)
    with identity.forward(NAMESPACE, 'zitadel', 8080) as port, lab.connect(kube, current) as bao:
        current['browser_route_host'] = '127.0.0.1' if platform.system() == 'Linux' or os.environ.get('LAB_BROWSER_RUNNER') == 'node' else 'host.docker.internal'
        current['browser_route_port'] = port
        yield bao, current, kube, port


def apply():
    current = state()
    kube = lab.Kube(identity.CONTEXT)
    objects = lab.render(current, identity.NAMESPACE, identity.CONNECTION)
    namespaces = [obj for obj in objects if obj['kind'] == 'OpenBaoNamespace']
    kube.apply(namespaces)
    kube.wait(namespaces)
    kube.apply([obj for obj in objects if obj['kind'] != 'OpenBaoNamespace'])
    kube.wait(objects)
    print('ZITADEL operator configuration Ready')


def machine_tokens(current, port):
    result = {}
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    for machine in current['machine_users']:
        raw = urllib.parse.quote_plus(machine['client_id']) + ':' + urllib.parse.quote_plus(machine['client_secret'])
        scope = 'openid urn:zitadel:iam:org:project:id:' + current['machine_project_id'] + ':aud urn:zitadel:iam:org:projects:roles'
        request = urllib.request.Request(f'http://127.0.0.1:{port}/oauth/v2/token',
            data=urllib.parse.urlencode({'grant_type': 'client_credentials', 'scope': scope}).encode(),
            headers={'Host': 'zitadel.obco-lab-zitadel.svc:8080', 'Content-Type': 'application/x-www-form-urlencoded',
                     'Authorization': 'Basic ' + base64.b64encode(raw.encode()).decode()})
        try:
            with opener.open(request, timeout=30) as response:
                value = json.load(response)['access_token']
        except urllib.error.HTTPError as error:
            error.close()
            raise RuntimeError(f'ZITADEL machine token request failed (HTTP {error.code})') from None
        except (urllib.error.URLError, KeyError, ValueError):
            raise RuntimeError('ZITADEL machine token request failed; response omitted') from None
        if not isinstance(value, str) or len(value.split('.')) != 3:
            raise RuntimeError('ZITADEL machine credential did not produce a signed JWT')
        try:
            encoded = value.split('.')[1]
            claims = json.loads(base64.urlsafe_b64decode(encoded + '=' * (-len(encoded) % 4)))
            if claims.get('sub') != machine['subject'] or claims['sub'] in {u['subject'] for u in current['users']}:
                raise ValueError()
        except (ValueError, KeyError, TypeError):
            raise RuntimeError('ZITADEL machine token subject does not match its dedicated service user') from None
        result[machine['namespace'], machine['role']] = value
    return result


def test():
    import jwt_lab
    with connect() as (bao, current, kube, port):
        lab.showcase(bao, current, kube, identity.NAMESPACE)
        human_tokens = jwt_lab.run(bao, current, kube, identity.NAMESPACE, identity.CONNECTION)
        jwt_lab.run_machine(bao, current, kube, machine_tokens(current, port), current['machine_project_id'],
                            identity.NAMESPACE, identity.CONNECTION, human_tokens=human_tokens)
    print('ZITADEL human OIDC, human JWT and machine JWT regressions passed')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=['up', 'apply', 'test'])
    args = parser.parse_args()
    try:
        {'up': up, 'apply': apply, 'test': test}[args.command]()
    except RuntimeError as error:
        raise SystemExit(str(error)) from None
    except (ValueError, KeyError, urllib.error.URLError, subprocess.TimeoutExpired):
        raise SystemExit('ZITADEL fixture setup failed; inspect dedicated namespace resources; provider responses omitted') from None
