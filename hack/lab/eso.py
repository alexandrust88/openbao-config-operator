#!/usr/bin/env python3
"""Exercise ESO with tenant-scoped Kubernetes auth configured by our operator."""
import argparse
import base64
import json
import os
import subprocess
import secrets
import time

import identity as lab

TENANTS = ("alpha", "beta")


def generated_resources(tenant):
    source = lab.ROOT / "examples/recipes/generated-credentials/credentials.yaml"
    result = subprocess.run([os.environ.get("YQ") or str(lab.ROOT / "bin/yq"),
                             "eval-all", "-o=json", "[.]", str(source)],
                            capture_output=True, text=True, check=True)
    if tenant not in TENANTS:
        raise ValueError("Unknown ESO lab tenant")
    objects = json.loads(result.stdout)
    for item in objects:
        if item["apiVersion"].endswith("openbaoconfig.io/v1alpha1"):
            item["metadata"]["name"] = tenant + item["metadata"]["name"][len("alpha"):]
            item["metadata"]["namespace"] = lab.NAMESPACE
            item["spec"]["namespace"] = "lab-" + tenant
            item["spec"]["connectionRef"]["name"] = lab.CONNECTION
        else:
            item["metadata"]["namespace"] = "obco-lab-" + tenant
    return objects


def projection_matches(current, expected):
    data = current.get("data", {})
    return set(data) == {"password"} and base64.b64decode(data["password"]).decode() == expected


def wait_generated(tenant, expected):
    deadline = time.monotonic() + 120
    while time.monotonic() < deadline:
        raw = lab.kube("-n", "obco-lab-" + tenant, "get", "secret", "generated-application",
                       "--ignore-not-found", "-o", "json")
        if raw and projection_matches(json.loads(raw), expected):
            return
        time.sleep(2)
    raise RuntimeError("Generated credential projection did not converge for " + tenant)


def wait_absent(namespace, kind, name):
    deadline = time.monotonic() + 120
    while time.monotonic() < deadline:
        if not lab.kube("-n", namespace, "get", kind, name, "--ignore-not-found", "-o", "name").strip():
            return
        time.sleep(2)
    raise RuntimeError("Credential lifecycle cleanup did not converge")


def read_generated(bao, tenant, token):
    status, response = bao.request(tenant, "GET", "kv/data/app/generated", token=token)
    lab.expect(status, 200, "read generated credential")
    return response["data"]


def test_generated(bao, tenant, token):
    objects = generated_resources(tenant)
    managed = objects[:2]
    lab.apply(objects)
    lab.wait_ready(managed)
    original = read_generated(bao, tenant, token)
    password = original["data"]["password"]
    wait_generated(tenant, password)
    # Seed a second key as disposable data: the ESO projection must never copy it.
    admin = lab.read_secret("lab-bootstrap", lab.NAMESPACE)["token"]
    extra = secrets.token_urlsafe(24)
    status, _ = bao.request(tenant, "POST", "kv/data/app/generated",
                            {"options": {"cas": original["metadata"]["version"]},
                             "data": {"password": password, "excluded": extra}}, admin)
    lab.expect(status, 200, "seed excluded credential key")
    stable = read_generated(bao, tenant, token)
    resource = managed[1]
    resource["spec"].pop("passwordPolicyName")
    resource["spec"]["generator"] = {"length": 48}
    lab.apply(managed)
    lab.wait_ready(managed)
    unchanged = read_generated(bao, tenant, token)
    if unchanged != stable:
        raise RuntimeError("Generator configuration unexpectedly changed existing credential")
    wait_generated(tenant, password)
    resource["spec"]["refreshNonce"] = secrets.token_hex(12)
    lab.apply(managed)
    lab.wait_ready(managed)
    rotated = read_generated(bao, tenant, token)
    new_password = rotated["data"]["password"]
    if new_password == password or rotated["data"].get("excluded") != extra:
        raise RuntimeError("Explicit credential rotation failed or changed an unrelated key")
    wait_generated(tenant, new_password)
    lab.kube("-n", "obco-lab-" + tenant, "delete", "secret", "generated-application")
    wait_generated(tenant, new_password)
    if read_generated(bao, tenant, token) != rotated:
        raise RuntimeError("Projection recovery changed the OpenBao credential")
    other = "beta" if tenant == "alpha" else "alpha"
    status, _ = bao.request(other, "GET", "kv/data/app/generated", token=token)
    lab.expect(status, 403, "generated credential cross-tenant read denied")
    status, _ = bao.request(tenant, "POST", "kv/data/app/generated",
                            {"data": {"password": secrets.token_urlsafe(24)}}, token)
    lab.expect(status, 403, "generated credential write denied")
    namespace = "obco-lab-" + tenant
    try:
        lab.kube("-n", namespace, "delete", "externalsecret", "generated-application")
        wait_absent(namespace, "secret", "generated-application")
        lab.kube("-n", lab.NAMESPACE, "delete", "randomsecret", resource["metadata"]["name"])
        wait_absent(lab.NAMESPACE, "randomsecret", resource["metadata"]["name"])
        if read_generated(bao, tenant, token) != rotated:
            raise RuntimeError("Orphan deletion changed the retained OpenBao credential")
    finally:
        lab.apply(objects)
        lab.wait_ready(managed)
    wait_generated(tenant, new_password)
    if read_generated(bao, tenant, token) != rotated:
        raise RuntimeError("Re-adoption changed the retained OpenBao credential")
    print(tenant + ": generated credential stability, rotation, projection, recovery, deletion and denials passed")


def up():
    ca = json.loads(lab.kube("-n", "openbao", "get", "configmap", "openbao-ca", "-o", "json"))["data"]
    objects = []
    for tenant in TENANTS:
        namespace = "obco-lab-" + tenant
        lab.apply([lab.obj("ServiceAccount", "eso-reader", namespace), lab.obj("ConfigMap", "openbao-ca", namespace, data=ca)])
        objects += [lab.cr("Policy", tenant + "-eso-reader", {"name": "lab-eso-reader", "policy":
                           'path "kv/data/app/*" { capabilities = ["read"] }\n'
                           'path "auth/token/lookup-self" { capabilities = ["read"] }\n'
                           'path "auth/token/revoke-self" { capabilities = ["update"] }'}, tenant),
                    lab.cr("AuthMount", tenant + "-kubernetes", {"path": "kubernetes", "type": "kubernetes"}, tenant),
                    lab.cr("KubernetesAuthConfig", tenant + "-kubernetes", {"mountPath": "kubernetes",
                           "kubernetesHost": "https://kubernetes.default.svc:443"}, tenant),
                    lab.cr("KubernetesAuthRole", tenant + "-eso-reader", {"mountPath": "kubernetes", "name": "eso-reader",
                           "boundServiceAccountNames": ["eso-reader"], "boundServiceAccountNamespaces": [namespace],
                           "audience": "openbao", "tokenPolicies": ["lab-eso-reader"], "tokenTTL": "10m",
                           "tokenMaxTTL": "15m", "tokenNoDefaultPolicy": True}, tenant)]
    lab.apply(objects)
    lab.wait_ready(objects)
    stores = []
    for tenant in TENANTS:
        namespace = "obco-lab-" + tenant
        stores += [lab.obj("SecretStore", "openbao", namespace, api="external-secrets.io/v1", spec={
                     "refreshInterval": 10, "provider": {"vault": {"server": "https://openbao-active.openbao.svc:8200", "path": "kv", "version": "v2",
                     "namespace": "lab-" + tenant, "caProvider": {"type": "ConfigMap", "name": "openbao-ca", "key": "ca.crt"},
                     "auth": {"kubernetes": {"mountPath": "kubernetes", "role": "eso-reader",
                         "serviceAccountRef": {"name": "eso-reader", "audiences": ["openbao"]}}}}}}),
                   lab.obj("ExternalSecret", "application", namespace, api="external-secrets.io/v1", spec={
                     "refreshInterval": "5s", "secretStoreRef": {"name": "openbao", "kind": "SecretStore"},
                     "target": {"name": "application", "creationPolicy": "Owner"},
                     "data": [{"secretKey": "value", "remoteRef": {"key": "app/config", "property": "value"}}]})]
    lab.apply(stores)
    for tenant in TENANTS:
        lab.apply(generated_resources(tenant))
    print("ESO tenant stores, generated credentials and ExternalSecrets applied")


def wait_value(tenant, expected):
    deadline = time.monotonic() + 120
    while time.monotonic() < deadline:
        raw = lab.kube("-n", "obco-lab-" + tenant, "get", "secret", "application", "--ignore-not-found", "-o", "json")
        if raw and base64.b64decode(json.loads(raw).get("data", {}).get("value", "")).decode() == expected:
            return
        time.sleep(2)
    raise RuntimeError("ESO did not sync the expected value for " + tenant)


def test():
    with lab.forward("openbao", "openbao-active", 8200) as port:
        bao = lab.Bao(port)
        for tenant in TENANTS:
            original = lab.read_secret(tenant + "-seed", lab.NAMESPACE)["value"]
            wait_value(tenant, original)
            jwt = lab.kube("-n", "obco-lab-" + tenant, "create", "token", "eso-reader", "--audience=openbao", "--duration=10m").strip()
            status, response = bao.request(tenant, "POST", "auth/kubernetes/login", {"role": "eso-reader", "jwt": jwt})
            lab.expect(status, 200, "ESO ServiceAccount login")
            token = response["auth"]["client_token"]
            try:
                status, _ = bao.request(tenant, "GET", "kv/data/app/config", token=token)
                lab.expect(status, 200, "ESO own tenant read")
                other = "beta" if tenant == "alpha" else "alpha"
                status, _ = bao.request(other, "POST", "auth/kubernetes/login", {"role": "eso-reader", "jwt": jwt})
                lab.expect(status, 403, "ESO other tenant login denied")
                status, _ = bao.request(other, "GET", "kv/data/app/config", token=token)
                lab.expect(status, 403, "ESO other tenant read denied")
                status, _ = bao.request(tenant, "POST", "kv/data/app/config", {"data": {"value": "forbidden"}}, token)
                lab.expect(status, 403, "ESO write denied")
                test_generated(bao, tenant, token)
            finally:
                status, _ = bao.request(tenant, "POST", "auth/token/revoke-self", token=token)
                lab.expect(status, 204, "revoke ESO test token")
            # A new value on each run proves an actual change propagates through both controllers.
            rotated = "lab-rotation-" + secrets.token_hex(12)
            lab.apply([lab.secret(tenant + "-seed", lab.NAMESPACE, {"value": rotated})])
            wait_value(tenant, rotated)
            print(tenant + ": ESO initial sync, rotation and paired cross-tenant/write denials passed")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("up", "test"))
    try:
        {"up": up, "test": test}[parser.parse_args().command]()
    except RuntimeError as error:
        raise SystemExit(str(error)) from None
