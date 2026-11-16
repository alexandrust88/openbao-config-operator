An OpenBaoConnection selects a platform-managed OpenBaoServer and an authentication method.
The server must admit the Kubernetes namespace of this connection. Credential and CA references
must satisfy the reference rules described in the fields below.

Namespaced connections use their configured identity, never the operator's own ServiceAccount.
Use a ClusterOpenBaoConnection when platform administrators deliberately need injected identity.
The authentication namespace identifies where login happens; the target namespace identifies where
configuration requests run. They need not be the same.

Ready/authenticated status proves the connection checks succeeded. It does not grant access to
every OpenBao endpoint: each managed resource can still report PermissionDenied. The OpenBao
policy attached to the identity is the effective capability boundary.

Delete dependent managed resources while the operator runs before deleting their connection and
server. In-use finalizers preserve those dependencies for cleanup; uninstalling the operator first
prevents it from completing that cleanup.
