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

// +kubebuilder:validation:XValidation:rule="has(self.clientTLSCert) == has(self.clientTLSKeySecretRef)",message="set clientTLSCert and clientTLSKeySecretRef together"
// +kubebuilder:validation:XValidation:rule="!has(self.bindPassSecretRef) || has(self.bindDN)",message="bindPassSecretRef requires bindDN"
// +kubebuilder:validation:XValidation:rule="!has(self.tlsMinVersion) || !has(self.tlsMaxVersion) || self.tlsMinVersion <= self.tlsMaxVersion",message="tlsMinVersion must not exceed tlsMaxVersion"

// LDAP server, bind and search parameters shared by the LDAP auth method and the LDAP group lookup of the Kerberos auth method.
type LDAPConnectionFields struct {
	// URL of the LDAP server. Several URLs separated by commas are tried in order. OpenBao stores it in lower case. Required. Maps to `url`. Example: ldaps://ldap1.example.com,ldaps://ldap2.example.com
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	// +required
	URL string `json:"url"`

	// Distinguished name of the service account used to search for users and groups. Unset uses an anonymous bind for searches. Required by bindPassSecretRef. Maps to `binddn`. Example: cn=openbao,ou=Services,dc=example,dc=org
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	BindDN string `json:"bindDN,omitempty"`

	// Secret key holding the password of bindDN. Read from this Secret key; the value is never stored in status. OpenBao never returns it, so it is compared only through status.writeOnlyHash and a Secret change forces a write. Maps to `bindpass`. Example: {name: ldap-bind, key: password}
	// +optional
	BindPassSecretRef *commonv1alpha1.SecretKeySelector `json:"bindPassSecretRef,omitempty"`

	// Search base for user entries. Maps to `userdn`. Example: ou=People,dc=example,dc=org
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	UserDN string `json:"userDN,omitempty"`

	// Attribute of a user entry that holds the login name, such as cn, uid or sAMAccountName. OpenBao defaults to cn and stores it in lower case. Maps to `userattr`. Example: samaccountname
	// +kubebuilder:validation:MaxLength=256
	// +optional
	UserAttr string `json:"userAttr,omitempty"`

	// Go template for the user search filter, with {{.UserAttr}} and {{.Username}} in scope. OpenBao defaults to ({{.UserAttr}}={{.Username}}). Maps to `userfilter`. Example: (&(objectClass=person)({{.UserAttr}}={{.Username}}))
	// +kubebuilder:validation:MaxLength=2048
	// +optional
	UserFilter string `json:"userFilter,omitempty"`

	// Active Directory user principal name domain: users bind as <username>@<upnDomain>. Maps to `upndomain`. Example: example.org
	// +kubebuilder:validation:MaxLength=256
	// +optional
	UPNDomain string `json:"upnDomain,omitempty"`

	// When true, an anonymous bind is used to discover the DN of the user before binding. Defaults to false. Maps to `discoverdn`. Example: true
	// +optional
	DiscoverDN *bool `json:"discoverDN,omitempty"`

	// When true, a login with an empty password is rejected instead of being treated as an unauthenticated bind. OpenBao defaults to true. Maps to `deny_null_bind`. Example: true
	// +optional
	DenyNullBind *bool `json:"denyNullBind,omitempty"`

	// When true, the login user name, not the user DN, becomes the entity alias name. Defaults to false. Maps to `username_as_alias`. Example: true
	// +optional
	UsernameAsAlias *bool `json:"usernameAsAlias,omitempty"`

	// Search base for group membership searches. Unset disables group lookups. Maps to `groupdn`. Example: ou=Groups,dc=example,dc=org
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	GroupDN string `json:"groupDN,omitempty"`

	// Go template for the group membership search, with {{.UserDN}} and {{.Username}} in scope. OpenBao defaults to (|(memberUid={{.Username}})(member={{.UserDN}})(uniqueMember={{.UserDN}})). Maps to `groupfilter`. Example: (&(objectClass=group)(member:1.2.840.113556.1.4.1941:={{.UserDN}}))
	// +kubebuilder:validation:MaxLength=2048
	// +optional
	GroupFilter string `json:"groupFilter,omitempty"`

	// Attribute of the entries returned by groupFilter that holds the group name, such as cn or memberOf. OpenBao defaults to cn. Maps to `groupattr`. Example: cn
	// +kubebuilder:validation:MaxLength=256
	// +optional
	GroupAttr string `json:"groupAttr,omitempty"`

	// When true, group membership is resolved through the Active Directory tokenGroups attribute of the user, which includes nested groups. Defaults to false. Maps to `use_token_groups`. Example: true
	// +optional
	UseTokenGroups *bool `json:"useTokenGroups,omitempty"`

	// When true, group searches use an anonymous bind; the bind credentials are still used for the initial connection. Defaults to false. Maps to `anonymous_group_search`. Example: false
	// +optional
	AnonymousGroupSearch *bool `json:"anonymousGroupSearch,omitempty"`

	// When true, user and group names are compared case-sensitively when matching OpenBao groups and users. Defaults to false. Maps to `case_sensitive_names`. Example: false
	// +optional
	CaseSensitiveNames *bool `json:"caseSensitiveNames,omitempty"`

	// PEM-encoded CA certificate used to verify the LDAP server certificate. Unset uses the system trust store. Maps to `certificate`. Example: "-----BEGIN CERTIFICATE-----\n..."
	// +kubebuilder:validation:MaxLength=65536
	// +optional
	Certificate string `json:"certificate,omitempty"`

	// PEM-encoded client certificate presented to the LDAP server for mutual TLS. Must be set together with clientTLSKeySecretRef. OpenBao does not return it, so it is compared only through status.writeOnlyHash. Maps to `client_tls_cert`. Example: "-----BEGIN CERTIFICATE-----\n..."
	// +kubebuilder:validation:MaxLength=65536
	// +optional
	ClientTLSCert string `json:"clientTLSCert,omitempty"`

	// Secret key holding the PEM private key of clientTLSCert. Read from this Secret key; the value is never stored in status. OpenBao never returns it, so it is compared only through status.writeOnlyHash. Must be set together with clientTLSCert. Maps to `client_tls_key`. Example: {name: ldap-client-tls, key: tls.key}
	// +optional
	ClientTLSKeySecretRef *commonv1alpha1.SecretKeySelector `json:"clientTLSKeySecretRef,omitempty"`

	// When true, a STARTTLS command is issued after connecting to an ldap:// URL. Defaults to false. Maps to `starttls`. Example: true
	// +optional
	StartTLS *bool `json:"startTLS,omitempty"`

	// When true, the LDAP server certificate is not verified. Insecure; do not use in production. Defaults to false. Maps to `insecure_tls`. Example: false
	// +optional
	InsecureTLS *bool `json:"insecureTLS,omitempty"`

	// Minimum TLS version: tls10, tls11, tls12 or tls13. OpenBao defaults both versions to tls12, so tls13 also needs tlsMaxVersion; must not exceed tlsMaxVersion. Maps to `tls_min_version`. Example: tls12
	// +kubebuilder:validation:Enum=tls10;tls11;tls12;tls13
	// +kubebuilder:validation:MaxLength=5
	// +optional
	TLSMinVersion string `json:"tlsMinVersion,omitempty"`

	// Maximum TLS version: tls10, tls11, tls12 or tls13. OpenBao defaults to tls12. Maps to `tls_max_version`. Example: tls13
	// +kubebuilder:validation:Enum=tls10;tls11;tls12;tls13
	// +kubebuilder:validation:MaxLength=5
	// +optional
	TLSMaxVersion string `json:"tlsMaxVersion,omitempty"`

	// Timeout of a request to the LDAP server, as a duration. OpenBao defaults to 90s. Maps to `request_timeout`. Example: 30s
	// +optional
	RequestTimeout *metav1.Duration `json:"requestTimeout,omitempty"`

	// Timeout of a connection attempt before the next URL is tried, as a duration. OpenBao defaults to 30s. Maps to `connection_timeout`. Example: 10s
	// +optional
	ConnectionTimeout *metav1.Duration `json:"connectionTimeout,omitempty"`

	// When LDAP aliases are dereferenced on searches: never, finding, searching or always. OpenBao defaults to never. Maps to `dereference_aliases`. Example: always
	// +kubebuilder:validation:Enum=never;finding;searching;always
	// +optional
	DereferenceAliases string `json:"dereferenceAliases,omitempty"`

	// Page size of paged LDAP searches, to stay under server size limits; 0 or unset disables paging. Maps to `max_page_size`. Example: 1000
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxPageSize *int32 `json:"maxPageSize,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="has(self.mountPath) == has(oldSelf.mountPath) && (!has(self.mountPath) || self.mountPath == oldSelf.mountPath)",message="mountPath is immutable"
// +kubebuilder:validation:XValidation:rule="!has(self.deletionPolicy) || self.deletionPolicy == 'Orphan'",message="deletionPolicy must be Orphan: the config endpoint cannot be deleted"

// Desired state of an LDAP auth method configuration, written to auth/<mountPath>/config. Token fields set the defaults of tokens issued at login. deletionPolicy must be Orphan.
type LDAPAuthConfigSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path of the LDAP auth method, without the auth/ prefix. Defaults to ldap. Immutable. Example: ldap/corp
	// +kubebuilder:default=ldap
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	LDAPConnectionFields `json:",inline"`

	TokenFields `json:",inline"`
}

// Observed state of the auth method configuration, written by the operator.
type LDAPAuthConfigStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// LDAPAuthConfig manages the configuration of an OpenBao LDAP auth method at auth/<mountPath>/config. Use it to connect the mount to an LDAP or Active Directory server and set how users and groups are found; one per mount.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: ldap
//	  url: ldaps://ldap.example.com
//	  bindDN: cn=openbao,ou=Services,dc=example,dc=org
//	  bindPassSecretRef: {name: ldap-bind, key: password}
//	  userDN: ou=People,dc=example,dc=org
//	  groupDN: ou=Groups,dc=example,dc=org
//	  deletionPolicy: Orphan
type LDAPAuthConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec LDAPAuthConfigSpec `json:"spec"`

	// +optional
	Status LDAPAuthConfigStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *LDAPAuthConfig) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &o.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *LDAPAuthConfig) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// LDAPAuthConfigList contains a list of LDAPAuthConfig.
type LDAPAuthConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []LDAPAuthConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LDAPAuthConfig{}, &LDAPAuthConfigList{})
}
