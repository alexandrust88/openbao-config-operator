"""Transit fixture isolation and credential-free workload Job contracts."""
import json
import http.server
import threading
import ssl
import urllib.request
import os
from pathlib import Path
import re
import unittest
from unittest.mock import Mock, patch

import transit


class TransitTests(unittest.TestCase):
    def test_operator_keys_are_nonexportable_by_default_and_workload_policy_is_key_scoped(self):
        for tenant in transit.TENANTS:
            values = transit.objects(tenant)
            keys = [value for value in values if value['kind'] == 'TransitKey']
            self.assertEqual({value['spec']['name'] for value in keys}, {'app', 'migration', 'other', 'orphan', 'guard', 'signer', 'mac', 'derived'} | {'alg-' + algorithm for algorithm in transit.ALGORITHMS})
            for key in keys:
                self.assertFalse(key['spec'].get('exportable', False))
                self.assertFalse(key['spec'].get('allowPlaintextBackup', False))
                self.assertEqual(key['spec'].get('openbaoNamespace'), None if tenant == 'root' else 'lab-alpha')
            policy = next(value['spec']['policy'] for value in values if value['kind'] == 'ACLPolicy')
            self.assertNotIn('*', policy)
            self.assertNotIn('/other', policy)
            self.assertNotIn('/export/', policy)
            self.assertNotIn('/backup/', policy)
            self.assertIn('auth/token/revoke-self', policy)
            role = next(value['spec'] for value in values if value['kind'] == 'KubernetesAuthRole')
            self.assertEqual(role['boundServiceAccountNamespaces'], ['obco-transit-' + tenant])
            self.assertTrue(role['tokenNoDefaultPolicy'])

    def test_application_job_uses_audience_bound_projected_identity_and_no_static_token(self):
        with patch.dict(os.environ, {'TRANSIT_APP_IMAGE': 'example.com/app:unique'}), patch.object(transit.lab, 'apply') as apply, patch.object(transit.lab, 'kube', return_value='{"ok":true}'):
            transit.application_job('alpha')
        job = apply.call_args.args[0][0]
        pod = job['spec']['template']['spec']
        self.assertFalse(pod['automountServiceAccountToken'])
        self.assertEqual(pod['serviceAccountName'], 'transit-app')
        self.assertIn('--migration-key', pod['containers'][0]['args'])
        self.assertIn('migration', pod['containers'][0]['args'])
        projection = next(volume['projected'] for volume in pod['volumes'] if volume['name'] == 'jwt')
        self.assertEqual(projection['sources'][0]['serviceAccountToken']['audience'], 'openbao')
        env = {item['name']: item['value'] for item in pod['containers'][0]['env']}
        self.assertEqual(env['BAO_NAMESPACE'], 'lab-alpha')
        self.assertNotIn('BAO_TOKEN', env)
        self.assertTrue(env['BAO_ADDR'].startswith('https://'))
        self.assertNotIn('Secret', json.dumps(job))

    def test_up_reuses_the_identity_lab_namespace_owner(self):
        ca = json.dumps({'data': {'ca.crt': 'synthetic-ca'}})
        with patch.object(transit.lab, 'kube', return_value=ca), patch.object(transit.lab, 'apply') as apply, patch.object(transit.lab, 'wait_ready'):
            transit.up()
        owners = [item for call in apply.call_args_list for item in call.args[0] if item['kind'] == 'BaoNamespace']
        self.assertEqual(owners, [transit.lab.cr('BaoNamespace', 'lab-alpha', {'name': 'lab-alpha'})])

    def test_algorithm_matrix_covers_the_crd_enum_except_external_keys(self):
        source = (Path(__file__).resolve().parents[2] / 'api/secrets/v1alpha1/transitkey_types.go').read_text()
        enum = set(re.search(r'Enum=([^\n]+)', source).group(1).split(';'))
        self.assertEqual(set(transit.ALGORITHMS), enum - {'external-key'})
        self.assertEqual(len(transit.ALGORITHMS), 15)

    def test_shared_bao_opener_cannot_follow_redirect_with_session_token(self):
        seen = []

        class Redirect(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                seen.append(self.path)
                self.send_response(302)
                self.send_header('Location', '/should-not-receive-token')
                self.end_headers()

            def log_message(self, *_):
                pass

        server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Redirect)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            ca = json.dumps({'data': {'ca.crt': 'synthetic-ca'}})
            with patch.object(transit.lab, 'kube', return_value=ca), patch.object(transit.lab.ssl, 'create_default_context', return_value=ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)):
                opener = transit.lab.Bao(server.server_port).opener
            request = urllib.request.Request('http://127.0.0.1:' + str(server.server_port) + '/initial',
                                             headers={'X-Vault-Token': 'synthetic-unit-fixture'})
            with self.assertRaisesRegex(RuntimeError, 'refused'):
                opener.open(request, timeout=2)
            self.assertEqual(seen, ['/initial'])
        finally:
            server.shutdown()
            server.server_close()
            thread.join(timeout=2)

    def test_nonexportability_requires_specific_refusal_and_no_key_material(self):
        export = (400, {'errors': ['private key material is not exportable']})
        backup = (500, {'errors': ['1 error occurred:\n\t* exporting is disallowed on the policy\n\n']})
        bao = Mock()
        bao.request.side_effect = [export, backup]
        transit.check_nonexportability(bao, 'root', 'synthetic-unit-fixture')
        for bad in ((500, {'errors': ['unrelated server failure']}),
                    (500, {'errors': backup[1]['errors'], 'data': {'backup': 'unexpected'}})):
            with self.subTest(response=bad):
                bao.request.side_effect = [export, bad]
                with self.assertRaisesRegex(RuntimeError, 'expected material-free refusal'):
                    transit.check_nonexportability(bao, 'root', 'synthetic-unit-fixture')

    def test_redirects_fail_closed(self):
        with self.assertRaisesRegex(RuntimeError, 'refused'):
            transit.lab.NoBaoRedirect().redirect_request(None, None, 302, '', {}, 'https://elsewhere.invalid')


if __name__ == '__main__':
    unittest.main()
