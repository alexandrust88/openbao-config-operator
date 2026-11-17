"""Reconcile a real Argo CD app-of-apps hierarchy in the disposable GitOps lab."""
import hashlib
import json
import re
import subprocess
import textwrap
import time
import urllib.request

import common

# Update the upstream manifest checksum and verified image digests together.
VERSION = 'v3.6.1'
MANIFEST_SHA256 = '0b38189d6e1fd76ef5c3db10355026c48a02020d82bf21a1a1b5ab16dffe4f3c'
IMAGES = {
    'quay.io/argoproj/argocd:v3.6.1': 'sha256:296e3d74f449b2ce45265613fcb68c18ed7366488360c8ea32f5f2a93d2e5c54',
    'public.ecr.aws/docker/library/redis:8.10.1-alpine': 'sha256:ef216d85ec44f4fdf7c0e6df2d1757a4734ed32fbb278cabd3181b5a27353b79',
}
NAMESPACE = 'argocd'
SERVER = 'https://kubernetes.default.svc'
APPLICATION_HEALTH = '''local hs = {status = "Progressing", message = "Waiting for child Application sync and health"}
if obj.status == nil then return hs end
if obj.status.operationState ~= nil and obj.status.operationState.phase == "Failed" then
  return {status = "Degraded", message = "Child sync failed"}
end
if obj.status.sync ~= nil and obj.status.sync.status == "Synced" and obj.status.health ~= nil then
  hs.status = obj.status.health.status or "Progressing"
  hs.message = obj.status.health.message or "Child Application health"
end
return hs
'''


def obj(kind, name, spec):
    return {'apiVersion': 'argoproj.io/v1alpha1', 'kind': kind,
            'metadata': {'name': name, 'namespace': NAMESPACE}, 'spec': spec}


def allow(group, *kinds):
    return [{'group': group, 'kind': kind} for kind in kinds]


def project(name, repo, namespace, namespaced, cluster=()):
    return obj('AppProject', name, {'sourceRepos': [repo],
        'destinations': [{'server': SERVER, 'namespace': namespace}],
        'namespaceResourceWhitelist': namespaced, 'clusterResourceWhitelist': list(cluster),
        'orphanedResources': {'warn': False}})


def projects(h):
    return [project('gitops-root', h.repo.url, NAMESPACE, allow('argoproj.io', 'Application')),
        project('gitops-openbao', h.repo.url, 'openbao',
                allow('', 'ConfigMap', 'Service', 'ServiceAccount') + allow('apps', 'StatefulSet') +
                allow('rbac.authorization.k8s.io', 'Role', 'RoleBinding') + allow('policy', 'PodDisruptionBudget'), allow('', 'Namespace')),
        project('gitops-operator', h.repo.url, 'openbao-config-operator-system',
                allow('', 'Service', 'ServiceAccount', 'Secret', 'ConfigMap') + allow('apps', 'Deployment') +
                allow('rbac.authorization.k8s.io', 'Role', 'RoleBinding') + allow('policy', 'PodDisruptionBudget') +
                allow('networking.k8s.io', 'NetworkPolicy'),
                allow('', 'Namespace') + allow('apiextensions.k8s.io', 'CustomResourceDefinition') +
                allow('rbac.authorization.k8s.io', 'ClusterRole', 'ClusterRoleBinding')),
        project('gitops-config', h.repo.url, h.namespace,
                allow('', 'ConfigMap') + allow('core.openbaoconfig.io', 'OpenBaoConnection') +
                [item for kind, group in common.GROUPS.items() for item in allow(group + '.openbaoconfig.io', kind)],
                allow('core.openbaoconfig.io', 'OpenBaoServer'))]


def application(name, project_name, repo, path, namespace, wave=None, helm=None):
    source = {'repoURL': repo, 'targetRevision': 'main', 'path': path}
    if helm is not None:
        source['helm'] = {'releaseName': 'openbao-config-operator', 'valuesObject': helm}
    app = obj('Application', name, {'project': project_name, 'source': source,
        'destination': {'server': SERVER, 'namespace': namespace},
        'syncPolicy': {'automated': {'prune': True, 'selfHeal': True},
            'retry': {'limit': 8, 'backoff': {'duration': '5s', 'factor': 2, 'maxDuration': '30s'}},
            'syncOptions': ['CreateNamespace=true', 'ServerSideApply=true', 'PruneLast=true']}})
    if wave is not None:
        app['metadata']['annotations'] = {'argocd.argoproj.io/sync-wave': str(wave)}
    return app


def manifests(h):
    children = [application('gitops-openbao', 'gitops-openbao', h.repo.url, 'openbao', 'openbao', 0),
                application('gitops-operator', 'gitops-operator', h.repo.url, 'operator', 'openbao-config-operator-system', 10, h.operator_values),
                application('gitops-config', 'gitops-config', h.repo.url, 'config', h.namespace, 20)]
    root = application('gitops-root', 'gitops-root', h.repo.url, 'argocd/root', NAMESPACE)
    return children, root


def pinned_manifest(raw):
    if hashlib.sha256(raw).hexdigest() != MANIFEST_SHA256:
        raise RuntimeError('Argo CD install manifest checksum differs from the pinned release')
    source = raw.decode()
    images = set(re.findall(r'^\s+image: (\S+)\s*$', source, re.MULTILINE))
    if images != set(IMAGES):
        raise RuntimeError('Argo CD install image inventory differs from verified pins')
    for image, digest in IMAGES.items():
        source = source.replace('image: ' + image, 'image: ' + image + '@' + digest)
    return source


def bootstrap_command(h, stage, *args, data=None):
    """Report a fixed bootstrap stage, never manifests or arbitrary API responses."""
    try:
        result = subprocess.run(['kubectl', '--context', h.context, *args], input=data,
                                text=True, capture_output=True, timeout=360)
    except subprocess.TimeoutExpired:
        raise RuntimeError(f'Argo CD bootstrap {stage} failed (command timeout)') from None
    if result.returncode:
        # Only known error categories are exposed: kubectl can include request
        # bodies in errors, so forwarding stderr would risk disclosing Secrets.
        categories = ('Conflict', 'NotFound', 'Forbidden', 'Invalid',
                      'no matches for kind', 'timed out', 'connection refused')
        category = next((value for value in categories if value.lower() in result.stderr.lower()), 'unclassified')
        raise RuntimeError(f'Argo CD bootstrap {stage} failed (exit {result.returncode}; {category})')
    return result.stdout


def install(h):
    h.apply({'apiVersion': 'v1', 'kind': 'Namespace', 'metadata': {'name': NAMESPACE}})
    url = f'https://raw.githubusercontent.com/argoproj/argo-cd/{VERSION}/manifests/core-install.yaml'
    with urllib.request.urlopen(url, timeout=60) as response:
        manifest = pinned_manifest(response.read())
    bootstrap_command(h, 'install pinned controller manifest', '-n', NAMESPACE, 'apply', '--server-side', '-f', '-', data=manifest)
    for crd in ('applications.argoproj.io', 'appprojects.argoproj.io'):
        bootstrap_command(h, 'establish ' + crd, 'wait', '--for=condition=Established', 'crd/' + crd, '--timeout=90s')
    health = (common.ROOT / 'docs/guides/argocd-cm-health.yaml').read_text()
    customizations = textwrap.dedent(health.split('  resource.customizations: |\n', 1)[1])
    bootstrap_command(h, 'configure health checks', '-n', NAMESPACE, 'patch', 'configmap', 'argocd-cm', '--type=merge', '-p', json.dumps({'data': {
        'resource.customizations': customizations,
        'resource.customizations.health.argoproj.io_Application': APPLICATION_HEALTH,
        'timeout.reconciliation': '15s', 'timeout.reconciliation.jitter': '0s',
        'application.resourceTrackingMethod': 'annotation'}}))
    for workload in ('deployment/argocd-redis', 'deployment/argocd-repo-server', 'statefulset/argocd-application-controller'):
        bootstrap_command(h, 'rollout ' + workload, '-n', NAMESPACE, 'rollout', 'status', workload, '--timeout=300s')
    h.apply(projects(h))
    children, root = manifests(h)
    common.write_json(h.repo.path / 'argocd/root/applications.json', {'apiVersion': 'v1', 'kind': 'List', 'items': children})
    common.write_json(h.repo.path / 'argocd/root/kustomization.yaml', {'apiVersion': 'kustomize.config.k8s.io/v1beta1', 'kind': 'Kustomization', 'resources': ['applications.json']})
    h.repo.commit('Declare Argo CD root and ordered child Applications')
    h.apply(root)
    h._argocd_initial_order_verified = False
    print('Argo CD bootstrapped; OpenBao, operator and configuration are declared only in Git')


def synced(app, revision):
    status = app.get('status', {})
    return (status.get('sync', {}).get('status') == 'Synced' and status.get('sync', {}).get('revision') == revision
            and status.get('health', {}).get('status') == 'Healthy')


def wait(h, revision, phase='all'):
    if phase not in ('all', 'infrastructure'):
        raise ValueError('Unknown Argo CD wait phase')
    expected = ['gitops-openbao', 'gitops-operator'] + (['gitops-config', 'gitops-root'] if phase == 'all' else [])
    deadline = time.monotonic() + 600
    pending = expected
    while time.monotonic() < deadline:
        apps = json.loads(h.kubectl('-n', NAMESPACE, 'get', 'applications', '-o', 'json'))['items']
        indexed = {item['metadata']['name']: item for item in apps}
        pending = [name for name in expected if name not in indexed or not synced(indexed[name], revision)]
        if not pending:
            verify_order(indexed)
            if phase == 'all' and not getattr(h, '_argocd_initial_order_verified', False):
                for previous, current in (('gitops-openbao', 'gitops-operator'), ('gitops-operator', 'gitops-config')):
                    operation = indexed[previous].get('status', {}).get('operationState', {})
                    if operation.get('phase') != 'Succeeded' or not operation.get('finishedAt') or operation['finishedAt'] > indexed[current]['metadata']['creationTimestamp']:
                        raise RuntimeError('Argo CD child was created before its dependency sync succeeded')
                h._argocd_initial_order_verified = True
            print('Argo CD ' + phase + ' Applications Synced and Healthy at the expected Git revision')
            return
        for name in pending:
            if name in indexed:
                h.kubectl('-n', NAMESPACE, 'annotate', 'application', name, 'argocd.argoproj.io/refresh=hard', '--overwrite')
        time.sleep(5)
    raise RuntimeError('Argo CD Applications did not converge: ' + ', '.join(pending))


def verify_order(apps):
    for previous, current in (('gitops-openbao', 'gitops-operator'), ('gitops-operator', 'gitops-config')):
        if current not in apps:
            continue
        if previous not in apps:
            raise RuntimeError('Argo CD created a child before its dependency')
        # Creation order is durable across later Git updates and self-heal passes.
        if apps[current]['metadata']['creationTimestamp'] < apps[previous]['metadata']['creationTimestamp']:
            raise RuntimeError('Argo CD child creation order violated its sync waves')


def assert_unhealthy(h, revision):
    def degraded():
        app = h.get('application', 'gitops-config', NAMESPACE)
        status = app.get('status', {})
        h.kubectl('-n', NAMESPACE, 'annotate', 'application', 'gitops-config', 'argocd.argoproj.io/refresh=hard', '--overwrite')
        return (status.get('sync', {}).get('revision') == revision and
                status.get('sync', {}).get('status') == 'Synced' and
                status.get('health', {}).get('status') == 'Degraded')
    common.eventually(degraded, 'Argo CD reports current Git configuration Degraded', timeout=180)
    print('Argo CD rejected invalid configuration health at the expected Git revision')
