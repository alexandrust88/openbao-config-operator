#!/usr/bin/env python3
"""Disposable real-controller GitOps regression; Git contains configuration, never credentials."""
import argparse
import base64
import importlib
import json
import os
from pathlib import Path
import secrets
import re
import ssl
import subprocess
import sys
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / 'hack/lab'))
import identity

NAMESPACE = 'obco-gitops'


def run(args, data=None, cwd=None, timeout=360):
    result = subprocess.run(args, input=data, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, cwd=cwd, timeout=timeout)
    if result.returncode:
        raise RuntimeError(f'{Path(args[0]).name} failed (exit {result.returncode}); output withheld')
    return result.stdout


def eventually(check, label, timeout=180):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if check():
            return
        time.sleep(2)
    raise RuntimeError('Timed out: ' + label)


def statefulset_ready(value):
    replicas = value.get('spec', {}).get('replicas', 0)
    generation = value.get('metadata', {}).get('generation')
    status = value.get('status', {})
    return (isinstance(replicas, int) and replicas > 0 and generation is not None
            and status.get('observedGeneration') == generation and status.get('readyReplicas') == replicas)


def load_yaml(path):
    return json.loads(run([os.environ.get('YQ', str(ROOT / 'bin/yq')), '-o=json', '.', str(path)]))


def fixtures():
    return json.loads(run([os.environ.get('YQ', str(ROOT / 'bin/yq')), 'eval-all', '-o=json', '[.]',
                           str(ROOT / 'gitops/config/root.yaml'), str(ROOT / 'gitops/config/child.yaml')]))


def policy():
    return next(obj['spec']['policy'] for obj in fixtures() if obj['metadata']['name'] == 'root-reader')


class Harness:
    def __init__(self):
        self.context = os.environ['LAB_CONTEXT']
        if not self.context.startswith('kind-'):
            raise ValueError('Only disposable kind contexts are allowed')
        self.namespace = NAMESPACE
        self.public_url = os.environ.get('GITOPS_REPOSITORY', 'https://github.com/alexandrust88/openbao-config-operator.git')
        if not re.fullmatch(r'https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\.git', self.public_url):
            raise ValueError('GitOps source must be a public HTTPS github.com repository without credentials')
        self.revision = os.environ.get('GITOPS_REVISION', 'main')
        if self.revision == 'main':
            advertised = run(['git', '-c', 'credential.helper=', '-c', 'http.extraHeader=', 'ls-remote', self.public_url, 'refs/heads/main'])
            rows = [row.split() for row in advertised.splitlines()]
            if len(rows) != 1 or len(rows[0]) != 2 or rows[0][1] != 'refs/heads/main':
                raise RuntimeError('Public Git main branch could not be resolved')
            self.revision = rows[0][0]
        if not re.fullmatch(r'[0-9a-f]{40}', self.revision):
            raise ValueError('GitOps revision must be main or a full commit SHA')

    def kubectl(self, *args, data=None):
        return run(['kubectl', '--context', self.context, *args], data=json.dumps(data) if data is not None else None)

    def apply(self, objects):
        if isinstance(objects, list):
            objects = {'apiVersion': 'v1', 'kind': 'List', 'items': objects}
        self.kubectl('apply', '--server-side', '--field-manager=gitops-lab-bootstrap', '-f', '-', data=objects)

    def get(self, kind, name, namespace=None, missing_ok=False):
        raw = self.kubectl(*(['-n', namespace] if namespace else []), 'get', kind, name, '-o', 'json',
                           *(['--ignore-not-found'] if missing_ok else []))
        return json.loads(raw) if raw else {}

    def bootstrap(self):
        self.apply({'apiVersion': 'v1', 'kind': 'Namespace', 'metadata': {'name': NAMESPACE}})
        self.apply({'apiVersion': 'v1', 'kind': 'Secret', 'metadata': {'name': 'openbao-e2e', 'namespace': 'openbao'},
                    'stringData': {'seal-key': secrets.token_hex(32), 'token': secrets.token_urlsafe(32)}})
    def connect_credentials(self):
        token = self.get('secret', 'openbao-e2e', 'openbao')['data']['token']
        self.apply({'apiVersion': 'v1', 'kind': 'Secret', 'metadata': {'name': 'gitops-bootstrap', 'namespace': NAMESPACE}, 'data': {'token': token}})
        ca = self.get('configmap', 'openbao-ca', 'openbao')['data']['ca.crt']
        self.apply({'apiVersion': 'v1', 'kind': 'ConfigMap', 'metadata': {'name': 'openbao-ca', 'namespace': NAMESPACE}, 'data': {'ca.crt': ca}})
        return base64.b64decode(token).decode()

    def wait_resources(self):
        for obj in fixtures():
            kind = obj['kind'] + '.' + obj['apiVersion'].split('/')[0]
            def ready():
                current = self.get(kind, obj['metadata']['name'], NAMESPACE, missing_ok=True)
                if not current:
                    return False
                return all(any(c['type'] == kind and c['status'] == 'True' and c.get('observedGeneration') == current['metadata']['generation'] for c in current.get('status', {}).get('conditions', [])) for kind in ('Ready', 'Synced'))
            eventually(ready, 'current-generation Ready ' + obj['metadata']['name'])


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        # A redirect must never forward the privileged lab token to another endpoint.
        return None


class Bao:
    def __init__(self, port, ca, token):
        self.address = f'https://127.0.0.1:{port}/v1/'
        self.token = token
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect(),
                                                  urllib.request.HTTPSHandler(context=ssl.create_default_context(cadata=ca)))

    def request(self, tenant, method, path, payload=None, token=None):
        headers = {'X-Vault-Token': self.token if token is None else token, 'Content-Type': 'application/json'}
        if tenant == 'child': headers['X-Vault-Namespace'] = 'gitops-team'
        req = urllib.request.Request(self.address + path, method=method, headers=headers, data=json.dumps(payload).encode() if payload is not None else None)
        try:
            with self.opener.open(req, timeout=30) as response:
                raw = response.read()
                return response.status, json.loads(raw) if raw else {}
        except urllib.error.HTTPError as exc:
            exc.close()
            return exc.code, {}
        except (urllib.error.URLError, ValueError):
            raise RuntimeError('OpenBao request failed; response withheld') from None


def assert_live(bao):
    for tenant in ('root', 'child'):
        status, body = bao.request(tenant, 'GET', 'sys/policies/acl/gitops-reader')
        if status != 200 or body.get('data', {}).get('policy') != policy():
            raise RuntimeError('Git policy did not reach OpenBao: ' + tenant)
        for path in ('gitops-transit/keys/app', 'auth/gitops-approle/role/reader', 'sys/mounts/gitops-kv/tune'):
            if bao.request(tenant, 'GET', path)[0] != 200:
                raise RuntimeError('Git object not readable in OpenBao: ' + tenant)
        plaintext = base64.b64encode(b'gitops-regression').decode()
        status, result = bao.request(tenant, 'POST', 'gitops-transit/encrypt/app', {'plaintext': plaintext})
        if status != 200: raise RuntimeError('Transit encryption failed')
        status, result = bao.request(tenant, 'POST', 'gitops-transit/decrypt/app', {'ciphertext': result['data']['ciphertext']})
        if status != 200 or result.get('data', {}).get('plaintext') != plaintext:
            raise RuntimeError('Transit round trip failed')


def assert_role_access(bao, tenant):
    status, role = bao.request(tenant, 'GET', 'auth/gitops-approle/role/reader/role-id')
    if status != 200: raise RuntimeError('AppRole role ID unavailable')
    status, secret = bao.request(tenant, 'POST', 'auth/gitops-approle/role/reader/secret-id', {})
    if status != 200: raise RuntimeError('AppRole SecretID issuance failed')
    status, body = bao.request(tenant, 'POST', 'auth/gitops-approle/login', {
        'role_id': role['data']['role_id'], 'secret_id': secret['data']['secret_id']}, token='')
    token = body.get('auth', {}).get('client_token')
    if status != 200 or not token: raise RuntimeError('AppRole login failed')
    try:
        if bao.request(tenant, 'POST', 'gitops-kv/data/app/probe', {'data': {'value': 'gitops-public-probe'}})[0] != 200:
            raise RuntimeError('Unable to seed nonsecret application probe')
        status, read = bao.request(tenant, 'GET', 'gitops-kv/data/app/probe', token=token)
        if status != 200 or read.get('data', {}).get('data', {}).get('value') != 'gitops-public-probe':
            raise RuntimeError('AppRole reader could not read its application probe')
        status, listing = bao.request(tenant, 'LIST', 'gitops-kv/metadata/app/', token=token)
        if status != 200 or 'probe' not in listing.get('data', {}).get('keys', []):
            raise RuntimeError('Declared reader policy did not allow application secret listing')
        for method, path, payload in [('POST', 'gitops-kv/data/app/probe', {'data': {'value': 'forbidden'}}),
                                      ('GET', 'sys/mounts', None), ('GET', 'gitops-kv/data/other/probe', None)]:
            if bao.request(tenant, method, path, payload, token=token)[0] != 403:
                raise RuntimeError('AppRole reader exceeded its intended ACL')
        other = 'child' if tenant == 'root' else 'root'
        if bao.request(other, 'GET', 'gitops-kv/data/app/probe', token=token)[0] != 403:
            raise RuntimeError('AppRole reader crossed the namespace boundary')
    finally:
        if bao.request(tenant, 'POST', 'auth/token/revoke-self', token=token)[0] != 204:
            raise RuntimeError('Unable to revoke the fixture login token')


def regression(h, provider, bao):
    h.wait_resources()
    assert_live(bao)
    for tenant in ('root', 'child'):
        assert_role_access(bao, tenant)
    print('Public Git configuration: root/tenant auth, ACL denials and transit round trips passed', flush=True)
    drift = 'path "gitops-kv/data/app/*" { capabilities = ["read"] }'
    for tenant in ('root', 'child'):
        h.kubectl('-n', NAMESPACE, 'patch', 'policies.sys.openbaoconfig.io', tenant + '-reader', '--type=merge', '-p', json.dumps({'spec': {'policy': drift}}))
    eventually(lambda: all(h.get('policies.sys.openbaoconfig.io', t + '-reader', NAMESPACE)['spec']['policy'] == policy() for t in ('root', 'child')), 'GitOps Kubernetes drift repair')
    for tenant in ('root', 'child'):
        if bao.request(tenant, 'PUT', 'sys/policies/acl/gitops-reader', {'policy': drift})[0] != 204:
            raise RuntimeError('Unable to introduce OpenBao policy drift')
    eventually(lambda: all(bao.request(t, 'GET', 'sys/policies/acl/gitops-reader')[1].get('data', {}).get('policy') == policy() for t in ('root', 'child')), 'operator OpenBao drift repair')
    h.wait_resources()
    provider.wait(h, h.revision, phase='all')
    assert_live(bao)
    print('Kubernetes and OpenBao drift repaired from the unchanged public Git revision', flush=True)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('provider', choices=('argocd', 'flux'))
    args = parser.parse_args()
    h = Harness()
    h.bootstrap()
    print('Private bootstrap material ready', flush=True)
    provider = importlib.import_module(args.provider)
    provider.install(h)
    provider.wait(h, h.revision, phase='infrastructure')
    eventually(lambda: statefulset_ready(h.get('statefulset', 'openbao', 'openbao')),
               'OpenBao StatefulSet current generation and all replicas Ready', timeout=300)
    token = h.connect_credentials()
    provider.wait(h, h.revision, phase='all')
    with identity.forward('openbao', 'openbao-active', 8200) as bao_port:
        ca = h.get('configmap', 'openbao-ca', 'openbao')['data']['ca.crt']
        regression(h, provider, Bao(bao_port, ca, token))


if __name__ == '__main__':
    try:
        main()
    except (RuntimeError, ValueError, subprocess.TimeoutExpired) as exc:
        print(str(exc), file=sys.stderr)
        raise SystemExit(1) from None
