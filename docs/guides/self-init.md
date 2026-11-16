# OpenBao self-initialisation

OpenBao can initialise itself at first boot from `initialize` blocks in its configuration. With
[examples/self-init/openbao.hcl](../../examples/self-init/openbao.hcl) the server:

1. enables the `kubernetes` auth method and points it at the in-cluster API server;
2. writes the policy `openbao-config-operator` (a platform operator in the root namespace; trim it
   with [OpenBao policy for the operator](../operate/openbao-policy.md));
3. creates the role `auth/kubernetes/role/openbao-config-operator`, bound to the ServiceAccount
   `openbao-config-operator` in the namespace `openbao-config-operator`, with audience
   `openbao-config-operator`;
4. revokes the root token it used. No root token is ever printed or stored.

The operator then logs in as itself, so nobody has to create a token for it.

## Use it

1. Add the file to the OpenBao server as an extra `-config` (for the OpenBao Helm chart, as an
   extra config file next to `server.hcl`). Self-init needs an auto-unseal seal; it does not work
   with Shamir.
2. Bind the OpenBao server's ServiceAccount to the `system:auth-delegator` ClusterRole, so the
   kubernetes auth method can review tokens.
3. Install the operator in the namespace `openbao-config-operator` with release name
   `openbao-config-operator` (the ServiceAccount name the role expects). The chart mounts a
   projected token for the audience `openbao-config-operator` by default:

   ```sh
   helm install openbao-config-operator oci://ghcr.io/alexandrust88/charts/openbao-config-operator \
     --version @VERSION@ \
     --namespace openbao-config-operator --create-namespace
   ```

4. Create a `ClusterOpenBaoConnection` with `auth.kubernetes.role: openbao-config-operator` and
   `injectedIdentity: {}`; its default `tokenPath` is the chart's token,
   `/var/run/secrets/openbao/token`. See [Connect to OpenBao](../get-started/connect.md#log-in-as-the-operator).

Self-init runs only once, on empty storage. The example denies the operator access to change its
own policy, auth mount, auth configuration and login role. Use a separate bootstrap administrator
identity to update those objects; a `Policy` or `AuthMount` using this connection cannot manage them.
Existing installations need the updated policy applied separately: upgrading the operator or
changing the self-init file does not rerun initialisation. Before applying the updated policy,
set `deletionPolicy: Orphan` on any resources that manage these bootstrap objects and remove
those resources from Kubernetes and their GitOps source. Keep the OpenBao objects in place;
otherwise the new denials can leave reconciliation or deletion blocked.

After a policy or login-role update, check the ClusterOpenBaoConnection's Ready and authenticated
status, then reconcile a harmless configuration resource using that connection. Keep the bootstrap
administrator available until both checks succeed. If login fails, repair the bootstrap objects
with that administrator; restarting the operator does not repair OpenBao configuration.

These denials prevent accidental self-lockout. They do not make broad policy-authoring rights a
safe tenant boundary: the operator can still author other powerful policies. Use separate,
restricted identities for tenant connections.

## Verify

[examples/self-init/verify.sh](../../examples/self-init/verify.sh) boots OpenBao 2.7.2 in a
container with the example, raft storage, a `static` seal and fake ServiceAccount files, then
checks through a test-only userpass login that the auth method, the policy and the role exist and
that the root token was revoked. It needs Docker, the `bao` CLI and `jq`:

```sh
examples/self-init/verify.sh
```

You should see `verify: self-init created kubernetes auth, the operator policy and role, and
revoked the root token`.
