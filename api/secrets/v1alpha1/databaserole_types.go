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

// The kind of credential a database role issues.
// +kubebuilder:validation:Enum=password;rsa_private_key;client_certificate
type DatabaseCredentialType string

const (
	DatabaseCredentialPassword          DatabaseCredentialType = "password"
	DatabaseCredentialRSAPrivateKey     DatabaseCredentialType = "rsa_private_key"
	DatabaseCredentialClientCertificate DatabaseCredentialType = "client_certificate"
)

// +kubebuilder:validation:XValidation:rule="!has(self.credentialConfig) || !('ca_private_key' in self.credentialConfig)",message="credentialConfig must not set ca_private_key; use caPrivateKeySecretRef"
// +kubebuilder:validation:XValidation:rule="!has(self.caPrivateKeySecretRef) || (has(self.credentialType) && self.credentialType == 'client_certificate')",message="caPrivateKeySecretRef requires credentialType client_certificate"

// Credential settings shared by dynamic and static database roles.
type DatabaseCredentialFields struct {
	// credentialType is the kind of credential issued (`credential_type`). Allowed: password, rsa_private_key, client_certificate. Defaults to password.
	// +optional
	CredentialType DatabaseCredentialType `json:"credentialType,omitempty"`

	// credentialConfig configures the generator of the credential type (`credential_config`), for example password_policy for password, or key_bits and format for rsa_private_key. Must not set ca_private_key. Up to 16. Example: `{password_policy: strong}`.
	// +kubebuilder:validation:MaxProperties=16
	// +optional
	CredentialConfig map[string]string `json:"credentialConfig,omitempty"`

	// caPrivateKeySecretRef selects the Secret key holding the PEM private key of the CA set in credentialConfig ca_cert (`credential_config.ca_private_key`); client_certificate only. Read from a Secret key; never stored in status. Write-only: a change of the Secret is written again. Example: `{name: db-client-ca, key: tls.key}`.
	// +optional
	CAPrivateKeySecretRef *commonv1alpha1.SecretKeySelector `json:"caPrivateKeySecretRef,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="self.mountPath == oldSelf.mountPath",message="mountPath is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.name) == has(oldSelf.name) && (!has(self.name) || self.name == oldSelf.name)",message="name is immutable"

// spec is the desired dynamic role of a database secrets engine at `<mountPath>/roles/<name>`.
type DatabaseRoleSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`
	DatabaseCredentialFields    `json:",inline"`

	// mountPath is the path of the database secrets engine mount, without leading or trailing slashes. Immutable. Example: `database`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +required
	MountPath string `json:"mountPath"`

	// name is the role name in OpenBao. Defaults to metadata.name. Immutable. Example: `billing-app`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^\w([\w.-]*\w)?$`
	// +optional
	Name string `json:"name,omitempty"`

	// dbName is the name of the database connection the role uses (`db_name`); the connection must list the role in allowedRoles. Example: `billing-postgres`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +required
	DBName string `json:"dbName"`

	// defaultTTL is the default lease duration of issued credentials (`default_ttl`), a duration. Defaults to the mount's default lease TTL. Example: `1h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	DefaultTTL *metav1.Duration `json:"defaultTTL,omitempty"`

	// maxTTL is the maximum lease duration of issued credentials (`max_ttl`), a duration. Defaults to the mount's max lease TTL. Example: `24h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	MaxTTL *metav1.Duration `json:"maxTTL,omitempty"`

	// creationStatements are the database statements that create a user (`creation_statements`), with `{{name}}`, `{{password}}` and `{{expiration}}` templates. Up to 64. Example: `["CREATE ROLE \"{{name}}\" WITH LOGIN PASSWORD '{{password}}';"]`.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=8192
	// +optional
	CreationStatements []string `json:"creationStatements,omitempty"`

	// revocationStatements are the database statements that revoke a user (`revocation_statements`). Empty uses the plugin default. Up to 64. Example: `["DROP ROLE \"{{name}}\";"]`.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=8192
	// +optional
	RevocationStatements []string `json:"revocationStatements,omitempty"`

	// rollbackStatements are the database statements that undo a failed user creation (`rollback_statements`). Up to 64.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=8192
	// +optional
	RollbackStatements []string `json:"rollbackStatements,omitempty"`

	// renewStatements are the database statements that extend the lifetime of a user (`renew_statements`). Up to 64.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=8192
	// +optional
	RenewStatements []string `json:"renewStatements,omitempty"`
}

// status is the observed state of the database role.
type DatabaseRoleStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=baodbr,categories=obco;obco-secrets
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// DatabaseRole manages a dynamic role of a database secrets engine at `<mount>/roles/<name>`: OpenBao creates a short-lived database user per request (`<mount>/creds/<name>`) with the creation statements.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: database
//	  name: billing-app
//	  dbName: billing-postgres
//	  defaultTTL: 1h
//	  creationStatements:
//	    - CREATE ROLE "{{name}}" WITH LOGIN PASSWORD '{{password}}';
type DatabaseRole struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec DatabaseRoleSpec `json:"spec"`

	// +optional
	Status DatabaseRoleStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (r *DatabaseRole) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &r.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (r *DatabaseRole) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &r.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// DatabaseRoleList contains a list of DatabaseRole.
type DatabaseRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []DatabaseRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DatabaseRole{}, &DatabaseRoleList{})
}
