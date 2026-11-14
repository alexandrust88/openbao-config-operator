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

// How the issuer certificate is produced: root or intermediate.
// +kubebuilder:validation:Enum=root;intermediate
type PKIIssuerType string

const (
	PKIIssuerTypeRoot         PKIIssuerType = "root"
	PKIIssuerTypeIntermediate PKIIssuerType = "intermediate"
)

// Where the private key of a new issuer comes from.
// +kubebuilder:validation:Enum=internal;exported;existing;kms
type PKIKeySource string

const (
	PKIKeySourceInternal PKIKeySource = "internal"
	PKIKeySourceExported PKIKeySource = "exported"
	PKIKeySourceExisting PKIKeySource = "existing"
	PKIKeySourceKMS      PKIKeySource = "kms"
)

// The algorithm of a PKI private key.
// +kubebuilder:validation:Enum=rsa;ec;ed25519;mldsa
type PKIKeyType string

// Selects an issuer by its OpenBao name or by a PKIIssuer object. Set exactly one field.
// +kubebuilder:validation:XValidation:rule="has(self.issuerName) != has(self.issuerRef)",message="set exactly one of issuerName and issuerRef"
type PKIIssuerReference struct {
	// issuerName is the name or ID of the issuer in OpenBao. Example: `int-2026`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +optional
	IssuerName string `json:"issuerName,omitempty"`

	// issuerRef names a PKIIssuer in the same namespace and PKI mount; it must be Ready. Example: `{name: int-2026}`.
	// +optional
	IssuerRef *commonv1alpha1.LocalObjectReference `json:"issuerRef,omitempty"`
}

// Selects a key by its OpenBao name or by a PKIKey object. Set exactly one field.
// +kubebuilder:validation:XValidation:rule="has(self.keyName) != has(self.keyRef)",message="set exactly one of keyName and keyRef"
type PKIKeyReference struct {
	// keyName is the name or ID of the key in OpenBao. Example: `int-2026`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +optional
	KeyName string `json:"keyName,omitempty"`

	// keyRef names a PKIKey in the same namespace and PKI mount; it must be Ready. Example: `{name: int-2026}`.
	// +optional
	KeyRef *commonv1alpha1.LocalObjectReference `json:"keyRef,omitempty"`
}

// The private key of a new issuer.
// +kubebuilder:validation:XValidation:rule="self.?source.orValue('internal') == 'existing' ? (has(self.existing) && !has(self.name) && !has(self.type) && !has(self.bits)) : !has(self.existing)",message="existing is required with source existing and excludes name, type and bits"
// +kubebuilder:validation:XValidation:rule="(self.?source.orValue('internal') == 'kms') == has(self.externalKeyRef)",message="externalKeyRef is required with source kms and allowed only there"
// +kubebuilder:validation:XValidation:rule="self.?source.orValue('internal') != 'kms' || (!has(self.type) && !has(self.bits))",message="type and bits come from the external key with source kms"
// +kubebuilder:validation:XValidation:rule="!has(self.privateKeyFormat) || self.?source.orValue('internal') == 'exported'",message="privateKeyFormat applies only to source exported"
type PKIIssuerKey struct {
	// source is `internal` (the key never leaves OpenBao), `exported` (also written once to writeSecretRef), `existing` (a key already stored in the mount) or `kms` (an OpenBao external key). Defaults to internal.
	// +kubebuilder:default=internal
	// +optional
	Source PKIKeySource `json:"source,omitempty"`

	// name is given to a newly created key in OpenBao (`key_name`). Not allowed with source existing. Example: `int-2026`.
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_]([A-Za-z0-9_.-]*[A-Za-z0-9_])?$`
	// +optional
	Name string `json:"name,omitempty"`

	// type is the algorithm of a newly created key (`key_type`). Allowed: rsa, ec, ed25519, mldsa. Defaults to rsa. Example: `ec`.
	// +optional
	Type PKIKeyType `json:"type,omitempty"`

	// bits is the size of a newly created key (`key_bits`): 2048, 3072, 4096 or 8192 for rsa; 224, 256, 384 or 521 for ec; 44, 65 or 87 for mldsa; unused for ed25519. Defaults to 2048 for rsa and 256 for ec. Example: `384`.
	// +kubebuilder:validation:Enum=44;65;87;224;256;384;521;2048;3072;4096;8192
	// +optional
	Bits *int32 `json:"bits,omitempty"`

	// existing selects the stored key to reuse when source is existing (`key_ref`). Example: `{keyRef: {name: int-2026}}`.
	// +optional
	Existing *PKIKeyReference `json:"existing,omitempty"`

	// externalKeyRef is the OpenBao external key used when source is kms; required there and allowed only there. Example: `hsm-pki-key`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	// +optional
	ExternalKeyRef string `json:"externalKeyRef,omitempty"`

	// privateKeyFormat is the format of an exported key (`private_key_format`): `der` writes a PKCS#1 or SEC1 PEM key, `pkcs8` a PKCS#8 PEM key. Source exported only. Defaults to der.
	// +kubebuilder:validation:Enum=der;pkcs8
	// +optional
	PrivateKeyFormat string `json:"privateKeyFormat,omitempty"`
}

// Subject and constraints of the issuer certificate. They apply when the certificate is generated (root) or signed (intermediate) and cannot change afterwards; create a new PKIIssuer to rotate a CA. Set at most one of ttl and notAfter.
// +kubebuilder:validation:XValidation:rule="!has(self.ttl) || !has(self.notAfter)",message="set at most one of ttl and notAfter"
type PKIIssuerCertificate struct {
	// commonName is the common name of the issuer certificate (`common_name`). Example: `Example Intermediate CA 2026`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +required
	CommonName string `json:"commonName"`

	// altNames are DNS names and email addresses added as subject alternative names (`alt_names`). Example: `[ca.example.com]`.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=256
	// +kubebuilder:validation:items:Pattern=`^[^,]+$`
	// +listType=atomic
	// +optional
	AltNames []string `json:"altNames,omitempty"`

	// ipSANs are IP addresses added as subject alternative names (`ip_sans`). Example: `[10.0.0.10]`.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=64
	// +listType=atomic
	// +optional
	IPSANs []string `json:"ipSANs,omitempty"`

	// uriSANs are URIs added as subject alternative names (`uri_sans`). Example: `[spiffe://example.com/ca]`.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=atomic
	// +optional
	URISANs []string `json:"uriSANs,omitempty"`

	// otherSANs are custom subject alternative names in the form `<oid>;<type>:<value>` (`other_sans`). Example: `["1.3.6.1.4.1.311.20.2.3;UTF8:ca@example.com"]`.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=atomic
	// +optional
	OtherSANs []string `json:"otherSANs,omitempty"`

	// excludeCNFromSANs keeps the common name out of the subject alternative names (`exclude_cn_from_sans`). Defaults to false.
	// +optional
	ExcludeCNFromSANs *bool `json:"excludeCNFromSANs,omitempty"`

	// ou is the organizational unit of the subject (`ou`). Example: `[Platform]`.
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=256
	// +listType=atomic
	// +optional
	OU []string `json:"ou,omitempty"`

	// organization is the organization of the subject (`organization`). Example: `[Example Corp]`.
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=256
	// +listType=atomic
	// +optional
	Organization []string `json:"organization,omitempty"`

	// country is the country of the subject (`country`). Example: `[DE]`.
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=256
	// +listType=atomic
	// +optional
	Country []string `json:"country,omitempty"`

	// locality is the locality (city) of the subject (`locality`). Example: `[Berlin]`.
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=256
	// +listType=atomic
	// +optional
	Locality []string `json:"locality,omitempty"`

	// province is the province (state) of the subject (`province`). Example: `[Berlin]`.
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=256
	// +listType=atomic
	// +optional
	Province []string `json:"province,omitempty"`

	// streetAddress is the street address of the subject (`street_address`). Example: `[Main Street 1]`.
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=256
	// +listType=atomic
	// +optional
	StreetAddress []string `json:"streetAddress,omitempty"`

	// postalCode is the postal code of the subject (`postal_code`). Example: `["10115"]`.
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=256
	// +listType=atomic
	// +optional
	PostalCode []string `json:"postalCode,omitempty"`

	// serialNumber is the subject's serialNumber attribute, not the certificate serial number (`serial_number`). Example: `CA-0001`.
	// +kubebuilder:validation:MaxLength=256
	// +optional
	SerialNumber string `json:"serialNumber,omitempty"`

	// ttl is the validity period of the certificate (`ttl`), a duration bounded by the mount's max lease TTL. Example: `43800h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	TTL *metav1.Duration `json:"ttl,omitempty"`

	// notAfter sets the expiry of the certificate as a UTC timestamp (`not_after`). Example: `2036-01-01T00:00:00Z`.
	// +kubebuilder:validation:Pattern=`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$`
	// +optional
	NotAfter string `json:"notAfter,omitempty"`

	// notBeforeDuration backdates the certificate's not-before time (`not_before_duration`), a duration. Defaults to `30s`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	NotBeforeDuration *metav1.Duration `json:"notBeforeDuration,omitempty"`

	// maxPathLength limits the number of intermediate CAs below this one (`max_path_length`); -1 means no limit. Defaults to -1. Example: `0`.
	// +kubebuilder:validation:Minimum=-1
	// +optional
	MaxPathLength *int32 `json:"maxPathLength,omitempty"`

	// permittedDNSDomains restricts the DNS names of certificates issued below this CA (`permitted_dns_domains`). Example: `[example.com]`.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=256
	// +listType=atomic
	// +optional
	PermittedDNSDomains []string `json:"permittedDNSDomains,omitempty"`

	// signatureBits selects the hash of the self-signature (root) or the CSR signature (intermediate) (`signature_bits`). Allowed: 256, 384, 512. Unset lets OpenBao pick one from the key.
	// +kubebuilder:validation:Enum=256;384;512
	// +optional
	SignatureBits *int32 `json:"signatureBits,omitempty"`

	// usePSS signs with RSASSA-PSS instead of PKCS#1 v1.5 for rsa keys (`use_pss`). Defaults to false.
	// +optional
	UsePSS *bool `json:"usePSS,omitempty"`

	// addBasicConstraints adds a CA basic constraints extension to the CSR of an intermediate (`add_basic_constraints`), for compatibility with Active Directory Certificate Services. Intermediate only. Defaults to false.
	// +optional
	AddBasicConstraints *bool `json:"addBasicConstraints,omitempty"`
}

// Selects the issuer that signs an intermediate. Set exactly one of issuerName and issuerRef.
// +kubebuilder:validation:XValidation:rule="has(self.issuerName) != has(self.issuerRef)",message="set exactly one of issuerName and issuerRef"
// +kubebuilder:validation:XValidation:rule="!has(self.mountPath) || has(self.issuerName)",message="mountPath applies only to issuerName; issuerRef takes the mount from the PKIIssuer"
type PKIIssuerSigner struct {
	// mountPath is the PKI mount that holds the signing issuer named by issuerName. Defaults to the mount of this issuer; not allowed with issuerRef. Example: `pki-root`.
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// issuerName is the name or ID of the signing issuer in OpenBao. Example: `root-2026`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +optional
	IssuerName string `json:"issuerName,omitempty"`

	// issuerRef names a PKIIssuer in the same namespace that signs this intermediate; it must be Ready. Its mount is taken from it. Example: `{name: root-2026}`.
	// +optional
	IssuerRef *commonv1alpha1.LocalObjectReference `json:"issuerRef,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="has(self.mountPath) == has(oldSelf.mountPath) && (!has(self.mountPath) || self.mountPath == oldSelf.mountPath)",message="mountPath is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.name) == has(oldSelf.name) && (!has(self.name) || self.name == oldSelf.name)",message="name is immutable"
// +kubebuilder:validation:XValidation:rule="self.type == oldSelf.type",message="type is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.key) == has(oldSelf.key) && (!has(self.key) || self.key == oldSelf.key)",message="key is immutable"
// +kubebuilder:validation:XValidation:rule="self.certificate == oldSelf.certificate",message="certificate is immutable; create a new PKIIssuer to rotate the CA"
// +kubebuilder:validation:XValidation:rule="has(self.signer) == has(oldSelf.signer) && (!has(self.signer) || self.signer == oldSelf.signer)",message="signer is immutable"
// +kubebuilder:validation:XValidation:rule="(self.type == 'intermediate') == has(self.signer)",message="signer is required for an intermediate and not allowed for a root"
// +kubebuilder:validation:XValidation:rule="self.type == 'intermediate' || !has(self.certificate.addBasicConstraints)",message="certificate.addBasicConstraints applies only to an intermediate"
// +kubebuilder:validation:XValidation:rule="(has(self.key) && self.key.?source.orValue('') == 'exported') == has(self.writeSecretRef)",message="writeSecretRef is required with key source exported and allowed only there"
// +kubebuilder:validation:XValidation:rule="self.?name.orValue('') != 'default'",message="default is a reserved issuer name"
// +kubebuilder:validation:XValidation:rule="!has(self.revocationSignatureAlgorithm) || self.?key.?source.orValue('internal') in ['existing', 'kms'] || (self.?key.?type.orValue('rsa') == 'rsa' ? self.revocationSignatureAlgorithm.contains('RSA') : self.?key.?type.orValue('rsa') == 'ec' ? self.revocationSignatureAlgorithm.startsWith('ECDSA') : self.?key.?type.orValue('rsa') == 'ed25519' ? self.revocationSignatureAlgorithm == 'Ed25519' : self.revocationSignatureAlgorithm.startsWith('MLDSA'))",message="revocationSignatureAlgorithm must match key.type (rsa by default): an RSA algorithm for rsa, ECDSA for ec, Ed25519 for ed25519, MLDSA for mldsa"
// +kubebuilder:validation:XValidation:rule="!self.?enableAIAURLTemplating.orValue(false) || has(self.issuingCertificates) || has(self.crlDistributionPoints) || has(self.ocspServers)",message="enableAIAURLTemplating requires at least one of issuingCertificates, crlDistributionPoints and ocspServers; OpenBao drops it otherwise"

// spec is the desired CA issuer of a PKI secrets engine at `<mountPath>/issuer/<name>`. A root issuer is generated and self-signed. An intermediate issuer gets a CSR generated in its mount, signed by the signer issuer, and the signed certificate imported back. The certificate is created once; afterwards only the issuer settings (usage, chain, URLs and so on) are reconciled.
type PKIIssuerSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path of the PKI secrets engine mount. Defaults to `pki`. Immutable. Example: `pki-int`.
	// +kubebuilder:default=pki
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// name is the issuer name in OpenBao (`issuer_name`). Defaults to metadata.name; `default` is reserved. Immutable. Example: `int-2026`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_]([A-Za-z0-9_.-]*[A-Za-z0-9_])?$`
	// +optional
	Name string `json:"name,omitempty"`

	// type is `root` for a self-signed CA or `intermediate` for a CA signed by another issuer. Immutable.
	// +required
	Type PKIIssuerType `json:"type"`

	// key describes the issuer's private key. Defaults to a new internal rsa key. Immutable.
	// +optional
	Key *PKIIssuerKey `json:"key,omitempty"`

	// certificate holds the subject and constraints of the issuer certificate. Immutable: create a new PKIIssuer to rotate the CA.
	// +required
	Certificate PKIIssuerCertificate `json:"certificate"`

	// signer selects the issuer that signs an intermediate; it may be in another PKI mount. Required for an intermediate, not allowed for a root. Immutable.
	// +optional
	Signer *PKIIssuerSigner `json:"signer,omitempty"`

	// writeSecretRef names the Secret that receives the private key once, when key.source is exported; required there and allowed only there. The Secret is owned by this resource and holds the keys `private_key` and `private_key_type`; never stored in status. Example: `{name: int-ca-key}`.
	// +optional
	WriteSecretRef *commonv1alpha1.LocalObjectReference `json:"writeSecretRef,omitempty"`

	// leafNotAfterBehavior controls a leaf whose expiry would exceed the issuer's (`leaf_not_after_behavior`): `err` rejects it, `truncate` shortens it, `permit` allows it. Defaults to err.
	// +kubebuilder:validation:Enum=err;truncate;permit
	// +optional
	LeafNotAfterBehavior string `json:"leafNotAfterBehavior,omitempty"`

	// usage lists what the issuer may do (`usage`): read-only, issuing-certificates, crl-signing, ocsp-signing; read-only is always added. Defaults to every usage. Example: `[issuing-certificates, crl-signing]`.
	// +kubebuilder:validation:MaxItems=4
	// +kubebuilder:validation:items:Enum=read-only;issuing-certificates;crl-signing;ocsp-signing
	// +listType=set
	// +optional
	Usage []string `json:"usage,omitempty"`

	// manualChain overrides the computed CA chain with issuer names or IDs (`manual_chain`); the first entry must be this issuer. Example: `[int-2026, root-2026]`.
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=128
	// +listType=atomic
	// +optional
	ManualChain []string `json:"manualChain,omitempty"`

	// revocationSignatureAlgorithm is the signature algorithm for CRLs and OCSP responses (`revocation_signature_algorithm`). Unset derives it from the key. Example: `ECDSAWithSHA384`.
	// +kubebuilder:validation:Enum=SHA256WithRSA;SHA384WithRSA;SHA512WithRSA;ECDSAWithSHA256;ECDSAWithSHA384;ECDSAWithSHA512;SHA256WithRSAPSS;SHA384WithRSAPSS;SHA512WithRSAPSS;Ed25519;MLDSA44;MLDSA65;MLDSA87
	// +optional
	RevocationSignatureAlgorithm string `json:"revocationSignatureAlgorithm,omitempty"`

	// issuingCertificates are the AIA URLs of this issuer's certificate (`issuing_certificates`), overriding the mount's config. Example: `[https://openbao.example.com/v1/pki-int/issuer/int-2026/der]`.
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=1024
	// +listType=atomic
	// +optional
	IssuingCertificates []string `json:"issuingCertificates,omitempty"`

	// crlDistributionPoints are the CRL URLs of this issuer (`crl_distribution_points`), overriding the mount's config. Example: `[https://openbao.example.com/v1/pki-int/issuer/int-2026/crl/der]`.
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=1024
	// +listType=atomic
	// +optional
	CRLDistributionPoints []string `json:"crlDistributionPoints,omitempty"`

	// ocspServers are the OCSP URLs of this issuer (`ocsp_servers`), overriding the mount's config. Example: `[https://openbao.example.com/v1/pki-int/ocsp]`.
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=1024
	// +listType=atomic
	// +optional
	OCSPServers []string `json:"ocspServers,omitempty"`

	// enableAIAURLTemplating allows `{{cluster_path}}`, `{{cluster_aia_path}}` and `{{issuer_id}}` in the URLs above (`enable_aia_url_templating`); requires at least one of them, because OpenBao keeps it only with an AIA URL. Defaults to false.
	// +optional
	EnableAIAURLTemplating *bool `json:"enableAIAURLTemplating,omitempty"`
}

// status is the observed state of the issuer; `id` holds the OpenBao issuer ID.
type PKIIssuerStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`

	// keyID is the OpenBao ID of the issuer's private key.
	// +optional
	KeyID string `json:"keyID,omitempty"`

	// csr is the PEM certificate signing request of an intermediate while it waits to be signed.
	// +optional
	CSR string `json:"csr,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// PKIIssuer manages a root or intermediate CA issuer of a PKI secrets engine at `<mount>/issuer/<name>`. A root is self-signed; an intermediate is signed by another issuer, possibly in another mount. The certificate is created once; rotate a CA with a new PKIIssuer.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: pki-int
//	  name: int-2026
//	  type: intermediate
//	  certificate:
//	    commonName: Example Intermediate CA 2026
//	    ttl: 43800h
//	  signer:
//	    issuerRef: {name: root-2026}
type PKIIssuer struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec PKIIssuerSpec `json:"spec"`

	// +optional
	Status PKIIssuerStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (p *PKIIssuer) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &p.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (p *PKIIssuer) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &p.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// PKIIssuerList contains a list of PKIIssuer.
type PKIIssuerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []PKIIssuer `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PKIIssuer{}, &PKIIssuerList{})
}
