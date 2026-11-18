#!/usr/bin/env python3
"""Exercise operator-managed Transit keys with real Kubernetes workload identities."""
import argparse
import base64
import json
import os
import secrets
import time

import identity as lab

TENANTS = ('root', 'alpha')
MOUNT = 'lab-transit'
AUTH = 'transit-k8s'
LABEL = {'lab.obco.dev/profile': 'transit'}
ENCRYPTION = ('aes128-gcm96', 'aes256-gcm96', 'chacha20-poly1305', 'xchacha20-poly1305', 'rsa-2048', 'rsa-3072', 'rsa-4096')
SIGNING = ('ecdsa-p256', 'ecdsa-p384', 'ecdsa-p521', 'ed25519', 'rsa-2048', 'rsa-3072', 'rsa-4096', 'mldsa-44', 'mldsa-65', 'mldsa-87')
ALGORITHMS = tuple(dict.fromkeys((*ENCRYPTION, *SIGNING, 'hmac')))


def cr(kind, name, spec, tenant='root'):
    group = 'secrets' if kind == 'TransitKey' else ('auth' if kind.startswith('KubernetesAuth') else 'sys')
    shared = {'connectionRef': {'name': lab.CONNECTION}, 'deletionPolicy': 'Orphan'}
    if tenant != 'root':
        shared['openbaoNamespace'] = 'lab-' + tenant
    return {'apiVersion': group + '.obco.dev/v1alpha1', 'kind': kind,
            'metadata': {'name': 'transit-' + tenant + '-' + name, 'namespace': lab.NAMESPACE, 'labels': LABEL},
            'spec': {**shared, **spec}}


def objects(tenant):
    namespace = 'obco-transit-' + tenant
    algorithm_paths = [MOUNT + '/' + action + '/alg-' + algorithm for algorithm in ALGORITHMS
                       for action in (('encrypt', 'decrypt') if algorithm in ENCRYPTION else ()) +
                       (('sign', 'verify') if algorithm in SIGNING else ()) +
                       (('hmac', 'verify') if algorithm == 'hmac' else ())]
    policy = '\n'.join('path "' + path + '" { capabilities = ["update"] }' for path in (
        MOUNT + '/encrypt/app', MOUNT + '/decrypt/app', MOUNT + '/rewrap/app',
        MOUNT + '/encrypt/migration', MOUNT + '/decrypt/migration',
        MOUNT + '/sign/signer', MOUNT + '/verify/signer', MOUNT + '/hmac/mac', MOUNT + '/verify/mac',
        MOUNT + '/encrypt/derived', MOUNT + '/decrypt/derived', 'auth/token/revoke-self', *algorithm_paths))
    values = [cr('SecretsMount', 'mount', {'path': MOUNT, 'type': 'transit'}, tenant),
              cr('AuthMount', 'auth', {'path': AUTH, 'type': 'kubernetes'}, tenant),
              cr('KubernetesAuthConfig', 'auth', {'mountPath': AUTH, 'kubernetesHost': 'https://kubernetes.default.svc:443'}, tenant),
              cr('ACLPolicy', 'app', {'name': 'transit-app', 'policy': policy}, tenant),
              cr('KubernetesAuthRole', 'app', {'mountPath': AUTH, 'name': 'transit-app',
                 'boundServiceAccountNames': ['transit-app'], 'boundServiceAccountNamespaces': [namespace],
                 'audience': 'openbao', 'tokenPolicies': ['transit-app'], 'tokenTTL': '15m',
                 'tokenMaxTTL': '20m', 'tokenNoDefaultPolicy': True}, tenant)]
    for name, extra in [('app', {'autoRotatePeriod': '1h'}), ('migration', {}), ('other', {}), ('orphan', {}), ('guard', {'deletionPolicy': 'Delete'}),
                        ('signer', {'type': 'ed25519'}), ('mac', {'type': 'hmac', 'keySize': 32}),
                        ('derived', {'derived': True})]:
        values.append(cr('TransitKey', name, {'mountPath': MOUNT, 'name': name, 'type': 'aes256-gcm96', **extra}, tenant))
    for algorithm in ALGORITHMS:
        extra = {'keySize': 32} if algorithm == 'hmac' else {}
        values.append(cr('TransitKey', 'alg-' + algorithm, {'mountPath': MOUNT, 'name': 'alg-' + algorithm, 'type': algorithm, **extra}, tenant))
    return values


def up():
    child = lab.cr('BaoNamespace', 'lab-alpha', {'name': 'lab-alpha'})
    lab.apply([child])
    lab.wait_ready([child])
    ca = json.loads(lab.kube('-n', 'openbao', 'get', 'configmap', 'openbao-ca', '-o', 'json'))['data']
    for tenant in TENANTS:
        namespace = 'obco-transit-' + tenant
        lab.apply([lab.obj('Namespace', namespace, namespace=''), lab.obj('ServiceAccount', 'transit-app', namespace),
                   lab.obj('ConfigMap', 'openbao-ca', namespace, data=ca)])
        values = objects(tenant)
        lab.apply(values)
        lab.wait_ready(values)
    print('Transit configuration Ready: root and child, dedicated workload auth and key-specific ACLs')


def wait(check, label, timeout=120):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if check():
            return
        time.sleep(1)
    raise RuntimeError('Timed out: ' + label)


def patch_key(tenant, name, changes):
    resource = cr('TransitKey', name, {})
    resource['metadata']['name'] = 'transit-' + tenant + '-' + name
    lab.kube('-n', lab.NAMESPACE, 'patch', 'transitkey', resource['metadata']['name'], '--type=merge',
             '-p', json.dumps({'spec': changes}))
    lab.wait_ready([resource])


def check_crypto(bao, tenant, token):
    plain = base64.b64encode(b'nonsecret transit application fixture').decode()
    call = lambda operation, data: bao.request(tenant, 'POST', MOUNT + '/' + operation, data, token)
    status, result = call('sign/signer', {'input': plain})
    lab.expect(status, 200, 'Ed25519 signing')
    signature = result['data']['signature']
    status, result = call('verify/signer', {'input': plain, 'signature': signature})
    lab.expect(status, 200, 'Ed25519 verification')
    if result['data'].get('valid') is not True:
        raise RuntimeError('Ed25519 verification failed')
    status, result = call('hmac/mac', {'input': plain})
    lab.expect(status, 200, 'HMAC generation')
    status, result = call('verify/mac', {'input': plain, 'hmac': result['data']['hmac']})
    lab.expect(status, 200, 'HMAC verification')
    if result['data'].get('valid') is not True:
        raise RuntimeError('HMAC verification failed')
    tampered = base64.b64encode(b'different synthetic input').decode()
    for algorithm in ALGORITHMS:
        key = 'alg-' + algorithm
        if algorithm in ENCRYPTION:
            status, encrypted = call('encrypt/' + key, {'plaintext': plain})
            lab.expect(status, 200, algorithm + ' encrypt')
            status, decrypted = call('decrypt/' + key, {'ciphertext': encrypted['data']['ciphertext']})
            lab.expect(status, 200, algorithm + ' decrypt')
            if decrypted['data']['plaintext'] != plain:
                raise RuntimeError(algorithm + ' plaintext mismatch')
        if algorithm in SIGNING or algorithm == 'hmac':
            operation, field = ('hmac', 'hmac') if algorithm == 'hmac' else ('sign', 'signature')
            status, signed = call(operation + '/' + key, {'input': plain})
            lab.expect(status, 200, algorithm + ' signing')
            for payload, expected in ((plain, True), (tampered, False)):
                status, verified = call('verify/' + key, {'input': payload, field: signed['data'][field]})
                lab.expect(status, 200, algorithm + ' verification')
                if verified['data'].get('valid') is not expected:
                    raise RuntimeError(algorithm + ' verification result incorrect')
    context = base64.b64encode(b'customer-A').decode()
    status, result = call('encrypt/derived', {'plaintext': plain, 'context': context})
    lab.expect(status, 200, 'derived encrypt')
    ciphertext = result['data']['ciphertext']
    status, result = call('decrypt/derived', {'ciphertext': ciphertext, 'context': context})
    lab.expect(status, 200, 'derived decrypt')
    if result['data']['plaintext'] != plain:
        raise RuntimeError('Derived decrypt mismatch')
    lab.expect(call('decrypt/derived', {'ciphertext': ciphertext, 'context': base64.b64encode(b'customer-B').decode()})[0], 400, 'wrong derivation context denied')
    batch_plaintexts = [plain, base64.b64encode(b'distinct second batch payload').decode()]
    status, result = call('encrypt/app', {'batch_input': [{'plaintext': value} for value in batch_plaintexts]})
    lab.expect(status, 200, 'batch encrypt')
    encrypted = result['data']['batch_results']
    if len(encrypted) != 2 or any(item.get('error') for item in encrypted):
        raise RuntimeError('Batch encryption item failed')
    status, result = call('decrypt/app', {'batch_input': [{'ciphertext': item['ciphertext']} for item in encrypted]})
    lab.expect(status, 200, 'batch decrypt')
    if [item.get('plaintext') for item in result['data']['batch_results']] != batch_plaintexts:
        raise RuntimeError('Batch decrypt mismatch')

    prefix, encoded = encrypted[0]['ciphertext'].rsplit(':', 1)
    damaged = bytearray(base64.b64decode(encoded))
    damaged[-1] ^= 1
    tampered_ciphertext = prefix + ':' + base64.b64encode(damaged).decode()
    lab.expect(call('decrypt/app', {'ciphertext': tampered_ciphertext})[0], 400, 'tampered ciphertext denied')
    status, mixed = call('decrypt/app', {'batch_input': [{'ciphertext': encrypted['ciphertext']} for encrypted in encrypted[:1]] + [{'ciphertext': 'invalid'}]})
    lab.expect(status, 400, 'mixed batch reports item failure')
    results = mixed['data']['batch_results']
    if len(results) != 2 or results[0].get('plaintext') != plain or not results[1].get('error'):
        raise RuntimeError('Mixed batch did not preserve successful item and report failed item')

def check_nonexportability(bao, tenant, admin):
    # OpenBao 2.7.2 backup propagates a plain Go error as HTTP 500; require its precise refusal.
    cases = (('export/encryption-key/app', 400, 'private key material is not exportable'),
             ('backup/app', 500, '1 error occurred:\n\t* exporting is disallowed on the policy\n\n'))
    for path, expected_status, message in cases:
        status, result = bao.request(tenant, 'GET', MOUNT + '/' + path, token=admin)
        lab.expect(status, expected_status, 'server key nonexportability: ' + path)
        if result.get('errors') != [message] or result.get('data'):
            raise RuntimeError('Key nonexportability did not return the expected material-free refusal')


def lifecycle(bao, tenant, token, admin):
    plain = base64.b64encode(b'nonsecret rotation fixture').decode()
    status, encrypted = bao.request(tenant, 'POST', MOUNT + '/encrypt/app', {'plaintext': plain}, token)
    lab.expect(status, 200, 'pre-rotation encrypt')
    old = encrypted['data']['ciphertext']
    status, metadata = bao.request(tenant, 'GET', MOUNT + '/keys/app', token=admin)
    lab.expect(status, 200, 'key metadata')
    previous = metadata['data']['latest_version']
    if metadata['data'].get('exportable') or metadata['data'].get('allow_plaintext_backup'):
        raise RuntimeError('Default key material unexpectedly exportable')
    check_nonexportability(bao, tenant, admin)
    if metadata['data'].get('auto_rotate_period') != 3600:
        raise RuntimeError('Automatic rotation period was not persisted')
    nonce = secrets.token_hex(8)
    patch_key(tenant, 'app', {'rotateNonce': nonce})
    status, metadata = bao.request(tenant, 'GET', MOUNT + '/keys/app', token=admin)
    lab.expect(status, 200, 'rotated key metadata')
    latest = metadata['data']['latest_version']
    if latest != previous + 1:
        raise RuntimeError('Operator rotateNonce did not create exactly one version')
    patch_key(tenant, 'app', {'rotateNonce': nonce, 'minEncryptionVersion': latest})
    status, unchanged = bao.request(tenant, 'GET', MOUNT + '/keys/app', token=admin)
    lab.expect(status, 200, 'unchanged rotation nonce metadata')
    if unchanged['data']['latest_version'] != latest:
        raise RuntimeError('Unchanged rotateNonce rotated again')
    lab.expect(bao.request(tenant, 'POST', MOUNT + '/encrypt/app', {'plaintext': plain, 'key_version': previous}, token)[0], 400, 'old encryption version denied')
    status, decrypted = bao.request(tenant, 'POST', MOUNT + '/decrypt/app', {'ciphertext': old}, token)
    lab.expect(status, 200, 'old ciphertext remains decryptable')
    if decrypted['data']['plaintext'] != plain:
        raise RuntimeError('Old ciphertext changed after rotation')
    status, wrapped = bao.request(tenant, 'POST', MOUNT + '/rewrap/app', {'ciphertext': old}, token)
    lab.expect(status, 200, 'rewrap')
    new = wrapped['data']['ciphertext']
    if not new.startswith('vault:v' + str(latest) + ':'):
        raise RuntimeError('Rewrap did not use latest version')
    patch_key(tenant, 'app', {'minDecryptionVersion': latest})
    try:
        lab.expect(bao.request(tenant, 'POST', MOUNT + '/decrypt/app', {'ciphertext': old}, token)[0], 400, 'old version floor denied')
        status, decrypted = bao.request(tenant, 'POST', MOUNT + '/decrypt/app', {'ciphertext': new}, token)
        lab.expect(status, 200, 'rewrapped version permitted')
        if decrypted['data']['plaintext'] != plain:
            raise RuntimeError('Rewrapped plaintext mismatch')
    finally:
        patch_key(tenant, 'app', {'minDecryptionVersion': 1, 'minEncryptionVersion': 0})
    for key in ('orphan', 'guard'):
        name = 'transit-' + tenant + '-' + key
        lab.kube('-n', lab.NAMESPACE, 'delete', 'transitkey', name, '--wait=false')
        if key == 'guard':
            def guarded():
                value = json.loads(lab.kube('-n', lab.NAMESPACE, 'get', 'transitkey', name, '-o', 'json'))
                return any(c.get('reason') == 'DeleteGuarded' for c in value.get('status', {}).get('conditions', []))
            wait(guarded, 'destructive delete guarded')
            lab.expect(bao.request(tenant, 'GET', MOUNT + '/keys/' + key, token=admin)[0], 200, 'guarded key retained')
            lab.kube('-n', lab.NAMESPACE, 'annotate', 'transitkey', name, 'obco.dev/allow-destructive-delete=true')
        lab.kube('-n', lab.NAMESPACE, 'wait', '--for=delete', 'transitkey/' + name, '--timeout=120s')
        lab.expect(bao.request(tenant, 'GET', MOUNT + '/keys/' + key, token=admin)[0], 404 if key == 'guard' else 200, 'remote deletion policy')
        if key == 'orphan':
            # Retention has been proven. Explicitly remove this test-created key so the lab can be rerun.
            lab.expect(bao.request(tenant, 'POST', MOUNT + '/keys/orphan/config', {'deletion_allowed': True}, admin)[0], 200, 'fixture orphan cleanup enabled')
            lab.expect(bao.request(tenant, 'DELETE', MOUNT + '/keys/orphan', token=admin)[0], 204, 'fixture orphan cleanup')
    print(tenant + ': rotation, rewrap, version floor, nonexportability and guarded/orphan deletion passed')


def application_job(tenant):
    image = os.environ['TRANSIT_APP_IMAGE']
    namespace = 'obco-transit-' + tenant
    name = 'transit-app-' + secrets.token_hex(4)
    env = {'BAO_ADDR': 'https://openbao-active.openbao.svc:8200',
           'BAO_NAMESPACE': '' if tenant == 'root' else 'lab-' + tenant,
           'BAO_AUTH_MOUNT': AUTH, 'BAO_AUTH_ROLE': 'transit-app', 'BAO_TRANSIT_MOUNT': MOUNT,
           'BAO_CA_FILE': '/bao-ca/ca.crt', 'BAO_JWT_FILE': '/identity/token'}
    job = lab.obj('Job', name, namespace, api='batch/v1', spec={
        'backoffLimit': 0, 'activeDeadlineSeconds': 120,
        'template': {'metadata': {'labels': LABEL}, 'spec': {
            'restartPolicy': 'Never', 'serviceAccountName': 'transit-app', 'automountServiceAccountToken': False,
            'securityContext': {'runAsNonRoot': True, 'runAsUser': 65532, 'runAsGroup': 65532, 'seccompProfile': {'type': 'RuntimeDefault'}},
            'containers': [{'name': 'app', 'image': image, 'imagePullPolicy': 'Never',
                'args': ['smoke', '--key', 'app', '--migration-key', 'migration', '--denied-key', 'other', '--denied-namespace', 'lab-alpha' if tenant == 'root' else 'root'],
                'env': [{'name': key, 'value': value} for key, value in env.items()],
                'resources': {'requests': {'cpu': '10m', 'memory': '16Mi'}, 'limits': {'memory': '64Mi'}},
                'securityContext': {'allowPrivilegeEscalation': False, 'readOnlyRootFilesystem': True, 'capabilities': {'drop': ['ALL']}},
                'volumeMounts': [{'name': 'ca', 'mountPath': '/bao-ca', 'readOnly': True}, {'name': 'jwt', 'mountPath': '/identity', 'readOnly': True}]}],
            'volumes': [{'name': 'ca', 'configMap': {'name': 'openbao-ca'}},
                        {'name': 'jwt', 'projected': {'sources': [{'serviceAccountToken': {'path': 'token', 'audience': 'openbao', 'expirationSeconds': 600}}]}}]}}})
    lab.apply([job])
    try:
        lab.kube('-n', namespace, 'wait', '--for=condition=Complete', 'job/' + name, '--timeout=150s')
        output = json.loads(lab.kube('-n', namespace, 'logs', 'job/' + name))
        if output != {'ok': True}:
            raise RuntimeError('Transit application did not report successful smoke verification')
        print(tenant + ': real projected-ServiceAccount application Job passed encryption and ACL denials')
    finally:
        lab.kube('-n', namespace, 'delete', 'job', name, '--wait=false')


def test():
    admin = lab.read_secret('openbao-e2e', 'openbao')['token']
    with lab.forward('openbao', 'openbao-active', 8200) as port:
        bao = lab.Bao(port)
        for tenant in TENANTS:
            application_job(tenant)
            jwt = lab.kube('-n', 'obco-transit-' + tenant, 'create', 'token', 'transit-app', '--audience=openbao', '--duration=15m').strip()
            status, response = bao.request(tenant, 'POST', 'auth/' + AUTH + '/login', {'role': 'transit-app', 'jwt': jwt})
            lab.expect(status, 200, 'Transit workload login')
            token = response['auth']['client_token']
            try:
                check_crypto(bao, tenant, token)
                lifecycle(bao, tenant, token, admin)
            finally:
                lab.expect(bao.request(tenant, 'POST', 'auth/token/revoke-self', token=token)[0], 204, 'workload token revoked')
    print('Transit selected algorithm and lifecycle matrix passed')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=['up', 'test'])
    try:
        {'up': up, 'test': test}[parser.parse_args().command]()
    except (RuntimeError, KeyError, ValueError) as error:
        raise SystemExit(str(error) if isinstance(error, RuntimeError) else 'Transit fixture response invalid') from None
