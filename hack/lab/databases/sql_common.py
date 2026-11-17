"""TLS-only SQL fixtures and database-native privilege probes."""
from functools import cache
import json
import re
from pathlib import Path

from . import common
from . import provider_utils as util


@cache
def manifest(name):
    return common.load_manifest(Path(__file__).with_name(name + '.yaml'))


def image(name):
    return next(item for item in manifest(name)['items'] if item['kind'] == 'Deployment')['spec']['template']['spec']['containers'][0]['image']


def install(name, mount_ca=True):
    namespace = 'obco-db-' + name
    util.admin_secret(name, namespace, 'openbao' if name == 'postgresql' else 'root')
    certificates = common.database_tls(namespace, name)
    common.lab.apply(manifest(name)['items'])
    common.lab.kube('-n', namespace, 'rollout', 'status', 'deployment/' + name, '--timeout=300s')
    try:
        probe(name, 'root', 'database-admin', 'connect')
    except RuntimeError:
        if name == 'mysql':
            mysql_tls_diagnostic()
        raise
    if name == 'postgresql' and mount_ca:
        common.mount_bao_ca(name, certificates['ca.crt'])


def mysql_tls_diagnostic():
    try:
        logs = common.lab.kube('-n', 'obco-db-mysql', 'logs', 'deployment/mysql', '--tail=300')
    except RuntimeError:
        return
    categories = (
        ('key', r'private key|SSL key'),
        ('certificate', r'certificate|SSL cert'),
        ('ca', r'CA file|verify location|issuer'),
        ('permission', r'Permission denied|access denied'),
        ('protocol', r'protocol|cipher'),
    )
    for line in logs.splitlines():
        if not re.search(r'SSL|TLS|certificate|private key', line, re.IGNORECASE):
            continue
        codes = sorted(set(re.findall(r'\bMY-[0-9]{6}\b', line)))
        found = [name for name, pattern in categories if re.search(pattern, line, re.IGNORECASE)]
        print('MYSQL_TLS_STARTUP codes=' + ','.join(codes or ['unknown']) +
              ' categories=' + ','.join(found or ['unclassified']), flush=True)


def connection(name, tenant):
    util.tenant_database(tenant)
    host = name + '.obco-db-' + name + '.svc'
    if name == 'postgresql':
        return util.connection(name, 'postgresql-database-plugin', {'postgresql': {
            'connectionURL': 'postgresql://{{username}}:{{password}}@' + host + ':5432/app?sslmode=verify-full&sslrootcert=/database-ca/postgresql/ca.crt',
            'username': 'openbao', 'passwordAuthentication': 'scram-sha-256'}})
    ca = json.loads(common.lab.kube('-n', 'obco-db-' + name, 'get', 'configmap', 'db-ca', '-o', 'json'))['data']['ca.crt']
    return util.connection(name, 'mysql-database-plugin', {'mysql': {
        'connectionURL': '{{username}}:{{password}}@tcp(' + host + ':3306)/', 'username': 'root',
        'tlsCA': ca, 'tlsServerName': host, 'tlsSkipVerify': False}})


def role(name, tenant):
    database = util.tenant_database(tenant)
    if name == 'postgresql':
        return {'creationStatements': [
            'CREATE ROLE "{{name}}" WITH LOGIN PASSWORD \'{{password}}\' VALID UNTIL \'{{expiration}}\'; '
            'GRANT USAGE ON SCHEMA ' + database + ' TO "{{name}}"; GRANT SELECT ON ' + database + '.probe TO "{{name}}";'],
            'renewStatements': ['ALTER ROLE "{{name}}" VALID UNTIL \'{{expiration}}\';'],
            'revocationStatements': ['REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA ' + database + ' FROM "{{name}}"; '
                'REVOKE USAGE ON SCHEMA ' + database + ' FROM "{{name}}"; DROP ROLE "{{name}}";']}
    return {'creationStatements': ["CREATE USER '{{name}}'@'%' IDENTIFIED BY '{{password}}' REQUIRE SSL; GRANT SELECT ON " + database + ".probe TO '{{name}}'@'%';"],
            'revocationStatements': ["DROP USER '{{name}}'@'%';"]}


POSTGRES_PROBE = r'''
set -eu
export PGPASSWORD="$DB_PASSWORD" PGUSER="$DB_USERNAME" PGDATABASE=app PGHOST=postgresql.obco-db-postgresql.svc PGCONNECT_TIMEOUT=10 PGSSLMODE=verify-full PGSSLROOTCERT=/ca/ca.crt
query() { psql -X -A -t -v ON_ERROR_STOP=1 -v VERBOSITY=sqlstate -c "$1" >/tmp/result 2>/tmp/error; }
if [ "$MODE" = revoked ]; then
  if query 'SELECT 1'; then exit 1; fi
  grep -q 'password authentication failed' /tmp/error
else
  query "SELECT value FROM lab_${TENANT}.probe WHERE id=1"
  test "$(cat /tmp/result)" = "${TENANT}-fixture"
  query 'SELECT ssl FROM pg_stat_ssl WHERE pid=pg_backend_pid()'
  test "$(cat /tmp/result)" = t
  if [ "$MODE" = verify ]; then
    if query "INSERT INTO lab_${TENANT}.probe VALUES (2, 'forbidden')"; then exit 1; fi
    grep -q '42501' /tmp/error
    if query "SELECT value FROM lab_${OTHER}.probe"; then exit 1; fi
    grep -q '42501' /tmp/error
  fi
fi
printf '{"ok":true}\n'
'''

MYSQL_PROBE = r'''
set -eu
failure() {
  status=$?
  if [ "$status" -ne 0 ]; then
    code=$(grep -Eo 'ERROR [0-9]{4}' /tmp/error 2>/dev/null | head -n 1 || true)
    code=${code#ERROR }
    case "$code" in [0-9][0-9][0-9][0-9]) ;; *) code=unknown ;; esac
    tls=unclassified
    if [ "$code" = 2026 ]; then
      if grep -Eqi 'certificate verify failed|certificate verification|certificate is not trusted|self.signed' /tmp/error; then tls=certificate
      elif grep -Eqi 'identity|hostname|does not match' /tmp/error; then tls=hostname
      elif grep -Eqi 'does not support SSL|SSL is required|not support TLS' /tmp/error; then tls=server-disabled
      elif grep -Eqi 'CA certificate|verify locations|verify paths|No such file|Permission denied' /tmp/error; then tls=ca-file
      elif grep -Eqi 'protocol version|unsupported protocol|no protocols|cipher' /tmp/error; then tls=protocol
      fi
    fi
    printf 'SQL_FAILURE code=%s tls=%s\n' "$code" "$tls" >/dev/termination-log 2>/dev/null || true
  fi
  exit "$status"
}
trap failure EXIT
export MYSQL_PWD="$DB_PASSWORD"
query() {
  if [ "$PROVIDER" = mariadb ]; then
    mariadb --protocol=TCP --host="$DB_HOST" --user="$DB_USERNAME" --connect-timeout=10 --ssl --ssl-verify-server-cert --ssl-ca=/ca/ca.crt --batch --skip-column-names --execute="$1" >/tmp/result 2>/tmp/error
  else
    mysql --protocol=TCP --host="$DB_HOST" --user="$DB_USERNAME" --connect-timeout=10 --ssl-mode=VERIFY_IDENTITY --ssl-ca=/ca/ca.crt --batch --skip-column-names --execute="$1" >/tmp/result 2>/tmp/error
  fi
}
if [ "$MODE" = revoked ]; then
  if query 'SELECT 1'; then exit 1; fi
  grep -q 'ERROR 1045' /tmp/error
else
  query "SELECT value FROM lab_${TENANT}.probe WHERE id=1"
  test "$(cat /tmp/result)" = "${TENANT}-fixture"
  query "SHOW SESSION STATUS LIKE 'Ssl_cipher'"
  read -r variable cipher < /tmp/result
  test "$variable" = Ssl_cipher
  test -n "$cipher"
  if [ "$MODE" = verify ]; then
    if query "INSERT INTO lab_${TENANT}.probe VALUES (2, 'forbidden')"; then exit 1; fi
    grep -q 'ERROR 1142' /tmp/error
    if query "SELECT value FROM lab_${OTHER}.probe"; then exit 1; fi
    grep -q 'ERROR 1142' /tmp/error
  fi
fi
printf '{"ok":true}\n'
'''


def probe(name, tenant, credential_secret_name, mode):
    util.tenant_database(tenant)
    if mode not in ('verify', 'connect', 'revoked'):
        raise ValueError('Unknown SQL probe mode')
    namespace = 'obco-db-' + name
    common.run_probe(image(name), ['sh', '-ec', POSTGRES_PROBE if name == 'postgresql' else MYSQL_PROBE],
        namespace, credential_secret_name, env={'TENANT': tenant, 'OTHER': 'alpha' if tenant == 'root' else 'root',
            'PROVIDER': name, 'DB_HOST': name + '.' + namespace + '.svc', 'MODE': mode},
        volumes=[{'name': 'ca', 'configMap': {'name': 'db-ca'}}],
        mounts=[{'name': 'ca', 'mountPath': '/ca', 'readOnly': True}])
