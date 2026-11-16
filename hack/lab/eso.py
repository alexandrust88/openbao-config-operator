#!/usr/bin/env python3
"""Exercise ESO with tenant-scoped Kubernetes auth configured by our operator."""
import argparse
import base64
import json
import secrets
import time

import identity as lab

TENANTS = ("alpha", "beta")


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
    print("ESO tenant stores and ExternalSecrets applied")


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
