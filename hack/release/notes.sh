#!/usr/bin/env bash
# Prints the head of the GitHub release notes: every published digest and how to verify them.
# Environment: TAG, IMAGE, IMAGE_DIGEST, PLATFORM_DIGESTS ("os/arch sha256:..." per line), CHART, CHART_DIGEST,
# REPOSITORY (owner/name).
set -euo pipefail

: "${TAG:?}" "${IMAGE:?}" "${IMAGE_DIGEST:?}" "${PLATFORM_DIGESTS:?}" "${CHART:?}" "${CHART_DIGEST:?}" "${REPOSITORY:?}"
version=${TAG#v}

cat <<EOF
## Published artifacts

| Artifact | Reference |
| --- | --- |
| Image (index) | \`$IMAGE@$IMAGE_DIGEST\` |
EOF
while read -r platform digest; do
  [ -n "$platform" ] && echo "| Image ($platform) | \`$IMAGE@$digest\` |"
done <<<"$PLATFORM_DIGESTS"
cat <<EOF
| Helm chart | \`oci://$CHART:$version\` (\`$CHART_DIGEST\`) |

The chart pins the image index digest. Install:

\`\`\`sh
helm install openbao-config-operator oci://$CHART --version $version \\
  --namespace openbao-config-operator --create-namespace
\`\`\`

## Verify

Every artifact is signed with keyless cosign by this repository's release workflow on a \`v*\` tag, and carries
SPDX and CycloneDX SBOMs plus SLSA build provenance. Release assets are listed in \`sha256sums.txt\`, signed in
\`sha256sums.txt.sigstore.json\`, with their SLSA provenance in \`openbao-config-operator.intoto.jsonl\`.

\`\`\`sh
hack/release/verify-release.sh $TAG
# or, with the GitHub CLI:
gh attestation verify oci://$IMAGE@$IMAGE_DIGEST --repo $REPOSITORY
gh attestation verify oci://$CHART@$CHART_DIGEST --repo $REPOSITORY
\`\`\`

The full set of commands is in docs/security/hardening.md (R-SUPPLY-01).
EOF
