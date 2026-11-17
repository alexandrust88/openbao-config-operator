"""Small Kubernetes fixtures for disposable built-in database integration tests."""
import json
import secrets

import identity as lab

# renovate: datasource=docker depName=python
PYTHON_IMAGE = 'python:3.14.10-alpine@sha256:5862d971d1f0ae26b258cefa4e893556b1d3aba81bd2ff30888704ad378bab67'


def admin_secret(name, namespace, username):
    lab.apply([lab.obj('Namespace', namespace, namespace='')])
    raw = lab.kube('-n', namespace, 'get', 'secret', 'database-admin', '--ignore-not-found', '-o', 'name')
    if not raw.strip():
        lab.apply([lab.secret('database-admin', namespace, {'username': username, 'password': secrets.token_hex(24)})])
    credentials = lab.read_secret('database-admin', namespace)
    lab.apply([lab.secret('database-' + name + '-admin', lab.NAMESPACE, credentials)])


def deploy(name, namespace, image, port, command=None, env=None, volumes=None, mounts=None, memory='512Mi', init_containers=None):
    container = {'name': name, 'image': image, 'ports': [{'containerPort': port}],
                 'env': env or [], 'volumeMounts': mounts or [],
                 'resources': {'requests': {'cpu': '100m', 'memory': '256Mi'}, 'limits': {'memory': memory}},
                 'readinessProbe': {'tcpSocket': {'port': port}, 'periodSeconds': 5, 'failureThreshold': 60}}
    if command:
        container['command'] = command
    lab.apply([lab.obj('Service', name, namespace, spec={'selector': {'app': name}, 'ports': [{'port': port}]}),
               lab.obj('Deployment', name, namespace, api='apps/v1', spec={'replicas': 1,
                 'selector': {'matchLabels': {'app': name}}, 'template': {'metadata': {'labels': {'app': name}},
                   'spec': {'automountServiceAccountToken': False, 'containers': [container], 'initContainers': init_containers or [], 'volumes': volumes or []}}})])
    lab.kube('-n', namespace, 'rollout', 'status', 'deployment/' + name, '--timeout=300s')


def secret_env(variable, key):
    return {'name': variable, 'valueFrom': {'secretKeyRef': {'name': 'database-admin', 'key': key}}}


def connection(name, plugin, config):
    return {'pluginName': plugin, 'passwordSecretRef': {'name': 'database-' + name + '-admin', 'key': 'password'}, **config}


def tenant_database(tenant):
    if tenant not in ('root', 'alpha'):
        raise ValueError('Unknown database lab tenant')
    return 'lab_' + tenant


def tls(name, namespace):
    from . import common
    bundle = common.database_tls(namespace, name)
    lab.apply([lab.secret('database-' + name + '-trust', lab.NAMESPACE,
                         {'ca.crt': bundle['ca.crt'], 'pem_json': json.dumps({'issuing_ca': bundle['ca.crt']})})])
    return bundle


def ca(namespace):
    return json.loads(lab.kube('-n', namespace, 'get', 'configmap', 'db-ca', '-o', 'json'))['data']['ca.crt']


def ca_volume():
    return [{'name': 'ca', 'configMap': {'name': 'db-ca'}}], [{'name': 'ca', 'mountPath': '/ca', 'readOnly': True}]
