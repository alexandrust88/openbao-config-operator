---
description: "Build a small multi-tenant OpenBao platform in four lessons: platform setup, a second server, tenant onboarding and team onboarding."
---

# Tutorials

Four lessons that build a small multi-tenant OpenBao platform on the kind cluster of the
[quickstart](../get-started/quickstart.md). In each one you act in the role the table names. Each lesson continues from the cluster the one
before it left, so follow them in order.

| # | tutorial | you act as | at the end |
|---|----------|------------|------------|
| 1 | [Set up the platform](platform-owner.md) | the platform owner, admin of OpenBao and of the cluster | the operator logs in to OpenBao as itself, and configures the root namespace and the platform's own OpenBao namespace |
| 2 | [Manage several OpenBao servers](multiple-instances.md) | the platform owner | a second OpenBao runs next to the first, and each one holds its own configuration plus one shared policy |
| 3 | [Onboard two tenants](tenant-onboarding.md) | the platform admin | `tenant-a` and `tenant-b` each have a Kubernetes namespace they administer, an OpenBao namespace and a connection limited to it |
| 4 | [Onboard teams as a tenant](team-onboarding.md) | the `tenant-a` admin, with rights in its own Kubernetes namespace only | the teams `devteam` and `payments` each have a child OpenBao namespace that only their workloads can read, and `tenant-a` cannot reach `tenant-b` or the root |

[Why this operator exists](../concepts/why.md) explains what the tenant boundary these lessons
build gives you that other tools do not.

!!! warning "Evaluation only"
    The tutorials use the quickstart's OpenBao dev servers: in-memory storage, plain HTTP and the
    root token `root`. They give the operator a full-admin OpenBao policy so the lessons stay
    short. For a real server, see [Install](../get-started/install.md),
    [Connect to OpenBao](../get-started/connect.md) and
    [OpenBao policy for the operator](../operate/openbao-policy.md).

## Before you begin

- Finish the [quickstart](../get-started/quickstart.md) up to step 9, and keep the cluster: do not
  run its clean-up step.
- Work in the same shell, at the root of the repository clone. Every manifest the tutorials apply
  is in `examples/tutorials/`, and every command on these pages is a section of a script in
  `hack/docs/`, one script per lesson.

To run the four lessons in one go instead of step by step, run `hack/docs/tutorials.sh`. It stops
at the first step that does not reach the state the page describes, and it ends with the
[uninstall steps](../operate/uninstall.md), which remove the operator:

```bash
hack/docs/tutorials.sh
```

## Clean up

When you are done, delete the kind cluster as in the quickstart's
[Clean up](../get-started/quickstart.md#10-clean-up). Both OpenBao servers and
everything they hold go with it.
