#!/usr/bin/env python3
"""Local Keycloak identity scenarios; OpenBao configuration is applied through this operator's CRDs."""

import argparse
import base64
import contextlib
import hashlib
import html.parser
import http.client
import http.cookiejar
import json
import os
import platform
import re
import secrets
import ssl
import subprocess
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
CONTEXT = os.environ.get("LAB_CONTEXT", "kind-openbao-config-operator-lab")
NAMESPACE = os.environ.get("LAB_NAMESPACE", "obco-lab")
CONNECTION = os.environ.get("LAB_CONNECTION", "lab-openbao")
IDENTITY_NS = "obco-lab-identity"
KEYCLOAK = "http://keycloak.obco-lab-identity.svc:8080"
ISSUER = KEYCLOAK + "/realms/obco-lab"
CALLBACK = "http://localhost:8250/oidc/callback"
TENANTS = ("root", "alpha", "beta")
ROLES = ("editor", "viewer")
SHOWCASE_ROLES = ("admin", "read", "ops")
LABEL = {"app.kubernetes.io/part-of": "obco-lab-identity"}
# renovate: datasource=docker depName=mcr.microsoft.com/playwright
PLAYWRIGHT_IMAGE = "mcr.microsoft.com/playwright:v1.65.1-noble@sha256:1507cbfc550a63a6b7396791ca4f00df17beb56342d2447b3f106c306f525579"


def kube(*args, data=None):
    if not CONTEXT.startswith("kind-"):
        raise RuntimeError("identity lab requires an explicitly named kind context")
    result = subprocess.run(["kubectl", "--context", CONTEXT, *args], input=data,
                            text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=360)
    if result.returncode:
        # kubectl errors can echo Secret patches; do not include its output here.
        raise RuntimeError("kubectl failed: " + " ".join(args[:3]))
    return result.stdout


def apply(objects):
    kube("apply", "-f", "-", data=json.dumps({"apiVersion": "v1", "kind": "List", "items": objects}))


def obj(kind, name, namespace=NAMESPACE, api="v1", **fields):
    metadata = {"name": name, "labels": LABEL}
    if namespace:
        metadata["namespace"] = namespace
    return {"apiVersion": api, "kind": kind, "metadata": metadata, **fields}


def secret(name, namespace, values):
    return obj("Secret", name, namespace, type="Opaque", stringData=values)


def read_secret(name, namespace):
    data = json.loads(kube("-n", namespace, "get", "secret", name, "-o", "json"))["data"]
    return {k: base64.b64decode(v).decode() for k, v in data.items()}


def cr(kind, name, spec, tenant="root"):
    groups = {"OpenBaoNamespace": "sys", "Policy": "sys", "SecretsMount": "sys", "AuthMount": "sys",
              "JWTAuthConfig": "auth", "JWTAuthRole": "auth", "KubernetesAuthConfig": "auth",
              "KubernetesAuthRole": "auth", "Group": "identity", "GroupAlias": "identity",
              "GroupMembership": "identity", "KVSecret": "secrets"}
    common = {"connectionRef": {"name": CONNECTION}, "deletionPolicy": "Orphan"}
    if tenant != "root":
        common["namespace"] = "lab-" + tenant
    return obj(kind, name, api=groups[kind] + ".openbaoconfig.io/v1alpha1", spec={**common, **spec})


def wait_ready(objects, timeout=300):
    deadline = time.monotonic() + timeout
    pending = objects[:]
    while pending and time.monotonic() < deadline:
        remaining = []
        for item in pending:
            name = item["metadata"]["name"]
            current = json.loads(kube("-n", item["metadata"]["namespace"], "get",
                                      item["kind"] + "." + item["apiVersion"].split("/")[0], name, "-o", "json"))
            generation = current["metadata"].get("generation", 1)
            conditions = current.get("status", {}).get("conditions", [])
            settled = {c["type"] for c in conditions if c["status"] == "True" and c.get("observedGeneration") == generation}
            if not {"Ready", "Synced"}.issubset(settled):
                remaining.append(item)
        pending = remaining
        if pending:
            time.sleep(2)
    if pending:
        raise RuntimeError("not Ready: " + ", ".join(x["metadata"]["name"] for x in pending))


def credentials():
    return read_secret("identity-users", IDENTITY_NS)


def realm(values):
    groups = [{"name": f"{tenant}-{role}s"} for tenant in TENANTS for role in ROLES + SHOWCASE_ROLES]
    mappers = [{"name": "groups", "protocol": "openid-connect", "protocolMapper": "oidc-group-membership-mapper",
                "config": {"claim.name": "groups", "full.path": "false", "id.token.claim": "true",
                           "access.token.claim": "true", "userinfo.token.claim": "true"}},
               {"name": "openbao-audience", "protocol": "openid-connect", "protocolMapper": "oidc-audience-mapper",
                "config": {"included.client.audience": "openbao", "id.token.claim": "true", "access.token.claim": "true"}}]
    clients = [{"clientId": "openbao", "enabled": True, "protocol": "openid-connect", "publicClient": False,
                "secret": values["client-secret"], "standardFlowEnabled": True, "directAccessGrantsEnabled": True,
                "redirectUris": [CALLBACK, "http://127.0.0.1:8250/oidc/callback"], "protocolMappers": mappers}]
    users = []
    for tenant in TENANTS:
        for role in ROLES + SHOWCASE_ROLES:
            user = f"{tenant}-{role}"
            users.append({"username": user, "enabled": True, "email": user + "@example.invalid",
                          "emailVerified": True, "firstName": tenant, "lastName": role,
                          "credentials": [{"type": "password", "value": values[user], "temporary": False}],
                          "groups": [f"/{tenant}-{role}s"]})
        client = "machine-" + tenant
        clients.append({"clientId": client, "enabled": True, "protocol": "openid-connect", "publicClient": False,
                        "secret": values[client], "serviceAccountsEnabled": True, "standardFlowEnabled": False,
                        "directAccessGrantsEnabled": False, "protocolMappers": mappers})
        users.append({"username": "service-account-" + client, "enabled": True,
                      "serviceAccountClientId": client, "groups": [f"/{tenant}-editors"]})
    return {"realm": "obco-lab", "enabled": True, "sslRequired": "none", "groups": groups,
            "clients": clients, "users": users}


def tenant_objects(tenant):
    kv = "lab-kv" if tenant == "root" else "kv"
    mount = "lab-oidc" if tenant == "root" else "oidc"
    objects = [cr("SecretsMount", tenant + "-kv", {"path": kv, "type": "kv", "options": {"version": "2"}}, tenant),
               cr("AuthMount", tenant + "-oidc", {"path": mount, "type": "oidc"}, tenant),
               cr("JWTAuthConfig", tenant + "-oidc", {"mountPath": mount, "oidcDiscoveryURL": ISSUER,
                  "boundIssuer": ISSUER, "oidcClientID": "openbao",
                  "oidcClientSecretSecretRef": {"name": "identity-client", "key": "client-secret"},
                  "defaultRole": "viewer"}, tenant)]
    for role in ROLES:
        caps = '["create", "read", "update"]' if role == "editor" else '["read"]'
        policy = f'path "{kv}/data/app/*" {{ capabilities = {caps} }}\n'
        objects += [cr("Policy", tenant + "-" + role, {"name": "lab-" + role, "policy": policy}, tenant),
                    cr("Group", tenant + "-" + role, {"name": "lab-" + role, "type": "external",
                       "policies": ["lab-" + role]}, tenant),
                    cr("GroupAlias", tenant + "-" + role, {"aliasName": f"{tenant}-{role}s", "mountPath": mount,
                       "groupRef": {"name": tenant + "-" + role}}, tenant),
                    cr("GroupMembership", tenant + "-" + role + "-readers", {
                       "groupRef": {"name": tenant + "-readers"}, "memberGroupRef": {"name": tenant + "-" + role}}, tenant)]
        for flow in ("oidc", "jwt"):
            name = role if flow == "oidc" else "jwt-" + role
            role_spec = {"mountPath": mount, "name": name, "roleType": flow, "userClaim": "sub",
                         "groupsClaim": "groups", "boundAudiences": ["openbao"],
                         "boundClaims": {"groups": [f"{tenant}-{role}s"]},
                         "tokenTTL": "10m", "tokenMaxTTL": "15m", "tokenNoDefaultPolicy": True}
            if flow == "oidc":
                role_spec.update(allowedRedirectURIs=[CALLBACK], oidcScopes=["profile", "email"], oidcDisableConfirmation=True)
            objects.append(cr("JWTAuthRole", tenant + "-" + name, role_spec, tenant))
    objects += [cr("Policy", tenant + "-shared", {"name": "lab-shared",
                "policy": f'path "{kv}/data/shared/*" {{ capabilities = ["read"] }}\n'
                          'path "auth/token/revoke-self" { capabilities = ["update"] }'}, tenant),
                cr("Group", tenant + "-readers", {"name": "lab-readers", "type": "internal", "policies": ["lab-shared"]}, tenant)]
    for path in ("app/config", "shared/config"):
        objects.append(cr("KVSecret", tenant + "-" + path.replace("/", "-"), {"mountPath": kv, "path": path,
                          "dataFrom": [{"secretRef": {"name": tenant + "-seed"}}]}, tenant))
    return objects


def up():
    apply([obj("Namespace", IDENTITY_NS, namespace=""),
           *[obj("Namespace", "obco-lab-" + t, namespace="") for t in ("alpha", "beta")]])
    existing = kube("-n", IDENTITY_NS, "get", "secret", "identity-users", "--ignore-not-found", "-o", "name")
    names = [f"{t}-{r}" for t in TENANTS for r in ROLES + SHOWCASE_ROLES] + ["client-secret"] + ["machine-" + t for t in TENANTS]
    values = credentials() if existing else {}
    missing = [name for name in names if name not in values]
    if missing:
        values.update({name: secrets.token_urlsafe(24) for name in missing})
        apply([secret("identity-users", IDENTITY_NS, values)])
    realm_json = json.dumps(realm(values), sort_keys=True, separators=(",", ":"))
    apply([secret("identity-realm", IDENTITY_NS, {"obco-lab-realm.json": realm_json}),
           secret("identity-client", NAMESPACE, {"client-secret": values["client-secret"]})])
    kube("apply", "-f", str(ROOT / "hack/lab/keycloak.yaml"))
    # Keycloak imports this disposable realm only on startup. Unchanged content
    # keeps the pod stable; new users or callbacks trigger a fresh realm import.
    digest = hashlib.sha256(realm_json.encode()).hexdigest()
    kube("-n", IDENTITY_NS, "patch", "deployment", "keycloak", "--type=merge", "-p",
         json.dumps({"spec": {"template": {"metadata": {"annotations": {"openbaoconfig.io/realm-sha256": digest}}}}}))
    kube("-n", IDENTITY_NS, "rollout", "status", "deployment/keycloak", "--timeout=300s")
    namespaces = [cr("OpenBaoNamespace", "lab-" + t, {"name": "lab-" + t}) for t in ("alpha", "beta")]
    apply(namespaces)
    wait_ready(namespaces)
    objects = []
    for tenant in TENANTS:
        apply([secret(tenant + "-seed", NAMESPACE, {"value": "lab-" + tenant + "-initial"})])
        objects += tenant_objects(tenant)
    apply(objects)
    wait_ready(objects)
    print("Identity CRDs Ready: root, lab-alpha and lab-beta; external groups nested in internal readers")


@contextlib.contextmanager
def forward(namespace, service, port):
    proc = subprocess.Popen(["kubectl", "--context", CONTEXT, "-n", namespace, "port-forward", "svc/" + service,
                             ":" + str(port)], stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True)
    found = []
    event = threading.Event()
    def drain():
        for line in proc.stdout:
            match = re.search(r"Forwarding from 127\.0\.0\.1:(\d+)", line)
            if match and not found:
                found.append(int(match.group(1)))
                event.set()
    thread = threading.Thread(target=drain, daemon=True)
    thread.start()
    try:
        if not event.wait(30):
            raise RuntimeError("port-forward did not start for " + service)
        yield found[0]
    finally:
        proc.terminate()
        try:
            proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait()
        thread.join(timeout=2)


class RouteHTTP(urllib.request.HTTPHandler):
    def __init__(self, routes):
        super().__init__()
        self.routes = routes

    def http_open(self, req):
        if req.host not in self.routes:
            raise RuntimeError("unexpected identity redirect host")
        address = self.routes[req.host]
        return self.do_open(lambda host, **kw: http.client.HTTPConnection(address, **kw), req)


class StopCallback(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        if newurl.startswith(CALLBACK + "?"):
            return None
        return super().redirect_request(req, fp, code, msg, headers, newurl)


class LoginForm(html.parser.HTMLParser):
    action = None

    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        if tag == "form" and attrs.get("id") == "kc-form-login":
            self.action = attrs.get("action")


class Bao:
    def __init__(self, port):
        ca = json.loads(kube("-n", "openbao", "get", "configmap", "openbao-ca", "-o", "json"))["data"]["ca.crt"]
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}),
                                                  urllib.request.HTTPSHandler(context=ssl.create_default_context(cadata=ca)))
        self.base = "https://127.0.0.1:" + str(port) + "/v1/"

    def request(self, tenant, method, path, data=None, token=None):
        headers = {"Content-Type": "application/json"}
        if tenant != "root":
            headers["X-Vault-Namespace"] = "lab-" + tenant
        if token:
            headers["X-Vault-Token"] = token
        request = urllib.request.Request(self.base + path, method=method, headers=headers,
                                         data=json.dumps(data).encode() if data is not None else None)
        try:
            with self.opener.open(request, timeout=30) as response:
                payload = response.read()
                return response.status, json.loads(payload) if payload else {}
        except urllib.error.HTTPError as err:
            return err.code, json.loads(err.read())


def expect(status, expected, label):
    if status != expected:
        raise RuntimeError(f"{label}: HTTP {status}, expected {expected}")


def identity_client(port):
    return urllib.request.build_opener(urllib.request.ProxyHandler({}), RouteHTTP({urllib.parse.urlparse(KEYCLOAK).netloc:
                                       "127.0.0.1:" + str(port)}),
                                       urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()), StopCallback())


def keycloak_token(port, values, user=None, machine=None):
    fields = {"client_id": "openbao", "client_secret": values["client-secret"], "scope": "openid",
              "grant_type": "password", "username": user, "password": values.get(user, "")}
    if machine:
        fields = {"grant_type": "client_credentials", "client_id": machine, "client_secret": values[machine]}
    request = urllib.request.Request(ISSUER + "/protocol/openid-connect/token",
                                     data=urllib.parse.urlencode(fields).encode())
    with identity_client(port).open(request, timeout=30) as response:
        return json.load(response)["access_token"]


def oidc_login(bao, port, values, tenant, role, user):
    mount = "lab-oidc" if tenant == "root" else "oidc"
    status, response = bao.request(tenant, "POST", f"auth/{mount}/oidc/auth_url", {"role": role, "redirect_uri": CALLBACK})
    expect(status, 200, "OIDC auth URL")
    url = response["data"].get("auth_url")
    if not url:
        raise RuntimeError("OpenBao returned no OIDC auth URL")
    client = identity_client(port)
    with client.open(url, timeout=30) as response:
        form = LoginForm()
        form.feed(response.read().decode())
    if not form.action:
        raise RuntimeError("Keycloak login form was not served")
    request = urllib.request.Request(form.action, data=urllib.parse.urlencode({"username": user,
                                     "password": values[user], "credentialId": ""}).encode())
    try:
        with client.open(request, timeout=30):
            raise RuntimeError("Keycloak did not redirect to the CLI callback")
    except urllib.error.HTTPError as response:
        if response.code != 302:
            raise RuntimeError("unexpected Keycloak login response") from None
        location = response.headers.get("Location", "")
    if not location.startswith(CALLBACK + "?"):
        raise RuntimeError("unexpected OIDC callback location")
    query = urllib.parse.parse_qs(urllib.parse.urlparse(location).query)
    if not query.get("state") or not query.get("code"):
        raise RuntimeError("OIDC callback omitted code or state")
    return bao.request(tenant, "GET", f"auth/{mount}/oidc/callback?" + urllib.parse.urlencode({
                       "state": query["state"][0], "code": query["code"][0]}))


def assert_policy(bao, tenant, token, role):
    try:
        _assert_policy(bao, tenant, token, role)
    finally:
        status, _ = bao.request(tenant, "POST", "auth/token/revoke-self", token=token)
        expect(status, 204, "revoke lab login token")


def _assert_policy(bao, tenant, token, role):
    kv = "lab-kv" if tenant == "root" else "kv"
    for path in ("app/config", "shared/config"):
        status, _ = bao.request(tenant, "GET", kv + "/data/" + path, token=token)
        expect(status, 200, tenant + " " + role + " reads " + path)
    status, _ = bao.request(tenant, "POST", kv + "/data/app/probe", {"data": {"value": "lab-probe"}}, token)
    expect(status, 200 if role == "editor" else 403, tenant + " " + role + " write")
    for path in ("data/other/config", "metadata/app/config"):
        status, _ = bao.request(tenant, "GET", kv + "/" + path, token=token)
        expect(status, 403, tenant + " forbidden prefix")
    status, _ = bao.request(tenant, "POST", kv + "/destroy/app/config", {"versions": [1]}, token)
    expect(status, 403, tenant + " destroy denied")
    other = "beta" if tenant != "beta" else "alpha"
    status, _ = bao.request(other, "GET", "kv/data/app/config", token=token)
    expect(status, 403, tenant + " cross-namespace read")


def test():
    values = credentials()
    with forward(IDENTITY_NS, "keycloak", 8080) as kc_port, forward("openbao", "openbao-active", 8200) as bao_port:
        bao = Bao(bao_port)
        for tenant in TENANTS:
            mount = "lab-oidc" if tenant == "root" else "oidc"
            for role in ROLES:
                user = f"{tenant}-{role}"
                status, response = oidc_login(bao, kc_port, values, tenant, role, user)
                expect(status, 200, "OIDC " + user)
                assert_policy(bao, tenant, response["auth"]["client_token"], role)
                jwt = keycloak_token(kc_port, values, user=user)
                status, response = bao.request(tenant, "POST", f"auth/{mount}/login", {"role": "jwt-" + role, "jwt": jwt})
                expect(status, 200, "JWT " + user)
                assert_policy(bao, tenant, response["auth"]["client_token"], role)
                wrong = "viewer" if role == "editor" else "editor"
                status, denied = bao.request(tenant, "POST", f"auth/{mount}/login", {"role": "jwt-" + wrong, "jwt": jwt})
                if status not in (400, 403) or "bound claim" not in " ".join(denied.get("errors", [])):
                    raise RuntimeError("wrong-group JWT was not rejected by its claim binding")
            jwt = keycloak_token(kc_port, values, machine="machine-" + tenant)
            status, response = bao.request(tenant, "POST", f"auth/{mount}/login", {"role": "jwt-editor", "jwt": jwt})
            expect(status, 200, "machine JWT " + tenant)
            assert_policy(bao, tenant, response["auth"]["client_token"], "editor")
            print(tenant + ": real OIDC auth code, user JWT, machine JWT, nested groups and allow/deny pairs passed")
    print("OIDC flow used real Keycloak HTML and HTTP redirects; this is not browser-rendering coverage")


def browser_test():
    package = json.loads((ROOT / "hack/lab/browser/package.json").read_text())
    version = package["dependencies"]["playwright"]
    if not PLAYWRIGHT_IMAGE.startswith("mcr.microsoft.com/playwright:v" + version + "-noble@sha256:"):
        raise RuntimeError("Playwright package and browser image versions must be updated together")
    values = credentials()
    with forward(IDENTITY_NS, "keycloak", 8080) as kc_port, forward("openbao", "openbao-active", 8200) as bao_port:
        bao = Bao(bao_port)
        for user, allowed in (("alpha-editor", True), ("alpha-viewer", False)):
            status, response = bao.request("alpha", "POST", "auth/oidc/oidc/auth_url",
                                           {"role": "editor", "redirect_uri": CALLBACK})
            expect(status, 200, "browser auth URL")
            url = response["data"].get("auth_url")
            if not url:
                raise RuntimeError("browser auth URL missing")
            artifact_dir = ROOT / ".tmp/lab/browser" / user
            artifact_dir.mkdir(parents=True, exist_ok=True)
            browser_artifacts = str(artifact_dir)
            browser_host = "127.0.0.1"
            command = ["node", str(ROOT / "hack/lab/browser/oidc.mjs")]
            if os.environ.get("LAB_BROWSER_RUNNER", "docker") == "docker":
                command = ["docker", "run", "--rm", "-i", "--shm-size=1g", "--volume",
                           str(ROOT / "hack/lab/browser") + ":/lab:ro", "--volume", str(artifact_dir) + ":/artifacts",
                           "--user", (f"{os.getuid()}:{os.getgid()}" if platform.system() == "Linux" else "1000:1000"),
                           "--workdir", "/lab"]
                browser_artifacts = "/artifacts"
                if platform.system() == "Linux":
                    command += ["--network=host"]
                else:
                    browser_host = "host.docker.internal"
                command += [PLAYWRIGHT_IMAGE, "node", "/lab/oidc.mjs"]
            result = subprocess.run(command,
                                     input=json.dumps({"artifacts": browser_artifacts, "issuer": ISSUER, "host": browser_host, "port": kc_port, "url": url,
                                                       "username": user, "password": values[user]}),
                                     text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=240)
            if result.returncode:
                raise RuntimeError("Chromium OIDC login failed: " + result.stderr.strip())
            callback = json.loads(result.stdout)
            if not callback.get("code") or not callback.get("state"):
                raise RuntimeError("browser did not return a valid OIDC callback")
            status, response = bao.request("alpha", "GET", "auth/oidc/oidc/callback?" + urllib.parse.urlencode(callback))
            if allowed:
                expect(status, 200, "browser editor login")
                assert_policy(bao, "alpha", response["auth"]["client_token"], "editor")
            elif status not in (400, 403) or "bound claim" not in " ".join(response.get("errors", [])):
                raise RuntimeError("browser viewer login was not rejected by editor group binding")
    print("Chromium: Keycloak login accepted editor and rejected viewer for the same OpenBao role")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("up", "test", "browser", "render"))
    args = parser.parse_args()
    if args.command == "render":
        print(json.dumps({"apiVersion": "v1", "kind": "List", "items": [o for t in TENANTS for o in tenant_objects(t)]}))
    else:
        try:
            {"up": up, "test": test, "browser": browser_test}[args.command]()
        except (RuntimeError, urllib.error.URLError) as error:
            raise SystemExit(str(error)) from None
