---
title: Configure OpenBao JWT and OIDC authentication
description: Let people log in to OpenBao through an OIDC provider and CI jobs log in with a JWT, using AuthMount, JWTAuthConfig and JWTAuthRole resources.
---

# Configure JWT and OIDC authentication

People log in to OpenBao through your identity provider, and CI jobs log in with the JWT their CI
system signs. You declare each auth method, its configuration and its roles as Kubernetes
resources, and the OpenBao Config Operator writes them to OpenBao.

## Before you begin

- The operator is installed and the `BaoConnection` named `openbao` in the Kubernetes namespace
  `openbao-config` is `Ready` ([Install](../get-started/install.md),
  [Connect to OpenBao](../get-started/connect.md)).
- The connection's OpenBao policy allows these paths
  ([OpenBao policy for the operator](../operate/openbao-policy.md)):

    ```hcl
    path "sys/auth"             { capabilities = ["read"] }
    path "sys/auth/oidc"        { capabilities = ["create", "read", "update", "delete", "sudo"] }
    path "sys/auth/oidc/tune"   { capabilities = ["create", "read", "update", "delete", "sudo"] }
    path "auth/oidc/config"     { capabilities = ["create", "read", "update", "delete"] }
    path "auth/oidc/role/*"     { capabilities = ["create", "read", "update", "delete"] }
    path "sys/auth/jwt-ci"      { capabilities = ["create", "read", "update", "delete", "sudo"] }
    path "sys/auth/jwt-ci/tune" { capabilities = ["create", "read", "update", "delete", "sudo"] }
    path "auth/jwt-ci/config"   { capabilities = ["create", "read", "update", "delete"] }
    path "auth/jwt-ci/role/*"   { capabilities = ["create", "read", "update", "delete"] }
    ```

    Append these lines to `openbao-configurer.hcl`, the connection's policy from Connect to OpenBao,
    and write it again with `bao policy write openbao-configurer openbao-configurer.hcl`.

- The `ACLPolicy` named `app-read` from [Manage OpenBao policies with Kubernetes](policies.md) is
  `Ready`.
- For OIDC login, steps 1 to 3: a client registered at the identity provider with the redirect
  URIs `https://<openbao-ui-address>/ui/vault/auth/oidc/oidc/callback` and
  `http://localhost:8250/oidc/callback`, and its client ID and client secret.
- For JWT login, steps 4 to 6: the JWKS URL and the issuer of the CI system. The two parts do not
  depend on each other.
- The `bao` CLI, with `BAO_ADDR` and a token that can read the paths in the last step.

## 1. Enable the OIDC auth method

An `AuthMount` enables the method at `auth/oidc`, the path people log in at. Save this as
`oidc-mount.yaml`:

```yaml
apiVersion: sys.obco.dev/v1alpha1
kind: AuthMount
metadata:
  name: oidc
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  path: oidc
  type: oidc
```

```bash
kubectl apply -f oidc-mount.yaml
kubectl -n openbao-config wait authmount/oidc --for=condition=Ready --timeout=2m
```

## 2. Configure the identity provider

A `JWTAuthConfig` tells the mount where the provider is and which client to use. OpenBao never
returns the client secret, so the operator reads it from a Secret, keeps only a hash of it in
status and writes it again when the Secret changes. Create the Secret:

```bash
kubectl -n openbao-config create secret generic oidc-client \
  --from-literal=client-secret='<client-secret>'
```

Save this as `oidc-config.yaml`:

```yaml
apiVersion: auth.obco.dev/v1alpha1
kind: JWTAuthConfig
metadata:
  name: oidc
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  mountPath: oidc
  oidcDiscoveryURL: <issuer-url>
  oidcClientID: <client-id>
  oidcClientSecretSecretRef:
    name: oidc-client
    key: client-secret
  defaultRole: people
```

Replace `<client-secret>` and `<client-id>` with the values of the registered client, and
`<issuer-url>` with the provider's issuer URL, without the `/.well-known/openid-configuration`
suffix. OpenBao fetches the discovery document itself; if the provider's certificate is not signed
by a CA that OpenBao trusts, add `oidcDiscoveryCAPEM`.

```bash
kubectl apply -f oidc-config.yaml
kubectl -n openbao-config wait jwtauthconfig/oidc --for=condition=Ready --timeout=2m
```

## 3. Create a role for people

A `JWTAuthRole` with `roleType: oidc` sets which redirect URIs the login may use and which
policies the token gets. Save this as `oidc-role.yaml`:

```yaml
apiVersion: auth.obco.dev/v1alpha1
kind: JWTAuthRole
metadata:
  name: people
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  mountPath: oidc
  name: people
  roleType: oidc
  userClaim: sub
  groupsClaim: groups
  allowedRedirectURIs:
    - https://<openbao-ui-address>/ui/vault/auth/oidc/oidc/callback
    - http://localhost:8250/oidc/callback
  tokenTTL: 1h
  policyRefs:
    - name: app-read
```

Replace `<openbao-ui-address>` with the address people's browsers use for OpenBao, including the
port if it is not 443, for example `openbao.example.com:8200`; this is not the in-cluster host the
operator reaches. The first URI serves the UI and the second
the `bao` CLI; both must be registered at the provider as well. Set `groupsClaim` only when the
provider sends that claim: it carries the provider's groups, which OpenBao groups map to policies
([Manage OpenBao identity with Kubernetes](identity.md),
[Grant KV access with access levels, grants and layouts](access-model.md)).

This role admits every user the provider authenticates for the client. To narrow it, add
`boundClaims`.

```bash
kubectl apply -f oidc-role.yaml
kubectl -n openbao-config wait jwtauthrole/people --for=condition=Ready --timeout=2m
```

## 4. Enable the JWT auth method

CI jobs get their own mount, because a mount verifies tokens from one source of keys. Save this as
`jwt-mount.yaml`:

```yaml
apiVersion: sys.obco.dev/v1alpha1
kind: AuthMount
metadata:
  name: jwt-ci
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  path: jwt-ci
  type: jwt
```

```bash
kubectl apply -f jwt-mount.yaml
kubectl -n openbao-config wait authmount/jwt-ci --for=condition=Ready --timeout=2m
```

## 5. Configure the token issuer

This `JWTAuthConfig` gives the mount the keys that verify a job's JWT and the issuer the JWT must
name. Save this as `jwt-config.yaml`:

```yaml
apiVersion: auth.obco.dev/v1alpha1
kind: JWTAuthConfig
metadata:
  name: jwt-ci
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  mountPath: jwt-ci
  jwksURL: <jwks-url>
  boundIssuer: <issuer>
```

Replace `<jwks-url>` with the URL of the CI system's signing keys and `<issuer>` with the `iss`
claim of its tokens. If the certificate of that URL is not signed by a CA that OpenBao trusts, add
`jwksCAPEM`.

Set exactly one source of keys per mount: `oidcDiscoveryURL`, `jwksURL`, `jwtValidationPubkeys`
or a `providerConfig` with `provider: kubernetes`. The API server refuses a spec with none or
several of them, and an `oidcClientID` without `oidcDiscoveryURL`. Static public keys and provider
configurations are in the example with
[every field](../reference/examples/auth.md#jwtauthconfig).

```bash
kubectl apply -f jwt-config.yaml
kubectl -n openbao-config wait jwtauthconfig/jwt-ci --for=condition=Ready --timeout=2m
```

## 6. Create a role for CI jobs

A `JWTAuthRole` with `roleType: jwt` binds claims of the job's JWT to policies. This one admits
jobs on protected refs of projects under `platform/`. Save this as `jwt-role.yaml`:

```yaml
apiVersion: auth.obco.dev/v1alpha1
kind: JWTAuthRole
metadata:
  name: ci-deploy
  namespace: openbao-config
spec:
  connectionRef:
    name: openbao
  mountPath: jwt-ci
  name: ci-deploy
  roleType: jwt
  userClaim: sub
  boundAudiences:
    - <audience>
  boundClaimsType: glob
  boundClaims:
    project_path:
      - platform/*
    ref_protected:
      - "true"
  tokenTTL: 10m
  policyRefs:
    - name: app-read
```

Replace `<audience>` with the `aud` claim of the job's JWT, and `project_path` and `ref_protected`
with the claims your CI system sends. A `jwt` role must set at least one of `boundAudiences`,
`boundSubject`, `boundClaims` and `tokenBoundCIDRs`. The other fields of a role are in the example
with [every field](../reference/examples/auth.md#jwtauthrole).

A `JWTCELRole` decides with a CEL program instead of bound claims
([example](../reference/examples/auth.md#jwtcelrole)). The operator writes it to
`auth/<mount>/cel/role/<name>`, so the connection's OpenBao policy must allow that path too.

```bash
kubectl apply -f jwt-role.yaml
```

## 7. Check that it worked

The roles are `Ready`. Leave out the one you did not create:

```bash
kubectl -n openbao-config wait jwtauthrole/people jwtauthrole/ci-deploy \
  --for=condition=Ready --timeout=2m
```

After a later edit, check `Synced` as well, because `Ready` keeps its last value while a write
fails:

```bash
kubectl -n openbao-config get jwtauthconfigs,jwtauthroles
```

OpenBao holds the roles. Set the CLI to the same OpenBao namespace as the connection:

```bash
bao read auth/oidc/role/people
bao read auth/jwt-ci/role/ci-deploy
```

Log in through the provider:

```bash
bao login -method=oidc -path=oidc role=people
```

Log in as a CI job:

```bash
bao write auth/jwt-ci/login role=ci-deploy jwt='<job-jwt>'
```

Replace `<job-jwt>` with a JWT the CI system issued to a job. Both responses list `app-read`
in the token's policies.

## Troubleshooting

| symptom | check |
|---------|-------|
| config reports `ReconcileError` with a discovery or JWKS error | OpenBao, not the operator, fetches the URL: its network path to the issuer, and `oidcDiscoveryCAPEM` or `jwksCAPEM` |
| config reports `ReferenceNotReady` | the Secret `oidc-client` and its key `client-secret` exist in the Kubernetes namespace of the resource |
| role reports `ReferenceNotReady` | the `ACLPolicy` in `policyRefs` exists in the role's Kubernetes namespace and is `Ready` |
| config or role reports `OutOfSync` and names a key | OpenBao normalises the value differently; write it the way `bao read` returns it |
| OIDC login fails with `redirect_uri` errors | `allowedRedirectURIs` on the role and the redirect URI registered at the provider are identical |
| JWT login fails with a claim or audience error | `boundClaims` and `boundAudiences` against the decoded JWT |

## Related

- API reference: [JWTAuthConfig](../reference/api/auth.md#jwtauthconfig),
  [JWTAuthRole](../reference/api/auth.md#jwtauthrole),
  [JWTCELRole](../reference/api/auth.md#jwtcelrole),
  [AuthMount](../reference/api/sys.md#authmount),
  [ACLPolicy](../reference/api/sys.md#aclpolicy).
- [Manage OpenBao identity with Kubernetes](identity.md): map the provider's groups to OpenBao
  groups.
- [Grant KV access with access levels, grants and layouts](access-model.md): `AccessLayout` writes
  the policies, groups and group aliases for you.
- [Connect to OpenBao](../get-started/connect.md): JWT auth for the operator's own login.
