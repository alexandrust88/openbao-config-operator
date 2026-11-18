---
title: Run the OIDC permissions showcase OpenBao lab
description: "Run browser OIDC journeys that prove group policies, denials and operator-driven permission changes against OpenBao."
---

# Run the OIDC permissions showcase lab

For contributors working in a clone, this page runs the hosted-provider permission showcase: real browser logins, ACL checks and an operator-driven group policy change against a disposable kind lab.

## What it covers

- Seventeen isolated browser logins across root, alpha and beta for `admin`, `read` and `ops`
- Wrong-group and wrong-tenant claim-binding denials
- KV versioning, transit encrypt/decrypt boundaries and revoke-self
- Removal and restoration of an alpha reader `IdentityGroup.spec.policies` entry through server-side apply

Self-hosted Authentik and ZITADEL profiles run equivalent journeys inside their own labs ([Authentik](../how-to/authentik-lab.md), [ZITADEL](../how-to/zitadel-lab.md)). Keycloak uses [Run the local lab](local-lab.md#test-keycloak-users-and-machines).

## Prerequisites

- A clone of this repository; run commands from the repository root
- The lab cluster, browser package and SaaS identity resources from `apply` in [Run the SaaS identity lab](../guides/saas-identity-lab.md#run), with the provider state from [Auth0](../how-to/auth0-lab.md) or [Okta](../how-to/okta-lab.md) provisioning

```bash
export LAB_DIR="${LAB_DIR:-$PWD/.local/saas-lab}"
export PROVIDER=auth0
export STATE="$LAB_DIR/$PROVIDER/state.json"
```

Use `PROVIDER=okta` when that is the prepared state. Keep `$STATE` private until cleanup succeeds.

## Run

```bash
hack/lab/saas/run.sh showcase --state "$STATE"
```

After `apply`, the operator has created OIDC mounts at root and in the OpenBao namespaces `saas-<provider>-alpha` and `saas-<provider>-beta`, where `<provider>` is `auth0` or `okta`. `showcase` runs the full matrix including the policy change; `browser` runs only the first fifteen logins. Test tokens are revoked; a later read with a revoked token must return HTTP 403.

## Inspect

| Journey | Operator resources | Positive result | Required rejection |
|---|---|---|---|
| Hosted login at root, alpha and beta | `AuthMount`, `JWTAuthConfig`, `JWTAuthRole`, `BaoNamespace` | OIDC code becomes a token with an entity | Wrong group / wrong tenant fail at claim binding |
| External and internal group inheritance | `IdentityGroup`, `IdentityGroupAlias`, `IdentityGroupMembership`, `ACLPolicy` | Role policy plus inherited session policy only | Direct token policies or unexpected grants fail |
| Application data administration | KV v2 `SecretsMount`, `KVSecret`, `ACLPolicy` | Admin writes versions 1–2, reads, lists, metadata | No version destroy; no other application prefix |
| Recover a previous value | Same KV resources | Deleting the latest version leaves version 1 readable | A missing or changed historical value fails the test |
| Reader and operator boundaries | `ACLPolicy`, identity groups | Read and ops read the seeded value | Writes, deletion, metadata, listing, destroy return 403 |
| Encryption without key administration | Transit `SecretsMount`, `TransitKey`, `ACLPolicy` | Admin and ops encrypt/decrypt | Readers cannot encrypt; no role may export or rotate keys |
| Tenant isolation | Namespace-local mounts, roles, policies | Each identity uses its own namespace | Same token cannot read the other namespaces |
| Session disposal | Inherited session `ACLPolicy` | Revoke-self succeeds | The revoked token cannot read the previously accessible value |
| Change a grant through Kubernetes | Alpha reader `IdentityGroup` | Removing the data policy denies data; restore returns access | A remaining read grant after removal fails the test |

A normal success ends with:

```text
alpha/read: operator IdentityGroup policy removal denied data; restoration reconciled and fresh OIDC regained read
```

If the process is killed before its `finally` block runs, reapply before retrying:

```bash
hack/lab/saas/run.sh apply --state "$STATE"
```

## Clean up

Run the provider guide's `--cleanup` with the original prefix, credentials and state, then remove the kind cluster as in [Run the SaaS identity lab](../guides/saas-identity-lab.md).
