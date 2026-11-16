"""Offline provisioning and role-claim contracts for the disposable ZITADEL fixture."""
import json
import base64
import io
import subprocess
import unittest
from unittest.mock import patch

import zitadel


class FakeAPI:
    def __init__(self):
        self.calls = []

    def call(self, method, path, data=None):
        self.calls.append((method, path, data))
        if method == 'GET' and path == '/admin/v1/policies/login':
            return {'policy': {'allowUsernamePassword': True}}
        number = str(len(self.calls))
        return {'id': number, 'userId': number, 'clientId': 'client-' + number, 'clientSecret': 'fake-test-value'}


class ZitadelTests(unittest.TestCase):
    def test_complete_matrix_uses_roles_and_real_service_users(self):
        api = FakeAPI()
        state = zitadel.provision(api)
        self.assertNotEqual(state['project_id'], state['machine_project_id'])
        self.assertEqual(len(state['users']), 9)
        self.assertEqual(len(state['machine_users']), 9)
        self.assertEqual({u['group'] for u in state['users']}, {u['group'] for u in state['machine_users']})
        grants = [body for method, path, body in api.calls if path.endswith('/grants')]
        self.assertEqual(len(grants), 18)
        self.assertTrue(all(len(body['roleKeys']) == 1 and body['projectId'] in {state['project_id'], state['machine_project_id']} for body in grants))
        humans = [body for _, path, body in api.calls if path == '/v2/users/human']
        self.assertTrue(all(u['email']['isVerified'] and not u['password']['changeRequired'] for u in humans))
        app = next(body for _, path, body in api.calls if path.endswith('/apps/oidc'))
        self.assertEqual(app['redirectUris'], [zitadel.lab.CALLBACK])
        self.assertEqual(app['authMethodType'], 'OIDC_AUTH_METHOD_TYPE_BASIC')
        self.assertEqual(app['grantTypes'], ['OIDC_GRANT_TYPE_AUTHORIZATION_CODE'])
        actions = [body for _, path, body in api.calls if path == '/management/v1/actions']
        self.assertEqual(len({a['name'] for a in actions}), 2)
        self.assertTrue(all(not action['allowedToFail'] for action in actions))

    def test_password_only_policy_is_configured_before_creating_users(self):
        api = FakeAPI()
        zitadel.provision(api)
        self.assertEqual(api.calls[0][:2], ('GET', '/admin/v1/policies/login'))
        self.assertEqual([call[0] for call in api.calls[1:4]], ['DELETE'] * 3)
        self.assertTrue(all(call[1].startswith('/admin/v1/policies/login/') for call in api.calls[1:4]))
        api = FakeAPI()
        with patch.object(api, 'call', return_value={'policy': {'allowUsernamePassword': True, 'forceMfa': True}}) as call:
            with self.assertRaises(RuntimeError):
                zitadel.configure_password_only_fixture(api)
            self.assertEqual(call.call_count, 1)
        self.assertEqual(zitadel.ISSUER, 'http://zitadel.obco-lab-zitadel.svc:8080')

    def test_action_flattens_only_owned_project_and_known_roles(self):
        script = zitadel.action_script('owned', 'client')
        fixture = {'v1': {'user': {'grants': {'grants': [
            {'projectId': 'owned', 'roles': ['alpha-reads', 'alpha-reads', 'administrator']},
            {'projectId': 'foreign', 'roles': ['root-admins']}]}}}}
        code = script + '\nconst ctx=' + json.dumps(fixture) + ''';
ctx.v1.application={getClientId:()=>process.argv[1]};
let value=null;obcoGroups(ctx,{v1:{claims:{setClaim:(name,v)=>value=v}}});
process.stdout.write(JSON.stringify(value));'''
        for client, expected in [('client', ['alpha-reads']), ('other', None)]:
            result = subprocess.run(['node', '-e', code, client], text=True, capture_output=True, check=True)
            self.assertEqual(json.loads(result.stdout), expected)

    def test_random_secrets_are_not_in_configmap_or_workload_literals(self):
        values = {'masterkey': 'a' * 32, 'database': 'test-private-value'}
        resources = zitadel.resources(values)
        for obj in resources:
            if obj['kind'] != 'Secret':
                self.assertNotIn(values['database'], json.dumps(obj))
        self.assertTrue(all(obj['metadata'].get('namespace') in (None, zitadel.NAMESPACE) for obj in resources))

    def test_password_complexity_and_uniqueness(self):
        values = {zitadel.password() for _ in range(20)}
        self.assertEqual(len(values), 20)
        self.assertTrue(all(len(v) == 36 for v in values))

    def test_loopback_api_disables_environment_proxy(self):
        with patch.object(zitadel.urllib.request, 'build_opener') as build:
            api = zitadel.API(12345, 'fake-bootstrap-token\n')
            self.assertEqual(api.token, 'fake-bootstrap-token')
            self.assertEqual(build.call_args.args[0].proxies, {})

    def test_machine_exchange_uses_basic_and_checks_subject(self):
        machine = {'namespace': 'root', 'role': 'read', 'client_id': 'machine',
                   'client_secret': 'fake', 'subject': 'service-user'}
        current = {'machine_users': [machine], 'machine_project_id': 'machine-project',
                   'users': [{'subject': 'human-user'}]}
        def encoded(subject):
            body = base64.urlsafe_b64encode(json.dumps({'sub': subject}).encode()).rstrip(b'=').decode()
            return json.dumps({'access_token': 'header.' + body + '.signature'}).encode()
        with patch.object(zitadel.urllib.request, 'build_opener') as build:
            build.return_value.open.return_value = io.BytesIO(encoded('service-user'))
            self.assertEqual(len(zitadel.machine_tokens(current, 12345)), 1)
            self.assertEqual(build.call_args.args[0].proxies, {})
            request = build.return_value.open.call_args.args[0]
            self.assertIn(b'projects%3Aroles', request.data)
            self.assertNotIn(b'client_id', request.data)
            self.assertNotIn(b'client_secret', request.data)
            self.assertTrue(request.headers['Authorization'].startswith('Basic '))
            build.return_value.open.return_value = io.BytesIO(encoded('human-user'))
            with self.assertRaises(RuntimeError):
                zitadel.machine_tokens(current, 12345)

    def test_redirect_refuses_forwarding(self):
        with self.assertRaises(RuntimeError):
            zitadel.NoRedirect().redirect_request(None, None, 302, None, None, 'https://other.test')


if __name__ == '__main__':
    unittest.main()
