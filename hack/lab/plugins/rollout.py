#!/usr/bin/env python3
"""Preserve raft/TLS settings while adding the immutable plugin image and directory."""
import json
import os
from pathlib import Path
import re
import subprocess

context = os.environ["LAB_CONTEXT"]
if not context.startswith("kind-"):
    raise SystemExit("The LDAP lab requires a kind context")
root = Path(__file__).resolve().parents[3]


def kubectl(*args, data=None):
    return subprocess.check_output(["kubectl", "--context", context, *args], input=data)


config = json.loads(kubectl("-n", "openbao", "get", "configmap", "openbao", "-o", "json"))
server = config["data"]["server.hcl"]
existing = re.findall(r'^\s*plugin_directory\s*=\s*"([^"]+)"', server, re.MULTILINE)
if existing and existing != ["/plugins"]:
    raise SystemExit("Existing plugin_directory differs from /plugins; refusing to replace it")
changed = not existing
if changed:
    server += '\nplugin_directory = "/plugins"\n'
    patch = {"data": {"server.hcl": server}}
    kubectl("-n", "openbao", "patch", "configmap", "openbao", "--type=merge", "--patch-file=/dev/stdin",
            data=json.dumps(patch).encode())
image = (root / ".tmp/lab/plugins/image").read_text().strip()
kubectl("-n", "openbao", "set", "image", "statefulset/openbao", "openbao=" + image)
if changed:
    kubectl("-n", "openbao", "rollout", "restart", "statefulset/openbao")
kubectl("-n", "openbao", "rollout", "status", "statefulset/openbao", "--timeout=5m")
kubectl("-n", "openbao", "wait", "pod", "-l", "app.kubernetes.io/name=openbao", "--for=condition=Ready", "--timeout=180s")
print("LDAP plugin image and directory configured on the raft pods")
