---
description: Configure OpenBao JWT and OIDC authentication with Kubernetes custom resources, Secret references, identity groups and tenant-specific roles.
---

# JWT and OIDC auth

This guide configures OpenBao's JWT/OIDC auth method from Kubernetes resources: people log in
through an identity provider, and machines such as CI jobs log in with a signed JWT.

## Before you begin

- A `Ready` connection whose OpenBao policy allows `sys/auth/<mount>` and `sys/auth/<mount>/tune` (with `sudo`), and
  `auth/<mount>/config` and `auth/<mount>/role/*` for every mount you manage
  ([OpenBao policy for the operator](../operate/openbao-policy.md)).
- For OIDC: a client registered at the identity provider, with the redirect URI
  `https://<openbao>/ui/vault/auth/<mount>/oidc/callback`, and its client secret.
- For JWT: the issuer's JWKS URL, its discovery URL or its public keys.

## 1. Enable the auth method

Create an `AuthMount` with `type: jwt` or `type: oidc` (both run the same plugin) at the path the
logins will use, as in this fragment of a spec:

```yaml
spec:
  connectionRef:
    name: openbao
  path: oidc
  type: oidc
```

[examples/sys/authmount.yaml](../../examples/sys/authmount.yaml) shows a complete `AuthMount`.

## 2. Configure the method

A `JWTAuthConfig` sets how tokens are verified. The client secret is read from a Secret and never
stored in status; OpenBao never returns it, so the operator records a hash of it and writes it
again when the Secret changes. The file configures four mounts: OIDC discovery with a client
secret, a JWKS URL for CI jobs, static public keys for ServiceAccount tokens, and a provider
configuration:

Adapt these field-reference examples to your identity provider before applying them. Select the
configuration you need and enable its matching auth mount first. Replace the example issuer,
client ID and Secret values. Replace the fake PEM blocks with valid certificates or public
keys; omit CA overrides when the issuer uses a certificate trusted by OpenBao. Keep only the
provider-specific settings your identity provider supports.

```yaml title="examples/auth/jwtauthconfig.yaml"
# Configures the oidc mount for browser logins through a Keycloak realm
# found by OIDC discovery, with the client secret read from a Secret.
# The other resources verify CI job JWTs with a JWKS URL, Kubernetes
# ServiceAccount tokens with a static public key, and Google Workspace
# logins with a provider configuration from a Secret.
apiVersion: v1
kind: Secret
metadata:
  name: oidc-client
type: Opaque
stringData:
  client-secret: fake-oidc-client-secret-not-real
---
apiVersion: v1
kind: Secret
metadata:
  name: gsuite-provider
type: Opaque
stringData:
  config: '{"gsuite_service_account": "{\"type\": \"service_account\", \"private_key\": \"fake-not-a-real-key\"}"}'
---
apiVersion: auth.openbaoconfig.io/v1alpha1
kind: JWTAuthConfig
metadata:
  name: oidc
spec:
  connectionRef:
    kind: OpenBaoConnection
    name: openbao
  creationPolicy: CreateOrAdopt
  deletionPolicy: Orphan
  driftDetection: Enabled
  mountPath: oidc
  oidcDiscoveryURL: https://keycloak.example.com/realms/platform
  oidcDiscoveryCAPEM: |
    -----BEGIN CERTIFICATE-----
    RkFLRS1DQS1DRVJUSUZJQ0FURS1GT1ItRVhBTVBMRVMtT05MWQ==
    -----END CERTIFICATE-----
  oidcClientID: openbao
  oidcClientSecretSecretRef:
    name: oidc-client
    key: client-secret
  oidcResponseMode: form_post
  oidcResponseTypes:
    - code
    - id_token
  jwtSupportedAlgs:
    - RS256
    - ES256
  boundIssuer: https://keycloak.example.com/realms/platform
  defaultRole: reader
  overrideAllowedServerNames:
    - keycloak.internal.example.com
  namespaceInState: true
  skipJWKSValidation: false
---
apiVersion: auth.openbaoconfig.io/v1alpha1
kind: JWTAuthConfig
metadata:
  name: jwt-jwks
spec:
  connectionRef:
    name: openbao
  mountPath: jwt-ci
  jwksURL: https://gitlab.example.com/oauth/discovery/keys
  jwksCAPEM: |
    -----BEGIN CERTIFICATE-----
    RkFLRS1DQS1DRVJUSUZJQ0FURS1GT1ItRVhBTVBMRVMtT05MWQ==
    -----END CERTIFICATE-----
  boundIssuer: https://gitlab.example.com
---
apiVersion: auth.openbaoconfig.io/v1alpha1
kind: JWTAuthConfig
metadata:
  name: jwt
spec:
  connectionRef:
    name: openbao
  mountPath: jwt
  jwtValidationPubkeys:
    - |
      -----BEGIN PUBLIC KEY-----
      RkFLRS1QVUJMSUMtS0VZLUZPUi1FWEFNUExFUy1PTkxZ
      -----END PUBLIC KEY-----
  boundIssuer: https://kubernetes.default.svc.cluster.local
---
apiVersion: auth.openbaoconfig.io/v1alpha1
kind: JWTAuthConfig
metadata:
  name: gsuite
spec:
  connectionRef:
    name: openbao
  mountPath: gsuite
  oidcDiscoveryURL: https://accounts.google.com
  oidcClientID: fake-client-id.apps.googleusercontent.com
  oidcClientSecretSecretRef:
    name: oidc-client
    key: client-secret
  providerConfig:
    provider: gsuite
    settings:
      admin_impersonate: admin@example.com
      fetch_groups: true
      groups_recurse_max_depth: 5
    secretRef:
      name: gsuite-provider
      key: config
```

Set exactly one source of keys per mount: `oidcDiscoveryURL`, `jwksURL`, `jwtValidationPubkeys`
or a `providerConfig` with `provider: kubernetes`. The API server refuses a spec with none or
several of them, and an `oidcClientID` without `oidcDiscoveryURL`.

## 3. Create roles

A `JWTAuthRole` binds claims to policies. This role lets CI jobs of protected `main` branches log
in on the `jwt-ci` mount:

Adapt the subject, audience, bound claims and network restrictions to the JWTs your CI system
issues. Create the referenced policies first. The exhaustive example also includes OIDC-only
fields; omit those for a JWT role. Set `groupsClaim` only when the issuer supplies that claim.

```yaml title="examples/auth/jwtauthrole.yaml"
# Lets CI jobs of the protected main branch of platform/app log in on the jwt-ci mount with their job JWT: the
# subject, audience and claims are bound, and the project path and ref become token metadata.
apiVersion: auth.openbaoconfig.io/v1alpha1
kind: JWTAuthRole
metadata:
  name: ci-deploy
spec:
  connectionRef:
    kind: OpenBaoConnection
    name: openbao
  creationPolicy: CreateOrAdopt
  deletionPolicy: Delete
  driftDetection: Enabled
  mountPath: jwt-ci
  name: ci-deploy
  roleType: jwt
  userClaim: sub
  userClaimJSONPointer: false
  boundAudiences:
    - https://openbao.example.com
  boundSubject: project_path:platform/app:ref_type:branch:ref:main
  boundClaimsType: glob
  boundClaims:
    project_path:
      - platform/*
    ref_protected:
      - "true"
  claimMappings:
    project_path: project
    ref: ref
  groupsClaim: groups
  oidcScopes:
    - profile
    - email
  allowedRedirectURIs:
    - https://openbao.example.com/ui/vault/auth/jwt-ci/oidc/callback
  callbackMode: device
  pollInterval: 5
  maxAge: 1h
  expirationLeeway: -1
  notBeforeLeeway: 2m
  clockSkewLeeway: 30
  oauth2Metadata:
    - id_token
  verboseOIDCLogging: false
  tokenPoliciesTemplateClaims: false
  oidcDisableConfirmation: false
  tokenTTL: 10m
  tokenMaxTTL: 1h
  tokenExplicitMaxTTL: 2h
  tokenPeriod: 0s
  tokenPolicies:
    - ci-deploy
  policyRefs:
    - name: obco-team-a-reader
  tokenBoundCIDRs:
    - 10.50.0.0/16
  tokenNumUses: 0
  tokenType: batch
  tokenNoDefaultPolicy: true
  tokenStrictlyBindIP: false
```

For browser logins, set `roleType: oidc`, `allowedRedirectURIs` and usually `groupsClaim`, so that
identity provider groups map to OpenBao groups ([Identity](identity.md),
[Access model](access-model.md)).

A `JWTCELRole` decides with a CEL program instead of bound claims:

```yaml title="examples/auth/jwtcelrole.yaml"
apiVersion: auth.openbaoconfig.io/v1alpha1
kind: JWTCELRole
metadata:
  name: ci-cel
spec:
  connectionRef:
    kind: OpenBaoConnection
    name: openbao
  creationPolicy: CreateOrAdopt
  deletionPolicy: Delete
  driftDetection: Enabled
  mountPath: jwt-ci
  name: ci-cel
  celProgram:
    variables:
      - name: project
        expression: claims.project_path
      - name: protected
        expression: claims.ref_protected == "true"
    expression: 'project.startsWith("platform/") && protected'
  message: only protected refs of platform projects may log in
  boundAudiences:
    - https://openbao.example.com
  expirationLeeway: 150
  notBeforeLeeway: 2m30s
  clockSkewLeeway: -1
```

## 4. Check that it worked

```bash
kubectl -n openbao-config get jwtauthconfigs,jwtauthroles,jwtcelroles
bao read auth/jwt-ci/role/ci-deploy
```

Confirm that the resources you applied report `Ready=True` and `Synced=True` for their current
`metadata.generation`; a stale `Ready` condition alone does not prove the latest edit succeeded.
For CLI OIDC login, register `http://localhost:8250/oidc/callback` at the identity provider and
include it in the role's `allowedRedirectURIs`, in addition to any UI callback. Then log in with
`bao login -method=oidc -path=oidc role=<role>`; for a JWT mount,
`bao write auth/jwt-ci/login role=ci-deploy jwt=<token>`.

## Troubleshooting

| symptom | check |
|---------|-------|
| config reports `ReconcileError` with a discovery error | OpenBao, not the operator, fetches the discovery URL: its network path to the issuer and `oidcDiscoveryCAPEM` |
| config reports `ReferenceNotReady` | the client secret Secret and key exist in the resource's namespace |
| login fails with `claim "sub" does not match` or `audience` errors | `boundSubject`, `boundClaims` and `boundAudiences` against the decoded JWT |
| OIDC login fails with `redirect_uri` errors | `allowedRedirectURIs` on the role and the redirect URI registered at the provider are identical |
| config or role reports `OutOfSync` and names a key | OpenBao normalises the value differently; write it the way `bao read` returns it |

## Related

- API reference: [JWTAuthConfig](../reference/api/auth.md#jwtauthconfig),
  [JWTAuthRole](../reference/api/auth.md#jwtauthrole),
  [JWTCELRole](../reference/api/auth.md#jwtcelrole),
  [AuthMount](../reference/api/sys.md#authmount).
- [Connect to OpenBao](../get-started/connect.md): JWT auth for the operator's own login.
