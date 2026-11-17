"""Install Flux and reconcile the disposable GitOps regression repository."""
import common

# Update the operator chart version and CHART_DIGEST together after verification.
OPERATOR_VERSION = '0.62.1'
OPERATOR_DIGEST = 'sha256:1d09a3f96524d0895fb11e8072d84a3dd029cecdec0f869efa371be6b001469c'
# renovate: datasource=github-releases depName=fluxcd/flux2
FLUX_VERSION = '2.10.0'
# Update the chart release and its verified OCI digest together.
CHART_DIGEST = 'sha256:1135514a01cc11e206f5ff0b568ac30d9defd675022b7a9da88d4a3474fbdf13'
NAMESPACE = 'flux-system'
SOURCE = 'flux-system'


def obj(api, kind, name, spec=None, namespace=NAMESPACE, **fields):
    value = {'apiVersion': api, 'kind': kind, 'metadata': {'name': name}}
    if namespace:
        value['metadata']['namespace'] = namespace
    if spec is not None:
        value['spec'] = spec
    return {**value, **fields}


def health_expression(managed):
    required = ['Ready', 'Synced'] if managed else ['Ready']
    return "has(status.conditions) && " + " && ".join(
        "status.conditions.exists(c, c.type == '" + condition + "' && c.status == 'True' && c.observedGeneration == metadata.generation)"
        for condition in required)


def manifests(h):
    source = {'kind': 'GitRepository', 'name': SOURCE}
    roots = []
    for name, path in [('openbao', './openbao'), ('operator', './flux/operator'), ('config', './config')]:
        spec = {'interval': '10s', 'retryInterval': '5s', 'path': path, 'prune': True,
                'sourceRef': source, 'wait': True, 'timeout': '5m',
                'serviceAccountName': 'config-reconciler' if name == 'config' else 'platform-reconciler'}
        if name == 'config':
            spec['dependsOn'] = [{'name': 'openbao'}, {'name': 'operator'}]
            spec['healthCheckExprs'] = [
                {'apiVersion': group + '.openbaoconfig.io/v1alpha1', 'kind': kind,
                 'current': health_expression(group != 'core'),
                 'failed': "has(status.conditions) && status.conditions.exists(c, (c.type == 'Ready' || c.type == 'Synced') && c.status == 'False' && c.observedGeneration == metadata.generation && c.reason == 'ReconcileError')"}
                for kind, group in {**common.GROUPS, 'OpenBaoServer': 'core', 'OpenBaoConnection': 'core'}.items()]
        roots.append(obj('kustomize.toolkit.fluxcd.io/v1', 'Kustomization', name, spec))
    release = obj('helm.toolkit.fluxcd.io/v2', 'HelmRelease', 'openbao-config-operator', {
        'interval': '10s', 'releaseName': 'openbao-config-operator',
        'targetNamespace': 'openbao-config-operator-system', 'serviceAccountName': 'platform-reconciler',
        'chart': {'spec': {'chart': './operator', 'sourceRef': source, 'reconcileStrategy': 'Revision', 'interval': '10s'}},
        'install': {'createNamespace': True, 'strategy': {'name': 'RetryOnFailure', 'retryInterval': '10s'}},
        'upgrade': {'strategy': {'name': 'RetryOnFailure', 'retryInterval': '10s'}},
        'driftDetection': {'mode': 'enabled'}, 'values': h.operator_values})
    return roots, release


def permissions(h):
    subjects = lambda name: [{'kind': 'ServiceAccount', 'name': name, 'namespace': NAMESPACE}]
    reference = lambda kind, name: {'apiGroup': 'rbac.authorization.k8s.io', 'kind': kind, 'name': name}
    verbs = ['get', 'list', 'watch', 'create', 'update', 'patch', 'delete']
    return [obj('v1', 'ServiceAccount', name) for name in ('platform-reconciler', 'config-reconciler')] + [
        obj('rbac.authorization.k8s.io/v1', 'ClusterRoleBinding', 'gitops-platform', namespace=None,
            subjects=subjects('platform-reconciler'), roleRef=reference('ClusterRole', 'cluster-admin')),
        obj('rbac.authorization.k8s.io/v1', 'Role', 'gitops-config', namespace=h.namespace,
            rules=[{'apiGroups': [g + '.openbaoconfig.io' for g in ('core', 'sys', 'auth', 'secrets')], 'resources': ['*'], 'verbs': verbs},
                   {'apiGroups': [''], 'resources': ['configmaps'], 'verbs': verbs}]),
        obj('rbac.authorization.k8s.io/v1', 'RoleBinding', 'gitops-config', namespace=h.namespace,
            subjects=subjects('config-reconciler'), roleRef=reference('Role', 'gitops-config')),
        obj('rbac.authorization.k8s.io/v1', 'ClusterRole', 'gitops-server', namespace=None,
            rules=[{'apiGroups': ['core.openbaoconfig.io'], 'resources': ['openbaoservers'], 'verbs': verbs}]),
        obj('rbac.authorization.k8s.io/v1', 'ClusterRoleBinding', 'gitops-server', namespace=None,
            subjects=subjects('config-reconciler'), roleRef=reference('ClusterRole', 'gitops-server'))]


def install(h):
    common.run([str(common.ROOT / 'bin/helm'), 'upgrade', '--install', 'flux-operator',
                'oci://ghcr.io/controlplaneio-fluxcd/charts/flux-operator@' + CHART_DIGEST,
                '--set-string', 'image.tag=v' + OPERATOR_VERSION + '@' + OPERATOR_DIGEST,
                '--kube-context', h.context, '--namespace', NAMESPACE, '--create-namespace', '--wait', '--timeout', '3m'])
    h.apply(permissions(h))
    roots, release = manifests(h)
    common.write_json(h.repo.path / 'flux/root/resources.json', {'apiVersion': 'v1', 'kind': 'List', 'items': roots})
    common.write_json(h.repo.path / 'flux/operator/release.json', release)
    for directory, filename in [('root', 'resources.json'), ('operator', 'release.json')]:
        common.write_json(h.repo.path / 'flux' / directory / 'kustomization.yaml', {
            'apiVersion': 'kustomize.config.k8s.io/v1beta1', 'kind': 'Kustomization', 'resources': [filename]})
    h.repo.commit('Declare Flux infrastructure and configuration dependency graph')
    h.apply(obj('fluxcd.controlplane.io/v1', 'FluxInstance', 'flux', {
        'distribution': {'version': FLUX_VERSION, 'registry': 'ghcr.io/fluxcd'},
        'components': ['source-controller', 'kustomize-controller', 'helm-controller'],
        'cluster': {'type': 'kubernetes', 'size': 'small', 'networkPolicy': True},
        'sync': {'kind': 'GitRepository', 'url': h.repo.url, 'ref': 'refs/heads/main',
                 'path': './flux/root', 'interval': '10s'}}))
    h.kubectl('-n', NAMESPACE, 'wait', '--for=condition=Ready',
              'fluxinstance/flux', '--timeout=300s')


def ready(resource):
    return any(c.get('type') == 'Ready' and c.get('status') == 'True' and
               c.get('observedGeneration') == resource.get('metadata', {}).get('generation')
               for c in resource.get('status', {}).get('conditions', []))


def wait(h, revision, phase='all'):
    names = ['openbao', 'operator'] + (['config'] if phase == 'all' else [])
    def converged():
        source = h.get('gitrepositories.source.toolkit.fluxcd.io', SOURCE, NAMESPACE, missing_ok=True)
        if not ready(source) or not source.get('status', {}).get('artifact', {}).get('revision', '').endswith(revision):
            return False
        for name in names:
            value = h.get('kustomizations.kustomize.toolkit.fluxcd.io', name, NAMESPACE, missing_ok=True)
            if not ready(value) or not value.get('status', {}).get('lastAppliedRevision', '').endswith(revision):
                return False
        release = h.get('helmreleases.helm.toolkit.fluxcd.io', 'openbao-config-operator', NAMESPACE, missing_ok=True)
        chart = h.get('helmcharts.source.toolkit.fluxcd.io', NAMESPACE + '-openbao-config-operator', NAMESPACE, missing_ok=True)
        chart_status = chart.get('status', {})
        chart_revision = chart_status.get('artifact', {}).get('revision')
        return bool(ready(release) and ready(chart) and chart_revision and
                    chart_status.get('observedSourceArtifactRevision', '').endswith(revision) and
                    release.get('status', {}).get('lastAttemptedRevision') == chart_revision)
    common.eventually(converged, 'Flux ' + phase + ' health and exact Git revision', timeout=600)


def assert_unhealthy(h, revision):
    def rejected():
        value = h.get('kustomizations.kustomize.toolkit.fluxcd.io', 'config', NAMESPACE)
        status = value.get('status', {})
        if not status.get('lastAttemptedRevision', '').endswith(revision):
            return False
        return any(c.get('type') == 'Ready' and c.get('status') == 'False' and
                   c.get('reason') == 'HealthCheckFailed' and
                   c.get('observedGeneration') == value['metadata']['generation']
                   for c in status.get('conditions', []))
    common.eventually(rejected, 'Flux rejects current Git revision on resource health', timeout=420)
