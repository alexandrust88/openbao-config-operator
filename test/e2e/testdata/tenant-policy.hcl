path "sys/policies/acl/e2e-*" {
  capabilities = ["create", "read", "update", "delete"]
}

path "sys/policies/password/e2e-*" {
  capabilities = ["create", "read", "update", "delete"]
}

path "secret/data/e2e/*" {
  capabilities = ["create", "read", "update", "delete"]
}

path "secret/metadata/e2e/*" {
  capabilities = ["read", "delete"]
}
