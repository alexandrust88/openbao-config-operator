#!/usr/bin/env python3
"""Exercise operator OIDC groups against the existing disposable Keycloak lab."""
import argparse
import contextlib
import platform
import os

import identity
from saas import operator_lab as lab


def state_from_credentials(values):
    return {"provider": "keycloak", "complete": True, "issuer": identity.ISSUER, "groups_claim": "groups",
            "callback_uri": lab.CALLBACK, "oidc_client_id": "openbao", "oidc_client_secret": values["client-secret"],
            "users": [{"namespace": tenant, "role": role, "username": f"{tenant}-{role}",
                       "password": values[f"{tenant}-{role}"], "group": f"{tenant}-{role}s"}
                      for tenant in lab.TENANTS for role in lab.ROLES]}


def apply():
    state = state_from_credentials(identity.credentials())
    kube = lab.Kube(identity.CONTEXT)
    objects = lab.render(state, identity.NAMESPACE, identity.CONNECTION)
    namespaces = [obj for obj in objects if obj["kind"] == "BaoNamespace"]
    kube.apply(namespaces)
    kube.wait(namespaces)
    kube.apply([obj for obj in objects if obj["kind"] != "BaoNamespace"])
    kube.wait(objects)
    print("Keycloak OIDC showcase CRDs Ready")


@contextlib.contextmanager
def connect():
    state = state_from_credentials(identity.credentials())
    kube = lab.Kube(identity.CONTEXT)
    with identity.forward(identity.IDENTITY_NS, "keycloak", 8080) as port, lab.connect(kube, state) as bao:
        state["browser_route_host"] = "127.0.0.1" if platform.system() == "Linux" or os.environ.get("LAB_BROWSER_RUNNER") == "node" else "host.docker.internal"
        state["browser_route_port"] = port
        yield bao, state


def test():
    with connect() as (bao, state):
        lab.showcase(bao, state, lab.Kube(identity.CONTEXT), identity.NAMESPACE)
    print("Keycloak: 17 OIDC group, ACL and operator grant-change journeys passed")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("apply", "test"))
    args = parser.parse_args()
    try:
        {"apply": apply, "test": test}[args.command]()
    except RuntimeError as error:
        raise SystemExit(str(error)) from None
    except (ValueError, KeyError):
        raise SystemExit("Keycloak OIDC regression failed; inspect CRD readiness and the safe browser diagnostics") from None
