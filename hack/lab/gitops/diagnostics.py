#!/usr/bin/env python3
"""Print bounded nonsecret status fields before the disposable cluster is removed."""
import json
import os
import re
import subprocess


def safe(value):
    value = str(value or '')
    return value if re.fullmatch(r'[A-Za-z0-9_.:/-]{0,120}', value) else 'omitted'


def generation(value):
    return value if type(value) is int and value >= 0 else None


def report(resource):
    result = subprocess.run(['kubectl', '--context', os.environ['LAB_CONTEXT'], 'get', resource, '-A', '-o', 'json'],
                            capture_output=True, text=True, timeout=15)
    if result.returncode:
        print(resource + ': status unavailable', flush=True)
        return
    for obj in json.loads(result.stdout).get('items', [])[:100]:
        meta, status = obj.get('metadata', {}), obj.get('status', {})
        ready = next((c for c in status.get('conditions', []) if c.get('type') == 'Ready'), {})
        print(json.dumps({'kind': safe(obj.get('kind')), 'namespace': safe(meta.get('namespace')), 'name': safe(meta.get('name')),
                          'generation': generation(meta.get('generation')), 'observedGeneration': generation(status.get('observedGeneration')),
                          'conditions': [{
                              'type': c['type'], 'status': safe(c.get('status')), 'reason': safe(c.get('reason')),
                              'observedGeneration': generation(c.get('observedGeneration'))
                          } for c in status.get('conditions', []) if c.get('type') in ('Ready', 'Synced')],
                          'phase': safe(status.get('phase')), 'ready': safe(ready.get('status')), 'reason': safe(ready.get('reason')),
                          'health': safe(status.get('health', {}).get('status')), 'sync': safe(status.get('sync', {}).get('status'))}), flush=True)


if __name__ == '__main__':
    for resource in ('pods', 'applications.argoproj.io', 'gitrepositories.source.toolkit.fluxcd.io',
                     'kustomizations.kustomize.toolkit.fluxcd.io', 'helmreleases.helm.toolkit.fluxcd.io',
                     'policies.sys.openbaoconfig.io', 'openbaoconnections.core.openbaoconfig.io'):
        try:
            report(resource)
        except (subprocess.TimeoutExpired, ValueError):
            print(resource + ': status unavailable', flush=True)
