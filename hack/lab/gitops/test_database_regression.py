"""Real public Argo chart delivery stays separate from private database bootstrap."""
import json
import unittest
from unittest.mock import Mock, patch

import common
import database_regression
from databases import common as databases
from databases import postgresql


class DatabaseGitOpsTests(unittest.TestCase):
    def test_public_applications_use_shared_chart_and_current_sql_values(self):
        for tenant in ('root', 'alpha'):
            app = common.load_yaml(database_regression.EXAMPLES / ('postgresql-' + tenant + '.yaml'))
            source = app['spec']['source']
            self.assertEqual(source['repoURL'], 'https://github.com/alexandrust88/openbao-config-operator.git')
            self.assertEqual(source['targetRevision'], 'main')
            self.assertEqual(source['path'], 'gitops/databases/chart')
            values_path = common.ROOT / source['path'] / source['helm']['valueFiles'][0]
            values = common.load_yaml(values_path)
            expected = databases.chart_values(postgresql, tenant)
            self.assertEqual(values['database'], expected['database'])
            self.assertEqual(values['tenant'], tenant)
            self.assertNotIn('Secret', json.dumps(app))

    def test_database_project_cannot_manage_secrets_or_cluster_resources(self):
        project = common.load_yaml(database_regression.EXAMPLES / 'project.yaml')['spec']
        self.assertEqual(project['clusterResourceWhitelist'], [])
        self.assertNotIn('Secret', [item['kind'] for item in project['namespaceResourceWhitelist']])
        self.assertEqual(project['destinations'][0]['namespace'], 'obco-gitops')

    def test_prepare_keeps_private_key_out_of_bao_and_does_not_patch_chart_statefulset(self):
        harness = Mock()
        with patch.object(database_regression.sql_common, 'install') as install, patch.object(databases.lab, 'read_secret', return_value={'ca.crt': 'public-ca', 'tls.key': 'private-fixture'}):
            database_regression.prepare(harness)
        install.assert_called_once_with('postgresql', mount_ca=False)
        obj = harness.apply.call_args.args[0]
        self.assertEqual(obj['kind'], 'ConfigMap')
        self.assertEqual(obj['data'], {'ca.crt': 'public-ca'})
        self.assertNotIn('private-fixture', json.dumps(obj))
        values = common.load_yaml(common.ROOT / 'gitops/openbao/values.yaml')['server']
        self.assertIn({'name': 'database-postgresql-ca', 'configMap': {'name': 'database-postgresql-ca', 'optional': True}}, values['volumes'])
        self.assertIn({'name': 'database-postgresql-ca', 'mountPath': '/database-ca/postgresql', 'readOnly': True}, values['volumeMounts'])


if __name__ == '__main__':
    unittest.main()
