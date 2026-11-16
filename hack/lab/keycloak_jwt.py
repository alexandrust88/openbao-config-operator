#!/usr/bin/env python3
"""Run the shared JWT discovery/JWKS matrix with the local Keycloak users."""

import argparse
import sys
import urllib.error
from pathlib import Path

import identity

sys.path.insert(0, str(Path(__file__).resolve().parent / "saas"))
from jwt_lab import check, render
import operator_lab as lab


def main():
    import keycloak_oidc
    values = identity.credentials()
    state = keycloak_oidc.state_from_credentials(values)
    state["jwks_url"] = state["issuer"] + "/protocol/openid-connect/certs"
    kube = lab.Kube(identity.CONTEXT)
    objects = render(state, identity.NAMESPACE, identity.CONNECTION)
    kube.apply(objects)
    kube.wait(objects)
    with identity.forward(identity.IDENTITY_NS, "keycloak", 8080) as port, lab.connect(kube, state) as bao:
        tokens = {(tenant, role): identity.keycloak_token(port, values, user=f"{tenant}-{role}")
                  for tenant in lab.TENANTS for role in lab.ROLES}
        check(bao, state, tokens)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", nargs="?", default="test", choices=("test",))
    parser.parse_args()
    try:
        main()
    except RuntimeError as error:
        raise SystemExit(str(error)) from None
    except (ValueError, KeyError, urllib.error.URLError):
        raise SystemExit("Keycloak JWT regression failed; private provider responses omitted") from None
