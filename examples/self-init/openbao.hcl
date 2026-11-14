# OpenBao self-initialization for openbao-config-operator. Pass this file as an extra -config next to the server
# configuration (storage, listener and an auto-unseal seal; self-init does not work with Shamir). At first boot
# OpenBao enables kubernetes auth, writes the operator policy and binds it to the operator ServiceAccount, then
# revokes the transient root token, so no root token is ever handed out. docs/operations/self-init.md has details.

initialize "kubernetes-auth" {
  request "enable-kubernetes-auth" {
    operation = "update"
    path      = "sys/auth/kubernetes"
    data = {
      type        = "kubernetes"
      description = "Kubernetes ServiceAccounts, including openbao-config-operator"
    }
  }

  # OpenBao runs in the cluster, so it reviews tokens with its own ServiceAccount and CA. That ServiceAccount
  # needs the system:auth-delegator ClusterRole.
  request "configure-kubernetes-auth" {
    operation = "update"
    path      = "auth/kubernetes/config"
    data = {
      kubernetes_host = "https://kubernetes.default.svc"
    }
  }
}

initialize "openbao-config-operator" {
  request "operator-policy" {
    operation = "update"
    path      = "sys/policies/acl/openbao-config-operator"
    data = {
      policy = <<-EOF
        path "auth/token/lookup-self" { capabilities = ["read"] }
        path "auth/token/renew-self"  { capabilities = ["update"] }

        path "sys/policies/acl/*"      { capabilities = ["create", "read", "update", "delete"] }
        path "sys/policies/password/*" { capabilities = ["create", "read", "update", "delete"] }
        path "sys/namespaces/*"        { capabilities = ["create", "read", "update", "delete"] }

        path "sys/auth"   { capabilities = ["read"] }
        path "sys/auth/*" { capabilities = ["create", "read", "update", "delete", "sudo"] }
        path "sys/mounts"   { capabilities = ["read"] }
        path "sys/mounts/*" { capabilities = ["create", "read", "update", "delete"] }

        path "auth/kubernetes/config" { capabilities = ["read"] }
        path "auth/kubernetes/role/*" { capabilities = ["create", "read", "update", "delete"] }

        path "identity/entity/name/*" { capabilities = ["create", "read", "update", "delete"] }
        path "identity/group/name/*"  { capabilities = ["create", "read", "update", "delete"] }
        path "identity/group/id/*"    { capabilities = ["read", "update"] }

        path "secret/data/*"     { capabilities = ["create", "read", "update"] }
        path "secret/metadata/*" { capabilities = ["delete"] }
      EOF
    }
  }

  # The operator logs in with the projected token the chart mounts for the audience "openbao-config-operator"
  # (chart value injectedIdentity.projectedToken) and a ClusterOpenBaoConnection with injectedIdentity.
  request "operator-role" {
    operation = "update"
    path      = "auth/kubernetes/role/openbao-config-operator"
    data = {
      bound_service_account_names      = ["openbao-config-operator"]
      bound_service_account_namespaces = ["openbao-config-operator"]
      audience                         = "openbao-config-operator"
      token_policies                   = ["openbao-config-operator"]
      token_ttl                        = "1h"
      token_max_ttl                    = "24h"
    }
  }
}
