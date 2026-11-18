"""InfluxDB 1.x dynamic users: real HTTP queries, scoped reads and revocation."""
from . import common
from . import provider_utils as util

NAME = 'influxdb'
NAMESPACE = 'obco-db-influxdb'
# renovate: datasource=docker depName=influxdb
IMAGE = 'influxdb:1.12.4@sha256:b4850cef0cf93d169581714bd0dd995fa8f1b5cdfdaaf35a43fbbdc643adb3ca'
PROBE = r'''
import base64,json,os,ssl,urllib.request,urllib.error,urllib.parse
class NoRedirect(urllib.request.HTTPRedirectHandler):
 def redirect_request(self,*args,**kwargs):
  raise RuntimeError('Database probe refused an HTTP redirect')
host='https://influxdb.obco-db-influxdb.svc:8086'
auth=base64.b64encode((os.environ['DB_USERNAME']+':'+os.environ['DB_PASSWORD']).encode()).decode()
opener=urllib.request.build_opener(NoRedirect(),urllib.request.ProxyHandler({}),urllib.request.HTTPSHandler(context=ssl.create_default_context(cafile='/ca/ca.crt')))
def call(path,params,body=None):
 req=urllib.request.Request(host+path+'?'+urllib.parse.urlencode(params),data=body,headers={'Authorization':'Basic '+auth})
 try:
  with opener.open(req,timeout=15) as r:
   data=r.read();return r.status,json.loads(data) if data else {}
 except urllib.error.HTTPError as e:
  status=e.code;e.close();return status,{}
def query(db,q):
 return call('/query',{'db':db,'q':q},b'')
def success(result):
 status,data=result
 return status==200 and not data.get('error') and all(not x.get('error') for x in data.get('results',[]))
mode=os.environ['MODE'];db='lab_'+os.environ['TENANT']
if mode=='bootstrap':
 for name in ['lab_root','lab_alpha']:
  assert success(query('', 'CREATE DATABASE '+name))
  assert call('/write',{'db':name},b'probe value=1i')[0]==204
elif mode=='revoked':
 assert query(db,'SELECT * FROM probe')[0]==401
else:
 status,data=query(db,'SELECT * FROM probe')
 assert success((status,data)) and data['results'][0]['series'][0]['values']
 if mode=='verify':
  assert call('/write',{'db':db},b'probe value=2i')[0]==403
  other='lab_alpha' if db=='lab_root' else 'lab_root'
  status,data=query(other,'SELECT * FROM probe')
  # InfluxDB 1.x reports query authorisation errors inside a successful HTTP response.
  assert status==403 or (status==200 and any('not authorized' in x.get('error','').lower() for x in data.get('results',[])))
print('{"ok":true}')
'''


def install_bao_trust(certificate):
    # OpenBao 2.7.2's Influx plugin loses CAChain while converting pem_json/pem_bundle.
    # Extend the process trust directories without disabling verification or replacing system roots.
    import json
    current = json.loads(common.lab.kube('-n', 'openbao', 'get', 'statefulset', 'openbao', '-o', 'json'))
    cluster = common.lab.CONTEXT.removeprefix('kind-')
    if current['metadata'].get('labels', {}).get('lab.obco.dev/cluster') != cluster:
        raise RuntimeError('Influx trust extension requires the owned disposable OpenBao fixture')
    container = next(item for item in current['spec']['template']['spec']['containers'] if item['name'] == 'openbao')
    trust = next((item for item in container.get('env', []) if item['name'] == 'SSL_CERT_DIR'), None)
    if trust and 'value' not in trust:
        raise RuntimeError('Cannot extend an indirectly configured OpenBao certificate directory')
    directories = (trust['value'] if trust else '/etc/ssl/certs:/etc/pki/tls/certs').split(':')
    if '/database-ca/influxdb' not in directories:
        directories.append('/database-ca/influxdb')
    common.mount_bao_ca(NAME, certificate, extra_env=[{'name': 'SSL_CERT_DIR', 'value': ':'.join(directories)}])


def install():
    util.admin_secret(NAME, NAMESPACE, 'obco')
    bundle = common.database_tls(NAMESPACE, NAME)
    install_bao_trust(bundle['ca.crt'])
    # InfluxDB rejects group/world-readable TLS keys. Secret volumes are root-owned;
    # copy once into memory with the image's server ownership and mode 0600.
    util.deploy(NAME, NAMESPACE, IMAGE, 8086, env=[{'name': 'INFLUXDB_HTTP_AUTH_ENABLED', 'value': 'true'},
                {'name': 'INFLUXDB_ADMIN_USER', 'value': 'obco'}, util.secret_env('INFLUXDB_ADMIN_PASSWORD', 'password'),
                {'name':'INFLUXDB_HTTP_HTTPS_ENABLED','value':'true'},
                {'name':'INFLUXDB_HTTP_HTTPS_CERTIFICATE','value':'/tls/tls.crt'},
                {'name':'INFLUXDB_HTTP_HTTPS_PRIVATE_KEY','value':'/tls/tls.key'}],
                volumes=[{'name':'tls','secret':{'secretName':'db-tls', 'defaultMode': 0o400}},
                         {'name':'private-tls','emptyDir':{'medium':'Memory'}}],
                mounts=[{'name':'private-tls','mountPath':'/tls','readOnly':True}],
                init_containers=[{'name':'prepare-tls', 'image':IMAGE,
                    'command':['/bin/sh','-ec',
                               'cp /source/tls.crt /source/tls.key /tls/; chmod 0600 /tls/*; chown influxdb:influxdb /tls/*'],
                    'securityContext':{'runAsUser':0, 'allowPrivilegeEscalation':False,
                                       'readOnlyRootFilesystem':True, 'capabilities':{'drop':['ALL'], 'add':['CHOWN']}},
                    'volumeMounts':[{'name':'tls','mountPath':'/source','readOnly':True},
                                    {'name':'private-tls','mountPath':'/tls'}]}])
    probe('root', 'database-admin', 'bootstrap')


def connection(tenant):
    util.tenant_database(tenant)
    return util.connection(NAME, 'influxdb-database-plugin', {'influxdb': {
        'host': NAME + '.' + NAMESPACE + '.svc', 'port': 8086, 'username': 'obco', 'tls': True}})


def role(tenant):
    db = util.tenant_database(tenant)
    return {'creationStatements': ['CREATE USER "{{username}}" WITH PASSWORD \'{{password}}\'; GRANT READ ON "' + db + '" TO "{{username}}";'],
            'revocationStatements': ['DROP USER "{{username}}";']}


def probe(tenant, credential_secret_name, mode):
    util.tenant_database(tenant)
    volumes, mounts = util.ca_volume()
    common.run_probe(util.PYTHON_IMAGE, ['python3', '-c', PROBE], NAMESPACE, credential_secret_name,
                     env={'TENANT': tenant, 'MODE': mode}, volumes=volumes, mounts=mounts)
