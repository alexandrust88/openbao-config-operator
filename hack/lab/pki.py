#!/usr/bin/env python3
"""Exercise cert-manager issuance through operator-managed OpenBao PKI and Kubernetes auth."""
import base64
import json
import os
import socket
import ssl
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request

from bootstrap import apply, connection, kubectl, namespace

APP_NAMESPACE = "obco-lab-pki"
BAO_NAMESPACE = "lab-pki"
LABEL = {"lab.obco.dev/profile": "pki"}


def resource(group, kind, name, spec):
    full_spec = {"connectionRef": {"name": connection}, "openbaoNamespace": BAO_NAMESPACE,
                 "creationPolicy": "CreateOrAdopt", "deletionPolicy": "Orphan", **spec}
    if not full_spec["openbaoNamespace"]:
        del full_spec["openbaoNamespace"]
    apply({"apiVersion": group + ".obco.dev/v1alpha1", "kind": kind,
           "metadata": {"name": name, "namespace": namespace, "labels": LABEL},
           "spec": full_spec})


def wait_ready(kind, name, ns=namespace):
    deadline = time.monotonic() + 180
    while time.monotonic() < deadline:
        obj = json.loads(kubectl("-n", ns, "get", kind, name, "-o", "json"))
        conditions = obj.get("status", {}).get("conditions", [])
        ready = [c for c in conditions if c["type"] == "Ready"]
        current = all(c["status"] == "True" and c.get("observedGeneration") == obj["metadata"]["generation"]
                      for c in conditions if c["type"] in ("Ready", "Synced"))
        if ready and ready[0]["status"] == "True" and current:
            return
        time.sleep(2)
    raise RuntimeError(f"{kind}/{name} did not become Ready for its current generation")


def up():
    apply({"apiVersion": "v1", "kind": "Namespace", "metadata": {"name": APP_NAMESPACE}})
    resource("sys", "BaoNamespace", "lab-pki", {"openbaoNamespace": "", "name": BAO_NAMESPACE})
    wait_ready("baonamespace", "lab-pki")
    for name in ("root", "intermediate"):
        resource("sys", "SecretsMount", "pki-" + name,
                 {"path": "pki-" + name, "type": "pki", "maxLeaseTTL": "8760h"})
        wait_ready("secretsmount", "pki-" + name)
    resource("secrets", "PKIIssuer", "pki-root", {
        "mountPath": "pki-root", "name": "lab-root", "type": "root",
        "key": {"source": "internal", "type": "ec", "bits": 256},
        "certificate": {"commonName": "OBCO disposable lab root", "ttl": "8760h", "maxPathLength": 1}})
    wait_ready("pkiissuer", "pki-root")
    resource("secrets", "PKIIssuer", "pki-intermediate", {
        "mountPath": "pki-intermediate", "name": "lab-intermediate", "type": "intermediate",
        "key": {"source": "internal", "type": "ec", "bits": 256},
        "certificate": {"commonName": "OBCO disposable lab intermediate", "ttl": "720h", "maxPathLength": 0},
        "signer": {"issuerRef": {"name": "pki-root"}}})
    wait_ready("pkiissuer", "pki-intermediate")
    resource("secrets", "PKIRole", "pki-localhost", {
        "mountPath": "pki-intermediate", "name": "localhost",
        "issuer": {"issuerRef": {"name": "pki-intermediate"}},
        "allowedDomains": ["localhost"], "allowLocalhost": True,
        "allowBareDomains": True, "allowSubdomains": True,
        "allowIPSANs": False, "allowWildcardCertificates": False,
        "keyType": "ec", "keyBits": 256, "maxTTL": "24h", "ttl": "1h"})
    resource("sys", "AuthMount", "pki-kubernetes", {"path": "cert-manager", "type": "kubernetes"})
    wait_ready("authmount", "pki-kubernetes")
    resource("auth", "KubernetesAuthConfig", "pki-kubernetes", {
        "mountPath": "cert-manager", "kubernetesHost": "https://kubernetes.default.svc:443"})
    resource("sys", "ACLPolicy", "pki-sign", {"name": "cert-manager-sign",
        "policy": 'path "pki-intermediate/sign/localhost" { capabilities = ["update"] }'})
    resource("auth", "KubernetesAuthRole", "pki-cert-manager", {
        "mountPath": "cert-manager", "name": "cert-manager",
        "boundServiceAccountNames": ["cert-manager-issuer"],
        "boundServiceAccountNamespaces": [APP_NAMESPACE],
        "audience": "vault://" + APP_NAMESPACE + "/openbao",
        "policyRefs": [{"name": "pki-sign"}], "tokenTTL": "5m", "tokenMaxTTL": "10m"})
    for kind, name in [("pkirole", "pki-localhost"), ("kubernetesauthconfig", "pki-kubernetes"),
                       ("aclpolicy.sys.obco.dev", "pki-sign"), ("kubernetesauthrole", "pki-cert-manager")]:
        wait_ready(kind, name)
    apply({"apiVersion": "v1", "kind": "ServiceAccount", "metadata": {"name": "cert-manager-issuer", "namespace": APP_NAMESPACE}})
    apply({"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "Role", "metadata": {"name": "issuer-token", "namespace": APP_NAMESPACE},
           "rules": [{"apiGroups": [""], "resources": ["serviceaccounts/token"], "resourceNames": ["cert-manager-issuer"], "verbs": ["create"]}]})
    apply({"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "RoleBinding", "metadata": {"name": "issuer-token", "namespace": APP_NAMESPACE},
           "roleRef": {"apiGroup": "rbac.authorization.k8s.io", "kind": "Role", "name": "issuer-token"},
           "subjects": [{"kind": "ServiceAccount", "name": "cert-manager", "namespace": "cert-manager"}]})
    ca = json.loads(kubectl("-n", "openbao", "get", "configmap", "openbao-ca", "-o", "json"))["data"]["ca.crt"]
    apply({"apiVersion": "cert-manager.io/v1", "kind": "Issuer", "metadata": {"name": "openbao", "namespace": APP_NAMESPACE}, "spec": {"vault": {
        "server": "https://openbao-active.openbao.svc:8200", "namespace": BAO_NAMESPACE,
        "path": "pki-intermediate/sign/localhost", "caBundle": base64.b64encode(ca.encode()).decode(),
        "auth": {"kubernetes": {"mountPath": "/v1/auth/cert-manager", "role": "cert-manager",
                                 "serviceAccountRef": {"name": "cert-manager-issuer"}}}}}})
    wait_ready("issuer", "openbao", APP_NAMESPACE)
    apply({"apiVersion": "cert-manager.io/v1", "kind": "Certificate", "metadata": {"name": "localhost", "namespace": APP_NAMESPACE}, "spec": {
        "secretName": "localhost-tls", "commonName": "localhost", "dnsNames": ["localhost", "app.localhost"],
        "duration": "1h", "renewBefore": "20m", "privateKey": {"algorithm": "ECDSA", "size": 256, "rotationPolicy": "Always"},
        "issuerRef": {"name": "openbao"}}})
    wait_ready("certificate", "localhost", APP_NAMESPACE)
    print("PKI lab Ready: operator root/intermediate + cert-manager localhost certificate")


def get_tls():
    return json.loads(kubectl("-n", APP_NAMESPACE, "get", "secret", "localhost-tls", "-o", "json"))["data"]


def check_tls(data, expected_root, expected_intermediate):
    with tempfile.TemporaryDirectory(prefix="obco-pki-") as tmp:
        files = {key: base64.b64decode(data[key]) for key in ("tls.crt", "tls.key")}
        files.update({"root.crt": expected_root, "intermediate.crt": expected_intermediate})
        for name, contents in files.items():
            path = os.path.join(tmp, name)
            with open(path, "wb") as stream:
                stream.write(contents)
            os.chmod(path, 0o600)
        # Only the expected issuers are trusted; the returned Secret's CA/bundle cannot select its own authority.
        subprocess.run(["openssl", "x509", "-in", tmp + "/tls.crt", "-out", tmp + "/leaf.crt"], check=True)
        subprocess.run(["openssl", "verify", "-CAfile", tmp + "/root.crt", "-no-CApath",
                        tmp + "/intermediate.crt"], check=True, stdout=subprocess.DEVNULL)
        for host in ("localhost", "app.localhost"):
            subprocess.run(["openssl", "verify", "-CAfile", tmp + "/root.crt", "-no-CApath",
                            "-untrusted", tmp + "/intermediate.crt", "-verify_hostname", host,
                            tmp + "/leaf.crt"], check=True, stdout=subprocess.DEVNULL)
        subprocess.run(["openssl", "verify", "-partial_chain", "-trusted", tmp + "/intermediate.crt",
                        "-no-CAfile", "-no-CApath", tmp + "/leaf.crt"], check=True, stdout=subprocess.DEVNULL)
        cert_key = subprocess.check_output(["openssl", "x509", "-in", tmp + "/leaf.crt", "-pubkey", "-noout"])
        private_key = subprocess.check_output(["openssl", "pkey", "-in", tmp + "/tls.key", "-pubout"])
        if cert_key != private_key:
            raise RuntimeError("Certificate and private key do not match")


def test_permissions():
    jwt = kubectl("-n", APP_NAMESPACE, "create", "token", "cert-manager-issuer",
                  "--audience=vault://" + APP_NAMESPACE + "/openbao", "--duration=10m").decode().strip()
    ca = json.loads(kubectl("-n", "openbao", "get", "configmap", "openbao-ca", "-o", "json"))["data"]["ca.crt"]
    requests = json.loads(kubectl("-n", APP_NAMESPACE, "get", "certificaterequests", "-o", "json"))["items"]
    requests = [r for r in requests if any(o.get("name") == "localhost" for o in r["metadata"].get("ownerReferences", []))]
    if not requests:
        raise RuntimeError("No CertificateRequest exists for the localhost certificate")
    csr = base64.b64decode(requests[-1]["spec"]["request"]).decode()
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
    forward = subprocess.Popen(["kubectl", "--context", os.environ["LAB_CONTEXT"], "-n", "openbao",
                                "port-forward", "--address=127.0.0.1", "svc/openbao-active", f"{port}:8200"],
                               stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    try:
        deadline = time.monotonic() + 30
        while True:
            try:
                with socket.create_connection(("127.0.0.1", port), timeout=1):
                    break
            except OSError:
                if forward.poll() is not None or time.monotonic() > deadline:
                    raise RuntimeError("OpenBao port-forward failed") from None
                time.sleep(0.2)
        tls = ssl.create_default_context(cadata=ca)
        expected_chain = []
        for name in ("pki-root", "pki-intermediate"):
            wait_ready("pkiissuer", name)
            issuer = json.loads(kubectl("-n", namespace, "get", "pkiissuer", name, "-o", "json"))
            issuer_id = issuer["status"].get("id")
            if not issuer_id or issuer["spec"].get("openbaoNamespace") != BAO_NAMESPACE:
                raise RuntimeError("Expected lab issuer identity is missing or in another namespace")
            path = issuer["spec"]["mountPath"] + "/issuer/" + urllib.parse.quote(issuer_id, safe="") + "/pem"
            req = urllib.request.Request(f"https://localhost:{port}/v1/" + path,
                                         headers={"X-Vault-Namespace": BAO_NAMESPACE})
            with urllib.request.urlopen(req, context=tls, timeout=30) as response:
                expected_chain.append(response.read())

        def request(path, payload, token=None):
            headers = {"Content-Type": "application/json", "X-Vault-Namespace": BAO_NAMESPACE}
            if token:
                headers["X-Vault-Token"] = token
            req = urllib.request.Request(f"https://localhost:{port}/v1/" + path,
                                         data=json.dumps(payload).encode(), headers=headers)
            try:
                with urllib.request.urlopen(req, context=tls, timeout=30) as response:
                    body = response.read()
                    return response.status, json.loads(body) if body else None
            except urllib.error.HTTPError as error:
                return error.code, None

        status, result = request("auth/cert-manager/login", {"role": "cert-manager", "jwt": jwt})
        if status != 200:
            raise RuntimeError(f"Issuer ServiceAccount login returned HTTP {status}")
        token = result["auth"]["client_token"]
        try:
            status, _ = request("pki-intermediate/sign/localhost", {"csr": csr, "ttl": "1h"}, token)
            if status != 200:
                raise RuntimeError(f"Allowed CSR signing returned HTTP {status}")
            status, _ = request("sys/policies/acl/lab-forbidden", {"policy": 'path "*" { capabilities = ["deny"] }'}, token)
            if status != 403:
                raise RuntimeError(f"Issuer policy mutation returned HTTP {status}, expected 403")
            print("PASS: issuer ServiceAccount can sign but cannot change policies")
        finally:
            request("auth/token/revoke-self", {}, token)
        return expected_chain
    finally:
        forward.terminate()
        forward.wait(timeout=10)


def test():
    wait_ready("certificate", "localhost", APP_NAMESPACE)
    expected_root, expected_intermediate = test_permissions()
    original = get_tls()
    check_tls(original, expected_root, expected_intermediate)
    # Losing an application TLS Secret must trigger issuance and key rotation without exporting the CA key.
    kubectl("-n", APP_NAMESPACE, "delete", "secret", "localhost-tls")
    deadline = time.monotonic() + 180
    while time.monotonic() < deadline:
        result = kubectl("-n", APP_NAMESPACE, "get", "secret", "localhost-tls", "--ignore-not-found", "-o", "json")
        if result.strip():
            replacement = json.loads(result)["data"]
            if replacement["tls.crt"] != original["tls.crt"] and replacement["tls.key"] != original["tls.key"]:
                check_tls(replacement, expected_root, expected_intermediate)
                wait_ready("certificate", "localhost", APP_NAMESPACE)
                print("PASS: localhost SANs, trust chain, matching key, and TLS Secret recovery with key rotation")
                return
        time.sleep(2)
    raise RuntimeError("cert-manager did not replace the deleted TLS Secret within 180s")


if __name__ == "__main__":
    {"up": up, "test": test}[sys.argv[1]]()
