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
// +kubebuilder:validation:XValidation:rule="!has(self.deletionPolicy) || self.deletionPolicy == 'Orphan'",message="the auto-tidy configuration cannot be deleted; deletionPolicy must be Orphan"
// +kubebuilder:validation:XValidation:rule="!self.?enabled.orValue(false) || self.?tidyCertStore.orValue(false) || self.?tidyRevokedCerts.orValue(false) || self.?tidyInvalidCerts.orValue(false) || self.?tidyRevokedCertIssuerAssociations.orValue(false) || self.?tidyExpiredIssuers.orValue(false) || self.?tidyMoveLegacyCABundle.orValue(false) || self.?tidyACME.orValue(false)",message="enabled requires at least one tidy operation"
// +kubebuilder:validation:XValidation:rule="!self.?publishStoredCertificateCountMetrics.orValue(false) || self.?maintainStoredCertificateCounts.orValue(false)",message="publishStoredCertificateCountMetrics requires maintainStoredCertificateCounts"

// spec is the desired periodic tidy configuration of a PKI secrets engine at `<mountPath>/config/auto-tidy`. It sets the schedule only; the operator never starts a tidy run itself. Fields left unset keep the server value. deletionPolicy must be Orphan.
type PKIAutoTidySpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path of the PKI secrets engine mount. Defaults to `pki`. Immutable. Example: `pki-int`.
	// +kubebuilder:default=pki
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// enabled turns on periodic tidy runs (`enabled`); requires at least one tidy operation. Defaults to false.
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// intervalDuration is the time between tidy runs (`interval_duration`), a duration. Defaults to `12h`. Example: `24h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	IntervalDuration *metav1.Duration `json:"intervalDuration,omitempty"`

	// tidyCertStore removes expired certificates from storage (`tidy_cert_store`). Defaults to false.
	// +optional
	TidyCertStore *bool `json:"tidyCertStore,omitempty"`

	// tidyRevokedCerts removes expired certificates from the revocation list (`tidy_revoked_certs`). Defaults to false.
	// +optional
	TidyRevokedCerts *bool `json:"tidyRevokedCerts,omitempty"`

	// tidyInvalidCerts removes certificates that cannot be parsed (`tidy_invalid_certs`). Defaults to false.
	// +optional
	TidyInvalidCerts *bool `json:"tidyInvalidCerts,omitempty"`

	// tidyRevokedCertIssuerAssociations repairs the issuer association of revoked certificates (`tidy_revoked_cert_issuer_associations`). Defaults to false.
	// +optional
	TidyRevokedCertIssuerAssociations *bool `json:"tidyRevokedCertIssuerAssociations,omitempty"`

	// tidyExpiredIssuers removes issuers that expired more than issuerSafetyBuffer ago (`tidy_expired_issuers`). Defaults to false.
	// +optional
	TidyExpiredIssuers *bool `json:"tidyExpiredIssuers,omitempty"`

	// tidyMoveLegacyCABundle moves the pre-multi-issuer CA bundle to a backup location (`tidy_move_legacy_ca_bundle`). Defaults to false.
	// +optional
	TidyMoveLegacyCABundle *bool `json:"tidyMoveLegacyCABundle,omitempty"`

	// tidyACME removes expired ACME accounts, orders and challenges (`tidy_acme`). Defaults to false.
	// +optional
	TidyACME *bool `json:"tidyACME,omitempty"`

	// safetyBuffer is how long after expiry a certificate is kept (`safety_buffer`), a duration. Defaults to `72h`. Example: `168h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	SafetyBuffer *metav1.Duration `json:"safetyBuffer,omitempty"`

	// revokedSafetyBuffer is how long after expiry a revoked certificate is kept (`revoked_safety_buffer`), a duration. Defaults to safetyBuffer. Example: `720h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	RevokedSafetyBuffer *metav1.Duration `json:"revokedSafetyBuffer,omitempty"`

	// issuerSafetyBuffer is how long after expiry an issuer is kept (`issuer_safety_buffer`), a duration. Defaults to `8760h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	IssuerSafetyBuffer *metav1.Duration `json:"issuerSafetyBuffer,omitempty"`

	// acmeAccountSafetyBuffer is how long an inactive ACME account is kept (`acme_account_safety_buffer`), a duration. Defaults to `720h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	ACMEAccountSafetyBuffer *metav1.Duration `json:"acmeAccountSafetyBuffer,omitempty"`

	// pageSize is the number of certificates processed per storage page (`page_size`). Minimum 5. Defaults to 1000.
	// +kubebuilder:validation:Minimum=5
	// +optional
	PageSize *int32 `json:"pageSize,omitempty"`

	// pauseDuration is the pause between certificates, to limit storage load (`pause_duration`), a duration. Defaults to `0s`. Example: `100ms`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	PauseDuration *metav1.Duration `json:"pauseDuration,omitempty"`

	// maintainStoredCertificateCounts keeps a running count of stored and revoked certificates (`maintain_stored_certificate_counts`). Defaults to false.
	// +optional
	MaintainStoredCertificateCounts *bool `json:"maintainStoredCertificateCounts,omitempty"`

	// publishStoredCertificateCountMetrics publishes those counts as metrics (`publish_stored_certificate_count_metrics`); requires maintainStoredCertificateCounts. Defaults to false.
	// +optional
	PublishStoredCertificateCountMetrics *bool `json:"publishStoredCertificateCountMetrics,omitempty"`
}

// status is the observed state of the auto-tidy configuration.
type PKIAutoTidyStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-secrets
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// PKIAutoTidy sets the periodic tidy configuration of a PKI secrets engine at `<mount>/config/auto-tidy`, which removes expired certificates, issuers and ACME data on a schedule. A singleton per mount; it is never deleted.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: pki-int
//	  enabled: true
//	  intervalDuration: 24h
//	  tidyCertStore: true
//	  tidyRevokedCerts: true
type PKIAutoTidy struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec PKIAutoTidySpec `json:"spec"`

	// +optional
	Status PKIAutoTidyStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (t *PKIAutoTidy) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &t.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (t *PKIAutoTidy) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &t.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// PKIAutoTidyList contains a list of PKIAutoTidy.
type PKIAutoTidyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []PKIAutoTidy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PKIAutoTidy{}, &PKIAutoTidyList{})
}
