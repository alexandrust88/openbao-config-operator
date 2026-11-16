---
description: "Test identity-provider browser login, tenant roles, group policies, access denial and operator-driven permission changes."
---

# OIDC permissions showcase

Follow a user from an identity-provider login to an OpenBao permission, then change that permission
through an operator-managed identity group. The showcase tests the result with real API requests.

## Before you start

Choose the [Auth0 or Okta setup](../guides/saas-identity-lab.md), or run the self-hosted
[Authentik](../how-to/authentik-lab.md) or [ZITADEL](../how-to/zitadel-lab.md) profile. Those profiles
execute the permission journeys below automatically. The commands on this page use a hosted
provider state file in a disposable kind lab. Install the browser package first:

```bash
npm ci --ignore-scripts --prefix hack/lab/browser
```

For hosted providers, select the private workspace from the provider guide:

```sh
export LAB_DIR="${LAB_DIR:-$PWD/.local/saas-lab}"
export PROVIDER=auth0  # or okta
export STATE="$LAB_DIR/$PROVIDER/state.json"
```

The default workspace is ignored by Git. Keep generated state private until cleanup succeeds.
Keycloak has its own [identity profile](local-lab.md#test-keycloak-users-and-machines).

## Apply the identity configuration

```bash
hack/lab/saas/run.sh apply --state "$STATE"
```

Wait for `SaaS identity CRDs Ready`. The operator has configured an OIDC mount at root and in
`saas-auth0-alpha` and `saas-auth0-beta`. Each namespace has three external identity groups,
an internal session group, a KV v2 mount and a Transit key. The groups receive policies through
`Group`, `GroupAlias` and `GroupMembership` resources; the OIDC roles have no direct token policies.

The `admin` role manages application data. It does not administer OpenBao or its cryptographic keys.

## Run the user journeys

```bash
hack/lab/saas/run.sh showcase --state "$STATE"
```

The command runs 17 isolated browser logins. Nine exercise the three roles in the three namespaces.
Six try a wrong group or tenant and must fail at claim binding. The final two exercise a policy
change and its restoration. Each browser login starts a fresh context. Test tokens are revoked.
For sessions with data access, a subsequent read with the revoked token must return HTTP 403.

The same command is used by the credentialed SaaS regression workflow. `browser` runs only the
first 15 logins; `showcase` also changes and restores the alpha reader's group policy.

## Observe the permissions

| Journey | Operator resources | Positive result | Required rejection |
|---|---|---|---|
| Hosted login at root, alpha and beta | `AuthMount`, `JWTAuthConfig`, `JWTAuthRole`, `OpenBaoNamespace` | OIDC authorization code becomes an OpenBao token with an entity | Wrong group and wrong-tenant login fail specifically at claim binding |
| External and internal group inheritance | `Group`, `GroupAlias`, `GroupMembership`, `Policy` | Token has exactly its role policy and inherited session policy as identity policies | Direct token policies or unexpected grants fail the test |
| Application data administration | KV v2 `SecretsMount`, `KVSecret`, `Policy` | Admin writes versions 1 and 2, reads both, lists keys and reads metadata | Admin cannot destroy versions or access another application prefix |
| Recover a previous value | Same KV resources | Deleting the latest version makes it unavailable while version 1 remains readable | A missing or changed historical value fails the test |
| Reader and operator boundaries | `Policy`, identity groups | Read and ops users read the seeded application value | Writes, deletion, metadata inspection, key listing and version destruction return 403 |
| Encryption without key administration | Transit `SecretsMount`, `TransitKey`, `Policy` | Admin and ops encrypt and decrypt a probe value | Read users cannot encrypt; every role is denied key export and rotation |
| Tenant isolation | Namespace-local mounts, roles, policies and aliases | Each identity uses its own namespace's resources | The same token cannot read either of the other namespaces |
| Session disposal | Inherited session `Policy` | Revoke-self succeeds | The revoked token cannot read the previously accessible value |
| Change a grant through Kubernetes | Alpha reader `Group`, existing membership and policy | Removing the external group's data policy leaves login available but denies data; restoring it restores access on a fresh login | A remaining read grant after removal fails the test |

The tests assert HTTP outcomes and returned data. A successful login alone is insufficient.
The versioning probe uses a new key for each run, so previous runs cannot satisfy its assertions.

## Watch a permission change reconcile

During the final journey, the runner removes the alpha reader's `Group.spec.policies` entry using
the same server-side apply field manager as the setup command. It waits for `Ready` and `Synced`
on the current generation before logging in again.

The resulting identity must retain only the inherited session policy. A read of the seeded
application value must return 403. In a `finally` block, the runner restores the original group
spec and waits for reconciliation. It refuses to overwrite an unexpected concurrent edit or a
replacement Group. If the removal test succeeds, a fresh login and read then verify restored access.

A normal success ends with:

```text
alpha/read: operator Group policy removal denied data; restoration reconciled and fresh OIDC regained read
```

If the process is forcibly terminated, such as a runner being killed, its `finally` block cannot
run. Reapply the lab resources before retrying:

```bash
hack/lab/saas/run.sh apply --state "$STATE"
```

## Interpret the evidence

The ordinary CI browser-fixture tests use Chromium against synthetic hosted login pages. They
verify the browser harness, state checking and challenge failures without requiring accounts.
The credentialed `showcase` run is the evidence for the actual Auth0 or Okta login and the
operator's resulting OpenBao configuration.

Hosted MFA, CAPTCHA and consent screens are not bypassed. They fail the automated journey if the
dedicated test application requires an unsupported interaction. This suite does not establish
MFA coverage, enterprise-provider capabilities, or production readiness of an account policy.
See the [setup and account requirements](../guides/saas-identity-lab.md) before enabling scheduled runs.
