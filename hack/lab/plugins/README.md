# Real LDAP plugin profile

This disposable profile runs OpenBao's published LDAP authentication plugin against
LLDAP over TLS. It exercises `Plugin`, `AuthMount`, `LDAPAuthConfig`, `LDAPAuthGroup`
and `LDAPAuthUser`, then logs in through LDAP and checks the resulting policy
boundaries. It is separate from the small binary-protocol fixture used by the live
kit and does not claim coverage of the other external-plugin controllers.

The [official auth-ldap v0.2.0 release](https://github.com/openbao/openbao-plugins/releases/tag/auth-ldap-v0.2.0)
provides Linux amd64 and arm64 binaries. The build helper verifies the pinned
archive SHA-256 before extracting the executable and computing its catalog hash.
The directory image is [LLDAP v0.6.3](https://github.com/lldap/lldap/releases/tag/v0.6.3),
pinned by OCI digest. The [upstream Vault integration example](https://github.com/lldap/lldap/blob/v0.6.3/example_configs/hashicorp-vault.md)
defines the directory filters used here.

## Prepare

Start the common lab first. The `lab-openbao` connection must target the root
OpenBao namespace and allow the profile's paths. Install the operator chart with
`pluginKinds.enabled=true`; this installs external CRDs and starts their controllers.

The setup command downloads the pinned plugin archive, builds and loads a derived
OpenBao image, configures `/plugins` without replacing the raft or TLS settings,
rolls out the three pods, and installs the LDAP chart. Existing binaries in the
base image are retained. Image labels record the source image, verified plugin and
license checksums, so repeating setup reuses the existing image without restarting
unchanged OpenBao pods. Run it while no other lab test is using OpenBao:

```sh
export LAB_CONTEXT=kind-openbao-config-operator-lab
hack/lab/plugins/up.sh
hack/lab/plugins/test.sh
```

The test command creates a temporary localhost port-forward, trusts the cluster's
public CA, runs the access checks, and removes the forward and temporary CA file.
The image and executable checksum remain under `.tmp/lab/plugins/`. Never copy
this profile's credentials or ephemeral directory configuration into production.

Optional environment settings are `LAB_NAMESPACE` (default `obco-lab`),
`LAB_IDENTITY_NAMESPACE` (default `obco-lab-identity`), `LAB_CONNECTION`
(default `lab-openbao`) and `LAB_LDAP_SHA_FILE` (default
`.tmp/lab/plugins/sha256`). The directory chart values can be inspected with
`helm show values hack/lab/plugins/chart`.

The config uses `CreateOrAdopt` because the LDAP plugin exposes its default config
as soon as the auth mount starts. A 30-second drift interval lets initially missing
mount dependencies converge during installation.

Installation generates random directory credentials and a seven-day LDAPS
certificate, stores credentials only in Kubernetes Secrets, and retains existing
Secrets on repeat runs. The directory uses an ephemeral database: this is a test
profile, not a directory backup or persistence test. Verification rejects a wrong
password, checks both real group membership and the explicit user mapping, checks
allowed read capabilities, reads both seeded group/user secrets, and rejects reads
of a seeded forbidden secret and an admin request, and revokes its token.
It does not issue cloud credentials or prove production LDAP failover.

## Update dependency pins

Renovate tracks the LLDAP image. Update the LDAP plugin release manually: change the
release URL, both architecture-specific archive checksums in `build-image.sh`, and
the catalogue version in `chart/values.yaml` together. Verify the release assets
and rerun this profile before merging; changing only the version would leave the
verified executable and catalogue metadata inconsistent.
