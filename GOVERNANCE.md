# Governance

This document says who decides what in openbao-config-operator and how to take on more responsibility. It is
written for a small project and will change as the project grows; changes follow the
[governance change rule](#changing-this-document).

## Values

- **Correctness over breadth.** A kind is added when it holds the engine rules: no writes in steady state, secrets
  only by reference, a typed reason on every condition. More kinds never justify a weaker rule.
- **Decisions in the open.** Design decisions are recorded as [ADRs](docs/adr/) and discussed in public issues or
  pull requests. Private discussion is only for security reports and conduct reports.
- **Users' OpenBao comes first.** Defaults that decide what happens to a live server (`creationPolicy`,
  `deletionPolicy`, the destructive-delete guard) change only with an ADR and a release note.
- **Contributors are peers.** Humans and coding agents follow the same rules in [AGENTS.md](AGENTS.md); the human
  who opens a pull request is responsible for it.

## Roles

### Contributor

Anyone who opens an issue, answers a question, reviews a pull request, improves the docs or sends code.
Contributors follow the [code of conduct](CODE_OF_CONDUCT.md) and [CONTRIBUTING.md](CONTRIBUTING.md). No
nomination is needed.

### Reviewer

A contributor who reviews pull requests in one or more areas (for example `area/secrets` or `area/chart`).

- Responsibilities: review pull requests in the area within a week, triage issues with `area/*` and `kind/*`
  labels, help new contributors.
- Privileges: triage permission on the repository. A reviewer's approving review is advisory: GitHub counts
  approvals only from users with write access, so it does not satisfy the merge rule on its own (see
  [what GitHub enforces](#what-github-enforces)).
- How to become one: at least five merged pull requests or substantial reviews in the area over at least two
  months, and a nomination by a maintainer, approved by lazy consensus of the maintainers.

### Maintainer

A reviewer who is responsible for the project as a whole.

- Responsibilities: merge pull requests, cut releases ([release process](docs/maintainers/release-process.md)),
  handle security reports, keep the [roadmap](docs/maintainers/roadmap.md) current, enforce the code of conduct,
  and keep CI and dependencies healthy.
- Privileges: write or admin permission on the repository, access to the release environment and to private
  vulnerability reports.
- How to become one: active as a reviewer for at least three months, a track record of sound reviews across more
  than one area, and a nomination by a maintainer approved by a two-thirds majority of the maintainers (while there
  is a single maintainer, that maintainer decides and records the reason in the pull request that edits
  [MAINTAINERS.md](MAINTAINERS.md)).

### Emeritus maintainer

A maintainer who steps back. Emeritus maintainers keep their place in [MAINTAINERS.md](MAINTAINERS.md), lose
write access, and can return through a lazy-consensus decision of the maintainers. A maintainer who has not
reviewed, merged or commented for six months is asked whether they want to stay active; with no answer within
a month they move to emeritus.

## Decision making

| decision | how |
|----------|-----|
| a bug fix, small feature, docs or test change | green required checks; with two or more maintainers, also one approving review from a maintainer other than the author |
| a new typed kind | as above, plus the add-a-kind checks in [AGENTS.md](AGENTS.md) (integration scenario, example, catalogue row) |
| an API change: new default, removed or renamed field, stricter validation | as above, plus a release note; follows the [deprecation policy](docs/maintainers/release-process.md#deprecation-policy) |
| a design change that contradicts an accepted ADR | a new ADR in a pull request, open for at least seven days, accepted by lazy consensus of the maintainers |
| adding or removing a maintainer | a two-thirds majority of the maintainers |
| changing this document or the license | a two-thirds majority of the maintainers, with the pull request open for at least fourteen days |

**Lazy consensus**: a proposal is accepted when no maintainer objects within the stated time. An objection must
give a reason and, where possible, an alternative. When consensus fails, the maintainers vote; a simple majority
decides, and the lead maintainer breaks a tie.

### What GitHub enforces

The repository settings ([github-settings.md](docs/maintainers/github-settings.md)) enforce the rows below on
`main` and `release-*` branches and on `v*` tags. The script that applies them has a solo mode for the
single-maintainer phase and a strict mode for two or more maintainers.

| | single-maintainer phase (solo mode) | two or more maintainers (strict mode) |
|-|-|-|
| how a change lands | a squash-merged pull request with green required checks and resolved conversations; no direct or force pushes | same |
| approvals | none required; the maintainer merges their own pull requests | one approval from a maintainer with write access, given after the latest push. GitHub never counts the author's approval or one from a user without write access |
| release workflow | pauses until the maintainer approves the `release` environment | pauses until a maintainer other than the one who pushed the tag approves the `release` environment |
| `v*` tags | only the owner can push them; a tag never moves and is never deleted | same |

The switch to strict mode is part of the pull request that adds the second maintainer to
[MAINTAINERS.md](MAINTAINERS.md).

Everything else in this document is a commitment of the maintainers, not a setting. In particular, while there is a
single maintainer, that maintainer leaves their own pull request open for at least 24 hours before merging it,
except for security fixes under embargo; GitHub does not check this. The waiting periods and votes in the table
above are not checked by GitHub either.

## Single-maintainer phase

The project starts with one maintainer. The bus-factor risk is stated openly: if the maintainer cannot continue, the
repository should not be left unmaintained without notice. The maintainer will:

- record every design decision as an ADR and every release step in [release-process.md](docs/maintainers/release-process.md),
  so that someone else can take over;
- name at least one reviewer and aim for a second maintainer from another organisation as early as possible;
- if they step down with no successor, archive the repository with a notice in the README rather than leave
  issues and security reports unanswered.

## Conduct

The [Contributor Covenant 2.1](CODE_OF_CONDUCT.md) applies in every project space. Maintainers enforce it using its
enforcement guidelines; the contact is in [MAINTAINERS.md](MAINTAINERS.md#code-of-conduct-contact).

## Neutral home

The project aims to stay vendor-neutral and to be useful to every OpenBao user. If it grows beyond a few
maintainers from more than one organisation, the maintainers will consider proposing it to the OpenBao project or
a foundation; that decision follows the governance change rule.

## Changing this document

Open a pull request that edits this file. It needs a two-thirds majority of the maintainers and stays open for at
least fourteen days.
