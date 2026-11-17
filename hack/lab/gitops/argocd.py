"""Install Argo CD with Helm and reconcile checked-in public GitOps examples."""
import json
import subprocess
import time

import common

NAMESPACE = 'argocd'
# renovate: datasource=helm depName=argo-cd registryUrl=https://argoproj.github.io/argo-helm
CHART_VERSION = '10.18.0'
CHART_REPOSITORY = 'https://argoproj.github.io/argo-helm'
PUBLIC_REPOSITORY = 'https://github.com/alexandrust88/openbao-config-operator.git'
EXAMPLES = common.ROOT / 'gitops/argocd'

def read_object(path):
    """Read the checked-in example using the shared, pinned YAML tool."""
    return common.load_yaml(path)


def root_application(repository=PUBLIC_REPOSITORY, revision='main'):
    app = read_object(EXAMPLES / 'root.yaml')
    app['spec']['source']['repoURL'] = repository
    app['spec']['source']['targetRevision'] = revision
    patches = []
    for name in ('gitops-openbao', 'gitops-operator', 'gitops-config'):
        indices = (1, 2) if name == 'gitops-openbao' else (1,)
        paths = [f'/spec/sources/{index}' for index in indices] if name != 'gitops-config' else ['/spec/source']
        operations = [{'op': 'replace', 'path': path + '/' + field, 'value': value}
                      for path in paths for field, value in (('repoURL', repository), ('targetRevision', revision))]
        patches.append({'target': {'kind': 'Application', 'name': name}, 'patch': json.dumps(operations)})
    app['spec']['source']['kustomize'] = {'patches': patches}
    return app


def bootstrap_command(h, stage, *args, data=None):
    try:
        result = subprocess.run(['kubectl', '--context', h.context, *args], input=data,
                                text=True, capture_output=True, timeout=360)
    except subprocess.TimeoutExpired:
        raise RuntimeError(f'Argo CD bootstrap {stage} failed (command timeout)') from None
    if result.returncode:
        categories = ('Conflict', 'NotFound', 'Forbidden', 'Invalid',
                      'no matches for kind', 'timed out', 'connection refused')
        category = next((value for value in categories if value.lower() in result.stderr.lower()), 'unclassified')
        raise RuntimeError(f'Argo CD bootstrap {stage} failed (exit {result.returncode}; {category})')
    return result.stdout


def install(h):
    helm = str(common.ROOT / 'bin/helm')
    common.run([helm, 'repo', 'add', 'argo', CHART_REPOSITORY, '--force-update'])
    common.run([helm, 'repo', 'update', 'argo'])
    common.run([helm, 'upgrade', '--install', 'argocd', 'argo/argo-cd',
                '--version', CHART_VERSION, '--kube-context', h.context,
                '--namespace', NAMESPACE, '--create-namespace', '--values', str(EXAMPLES / 'values/argocd.yaml'),
                '--wait', '--timeout', '10m'], timeout=660)
    for crd in ('applications.argoproj.io', 'appprojects.argoproj.io'):
        bootstrap_command(h, 'establish ' + crd, 'wait', '--for=condition=Established', 'crd/' + crd, '--timeout=90s')
    for path in sorted((EXAMPLES / 'projects').glob('gitops-*.yaml')):
        project = read_object(path)
        project['spec']['sourceRepos'] = [h.public_url if url == PUBLIC_REPOSITORY else url
                                         for url in project['spec']['sourceRepos']]
        h.apply(project)
    h.apply(root_application(h.public_url, h.revision))
    h._argocd_initial_order_verified = False
    print('Argo CD Helm server and UI ready; Applications use public Git and published charts')


def synced(app, revision):
    status = app.get('status', {})
    sync = status.get('sync', {})
    if app.get('spec', {}).get('sources'):
        sources = app['spec']['sources']
        revisions = sync.get('revisions', [])
        current = (len(sources) == len(revisions) and
                   all(actual == (source['targetRevision'] if source.get('chart') else revision)
                       for source, actual in zip(sources, revisions)))
    else:
        current = sync.get('revision') == revision
    return (sync.get('status') == 'Synced' and current
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
