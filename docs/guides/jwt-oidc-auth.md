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

??? example "examples/auth/jwtauthconfig.yaml"

    ```yaml
    --8<-- "examples/auth/jwtauthconfig.yaml"
    ```

Set exactly one source of keys per mount: `oidcDiscoveryURL`, `jwksURL` or
`jwtValidationPubkeys`.

## 3. Create roles

A `JWTAuthRole` binds claims to policies. This role lets CI jobs of protected `main` branches log
in on the `jwt-ci` mount:

??? example "examples/auth/jwtauthrole.yaml"

    ```yaml
    --8<-- "examples/auth/jwtauthrole.yaml"
    ```

For browser logins, set `roleType: oidc`, `allowedRedirectURIs` and usually `groupsClaim`, so that
identity provider groups map to OpenBao groups ([Identity](identity.md),
[Access model](access-model.md)).

A `JWTCELRole` decides with a CEL program instead of bound claims:

```yaml title="examples/auth/jwtcelrole.yaml"
--8<-- "examples/auth/jwtcelrole.yaml"
```

## 4. Check that it worked

```bash
kubectl -n openbao-config get jwtauthconfigs,jwtauthroles,jwtcelroles
bao read auth/jwt-ci/role/ci-deploy
```

Every resource shows `Ready` `True`. For an OIDC mount, log in with
`bao login -method=oidc -path=oidc role=<role>`; for a JWT mount,
`bao write auth/jwt-ci/login role=ci-deploy jwt=<token>`.

## Troubleshooting

| symptom | check |
|---------|-------|
| config reports `ReconcileError` with a discovery error | OpenBao, not the operator, fetches the discovery URL: its network path to the issuer and `oidcDiscoveryCAPEM` |
| config reports `ReferenceNotReady` | the client secret Secret and key exist in the resource's namespace |
| login fails with `claim "sub" does not match` or `audience` errors | `boundSubject`, `boundClaims` and `boundAudiences` against the decoded JWT |
| OIDC login fails with `redirect_uri` errors | `allowedRedirectURIs` on the role and the redirect URI registered at the provider are identical |
| `Synced=False` keeps naming a field | a value OpenBao normalises differently; write it the way `bao read` returns it |

## Related

- API reference: [JWTAuthConfig](../reference/api/auth.md#jwtauthconfig),
  [JWTAuthRole](../reference/api/auth.md#jwtauthrole),
  [JWTCELRole](../reference/api/auth.md#jwtcelrole),
  [AuthMount](../reference/api/sys.md#authmount).
- [Connect to OpenBao](../get-started/connect.md): JWT auth for the operator's own login.
