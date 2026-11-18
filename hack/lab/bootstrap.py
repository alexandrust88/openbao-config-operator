#!/usr/bin/env python3
"""Connect lab CRDs to the disposable e2e OpenBao server without logging credentials."""
import json
import os
import subprocess

context = os.environ["LAB_CONTEXT"]
if not context.startswith("kind-"):
    raise SystemExit("The lab requires a kind context")
namespace = os.environ["LAB_NAMESPACE"]
connection = os.environ["LAB_CONNECTION"]


def kubectl(*args, data=None):
    return subprocess.check_output(
        ["kubectl", "--context", context, *args],
        input=json.dumps(data).encode() if data is not None else None,
    )


def apply(obj):
    kubectl("apply", "--server-side", "--field-manager=obco-lab", "-f", "-", data=obj)


def main():
    token = json.loads(kubectl("-n", "openbao", "get", "secret", "openbao-e2e", "-o", "json"))["data"]["token"]
    ca = json.loads(kubectl("-n", "openbao", "get", "configmap", "openbao-ca", "-o", "json"))["data"]["ca.crt"]
    apply({"apiVersion": "v1", "kind": "Namespace", "metadata": {"name": namespace}})
    for kind, name, data in [("Secret", "lab-bootstrap", {"token": token}), ("ConfigMap", "openbao-ca", {"ca.crt": ca})]:
        apply({"apiVersion": "v1", "kind": kind, "metadata": {"name": name, "namespace": namespace}, "data": data})
    apply({"apiVersion": "core.obco.dev/v1alpha1", "kind": "BaoServer", "metadata": {"name": connection}, "spec": {
        "address": "https://openbao-active.openbao.svc:8200",
        "caBundle": {"configMapRef": {"name": "openbao-ca", "namespace": namespace, "key": "ca.crt"}},
        "allowedNamespaces": {"matchLabels": {"kubernetes.io/metadata.name": namespace}},
        "allowedAudiences": ["openbao"], "requestTimeout": "3m",
    }})
    apply({"apiVersion": "core.obco.dev/v1alpha1", "kind": "BaoConnection", "metadata": {"name": connection, "namespace": namespace}, "spec": {
        "serverRef": {"name": connection}, "auth": {"token": {"secretRef": {"name": "lab-bootstrap", "key": "token"}}},
    }})
    kubectl("-n", namespace, "wait", "--for=condition=Ready", "baoconnection/" + connection, "--timeout=180s")
    print("Lab bootstrap connection Ready")


if __name__ == "__main__":
    main()
