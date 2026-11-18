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

// +kubebuilder:validation:XValidation:rule="has(self.mountPath) == has(oldSelf.mountPath) && (!has(self.mountPath) || self.mountPath == oldSelf.mountPath)",message="mountPath is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.name) == has(oldSelf.name) && (!has(self.name) || self.name == oldSelf.name)",message="name is immutable"
// +kubebuilder:validation:XValidation:rule="!has(self.ttl) || !has(self.maxTTL) || duration(self.maxTTL) == duration('0s') || duration(self.ttl) <= duration(self.maxTTL)",message="ttl must not exceed maxTTL"
// +kubebuilder:validation:XValidation:rule="!has(self.keyBits) || (self.?keyType.orValue('rsa') == 'rsa' ? self.keyBits in [2048, 3072, 4096, 8192] : self.?keyType.orValue('rsa') == 'ec' ? self.keyBits in [224, 256, 384, 521] : self.?keyType.orValue('rsa') == 'mldsa' && self.keyBits in [44, 65, 87])",message="keyBits must be 2048, 3072, 4096 or 8192 for keyType rsa (the default), 224, 256, 384 or 521 for ec, 44, 65 or 87 for mldsa, and is not allowed for ed25519 or any"
// +kubebuilder:validation:XValidation:rule="!(self.?generateLease.orValue(false) && self.?noStore.orValue(false))",message="generateLease requires the certificate to be stored; unset noStore"

// spec is the desired role of a PKI secrets engine at `<mountPath>/roles/<name>`: the policy for certificates that clients issue or sign through it (cert-manager, ACME or the issue and sign endpoints). Fields left unset take the OpenBao default.
type PKIRoleSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path of the PKI secrets engine mount. Defaults to `pki`. Immutable. Example: `pki-int`.
	// +kubebuilder:default=pki
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// name is the role name in OpenBao. Defaults to metadata.name. Immutable. Example: `web-server`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_]([A-Za-z0-9_.-]*[A-Za-z0-9_])?$`
	// +optional
	Name string `json:"name,omitempty"`

	// issuer selects the issuer that signs certificates of this role (`issuer_ref`). Defaults to the mount's default issuer. Example: `{issuerRef: {name: int-2026}}`.
	// +optional
	Issuer *PKIIssuerReference `json:"issuer,omitempty"`

	// ttl is the default validity period of issued certificates (`ttl`), a duration. Must not exceed maxTTL. Defaults to the mount's default lease TTL. Example: `720h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	TTL *metav1.Duration `json:"ttl,omitempty"`

	// maxTTL is the longest validity period a request may ask for (`max_ttl`), a duration. Defaults to the mount's max lease TTL. Example: `2160h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	MaxTTL *metav1.Duration `json:"maxTTL,omitempty"`

	// allowLocalhost allows localhost and localdomain as names (`allow_localhost`). Defaults to true.
	// +optional
	AllowLocalhost *bool `json:"allowLocalhost,omitempty"`

	// allowedDomains are the domains certificates may be issued for (`allowed_domains`), interpreted by the allow flags below. Example: `[example.com]`.
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:items:MaxLength=256
	// +kubebuilder:validation:items:Pattern=`^[^,]+$`
	// +listType=set
	// +optional
	AllowedDomains []string `json:"allowedDomains,omitempty"`

	// allowedDomainsTemplate allows identity templates in allowedDomains (`allowed_domains_template`). Defaults to false.
	// +optional
	AllowedDomainsTemplate *bool `json:"allowedDomainsTemplate,omitempty"`

	// allowGlobsInIdentityTemplates evaluates glob patterns inside rendered identity templates (`allow_globs_in_identity_templates`). Defaults to false.
	// +optional
	AllowGlobsInIdentityTemplates *bool `json:"allowGlobsInIdentityTemplates,omitempty"`

	// allowBareDomains allows certificates for the allowed domains themselves (`allow_bare_domains`). Defaults to false.
	// +optional
	AllowBareDomains *bool `json:"allowBareDomains,omitempty"`

	// allowSubdomains allows certificates for subdomains of the allowed domains (`allow_subdomains`). Defaults to false.
	// +optional
	AllowSubdomains *bool `json:"allowSubdomains,omitempty"`

	// allowGlobDomains treats allowedDomains entries as glob patterns (`allow_glob_domains`). Defaults to false.
	// +optional
	AllowGlobDomains *bool `json:"allowGlobDomains,omitempty"`

	// allowWildcardCertificates allows wildcard names such as `*.example.com` (`allow_wildcard_certificates`). Defaults to true.
	// +optional
	AllowWildcardCertificates *bool `json:"allowWildcardCertificates,omitempty"`

	// allowAnyName allows any common name and DNS SAN (`allow_any_name`). Defaults to false.
	// +optional
	AllowAnyName *bool `json:"allowAnyName,omitempty"`

	// enforceHostnames requires names to be valid hostnames (`enforce_hostnames`). Defaults to true.
	// +optional
	EnforceHostnames *bool `json:"enforceHostnames,omitempty"`

	// allowIPSANs allows IP subject alternative names (`allow_ip_sans`). Defaults to true.
	// +optional
	AllowIPSANs *bool `json:"allowIPSANs,omitempty"`

	// allowedIPSANsCIDR limits IP subject alternative names to these networks (`allowed_ip_sans_cidr`), in canonical form (10.0.0.0/8, not 10.1.2.3/8). Example: `[10.0.0.0/8]`.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=64
	// +kubebuilder:validation:XValidation:rule="self.all(c, isCIDR(c) && cidr(c) == cidr(c).masked())",message="entries must be canonical CIDRs"
	// +listType=set
	// +optional
	AllowedIPSANsCIDR []string `json:"allowedIPSANsCIDR,omitempty"`

	// allowedURISANs are the URI subject alternative names allowed (`allowed_uri_sans`); globs are supported. Example: `["spiffe://example.com/*"]`.
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:items:MaxLength=512
	// +kubebuilder:validation:items:Pattern=`^[^,]+$`
	// +listType=set
	// +optional
	AllowedURISANs []string `json:"allowedURISANs,omitempty"`

	// allowedURISANsTemplate allows identity templates in allowedURISANs (`allowed_uri_sans_template`). Defaults to false.
	// +optional
	AllowedURISANsTemplate *bool `json:"allowedURISANsTemplate,omitempty"`

	// allowedOtherSANs are the custom subject alternative names allowed, in the form `<oid>;<type>:<value>` (`allowed_other_sans`); `*` allows any. Example: `["1.3.6.1.4.1.311.20.2.3;UTF8:*"]`.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=512
	// +kubebuilder:validation:items:Pattern=`^[^,]+$`
	// +listType=set
	// +optional
	AllowedOtherSANs []string `json:"allowedOtherSANs,omitempty"`

	// allowedSerialNumbers are the subject serialNumber values allowed (`allowed_serial_numbers`); globs are supported. Example: `["DEV-*"]`.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=256
	// +kubebuilder:validation:items:Pattern=`^[^,]+$`
	// +listType=set
	// +optional
	AllowedSerialNumbers []string `json:"allowedSerialNumbers,omitempty"`

	// allowedUserIDs are the subject UID values allowed (`allowed_user_ids`); globs are supported. Example: `[alice]`.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=256
	// +kubebuilder:validation:items:Pattern=`^[^,]+$`
	// +listType=set
	// +optional
	AllowedUserIDs []string `json:"allowedUserIDs,omitempty"`

	// serverFlag adds the server authentication extended key usage (`server_flag`). Defaults to true.
	// +optional
	ServerFlag *bool `json:"serverFlag,omitempty"`

	// clientFlag adds the client authentication extended key usage (`client_flag`). Defaults to true.
	// +optional
	ClientFlag *bool `json:"clientFlag,omitempty"`

	// codeSigningFlag adds the code signing extended key usage (`code_signing_flag`). Defaults to false.
	// +optional
	CodeSigningFlag *bool `json:"codeSigningFlag,omitempty"`

	// emailProtectionFlag adds the email protection extended key usage (`email_protection_flag`). Defaults to false.
	// +optional
	EmailProtectionFlag *bool `json:"emailProtectionFlag,omitempty"`

	// keyType is the algorithm required of the certificate key (`key_type`); `any` accepts every type. Allowed: rsa, ec, ed25519, mldsa, any. Defaults to rsa.
	// +kubebuilder:validation:Enum=rsa;ec;ed25519;mldsa;any
	// +optional
	KeyType string `json:"keyType,omitempty"`

	// keyBits is the key size required of the certificate key (`key_bits`): 2048, 3072, 4096 or 8192 for rsa; 224, 256, 384 or 521 for ec; 44, 65 or 87 for mldsa. Not for ed25519 or any. Defaults to 2048 for rsa, 256 for ec and 44 for mldsa.
	// +kubebuilder:validation:Enum=44;65;87;224;256;384;521;2048;3072;4096;8192
	// +optional
	KeyBits *int32 `json:"keyBits,omitempty"`

	// signatureBits selects the signature hash (`signature_bits`). Allowed: 256, 384, 512. Unset lets OpenBao pick one from the issuer key.
	// +kubebuilder:validation:Enum=256;384;512
	// +optional
	SignatureBits *int32 `json:"signatureBits,omitempty"`

	// usePSS signs with RSASSA-PSS when the issuer key is rsa (`use_pss`). Defaults to false.
	// +optional
	UsePSS *bool `json:"usePSS,omitempty"`

	// keyUsage lists the key usages of issued certificates (`key_usage`). Defaults to DigitalSignature, KeyAgreement and KeyEncipherment. Example: `[DigitalSignature, KeyEncipherment]`.
	// +kubebuilder:validation:MaxItems=9
	// +kubebuilder:validation:items:Enum=DigitalSignature;ContentCommitment;KeyEncipherment;DataEncipherment;KeyAgreement;CertSign;CRLSign;EncipherOnly;DecipherOnly
	// +listType=set
	// +optional
	KeyUsage []string `json:"keyUsage,omitempty"`

	// extKeyUsage lists additional extended key usages (`ext_key_usage`). Example: `[TimeStamping]`.
	// +kubebuilder:validation:MaxItems=14
	// +kubebuilder:validation:items:Enum=ServerAuth;ClientAuth;CodeSigning;EmailProtection;IPSECEndSystem;IPSECTunnel;IPSECUser;TimeStamping;OCSPSigning;MicrosoftServerGatedCrypto;NetscapeServerGatedCrypto;MicrosoftCommercialCodeSigning;MicrosoftKernelCodeSigning;Any
	// +listType=set
	// +optional
	ExtKeyUsage []string `json:"extKeyUsage,omitempty"`

	// extKeyUsageOIDs lists additional extended key usages as OIDs (`ext_key_usage_oids`). Example: `["1.3.6.1.5.5.7.3.8"]`.
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:items:Pattern=`^[0-9]+(\.[0-9]+)+$`
	// +kubebuilder:validation:items:MaxLength=128
	// +listType=set
	// +optional
	ExtKeyUsageOIDs []string `json:"extKeyUsageOIDs,omitempty"`

	// useCSRCommonName takes the common name from the CSR when signing (`use_csr_common_name`). Defaults to true.
	// +optional
	UseCSRCommonName *bool `json:"useCSRCommonName,omitempty"`

	// useCSRSANs takes the subject alternative names from the CSR when signing (`use_csr_sans`). Defaults to true.
	// +optional
	UseCSRSANs *bool `json:"useCSRSANs,omitempty"`

	// ou is the organizational unit of issued certificates (`ou`). Example: `[Platform]`.
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=256
	// +kubebuilder:validation:items:Pattern=`^[^,]+$`
	// +listType=set
	// +optional
	OU []string `json:"ou,omitempty"`

	// organization is the organization of issued certificates (`organization`). Example: `[Example Corp]`.
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=256
	// +kubebuilder:validation:items:Pattern=`^[^,]+$`
	// +listType=set
	// +optional
	Organization []string `json:"organization,omitempty"`

	// country is the country of issued certificates (`country`). Example: `[DE]`.
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=256
	// +kubebuilder:validation:items:Pattern=`^[^,]+$`
	// +listType=set
	// +optional
	Country []string `json:"country,omitempty"`

	// locality is the locality (city) of issued certificates (`locality`). Example: `[Berlin]`.
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=256
	// +kubebuilder:validation:items:Pattern=`^[^,]+$`
	// +listType=set
	// +optional
	Locality []string `json:"locality,omitempty"`

	// province is the province (state) of issued certificates (`province`). Example: `[Berlin]`.
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=256
	// +kubebuilder:validation:items:Pattern=`^[^,]+$`
	// +listType=set
	// +optional
	Province []string `json:"province,omitempty"`

	// streetAddress is the street address of issued certificates (`street_address`). Example: `[Main Street 1]`.
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=256
	// +kubebuilder:validation:items:Pattern=`^[^,]+$`
	// +listType=set
	// +optional
	StreetAddress []string `json:"streetAddress,omitempty"`

	// postalCode is the postal code of issued certificates (`postal_code`). Example: `["10115"]`.
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=256
	// +kubebuilder:validation:items:Pattern=`^[^,]+$`
	// +listType=set
	// +optional
	PostalCode []string `json:"postalCode,omitempty"`

	// generateLease attaches a lease to issued certificates so they are revoked when the lease ends (`generate_lease`); requires noStore unset. Defaults to false.
	// +optional
	GenerateLease *bool `json:"generateLease,omitempty"`

	// noStore skips storing issued certificates (`no_store`), which then cannot be listed or revoked by serial. Defaults to false.
	// +optional
	NoStore *bool `json:"noStore,omitempty"`

	// requireCN rejects requests without a common name (`require_cn`). Defaults to true.
	// +optional
	RequireCN *bool `json:"requireCN,omitempty"`

	// cnValidations lists the checks applied to the common name (`cn_validations`): email, hostname, or disabled alone. Defaults to email and hostname. Example: `[hostname]`.
	// +kubebuilder:validation:MaxItems=2
	// +kubebuilder:validation:items:Enum=email;hostname;disabled
	// +listType=set
	// +optional
	CNValidations []string `json:"cnValidations,omitempty"`

	// policyIdentifiers are certificate policy OIDs added to issued certificates (`policy_identifiers`). Example: `["1.3.6.1.4.1.44947.1.1.1"]`.
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:items:Pattern=`^[0-9]+(\.[0-9]+)+$`
	// +kubebuilder:validation:items:MaxLength=128
	// +listType=set
	// +optional
	PolicyIdentifiers []string `json:"policyIdentifiers,omitempty"`

	// basicConstraintsValidForNonCA marks the basic constraints extension valid on non-CA certificates (`basic_constraints_valid_for_non_ca`). Defaults to false.
	// +optional
	BasicConstraintsValidForNonCA *bool `json:"basicConstraintsValidForNonCA,omitempty"`

	// notBeforeDuration backdates issued certificates (`not_before_duration`), a duration. Defaults to `30s`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	NotBeforeDuration *metav1.Duration `json:"notBeforeDuration,omitempty"`

	// notBefore sets a fixed not-before time on issued certificates, as a UTC timestamp (`not_before`). Example: `2026-01-01T00:00:00Z`.
	// +kubebuilder:validation:Pattern=`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$`
	// +optional
	NotBefore string `json:"notBefore,omitempty"`

	// notBeforeBound controls whether requests may set not_before (`not_before_bound`): `permit`, `duration` (only through not_before_duration) or `forbid`. Defaults to permit.
	// +kubebuilder:validation:Enum=permit;duration;forbid
	// +optional
	NotBeforeBound string `json:"notBeforeBound,omitempty"`

	// notAfter sets a fixed expiry on issued certificates, as a UTC timestamp (`not_after`). Example: `2027-01-01T00:00:00Z`.
	// +kubebuilder:validation:Pattern=`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$`
	// +optional
	NotAfter string `json:"notAfter,omitempty"`

	// notAfterBound controls whether requests may set not_after (`not_after_bound`): `permit`, `ttl-limited`, `forbid`, or a UTC timestamp no certificate may exceed. Defaults to permit. Example: `ttl-limited`.
	// +kubebuilder:validation:Pattern=`^(permit|ttl-limited|forbid|[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z)$`
	// +optional
	NotAfterBound string `json:"notAfterBound,omitempty"`
}

// status is the observed state of the PKI role.
type PKIRoleStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=baopkir,categories=obco;obco-secrets
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// PKIRole manages a certificate role of a PKI secrets engine at `<mount>/roles/<name>`: which names, key types, lifetimes and usages certificates issued or signed through it (`<mount>/issue/<name>`, `<mount>/sign/<name>`) may have.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: pki-int
//	  name: web-server
//	  allowedDomains: [example.com]
//	  allowSubdomains: true
//	  maxTTL: 2160h
type PKIRole struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec PKIRoleSpec `json:"spec"`

	// +optional
	Status PKIRoleStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (r *PKIRole) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &r.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (r *PKIRole) GetResourceStatus() *commonv1alpha1.ResourceStatus { return &r.Status.ResourceStatus }

// +kubebuilder:object:root=true

// PKIRoleList contains a list of PKIRole.
type PKIRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []PKIRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PKIRole{}, &PKIRoleList{})
}
