path "sys/policies/acl/quickstart-*" {
  capabilities = ["create", "read", "update", "delete"]
}

path "sys/mounts" {
  capabilities = ["read"]
}

path "sys/mounts/quickstart-kv" {
  capabilities = ["create", "read", "update", "delete"]
}

path "sys/mounts/quickstart-kv/tune" {
  capabilities = ["create", "read", "update"]
}

path "auth/kubernetes/role/quickstart-*" {
  capabilities = ["create", "read", "update", "delete"]
}

path "quickstart-kv/data/*" {
  capabilities = ["create", "read", "update"]
}

path "quickstart-kv/metadata/*" {
  capabilities = ["read", "delete"]
}
