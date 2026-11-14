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

// Mount-wide authority information access (AIA) settings, written to `<mountPath>/config/urls`.
type PKIURLsConfig struct {
	// issuingCertificates are the URLs where the issuing CA certificate can be fetched (`issuing_certificates`). Up to 16. Example: `[https://openbao.example.com/v1/pki-int/ca]`.
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=1024
	// +listType=atomic
	// +optional
	IssuingCertificates []string `json:"issuingCertificates,omitempty"`

	// crlDistributionPoints are the URLs where the CRL can be fetched (`crl_distribution_points`). Up to 16. Example: `[https://openbao.example.com/v1/pki-int/crl]`.
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=1024
	// +listType=atomic
	// +optional
	CRLDistributionPoints []string `json:"crlDistributionPoints,omitempty"`

	// deltaCRLDistributionPoints are the URLs where the delta CRL can be fetched (`delta_crl_distribution_points`). Up to 16.
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=1024
	// +listType=atomic
	// +optional
	DeltaCRLDistributionPoints []string `json:"deltaCRLDistributionPoints,omitempty"`

	// ocspServers are the URLs of the OCSP responders (`ocsp_servers`). Up to 16. Example: `[https://openbao.example.com/v1/pki-int/ocsp]`.
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=1024
	// +listType=atomic
	// +optional
	OCSPServers []string `json:"ocspServers,omitempty"`

	// enableTemplating allows `{{cluster_path}}`, `{{cluster_aia_path}}` and `{{issuer_id}}` in the URLs (`enable_templating`); the cluster section must then be set. Defaults to false.
	// +optional
	EnableTemplating *bool `json:"enableTemplating,omitempty"`
}

// Revocation settings, written to `<mountPath>/config/crl`.
type PKICRLConfig struct {
	// expiry is the validity of a generated CRL (`expiry`), a duration. Defaults to `72h`. Example: `48h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	Expiry *metav1.Duration `json:"expiry,omitempty"`

	// disable turns off CRL building (`disable`). Defaults to false.
	// +optional
	Disable *bool `json:"disable,omitempty"`

	// ocspDisable turns off the OCSP responder (`ocsp_disable`). Defaults to false.
	// +optional
	OCSPDisable *bool `json:"ocspDisable,omitempty"`

	// ocspExpiry is the validity of OCSP responses (`ocsp_expiry`), a duration; `0s` means no expiry. Defaults to `12h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	OCSPExpiry *metav1.Duration `json:"ocspExpiry,omitempty"`

	// autoRebuild rebuilds the CRL before it expires instead of on every revocation (`auto_rebuild`). Defaults to false.
	// +optional
	AutoRebuild *bool `json:"autoRebuild,omitempty"`

	// autoRebuildGracePeriod is how long before expiry an automatic rebuild runs (`auto_rebuild_grace_period`), a duration shorter than expiry. Defaults to `12h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	AutoRebuildGracePeriod *metav1.Duration `json:"autoRebuildGracePeriod,omitempty"`

	// enableDelta builds delta CRLs between full rebuilds (`enable_delta`); requires autoRebuild. Defaults to false.
	// +optional
	EnableDelta *bool `json:"enableDelta,omitempty"`

	// deltaRebuildInterval is how often delta CRLs are rebuilt (`delta_rebuild_interval`), a duration shorter than expiry. Defaults to `15m`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	DeltaRebuildInterval *metav1.Duration `json:"deltaRebuildInterval,omitempty"`

	// allowExpiredCertRevocation allows revoking certificates that have already expired (`allow_expired_cert_revocation`). Defaults to false.
	// +optional
	AllowExpiredCertRevocation *bool `json:"allowExpiredCertRevocation,omitempty"`
}

// Cluster-local address settings, written to `<mountPath>/config/cluster`; used by templated AIA URLs and required by ACME. Set path or aiaPath.
// +kubebuilder:validation:XValidation:rule="has(self.path) || has(self.aiaPath)",message="set path or aiaPath"
type PKIClusterConfig struct {
	// path is the URL of this mount on this cluster (`path`). Example: `https://openbao.example.com/v1/pki-int`.
	// +kubebuilder:validation:MaxLength=1024
	// +kubebuilder:validation:Pattern=`^[A-Za-z][A-Za-z0-9+.-]*://[^/]+.*$`
	// +optional
	Path string `json:"path,omitempty"`

	// aiaPath is the URL of this mount used in AIA fields (`aia_path`); it may be a non-TLS URL. Example: `http://pki.example.com/v1/pki-int`.
	// +kubebuilder:validation:MaxLength=1024
	// +kubebuilder:validation:Pattern=`^[A-Za-z][A-Za-z0-9+.-]*://[^/]+.*$`
	// +optional
	AIAPath string `json:"aiaPath,omitempty"`
}

// The default issuer of the mount, written to `<mountPath>/config/issuers`.
type PKIIssuersConfig struct {
	// default selects the issuer used by requests that do not name one (`default`). Example: `{issuerName: int-2026}`.
	// +required
	Default PKIIssuerReference `json:"default"`

	// defaultFollowsLatestIssuer moves the default to every newly generated or imported issuer (`default_follows_latest_issuer`). Defaults to false.
	// +optional
	DefaultFollowsLatestIssuer *bool `json:"defaultFollowsLatestIssuer,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="has(self.mountPath) == has(oldSelf.mountPath) && (!has(self.mountPath) || self.mountPath == oldSelf.mountPath)",message="mountPath is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.urls) || has(self.crl) || has(self.cluster) || has(self.issuers)",message="set at least one of urls, crl, cluster and issuers"
// +kubebuilder:validation:XValidation:rule="!has(self.deletionPolicy) || self.deletionPolicy == 'Orphan'",message="the PKI mount configuration cannot be deleted; deletionPolicy must be Orphan"

// spec is the desired mount-wide configuration of a PKI secrets engine: AIA URLs, CRL, cluster address and the default issuer. Every field of urls and crl is managed: an unset field or section is returned to its OpenBao default. The cluster fields and issuers are managed only when set, because OpenBao cannot clear a cluster path and the default issuer has no default to return to. Set at least one section. deletionPolicy must be Orphan.
type PKIConfigSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path of the PKI secrets engine mount. Defaults to `pki`. Immutable. Example: `pki-int`.
	// +kubebuilder:default=pki
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// urls is the mount-wide AIA configuration (`<mountPath>/config/urls`).
	// +optional
	URLs *PKIURLsConfig `json:"urls,omitempty"`

	// crl is the revocation configuration (`<mountPath>/config/crl`).
	// +optional
	CRL *PKICRLConfig `json:"crl,omitempty"`

	// cluster is the cluster-local address configuration (`<mountPath>/config/cluster`).
	// +optional
	Cluster *PKIClusterConfig `json:"cluster,omitempty"`

	// issuers selects the default issuer (`<mountPath>/config/issuers`).
	// +optional
	Issuers *PKIIssuersConfig `json:"issuers,omitempty"`
}

// status is the observed state of the PKI mount configuration.
type PKIConfigStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// PKIConfig sets the mount-wide configuration of a PKI secrets engine: AIA URLs (`config/urls`), CRL (`config/crl`), cluster address (`config/cluster`) and default issuer (`config/issuers`). Use one per mount; it is never deleted.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: pki-int
//	  urls:
//	    issuingCertificates: [https://openbao.example.com/v1/pki-int/ca]
//	    crlDistributionPoints: [https://openbao.example.com/v1/pki-int/crl]
//	  issuers:
//	    default: {issuerName: int-2026}
type PKIConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec PKIConfigSpec `json:"spec"`

	// +optional
	Status PKIConfigStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (c *PKIConfig) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &c.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (c *PKIConfig) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &c.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// PKIConfigList contains a list of PKIConfig.
type PKIConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []PKIConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PKIConfig{}, &PKIConfigList{})
}
