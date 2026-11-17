"""Cassandra CQL dynamic roles with authentication and per-keyspace permissions."""
import base64
import os
from pathlib import Path
import secrets
import subprocess
import tempfile

from . import common
from . import provider_utils as util

NAME = 'cassandra'
NAMESPACE = 'obco-db-cassandra'
# renovate: datasource=docker depName=cassandra
IMAGE = 'cassandra:5.0.9@sha256:acd754f07c73f3a166bb477354b56ce9a05690c3e5e2aaacc1aab2afddba6436'
PROBE = r'''
import configparser,os,pathlib,subprocess,tempfile,time
host='cassandra.obco-db-cassandra.svc'
mode=os.environ['MODE'];tenant=os.environ['TENANT']
def query(statement,user=None,password=None):
 with tempfile.TemporaryDirectory() as directory:
  path=pathlib.Path(directory)/'cqlshrc';config=configparser.ConfigParser(interpolation=None)
  config['authentication']={'username':user or os.environ['DB_USERNAME'],'password':password or os.environ['DB_PASSWORD']}
  config['ssl']={'certfile':'/ca/ca.crt','validate':'true'}
  with path.open('w') as f:config.write(f)
  path.chmod(0o600)
  return subprocess.run(['cqlsh','--ssl','--cqlshrc',str(path),'--connect-timeout=15','--request-timeout=30',host,'9042'],
      input=statement,text=True,capture_output=True,timeout=60)
def require(result):
 assert result.returncode==0 and 'Error' not in result.stderr
if mode=='bootstrap':
 # Cassandra creates this bootstrap account itself. Replace it before provisioning any client role.
 existing=query('SELECT release_version FROM system.local;')
 if existing.returncode!=0:
  for _ in range(60):
   result=query('SELECT release_version FROM system.local;', 'cassandra', 'cassandra')
   if result.returncode==0:break
   time.sleep(2)
  else:raise RuntimeError('Cassandra bootstrap authentication did not become available')
  user=os.environ['DB_USERNAME'];password=os.environ['DB_PASSWORD']
  assert user=='obco' and all(c in '0123456789abcdef' for c in password)
  require(query("CREATE ROLE IF NOT EXISTS obco WITH PASSWORD = '"+password+"' AND LOGIN = true AND SUPERUSER = true;",'cassandra','cassandra'))
 require(query('SELECT release_version FROM system.local;'))
 for name in ['lab_root','lab_alpha']:
  require(query("CREATE KEYSPACE IF NOT EXISTS "+name+" WITH replication = {'class':'SimpleStrategy','replication_factor':1}; CREATE TABLE IF NOT EXISTS "+name+".probe (id int PRIMARY KEY, value text); INSERT INTO "+name+".probe (id,value) VALUES (1,'nonsecret-fixture');"))
 require(query('ALTER ROLE cassandra WITH LOGIN = false;'))
elif mode=='revoked':
 result=query('SELECT release_version FROM system.local;')
 assert result.returncode!=0 and ('AuthenticationFailed' in result.stderr or 'authentication failed' in result.stderr.lower())
else:
 result=query('SELECT value FROM lab_'+tenant+'.probe WHERE id=1;');require(result)
 assert 'nonsecret-fixture' in result.stdout
 if mode=='verify':
  for statement in ["INSERT INTO lab_"+tenant+".probe (id,value) VALUES (2,'forbidden');",'SELECT * FROM lab_'+('alpha' if tenant=='root' else 'root')+'.probe;']:
   denied=query(statement)
   assert 'Unauthorized' in denied.stderr and 'has no' in denied.stderr
print('{"ok":true}')
'''


def install():
    util.admin_secret(NAME, NAMESPACE, 'obco')
    bundle = util.tls(NAME, NAMESPACE)
    password = secrets.token_hex(24)
    with tempfile.TemporaryDirectory() as directory:
        path = Path(directory)
        for key in ('tls.crt', 'tls.key', 'ca.crt'):
            (path / key).write_text(bundle[key])
            (path / key).chmod(0o600)
        result = subprocess.run(['openssl', 'pkcs12', '-export', '-in', str(path / 'tls.crt'),
                                 '-inkey', str(path / 'tls.key'), '-certfile', str(path / 'ca.crt'),
                                 '-out', str(path / 'keystore.p12'), '-passout', 'env:LAB_KEYSTORE_PASSWORD'],
                                env={**os.environ, 'LAB_KEYSTORE_PASSWORD': password}, capture_output=True)
        if result.returncode:
            raise RuntimeError('Cannot create disposable Cassandra TLS keystore')
        util.lab.apply([util.lab.obj('Secret', 'db-java-tls', NAMESPACE, data={
            'keystore.p12': base64.b64encode((path / 'keystore.p12').read_bytes()).decode(),
            'password': base64.b64encode(password.encode()).decode()})])
    configure = r"""
import re,pathlib
path=pathlib.Path('/etc/cassandra/cassandra.yaml')
text=path.read_text()
settings = {
 'authenticator': 'PasswordAuthenticator',
 'authorizer': 'CassandraAuthorizer',
 'roles_validity': '0ms',
 'permissions_validity': '0ms',
 'credentials_validity': '0ms',
}
for key,value in settings.items():
 text=re.sub('(?m)^'+key+':.*$',key+': '+value,text)
block='client_encryption_options:\n  enabled: true\n  optional: false\n  keystore: /tls/keystore.p12\n  keystore_password: '+pathlib.Path('/tls/password').read_text()+'\n  store_type: PKCS12\n  require_client_auth: false\n'
text=re.sub(r'(?ms)^client_encryption_options:\n.*?(?=^[a-zA-Z_]|\Z)',lambda _:block,text)
path.write_text(text)
"""
    command = ['bash', '-ec', 'python3 -c "$1"; exec docker-entrypoint.sh cassandra -f', 'configure', configure]
    util.deploy(NAME, NAMESPACE, IMAGE, 9042, command=command, memory='2Gi',
                env=[{'name': 'MAX_HEAP_SIZE', 'value': '768M'}, {'name': 'HEAP_NEWSIZE', 'value': '192M'}],
                volumes=[{'name':'tls','secret':{'secretName':'db-java-tls'}}],
                mounts=[{'name':'tls','mountPath':'/tls','readOnly':True}])
    probe('root', 'database-admin', 'bootstrap')


def connection(tenant):
    util.tenant_database(tenant)
    return util.connection(NAME, 'cassandra-database-plugin', {'cassandra': {
        'hosts': [NAME + '.' + NAMESPACE + '.svc'], 'port': 9042, 'username': 'obco',
        'protocolVersion': 4, 'tls': True, 'connectTimeout': '15s',
        'tlsServerName': NAME + '.' + NAMESPACE + '.svc',
        'pemBundleSecretRef': {'name': 'database-cassandra-trust', 'key': 'ca.crt'}}})


def role(tenant):
    db = util.tenant_database(tenant)
    return {'creationStatements': ["CREATE ROLE '{{username}}' WITH PASSWORD = '{{password}}' AND LOGIN = true; GRANT SELECT ON KEYSPACE " + db + " TO '{{username}}';"],
            'revocationStatements': ["DROP ROLE '{{username}}';"]}


def probe(tenant, credential_secret_name, mode):
    util.tenant_database(tenant)
    volumes, mounts = util.ca_volume()
    common.run_probe(IMAGE, ['python3', '-c', PROBE], NAMESPACE, credential_secret_name,
                     env={'TENANT': tenant, 'MODE': mode}, timeout=240, volumes=volumes, mounts=mounts)
