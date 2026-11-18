---
title: Import existing OpenBao configuration safely
description: Generate Kubernetes resources from an existing OpenBao server, review them in Observe mode and adopt configuration incrementally.
---

# Import existing OpenBao configuration safely

`obc-import` reads a running OpenBao server and writes one custom resource per object it can map to a
typed kind. Every imported resource has `managementPolicy: Observe` and `creationPolicy: Adopt`: once
applied, the operator reads the object and reports drift, and never writes or deletes. You then switch
resources to `Manage` one at a time ([Migrate to the operator](migration.md)).

## Before you begin

- The operator is installed and the tenant connection `openbao` in the Kubernetes namespace
  `team-a`, based in the OpenBao namespace `tenants/team-a`, is `Ready`
  ([Install](../get-started/install.md),
  [Organise OpenBao namespaces and tenants](namespaces-and-tenants.md)). Every imported resource
  references this existing connection (step 4).
- The connection's OpenBao policy lets the operator read every path you import
  ([OpenBao policy for the operator](../operate/openbao-policy.md) lists the paths per kind).
  `Manage` later needs the write capabilities as well.
- Go 1.26 or later. Releases do not ship an `obc-import` binary: you build it from the source of
  the release you run.
- An OpenBao token for the import that can read and list the paths you import. Importing audit
  devices also needs `sudo` on `sys/audit`, and importing entity and group aliases needs `update`
  on `identity/lookup/entity` and `identity/lookup/group`.
- `kubectl` access to the Kubernetes namespace that will hold the resources; this guide uses
  `team-a`.
- The `bao` CLI, with `BAO_ADDR` and a token that can read the paths in the last step.

## 1. Build the tool

Build the command from the release you run. Go installs it as `obc-import` in `$(go env GOPATH)/bin`;
add that directory to your `PATH`:

```bash
go install github.com/alexandrust88/openbao-config-operator/cmd/obc-import@@TAG@
```

## 2. Run the import

`read` prompts for the token without echoing it, so the token stays out of the shell history:

```bash
read -rs BAO_TOKEN
export BAO_TOKEN BAO_ADDR=<openbao-address> BAO_CACERT=<ca-file>
obc-import -connection openbao -k8s-namespace team-a \
  -namespace tenants/team-a -recursive -o imported/
```

`<openbao-address>` is the `https://` address of the server and `<ca-file>` is a PEM file with its
CA bundle. This command reads the OpenBao namespace `tenants/team-a` and every OpenBao namespace
below it, and writes one file per kind to `imported/`. Every resource references the connection
`openbao` in `team-a`, and its `spec.openbaoNamespace` is relative to `tenants/team-a`. To import the root
OpenBao namespace instead, leave out `-namespace` and use the root connection:
`-connection openbao -k8s-namespace openbao-config`.

| flag | meaning |
| --- | --- |
| `-address`, `-ca-cert`, `-token` | server, CA bundle and token; default to `BAO_ADDR`, `BAO_CACERT`, `BAO_TOKEN`. Prefer the environment for the token: a flag shows in the process list |
| `-namespace` | OpenBao namespace to read (default `BAO_NAMESPACE`); use the OpenBao namespace the connection points at, `spec.openbaoNamespace` of each resource is relative to it |
| `-recursive` | also read every child OpenBao namespace |
| `-connection`, `-connection-kind` | `connectionRef` of every resource (`BaoConnection` by default, or `ClusterBaoConnection`); `-connection` is required |
| `-server` | also emit the `BaoServer` of this name and the connection itself (step 4) |
| `-ca-namespace` | Kubernetes namespace of the `BaoServer` CA bundle Secret; required with `-server` and `-ca-cert`. Use a platform namespace tenants cannot write |
| `-k8s-namespace` | `metadata.namespace` of every namespaced resource; required |
| `-include-kv-paths` | emit a `KVSecret` skeleton per KV path; values are never read |
| `-o` | a directory (one file per kind, `<group>-<kind>.yaml`, mode 0600) or `-` for one stream on stdout (default) |
| `-timeout` | timeout of each OpenBao request (default `30s`) |

The tool sends read and list requests, and for aliases the two identity lookups, which OpenBao
serves as updates and which change nothing. A path it cannot read is named on stderr and skipped.
It stops without writing any file when it cannot read the mount tables (`sys/auth`, `sys/mounts`)
of the OpenBao namespace you import, or when `-recursive` cannot list the child OpenBao namespaces of an
OpenBao namespace it reads.

## 3. Read the summary

The tool prints a summary on stderr. An import is an inventory of what the token could read and
what the tool has a kind for, not proof that every configuration path was exported.

| summary line | meaning | what to do |
| --- | --- | --- |
| `imported <n> resources` | the number of resources written | compare it with what you expect on the server |
| `warning: BaoServer/<name> sets allowInsecureHTTP: true` | the address was `http://`, for example a port-forward | replace the address with the `https://` one before you apply or commit the files (step 4) |
| `write-only inputs OpenBao never returns` and a list | each line names a resource, a field and the Secret and key the field points at | create those Secrets (step 5) |
| `<kind> ... not imported: <error>` | a path could not be read or listed, or an object could not be mapped | fix the token's policy and run the import again, or write that resource by hand |
| `<resource>: Observe will report drift on <keys>` | the generated spec does not read back equal in those keys | review those keys before `Manage` |
| `<resource>: <field> held credentials in a URL, emitted as REDACTED` | a URL carried a password or a credential parameter | set the real value before `Manage` (step 5) |
| `<resource>: OpenBao redacts the password in connection_url` | the connection URL holds OpenBao's placeholder, not the password | replace it before `Manage` (step 5) |
| `<resource>: spec.<field> not imported, its OpenBao value has an unexpected type` | one field was left out of the spec | set the field by hand |

The tool imports these kinds:

| group | kinds |
| --- | --- |
| `sys` | `BaoNamespace`, `ACLPolicy`, `PasswordPolicy`, `AuthMount`, `SecretsMount`; `Audit` and `RateLimitQuota` in the root namespace only |
| `auth` | `KubernetesAuthConfig`, `KubernetesAuthRole`, `JWTAuthConfig` and `JWTAuthRole` (mounts of type `jwt` and `oidc`), `AppRole`, `UserpassUser`, `CertAuthConfig`, `CertAuthRole`, `TokenRole` |
| `identity` | `IdentityEntity`, `IdentityEntityAlias`, `IdentityGroup`, `IdentityGroupAlias`, `OIDCKey`, `OIDCScope`, `OIDCClient`, `OIDCAssignment`, `OIDCProvider` |
| `secrets` | `PKIIssuer`, `PKIConfig`, `PKIRole`, `TransitKey`, `SSHRole`, `DatabaseConnection`, `DatabaseRole`; `KVSecret` with `-include-kv-paths` |

Audit devices declared in the server configuration are adopted the same way. Built-in objects that
users often customise, such as the `default` policy and the `default` OIDC key and provider, are
imported too; delete the resources you do not want. The `root` policy, the `token` auth mount, the
`sys`, `identity` and `cubbyhole` mounts and the `allow_all` OIDC assignment are left out.

Nothing else is imported, and the summary does not list it: an auth method or secrets engine
outside this table gives only its `AuthMount` or `SecretsMount`. Write those resources by hand
from the [kind catalogue](../kinds.md).

References use names instead of server IDs: aliases name their entity or group and their auth mount
path, and OIDC assignments and providers name entities, groups and clients. An entity, group or
client the tool cannot find by name keeps its ID.

## 4. Review the server and the connection

Connections hold no address: a connection names a `BaoServer` in `spec.serverRef`, and the server
holds the address, CA bundle and limits. Every imported resource references the connection named by
`-connection`, so that connection must use `serverRef` to the server you imported from, with
`spec.openbaoNamespace` set to the `-namespace` you imported. The tenant connection `openbao` in `team-a`
already does, so the command in step 2 generates neither a server nor a connection.

Use `-server <name>` only for a server that has no `BaoServer` yet, with a server name and a
`-connection` name that do not exist in the cluster: applying the files replaces an existing
`BaoServer` and connection of the same names. With `-server`, the tool writes both for you:

- a `BaoServer` with the `-address`, allowed for `-k8s-namespace` and the token audience
  `openbao`. When `-ca-cert` is set, its `caBundle` points at a Secret `<name>-ca`, key `ca.crt`, in
  `-ca-namespace`, a Kubernetes namespace that only the platform team can write;
- the connection, with `serverRef` and `openbaoNamespace`, that logs in with Kubernetes auth as the
  ServiceAccount named after the connection, using the OpenBao role of the same name.

The tool creates neither the CA bundle Secret, the ServiceAccount nor the OpenBao role. Create
them, or edit `spec.auth` to the login you already use; the summary repeats what it generated. The
generated connection sets no `openbaoAuthNamespace`, so the role must exist on the `kubernetes` auth mount
of the OpenBao namespace you imported.

!!! warning "Plain HTTP addresses"
    An `http://` address, such as a port-forward, sets `allowInsecureHTTP: true` on the generated
    server, and the summary prints a warning. Tokens and secret inputs would then cross the network
    in clear text: replace the address with the `https://` one before you apply or commit the files.

## 5. Create the Secrets for write-only inputs

OpenBao never returns write-only inputs such as passwords and client secrets. Where the kind requires
the input or OpenBao reports that one is set, the tool points the field at a Secret named after the
resource, keyed by the OpenBao field name, and lists it in the summary, for example:

```text
KubernetesAuthConfig/kubernetes spec.tokenReviewerJWTSecretRef: Secret kubernetes key token_reviewer_jwt
```

An optional input that OpenBao gives no sign of stays unset in the resource: add the reference
yourself where you know one is configured. A `KVSecret` gets `dataFrom` pointing at a Secret of its
own name; values are never read. Audit device options that can carry credentials (`headers`) become
`optionsSecretRefs`.

Create these Secrets with their real values in the Kubernetes namespace of the resources before you
switch to `Manage`:

```bash
kubectl -n team-a create secret generic kubernetes \
  --from-file=token_reviewer_jwt=<reviewer-token-file>
```

`<reviewer-token-file>` is a file that holds the real value of that input.

!!! warning "Observe cannot check write-only inputs"
    In `Observe` a missing write-only Secret resolves to an empty value, and write-only values are
    never compared, so the resource reports `Observed` either way. The first pass in `Manage` writes
    whatever the Secret holds, an empty value included. `KVSecret` data and `Audit`
    `optionsSecretRefs` are compared, so those Secrets need the real data before the resource reports
    `Observed`.

A credential inside a URL is never emitted. The field holds `REDACTED`, or for a database
`connection_url` the placeholder OpenBao returns instead of the password, and the summary names the
field. Set the real value in the resource, or move the credential to the kind's Secret reference,
before `Manage`: a write sends the field as the resource has it.

Keep the output directory private, and do not commit it until this review is complete.

## 6. Apply the resources in Observe mode

Apply the reviewed files with `managementPolicy: Observe` and `creationPolicy: Adopt` unchanged:

```bash
kubectl apply -f imported/
```

The operator adds no finalizer to a resource in `Observe`, so deleting one of these resources leaves
OpenBao as it is.

## 7. Check that it worked

List every resource of the operator in the Kubernetes namespace:

```bash
kubectl -n team-a get obco
```

Each imported resource shows `READY` `True`. `SYNCED` is `True` (reason `Observed`) when the fields
the operator compares match the spec, and `False` (reason `Drifted`) when they differ; the message
names the keys. For one resource, for example a policy:

```bash
kubectl -n team-a describe aclpolicies.sys.obco.dev <imported-policy-name>
```

Then confirm in OpenBao that the object is as it was, for the same policy:

```bash
bao policy read -namespace=tenants/team-a <policy-name>
```

`<imported-policy-name>` is the `metadata.name` of the resource and `<policy-name>` its `spec.name`.
The text equals `spec.policy` of the resource.

`Observed` does not prove that write-only inputs are correct. Resolve them (step 5) before you allow
writes. Then follow [Migrate to the operator](migration.md) to stop the previous writer and switch
resources to `Manage` one at a time. Keep `deletionPolicy: Orphan` until you have reviewed what
deleting each resource would remove.

## Troubleshooting

| symptom | check |
| --- | --- |
| `obc-import` stops with `read sys/auth in ...` or `read sys/mounts in ...` | the token cannot read the mount tables of the OpenBao namespace given with `-namespace` |
| `obc-import` stops with `list namespaces in ...` | with `-recursive` the token must list `sys/namespaces` in every OpenBao namespace it reads |
| the summary says a kind or an OpenBao namespace was `not imported` | the error after the colon; usually the token's policy lacks `read` or `list` on that path |
| no `Audit` or `RateLimitQuota` resources | both exist only in the root OpenBao namespace: import it without `-namespace`, with the root connection |
| the generated connection is not `Ready` | the ServiceAccount and the OpenBao role named after the connection, and the CA bundle Secret, must exist (step 4) |
| a resource reports `NotFound` | the object was removed or renamed after the import, or the connection's `openbaoNamespace` is not the `-namespace` you imported |
| a resource reports `PermissionDenied` | the connection's OpenBao policy does not allow reading that path |
| a resource reports `Drifted` | compare the keys in the message with the spec; the summary listed the ones the tool could not map |

## Related

- [BaoServer](../reference/api/core.md#baoserver)
- [BaoConnection](../reference/api/core.md#baoconnection)
- [Status and events](../reference/status-and-events.md): the `Observed`, `Drifted` and `NotFound`
  reasons.
- [Kind catalogue](../kinds.md): every typed kind with its OpenBao path.
- [Migrate to the operator](migration.md): hand the imported objects over.
- [Connect to OpenBao](../get-started/connect.md): servers, connections and their logins.
- [ADR 0016](../adr/0016-observe-mode-and-import-tool.md): why adoption goes through `Observe`.
