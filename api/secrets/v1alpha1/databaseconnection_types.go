/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
)

// +kubebuilder:validation:XValidation:rule="self.mountPath == oldSelf.mountPath",message="mountPath is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.name) == has(oldSelf.name) && (!has(self.name) || self.name == oldSelf.name)",message="name is immutable"
// +kubebuilder:validation:XValidation:rule="self.pluginName == oldSelf.pluginName",message="pluginName is immutable"
// +kubebuilder:validation:XValidation:rule="!has(self.postgresql) || self.pluginName == 'postgresql-database-plugin'",message="postgresql requires pluginName postgresql-database-plugin"
// +kubebuilder:validation:XValidation:rule="!has(self.mysql) || self.pluginName in ['mysql-database-plugin', 'mysql-aurora-database-plugin', 'mysql-rds-database-plugin', 'mysql-legacy-database-plugin']",message="mysql requires a mysql pluginName"
// +kubebuilder:validation:XValidation:rule="!has(self.cassandra) || self.pluginName == 'cassandra-database-plugin'",message="cassandra requires pluginName cassandra-database-plugin"
// +kubebuilder:validation:XValidation:rule="!has(self.influxdb) || self.pluginName == 'influxdb-database-plugin'",message="influxdb requires pluginName influxdb-database-plugin"
// +kubebuilder:validation:XValidation:rule="!has(self.valkey) || self.pluginName in ['valkey-database-plugin', 'redis-database-plugin']",message="valkey requires pluginName valkey-database-plugin or redis-database-plugin"
// +kubebuilder:validation:XValidation:rule="!has(self.pluginConfig) || ['name', 'plugin_name', 'plugin_version', 'verify_connection', 'allowed_roles', 'root_rotation_statements', 'password_policy', 'password', 'private_key'].all(k, !(k in self.pluginConfig))",message="pluginConfig must not set top-level fields or secret material; use the typed fields and secret references"
// +kubebuilder:validation:XValidation:rule="!has(self.rotateRootOnCreate) || !self.rotateRootOnCreate || has(self.passwordSecretRef) || has(self.privateKeySecretRef)",message="rotateRootOnCreate requires passwordSecretRef or privateKeySecretRef"

// spec is the desired connection of a database secrets engine at `<mountPath>/config/<name>`. At most one plugin section matching pluginName may be set; pluginConfig carries settings of any other plugin or extra settings of a builtin one. An omitted allowedRoles, rootRotationStatements or passwordPolicy is reset to its default. OpenBao merges connection settings on update, so a setting removed from the spec keeps its last value until the connection is created again.
type DatabaseConnectionSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path of the database secrets engine mount, without leading or trailing slashes. Immutable. Example: `database`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +required
	MountPath string `json:"mountPath"`

	// name is the connection name in OpenBao. Defaults to metadata.name. Immutable. Example: `billing-postgres`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^\w([\w.-]*\w)?$`
	// +optional
	Name string `json:"name,omitempty"`

	// pluginName is the database plugin (`plugin_name`): a builtin plugin or one registered in the plugin catalog. Immutable. Example: `postgresql-database-plugin`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +required
	PluginName string `json:"pluginName"`

	// pluginVersion pins a version of a catalog plugin (`plugin_version`), in the canonical form OpenBao stores. Leave it empty for builtin plugins. Example: `v1.2.0`.
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$`
	// +optional
	PluginVersion string `json:"pluginVersion,omitempty"`

	// verifyConnection makes OpenBao connect to the database on every write of this connection (`verify_connection`). Sent with every write and never read back. Defaults to true.
	// +optional
	VerifyConnection *bool `json:"verifyConnection,omitempty"`

	// allowedRoles lists the roles that may use this connection (`allowed_roles`); `*` allows every role, empty allows none. Up to 256. Example: `[billing-app, billing-migrations]`.
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:items:MaxLength=128
	// +listType=set
	// +optional
	AllowedRoles []string `json:"allowedRoles,omitempty"`

	// rootRotationStatements are the database statements run to rotate the root credentials (`root_rotation_statements`). Empty uses the plugin default. Up to 64. Example: `["ALTER USER \"{{name}}\" WITH PASSWORD '{{password}}';"]`.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=4096
	// +optional
	RootRotationStatements []string `json:"rootRotationStatements,omitempty"`

	// passwordPolicy is the OpenBao password policy that generates passwords for this connection (`password_policy`). Empty uses the plugin default. Example: `postgres`.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	PasswordPolicy string `json:"passwordPolicy,omitempty"`

	// passwordSecretRef selects the Secret key holding the root password (`password`). Read from a Secret key; never stored in status. Write-only: OpenBao never returns it; a change of the Secret is written again. Example: `{name: pg-root, key: password}`.
	// +optional
	PasswordSecretRef *commonv1alpha1.SecretKeySelector `json:"passwordSecretRef,omitempty"`

	// privateKeySecretRef selects the Secret key holding the root private key, for plugins that authenticate with a key pair (`private_key`). Read from a Secret key; never stored in status. Write-only. Example: `{name: snowflake-root, key: rsa_key.p8}`.
	// +optional
	PrivateKeySecretRef *commonv1alpha1.SecretKeySelector `json:"privateKeySecretRef,omitempty"`

	// rotateRootOnCreate rotates the root credentials once (`<mountPath>/rotate-root/<name>`), right after the connection is created, so only OpenBao knows them. The password or private key in the Secret is then stale: it is sent only when the connection is created again. Requires passwordSecretRef or privateKeySecretRef. Defaults to false.
	// +optional
	RotateRootOnCreate bool `json:"rotateRootOnCreate,omitempty"`

	// postgresql holds the settings of postgresql-database-plugin.
	// +optional
	PostgreSQL *DatabasePostgreSQLConfig `json:"postgresql,omitempty"`

	// mysql holds the settings of the mysql, mysql-aurora, mysql-rds and mysql-legacy database plugins.
	// +optional
	MySQL *DatabaseMySQLConfig `json:"mysql,omitempty"`

	// cassandra holds the settings of cassandra-database-plugin.
	// +optional
	Cassandra *DatabaseCassandraConfig `json:"cassandra,omitempty"`

	// influxdb holds the settings of influxdb-database-plugin.
	// +optional
	InfluxDB *DatabaseInfluxDBConfig `json:"influxdb,omitempty"`

	// valkey holds the settings of valkey-database-plugin and redis-database-plugin.
	// +optional
	Valkey *DatabaseValkeyConfig `json:"valkey,omitempty"`

	// pluginConfig holds further connection settings by their OpenBao parameter name, for plugins without a section above. Values are sent as strings and must not be secret; top-level fields and secret material are rejected. Up to 64. Example: `{connection_url: "snowflake://{{username}}:{{password}}@acct/db"}`.
	// +kubebuilder:validation:MaxProperties=64
	// +optional
	PluginConfig map[string]string `json:"pluginConfig,omitempty"`
}

// Connection settings shared by the SQL database plugins.
type DatabaseSQLConfig struct {
	// connectionURL is the connection string (`connection_url`). Use the `{{username}}` and `{{password}}` templates instead of literal credentials so the root password can be rotated. Example: `postgresql://{{username}}:{{password}}@pg.db.svc:5432/billing`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="!self.matches('^([A-Za-z][A-Za-z0-9+.-]*://)?[^/?#@:]*:[^/?#@]*@') || self.matches('^([A-Za-z][A-Za-z0-9+.-]*://)?[^/?#@:]*:[{][{]password[}][}]@')",message="connectionURL must not contain a literal password: use the {{password}} template and passwordSecretRef"
	// +kubebuilder:validation:MaxLength=2048
	// +required
	ConnectionURL string `json:"connectionURL"`

	// username is the root user substituted for `{{username}}` (`username`). Example: `openbao`.
	// +kubebuilder:validation:MaxLength=256
	// +optional
	Username string `json:"username,omitempty"`

	// maxOpenConnections is the maximum number of open connections to the database (`max_open_connections`). Defaults to 4.
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxOpenConnections *int32 `json:"maxOpenConnections,omitempty"`

	// maxIdleConnections is the maximum number of idle connections to the database (`max_idle_connections`). 0 means maxOpenConnections. Defaults to 0.
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxIdleConnections *int32 `json:"maxIdleConnections,omitempty"`

	// maxConnectionLifetime is the maximum time a connection may be reused (`max_connection_lifetime`), a duration. 0 means no limit. Example: `30s`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	MaxConnectionLifetime *metav1.Duration `json:"maxConnectionLifetime,omitempty"`

	// usernameTemplate is the template for the names of generated users (`username_template`). Empty uses the plugin default. Example: `{{.RoleName}}-{{random 8}}`.
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	UsernameTemplate string `json:"usernameTemplate,omitempty"`
}

// Settings of postgresql-database-plugin.
type DatabasePostgreSQLConfig struct {
	DatabaseSQLConfig `json:",inline"`

	// disableEscaping turns off URL escaping of the username and password in the connection URL (`disable_escaping`). Defaults to false.
	// +optional
	DisableEscaping *bool `json:"disableEscaping,omitempty"`

	// passwordAuthentication is how passwords are sent to the PostgreSQL server (`password_authentication`). Allowed: password, scram-sha-256. Defaults to password.
	// +kubebuilder:validation:Enum=password;scram-sha-256
	// +optional
	PasswordAuthentication string `json:"passwordAuthentication,omitempty"`
}

// Settings of the MySQL database plugins.
type DatabaseMySQLConfig struct {
	DatabaseSQLConfig `json:",inline"`

	// tlsCA is the PEM-encoded CA certificate that verifies the MySQL server (`tls_ca`). Example: `-----BEGIN CERTIFICATE-----...`.
	// +kubebuilder:validation:MaxLength=65536
	// +optional
	TLSCA string `json:"tlsCA,omitempty"`

	// tlsCertificateKeySecretRef selects the Secret key holding the PEM-encoded client certificate and private key (`tls_certificate_key`). Read from a Secret key; never stored in status. Write-only. Example: `{name: mysql-client, key: tls.pem}`.
	// +optional
	TLSCertificateKeySecretRef *commonv1alpha1.SecretKeySelector `json:"tlsCertificateKeySecretRef,omitempty"`

	// tlsServerName overrides the server name used to verify the MySQL server certificate (`tls_server_name`). Example: `mysql.db.svc`.
	// +kubebuilder:validation:MaxLength=253
	// +optional
	TLSServerName string `json:"tlsServerName,omitempty"`

	// tlsSkipVerify disables verification of the MySQL server certificate (`tls_skip_verify`). Defaults to false.
	// +optional
	TLSSkipVerify *bool `json:"tlsSkipVerify,omitempty"`
}

// TLS settings shared by the Cassandra and InfluxDB plugins.
type DatabaseTLSPemConfig struct {
	// tls enables TLS to the database (`tls`). Defaults to false.
	// +optional
	TLS *bool `json:"tls,omitempty"`

	// insecureTLS skips verification of the database server certificate (`insecure_tls`). Defaults to false.
	// +optional
	InsecureTLS *bool `json:"insecureTLS,omitempty"`

	// tlsMinVersion is the minimum TLS version (`tls_min_version`). Allowed: tls10, tls11, tls12, tls13. Defaults to tls12.
	// +kubebuilder:validation:Enum=tls10;tls11;tls12;tls13
	// +optional
	TLSMinVersion string `json:"tlsMinVersion,omitempty"`

	// pemBundleSecretRef selects the Secret key holding a PEM bundle with the client certificate, its private key and the CA chain (`pem_bundle`). Read from a Secret key; never stored in status. Write-only. Example: `{name: cassandra-client, key: bundle.pem}`.
	// +optional
	PemBundleSecretRef *commonv1alpha1.SecretKeySelector `json:"pemBundleSecretRef,omitempty"`

	// pemJSONSecretRef selects the Secret key holding the JSON output of an OpenBao PKI issue call (`pem_json`), as an alternative to pemBundleSecretRef. Read from a Secret key; never stored in status. Write-only. Example: `{name: cassandra-client, key: issue.json}`.
	// +optional
	PemJSONSecretRef *commonv1alpha1.SecretKeySelector `json:"pemJSONSecretRef,omitempty"`

	// connectTimeout is the timeout for connecting to the database (`connect_timeout`), a duration. Example: `5s`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	ConnectTimeout *metav1.Duration `json:"connectTimeout,omitempty"`

	// usernameTemplate is the template for the names of generated users (`username_template`). Empty uses the plugin default. Example: `{{.RoleName}}_{{random 8}}`.
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	UsernameTemplate string `json:"usernameTemplate,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="!(has(self.pemBundleSecretRef) && has(self.pemJSONSecretRef))",message="set at most one of pemBundleSecretRef and pemJSONSecretRef"

// Settings of cassandra-database-plugin. Set at most one of pemBundleSecretRef and pemJSONSecretRef.
type DatabaseCassandraConfig struct {
	DatabaseTLSPemConfig `json:",inline"`

	// hosts are the Cassandra hosts to connect to (`hosts`, comma-joined). 1-64. Example: `[cassandra-0.db.svc, cassandra-1.db.svc]`.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=253
	// +required
	Hosts []string `json:"hosts"`

	// port is the native protocol port (`port`). 1-65535. Defaults to 9042.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +optional
	Port *int32 `json:"port,omitempty"`

	// protocolVersion is the CQL protocol version (`protocol_version`). Defaults to 2. Example: `4`.
	// +kubebuilder:validation:Minimum=1
	// +optional
	ProtocolVersion *int32 `json:"protocolVersion,omitempty"`

	// username is the root user (`username`). Example: `openbao`.
	// +kubebuilder:validation:MaxLength=256
	// +optional
	Username string `json:"username,omitempty"`

	// tlsServerName overrides the server name used to verify the Cassandra server certificate (`tls_server_name`). Example: `cassandra.db.svc`.
	// +kubebuilder:validation:MaxLength=253
	// +optional
	TLSServerName string `json:"tlsServerName,omitempty"`

	// skipVerification skips the consistency check of the root credentials on connect (`skip_verification`). Defaults to false.
	// +optional
	SkipVerification *bool `json:"skipVerification,omitempty"`

	// socketKeepAlive is the keep-alive period of connections (`socket_keep_alive`), a duration. Example: `30s`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	SocketKeepAlive *metav1.Duration `json:"socketKeepAlive,omitempty"`

	// consistency is the consistency level for statements (`consistency`). Example: `LOCAL_QUORUM`.
	// +kubebuilder:validation:MaxLength=32
	// +optional
	Consistency string `json:"consistency,omitempty"`

	// localDatacenter makes the driver prefer hosts of this datacenter (`local_datacenter`). Example: `dc1`.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	LocalDatacenter string `json:"localDatacenter,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="!(has(self.pemBundleSecretRef) && has(self.pemJSONSecretRef))",message="set at most one of pemBundleSecretRef and pemJSONSecretRef"

// Settings of influxdb-database-plugin. Set at most one of pemBundleSecretRef and pemJSONSecretRef.
type DatabaseInfluxDBConfig struct {
	DatabaseTLSPemConfig `json:",inline"`

	// host is the InfluxDB host (`host`). Example: `influxdb.monitoring.svc`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +required
	Host string `json:"host"`

	// port is the InfluxDB port (`port`). 1-65535. Defaults to 8086.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +optional
	Port *int32 `json:"port,omitempty"`

	// username is the root user (`username`). Example: `openbao`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +required
	Username string `json:"username"`
}

// +kubebuilder:validation:XValidation:rule="has(self.connectionURL) != (has(self.host) || has(self.port) || has(self.username))",message="set either connectionURL or host, port and username"

// Settings of valkey-database-plugin and redis-database-plugin. Set either connectionURL or host, port and username.
type DatabaseValkeyConfig struct {
	// connectionURL is the server URL (`connection_url`); it excludes host, port and username. Example: `valkeys://valkey.cache.svc:6379`.
	// +kubebuilder:validation:XValidation:rule="!self.matches('^([A-Za-z][A-Za-z0-9+.-]*://)?[^/?#@:]*:[^/?#@]*@') || self.matches('^([A-Za-z][A-Za-z0-9+.-]*://)?[^/?#@:]*:[{][{]password[}][}]@')",message="connectionURL must not contain a literal password: use the {{password}} template and passwordSecretRef"
	// +kubebuilder:validation:MaxLength=2048
	// +optional
	ConnectionURL string `json:"connectionURL,omitempty"`

	// host is the server host (`host`). Example: `valkey.cache.svc`.
	// +kubebuilder:validation:MaxLength=253
	// +optional
	Host string `json:"host,omitempty"`

	// port is the server port (`port`). 1-65535. Example: `6379`.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +optional
	Port *int32 `json:"port,omitempty"`

	// username is the root user (`username`). Example: `default`.
	// +kubebuilder:validation:MaxLength=256
	// +optional
	Username string `json:"username,omitempty"`

	// tls enables TLS (`tls`); it requires caCert. Defaults to false.
	// +optional
	TLS *bool `json:"tls,omitempty"`

	// insecureTLS skips verification of the server certificate (`insecure_tls`). Defaults to false.
	// +optional
	InsecureTLS *bool `json:"insecureTLS,omitempty"`

	// caCert is the PEM-encoded CA certificate that verifies the server (`ca_cert`). Example: `-----BEGIN CERTIFICATE-----...`.
	// +kubebuilder:validation:MaxLength=65536
	// +optional
	CACert string `json:"caCert,omitempty"`
}

// status is the observed state of the database connection.
type DatabaseConnectionStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=baodbc,categories=obco;obco-secrets
// +kubebuilder:printcolumn:name="Plugin",type=string,JSONPath=`.spec.pluginName`,priority=1
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// DatabaseConnection manages a connection of a database secrets engine at `<mount>/config/<name>`: the plugin, the root credentials (from a Secret) and the roles allowed to use it. DatabaseRole and DatabaseStaticRole objects issue credentials through it.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: database
//	  name: billing-postgres
//	  pluginName: postgresql-database-plugin
//	  allowedRoles: [billing-app]
//	  passwordSecretRef: {name: pg-root, key: password}
//	  postgresql:
//	    connectionURL: postgresql://{{username}}:{{password}}@pg:5432/billing
//	    username: openbao
type DatabaseConnection struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec DatabaseConnectionSpec `json:"spec"`

	// +optional
	Status DatabaseConnectionStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (c *DatabaseConnection) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &c.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (c *DatabaseConnection) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &c.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// DatabaseConnectionList contains a list of DatabaseConnection.
type DatabaseConnectionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []DatabaseConnection `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DatabaseConnection{}, &DatabaseConnectionList{})
}
