---
description: "Find support, report issues and contribute to the OpenBao Config Operator community."
---

# Community

openbao-config-operator is built in the open for everyone who runs OpenBao on Kubernetes. This page says where the
community meets and how to get involved.

## Channels

[SUPPORT.md](../../SUPPORT.md) lists where to ask a question, discuss an idea, report a bug, request a kind or
report a vulnerability. [GitHub Discussions](https://github.com/alexandrust88/openbao-config-operator/discussions)
also carries release announcements and show and tell.

There is no chat room or community meeting yet. When there is enough interest, a channel will be opened in the
OpenBao community spaces rather than a separate one, and announced in Discussions.

## Ways to contribute

You do not need to write Go to help.

- **Use it and tell us.** Try the [quickstart](../get-started/quickstart.md), and post what worked and what did
  not in Discussions. Reports from real OpenBao setups are the most valuable input the project gets.
- **Answer questions** in Discussions Q&A.
- **Improve the docs.** Every page has a source file under `docs/`; fix what confused you.
- **Request a kind** for an OpenBao path you configure today with `OpenBaoEndpoint`, using the
  [new kind form](https://github.com/alexandrust88/openbao-config-operator/issues/new?template=new_kind.yml).
  Include the `bao read -format=json` output from a dev server: it is half the work.
- **Add a kind.** Pick one from [first issues](first-issues.md) or the [roadmap](../maintainers/roadmap.md); the
  recipe is in [AGENTS.md](../../AGENTS.md#adding-a-kind).
- **Review pull requests** in an area you know. Regular reviewers can become [reviewers](../../GOVERNANCE.md#reviewer).

Before your first pull request, read [CONTRIBUTING.md](../../CONTRIBUTING.md). Titles are conventional commits
(`feat(secrets): add TransitCacheConfig`).

## How issues are handled

1. A new issue gets `triage/needs-triage`. A maintainer labels it with `kind/*`, `area/*` and, when accepted,
   `triage/accepted` and a `priority/*`.
2. Small, well-described issues get `good first issue`; issues the maintainers would welcome a pull request for
   get `help wanted`.
3. A proposal that contradicts an [ADR](../adr/) gets `needs-adr` and is discussed as a new ADR first.
4. Issues and pull requests with no activity are marked stale after 90 and 60 days and closed 30 days later.
   Assigned or milestoned items, items with `priority/critical-urgent`, `priority/important-soon` or
   `lifecycle/frozen` or `kind/security`, issues with `triage/accepted`, `good first issue` or `help wanted`,
   and draft pull requests are never marked stale.

## Recognition

Every contributor is credited in the release notes of the release that ships their change. Reviewers and
maintainers are listed in [MAINTAINERS.md](../../MAINTAINERS.md).

## Code of conduct

Everyone in project spaces follows the [Contributor Covenant 2.1](../../CODE_OF_CONDUCT.md).
