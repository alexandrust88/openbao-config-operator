# Quickstart on kind

In this tutorial you run OpenBao and the operator on a local kind cluster, then manage your first
OpenBao objects from Kubernetes. At the end, a workload logs in to OpenBao and reads a password the
operator generated, and you watch the operator undo a change made behind its back.

It takes about ten minutes.

!!! warning "Evaluation only"
    The OpenBao server in this tutorial runs in dev mode: it keeps everything in memory, listens on
    plain HTTP and uses the root token `root`. Everything is lost when its pod restarts. Use this
    setup to learn the operator, and never to hold real secrets. [Install](install.md) and
    [Connect to OpenBao](connect.md) cover a real server.

## Before you begin

You need:

- Docker, or another container runtime that kind supports;
- [kind](https://kind.sigs.k8s.io/) 0.25 or later, whose default node image runs Kubernetes 1.31
  or later, as the chart requires;
- `kubectl`;
- [Helm](https://helm.sh/) 3.8 or later;
- `git`.

Clone the repository at the release you install. Every manifest this tutorial applies is in
`examples/quickstart/`, and every command is in `hack/docs/quickstart.sh`:

```bash
git clone --branch @TAG@ https://github.com/alexandrust88/openbao-config-operator.git
cd openbao-config-operator
```

Set the chart version and the names the commands use:

```bash
export VERSION=@VERSION@
--8<-- "hack/docs/quickstart.sh:env"
```

!!! tip "Run it in one go"
    `hack/docs/quickstart.sh` runs every step below and fails at the first one that does not reach
    its expected state. CI runs the same script on every change, so the steps on this page are
    tested:

    ```bash
    VERSION=@VERSION@ hack/docs/quickstart.sh
    ```

## 1. Create a kind cluster

```bash
--8<-- "hack/docs/quickstart.sh:cluster"
```

kind creates a one-node cluster named `openbao-quickstart` and switches `kubectl` to the context
`kind-openbao-quickstart`.

Confirm:

- [ ] `kubectl get nodes` shows one node with the status `Ready`.

## 2. Start OpenBao

Apply [`examples/quickstart/openbao-dev.yaml`](../../examples/quickstart/openbao-dev.yaml):
one OpenBao 2.7.2 pod in dev mode, a Service in front of it, and a binding that lets OpenBao
check ServiceAccount tokens with the Kubernetes TokenReview API.

```bash
--8<-- "hack/docs/quickstart.sh:openbao"
```

```text title="Expected output"
namespace/openbao created
serviceaccount/openbao created
clusterrolebinding.rbac.authorization.k8s.io/openbao-quickstart-auth-delegator created
deployment.apps/openbao created
service/openbao created
deployment "openbao" successfully rolled out
```

Dev mode starts OpenBao initialised and unsealed, so it is ready as soon as the pod is.

## 3. Let the operator log in to OpenBao

The operator never uses a root token. It logs in as a Kubernetes ServiceAccount through OpenBao's
Kubernetes auth method, and the OpenBao policy of that login decides what it may change. This
policy lets the operator manage ACL policies and Kubernetes auth roles whose names start with
`quickstart-`, and the KV mount `quickstart-kv`:

```hcl title="examples/quickstart/operator-quickstart.hcl"
--8<-- "examples/quickstart/operator-quickstart.hcl"
```

Enable the Kubernetes auth method, upload the policy and create the role
`operator-quickstart`. The role accepts only the ServiceAccount `openbao-config` of the namespace
`quickstart`, with a token for the audience `openbao`:

```bash
--8<-- "hack/docs/quickstart.sh:openbao-auth-enable"
--8<-- "hack/docs/quickstart.sh:openbao-auth"
```

```text title="Expected output"
Success! Enabled kubernetes auth method at: kubernetes/
Success! Data written to: auth/kubernetes/config
Success! Uploaded policy: operator-quickstart
Success! Data written to: auth/kubernetes/role/operator-quickstart
```

## 4. Install the operator

Install the chart from the OCI registry, with one replica, which is enough for a laptop:

```bash
--8<-- "hack/docs/quickstart.sh:operator"
```

Helm waits until the manager pod is ready and prints `STATUS: deployed`.

Confirm:

- [ ] `kubectl -n openbao-config-operator get deployment openbao-config-operator` shows `1/1`
      ready.

## 5. Connect the operator to OpenBao

Three objects connect the operator to OpenBao:

- the namespace `quickstart`, with the label `openbaoconfig.io/tenant: "true"` and the
  ServiceAccounts `openbao-config` (the operator's identity) and `app` (a workload);
- an `OpenBaoServer`, which says where OpenBao is and which namespaces may connect to it;
- an `OpenBaoConnection`, which says how the operator logs in.

=== "namespace.yaml"

    ```yaml
    --8<-- "examples/quickstart/namespace.yaml"
    ```

=== "openbaoserver.yaml"

    ```yaml
    --8<-- "examples/quickstart/openbaoserver.yaml"
    ```

=== "openbaoconnection.yaml"

    ```yaml
    --8<-- "examples/quickstart/openbaoconnection.yaml"
    ```

Apply them and wait until both report `Ready`:

```bash
--8<-- "hack/docs/quickstart.sh:connect"
```

```text title="Expected output"
namespace/quickstart created
serviceaccount/openbao-config created
serviceaccount/app created
openbaoserver.core.openbaoconfig.io/quickstart created
openbaoconnection.core.openbaoconfig.io/openbao created
openbaoserver.core.openbaoconfig.io/quickstart condition met
openbaoconnection.core.openbaoconfig.io/openbao condition met
```

The server is `Ready` when OpenBao answers its health check unsealed. The connection is `Ready`
when the operator has logged in as `openbao-config`.

## 6. Create your first resources

Four resources describe what you want in OpenBao:

- a `Policy` that may read one secret;
- a `SecretsMount`: a KV version 2 secrets engine at `quickstart-kv`;
- a `KubernetesAuthRole` that lets the ServiceAccount `app` log in and get that policy;
- a `RandomSecret` that generates a password into `quickstart-kv/app` and copies it to a
  Kubernetes Secret.

=== "policy.yaml"

    ```yaml
    --8<-- "examples/quickstart/policy.yaml"
    ```

=== "secretsmount.yaml"

    ```yaml
    --8<-- "examples/quickstart/secretsmount.yaml"
    ```

=== "kubernetesauthrole.yaml"

    ```yaml
    --8<-- "examples/quickstart/kubernetesauthrole.yaml"
    ```

=== "randomsecret.yaml"

    ```yaml
    --8<-- "examples/quickstart/randomsecret.yaml"
    ```

Notice that the role does not repeat the policy name: `policyRefs` names the `Policy` resource,
and the role waits until that policy is in OpenBao.

Apply them and wait until each one is `Ready`:

```bash
--8<-- "hack/docs/quickstart.sh:resources"
```

```text title="Expected output"
policy.sys.openbaoconfig.io/quickstart-app-read created
secretsmount.sys.openbaoconfig.io/quickstart-kv created
kubernetesauthrole.auth.openbaoconfig.io/quickstart-app created
randomsecret.secrets.openbaoconfig.io/quickstart-app-password created
policy.sys.openbaoconfig.io/quickstart-app-read condition met
secretsmount.sys.openbaoconfig.io/quickstart-kv condition met
kubernetesauthrole.auth.openbaoconfig.io/quickstart-app condition met
randomsecret.secrets.openbaoconfig.io/quickstart-app-password condition met
```

Every kind of the operator belongs to the `openbao` category, so one command lists them all with
their `Ready` and `Synced` columns:

```bash
--8<-- "hack/docs/quickstart.sh:status"
```

Confirm:

- [ ] Every resource in the list shows `Ready` `True`.

## 7. Check the result in OpenBao

Ask OpenBao directly. Read the policy, the type of the new mount and the policies of the new role:

```bash
--8<-- "hack/docs/quickstart.sh:verify"
```

```text title="Expected output"
path "quickstart-kv/data/app" {
  capabilities = ["read"]
}
kv
[quickstart-app-read]
```

## 8. Log in as the application

Now act as the workload. Request a token for the ServiceAccount `app`, log in to OpenBao with it,
and read the generated password with the OpenBao token you get back:

```bash
--8<-- "hack/docs/quickstart.sh:app-login"
```

OpenBao prints a random 32-character password. Read the copy in the Kubernetes Secret:

```bash
--8<-- "hack/docs/quickstart.sh:secret"
```

You should see the same password. The application's token can read `quickstart-kv/app` and
nothing else, because its only policy besides `default` is `quickstart-app-read`.

## 9. Watch the operator repair drift

Change the policy in OpenBao behind the operator's back, so that it grants every secret in the
mount:

```bash
--8<-- "hack/docs/quickstart.sh:drift"
```

```text title="Expected output"
Success! Uploaded policy: quickstart-app-read
```

The `Policy` resource checks OpenBao every 30 seconds (`driftInterval: 30s`), with up to 20%
jitter. Wait about a minute, then read the policy again and list the events of the resource:

```bash
--8<-- "hack/docs/quickstart.sh:drift-check"
```

The policy grants only `quickstart-kv/data/app` again, and an `Updated` event records the
rewrite.

## 10. Clean up

Delete the kind cluster. OpenBao, the operator and everything they hold go with it:

```bash
--8<-- "hack/docs/quickstart.sh:cleanup"
```

## What you built

- An OpenBao server and the operator on one kind cluster.
- An `OpenBaoServer` that admits only labelled namespaces, and an `OpenBaoConnection` that logs in
  with a short-lived ServiceAccount token instead of a stored credential.
- A policy, a KV version 2 mount, a Kubernetes auth role and a generated password, all declared in
  Kubernetes and checked against OpenBao.
- Proof that a workload can log in and read its secret, and that the operator restores what
  someone changes in OpenBao by hand.

## Troubleshooting

| symptom | check |
|---------|-------|
| `kind create cluster` fails because the cluster exists | reuse it (`kubectl config use-context kind-openbao-quickstart`) or delete it with step 10 |
| `helm upgrade --install` cannot find the chart | `VERSION` is a released chart version without the `v` prefix: `helm show chart oci://ghcr.io/alexandrust88/charts/openbao-config-operator --version "$VERSION"` |
| `openbaoserver/quickstart` does not become `Ready` | `kubectl get openbaoserver quickstart -o yaml` shows the reason; `kubectl -n openbao get pods` shows whether OpenBao runs |
| the connection reports `ConnectionDenied` | the namespace label: `kubectl get namespace quickstart --show-labels` must list `openbaoconfig.io/tenant=true` |
| the connection reports `AuthenticationFailed` | `kubectl -n quickstart describe openbaoconnection openbao` shows the OpenBao error; repeat step 3, and check that the ClusterRoleBinding `openbao-quickstart-auth-delegator` exists |
| a resource reports `PermissionDenied` | the operator's policy allows only names that start with `quickstart-` and the mount `quickstart-kv`: compare the resource with `examples/quickstart/operator-quickstart.hcl` |
| everything was `Ready`, and now the connection fails | the OpenBao pod restarted and dev mode lost its data: repeat step 3; the operator then recreates every object at its next check, and the `RandomSecret` generates a new password |

[Troubleshooting](../operate/troubleshooting.md) lists every condition reason.

## Next steps

<div class="grid cards" markdown>

-   :material-download: **[Install on a real cluster](install.md)**

    ---

    Verify the release, choose the scope and tenancy model, and install the chart.

-   :material-connection: **[Connect to your OpenBao](connect.md)**

    ---

    A TLS server, a CA bundle and the login method that fits your platform.

-   :material-key-chain: **[Kubernetes auth](../guides/kubernetes-auth.md)**

    ---

    Manage the auth method, its configuration and roles for your workloads.

-   :material-lightbulb-on: **[Concepts](../concepts/index.md)**

    ---

    Servers, connections, the tenant boundary and how the engine reconciles.

</div>
