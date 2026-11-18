#!/usr/bin/env python3
"""Apply SaaS identity CRDs and test hosted human OIDC groups against the kind lab."""
import argparse
import ast
import platform
import base64
import contextlib
import copy
import http.server
import json
import os
from pathlib import Path
import re
import secrets
import ssl
import stat
import subprocess
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import webbrowser

TENANTS = ("root", "alpha", "beta")
ROLES = ("admin", "read", "ops")
CALLBACK = "http://127.0.0.1:8250/oidc/callback"
LOCAL_ISSUERS = {
    "keycloak": "http://keycloak.obco-lab-identity.svc:8080/realms/obco-lab",
    "authentik": "http://authentik.obco-lab-authentik.svc:9000/application/o/openbao/",
    "zitadel": "http://zitadel.obco-lab-zitadel.svc:8080",
}
KEYCLOAK_ISSUER = LOCAL_ISSUERS["keycloak"]


def local_provider(state):
    return state.get("provider") in LOCAL_ISSUERS and state.get("issuer") == LOCAL_ISSUERS[state["provider"]]

GROUPS = {"BaoNamespace": "sys", "ACLPolicy": "sys", "SecretsMount": "sys", "AuthMount": "sys",
          "JWTAuthConfig": "auth", "JWTAuthRole": "auth", "IdentityGroup": "identity", "IdentityGroupAlias": "identity",
          "IdentityGroupMembership": "identity", "KVSecret": "secrets", "TransitKey": "secrets"}


def load_state(path):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd) as source:
        info = os.fstat(source.fileno())
        if not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) & 0o077:
            raise ValueError("state must be a private regular file (chmod 600)")
        state = json.load(source)
    validate_state(state)
    return state


def validate_state(state):
    if state.get("provider") not in ("okta", "auth0", *LOCAL_ISSUERS):
        raise ValueError("unknown provider")
    if (state["provider"] == "auth0" and state.get("status") != "complete") or (state["provider"] != "auth0" and state.get("complete") is not True):
        raise ValueError("provider provisioning must be complete")
    if state["provider"] in LOCAL_ISSUERS and not local_provider(state):
        raise ValueError("Local provider mode is restricted to its exact disposable lab issuer")
    issuer = urllib.parse.urlsplit(state.get("issuer", ""))
    if (issuer.scheme != "https" and not local_provider(state)) or not issuer.hostname or issuer.username or issuer.password or issuer.query or issuer.fragment:
        raise ValueError("issuer must be an HTTPS URL without credentials, query or fragment")
    if state["provider"] == "auth0" and not re.fullmatch(r"[A-Za-z0-9_-]+", state.get("connection_name", "")):
        raise ValueError("Auth0 state must name the dedicated user connection")
    if state.get("callback_uri", CALLBACK) != CALLBACK:
        raise ValueError("callback_uri must be " + CALLBACK)
    for key in ("groups_claim", "oidc_client_id", "oidc_client_secret"):
        if not isinstance(state.get(key), str) or not state[key]:
            raise ValueError("missing required state field: " + key)
    scopes = state.get("oidc_scopes", ["profile", "email"])
    if not isinstance(scopes, list) or not scopes or any(not isinstance(scope, str) or not re.fullmatch(r"[A-Za-z0-9:._/-]+", scope) for scope in scopes):
        raise ValueError("oidc_scopes must contain non-empty OAuth scope names")
    if state.get("token_endpoint_auth_method", "client_secret_basic") not in ("client_secret_basic", "client_secret_post"):
        raise ValueError("unsupported confidential client authentication method")
    users = state.get("users", [])
    pairs = [(row.get("namespace"), row.get("role")) for row in users]
    if len(pairs) != len(set(pairs)) or set(pairs) != {(t, r) for t in TENANTS for r in ROLES}:
        raise ValueError("users must contain exactly one entry for every tenant and role")
    groups = []
    for user in users:
        if not isinstance(user.get("group"), str) or not user["group"]:
            raise ValueError("user is missing group")
        groups.append(user["group"])
    if len(set(groups)) != len(groups):
        raise ValueError("human groups must be unique across tenants and roles")


def prefix(state):
    return "saas-" + state["provider"]


def bao_namespace(state, tenant):
    if tenant not in TENANTS:
        raise ValueError("unknown tenant")
    return "" if tenant == "root" else prefix(state) + "-" + tenant


def policy(state, role):
    kv, transit = prefix(state) + "-kv", prefix(state) + "-transit"
    caps = '["create", "read", "update", "delete"]' if role == "admin" else '["read"]'
    result = f'path "{kv}/data/app/*" {{ capabilities = {caps} }}\n'
    if role == "admin":
        result += f'path "{kv}/metadata/app" {{ capabilities = ["list"] }}\n'
        result += f'path "{kv}/metadata/app/*" {{ capabilities = ["read", "list"] }}\n'
    if role in ("admin", "ops"):
        for operation in ("encrypt", "decrypt"):
            result += f'path "{transit}/{operation}/app" {{ capabilities = ["update"] }}\n'
    return result


def render(state, kube_namespace="obco-lab", connection="lab-openbao"):
    validate_state(state)
    p = prefix(state)
    label = {"app.kubernetes.io/part-of": p}
    def obj(kind, name, spec=None, tenant="root", **fields):
        item = {"apiVersion": GROUPS[kind] + ".obco.dev/v1alpha1" if kind in GROUPS else "v1",
                "kind": kind, "metadata": {"name": name, "namespace": kube_namespace, "labels": label.copy()}, **fields}
        if spec is not None:
            common = {"connectionRef": {"name": connection}, "deletionPolicy": "Orphan"}
            if tenant != "root":
                common["openbaoNamespace"] = bao_namespace(state, tenant)
            item["spec"] = {**common, **spec}
        return item
    objects = [obj("Secret", p + "-oidc", type="Opaque", stringData={"client-secret": state["oidc_client_secret"]}),
               obj("Secret", p + "-seed", type="Opaque", stringData={"value": "saas-lab-marker"})]
    for tenant in TENANTS:
        name = p + "-" + tenant
        if tenant != "root":
            objects.append(obj("BaoNamespace", name, {"name": bao_namespace(state, tenant)}))
        for engine, kind in (("kv", "kv"), ("transit", "transit")):
            spec = {"path": p + "-" + engine, "type": kind}
            if engine == "kv":
                spec["options"] = {"version": "2"}
            objects.append(obj("SecretsMount", name + "-" + engine, spec, tenant))
        objects += [obj("TransitKey", name + "-key", {"mountPath": p + "-transit", "name": "app", "type": "aes256-gcm96"}, tenant),
                    obj("KVSecret", name + "-seed", {"mountPath": p + "-kv", "path": "app/config",
                        "dataFrom": [{"secretRef": {"name": p + "-seed"}}]}, tenant)]
        mount = p + "-oidc"
        config = {"mountPath": mount, "oidcDiscoveryURL": state["issuer"], "boundIssuer": state["issuer"],
                  "oidcClientID": state["oidc_client_id"], "oidcClientSecretSecretRef": {"name": p + "-oidc", "key": "client-secret"}}
        objects += [obj("AuthMount", name + "-oidc", {"path": mount, "type": "oidc"}, tenant),
                    obj("JWTAuthConfig", name + "-oidc", config, tenant)]
        shared = p + "-session"
        objects += [obj("ACLPolicy", name + "-session", {"name": shared,
                       "policy": 'path "auth/token/revoke-self" { capabilities = ["update"] }\n'}, tenant),
                    obj("IdentityGroup", name + "-session", {"name": shared, "type": "internal", "policies": [shared]}, tenant)]
        for role in ROLES:
            user = next(x for x in state["users"] if x["namespace"] == tenant and x["role"] == role)
            policy_name = p + "-" + role
            objects += [obj("ACLPolicy", name + "-" + role, {"name": policy_name, "policy": policy(state, role)}, tenant),
                        obj("IdentityGroup", name + "-" + role, {"name": policy_name, "type": "external", "policies": [policy_name]}, tenant),
                        obj("IdentityGroupAlias", name + "-" + role, {"aliasName": user["group"], "mountPath": p + "-oidc",
                            "groupRef": {"name": name + "-" + role}}, tenant),
                        obj("IdentityGroupMembership", name + "-" + role, {"groupRef": {"name": name + "-session"},
                            "memberGroupRef": {"name": name + "-" + role}}, tenant)]
            common = {"name": role, "userClaim": "sub", "tokenTTL": "5m", "tokenMaxTTL": "10m", "tokenNoDefaultPolicy": True}
            objects += [obj("JWTAuthRole", name + "-oidc-" + role, {**common, "mountPath": p + "-oidc", "roleType": "oidc",
                            "groupsClaim": state["groups_claim"], "boundClaims": {state["groups_claim"]: [user["group"]]},
                            "boundAudiences": [state["oidc_client_id"]], "allowedRedirectURIs": [CALLBACK],
                            "oidcScopes": state.get("oidc_scopes", ["profile", "email"]), "callbackMode": "client"}, tenant)]
    return objects


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


OIDC_ERROR_MARKERS = {
    "binding": ("does not match any associated bound claim values",),
    "exchange": ("error exchanging code", "token exchange", "invalid_grant"),
    "rate-limit": ("rate limit", "too many requests"),
    "nonce": ("nonce",),
    "expired": ("expired",),
    "signature": ("signature",),
}


def request_json(opener, url, method="GET", data=None, headers=None):
    request = urllib.request.Request(url, data=data, headers=headers or {}, method=method)
    try:
        with opener.open(request, timeout=30) as response:
            body = response.read()
            return response.status, json.loads(body) if body else {}
    except urllib.error.HTTPError as error:
        try:
            errors = json.loads(error.read(65536)).get("errors", [])
            if not isinstance(errors, list):
                errors = []
            category = next((category for category, markers in OIDC_ERROR_MARKERS.items()
                             if any(isinstance(message, str) and marker in message.lower()
                                    for message in errors for marker in markers)), "unknown")
        except (ValueError, AttributeError, TypeError):
            category = "unknown"
        finally:
            error.close()
        return error.code, {"binding_denied": category == "binding", "rejection": category}
    except (urllib.error.URLError, ValueError):
        raise RuntimeError("HTTP request failed; response omitted to protect credentials") from None


class Kube:
    def __init__(self, context):
        if not context.startswith("kind-"):
            raise ValueError("SaaS lab requires an explicit kind context")
        self.context = context

    def run(self, *args, data=None):
        result = subprocess.run(["kubectl", "--context", self.context, *args], input=data, text=True,
                                stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=360)
        if result.returncode:
            raise RuntimeError("kubectl failed; output omitted to protect credentials")
        return result.stdout

    def apply(self, objects):
        self.run("apply", "--server-side", "--field-manager=obco-saas-lab", "-f", "-",
                 data=json.dumps({"apiVersion": "v1", "kind": "List", "items": objects}))

    def wait(self, objects, timeout=300):
        deadline = time.monotonic() + timeout
        pending = [o for o in objects if o["kind"] in GROUPS]
        while pending and time.monotonic() < deadline:
            waiting = []
            for item in pending:
                current = json.loads(self.run("-n", item["metadata"]["namespace"], "get", item["kind"] + "." + item["apiVersion"].split("/")[0], item["metadata"]["name"], "-o", "json"))
                generation = current["metadata"].get("generation", 1)
                ready = {c["type"] for c in current.get("status", {}).get("conditions", [])
                         if c["status"] == "True" and c.get("observedGeneration") == generation}
                if not {"Ready", "Synced"}.issubset(ready):
                    waiting.append(item)
            pending = waiting
            if pending:
                time.sleep(2)
        if pending:
            raise RuntimeError("CRDs did not become Ready: " + ", ".join(x["metadata"]["name"] for x in pending))


@contextlib.contextmanager
def connect(kube, state):
    ca = json.loads(kube.run("-n", "openbao", "get", "configmap", "openbao-ca", "-o", "json"))["data"]["ca.crt"]
    proc = subprocess.Popen(["kubectl", "--context", kube.context, "-n", "openbao", "port-forward", "svc/openbao-active", ":8200"],
                            stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True)
    ports, event = [], threading.Event()
    def drain():
        for line in proc.stdout:
            match = re.search(r"Forwarding from 127\.0\.0\.1:(\d+)", line)
            if match and not ports:
                ports.append(int(match[1]))
                event.set()
    thread = threading.Thread(target=drain, daemon=True)
    thread.start()
    try:
        if not event.wait(30):
            raise RuntimeError("OpenBao port-forward did not start")
        yield Bao(state, "https://127.0.0.1:" + str(ports[0]), ca)
    finally:
        proc.terminate()
        try:
            proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait()
        thread.join(timeout=2)


class Bao:
    def __init__(self, state, address, ca):
        self.state, self.address = state, address
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect(),
                      urllib.request.HTTPSHandler(context=ssl.create_default_context(cadata=ca)))

    def request(self, tenant, method, path, data=None, token=None):
        headers = {"Content-Type": "application/json"}
        if tenant != "root":
            headers["X-Vault-Namespace"] = bao_namespace(self.state, tenant)
        if token:
            headers["X-Vault-Token"] = token
        return request_json(self.opener, self.address + "/v1/" + path, method,
                            json.dumps(data).encode() if data is not None else None, headers)


def expect(status, wanted, label):
    if status != wanted:
        raise RuntimeError(f"{label}: HTTP {status}, expected {wanted}")


def test_access(bao, state, tenant, role, auth):
    p = prefix(state)
    token = auth.get("client_token")
    if not isinstance(token, str) or not token:
        raise RuntimeError("OIDC login returned no token")
    try:
        expected_policies = {p + "-" + role, p + "-session"}
        if not auth.get("entity_id") or set(auth.get("identity_policies") or []) != expected_policies:
            raise RuntimeError("external group and inherited internal group policies were not both resolved")
        if auth.get("token_policies") or set(auth.get("policies") or []) != expected_policies or auth.get("external_namespace_policies"):
            raise RuntimeError("direct token or unexpected policies mask the identity group mapping")
        status, body = bao.request(tenant, "GET", p + "-kv/data/app/config", token=token)
        expect(status, 200, "read seed")
        if body.get("data", {}).get("data", {}).get("value") != "saas-lab-marker":
            raise RuntimeError("read returned the wrong seed")
        probe = "app/probe-" + secrets.token_hex(6)
        if role == "admin":
            for version, value in ((1, "first"), (2, "second")):
                status, written = bao.request(tenant, "POST", p + "-kv/data/" + probe, {"data": {"value": value}}, token)
                expect(status, 200, "KV version write")
                if written.get("data", {}).get("version") != version:
                    raise RuntimeError("KV write did not create the expected version")
            for version, value in ((1, "first"), (2, "second")):
                status, previous = bao.request(tenant, "GET", p + "-kv/data/" + probe + "?version=" + str(version), token=token)
                expect(status, 200, "read explicit KV version")
                if previous.get("data", {}).get("data", {}).get("value") != value:
                    raise RuntimeError("KV history did not preserve the expected value")
            status, metadata = bao.request(tenant, "GET", p + "-kv/metadata/" + probe, token=token)
            expect(status, 200, "read KV metadata")
            if metadata.get("data", {}).get("current_version") != 2:
                raise RuntimeError("KV metadata did not record the current version")
            status, listing = bao.request(tenant, "LIST", p + "-kv/metadata/app", token=token)
            expect(status, 200, "list KV app keys")
            if probe.split("/", 1)[1] not in listing.get("data", {}).get("keys", []):
                raise RuntimeError("KV listing did not include the probe")
            status, _ = bao.request(tenant, "DELETE", p + "-kv/data/" + probe, token=token)
            expect(status, 204, "delete latest KV version")
            status, _ = bao.request(tenant, "GET", p + "-kv/data/" + probe, token=token)
            expect(status, 404, "deleted latest KV version absent")
            status, previous = bao.request(tenant, "GET", p + "-kv/data/" + probe + "?version=1", token=token)
            expect(status, 200, "previous KV version remains readable")
            if previous.get("data", {}).get("data", {}).get("value") != "first":
                raise RuntimeError("deleting the latest version changed the previous value")
        else:
            for method, path, data in (("POST", "data/" + probe, {"data": {"value": "probe"}}),
                                      ("DELETE", "data/" + probe, None), ("GET", "metadata/app/config", None),
                                      ("LIST", "metadata/app", None)):
                status, _ = bao.request(tenant, method, p + "-kv/" + path, data, token)
                expect(status, 403, "KV write/delete/metadata/list denied")
        plaintext = base64.b64encode(b"saas-lab-probe").decode()
        status, encrypted = bao.request(tenant, "POST", p + "-transit/encrypt/app", {"plaintext": plaintext}, token)
        expect(status, 403 if role == "read" else 200, "transit encrypt policy")
        if role != "read":
            status, decrypted = bao.request(tenant, "POST", p + "-transit/decrypt/app", {"ciphertext": encrypted["data"]["ciphertext"]}, token)
            expect(status, 200, "transit decrypt policy")
            if decrypted.get("data", {}).get("plaintext") != plaintext:
                raise RuntimeError("transit round-trip mismatch")
        for method, path, data in (("GET", "sys/mounts", None), ("POST", p + "-kv/destroy/app/config", {"versions": [1]}),
                                   ("GET", p + "-kv/data/forbidden/config", None),
                                   ("GET", p + "-transit/export/encryption-key/app", None),
                                   ("POST", p + "-transit/keys/app/rotate", {})):
            status, _ = bao.request(tenant, method, path, data, token)
            expect(status, 403, "administrative or forbidden path denied")
        for other in TENANTS:
            if other != tenant:
                status, _ = bao.request(other, "GET", p + "-kv/data/app/config", token=token)
                expect(status, 403, "cross-namespace denied")
    finally:
        status, _ = bao.request(tenant, "POST", "auth/token/revoke-self", token=token)
        expect(status, 204, "revoke test token")
        status, _ = bao.request(tenant, "GET", p + "-kv/data/app/config", token=token)
        expect(status, 403, "revoked token cannot read")


def trusted_provider_url(state, url):
    parsed = urllib.parse.urlsplit(url)
    issuer = urllib.parse.urlsplit(state["issuer"])
    local = local_provider(state)
    if state["provider"] in LOCAL_ISSUERS and not local:
        return False
    return parsed.scheme == issuer.scheme and parsed.netloc == issuer.netloc and (parsed.scheme == "https" or local)


def authorization_url(state, url):
    parsed = urllib.parse.urlsplit(url)
    if not trusted_provider_url(state, url):
        raise RuntimeError("OIDC authorization URL is not on the configured provider")
    if state["provider"] == "auth0":
        connection = state.get("connection_name", "")
        if not isinstance(connection, str) or not re.fullmatch(r"[A-Za-z0-9_-]+", connection):
            raise RuntimeError("Auth0 state must name the dedicated user connection")
        # New applications may inherit a different tenant-default database connection.
        query = [(key, value) for key, value in urllib.parse.parse_qsl(parsed.query, keep_blank_values=True) if key != "connection"]
        query.append(("connection", connection))
        return urllib.parse.urlunsplit(parsed._replace(query=urllib.parse.urlencode(query)))
    return url


def oidc(bao, state, tenant, role, expect_denied=False):
    mount = prefix(state) + "-oidc"
    client_nonce = secrets.token_urlsafe(32)
    status, body = bao.request(tenant, "POST", f"auth/{mount}/oidc/auth_url", {"role": role, "redirect_uri": CALLBACK, "client_nonce": client_nonce})
    expect(status, 200, "OIDC auth URL")
    url = authorization_url(state, body.get("data", {}).get("auth_url", ""))
    parsed = urllib.parse.urlsplit(url)
    if not trusted_provider_url(state, url):
        raise RuntimeError("OIDC authorization URL is not on the configured provider")
    expected_state = urllib.parse.parse_qs(parsed.query).get("state", [])
    if len(expected_state) != 1:
        raise RuntimeError("OIDC authorization state is missing")
    callback = {}
    class Handler(http.server.BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass
        def do_GET(self):
            query = urllib.parse.urlsplit(self.path)
            params = urllib.parse.parse_qs(query.query)
            valid = query.path == "/oidc/callback" and params.get("state") == expected_state and len(params.get("code", [])) == 1
            self.send_response(200 if valid else 400)
            self.send_header("Content-Type", "text/plain")
            self.send_header("Cache-Control", "no-store")
            self.end_headers()
            self.wfile.write(b"Return to the terminal. No credentials are displayed here.")
            if valid:
                callback.update(state=params["state"][0], code=params["code"][0])
    with http.server.HTTPServer(("127.0.0.1", 8250), Handler) as server:
        server.timeout = 1
        if not webbrowser.open(url):
            raise RuntimeError("could not open browser; configure a local browser and retry")
        print("Complete the hosted login in your browser; waiting up to five minutes")
        deadline = time.monotonic() + 300
        while not callback and time.monotonic() < deadline:
            server.handle_request()
    if not callback:
        raise RuntimeError("OIDC callback timed out")
    callback["client_nonce"] = client_nonce
    status, body = bao.request(tenant, "GET", f"auth/{mount}/oidc/callback?" + urllib.parse.urlencode(callback))
    token = body.get("auth", {}).get("client_token")
    if expect_denied:
        if token:
            bao.request(tenant, "POST", "auth/token/revoke-self", token=token)
        if status not in (400, 403) or not body.get("binding_denied"):
            raise RuntimeError("wrong-group OIDC login was not rejected by its claim binding")
        print("Wrong-group OIDC login rejected")
        return
    expect(status, 200, "OIDC login")
    if not token:
        raise RuntimeError("OIDC callback returned no OpenBao token")
    test_access(bao, state, tenant, role, body["auth"])
    print(f"{tenant}/{role}: hosted OIDC and ACL boundary checks passed")


def browser_command(local_service=False):
    lab_dir = Path(__file__).resolve().parent.parent
    if os.environ.get("LAB_BROWSER_RUNNER", "docker") == "node":
        return ["node", str(lab_dir / "saas/browser.mjs")]
    if os.environ.get("LAB_BROWSER_RUNNER", "docker") != "docker":
        raise ValueError("LAB_BROWSER_RUNNER must be docker or node")
    tree = ast.parse((lab_dir / "identity.py").read_text())
    image = next(node.value.value for node in tree.body if isinstance(node, ast.Assign)
                 and any(isinstance(target, ast.Name) and target.id == "PLAYWRIGHT_IMAGE" for target in node.targets))
    version = json.loads((lab_dir / "browser/package.json").read_text())["dependencies"]["playwright"]
    if not image.startswith("mcr.microsoft.com/playwright:v" + version + "-noble@sha256:"):
        raise RuntimeError("Playwright image and package versions do not match")
    command = ["docker", "run", "--rm", "-i", "--shm-size=1g", "--user",
               f"{os.getuid()}:{os.getgid()}" if platform.system() == "Linux" else "1000:1000",
               "--volume", str(lab_dir) + ":/lab:ro", "--workdir", "/lab"]
    if local_service and platform.system() == "Linux":
        command += ["--network=host"]
    return command + [image, "node", "/lab/saas/browser.mjs"]


def browser_failure(stderr):
    phases = {"launch Chromium", "load hosted login", "submit password", "submit username", "runner setup",
              "interactive challenge (MFA, CAPTCHA and consent are not bypassed)",
              "await hosted callback (MFA, CAPTCHA and consent are not bypassed)"}
    reasons = {"browser-error", "untrusted-navigation", "interactive-challenge", "repeated-form", "callback-timeout",
               "timeout", "invalid-input-or-runner", "provider-error"}
    try:
        failure = json.loads(stderr.strip().splitlines()[-1])
        if set(failure) == {"phase", "reason"} and failure["phase"] in phases and failure["reason"] in reasons:
            return "hosted browser login failed at " + failure["phase"] + " (" + failure["reason"] + "); verify dedicated test app credentials and MFA/CAPTCHA/consent policy"
    except (ValueError, TypeError, IndexError):
        pass
    return "hosted browser login failed; verify dedicated test app credentials and MFA/CAPTCHA/consent policy"


def browser_auth(bao, state, tenant, role, user):
    mount = prefix(state) + "-oidc"
    nonce = secrets.token_urlsafe(32)
    status, body = bao.request(tenant, "POST", f"auth/{mount}/oidc/auth_url",
                               {"role": role, "redirect_uri": CALLBACK, "client_nonce": nonce})
    expect(status, 200, "browser OIDC auth URL")
    url = authorization_url(state, body.get("data", {}).get("auth_url", ""))
    parsed = urllib.parse.urlsplit(url)
    states = urllib.parse.parse_qs(parsed.query).get("state", [])
    if not trusted_provider_url(state, url) or len(states) != 1:
        raise RuntimeError("untrusted browser authorization URL")
    browser_config = {"provider": state["provider"], "issuer": state["issuer"], "url": url, "state": states[0],
                      "username": user["username"], "password": user["password"]}
    if local_provider(state):
        browser_config.update(route_host=state["browser_route_host"], route_port=state["browser_route_port"])
    result = subprocess.run(browser_command(local_provider(state)), input=json.dumps(browser_config),
                            text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=210)
    if result.returncode:
        raise RuntimeError(browser_failure(result.stderr))
    callback = json.loads(result.stdout)
    if callback.get("state") != states[0] or not isinstance(callback.get("code"), str) or not callback["code"]:
        raise RuntimeError("browser callback state or code invalid")
    status, body = bao.request(tenant, "GET", f"auth/{mount}/oidc/callback?" + urllib.parse.urlencode({
                               "state": states[0], "code": callback["code"], "client_nonce": nonce}))
    return status, body


def browser_login(bao, state, tenant, role, user, expect_denied=False):
    status, body = browser_auth(bao, state, tenant, role, user)
    token = body.get("auth", {}).get("client_token")
    if expect_denied:
        if token:
            bao.request(tenant, "POST", "auth/token/revoke-self", token=token)
        if status not in (400, 403) or not body.get("binding_denied"):
            category = body.get("rejection", "unknown")
            if category not in OIDC_ERROR_MARKERS:
                category = "unknown"
            raise RuntimeError("browser wrong-group login was not rejected by its claim binding"
                               f" (HTTP {status}, token={bool(token)}, category={category})")
    else:
        expect(status, 200, "browser OIDC login")
        if not token:
            raise RuntimeError("browser callback returned no OpenBao token")
        test_access(bao, state, tenant, role, body["auth"])


def test_browser(bao, state):
    validate_state(state)
    if not all(isinstance(user.get(key), str) and user[key] for user in state["users"] for key in ("username", "password")):
        raise ValueError("browser tests need full ephemeral provisioning state with test user credentials")
    for tenant in TENANTS:
        for role in ROLES:
            user = next(user for user in state["users"] if user["namespace"] == tenant and user["role"] == role)
            browser_login(bao, state, tenant, role, user)
            print(f"{tenant}/{role}: hosted Chromium OIDC and ACL checks passed")
        reader = next(user for user in state["users"] if user["namespace"] == tenant and user["role"] == "read")
        browser_login(bao, state, tenant, "admin", reader, expect_denied=True)
        print(f"{tenant}: hosted Chromium rejected read-group login to admin role")
        other = TENANTS[(TENANTS.index(tenant) + 1) % len(TENANTS)]
        foreign_reader = next(user for user in state["users"] if user["namespace"] == other and user["role"] == "read")
        browser_login(bao, state, tenant, "read", foreign_reader, expect_denied=True)
        print(f"{tenant}: hosted Chromium rejected cross-tenant login from {other}")


def test_group_policy_change(bao, state, kube, kube_namespace="obco-lab"):
    name = prefix(state) + "-alpha-read"
    resource = "identitygroups.identity.obco.dev"
    def read_group():
        return json.loads(kube.run("-n", kube_namespace, "get", resource, name, "-o", "json"))
    original = read_group()
    if original.get("metadata", {}).get("labels", {}).get("app.kubernetes.io/part-of") != prefix(state):
        raise RuntimeError("policy-change regression requires the owned lab IdentityGroup")
    if original.get("spec", {}).get("openbaoNamespace") != bao_namespace(state, "alpha") or original["spec"].get("policies") != [prefix(state) + "-read"]:
        raise RuntimeError("policy-change regression requires the unmodified alpha read IdentityGroup")
    original_spec = copy.deepcopy(original["spec"])
    def manifest(current, spec):
        return {"apiVersion": original["apiVersion"], "kind": "IdentityGroup", "metadata": {
                    "name": name, "namespace": kube_namespace, "labels": original["metadata"]["labels"],
                    "resourceVersion": current["metadata"]["resourceVersion"]}, "spec": spec}
    changed_spec = {**original_spec, "policies": []}
    changed = manifest(original, changed_spec)
    reader = next(user for user in state["users"] if user["namespace"] == "alpha" and user["role"] == "read")
    try:
        kube.apply([changed])
        kube.wait([changed])
        status, body = browser_auth(bao, state, "alpha", "read", reader)
        expect(status, 200, "OIDC remains available after removing its data policy")
        auth = body.get("auth", {})
        token = auth.get("client_token")
        if not token:
            raise RuntimeError("policy-change login returned no token")
        try:
            expected = {prefix(state) + "-session"}
            if not auth.get("entity_id") or set(auth.get("identity_policies") or []) != expected or set(auth.get("policies") or []) != expected or auth.get("token_policies"):
                raise RuntimeError("removing the external IdentityGroup policy did not change the identity grant")
            status, _ = bao.request("alpha", "GET", prefix(state) + "-kv/data/app/config", token=token)
            expect(status, 403, "operator IdentityGroup policy removal denies data read")
        finally:
            status, _ = bao.request("alpha", "POST", "auth/token/revoke-self", token=token)
            expect(status, 204, "revoke policy-change test token")
    finally:
        current = read_group()
        # The API omits an empty policies slice; this is the same removed grant.
        current_spec = {**current.get("spec", {}), "policies": current.get("spec", {}).get("policies", [])}
        if current["metadata"].get("uid") != original["metadata"].get("uid") or current_spec not in (changed_spec, original_spec):
            raise RuntimeError("IdentityGroup changed concurrently; inspect the lab IdentityGroup spec before retrying")
        restored = manifest(current, original_spec)
        if current_spec == changed_spec:
            kube.apply([restored])
        kube.wait([restored])
    browser_login(bao, state, "alpha", "read", reader)
    print("alpha/read: operator IdentityGroup policy removal denied data; restoration reconciled and fresh OIDC regained read")


def showcase(bao, state, kube, kube_namespace="obco-lab"):
    test_browser(bao, state)
    test_group_policy_change(bao, state, kube, kube_namespace)


def write_private(path, document):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "w") as output:
        json.dump(document, output, indent=2)
        output.write("\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("render", "apply", "oidc", "browser", "showcase", "jwt"))
    parser.add_argument("--state", required=True)
    parser.add_argument("--output")
    parser.add_argument("--context", default=os.environ.get("LAB_CONTEXT", "kind-openbao-config-operator-lab"))
    parser.add_argument("--kube-namespace", default=os.environ.get("LAB_NAMESPACE", "obco-lab"))
    parser.add_argument("--connection", default=os.environ.get("LAB_CONNECTION", "lab-openbao"))
    parser.add_argument("--tenant", choices=TENANTS)
    parser.add_argument("--role", choices=ROLES)
    parser.add_argument("--expect-denied", action="store_true")
    args = parser.parse_args()
    try:
        state = load_state(args.state)
        if args.command == "render":
            if not args.output:
                parser.error("render requires --output; manifests contain a client secret")
            write_private(args.output, {"apiVersion": "v1", "kind": "List", "items": render(state, args.kube_namespace, args.connection)})
            print("Wrote private CRD manifest; do not commit or upload it")
        elif args.command == "apply":
            kube = Kube(args.context)
            objects = render(state, args.kube_namespace, args.connection)
            namespaces = [o for o in objects if o["kind"] == "BaoNamespace"]
            kube.apply(namespaces)
            kube.wait(namespaces)
            kube.apply([o for o in objects if o["kind"] != "BaoNamespace"])
            kube.wait(objects)
            print("SaaS identity CRDs Ready")
        else:
            if args.command == "oidc" and (not args.tenant or not args.role):
                parser.error("oidc requires --tenant and --role")
            with connect(Kube(args.context), state) as bao:
                if args.command == "jwt":
                    import jwt_lab
                    jwt_lab.run(bao, state, Kube(args.context), args.kube_namespace, args.connection)
                elif args.command == "showcase":
                    showcase(bao, state, Kube(args.context), args.kube_namespace)
                elif args.command == "browser":
                    test_browser(bao, state)
                else:
                    oidc(bao, state, args.tenant, args.role, args.expect_denied)
    except RuntimeError as error:
        raise SystemExit(str(error)) from None
    except (ValueError, OSError, KeyError, subprocess.SubprocessError):
        raise SystemExit("SaaS lab operation failed; details suppressed to protect credentials. Check state permissions, provider setup, CRD readiness and cluster access.") from None


if __name__ == "__main__":
    main()
