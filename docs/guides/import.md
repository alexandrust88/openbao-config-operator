---
title: Import existing OpenBao configuration safely
description: Generate Kubernetes resources from an existing OpenBao server, review them in Observe mode and adopt configuration incrementally.
---

# Import existing OpenBao configuration safely

`obc-import` reads a live OpenBao server and writes one custom resource per object it can map to a typed
kind. Every resource has `managementPolicy: Observe` and `creationPolicy: Adopt`: once applied, the operator
reads the object and reports drift, and never writes or deletes. You switch resources to `Manage` one at a
time, so adoption of a running server is incremental.

## Before you begin

- A clone of the repository at the release you run, as in the
  [quickstart](../get-started/quickstart.md#before-you-begin), and Go 1.26 or later. Releases do
  not ship an `obc-import` binary.
- An OpenBao token that may read and list the paths you import.

## Run it

`read` prompts for a read-only token without echoing it:

```bash
go build -o bin/obc-import ./cmd/obc-import
read -rs BAO_TOKEN
export BAO_ADDR=https://openbao.example:8200 BAO_CACERT=ca.pem BAO_TOKEN
bin/obc-import -connection openbao -server openbao -ca-namespace openbao-system -k8s-namespace openbao-config -namespace tenants/a -recursive -o imported/
```

| flag | meaning |
| --- | --- |
| `-address`, `-ca-cert`, `-token` | server, CA bundle and token; default to `BAO_ADDR`, `BAO_CACERT`, `BAO_TOKEN`. Prefer the env for the token |
| `-namespace` | OpenBao namespace to read (default `BAO_NAMESPACE`); use the namespace the connection points at, `spec.namespace` of each resource is relative to it |
| `-recursive` | also read every child namespace |
| `-connection`, `-connection-kind` | `connectionRef` of every resource (`OpenBaoConnection` by default, or `ClusterOpenBaoConnection`); `-connection` is required |
| `-server` | also emit the `OpenBaoServer` of this name and the connection itself, see below |
| `-ca-namespace` | namespace of the `OpenBaoServer` CA bundle Secret; required with `-server` and `-ca-cert`. Use a platform namespace tenants cannot write |
| `-k8s-namespace` | `metadata.namespace` of every resource; required |
| `-include-kv-paths` | emit a `KVSecret` skeleton per KV path |
| `-o` | a directory (one file per kind, `<group>-<kind>.yaml`, mode 0600) or `-` for one stream on stdout (default) |
| `-timeout` | timeout of each OpenBao request (default `30s`) |

The token needs read and list on the imported paths, plus `sudo` on `sys/audit` for audit devices. The
tool only sends GET and LIST requests. A path it cannot read is listed on stderr and skipped; only unreadable
mount tables (`sys/auth`, `sys/mounts`) of the top namespace stop it.

## The connection

Connections hold no address: a connection names an `OpenBaoServer` in `spec.serverRef`, and the server holds the
address, CA bundle and limits. Every imported resource references the connection named by `-connection`, so
that connection must use `serverRef` to the server you imported from, with `spec.namespace` set to the
`-namespace` you imported.

With `-server <name>` the tool writes both for you: an `OpenBaoServer` with the `-address` (and, when
`-ca-cert` is set, `caBundle` from a Secret `<name>-ca`, key `ca.crt`, in `-ca-namespace`), allowed for
`-k8s-namespace` and the token audience `openbao`; and the connection, with `serverRef` and `namespace`, that
logs in with Kubernetes auth as the ServiceAccount named after the connection, using the OpenBao role of the
same name. Review that `spec.auth` before applying; the summary repeats it. Without `-server`, write the
connection yourself. An `http://` address (such as a port-forward) sets `allowInsecureHTTP: true` on the
server and the summary prints a warning: replace the address with the https one before you apply or commit
the files. Output files are written with mode 0600.

## What is imported

Namespaces, ACL and password policies, auth and secrets mounts, Kubernetes, JWT/OIDC, AppRole, userpass and
cert auth configs and roles, token roles, entities and entity aliases, groups and group aliases, the OIDC
provider set (keys, scopes, clients, assignments, providers), PKI issuers, config and roles, transit keys, SSH
roles, database connections and roles, audit devices and rate limit quotas (both root namespace only), and
with `-include-kv-paths` KV paths. Audit devices declared in the server configuration are adopted the same way;
their options that can carry credentials (`headers`) become `optionsSecretRefs`. Built-in objects that users often customise, such as the `default` policy and the `default` OIDC
key and provider, are imported too; delete the resources you do not want.

References use names, never server IDs: aliases name their entity, group and auth mount path, OIDC
assignments and providers name entities, groups and clients. The tool checks each resource with the kind's
own comparison and lists on stderr every resource that would report drift.

## What you must add

OpenBao never returns write-only inputs. The tool sets each such field to a Secret named after the resource,
keyed by the OpenBao field name (for example `KubernetesAuthConfig/kubernetes spec.tokenReviewerJWTSecretRef:
Secret kubernetes key token_reviewer_jwt`), and lists them on stderr. A `KVSecret` gets `dataFrom` pointing at a
Secret of its own name; values are never read.

Create these Secrets with their real values before you switch to `Manage`. In Observe mode a missing write-only
Secret resolves to an empty value and write-only values are never compared, so the resource reports `Observed`
either way; the first write in `Manage` then sends whatever the Secret holds, empty included. A `KVSecret`
is compared, so its Secret needs the real data.

Not imported: objects of kinds without a typed CRD. The stderr summary names everything it skipped.


## Review before enabling management

1. Read the import summary and skipped-path messages. An import is an inventory of the paths the
   token could read, not proof that every configuration path was exported.
2. Review the generated server, connection, OpenBao namespaces and policy contents. Prepare any
   missing Secret inputs described above. Keep the output directory private; do not commit it
   until that review is complete.
3. Apply the reviewed resources while retaining `managementPolicy: Observe` and
   `creationPolicy: Adopt`:

   ```bash
   kubectl apply -f imported/
   kubectl -n openbao-config get openbao
   ```

4. Inspect each resource's `Synced` condition. `Observed` means the comparable fields match;
   `Drifted` identifies differences to review. For a policy, for example:

   ```bash
   kubectl -n openbao-config describe policy <imported-policy-name>
   ```

   Observe mode does not prove that missing write-only inputs are correct. Resolve those inputs
   before allowing writes.
5. Follow [Migrate to the operator](migration.md) to stop the previous writer and switch resources
   to `Manage` one at a time. Keep `deletionPolicy: Orphan` until you have reviewed deletion effects.

See [status and events](../reference/status-and-events.md) when an imported resource is not
observed as expected, and the [kind catalogue](../kinds.md) for supported paths and lifecycle notes.
