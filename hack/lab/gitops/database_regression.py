"""Reconcile the public database configuration chart with real Argo CD Applications."""
import common
import argocd
from databases import common as database_common
from databases import postgresql, sql_common

EXAMPLES = common.ROOT / 'gitops/databases/argocd'


def prepare(h):
    # The database server and private bootstrap credentials are test infrastructure, outside GitOps configuration.
    sql_common.install('postgresql', mount_ca=False)
    certificate = database_common.lab.read_secret('db-tls', postgresql.NAMESPACE)['ca.crt']
    h.apply({'apiVersion': 'v1', 'kind': 'ConfigMap',
             'metadata': {'name': 'database-postgresql-ca', 'namespace': 'openbao'}, 'data': {'ca.crt': certificate}})
    print('PostgreSQL TLS fixture prepared; public CA is mounted by the official OpenBao Helm values', flush=True)


def regression(h):
    project = common.load_yaml(EXAMPLES / 'project.yaml')
    project['spec']['sourceRepos'] = [h.public_url]
    h.apply(project)
    names = []
    for tenant in database_common.TENANTS:
        database_common.lab.apply([database_common.lab.obj('ServiceAccount', 'db-' + tenant, postgresql.NAMESPACE)])
        app = common.load_yaml(EXAMPLES / ('postgresql-' + tenant + '.yaml'))
        app['spec']['source']['repoURL'] = h.public_url
        app['spec']['source']['targetRevision'] = h.revision
        h.apply(app)
        names.append(app['metadata']['name'])
    common.eventually(lambda: all(argocd.synced(h.get('application', name, 'argocd'), h.revision) for name in names),
                      'database Applications Synced and Healthy at the tested public revision', timeout=600)
    for tenant in database_common.TENANTS:
        resources = database_common.objects(postgresql, tenant)
        database_common.lab.wait_ready(resources)
        managed = h.get('application', 'database-postgresql-' + tenant, 'argocd')['status'].get('resources', [])
        expected = {(item['kind'], item['metadata']['name']) for item in resources}
        actual = {(item['kind'], item['name']) for item in managed}
        if not expected.issubset(actual):
            raise RuntimeError('Argo CD did not report ownership of the database chart resources')
    database_common.test(postgresql)
    print('Public Argo CD database chart: operator configuration and actual SQL credential lifecycle passed', flush=True)
