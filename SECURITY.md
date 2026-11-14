# Security policy

## Reporting a vulnerability

Do not open a public issue, pull request or discussion for a security problem.

Report it privately through GitHub's private vulnerability reporting: open the repository's
**Security** tab and select **Report a vulnerability**
(<https://github.com/alexandrust88/openbao-config-operator/security/advisories/new>).
Include the affected version or commit, the steps to reproduce, and the impact you see. The
report stays private between you and the maintainers until an advisory is published.

## Response targets

| Step | Target |
|------|--------|
| Acknowledge the report | 5 working days |
| Confirm or reject it, with a severity (CVSS) | 10 working days |
| Release a fix for a critical or high finding | 30 days after confirmation |
| Release a fix for a medium or low finding | next minor release, at most 90 days |

These are targets for a volunteer project, not a contract. We agree the disclosure date with you,
credit you in the advisory unless you ask us not to, and request a CVE through the GitHub security
advisory. Fixes ship as a patch release with the advisory.

## Supported versions

| Version | Supported |
|---------|-----------|
| latest minor release (`v0.Y.x`) | yes |
| previous minor release | critical security fixes only, for 30 days after the next minor is released, at the maintainers' discretion |
| older minor releases | no, upgrade to the latest |
| `main` | yes, until the first release |

The project is pre-1.0: the API is `v1alpha1`. The full support window, including the rules from
1.0.0, is in the [release process](docs/maintainers/release-process.md#support-window).

## Scope

In scope:

- The operator, its CRDs, the Helm chart and the published images.
- Ways for a tenant to act with an OpenBao identity other than its own (see the tenant boundary
  in [docs/architecture.md](docs/architecture.md#connections-and-the-tenant-boundary)).
- Tokens, secret inputs or OpenBao response bodies leaking into status, events, logs or metrics.

Out of scope:

- Vulnerabilities in OpenBao itself; report those to the OpenBao security team.
- Misconfigured OpenBao policies that grant a tenant more than intended.

## Verifying releases

The release workflow signs the image index, each platform image and the Helm chart with keyless
cosign, attests SPDX and CycloneDX SBOMs and build provenance on all of them (GitHub artifact attestations on the
image index and the chart: SLSA Build L2; cosign `slsaprovenance1` statement written by the release job: no SLSA
level), and signs the checksums of the release assets. Verify a release before you install it:

```sh
hack/release/verify-release.sh v0.2.0
```

or check the image signature by hand, pinned to the release workflow run for exactly that tag:

```sh
cosign verify ghcr.io/alexandrust88/openbao-config-operator@<digest> \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity https://github.com/alexandrust88/openbao-config-operator/.github/workflows/release.yml@refs/tags/v0.2.0
```

The commands for the SBOMs, the provenance, the chart and `gh attestation verify` are in
[docs/security/hardening.md](docs/security/hardening.md#verify-the-release).
