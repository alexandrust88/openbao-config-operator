"""Valkey ACL users verified through the actual RESP protocol without extra drivers."""
from . import common
from . import provider_utils as util

NAME = 'valkey'
NAMESPACE = 'obco-db-valkey'
# renovate: datasource=docker depName=valkey/valkey
IMAGE = 'valkey/valkey:8.1.11@sha256:8f836008e0986c36adf50da714c7060cf689dbb335f61fe077939d90fa650809'
PROBE = r'''
import json,os,socket,ssl
sock=ssl.create_default_context(cafile='/ca/ca.crt').wrap_socket(socket.create_connection(('valkey.obco-db-valkey.svc',6379),timeout=15),server_hostname='valkey.obco-db-valkey.svc')
stream=sock.makefile('rb')
def call(*args):
 values=[str(a).encode() for a in args]
 sock.sendall(b'*'+str(len(values)).encode()+b'\r\n'+b''.join(b'$'+str(len(v)).encode()+b'\r\n'+v+b'\r\n' for v in values))
 line=stream.readline();kind=line[:1];value=line[1:-2]
 if kind==b'$':
  length=int(value)
  if length<0:return kind,None
  data=stream.read(length);assert stream.read(2)==b'\r\n';return kind,data
 return kind,value
mode=os.environ['MODE'];tenant=os.environ['TENANT']
kind,value=call('AUTH',os.environ['DB_USERNAME'],os.environ['DB_PASSWORD'])
if mode=='revoked':
 assert kind==b'-' and value.startswith(b'WRONGPASS')
else:
 assert (kind,value)==(b'+',b'OK')
 if mode=='bootstrap':
  for name in ['root','alpha']:assert call('SET','lab:'+name+':probe','nonsecret-fixture')==(b'+',b'OK')
 else:
  assert call('GET','lab:'+tenant+':probe')==(b'$',b'nonsecret-fixture')
  if mode=='verify':
   kind,value=call('SET','lab:'+tenant+':probe','forbidden');assert kind==b'-' and value.startswith(b'NOPERM')
   other='alpha' if tenant=='root' else 'root'
   kind,value=call('GET','lab:'+other+':probe');assert kind==b'-' and value.startswith(b'NOPERM')
sock.close();print('{"ok":true}')
'''


def install():
    util.admin_secret(NAME, NAMESPACE, 'default')
    util.tls(NAME, NAMESPACE)
    util.deploy(NAME, NAMESPACE, IMAGE, 6379,
        command=['sh', '-ec', 'umask 077; printf "user default on >%s ~* &* +@all\\n" "$(cat /credentials/password)" > /tmp/users.acl; printf "aclfile /tmp/users.acl\\nbind 0.0.0.0\\nprotected-mode yes\\nport 0\\ntls-port 6379\\ntls-cert-file /tls/tls.crt\\ntls-key-file /tls/tls.key\\ntls-ca-cert-file /tls/ca.crt\\ntls-auth-clients no\\n" > /tmp/valkey.conf; exec valkey-server /tmp/valkey.conf'],
        volumes=[{'name':'tls','secret':{'secretName':'db-tls'}}, {'name': 'credentials', 'secret': {'secretName': 'database-admin'}}],
        mounts=[{'name':'tls','mountPath':'/tls','readOnly':True}, {'name': 'credentials', 'mountPath': '/credentials', 'readOnly': True}])
    probe('root', 'database-admin', 'bootstrap')


def connection(tenant):
    util.tenant_database(tenant)
    return util.connection(NAME, 'valkey-database-plugin', {'valkey': {
        'host': NAME + '.' + NAMESPACE + '.svc', 'port': 6379, 'username': 'default', 'tls': True, 'caCert': util.ca(NAMESPACE)}})


def role(tenant):
    util.tenant_database(tenant)
    return {'creationStatements': ['["~lab:' + tenant + ':*", "+get"]']}


def probe(tenant, credential_secret_name, mode):
    util.tenant_database(tenant)
    volumes, mounts = util.ca_volume()
    common.run_probe(util.PYTHON_IMAGE, ['python3', '-c', PROBE], NAMESPACE, credential_secret_name,
                     env={'TENANT': tenant, 'MODE': mode}, volumes=volumes, mounts=mounts)
