"""Database fixture transport, privilege and chart contracts."""
import contextlib
import io
import json
import subprocess
import tempfile
from pathlib import Path
import unittest
from unittest.mock import Mock, patch

from databases import common, postgresql, mysql, mariadb, sql_common


class DatabaseTests(unittest.TestCase):
    def test_sql_manifests_pin_images_and_enforce_tls_without_static_credentials(self):
        for provider in (postgresql, mysql, mariadb):
            with self.subTest(provider=provider.NAME):
                values = sql_common.manifest(provider.NAME)
                deployment = next(item for item in values['items'] if item['kind'] == 'Deployment')
                pod = deployment['spec']['template']['spec']
                self.assertFalse(pod['automountServiceAccountToken'])
                self.assertTrue(pod['securityContext']['runAsNonRoot'])
                server = pod['containers'][0]
                self.assertIn('@sha256:', server['image'])
                self.assertIn('ssl=on' if provider.NAME == 'postgresql' else '--require-secure-transport=ON', server['args'])
                passwords = [item for item in server['env'] if item['name'].endswith('PASSWORD')]
                self.assertTrue(passwords)
                self.assertTrue(all('secretKeyRef' in item['valueFrom'] for item in passwords))

    def test_mysql_tls_files_are_owned_by_server_and_private(self):
        deployment = next(item for item in sql_common.manifest('mysql')['items'] if item['kind'] == 'Deployment')
        pod = deployment['spec']['template']['spec']
        server, prepare = pod['containers'][0], pod['initContainers'][0]
        self.assertEqual(pod['securityContext']['runAsUser'], 999)
        self.assertEqual(prepare['image'], server['image'])
        self.assertEqual(prepare['securityContext']['capabilities']['drop'], ['ALL'])
        self.assertFalse(prepare['securityContext']['allowPrivilegeEscalation'])
        self.assertEqual(next(v for v in pod['volumes'] if v['name'] == 'tls')['emptyDir']['medium'], 'Memory')
        self.assertNotIn('tls-source', [v['name'] for v in server['volumeMounts']])
        self.assertTrue(next(v for v in server['volumeMounts'] if v['name'] == 'tls')['readOnly'])
        self.assertEqual(next(v for v in server['volumeMounts'] if v['name'] == 'tls')['mountPath'], '/etc/mysql/tls')
        self.assertIn('--ssl-key=/etc/mysql/tls/tls.key', server['args'])
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source, target = root / 'source', root / 'target'
            source.mkdir()
            target.mkdir()
            for name in ('ca.crt', 'tls.crt', 'tls.key'):
                (source / name).write_text('synthetic-' + name)
                (source / name).chmod(0o640)
            script = prepare['command'][2].replace('/tls-source/', str(source) + '/').replace('/tls/', str(target) + '/')
            subprocess.run(['sh', '-ec', script], check=True, capture_output=True)
            for name in ('ca.crt', 'tls.crt', 'tls.key'):
                self.assertEqual((target / name).read_text(), 'synthetic-' + name)
                self.assertEqual((target / name).stat().st_mode & 0o777, 0o600)
                self.assertEqual((target / name).stat().st_uid, target.stat().st_uid)
            (source / 'tls.key').unlink()
            self.assertNotEqual(subprocess.run(['sh', '-ec', script], capture_output=True).returncode, 0)

    def test_actual_configuration_chart_preserves_tenant_sql_scope(self):
        with patch.object(common.lab, 'kube', return_value=json.dumps({'data': {'ca.crt': 'synthetic-ca'}})):
            for provider in (postgresql, mysql, mariadb):
                for tenant in common.TENANTS:
                    with self.subTest(provider=provider.NAME, tenant=tenant):
                        resources = common.objects(provider, tenant)
                        connection = next(item['spec'] for item in resources if item['kind'] == 'DatabaseConnection')
                        role = next(item['spec'] for item in resources if item['kind'] == 'DatabaseRole')
                        self.assertEqual(connection['allowedRoles'], ['reader'])
                        self.assertEqual(connection.get('namespace'), None if tenant == 'root' else 'lab-alpha')
                        self.assertIn('lab_' + tenant, role['creationStatements'][0])
                        self.assertNotIn('GRANT ALL', role['creationStatements'][0])
                        self.assertIn('passwordSecretRef', connection)
                        if provider.NAME == 'postgresql':
                            self.assertIn('sslmode=verify-full', connection['postgresql']['connectionURL'])
                        else:
                            self.assertFalse(connection['mysql']['tlsSkipVerify'])
                            self.assertEqual(connection['mysql']['tlsCA'], 'synthetic-ca')

    def test_probe_credentials_are_secret_refs_and_tls_mount_has_only_public_ca(self):
        with patch.object(common.lab, 'apply') as apply, patch.object(common.lab, 'kube', return_value='{"ok":true}'):
            sql_common.probe('postgresql', 'alpha', 'generated-fixture', 'verify')
        pod = apply.call_args.args[0][0]['spec']['template']['spec']
        container = pod['containers'][0]
        env = {item['name']: item for item in container['env']}
        self.assertNotIn('value', env['DB_PASSWORD'])
        self.assertEqual(env['DB_PASSWORD']['valueFrom']['secretKeyRef']['name'], 'generated-fixture')
        self.assertFalse(pod['automountServiceAccountToken'])
        ca = next(item for item in pod['volumes'] if item['name'] == 'ca')
        self.assertEqual(ca['configMap'], {'name': 'db-ca'})
        self.assertNotIn('secret', ca)

    def test_cleanup_attempts_remaining_resources_after_probe_and_revoke_failures(self):
        provider = Mock(NAME='fixture', NAMESPACE='fixture-namespace')
        provider.probe.side_effect = RuntimeError('probe failed')
        bao = Mock()
        bao.request.side_effect = [
            (200, {'auth': {'client_token': 'session-fixture'}}), (403, {}),
            *[(200, {'lease_id': 'lab-db-fixture/creds/reader/lease-' + n, 'renewable': True, 'lease_duration': 300,
                     'data': {'username': 'user-' + n, 'password': 'fixture-value-' + n}}) for n in ('1', '2')],
            (500, {}), (204, {}), (204, {}),
        ]
        with patch.object(common.lab, 'read_secret', return_value={'token': 'admin-fixture'}), \
             patch.object(common.lab, 'forward', return_value=contextlib.nullcontext(8200)), \
             patch.object(common.lab, 'Bao', return_value=bao), \
             patch.object(common.lab, 'apply'), \
             patch.object(common.lab, 'kube', side_effect=['jwt-fixture', RuntimeError('delete failed'), '']) as kube:
            with self.assertRaisesRegex(RuntimeError, '^Database fixture cleanup did not complete$'):
                common.test(provider)
        self.assertEqual(bao.request.call_count, 7)
        self.assertEqual(kube.call_count, 3)
        self.assertEqual(bao.request.call_args.args[2], 'auth/token/revoke-self')
        revoked = [call.args[3]['lease_id'] for call in bao.request.call_args_list
                   if call.args[2] == 'sys/leases/revoke']
        self.assertEqual(revoked, ['lab-db-fixture/creds/reader/lease-1', 'lab-db-fixture/creds/reader/lease-2'])

    def test_readiness_diagnostic_prints_categories_not_provider_message(self):
        value = {'metadata': {'generation': 2}, 'status': {'conditions': [
            {'type': 'Synced', 'status': 'False', 'observedGeneration': 2, 'reason': 'ReconcileError',
             'message': 'openbao: status 400: Access denied; synthetic-private-value'}]}}
        output = io.StringIO()
        with patch.object(common.lab, 'kube', return_value=json.dumps(value)), contextlib.redirect_stdout(output):
            common.readiness_failure([{'kind': 'DatabaseConnection', 'metadata': {'name': 'fixture'}}])
        self.assertIn('category=authentication http_status=400', output.getvalue())
        self.assertNotIn('synthetic-private-value', output.getvalue())
        self.assertNotIn('Access denied', output.getvalue())

    def test_probe_failure_reports_only_exact_numeric_sql_marker_and_still_fails(self):
        statuses = [{'state': {'terminated': {'message': value}}} for value in
                    ('SQL_FAILURE code=1045', 'SQL_FAILURE code=2026 tls=certificate',
                     'SQL_FAILURE code=1045 synthetic-private-value', 'SQL_FAILURE code=2026 tls=synthetic-private-value')]

        def kube(*args):
            if 'wait' in args:
                raise RuntimeError('Synthetic failed probe')
            if 'pods' in args:
                return json.dumps({'items': [{'status': {'containerStatuses': statuses}}]})
            return ''

        output = io.StringIO()
        with patch.object(common.lab, 'kube', side_effect=kube) as kubectl, patch.object(common.lab, 'apply'), contextlib.redirect_stdout(output):
            with self.assertRaisesRegex(RuntimeError, 'Synthetic failed probe'):
                common.run_probe('fixture:unit', ['true'], 'fixture', 'fixture-secret')
        self.assertEqual(output.getvalue(), 'SQL_FAILURE code=1045\nSQL_FAILURE code=2026 tls=certificate\n')
        self.assertIn('delete', kubectl.call_args.args)

    def test_mysql_server_diagnostic_exposes_only_codes_and_categories(self):
        logs = '[MY-010295] SSL private key: Permission denied synthetic-private-value\n' + \
               '[MY-010001] unrelated synthetic-private-value\n'
        output = io.StringIO()
        with patch.object(common.lab, 'kube', return_value=logs), contextlib.redirect_stdout(output):
            sql_common.mysql_tls_diagnostic()
        self.assertEqual(output.getvalue(), 'MYSQL_TLS_STARTUP codes=MY-010295 categories=key,permission\n')

    def test_mysql_tls_diagnostic_classifies_without_exposing_error(self):
        cases = {
            'certificate verify failed': 'certificate',
            'hostname does not match': 'hostname',
            'server does not support SSL': 'server-disabled',
            'unable to set verify locations': 'ca-file',
            'unsupported protocol': 'protocol',
            'synthetic-private-value': 'unclassified',
        }
        script = sql_common.MYSQL_PROBE.split('export MYSQL_PWD=', 1)[0] + 'exit 1\n'
        script = script.replace('/tmp/error', './error').replace('/dev/termination-log', './termination')
        for message, category in cases.items():
            with self.subTest(category=category), tempfile.TemporaryDirectory() as directory:
                Path(directory, 'error').write_text('ERROR 2026 (HY000): ' + message + ' synthetic-private-value')
                result = subprocess.run(['sh', '-c', script], cwd=directory, capture_output=True, text=True, check=False)
                self.assertEqual(result.returncode, 1)
                self.assertEqual(result.stdout + result.stderr, '')
                self.assertEqual(Path(directory, 'termination').read_text(),
                                 'SQL_FAILURE code=2026 tls=' + category + '\n')

    def test_bao_ca_mount_refuses_unowned_statefulset(self):
        with patch.object(common.lab, 'kube', return_value='{"metadata":{"labels":{}}}'), patch.object(common.lab, 'apply') as apply:
            with self.assertRaisesRegex(RuntimeError, 'unowned'):
                common.mount_bao_ca('postgresql', 'synthetic-ca')
            apply.assert_not_called()


if __name__ == '__main__':
    unittest.main()
