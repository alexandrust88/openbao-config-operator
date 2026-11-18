---
title: Provision Auth0 for the OpenBao SaaS identity lab
description: "Provision disposable Auth0 resources and a state file for the OpenBao Config Operator SaaS identity lab, then clean them up."
---

# Provision Auth0 for the SaaS identity lab

For contributors working in a clone, this page provisions disposable Auth0 resources and writes the state file that [Run the SaaS identity lab](../guides/saas-identity-lab.md) consumes.

## What it covers

- A Management API machine application used only for create/delete
- One confidential web application, one database connection, nine users and a post-login Action
- Roles `admin`, `read` and `ops` in root, alpha and beta (OpenBao application-data roles, not Auth0 admin)
- Preview, `--apply` and `--cleanup` through `hack/lab/saas/auth0.py`

## Prerequisites

- A clone of this repository; run commands from the repository root
- A dedicated Auth0 development tenant (not a production directory)
- Permission to create a Machine to Machine application authorised for the Auth0 Management API with:

```text
read:clients create:clients read:client_keys
read:connections create:connections
read:users create:users
read:actions create:actions update:actions
delete:clients delete:connections delete:users delete:actions
```

- Python 3; for login tests also the [SaaS identity lab](../guides/saas-identity-lab.md) prerequisites

## Run

Create a private workspace and credentials file:

```bash
export LAB_DIR="${LAB_DIR:-$PWD/.local/saas-lab}"
umask 077
install -d -m 700 "$LAB_DIR/auth0"
${EDITOR:-vi} "$LAB_DIR/auth0/credentials.json"
chmod 600 "$LAB_DIR/auth0/credentials.json"
```

Credential shape:

```json
{
  "domain": "<tenant>.us.auth0.com",
  "client_id": "<provisioner-client-id>",
  "client_secret": "<provisioner-client-secret>",
  "email_address": "<you>@example.com"
}
```

Replace `<tenant>` with your Auth0 tenant name, `<provisioner-client-id>` and `<provisioner-client-secret>` with the Machine to Machine application's credentials, and `<you>@example.com` with a mailbox you control. Use the lowercase tenant hostname without `https://`. Prefer `email_address` with plus addressing, or `email_domain`. Without either, addresses use `example.invalid`.

Preview (no network, no credential):

```bash
python3 hack/lab/saas/auth0.py \
  --state "$LAB_DIR/auth0/state.json" --prefix obco-auth0-demo
```

Apply:

```bash
python3 hack/lab/saas/auth0.py \
  --credentials "$LAB_DIR/auth0/credentials.json" \
  --state "$LAB_DIR/auth0/state.json" \
  --prefix obco-auth0-demo --apply
```

Callback is `http://127.0.0.1:8250/oidc/callback`. The Action sets `https://obco.dev/groups` on the ID token. Then continue with [Run the SaaS identity lab](../guides/saas-identity-lab.md#run) using this state file.

Offline unit check of the provisioner (no Auth0 contact):

```bash
python3 -m unittest discover -s hack/lab/saas -p test_auth0.py -v
```

## Inspect

Failures name a fixed API stage and an allowlisted category (for example `category=password-strength`); they never print provider response text, emails, IDs or credentials. HTTP 400 is not retried blindly; HTTP 429 has bounded retries. Keep state until cleanup finishes. The Management API credential never enters Kubernetes.

## Clean up

```bash
python3 hack/lab/saas/auth0.py \
  --credentials "$LAB_DIR/auth0/credentials.json" \
  --state "$LAB_DIR/auth0/state.json" \
  --prefix obco-auth0-demo --cleanup
```

Cleanup deletes only IDs recorded in state. If a create timed out, check the Auth0 dashboard for the prefix before deleting state or re-applying. Revoke the provisioning credential when finished.
