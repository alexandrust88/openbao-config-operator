"""Shared credential lifecycle and isolated Kubernetes probes for database labs."""
import json
import re
import secrets
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import identity as lab

TENANTS = ('root', 'alpha')
ROOT = Path(__file__).resolve().parents[3]
LABEL = {'lab.openbaoconfig.io/profile': 'database'}


def mount(provider):
    return 'lab-db-' + provider.NAME


def run_probe(image, command, namespace, secret_name, env=None, volumes=None, mounts=None, timeout=120):
    name = 'db-probe-' + secrets.token_hex(5)
    environment = [{'name': 'DB_USERNAME', 'valueFrom': {'secretKeyRef': {'name': secret_name, 'key': 'username'}}},
                   {'name': 'DB_PASSWORD', 'valueFrom': {'secretKeyRef': {'name': secret_name, 'key': 'password'}}}]
    environment += [{'name': key, 'value': str(value)} for key, value in
                    {'HOME': '/tmp', 'LC_ALL': 'C', 'PYTHONDONTWRITEBYTECODE': '1', **(env or {})}.items()]
    job = lab.obj('Job', name, namespace, api='batch/v1', spec={
        'backoffLimit': 0, 'activeDeadlineSeconds': timeout, 'template': {'metadata': {'labels': LABEL}, 'spec': {
            'restartPolicy': 'Never', 'automountServiceAccountToken': False,
            'securityContext': {'runAsNonRoot': True, 'runAsUser': 65532, 'runAsGroup': 65532,
                                                    'seccompProfile': {'type': 'RuntimeDefault'}},
            'containers': [{'name': 'probe', 'image': image, 'command': command, 'env': environment,
                            'securityContext': {'allowPrivilegeEscalation': False, 'readOnlyRootFilesystem': True,
                                                'capabilities': {'drop': ['ALL']}},
                            'resources': {'requests': {'cpu': '10m', 'memory': '32Mi'}, 'limits': {'memory': '256Mi'}},
                            'volumeMounts': [{'name': 'tmp', 'mountPath': '/tmp'}, *(mounts or [])]}],
            'volumes': [{'name': 'tmp', 'emptyDir': {}}, *(volumes or [])]}}})
    lab.apply([job])
    try:
        lab.kube('-n', namespace, 'wait', '--for=condition=Complete', 'job/' + name, '--timeout=' + str(timeout + 15) + 's')
        # Probes only emit this fixed success marker; database output stays inside the ephemeral pod.
        if json.loads(lab.kube('-n', namespace, 'logs', 'job/' + name)) != {'ok': True}:
            raise RuntimeError('Database probe did not return its fixed success marker')
    except RuntimeError:
        try:
            pods = json.loads(lab.kube('-n', namespace, 'get', 'pods', '-l', 'job-name=' + name, '-o', 'json'))
            for pod in pods.get('items', []):
                for container in pod.get('status', {}).get('containerStatuses', []):
                    terminated = container.get('state', {}).get('terminated', {})
                    message = terminated.get('message', '').strip()
                    if re.fullmatch(r'SQL_FAILURE code=(?:[0-9]{4}|unknown)(?: tls=(?:certificate|hostname|server-disabled|ca-file|protocol|unclassified))?', message):
                        print(message, flush=True)
        except (RuntimeError, ValueError):
            pass
        raise
    finally:
        lab.kube('-n', namespace, 'delete', 'job', name, '--wait=false', '--ignore-not-found')


def chart_values(provider, tenant):
    return {'configNamespace': lab.NAMESPACE, 'connectionRef': lab.CONNECTION,
            'provider': provider.NAME, 'tenant': tenant, 'workloadNamespace': provider.NAMESPACE,
            'database': {'connection': provider.connection(tenant), 'role': provider.role(tenant)}}


def objects(provider, tenant):
    import subprocess
    values = json.dumps(chart_values(provider, tenant))
    result = subprocess.run([str(ROOT / 'bin/helm'), 'template', 'database-demo',
                             str(ROOT / 'gitops/databases/chart'), '--values', '-'],
                            input=values, text=True, capture_output=True, timeout=30)
    if result.returncode:
        raise RuntimeError('Database configuration chart failed to render')
    parsed = subprocess.run([str(ROOT / 'bin/yq'), 'eval-all', '-o=json', '[.]', '-'],
                            input=result.stdout, text=True, capture_output=True, timeout=30)
    if parsed.returncode:
        raise RuntimeError('Rendered database configuration was invalid YAML')
    return [item for item in json.loads(parsed.stdout) if item]


def readiness_failure(resources):
    """Expose only fixed diagnostic categories; provider errors may contain credentials."""
    patterns = (('authentication', r'access denied|password authentication failed'),
                ('tls-trust', r'unknown authority|unable to verify|certificate verify failed'),
                ('tls-hostname', r'certificate.*not valid|doesn.t contain.*SAN'),
                ('tls-required', r'insecure transport|secure transport|ssl connection is required'),
                ('tls', r'\btls\b|x509|\bssl\b'),
                ('transport', r'connection refused|dial tcp|i/o timeout|network is unreachable'),
                ('plugin', r'plugin.*not found|unknown plugin'))
    for resource in resources:
        if resource['kind'] != 'DatabaseConnection':
            continue
        current = json.loads(lab.kube('-n', lab.NAMESPACE, 'get', 'databaseconnection', resource['metadata']['name'], '-o', 'json'))
        for condition in current.get('status', {}).get('conditions', []):
            if condition.get('status') != 'False' or condition.get('observedGeneration') != current['metadata'].get('generation'):
                continue
            message = condition.get('message', '')
            category = next((name for name, pattern in patterns if re.search(pattern, message, re.IGNORECASE)), 'unclassified')
            status = re.search(r'(?:status|HTTP)\s+([1-5][0-9]{2})', message)
            reason = condition.get('reason', '')
            if not re.fullmatch(r'[A-Za-z]{1,64}', reason):
                reason = 'Unclassified'
            print('DatabaseConnection readiness failed: reason=' + reason + ' category=' + category +
                  ' http_status=' + (status.group(1) if status else 'unknown'), flush=True)


def up(provider):
    provider.install()
    for tenant in TENANTS:
        lab.apply([lab.obj('ServiceAccount', 'db-' + tenant, provider.NAMESPACE)])
        resources = objects(provider, tenant)
        lab.apply(resources)
        try:
            lab.wait_ready(resources, timeout=180)
        except RuntimeError:
            readiness_failure(resources)
            raise
    print(provider.NAME + ': operator database configuration Ready in root and child')


def test(provider):
    admin = lab.read_secret('openbao-e2e', 'openbao')['token']
    with lab.forward('openbao', 'openbao-active', 8200) as port:
        bao = lab.Bao(port)
        for tenant in TENANTS:
            jwt = lab.kube('-n', provider.NAMESPACE, 'create', 'token', 'db-' + tenant,
                           '--audience=openbao', '--duration=15m').strip()
            status, response = bao.request(tenant, 'POST', 'auth/db-' + provider.NAME + '-k8s/login',
                                           {'role': 'reader', 'jwt': jwt})
            lab.expect(status, 200, 'database workload authentication')
            token = response['auth']['client_token']
            leases, secret_names, credentials = [], [], []
            try:
                other = 'alpha' if tenant == 'root' else 'root'
                lab.expect(bao.request(other, 'GET', mount(provider) + '/creds/reader', token=token)[0], 403,
                           'cross-namespace credential issuance denied')
                for _ in range(2):
                    status, response = bao.request(tenant, 'GET', mount(provider) + '/creds/reader', token=token)
                    lab.expect(status, 200, 'dynamic database credential issuance')
                    lease = response.get('lease_id')
                    data = response.get('data', {})
                    if isinstance(lease, str) and lease.startswith(mount(provider) + '/creds/reader/'):
                        leases.append(lease)
                    if not lease or lease not in leases or not response.get('renewable') or response.get('lease_duration', 0) <= 0 or not data.get('username') or not data.get('password'):
                        raise RuntimeError('Dynamic credential lacks a renewable lease or login fields')
                    credentials.append(data)
                    name = 'db-credential-' + secrets.token_hex(5)
                    secret_names.append(name)
                    lab.apply([lab.secret(name, provider.NAMESPACE, {'username': data['username'], 'password': data['password']})])
                if leases[0] == leases[1] or credentials[0]['username'] == credentials[1]['username']:
                    raise RuntimeError('Independent requests reused a database identity or lease')
                provider.probe(tenant, secret_names[0], 'verify')
                provider.probe(tenant, secret_names[1], 'connect')
                status, renewed = bao.request(tenant, 'POST', 'sys/leases/renew', {'lease_id': leases[0], 'increment': 300}, admin)
                lab.expect(status, 200, 'database lease renewal')
                if renewed.get('lease_id') != leases[0] or renewed.get('lease_duration', 0) <= 0:
                    raise RuntimeError('Renewal did not retain the active lease')
                provider.probe(tenant, secret_names[0], 'connect')
                lab.expect(bao.request(tenant, 'POST', 'sys/leases/revoke', {'lease_id': leases[0], 'sync': True}, admin)[0], 204,
                           'database lease revocation')
                leases.pop(0)
                provider.probe(tenant, secret_names[0], 'revoked')
                provider.probe(tenant, secret_names[1], 'connect')
                print(provider.NAME + '/' + tenant + ': real login, read-only and tenant denials, distinct leases, renewal and revoked-login denial passed')
            finally:
                cleanup_failed = False
                for lease in leases:
                    try:
                        lab.expect(bao.request(tenant, 'POST', 'sys/leases/revoke', {'lease_id': lease, 'sync': True}, admin)[0], 204,
                                   'remaining fixture lease revoked')
                    except Exception:
                        cleanup_failed = True
                for name in secret_names:
                    try:
                        lab.kube('-n', provider.NAMESPACE, 'delete', 'secret', name, '--ignore-not-found')
                    except Exception:
                        cleanup_failed = True
                try:
                    lab.expect(bao.request(tenant, 'POST', 'auth/token/revoke-self', token=token)[0], 204,
                               'database workload session revoked')
                except Exception:
                    cleanup_failed = True
                if cleanup_failed:
                    raise RuntimeError('Database fixture cleanup did not complete') from None


def database_tls(namespace, service):
    """Create a disposable CA and service certificate, keeping its private key in the database namespace."""
    import subprocess
    import tempfile
    existing = lab.kube('-n', namespace, 'get', 'secret', 'db-tls', '--ignore-not-found', '-o', 'name')
    if existing:
        values = lab.read_secret('db-tls', namespace)
    else:
        with tempfile.TemporaryDirectory(prefix='obco-db-tls-') as temporary:
            directory = Path(temporary)
            names = (service, service + '.' + namespace, service + '.' + namespace + '.svc',
                     service + '.' + namespace + '.svc.cluster.local')
            (directory / 'extensions').write_text('subjectAltName=' + ','.join('DNS:' + name for name in names) + '\nextendedKeyUsage=serverAuth\n')
            commands = [
                ['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '2', '-subj', '/CN=Disposable database lab CA', '-addext', 'basicConstraints=critical,CA:TRUE', '-addext', 'keyUsage=critical,keyCertSign,cRLSign', '-keyout', 'ca.key', '-out', 'ca.crt'],
                ['openssl', 'req', '-newkey', 'rsa:2048', '-nodes', '-subj', '/CN=' + names[2], '-keyout', 'tls.key', '-out', 'server.csr'],
                ['openssl', 'x509', '-req', '-in', 'server.csr', '-CA', 'ca.crt', '-CAkey', 'ca.key', '-CAcreateserial', '-days', '2', '-extfile', 'extensions', '-out', 'tls.crt'],
            ]
            for command in commands:
                result = subprocess.run(command, cwd=directory, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=30)
                if result.returncode:
                    raise RuntimeError('Disposable database certificate generation failed')
            values = {name: (directory / name).read_text() for name in ('ca.crt', 'tls.crt', 'tls.key')}
        lab.apply([lab.secret('db-tls', namespace, values)])
    lab.apply([lab.obj('ConfigMap', 'db-ca', namespace, data={'ca.crt': values['ca.crt']})])
    return values


def mount_bao_ca(name, certificate, extra_env=None):
    # PostgreSQL's pgx driver accepts a CA file path on the OpenBao server, not an inline PEM.
    statefulset = json.loads(lab.kube('-n', 'openbao', 'get', 'statefulset', 'openbao', '-o', 'json'))
    owner = statefulset['metadata'].get('labels', {}).get('lab.openbaoconfig.io/cluster')
    if owner != lab.CONTEXT.removeprefix('kind-'):
        raise RuntimeError('Refusing to mount a database CA into an unowned OpenBao fixture')
    lab.apply([lab.obj('ConfigMap', 'database-' + name + '-ca', 'openbao', data={'ca.crt': certificate})])
    patch = {'spec': {'template': {'spec': {
        'volumes': [{'name': 'database-' + name + '-ca', 'configMap': {'name': 'database-' + name + '-ca'}}],
        'containers': [{'name': 'openbao', 'volumeMounts': [{'name': 'database-' + name + '-ca',
                        'mountPath': '/database-ca/' + name, 'readOnly': True}]}]}}}}
    if extra_env:
        patch['spec']['template']['spec']['containers'][0]['env'] = extra_env
    lab.kube('-n', 'openbao', 'patch', 'statefulset', 'openbao', '--type=strategic', '-p', json.dumps(patch))
    lab.kube('-n', 'openbao', 'rollout', 'status', 'statefulset/openbao', '--timeout=300s')
    lab.kube('-n', 'openbao', 'wait', 'pod', '-l', 'openbao-active=true', '--for=condition=Ready', '--timeout=120s')


def load_manifest(path):
    import subprocess
    result = subprocess.run([str(ROOT / 'bin/yq'), '-o=json', '.', str(path)], stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, text=True, timeout=30)
    if result.returncode:
        raise RuntimeError('Database fixture manifest could not be loaded')
    return json.loads(result.stdout)
