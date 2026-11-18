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

// +kubebuilder:validation:XValidation:rule="!has(self.openbaoNamespace) || self.openbaoNamespace == ''",message="QuotaConfig exists only in the root namespace: leave openbaoNamespace empty"

// QuotaConfigSpec is the server-wide configuration of rate-limit quotas at sys/quotas/config. Every write replaces the whole configuration. Deleting the resource with deletionPolicy Delete restores the OpenBao defaults.
type QuotaConfigSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// API paths, relative to the root namespace and without /v1/, that no rate-limit quota applies to; order does not matter. Maps to `rate_limit_exempt_paths`. Leave it unset to keep the OpenBao defaults (sys/health, sys/seal-status, sys/unseal and the generate-root and generate-recovery-token endpoints); set it to an empty list to exempt nothing. Example: ["sys/health", "sys/metrics"]
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	RateLimitExemptPaths []string `json:"rateLimitExemptPaths,omitempty"`

	// Whether requests rejected by a rate-limit quota are written to the audit log. Maps to `enable_rate_limit_audit_logging`. Default: false. Example: true
	// +optional
	EnableRateLimitAuditLogging bool `json:"enableRateLimitAuditLogging,omitempty"`

	// Whether every response carries the rate-limit headers (X-Ratelimit-Limit, X-Ratelimit-Remaining, X-Ratelimit-Reset). Maps to `enable_rate_limit_response_headers`. Default: false. Example: true
	// +optional
	EnableRateLimitResponseHeaders bool `json:"enableRateLimitResponseHeaders,omitempty"`
}

// QuotaConfigStatus is the observed state of a QuotaConfig.
type QuotaConfigStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-sys
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// QuotaConfig manages the server-wide rate-limit quota settings at sys/quotas/config: exempt paths, audit logging of rejections and response headers. A root-namespace singleton that always exists, so use creationPolicy Adopt or CreateOrAdopt; every write replaces the whole configuration. deletionPolicy defaults to Orphan; Delete restores the OpenBao defaults.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  creationPolicy: CreateOrAdopt
//	  rateLimitExemptPaths:
//	    - sys/health
//	    - sys/metrics
//	  enableRateLimitAuditLogging: true
type QuotaConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// Desired state of the rate-limit quota settings.
	// +required
	Spec QuotaConfigSpec `json:"spec"`

	// Observed state reported by the operator: conditions, OpenBao path and ownership.
	// +optional
	Status QuotaConfigStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (q *QuotaConfig) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &q.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (q *QuotaConfig) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &q.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// QuotaConfigList contains a list of QuotaConfig.
type QuotaConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []QuotaConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&QuotaConfig{}, &QuotaConfigList{})
}
