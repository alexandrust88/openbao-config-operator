#!/usr/bin/env python3
"""Prove LDAP login and group/user policy boundaries without using an OpenBao admin token."""
import base64
import json
import os
import ssl
import subprocess
import urllib.error
import urllib.request


def main():
    context = os.environ.get("LAB_CONTEXT", "kind-openbao-config-operator-lab")
    if not context.startswith("kind-"):
        raise SystemExit("LAB_CONTEXT must name the disposable kind cluster")
    address = os.environ["LAB_BAO_ADDR"].rstrip("/")
    if not address.startswith("https://"):
        raise SystemExit("LAB_BAO_ADDR must use HTTPS")
    tls = ssl.create_default_context(cafile=os.environ["LAB_BAO_CACERT"])
    namespace = os.environ.get("LAB_IDENTITY_NAMESPACE", "obco-lab-identity")
    raw = subprocess.check_output(["kubectl", "--context", context, "-n", namespace, "get", "secret",
                                   "lab-ldap-credentials", "-o", "json"])
    password = base64.b64decode(json.loads(raw)["data"]["password"]).decode()

    def request(path, payload=None, token=None):
        headers = {"Content-Type": "application/json"}
        if token:
            headers["X-Vault-Token"] = token
        req = urllib.request.Request(address + "/v1/" + path,
                                     data=None if payload is None else json.dumps(payload).encode(), headers=headers)
        try:
            with urllib.request.urlopen(req, context=tls, timeout=30) as response:
                body = response.read()
                return response.status, json.loads(body) if body else {}
        except urllib.error.HTTPError as error:
            return error.code, {}

    code, _ = request("auth/lab-ldap/login/admin", {"password": password + "-incorrect"})
    if code not in (400, 403):
        raise SystemExit(f"Incorrect LDAP password was not rejected: HTTP {code}")
    code, result = request("auth/lab-ldap/login/admin", {"password": password})
    if code != 200 or not result.get("auth", {}).get("client_token"):
        raise SystemExit(f"LDAP login did not return a token: HTTP {code}")
    token = result["auth"]["client_token"]
    try:
        policies = set(result["auth"].get("policies", []))
        if not {"lab-ldap-group", "lab-ldap-user"}.issubset(policies):
            raise SystemExit("LDAP login did not apply both directory-group and explicit-user policies")
        allowed = ["lab-ldap-data/data/group", "lab-ldap-data/data/user"]
        code, result = request("sys/capabilities-self", {"paths": allowed + ["sys/mounts"]}, token)
        if code != 200:
            raise SystemExit(f"Cannot inspect LDAP token capabilities: HTTP {code}")
        data = result.get("data", {})
        if any(data.get(path) != ["read"] for path in allowed) or data.get("sys/mounts") != ["deny"]:
            raise SystemExit("LDAP group/user capabilities or admin-denial boundary did not match")
        for name in ("group", "user"):
            code, result = request("lab-ldap-data/data/" + name, token=token)
            if code != 200 or result.get("data", {}).get("data", {}).get("value") != f"lab-ldap-{name}-fixture":
                raise SystemExit(f"LDAP allowed {name} read did not return its fixture: HTTP {code}")
        code, _ = request("lab-ldap-data/data/forbidden", token=token)
        if code != 403:
            raise SystemExit(f"LDAP token was not denied the seeded forbidden secret: HTTP {code}")
        code, _ = request("sys/mounts", token=token)
        if code != 403:
            raise SystemExit(f"LDAP token was not denied the admin API: HTTP {code}")
        print("PASS: bad LDAP password rejected; LDAPS login reads group/user fixtures; forbidden secret and admin API denied")
    finally:
        request("auth/token/revoke-self", {}, token)


if __name__ == "__main__":
    main()
