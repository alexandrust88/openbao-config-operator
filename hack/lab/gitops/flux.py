"""Install Flux and reconcile the disposable GitOps regression repository."""
import json
import os
import re

import common

# Update the operator chart version and CHART_DIGEST together after verification.
OPERATOR_VERSION = '0.62.1'
OPERATOR_DIGEST = 'sha256:1d09a3f96524d0895fb11e8072d84a3dd029cecdec0f869efa371be6b001469c'
# Update the chart release and its verified OCI digest together.
CHART_DIGEST = 'sha256:1135514a01cc11e206f5ff0b568ac30d9defd675022b7a9da88d4a3474fbdf13'
NAMESPACE = 'flux-system'
SOURCE = 'openbao-config-operator'
EXAMPLES = common.ROOT / 'gitops/flux'


def load(relative):
    return json.loads(common.run([os.environ.get('YQ', str(common.ROOT / 'bin/yq')), '-o=json', '.', str(EXAMPLES / relative)]))


def health_expressions():
    roots = load('bootstrap/kustomizations.yaml')['items']
    return next(item for item in roots if item['metadata']['name'] == 'config')['spec']['healthCheckExprs']


def bootstrap(h):
    source = load('bootstrap/source.yaml')
    source['spec']['url'] = h.public_url
    if h.revision != 'main':
        if not re.fullmatch(r'[0-9a-f]{40}', h.revision):
            raise ValueError('GitOps revision must be main or an exact commit SHA')
        source['spec']['ref'] = {'commit': h.revision}
    return [load('bootstrap/rbac.yaml'), source, load('bootstrap/kustomizations.yaml')]


def install(h):
    common.run([str(common.ROOT / 'bin/helm'), 'upgrade', '--install', 'flux-operator',
                'oci://ghcr.io/controlplaneio-fluxcd/charts/flux-operator@' + CHART_DIGEST,
                '--set-string', 'image.tag=v' + OPERATOR_VERSION + '@' + OPERATOR_DIGEST,
                '--kube-context', h.context, '--namespace', NAMESPACE, '--create-namespace', '--wait', '--timeout', '3m'])
    h.apply(load('flux-instance.yaml'))
    h.kubectl('-n', NAMESPACE, 'wait', '--for=condition=Ready', 'fluxinstance/flux', '--timeout=300s')
    for resource in bootstrap(h):
        h.apply(resource)


def ready(resource):
    return any(c.get('type') == 'Ready' and c.get('status') == 'True' and
               c.get('observedGeneration') == resource.get('metadata', {}).get('generation')
               for c in resource.get('status', {}).get('conditions', []))


def wait(h, revision, phase='all'):
    names = ['openbao', 'operator'] + (['config'] if phase == 'all' else [])
    def converged():
        source = h.get('gitrepositories.source.toolkit.fluxcd.io', SOURCE, NAMESPACE, missing_ok=True)
        if not ready(source) or not matches_revision(source.get('status', {}).get('artifact', {}).get('revision', ''), revision):
            return False
        source_revision = source['status']['artifact']['revision']
        for name in names:
            value = h.get('kustomizations.kustomize.toolkit.fluxcd.io', name, NAMESPACE, missing_ok=True)
            if not ready(value) or value.get('status', {}).get('lastAppliedRevision') != source_revision:
                return False
        for name in ('openbao', 'openbao-config-operator'):
            release = h.get('helmreleases.helm.toolkit.fluxcd.io', name, NAMESPACE, missing_ok=True)
            if not ready(release):
                return False
        return True
    common.eventually(converged, 'Flux ' + phase + ' health and exact Git revision', timeout=600)




def matches_revision(actual, requested):
    if requested == 'main':
        return actual.startswith(('main@sha1:', 'refs/heads/main@sha1:'))
    return actual == 'sha1:' + requested or actual.endswith('@sha1:' + requested)
