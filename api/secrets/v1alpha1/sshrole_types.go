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

// The credential type of an SSH role: ca or otp.
// +kubebuilder:validation:Enum=ca;otp
type SSHRoleKeyType string

const (
	SSHRoleKeyTypeCA  SSHRoleKeyType = "ca"
	SSHRoleKeyTypeOTP SSHRoleKeyType = "otp"
)

// +kubebuilder:validation:XValidation:rule="has(self.mountPath) == has(oldSelf.mountPath) && (!has(self.mountPath) || self.mountPath == oldSelf.mountPath)",message="mountPath is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.name) == has(oldSelf.name) && (!has(self.name) || self.name == oldSelf.name)",message="name is immutable"
// +kubebuilder:validation:XValidation:rule="self.keyType != 'ca' || (has(self.allowUserCertificates) && self.allowUserCertificates) || (has(self.allowHostCertificates) && self.allowHostCertificates)",message="a ca role needs allowUserCertificates or allowHostCertificates"
// +kubebuilder:validation:XValidation:rule="self.keyType != 'otp' || has(self.defaultUser)",message="an otp role needs defaultUser"
// +kubebuilder:validation:XValidation:rule="self.keyType == 'otp' || (!has(self.cidrList) && !has(self.excludeCIDRList) && !has(self.port))",message="cidrList, excludeCIDRList and port apply only to otp roles"
// +kubebuilder:validation:XValidation:rule="self.keyType == 'ca' || (!has(self.defaultUserTemplate) && !has(self.allowedUsersTemplate) && !has(self.allowedDomains) && !has(self.allowedDomainsTemplate) && !has(self.ttl) && !has(self.maxTTL) && !has(self.allowedCriticalOptions) && !has(self.allowedExtensions) && !has(self.defaultCriticalOptions) && !has(self.defaultExtensions) && !has(self.defaultExtensionsTemplate) && !has(self.allowUserCertificates) && !has(self.allowHostCertificates) && !has(self.allowBareDomains) && !has(self.allowSubdomains) && !has(self.allowUserKeyIDs) && !has(self.allowEmptyPrincipals) && !has(self.allowCommasInIdentityTemplates) && !has(self.allowWildcardsInIdentityTemplates) && !has(self.keyIDFormat) && !has(self.allowedUserKeyLengths) && !has(self.algorithmSigner) && !has(self.notBeforeDuration) && !has(self.issuerRef))",message="certificate fields apply only to ca roles"
// +kubebuilder:validation:XValidation:rule="!has(self.ttl) || !has(self.maxTTL) || duration(self.maxTTL) == duration('0s') || duration(self.ttl) <= duration(self.maxTTL)",message="ttl must not exceed maxTTL"

// spec is the desired role of an SSH secrets engine at `<mountPath>/roles/<name>`. A ca role signs SSH certificates; an otp role issues one-time passwords for hosts that run the OpenBao SSH helper. Certificate fields apply only to ca roles; cidrList, excludeCIDRList and port only to otp roles.
type SSHRoleSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path of the SSH secrets engine mount. Defaults to `ssh`. Immutable. Example: `ssh-client-signer`.
	// +kubebuilder:default=ssh
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// name is the role name in OpenBao. Defaults to metadata.name. Immutable. Example: `ops`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.@-]+$`
	// +optional
	Name string `json:"name,omitempty"`

	// keyType is `ca` for certificate signing or `otp` for one-time passwords (`key_type`). Required.
	// +required
	KeyType SSHRoleKeyType `json:"keyType"`

	// defaultUser is the user a credential is issued for when the request names none (`default_user`). Required for otp roles. Example: `ubuntu`.
	// +kubebuilder:validation:MaxLength=256
	// +optional
	DefaultUser string `json:"defaultUser,omitempty"`

	// defaultUserTemplate allows identity templates in defaultUser (`default_user_template`). Defaults to false.
	// +optional
	DefaultUserTemplate *bool `json:"defaultUserTemplate,omitempty"`

	// allowedUsers are the users a credential may be issued for (`allowed_users`); `*` allows any. For ca roles an empty list allows none. Up to 256. Example: `[ubuntu, ops]`.
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=256
	// +kubebuilder:validation:items:Pattern=`^[^,]+$`
	// +listType=atomic
	// +optional
	AllowedUsers []string `json:"allowedUsers,omitempty"`

	// allowedUsersTemplate allows identity templates in allowedUsers (`allowed_users_template`). Defaults to false.
	// +optional
	AllowedUsersTemplate *bool `json:"allowedUsersTemplate,omitempty"`

	// allowedDomains are the domains host certificates may be signed for (`allowed_domains`). Up to 256. Example: `[example.com]`.
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=256
	// +kubebuilder:validation:items:Pattern=`^[^,]+$`
	// +listType=atomic
	// +optional
	AllowedDomains []string `json:"allowedDomains,omitempty"`

	// allowedDomainsTemplate allows identity templates in allowedDomains (`allowed_domains_template`). Defaults to false.
	// +optional
	AllowedDomainsTemplate *bool `json:"allowedDomainsTemplate,omitempty"`

	// cidrList are the CIDR blocks of the hosts an otp role applies to (`cidr_list`). Up to 256. Example: `[10.0.0.0/16]`.
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:items:MaxLength=64
	// +kubebuilder:validation:XValidation:rule="self.all(c, isCIDR(c))",message="cidrList entries must be CIDR blocks"
	// +listType=atomic
	// +optional
	CIDRList []string `json:"cidrList,omitempty"`

	// excludeCIDRList are CIDR blocks excluded from cidrList (`exclude_cidr_list`). Up to 256. Example: `[10.0.5.0/24]`.
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:items:MaxLength=64
	// +kubebuilder:validation:XValidation:rule="self.all(c, isCIDR(c))",message="excludeCIDRList entries must be CIDR blocks"
	// +listType=atomic
	// +optional
	ExcludeCIDRList []string `json:"excludeCIDRList,omitempty"`

	// port is the SSH port returned to otp clients (`port`). 1-65535. Defaults to 22.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +optional
	Port *int32 `json:"port,omitempty"`

	// ttl is the default lifetime of signed certificates (`ttl`), a duration. Must not exceed maxTTL. Defaults to maxTTL. Example: `30m`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	TTL *metav1.Duration `json:"ttl,omitempty"`

	// maxTTL is the maximum lifetime of signed certificates (`max_ttl`), a duration. Defaults to the mount's max lease TTL. Example: `24h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	MaxTTL *metav1.Duration `json:"maxTTL,omitempty"`

	// allowedCriticalOptions are the critical options a certificate request may set (`allowed_critical_options`); empty allows any. Up to 64. Example: `[force-command]`.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=128
	// +kubebuilder:validation:items:Pattern=`^[^,]+$`
	// +listType=atomic
	// +optional
	AllowedCriticalOptions []string `json:"allowedCriticalOptions,omitempty"`

	// allowedExtensions are the extensions a certificate request may set (`allowed_extensions`); `*` allows any, empty allows none. Up to 64. Example: `[permit-pty, permit-port-forwarding]`.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=128
	// +kubebuilder:validation:items:Pattern=`^[^,]+$`
	// +listType=atomic
	// +optional
	AllowedExtensions []string `json:"allowedExtensions,omitempty"`

	// defaultCriticalOptions are added to certificates whose request sets none (`default_critical_options`). Up to 64. Example: `{force-command: /usr/bin/uptime}`.
	// +kubebuilder:validation:MaxProperties=64
	// +optional
	DefaultCriticalOptions map[string]string `json:"defaultCriticalOptions,omitempty"`

	// defaultExtensions are added to certificates whose request sets none (`default_extensions`). Up to 64. Example: `{permit-pty: ""}`.
	// +kubebuilder:validation:MaxProperties=64
	// +optional
	DefaultExtensions map[string]string `json:"defaultExtensions,omitempty"`

	// defaultExtensionsTemplate allows identity templates in defaultExtensions values (`default_extensions_template`). Defaults to false.
	// +optional
	DefaultExtensionsTemplate *bool `json:"defaultExtensionsTemplate,omitempty"`

	// allowUserCertificates allows signing user certificates (`allow_user_certificates`). A ca role needs this or allowHostCertificates. Defaults to false.
	// +optional
	AllowUserCertificates *bool `json:"allowUserCertificates,omitempty"`

	// allowHostCertificates allows signing host certificates (`allow_host_certificates`). Defaults to false.
	// +optional
	AllowHostCertificates *bool `json:"allowHostCertificates,omitempty"`

	// allowBareDomains allows host certificates for the domains in allowedDomains themselves (`allow_bare_domains`). Defaults to false.
	// +optional
	AllowBareDomains *bool `json:"allowBareDomains,omitempty"`

	// allowSubdomains allows host certificates for subdomains of allowedDomains (`allow_subdomains`). Defaults to false.
	// +optional
	AllowSubdomains *bool `json:"allowSubdomains,omitempty"`

	// allowUserKeyIDs lets the request choose the key ID of the certificate (`allow_user_key_ids`). Defaults to false.
	// +optional
	AllowUserKeyIDs *bool `json:"allowUserKeyIDs,omitempty"`

	// allowEmptyPrincipals allows certificates without valid principals (`allow_empty_principals`); leave it disabled. Defaults to false.
	// +optional
	AllowEmptyPrincipals *bool `json:"allowEmptyPrincipals,omitempty"`

	// allowCommasInIdentityTemplates allows commas in values substituted into identity templates. Defaults to false.
	// +optional
	AllowCommasInIdentityTemplates *bool `json:"allowCommasInIdentityTemplates,omitempty"`

	// allowWildcardsInIdentityTemplates allows wildcards in values substituted into identity templates. Defaults to false.
	// +optional
	AllowWildcardsInIdentityTemplates *bool `json:"allowWildcardsInIdentityTemplates,omitempty"`

	// keyIDFormat is the key ID format (`key_id_format`), with `{{token_display_name}}`, `{{role_name}}` and `{{public_key_hash}}`. Example: `vault-{{role_name}}-{{token_display_name}}`.
	// +kubebuilder:validation:MaxLength=512
	// +optional
	KeyIDFormat string `json:"keyIDFormat,omitempty"`

	// allowedUserKeyLengths maps a key type (rsa, ec, ed25519 or an OpenSSH type) to the allowed key lengths (`allowed_user_key_lengths`). Up to 16 types. Example: `{rsa: [3072, 4096]}`.
	// +kubebuilder:validation:MaxProperties=16
	// +optional
	AllowedUserKeyLengths map[string][]int32 `json:"allowedUserKeyLengths,omitempty"`

	// algorithmSigner is the signature algorithm for RSA CA keys (`algorithm_signer`). Allowed: default, ssh-rsa, rsa-sha2-256, rsa-sha2-512. Defaults to default.
	// +kubebuilder:validation:Enum=default;ssh-rsa;rsa-sha2-256;rsa-sha2-512
	// +optional
	AlgorithmSigner string `json:"algorithmSigner,omitempty"`

	// notBeforeDuration backdates certificates by this duration (`not_before_duration`). Defaults to `30s`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	NotBeforeDuration *metav1.Duration `json:"notBeforeDuration,omitempty"`

	// issuerRef is the name or ID of the issuer that signs for this role (`issuer_ref`). Defaults to `default`. Example: `default`.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	IssuerRef string `json:"issuerRef,omitempty"`
}

// status is the observed state of the SSH role.
type SSHRoleStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-secrets
// +kubebuilder:printcolumn:name="Key Type",type=string,JSONPath=`.spec.keyType`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// SSHRole manages a role of an SSH secrets engine at `<mount>/roles/<name>`: a ca role signs SSH user or host certificates with the SSHCA, an otp role issues one-time SSH passwords. Use one role per access pattern.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: ssh-client-signer
//	  name: ops
//	  keyType: ca
//	  allowUserCertificates: true
//	  allowedUsers: [ubuntu]
//	  defaultExtensions: {permit-pty: ""}
type SSHRole struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec SSHRoleSpec `json:"spec"`

	// +optional
	Status SSHRoleStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (r *SSHRole) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &r.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (r *SSHRole) GetResourceStatus() *commonv1alpha1.ResourceStatus { return &r.Status.ResourceStatus }

// +kubebuilder:object:root=true

// SSHRoleList contains a list of SSHRole.
type SSHRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []SSHRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SSHRole{}, &SSHRoleList{})
}
