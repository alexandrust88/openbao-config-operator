"""Database provider contracts and strict protocol failure classifications."""
import ast
import contextlib
import io
import http.server
import threading
import urllib.request
import json
import os
from pathlib import Path
import secrets
import unittest
import urllib.error
from unittest.mock import Mock, patch

from databases import cassandra, influxdb, valkey


class BuiltinProviderTests(unittest.TestCase):
    def test_influx_private_key_is_owned_by_server_with_strict_permissions(self):
        with patch('databases.provider_utils.admin_secret'), patch('databases.provider_utils.deploy') as deploy, \
             patch.object(influxdb, 'probe'), patch.object(influxdb, 'install_bao_trust'), \
             patch.object(influxdb.common, 'database_tls', return_value={'ca.crt': 'public-ca'}):
            influxdb.install()
        settings = deploy.call_args.kwargs
        source = next(volume for volume in settings['volumes'] if volume['name'] == 'tls')
        self.assertEqual(source['secret']['defaultMode'], 0o400)
        init = settings['init_containers'][0]
        self.assertEqual(init['image'], influxdb.IMAGE)
        self.assertIn('chmod 0600', init['command'][-1])
        self.assertIn('chown influxdb:influxdb', init['command'][-1])
        self.assertTrue(settings['mounts'][0]['readOnly'])
        self.assertEqual(settings['mounts'][0]['name'], 'private-tls')
        self.assertEqual(init['securityContext']['capabilities']['add'], ['CHOWN'])

    def test_pins_tenant_scopes_and_tls(self):
        for provider in (cassandra, influxdb, valkey):
            self.assertRegex(provider.IMAGE, r':[^@]+@sha256:[0-9a-f]{64}$')
            compile(provider.PROBE, provider.NAME, 'exec')
            with patch('databases.provider_utils.ca', return_value='ca-fixture'):
                config = provider.connection('root')
            self.assertTrue(config[provider.NAME]['tls'])
            self.assertNotIn('password', config)
            self.assertEqual(config['passwordSecretRef']['key'], 'password')
            root = json.dumps(provider.role('root'))
            child = json.dumps(provider.role('alpha'))
            self.assertNotEqual(root, child)
            self.assertNotIn('ALL KEYSPACES', root)
            with self.assertRaises(ValueError):
                provider.role('untrusted')

    def run_probe(self, provider, mode, **mocks):
        environment = {'MODE': mode, 'TENANT': 'root', 'DB_USERNAME': 'fixture', 'DB_PASSWORD': secrets.token_hex(24)}
        output = io.StringIO()
        with patch.dict(os.environ, environment), contextlib.redirect_stdout(output), contextlib.ExitStack() as stack:
            for target, value in mocks.items():
                stack.enter_context(patch(target, value))
            exec(compile(provider.PROBE, provider.NAME, 'exec'), {})
        self.assertEqual(json.loads(output.getvalue()), {'ok': True})

    def test_valkey_real_resp_read_write_and_cross_key_denials(self):
        sock = Mock()
        sock.makefile.return_value = io.BytesIO(b'+OK\r\n$17\r\nnonsecret-fixture\r\n-NOPERM cannot write\r\n-NOPERM cannot read key\r\n')
        tls = Mock()
        tls.wrap_socket.return_value = sock
        self.run_probe(valkey, 'verify', **{'ssl.create_default_context': Mock(return_value=tls),
                                          'socket.create_connection': Mock(return_value=Mock())})
        self.assertEqual(sock.sendall.call_count, 4)

    def test_valkey_revocation_requires_auth_rejection_not_connection_error(self):
        for response, succeeds in ((b'-WRONGPASS invalid credentials\r\n', True), (b'-ERR server unavailable\r\n', False)):
            sock = Mock()
            sock.makefile.return_value = io.BytesIO(response)
            tls = Mock()
            tls.wrap_socket.return_value = sock
            if succeeds:
                self.run_probe(valkey, 'revoked', **{'ssl.create_default_context': Mock(return_value=tls),
                                                     'socket.create_connection': Mock()})
            else:
                with self.assertRaises(AssertionError):
                    self.run_probe(valkey, 'revoked', **{'ssl.create_default_context': Mock(return_value=tls),
                                                         'socket.create_connection': Mock()})

    def test_influx_revocation_rejects_server_failure(self):
        for status in (401, 500):
            opener = Mock()
            opener.open.side_effect = urllib.error.HTTPError('https://fixture', status, '', {}, io.BytesIO())
            mocks = {'urllib.request.build_opener': Mock(return_value=opener), 'ssl.create_default_context': Mock()}
            if status == 401:
                self.run_probe(influxdb, 'revoked', **mocks)
            else:
                with self.assertRaises(AssertionError):
                    self.run_probe(influxdb, 'revoked', **mocks)

    def test_cassandra_revocation_requires_authentication_failure(self):
        for reason, succeeds in (('AuthenticationFailed: invalid credentials', True), ('Connection refused', False)):
            result = Mock(returncode=1, stderr=reason, stdout='')
            if succeeds:
                self.run_probe(cassandra, 'revoked', **{'subprocess.run': Mock(return_value=result)})
            else:
                with self.assertRaises(AssertionError):
                    self.run_probe(cassandra, 'revoked', **{'subprocess.run': Mock(return_value=result)})

    def test_server_fixtures_enable_tls_and_mount_credentials(self):
        for provider in (influxdb, valkey):
            with patch('databases.provider_utils.admin_secret'), patch('databases.provider_utils.tls'), \
                 patch('databases.provider_utils.deploy') as deploy, patch.object(provider, 'probe'), \
                 patch.object(influxdb, 'install_bao_trust'), \
                 patch.object(influxdb.common, 'database_tls', return_value={'ca.crt': 'public-ca'}):
                provider.install()
            settings = deploy.call_args.kwargs
            self.assertEqual(next(volume for volume in settings['volumes'] if volume['name'] == 'tls')['secret']['secretName'], 'db-tls')
            if provider is influxdb:
                values = {item['name']: item.get('value') for item in settings['env']}
                self.assertEqual(values['INFLUXDB_HTTP_HTTPS_ENABLED'], 'true')
                self.assertEqual(values['INFLUXDB_HTTP_AUTH_ENABLED'], 'true')
            else:
                self.assertIn('port 0', settings['command'][2])
                self.assertIn('tls-port 6379', settings['command'][2])

    def test_cassandra_startup_configuration_script_is_valid_python(self):
        tree = ast.parse(Path(cassandra.__file__).read_text())
        configure = next(node.value.value for node in ast.walk(tree) if isinstance(node, ast.Assign)
                         and any(isinstance(target, ast.Name) and target.id == 'configure' for target in node.targets))
        compile(configure, 'cassandra TLS startup configuration', 'exec')
        self.assertIn('optional: false', configure)
        self.assertIn('store_type: PKCS12', configure)
        self.assertIn('PasswordAuthenticator', configure)
        self.assertIn('CassandraAuthorizer', configure)
        config_file = Mock()
        config_file.read_text.return_value = ('authenticator: AllowAllAuthenticator\n'
                                             'authorizer: AllowAllAuthorizer\n'
                                             'roles_validity: 2s\npermissions_validity: 2s\n'
                                             'credentials_validity: 2s\nclient_encryption_options:\n  enabled: false\n')
        key_file = Mock()
        key_file.read_text.return_value = secrets.token_hex(24)
        with patch('pathlib.Path', side_effect=lambda name: config_file if name.endswith('cassandra.yaml') else key_file):
            exec(compile(configure, 'cassandra configuration', 'exec'), {})
        rendered = config_file.write_text.call_args.args[0]
        self.assertIn('authenticator: PasswordAuthenticator', rendered)
        self.assertIn('authorizer: CassandraAuthorizer', rendered)
        self.assertNotIn('AllowAll', rendered)
        self.assertIn('credentials_validity: 0ms', rendered)
        self.assertIn('optional: false', rendered)


    def test_influx_redirect_never_receives_authorization(self):
        seen = []
        class Handler(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                seen.append(self.path)
                self.send_response(302)
                self.send_header('Location', '/credential-target')
                self.end_headers()
            def log_message(self, *args):
                pass
        server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            node = next(item for item in ast.parse(influxdb.PROBE).body if isinstance(item, ast.ClassDef))
            scope = {'urllib': __import__('urllib')}
            exec(compile(ast.Module(body=[node], type_ignores=[]), 'redirect guard', 'exec'), scope)
            opener = urllib.request.build_opener(scope['NoRedirect'](), urllib.request.ProxyHandler({}))
            request = urllib.request.Request('http://127.0.0.1:' + str(server.server_port) + '/source',
                                             headers={'Authorization': 'Basic synthetic-test-marker'})
            with self.assertRaisesRegex(RuntimeError, 'refused an HTTP redirect'):
                opener.open(request, timeout=5)
            self.assertEqual(seen, ['/source'])
        finally:
            server.shutdown()
            server.server_close()
            thread.join(timeout=5)

    def test_influx_workaround_extends_trust_without_disabling_tls(self):
        from databases import common
        fixture = {'metadata': {'labels': {'lab.obco.dev/cluster': common.lab.CONTEXT.removeprefix('kind-')}},
                   'spec': {'template': {'spec': {'containers': [{'name': 'openbao', 'env': [
                       {'name': 'SSL_CERT_DIR', 'value': '/etc/ssl/certs:/existing-public-ca'}]}]}}}}
        with patch.object(common.lab, 'kube', return_value=json.dumps(fixture)), \
             patch.object(common, 'mount_bao_ca') as mount:
            influxdb.install_bao_trust('public-ca')
        environment = mount.call_args.kwargs['extra_env'][0]
        self.assertEqual(environment, {'name': 'SSL_CERT_DIR',
                         'value': '/etc/ssl/certs:/existing-public-ca:/database-ca/influxdb'})
        self.assertTrue(influxdb.connection('root')['influxdb']['tls'])
        self.assertNotIn('insecureTLS', influxdb.connection('root')['influxdb'])
