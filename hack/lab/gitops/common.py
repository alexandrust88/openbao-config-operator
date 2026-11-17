#!/usr/bin/env python3
"""Disposable real-controller GitOps regression; Git contains configuration, never credentials."""
import argparse
import base64
import importlib
import json
import os
from pathlib import Path
import secrets
import shutil
import ssl
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / 'hack/lab'))
import identity

NAMESPACE = 'obco-gitops'
CONNECTION = 'gitops-openbao'
GROUPS = {'Policy': 'sys', 'SecretsMount': 'sys', 'AuthMount': 'sys', 'OpenBaoNamespace': 'sys', 'AppRole': 'auth', 'TransitKey': 'secrets'}


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


def write_json(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, indent=2) + '\n')


class Repository:
    def __init__(self, path, push_url):
        self.path = Path(path)
        self.url = 'http://git.obco-gitops.svc:8080/repo.git'
        self.path.mkdir(parents=True, exist_ok=False)
        run(['git', 'init', '--initial-branch=main'], cwd=self.path)
        run(['git', 'config', 'user.email', 'gitops-lab@example.invalid'], cwd=self.path)
        run(['git', 'config', 'user.name', 'GitOps regression'], cwd=self.path)
        run(['git', 'remote', 'add', 'origin', push_url], cwd=self.path)

    @property
    def revision(self):
        return run(['git', 'rev-parse', 'HEAD'], cwd=self.path).strip()

    def commit(self, message):
        run(['git', 'add', '.'], cwd=self.path)
        run(['git', 'commit', '-m', message], cwd=self.path)
        run(['git', '-c', 'http.proxy=', 'push', 'origin', 'main'], cwd=self.path)
        return self.revision


def resource(kind, name, spec, tenant='root'):
    shared = {'connectionRef': {'name': CONNECTION}, 'creationPolicy': 'Create',
              'deletionPolicy': 'Orphan', 'driftDetection': 'Enabled', 'driftInterval': '30s'}
    if tenant != 'root':
        shared['namespace'] = 'gitops-team'
    return {'apiVersion': GROUPS[kind] + '.openbaoconfig.io/v1alpha1', 'kind': kind,
            'metadata': {'name': tenant + '-' + name, 'namespace': NAMESPACE}, 'spec': {**shared, **spec}}


def policy(version=1):
    base = 'path "gitops-kv/data/app/*" { capabilities = ["read"] }'
    return base + ('\npath "gitops-kv/metadata/app/*" { capabilities = ["list"] }' if version == 2 else '')


def fixtures(version=1):
    values = [resource('OpenBaoNamespace', 'namespace', {'name': 'gitops-team'})]
    for tenant in ('root', 'child'):
        values += [resource('Policy', 'reader', {'name': 'gitops-reader', 'policy': policy(version)}, tenant),
                   resource('Policy', 'delete', {'name': 'gitops-delete', 'policy': policy(), 'deletionPolicy': 'Delete'}, tenant),
                   resource('Policy', 'orphan', {'name': 'gitops-orphan', 'policy': policy()}, tenant),
                   resource('SecretsMount', 'kv', {'path': 'gitops-kv', 'type': 'kv', 'options': {'version': '2'}}, tenant),
                   resource('SecretsMount', 'transit', {'path': 'gitops-transit', 'type': 'transit'}, tenant),
                   resource('TransitKey', 'key', {'mountPath': 'gitops-transit', 'name': 'app', 'type': 'aes256-gcm96'}, tenant),
                   resource('AuthMount', 'auth', {'path': 'gitops-approle', 'type': 'approle'}, tenant),
                   resource('AppRole', 'role', {'mountPath': 'gitops-approle', 'name': 'reader', 'tokenPolicies': ['gitops-reader'], 'tokenTTL': '5m'}, tenant)]
    return values


class Harness:
    def __init__(self):
        self.context = os.environ['LAB_CONTEXT']
        if not self.context.startswith('kind-'):
            raise ValueError('Only disposable kind contexts are allowed')
        self.namespace = NAMESPACE
        image = os.environ['LAB_OPERATOR_IMAGE']
        repository, tag = image.rsplit(':', 1)
        self.operator_values = {'image': {'repository': repository, 'tag': tag, 'digest': '', 'pullPolicy': 'IfNotPresent'},
                                'resources': {'requests': {'cpu': '50m', 'memory': '128Mi'}, 'limits': {'memory': '512Mi'}}}
        self.repo = None

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
                    'stringData': {'seal-key': secrets.token_hex(32), 'role-id': secrets.token_hex(16),
                                   'secret-id': secrets.token_urlsafe(32), 'token': secrets.token_urlsafe(32)}})
        labels = {'app': 'gitops-git'}
        self.apply([{'apiVersion': 'apps/v1', 'kind': 'Deployment', 'metadata': {'name': 'git', 'namespace': NAMESPACE},
            'spec': {'replicas': 1, 'selector': {'matchLabels': labels}, 'template': {'metadata': {'labels': labels}, 'spec': {
                'automountServiceAccountToken': False, 'securityContext': {'runAsUser': 1000, 'runAsGroup': 1000, 'fsGroup': 1000},
                'containers': [{'name': 'git', 'image': 'example.com/obco-gitops-server:lab', 'imagePullPolicy': 'Never',
                    'securityContext': {'allowPrivilegeEscalation': False, 'capabilities': {'drop': ['ALL']}},
                    'ports': [{'containerPort': 8080}], 'readinessProbe': {'tcpSocket': {'port': 8080}},
                    'resources': {'requests': {'cpu': '20m', 'memory': '32Mi'}, 'limits': {'memory': '256Mi'}},
                    'volumeMounts': [{'name': 'repo', 'mountPath': '/git'}]}], 'volumes': [{'name': 'repo', 'emptyDir': {}}]}}}},
            {'apiVersion': 'v1', 'kind': 'Service', 'metadata': {'name': 'git', 'namespace': NAMESPACE},
             'spec': {'selector': labels, 'ports': [{'port': 8080, 'targetPort': 8080}]}}])
        self.kubectl('-n', NAMESPACE, 'rollout', 'status', 'deployment/git', '--timeout=180s')

    def prepare_repository(self, port):
        parent = ROOT / '.tmp/lab' / self.context.removeprefix('kind-')
        parent.mkdir(parents=True, exist_ok=True)
        path = Path(tempfile.mkdtemp(prefix='gitops-run-', dir=parent)) / 'repository'
        self.repo = Repository(path, f'http://127.0.0.1:{port}/repo.git')
        # The source fixture's Secret is deliberately replaced by random bootstrap-only material.
        docs = (ROOT / 'hack/e2e/openbao.yaml').read_text().split('\n---\n')
        docs = [doc for doc in docs if '\nkind: Secret\n' not in '\n' + doc]
        directory = self.repo.path / 'openbao'
        directory.mkdir()
        (directory / 'openbao.yaml').write_text('\n---\n'.join(docs))
        write_json(directory / 'kustomization.yaml', {'apiVersion': 'kustomize.config.k8s.io/v1beta1', 'kind': 'Kustomization', 'resources': ['openbao.yaml']})
        shutil.copytree(ROOT / 'charts/openbao-config-operator', self.repo.path / 'operator')
        write_json(self.repo.path / 'operator-values.yaml', self.operator_values)
        ca = self.get('configmap', 'openbao-ca', 'openbao')['data']['ca.crt']
        connection = [
            {'apiVersion': 'v1', 'kind': 'ConfigMap', 'metadata': {'name': 'openbao-ca', 'namespace': NAMESPACE}, 'data': {'ca.crt': ca}},
            {'apiVersion': 'core.openbaoconfig.io/v1alpha1', 'kind': 'OpenBaoServer', 'metadata': {'name': CONNECTION}, 'spec': {
                'address': 'https://openbao-active.openbao.svc:8200', 'caBundle': {'configMapRef': {'name': 'openbao-ca', 'namespace': NAMESPACE, 'key': 'ca.crt'}},
                'allowedNamespaces': {'matchLabels': {'kubernetes.io/metadata.name': NAMESPACE}}, 'allowedAudiences': ['openbao']}},
            {'apiVersion': 'core.openbaoconfig.io/v1alpha1', 'kind': 'OpenBaoConnection', 'metadata': {'name': CONNECTION, 'namespace': NAMESPACE},
             'spec': {'serverRef': {'name': CONNECTION}, 'auth': {'token': {'secretRef': {'name': 'gitops-bootstrap', 'key': 'token'}}}}}]
        write_json(self.repo.path / 'config/connection.json', {'apiVersion': 'v1', 'kind': 'List', 'items': connection})
        self.write_fixtures()
        self.repo.commit('Declare OpenBao, operator chart and root/tenant configuration')

    def write_fixtures(self, version=1, prune=False):
        objects = fixtures(version)
        if prune:
            objects = [obj for obj in objects if obj['metadata']['name'] not in ('root-delete', 'root-orphan', 'child-delete', 'child-orphan')]
        write_json(self.repo.path / 'config/objects.json', {'apiVersion': 'v1', 'kind': 'List', 'items': objects})
        write_json(self.repo.path / 'config/kustomization.yaml', {'apiVersion': 'kustomize.config.k8s.io/v1beta1', 'kind': 'Kustomization', 'resources': ['connection.json', 'objects.json']})

    def connect_credentials(self):
        token = self.get('secret', 'openbao-e2e', 'openbao')['data']['token']
        self.apply({'apiVersion': 'v1', 'kind': 'Secret', 'metadata': {'name': 'gitops-bootstrap', 'namespace': NAMESPACE}, 'data': {'token': token}})
        return base64.b64decode(token).decode()

    def wait_resources(self, prune=False):
        for obj in fixtures():
            if prune and obj['metadata']['name'].endswith(('-delete', '-orphan')):
                continue
            kind = obj['kind'] + '.' + obj['apiVersion'].split('/')[0]
            def ready():
                current = self.get(kind, obj['metadata']['name'], NAMESPACE, missing_ok=True)
                if not current:
                    return False
                return all(any(c['type'] == kind and c['status'] == 'True' and c.get('observedGeneration') == current['metadata']['generation'] for c in current.get('status', {}).get('conditions', [])) for kind in ('Ready', 'Synced'))
            eventually(ready, 'current-generation Ready ' + obj['metadata']['name'])


class Bao:
    def __init__(self, port, ca, token):
        self.address = f'https://127.0.0.1:{port}/v1/'
        self.token = token
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPSHandler(context=ssl.create_default_context(cadata=ca)))

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


def assert_live(h, bao, version=1):
    for tenant in ('root', 'child'):
        status, body = bao.request(tenant, 'GET', 'sys/policies/acl/gitops-reader')
        if status != 200 or body.get('data', {}).get('policy') != policy(version):
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


def assert_role_access(bao, tenant, version=1):
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
        if version == 1 and status != 403:
            raise RuntimeError('Initial reader policy unexpectedly allowed metadata listing')
        if version == 2 and (status != 200 or 'probe' not in listing.get('data', {}).get('keys', [])):
            raise RuntimeError('Git permission update did not enable application secret listing')
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


def invalid_policy_rejected(current):
    if current.get('spec', {}).get('policy') != 'path "unterminated {':
        return False
    generation = current.get('metadata', {}).get('generation')
    return generation is not None and any(
        c.get('type') == 'Synced' and c.get('status') == 'False' and c.get('reason') == 'ReconcileError'
        and c.get('observedGeneration') == generation
        for c in current.get('status', {}).get('conditions', []))


def regression(h, provider, bao):
    h.wait_resources()
    assert_live(h, bao)
    for tenant in ('root', 'child'):
        assert_role_access(bao, tenant)
    print('Git initial revision: root/tenant objects Ready and live transit round trips passed', flush=True)
    h.write_fixtures(version=2)
    revision = h.repo.commit('Update reader policies through Git')
    provider.wait(h, revision, phase='all')
    h.wait_resources()
    assert_live(h, bao, 2)
    for tenant in ('root', 'child'):
        assert_role_access(bao, tenant, 2)
    print('Git update reached both Kubernetes and OpenBao', flush=True)
    file = h.repo.path / 'config/objects.json'
    invalid = json.loads(file.read_text())
    next(obj for obj in invalid['items'] if obj['metadata']['name'] == 'root-reader')['spec']['policy'] = 'path "unterminated {'
    write_json(file, invalid)
    broken_revision = h.repo.commit('Prove unhealthy policy reporting')
    def rejected_policy():
        current = h.get('policies.sys.openbaoconfig.io', 'root-reader', NAMESPACE)
        return invalid_policy_rejected(current)
    eventually(rejected_policy, 'current-generation failed policy')
    provider.assert_unhealthy(h, broken_revision)
    h.write_fixtures(version=2)
    recovered_revision = h.repo.commit('Repair invalid policy through Git')
    provider.wait(h, recovered_revision, phase='all')
    h.wait_resources()
    assert_live(h, bao, 2)
    print('Invalid Git policy surfaced unhealthy status; Git correction restored health and live state', flush=True)
    for tenant in ('root', 'child'):
        h.kubectl('-n', NAMESPACE, 'patch', 'policies.sys.openbaoconfig.io', tenant + '-reader', '--type=merge', '-p', json.dumps({'spec': {'policy': policy()}}))
    eventually(lambda: all(h.get('policies.sys.openbaoconfig.io', t + '-reader', NAMESPACE)['spec']['policy'] == policy(2) for t in ('root', 'child')), 'GitOps Kubernetes drift repair')
    for tenant in ('root', 'child'):
        if bao.request(tenant, 'PUT', 'sys/policies/acl/gitops-reader', {'policy': policy()})[0] != 204:
            raise RuntimeError('Unable to introduce OpenBao policy drift')
    eventually(lambda: all(bao.request(t, 'GET', 'sys/policies/acl/gitops-reader')[1].get('data', {}).get('policy') == policy(2) for t in ('root', 'child')), 'operator OpenBao drift repair')
    print('Kubernetes drift and OpenBao drift repaired through their respective controllers', flush=True)
    h.write_fixtures(version=2, prune=True)
    revision = h.repo.commit('Prune explicit Delete and default Orphan policies')
    provider.wait(h, revision, phase='all')
    for tenant in ('root', 'child'):
        for suffix in ('delete', 'orphan'):
            h.kubectl('-n', NAMESPACE, 'wait', '--for=delete', 'policies.sys.openbaoconfig.io/' + tenant + '-' + suffix, '--timeout=120s')
        if bao.request(tenant, 'GET', 'sys/policies/acl/gitops-delete')[0] != 404:
            raise RuntimeError('Delete policy survived Git prune')
        if bao.request(tenant, 'GET', 'sys/policies/acl/gitops-orphan')[0] != 200:
            raise RuntimeError('Orphan policy was removed by Git prune')
    h.wait_resources(prune=True)
    assert_live(h, bao, 2)
    print('Git prune: Delete removed remote policies; Orphan retained them in root and tenant', flush=True)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('provider', choices=('argocd', 'flux'))
    args = parser.parse_args()
    h = Harness()
    h.bootstrap()
    print('Git service and private bootstrap material ready', flush=True)
    provider = importlib.import_module(args.provider)
    with identity.forward(NAMESPACE, 'git', 8080) as port:
        h.prepare_repository(port)
        print('Configuration committed and pushed to the in-cluster Git service', flush=True)
        provider.install(h)
        provider.wait(h, h.repo.revision, phase='infrastructure')
        h.kubectl('-n', 'openbao', 'rollout', 'status', 'statefulset/openbao', '--timeout=300s')
        token = h.connect_credentials()
        provider.wait(h, h.repo.revision, phase='all')
        with identity.forward('openbao', 'openbao-active', 8200) as bao_port:
            ca = h.get('configmap', 'openbao-ca', 'openbao')['data']['ca.crt']
            regression(h, provider, Bao(bao_port, ca, token))


if __name__ == '__main__':
    try:
        main()
    except (RuntimeError, ValueError, subprocess.TimeoutExpired) as exc:
        print(str(exc), file=sys.stderr)
        raise SystemExit(1) from None
