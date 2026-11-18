---
title: Run the local OpenBao lab
description: "Run the kind lab that exercises OpenBao Config Operator identity, ESO, transit, PKI and database profiles from a clone."
---

# Run the local lab

For contributors working in a clone, this page runs the disposable kind lab that boots OpenBao and the operator, then exercises Keycloak, Authentik, ZITADEL, ESO, transit, PKI and database profiles through `make` and `hack/lab`.

## What it covers

- A three-node TLS raft OpenBao cluster and the operator in kind (`make lab-up` / `hack/lab/up.sh`)
- Keycloak human OIDC, JWT discovery/JWKS and group access (`make lab-identity`)
- Authentik and ZITADEL provider profiles (`make lab-authentik`, `make lab-zitadel`)
- ESO secret delivery, transit encryption, cert-manager PKI and dynamic database credentials
- An optional LDAP external-plugin profile under `hack/lab/plugins/`

The user-access scenarios follow the method of the public [Stakater integration lab](https://github.com/stakater/mto-extension-vault-enterprise): pair each denial with an allowed operation, exercise OIDC in a browser, and fail when required results are absent.

## Prerequisites

- A clone of this repository; run every command from the repository root
- Docker, kubectl, Go, Python 3, OpenSSL, Node.js and npm (browser profiles install the locked Playwright package)
- Repository tools via Make (`bin/kind`, `bin/helm`, and the rest the targets pull in)
- Enough memory for OpenBao, the operator and the controllers you enable; generated credentials stay in the lab only

## Run

Create the base lab (default profile `raft`):

```bash
export LAB_CONTEXT=kind-openbao-config-operator-lab
make lab-up
```

`LAB_PROFILE=postgres make lab-up` adds PostgreSQL as a database-secrets target; OpenBao storage remains raft. Override operator Helm values with `hack/lab/up.sh postgres -f hack/lab/profiles/operator-single.yaml`. Lab kubeconfig lives under `.tmp/lab/` and does not change your default context. To use an existing kind cluster created by the e2e suite, set `LAB_CONTEXT` to that kind context and run `hack/lab/bootstrap.sh`.

Keycloak identity (browser package, OIDC, JWT and group checks):

```bash
make lab-identity
```

Run each identity provider in its own cluster: [Authentik](../how-to/authentik-lab.md), [ZITADEL](../how-to/zitadel-lab.md).

ESO, transit, PKI and one database profile:

```bash
hack/lab/identity-up.sh
make lab-eso
make lab-transit
make lab-pki
make lab-database DATABASE_PROFILE=postgresql
```

Hosted Auth0/Okta flows are separate: [Run the SaaS identity lab](../guides/saas-identity-lab.md).

## Inspect { #test-keycloak-users-and-machines }

Keycloak scenarios cover root plus tenants `lab-alpha` and `lab-beta`, with editors, viewers and a machine client. Group claims map through `IdentityGroupAlias` to external OpenBao groups. Check allowed reads, viewer write rejection, inherited shared reads, forbidden paths and cross-tenant rejection.

| Mode | Operator configuration | Login |
| --- | --- | --- |
| Browser OIDC | `JWTAuthConfig.oidcDiscoveryURL`, `oidcClientID`, `oidcClientSecretSecretRef` | Authorization code through the Keycloak form |
| JWT with discovery | `JWTAuthConfig.oidcDiscoveryURL`, `boundIssuer` | Keycloak-issued JWT submitted to OpenBao |
| JWT with JWKS | `JWTAuthConfig.jwksURL`, `boundIssuer` | Same JWT verified through the realm JWKS |

Keycloak uses HTTP inside the kind network; OpenBao API calls use HTTPS with the lab CA. Do not expose this lab or reuse its credentials elsewhere.

ESO checks initial delivery, source rotation and cross-tenant rejection. The transit profile reports through its test script.

The PKI profile creates a root, an intermediate issuer, a restricted signing role and Kubernetes authentication in the OpenBao namespace `lab-pki`; cert-manager writes `localhost-tls` in the Kubernetes namespace `obco-lab-pki`. `hack/lab/pki-test.sh` verifies the certificate against the root and intermediate fetched independently from OpenBao, checks the `localhost` and `app.localhost` DNS names and the key match, and checks that the issuer identity can sign a CSR but cannot change policies. It then deletes the TLS Secret and checks that cert-manager recreates it with a new certificate and key.

The PKI profile does not wait for scheduled renewal and does not use ACME, and the lab CA is not installed in your machine's trust store.

## Add a real external plugin { #add-a-real-external-plugin }

The [LDAP profile](../../hack/lab/plugins/README.md) builds a test OpenBao image with the published LDAP auth plugin, deploys a directory, and exercises catalogue, mount, config and mappings through operator CRDs. It is separate from the synthetic plugin used by the live kit.

## Clean up { #remove-the-lab }

Delete only the cluster you created for this lab:

```bash
bin/kind delete cluster --name openbao-config-operator-lab
```

If you set `KIND_CLUSTER`, use that name instead. Deleting the cluster removes Kubernetes resources and raft volumes. Reports and kubeconfig remain under git-ignored `.tmp/lab/`.
