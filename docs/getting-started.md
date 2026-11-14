# Getting started

This page installs the operator, points it at an OpenBao server and manages a first ACL policy.
It takes about ten minutes. You need:

- a Kubernetes cluster (1.29 or later) and `kubectl` with cluster-admin rights for the install;
- Helm 3.8 or later;
- an initialised, unsealed OpenBao server (2.6 or later) that the operator pods can reach, and a
  `bao` CLI logged in with enough rights to enable an auth method and write a policy.

The GitHub organisation is not final. The commands use `openbao-config-operator`, the
organisation in the Go module path; replace it if the project moves.

## 1. Install the operator

The chart is published as an OCI artifact on ghcr.io. Verify the release first
([hardening: verify the release](security/hardening.md#verify-the-release)); the chart pins the
image by digest, so a verified chart pulls the verified image.

```sh
helm install openbao-config-operator \
  oci://ghcr.io/alexandrust88/charts/openbao-config-operator \
  --version <version> \
  --namespace openbao-config-operator --create-namespace
kubectl -n openbao-config-operator rollout status deployment/openbao-config-operator
```

The same chart is also served from a classic Helm repository on this site, for tools without
OCI support:

```sh
helm repo add openbao-config-operator https://alexandrust88.github.io/openbao-config-operator/charts
helm install openbao-config-operator openbao-config-operator/openbao-config-operator \
  --version <version> --namespace openbao-config-operator --create-namespace
```

[Install](operations/install.md) covers `install.yaml`, CRD handling, namespace-scoped installs,
high availability and monitoring.

## 2. Prepare OpenBao

The operator logs in with a ServiceAccount token of the tenant namespace, so it never needs a
root token. Enable the Kubernetes auth method and bind a role to a policy that limits what the
tenant may configure:

```sh
bao auth enable -path=kubernetes kubernetes
bao write auth/kubernetes/config kubernetes_host=https://kubernetes.default.svc:443

bao policy write team-a-config - <<'EOF'
path "sys/policies/acl/team-a-*" {
  capabilities = ["create", "read", "update", "delete"]
}
path "auth/token/lookup-self" {
  capabilities = ["read"]
}
path "auth/token/renew-self" {
  capabilities = ["update"]
}
EOF

bao write auth/kubernetes/role/team-a-config \
  bound_service_account_names=openbao-config \
  bound_service_account_namespaces=team-a \
  audience=openbao \
  token_policies=team-a-config \
  token_ttl=1h token_max_ttl=4h
```

[OpenBao policy](operations/openbao-policy.md) has least-privilege policies for platform and
tenant identities, and [self-init](operations/self-init.md) creates the operator's role at
OpenBao's first boot.

## 3. Declare the server

A platform admin declares the server once. `allowedNamespaces` selects the namespaces whose
connections may use it and `allowedAudiences` lists the token audiences they may request:

```yaml
apiVersion: core.openbaoconfig.io/v1alpha1
kind: OpenBaoServer
metadata:
  name: openbao
spec:
  address: https://openbao.openbao.svc:8200
  caBundle:
    configMapRef:
      namespace: openbao
      name: openbao-ca
      key: ca.crt
  allowedNamespaces:
    matchLabels:
      openbaoconfig.io/tenant: "true"
  allowedAudiences:
    - openbao
```

```sh
kubectl create namespace team-a
kubectl label namespace team-a openbaoconfig.io/tenant=true
kubectl apply -f openbaoserver.yaml
kubectl get openbaoserver openbao
```

`Ready=True` means the server answered `sys/health` and is unsealed.

## 4. Connect a tenant namespace

The connection logs in as a ServiceAccount of its own namespace with a short-lived TokenRequest
token for the `openbao` audience:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: openbao-config
  namespace: team-a
---
apiVersion: core.openbaoconfig.io/v1alpha1
kind: OpenBaoConnection
metadata:
  name: openbao
  namespace: team-a
spec:
  serverRef:
    name: openbao
  auth:
    kubernetes:
      mountPath: kubernetes
      role: team-a-config
      serviceAccountRef:
        name: openbao-config
      audiences:
        - openbao
```

```sh
kubectl apply -f connection.yaml
kubectl -n team-a get openbaoconnection openbao
```

[Connections](operations/connections.md) covers JWT and AppRole auth, OpenBao namespaces and
cluster-scoped connections.

## 5. Manage a policy

```yaml
apiVersion: sys.openbaoconfig.io/v1alpha1
kind: Policy
metadata:
  name: team-a-app-read
  namespace: team-a
spec:
  connectionRef:
    name: openbao
  policy: |
    path "secret/data/team-a/app/*" {
      capabilities = ["read"]
    }
```

```sh
kubectl apply -f policy.yaml
kubectl -n team-a get policies.sys.openbaoconfig.io team-a-app-read
bao policy read team-a-app-read
```

`Ready=True` means the policy in OpenBao matches the spec; `Synced=True` means the last reconcile
succeeded. Edit the policy in OpenBao and the operator puts it back at the next drift check.

The policy defaults to `creationPolicy: Create` and `deletionPolicy: Orphan`: deleting the
custom resource leaves the policy in OpenBao. Set `deletionPolicy: Delete` to remove it with the
resource.

## Next steps

- [Concepts](concepts.md): connections, the tenant boundary and the engine rules.
- [Kind catalogue](kinds.md) and [API reference](reference/api/index.md): every kind and field.
- [Import CLI](import.md): adopt an OpenBao server that is already configured.
- [Troubleshooting](operations/troubleshooting.md): conditions, reasons and metrics.
