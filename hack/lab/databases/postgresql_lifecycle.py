"""Isolated PostgreSQL root-password and scheduled static-password rotation proof."""
from datetime import datetime
import secrets
import time

from . import common, postgresql, provider_utils, sql_common

PERIOD = 60
STABLE_PERIOD = 3600

ADMIN_PROBE = r'''
set -eu
export PGPASSWORD="$DB_PASSWORD" PGUSER="$DB_USERNAME" PGDATABASE=app PGHOST=postgresql.obco-db-postgresql.svc PGCONNECT_TIMEOUT=10 PGSSLMODE=verify-full PGSSLROOTCERT=/ca/ca.crt
if [ "$MODE" = bootstrap ]; then
  admin_password=$(cat /lifecycle/password)
  static_password=$(cat /lifecycle/static-password)
  psql -X -v ON_ERROR_STOP=1 >/tmp/result 2>/tmp/error <<SQL
CREATE ROLE "$ADMIN_USER" WITH LOGIN CREATEROLE PASSWORD '$admin_password';
GRANT USAGE ON SCHEMA lab_${TENANT} TO "$ADMIN_USER" WITH GRANT OPTION;
GRANT SELECT ON lab_${TENANT}.probe TO "$ADMIN_USER" WITH GRANT OPTION;
CREATE ROLE "$STATIC_USER" WITH LOGIN PASSWORD '$static_password';
GRANT USAGE ON SCHEMA lab_${TENANT} TO "$STATIC_USER";
GRANT SELECT ON lab_${TENANT}.probe TO "$STATIC_USER";
GRANT "$STATIC_USER" TO "$ADMIN_USER" WITH ADMIN OPTION;
SQL
else
  psql -X -v ON_ERROR_STOP=1 >/tmp/result 2>/tmp/error <<SQL
SELECT format('DROP OWNED BY %I',rolname) FROM pg_roles WHERE rolname IN ('$ADMIN_USER','$STATIC_USER') \gexec
SELECT format('DROP ROLE %I',rolname) FROM pg_roles WHERE rolname IN ('$STATIC_USER','$ADMIN_USER') \gexec
SQL
fi
printf '{"ok":true}\n'
'''


def names(tenant):
    provider_utils.tenant_database(tenant)
    suffix = secrets.token_hex(5)
    return 'pg-life-' + tenant + '-' + suffix, 'obco_rotate_' + tenant + '_' + suffix, 'obco_static_' + tenant + '_' + suffix


def resource(tenant, kind, name, spec):
    base = {'connectionRef': {'name': common.lab.CONNECTION}}
    if tenant != 'root':
        base['openbaoNamespace'] = 'lab-' + tenant
    return common.lab.obj(kind, name, common.lab.NAMESPACE,
                          api='secrets.obco.dev/v1alpha1', spec={**base, **spec})


def resources(tenant, name, administrator, static_user):
    connection = postgresql.connection(tenant)
    connection['postgresql']['username'] = administrator
    connection['passwordSecretRef'] = {'name': name, 'key': 'password'}
    base = {'mountPath': common.mount(postgresql), 'deletionPolicy': 'Delete'}
    return [
        resource(tenant, 'DatabaseConnection', name, {
            **base, **connection, 'name': name, 'allowedRoles': [name + '-reader', name + '-static'],
            'rotateRootOnCreate': True}),
        resource(tenant, 'DatabaseRole', name + '-reader', {
            **base, **postgresql.role(tenant), 'name': name + '-reader', 'dbName': name,
            'defaultTTL': '5m', 'maxTTL': '15m'}),
        resource(tenant, 'DatabaseStaticRole', name + '-static', {
            **base, 'name': name + '-static', 'dbName': name, 'username': static_user, 'rotationPeriod': str(STABLE_PERIOD) + 's'}),
    ]


def admin_probe(tenant, name, administrator, static_user, mode):
    volumes, mounts = provider_utils.ca_volume()
    volumes += [{'name': 'lifecycle', 'secret': {'secretName': name}}]
    mounts += [{'name': 'lifecycle', 'mountPath': '/lifecycle', 'readOnly': True}]
    common.run_probe(sql_common.image('postgresql'), ['sh', '-ec', ADMIN_PROBE], postgresql.NAMESPACE,
                     'database-admin', env={'TENANT': tenant, 'ADMIN_USER': administrator,
                                            'STATIC_USER': static_user, 'MODE': mode}, volumes=volumes, mounts=mounts)


def static_data(bao, tenant, name, username, token, period=PERIOD):
    status, response = bao.request(tenant, 'GET', common.mount(postgresql) + '/static-creds/' + name + '-static', token=token)
    common.lab.expect(status, 200, 'read PostgreSQL static credential')
    data = response.get('data', {})
    try:
        # OpenBao 2.7.2 path_creds_create.go uses this wire key, despite its older product name.
        rotated = datetime.fromisoformat(data['last_vault_rotation'].replace('Z', '+00:00'))
    except (KeyError, TypeError, ValueError, AttributeError):
        raise RuntimeError('Static credential has no valid rotation timestamp') from None
    if rotated.tzinfo is None or data.get('username') != username or not isinstance(data.get('password'), str) or not data['password']:
        raise RuntimeError('Static credential identity or password is invalid')
    if data.get('rotation_period') != period:
        raise RuntimeError('Static credential rotation period differs from the CR')
    return data, rotated


def wait_rotation(bao, tenant, name, username, token, initial, timestamp):
    deadline = time.monotonic() + 150
    while time.monotonic() < deadline:
        current, rotated = static_data(bao, tenant, name, username, token)
        if current['password'] != initial['password'] and (rotated - timestamp).total_seconds() >= PERIOD:
            return current
        time.sleep(2)
    raise RuntimeError('Scheduled PostgreSQL static-password rotation did not complete')


def credential_secret(name, values):
    common.lab.apply([common.lab.secret(name, postgresql.NAMESPACE,
                                       {'username': values['username'], 'password': values['password']})])


def cleanup(bao, tenant, token, lease, objects, tenant_names, secret_names):
    name, administrator, static_user = tenant_names
    failed = False
    if lease:
        try:
            common.lab.expect(bao.request(tenant, 'POST', 'sys/leases/revoke', {'lease_id': lease, 'sync': True}, token)[0],
                              204, 'revoke PostgreSQL lifecycle lease')
        except Exception:
            failed = True
    for item in reversed(objects):
        try:
            kind = item['kind'] + '.' + item['apiVersion'].split('/')[0]
            common.lab.kube('-n', common.lab.NAMESPACE, 'delete', kind, item['metadata']['name'],
                            '--ignore-not-found', '--wait=true', '--timeout=90s')
        except Exception:
            failed = True
    try:
        admin_probe(tenant, name, administrator, static_user, 'cleanup')
    except Exception:
        failed = True
    for namespace, secret_name in secret_names:
        try:
            common.lab.kube('-n', namespace, 'delete', 'secret', secret_name, '--ignore-not-found')
        except Exception:
            failed = True
    if failed:
        raise RuntimeError('PostgreSQL lifecycle cleanup did not complete') from None


def test_tenant(bao, tenant, token):
    tenant_names = names(tenant)
    name, administrator, static_user = tenant_names
    values = {'username': administrator, 'password': secrets.token_hex(24), 'static-password': secrets.token_hex(24)}
    objects = resources(tenant, name, administrator, static_user)
    secret_names = [(postgresql.NAMESPACE, name), (common.lab.NAMESPACE, name)]
    lease = None
    try:
        common.lab.apply([common.lab.secret(name, postgresql.NAMESPACE, values),
                          common.lab.secret(name, common.lab.NAMESPACE, {'username': administrator, 'password': values['password']})])
        admin_probe(tenant, name, administrator, static_user, 'bootstrap')
        postgresql.probe(tenant, name, 'connect')
        # Wait for the connection's one-time rotation before adding its roles.
        common.lab.apply(objects[:1])
        common.lab.wait_ready(objects[:1])
        postgresql.probe(tenant, name, 'revoked')
        common.lab.apply(objects[1:2])
        common.lab.wait_ready(objects[1:2])
        status, issued = bao.request(tenant, 'GET', common.mount(postgresql) + '/creds/' + name + '-reader', token=token)
        common.lab.expect(status, 200, 'issuance after PostgreSQL root rotation')
        candidate = issued.get('lease_id')
        if not isinstance(candidate, str) or not candidate.startswith(common.mount(postgresql) + '/creds/' + name + '-reader/'):
            raise RuntimeError('PostgreSQL lifecycle credential has no owned lease')
        lease = candidate
        generated = issued.get('data', {})
        if not isinstance(generated.get('username'), str) or not generated.get('username') or not generated.get('password'):
            raise RuntimeError('PostgreSQL lifecycle credential has no login fields')
        dynamic_secret = name + '-dynamic'
        secret_names.append((postgresql.NAMESPACE, dynamic_secret))
        credential_secret(dynamic_secret, generated)
        postgresql.probe(tenant, dynamic_secret, 'verify')
        common.lab.apply(objects[2:])
        common.lab.wait_ready(objects[2:])
        initial, timestamp = static_data(bao, tenant, name, static_user, token, STABLE_PERIOD)
        if initial['password'] == values['static-password']:
            raise RuntimeError('Static role creation did not rotate its initial password')
        old_secret, new_secret = name + '-static-old', name + '-static-new'
        secret_names.extend([(postgresql.NAMESPACE, old_secret), (postgresql.NAMESPACE, new_secret)])
        credential_secret(old_secret, initial)
        postgresql.probe(tenant, old_secret, 'verify')
        objects[2]['spec']['rotationPeriod'] = str(PERIOD) + 's'
        common.lab.apply(objects[2:])
        common.lab.wait_ready(objects[2:])
        wait_rotation(bao, tenant, name, static_user, token, initial, timestamp)
        objects[2]['spec']['rotationPeriod'] = str(STABLE_PERIOD) + 's'
        common.lab.apply(objects[2:])
        common.lab.wait_ready(objects[2:])
        rotated, final_timestamp = static_data(bao, tenant, name, static_user, token, STABLE_PERIOD)
        if rotated['password'] == initial['password'] or (final_timestamp - timestamp).total_seconds() < PERIOD:
            raise RuntimeError('Static credential lost scheduled-rotation evidence after extending its period')
        credential_secret(new_secret, rotated)
        postgresql.probe(tenant, old_secret, 'revoked')
        postgresql.probe(tenant, new_secret, 'verify')
        print('postgresql/' + tenant + ': isolated root-password rotation and scheduled static-password rotation passed')
    finally:
        cleanup(bao, tenant, token, lease, objects, tenant_names, secret_names)


def test():
    token = common.lab.read_secret('openbao-e2e', 'openbao')['token']
    with common.lab.forward('openbao', 'openbao-active', 8200) as port:
        bao = common.lab.Bao(port)
        for tenant in common.TENANTS:
            test_tenant(bao, tenant, token)
