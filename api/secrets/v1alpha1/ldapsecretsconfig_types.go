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

// The directory schema the LDAP secrets plugin uses to store passwords.
// +kubebuilder:validation:Enum=openldap;racf;ad
type LDAPSchema string

const (
	LDAPSchemaOpenLDAP LDAPSchema = "openldap"
	LDAPSchemaRACF     LDAPSchema = "racf"
	LDAPSchemaAD       LDAPSchema = "ad"
)

// A TLS protocol version.
// +kubebuilder:validation:Enum=tls10;tls11;tls12;tls13
type LDAPTLSVersion string

// +kubebuilder:validation:XValidation:rule="has(self.mountPath) == has(oldSelf.mountPath) && (!has(self.mountPath) || self.mountPath == oldSelf.mountPath)",message="mountPath is immutable"
// +kubebuilder:validation:XValidation:rule="!has(self.deletionPolicy) || self.deletionPolicy == 'Orphan'",message="deletionPolicy must be Orphan: the plugin configuration is kept when the resource is deleted"
// +kubebuilder:validation:XValidation:rule="has(self.clientTLSCert) == has(self.clientTLSKeySecretRef)",message="set clientTLSCert and clientTLSKeySecretRef together"
// +kubebuilder:validation:XValidation:rule="!has(self.url) || self.url == self.url.lowerAscii()",message="url must be lowercase: OpenBao stores it lowercased"
// +kubebuilder:validation:XValidation:rule="(has(self.tlsMaxVersion) ? self.tlsMaxVersion : 'tls12') >= (has(self.tlsMinVersion) ? self.tlsMinVersion : 'tls12')",message="tlsMaxVersion must not be lower than tlsMinVersion; both default to tls12"

// spec is the desired directory connection of the LDAP secrets plugin at `<mountPath>/config`. The bind password is written whenever any field or the Secret changes, which overwrites a password changed by rotate-root; rotate the Secret instead of calling rotate-root on a mount this resource manages. deletionPolicy must be Orphan.
type LDAPSecretsConfigSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path the LDAP secrets plugin is mounted at. Defaults to `ldap`. Immutable. Example: `ldap`.
	// +kubebuilder:default=ldap
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// bindDN is the distinguished name OpenBao binds as to manage passwords (`binddn`). Example: `cn=openbao,ou=services,dc=example,dc=com`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	// +required
	BindDN string `json:"bindDN"`

	// bindPassSecretRef selects the Secret key holding the password of bindDN (`bindpass`). Read from a Secret key; never stored in status. Write-only. Example: `{name: ldap-bind, key: password}`.
	// +required
	BindPassSecretRef commonv1alpha1.SecretKeySelector `json:"bindPassSecretRef"`

	// url is the directory URL in lower case (`url`); separate several URLs with commas, tried in order. Defaults to `ldap://127.0.0.1`. Example: `ldaps://ldap.example.com`.
	// +kubebuilder:validation:XValidation:rule="!self.matches('://[^/?#]*@')",message="url must not contain credentials (user:password@ or token@): use the secret reference fields"
	// +kubebuilder:validation:MaxLength=2048
	// +optional
	URL string `json:"url,omitempty"`

	// userDN is the base DN under which user entries are searched (`userdn`). Example: `ou=users,dc=example,dc=com`.
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	UserDN string `json:"userDN,omitempty"`

	// userAttr is the attribute that holds the user name (`userattr`), such as cn, uid or userPrincipalName. Defaults to the schema's attribute (cn for openldap). Example: `uid`.
	// +kubebuilder:validation:MaxLength=256
	// +optional
	UserAttr string `json:"userAttr,omitempty"`

	// upnDomain is the userPrincipalName domain, so users can be found as `<name>@<upnDomain>` (`upndomain`). Example: `example.com`.
	// +kubebuilder:validation:MaxLength=256
	// +optional
	UPNDomain string `json:"upnDomain,omitempty"`

	// certificate is the PEM CA certificate that verifies the directory's TLS certificate (`certificate`). Example: `-----BEGIN CERTIFICATE-----...`.
	// +kubebuilder:validation:MaxLength=65536
	// +optional
	Certificate string `json:"certificate,omitempty"`

	// clientTLSCert is the PEM client certificate presented to the directory (`client_tls_cert`); set together with clientTLSKeySecretRef. OpenBao does not return it on read, so a change in OpenBao is not detected. Example: `-----BEGIN CERTIFICATE-----...`.
	// +kubebuilder:validation:MaxLength=65536
	// +optional
	ClientTLSCert string `json:"clientTLSCert,omitempty"`

	// clientTLSKeySecretRef selects the Secret key holding the PEM key of clientTLSCert (`client_tls_key`). Read from a Secret key; never stored in status. Write-only. Example: `{name: ldap-client, key: tls.key}`.
	// +optional
	ClientTLSKeySecretRef *commonv1alpha1.SecretKeySelector `json:"clientTLSKeySecretRef,omitempty"`

	// insecureTLS skips verification of the directory's TLS certificate (`insecure_tls`); not for production. Defaults to false.
	// +optional
	InsecureTLS *bool `json:"insecureTLS,omitempty"`

	// startTLS issues a STARTTLS command after connecting over plain LDAP (`starttls`). Defaults to false.
	// +optional
	StartTLS *bool `json:"startTLS,omitempty"`

	// tlsMinVersion is the minimum TLS version accepted from the directory (`tls_min_version`). Allowed: tls10, tls11, tls12, tls13. Defaults to tls12.
	// +optional
	TLSMinVersion LDAPTLSVersion `json:"tlsMinVersion,omitempty"`

	// tlsMaxVersion is the maximum TLS version accepted from the directory (`tls_max_version`); not lower than tlsMinVersion. Allowed: tls10, tls11, tls12, tls13. Defaults to tls12.
	// +optional
	TLSMaxVersion LDAPTLSVersion `json:"tlsMaxVersion,omitempty"`

	// connectionTimeout is how long to wait for a connection before trying the next URL (`connection_timeout`), a duration. Defaults to `30s`. Example: `10s`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	ConnectionTimeout *metav1.Duration `json:"connectionTimeout,omitempty"`

	// requestTimeout is how long to wait for a response to a directory request (`request_timeout`), a duration. Defaults to `90s`. Example: `30s`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	RequestTimeout *metav1.Duration `json:"requestTimeout,omitempty"`

	// passwordPolicy is the OpenBao password policy that generates passwords (`password_policy`). Empty uses the plugin default. Example: `ldap`.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	PasswordPolicy string `json:"passwordPolicy,omitempty"`

	// schema is the directory type (`schema`). Allowed: openldap, racf, ad. Defaults to openldap.
	// +optional
	Schema LDAPSchema `json:"schema,omitempty"`

	// skipStaticRoleImportRotation is the default for new static roles: do not rotate the password when the role is created (`skip_static_role_import_rotation`). Defaults to false.
	// +optional
	SkipStaticRoleImportRotation *bool `json:"skipStaticRoleImportRotation,omitempty"`
}

// status is the observed state of the LDAP secrets plugin configuration.
type LDAPSecretsConfigStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-secrets
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// LDAPSecretsConfig sets the directory connection of an LDAP secrets plugin mount (external plugin) at `<mount>/config`: where the directory is and the account OpenBao binds as to manage passwords. A singleton per mount; it is never deleted.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: ldap
//	  url: ldaps://ldap.example.com
//	  bindDN: cn=openbao,ou=services,dc=example,dc=com
//	  bindPassSecretRef: {name: ldap-bind, key: password}
//	  schema: openldap
type LDAPSecretsConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec LDAPSecretsConfigSpec `json:"spec"`

	// +optional
	Status LDAPSecretsConfigStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *LDAPSecretsConfig) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &o.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *LDAPSecretsConfig) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// LDAPSecretsConfigList contains a list of LDAPSecretsConfig.
type LDAPSecretsConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []LDAPSecretsConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LDAPSecretsConfig{}, &LDAPSecretsConfigList{})
}
