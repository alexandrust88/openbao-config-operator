#!/usr/bin/env python3
"""Install the disposable LDAP profile after the LDAP-enabled OpenBao image is running."""
import base64
import json
import os
from pathlib import Path
import secrets
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[3]
CONTEXT = os.environ.get("LAB_CONTEXT", "kind-openbao-config-operator-lab")
NAMESPACE = os.environ.get("LAB_NAMESPACE", "obco-lab")
IDENTITY = os.environ.get("LAB_IDENTITY_NAMESPACE", "obco-lab-identity")
CONNECTION = os.environ.get("LAB_CONNECTION", "lab-openbao")
if not CONTEXT.startswith("kind-"):
    raise SystemExit("LAB_CONTEXT must name the disposable kind cluster")


def kubectl(*args, data=None):
    return subprocess.check_output(
        ["kubectl", "--context", CONTEXT, *args], input=data
    )


def apply(obj):
    kubectl("apply", "-f", "-", data=json.dumps(obj).encode())


def secret(namespace, name, values=None):
    result = kubectl("-n", namespace, "get", "secret", name, "--ignore-not-found", "-o", "json")
    if result:
        return json.loads(result)["data"]
    encoded = {key: base64.b64encode(value).decode() for key, value in values.items()}
    apply({"apiVersion": "v1", "kind": "Secret", "metadata": {"name": name, "namespace": namespace}, "data": encoded})
    return encoded


def main():
    for namespace in (NAMESPACE, IDENTITY):
        apply({"apiVersion": "v1", "kind": "Namespace", "metadata": {"name": namespace}})
    credentials = secret(IDENTITY, "lab-ldap-credentials", {
        "password": secrets.token_urlsafe(24).encode(),
        "jwt": secrets.token_urlsafe(32).encode(),
        "seed": secrets.token_urlsafe(32).encode(),
    })
    apply({"apiVersion": "v1", "kind": "Secret", "metadata": {"name": "lab-ldap-bind", "namespace": NAMESPACE},
           "data": {"password": credentials["password"]}})
    with tempfile.TemporaryDirectory(prefix="obco-lab-ldap-") as temp:
        temp = Path(temp)
        subprocess.run([
            "openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "7",
            "-subj", "/CN=lab-ldap",
            "-addext", "basicConstraints=critical,CA:TRUE",
            "-addext", "keyUsage=critical,digitalSignature,keyEncipherment,keyCertSign",
            "-addext", "extendedKeyUsage=serverAuth",
            "-addext", "subjectKeyIdentifier=hash",
            "-addext", "authorityKeyIdentifier=keyid:always", "-addext",
            f"subjectAltName=DNS:lab-ldap,DNS:lab-ldap.{IDENTITY}.svc,DNS:lab-ldap.{IDENTITY}.svc.cluster.local",
            "-keyout", str(temp / "key.pem"), "-out", str(temp / "cert.pem"),
        ], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
        tls = secret(IDENTITY, "lab-ldap-tls", {"tls.key": (temp / "key.pem").read_bytes(),
                                               "tls.crt": (temp / "cert.pem").read_bytes()})
        checksum = Path(os.environ.get("LAB_LDAP_SHA_FILE", str(ROOT / ".tmp/lab/plugins/sha256"))).read_text().strip()
        values = {"configNamespace": NAMESPACE, "connection": CONNECTION,
                  "directory": {"certificate": base64.b64decode(tls["tls.crt"]).decode()},
                  "plugin": {"sha256": checksum}}
        values_file = temp / "values.json"
        values_file.write_text(json.dumps(values))
        subprocess.run([str(ROOT / "bin/helm"), "upgrade", "--install", "lab-ldap", str(ROOT / "hack/lab/plugins/chart"),
                        "--kube-context", CONTEXT, "--namespace", IDENTITY, "--values", str(values_file),
                        "--wait", "--timeout", "5m"], check=True, stdout=subprocess.DEVNULL)
    for resource in ("plugins.sys.openbaoconfig.io/lab-auth-ldap", "authmounts.sys.openbaoconfig.io/lab-ldap",
                     "policies.sys.openbaoconfig.io/lab-ldap-group", "policies.sys.openbaoconfig.io/lab-ldap-user",
                     "ldapauthconfigs.auth.openbaoconfig.io/lab-ldap", "ldapauthgroups.auth.openbaoconfig.io/lab-ldap-admins",
                     "ldapauthusers.auth.openbaoconfig.io/lab-ldap-admin",
                     "secretsmounts.sys.openbaoconfig.io/lab-ldap-data",
                     "kvsecrets.secrets.openbaoconfig.io/lab-ldap-data-group",
                     "kvsecrets.secrets.openbaoconfig.io/lab-ldap-data-user",
                     "kvsecrets.secrets.openbaoconfig.io/lab-ldap-data-forbidden"):
        kubectl("-n", NAMESPACE, "wait", "--for=condition=Ready", "--timeout=5m", resource)
    print("LDAP profile Ready: official plugin, LDAPS directory, config, group and user mappings")


if __name__ == "__main__":
    main()
