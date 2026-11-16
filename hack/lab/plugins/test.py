#!/usr/bin/env python3
"""Forward the active node over verified TLS, run LDAP checks, and clean up."""
import json
import os
import socket
import subprocess
import tempfile
import time

from verify import main

context = os.environ["LAB_CONTEXT"]
if not context.startswith("kind-"):
    raise SystemExit("The LDAP lab requires a kind context")
ca = json.loads(subprocess.check_output(["kubectl", "--context", context, "-n", "openbao",
                                        "get", "configmap", "openbao-ca", "-o", "json"]))["data"]["ca.crt"]
with socket.socket() as listener:
    listener.bind(("127.0.0.1", 0))
    port = listener.getsockname()[1]
forward = subprocess.Popen(["kubectl", "--context", context, "-n", "openbao", "port-forward",
                            "--address=127.0.0.1", "svc/openbao-active", f"{port}:8200"],
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
try:
    deadline = time.monotonic() + 30
    while True:
        try:
            with socket.create_connection(("127.0.0.1", port), timeout=1):
                break
        except OSError:
            if forward.poll() is not None or time.monotonic() > deadline:
                raise SystemExit("OpenBao port-forward failed") from None
            time.sleep(0.2)
    with tempfile.NamedTemporaryFile(mode="w", suffix=".crt") as trust:
        trust.write(ca)
        trust.flush()
        os.environ["LAB_BAO_ADDR"] = f"https://localhost:{port}"
        os.environ["LAB_BAO_CACERT"] = trust.name
        main()
finally:
    forward.terminate()
    forward.wait(timeout=10)
