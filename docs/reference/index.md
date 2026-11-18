---
description: Find OpenBao Config Operator custom resources, API fields, Helm values, connection settings, status conditions and compatibility requirements.
---

# Reference

Exact facts about the operator's API, behaviour and packaging, for looking up while you work.
For the mental model, read the [concepts](../concepts/index.md); for tasks, use the guides.

| page | look here for |
|------|---------------|
| [API reference](api/index.md) | every kind and field, generated from the CRD schemas, with an example per kind |
| [Examples](examples/index.md) | the curated and generated example manifests, validated against the CRDs |
| [Kind catalogue](../kinds.md) | every kind with its OpenBao path, availability class, roadmap phase and status |
| [Constants and timings](constants.md) | drift intervals, backoffs, retry counts, rate shares, write budget, login and renewal points |
| [API coverage](../api-coverage.md) | how many OpenBao API paths have a typed kind, per OpenBao release |
| [Connections](connections.md) | `BaoServer`, `BaoConnection` and `ClusterBaoConnection` fields, authentication methods, tenant boundary rules, TLS |
| [Status and events](status-and-events.md) | conditions, every reason and what to do about it, status fields, events, print columns |
| [Helm values](helm-values.md) | every chart value with its type, default and constraints |
| [Compatibility](compatibility.md) | supported OpenBao, Kubernetes and Helm versions, the versions they are verified with, image platforms, kind availability classes |
| [Known limitations](limitations.md) | what the current release does not do or does not cover, with workarounds |

Related pages outside this section:

| page | look here for |
|------|---------------|
| [Observability](../operate/observability.md) | metrics, labels, example queries and alert rules, log flags |
| [Troubleshooting](../operate/troubleshooting.md) | symptoms, checks and fixes |
| [Architecture](../architecture.md) | the design rules the engine enforces |
| [Decisions (ADRs)](../adr/README.md) | why the design is the way it is |
