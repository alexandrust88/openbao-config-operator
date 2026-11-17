"""Evidence predicates and ownership isolation for the PostgreSQL lifecycle extension."""
from datetime import datetime, timezone
from unittest.mock import Mock, patch
import unittest

from databases import postgresql_lifecycle as lifecycle


class PostgreSQLLifecycleTests(unittest.TestCase):
    def response(self, password='new', timestamp='2026-11-17T10:01:00Z', username='fixture'):
        return {'data': {'username': username, 'password': password, 'last_vault_rotation': timestamp,
                         'rotation_period': lifecycle.PERIOD}}

    def test_rotation_requires_same_identity_changed_password_and_later_timestamp(self):
        initial = {'username': 'fixture', 'password': 'old'}
        timestamp = datetime(2026, 11, 17, 10, tzinfo=timezone.utc)
        bao = Mock()
        bao.request.side_effect = [(200, self.response('old')), (200, self.response('new', '2026-11-17T10:00:30Z')),
                                   (200, self.response())]
        with patch.object(lifecycle.time, 'sleep'), patch.object(lifecycle.time, 'monotonic', side_effect=[0, 1, 2, 3]):
            result = lifecycle.wait_rotation(bao, 'root', 'owned', 'fixture', 'fake-token', initial, timestamp)
        self.assertEqual(result['password'], 'new')
        self.assertEqual(bao.request.call_count, 3)
        bao.request.return_value = (200, self.response(username='other'))
        bao.request.side_effect = None
        with self.assertRaisesRegex(RuntimeError, 'identity'):
            lifecycle.static_data(bao, 'root', 'owned', 'fixture', 'fake-token')

    def test_scheduled_rotation_timeout_is_failure(self):
        bao = Mock()
        bao.request.return_value = (200, self.response('unchanged'))
        initial = {'password': 'unchanged'}
        with patch.object(lifecycle.time, 'sleep'), patch.object(lifecycle.time, 'monotonic', side_effect=[0, 1, 151]):
            with self.assertRaisesRegex(RuntimeError, 'did not complete'):
                lifecycle.wait_rotation(bao, 'root', 'owned', 'fixture', 'fake-token', initial,
                                        datetime(2026, 11, 17, tzinfo=timezone.utc))

    def test_malformed_static_response_is_not_rotation_evidence(self):
        bao = Mock()
        for response in ({'data': {}}, self.response(timestamp='bad'), self.response(timestamp='2026-11-17T10:00:00'),
                         self.response(password='')):
            bao.request.return_value = (200, response)
            with self.assertRaises(RuntimeError):
                lifecycle.static_data(bao, 'alpha', 'owned', 'fixture', 'fake-token')

    def test_rotation_connection_never_targets_shared_administrator(self):
        for tenant in ('root', 'alpha'):
            name, administrator, user = lifecycle.names(tenant)
            objects = lifecycle.resources(tenant, name, administrator, user)
            config, dynamic, static = [item['spec'] for item in objects]
            self.assertNotEqual(config['postgresql']['username'], 'openbao')
            self.assertNotEqual(config['passwordSecretRef']['name'], 'database-postgresql-admin')
            self.assertEqual(config['postgresql']['username'], administrator)
            self.assertTrue(config['rotateRootOnCreate'])
            self.assertEqual(static['username'], user)
            self.assertNotEqual(user, administrator)
            self.assertEqual(dynamic['dbName'], config['name'])
            self.assertEqual(static['dbName'], config['name'])
            self.assertEqual(config.get('namespace'), 'lab-alpha' if tenant == 'alpha' else None)
            self.assertTrue(all(item['spec']['deletionPolicy'] == 'Delete' for item in objects))
        with self.assertRaises(ValueError):
            lifecycle.names("root'; DROP ROLE openbao; --")

    def test_long_period_is_required_for_stable_login_checks(self):
        bao = Mock()
        response = self.response()
        response['data']['rotation_period'] = lifecycle.STABLE_PERIOD
        bao.request.return_value = (200, response)
        lifecycle.static_data(bao, 'root', 'owned', 'fixture', 'fake-token', lifecycle.STABLE_PERIOD)
        with self.assertRaisesRegex(RuntimeError, 'period differs'):
            lifecycle.static_data(bao, 'root', 'owned', 'fixture', 'fake-token')
        config = lifecycle.resources('root', 'owned', 'admin', 'static')[2]['spec']
        self.assertEqual(config['rotationPeriod'], str(lifecycle.STABLE_PERIOD) + 's')

    def test_cleanup_failure_cannot_report_success(self):
        bao = Mock()
        bao.request.return_value = (500, {})
        with patch.object(lifecycle, 'admin_probe'), patch.object(lifecycle.common.lab, 'kube'):
            with self.assertRaisesRegex(RuntimeError, 'cleanup did not complete'):
                lifecycle.cleanup(bao, 'root', 'fake-token', 'owned-lease', [], ('owned', 'admin', 'static'), [])


if __name__ == '__main__':
    unittest.main()
