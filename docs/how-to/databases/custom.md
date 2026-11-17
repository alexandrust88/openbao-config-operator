---
description: "Requirements for safely registering external OpenBao database plugins, including MongoDB, with operator-managed connections."
---

# Prepare a custom database plugin

The database lab runs six profiles using built-in OpenBao plugins. MongoDB requires a
separate plugin and is not included in the runnable suite. The reviewed official MongoDB
release binaries contained known vulnerable dependencies, so the lab does not download or
install them. A checksum establishes artifact identity; it does not establish security.

Before using an external database plugin:

1. Select a maintained release compatible with your OpenBao version. Verify its publisher,
   checksum, licence and vulnerability scan results, including its Go runtime and dependencies.
2. Install the approved executable in the configured plugin directory on every OpenBao node
   that may run it. The operator does not distribute binaries.
3. Register the executable using a `Plugin` CR with its exact checksum and database type.
   Wait for the registration to become Ready before configuring a connection.
4. Set `DatabaseConnection.spec.pluginName` to the registered catalogue name. Use
   `passwordSecretRef` for credentials and `pluginConfig` for the plugin's supported settings.
   There is no `pluginRef` field.
5. Verify TLS, tenant-scoped grants, real database login and read access, rejected writes,
   lease renewal and rejected fresh login after revocation before relying on the integration.

The [official OpenBao MongoDB plugin](https://github.com/openbao/openbao-plugins/tree/main/database/mongodb)
is an external implementation, not a built-in OpenBao 2.7.2 plugin. The existence of a Vault
plugin or a KubeVault example does not establish OpenBao compatibility; Elasticsearch is
also outside this suite's verified coverage.

See [database configuration](../../guides/database-secrets.md) for the operator resources
and the [dynamic database tutorial](../../tutorials/dynamic-database-credentials.md) for
runnable built-in profiles.
