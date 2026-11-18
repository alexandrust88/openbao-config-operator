---
title: Provision Okta for the OpenBao SaaS identity lab
description: "Provision disposable Okta resources and a state file for the OpenBao Config Operator SaaS identity lab, then clean them up."
---

# Provision Okta for the SaaS identity lab

For contributors working in a clone, this page provisions disposable Okta resources and writes the state file that [Run the SaaS identity lab](../guides/saas-identity-lab.md) consumes.

## What it covers

- One browser OIDC application, nine groups and nine users (`admin`, `read`, `ops` × root/alpha/beta)
- A new custom authorization server (org and `default` servers are left unchanged)
- Groups claim filtered to the lab prefix; authorization-code grant only
- Preview, `--apply` and `--cleanup` through `hack/lab/saas/okta.py`

## Prerequisites

- A clone of this repository; run commands from the repository root
- An Okta test org that can create a **new custom authorization server** (API Access Management entitlement where required)
- Capacity for nine new users plus your administrator
- An SSWS API token from an administrator allowed to manage users, groups, applications and authorization servers (including deactivation/deletion)
- Optional dedicated application sign-on policy ID for unattended browser login
- Python 3; for login tests also the [SaaS identity lab](../guides/saas-identity-lab.md) prerequisites

## Run

```bash
export LAB_DIR="${LAB_DIR:-$PWD/.local/saas-lab}"
umask 077
install -d -m 700 "$LAB_DIR/okta"
${EDITOR:-vi} "$LAB_DIR/okta/credentials.json"
chmod 600 "$LAB_DIR/okta/credentials.json"
```

Credential shape:

```json
{
  "domain": "<org>.okta.com",
  "api_token": "<api-token>",
  "email_address": "<you>@example.com",
  "app_sign_on_policy_id": "<app-sign-on-policy-id>"
}
```

Replace `<org>` with your Okta org name, `<api-token>` with the SSWS API token, `<you>@example.com` with a mailbox you control, and `<app-sign-on-policy-id>` with the dedicated application sign-on policy ID. `domain` is the org hostname without an `/oauth2` path. Prefer `email_address` with plus addressing. `app_sign_on_policy_id` is optional; when set, the script verifies an active `ACCESS_POLICY` and assigns it only to the new app—it never modifies or deletes that policy.

Preview:

```bash
python3 hack/lab/saas/okta.py \
  --prefix obco-demo \
  --state "$LAB_DIR/okta/state.json"
```

Apply:

```bash
python3 hack/lab/saas/okta.py \
  --prefix obco-demo \
  --credentials "$LAB_DIR/okta/credentials.json" \
  --state "$LAB_DIR/okta/state.json" \
  --apply
```

Callback is `http://127.0.0.1:8250/oidc/callback`. Audience is `api://obco-demo` for prefix `obco-demo`. Then continue with [Run the SaaS identity lab](../guides/saas-identity-lab.md#run) using this state file.

Offline unit check:

```bash
python3 -m unittest discover -s hack/lab/saas -p test_okta.py -v
```

## Inspect

The script does not disable MFA, add bypass rules or change global policy. Unattended browser login fails if org rules require interaction the runner cannot complete. The SSWS token stays with provisioning; only the OIDC client secret reaches Kubernetes. Keep state until cleanup finishes.

## Clean up

```bash
python3 hack/lab/saas/okta.py \
  --prefix obco-demo \
  --credentials "$LAB_DIR/okta/credentials.json" \
  --state "$LAB_DIR/okta/state.json" \
  --cleanup
```

Cleanup deletes only recorded IDs. If a create timed out, check the Okta System Log for the prefix before deleting state. Revoke the API token when finished.
