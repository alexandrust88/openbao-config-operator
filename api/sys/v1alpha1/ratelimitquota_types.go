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

// +kubebuilder:validation:XValidation:rule="has(self.name) == has(oldSelf.name) && (!has(self.name) || self.name == oldSelf.name)",message="name is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.role) == has(oldSelf.role) && (!has(self.role) || self.role == oldSelf.role)",message="role is immutable"
// +kubebuilder:validation:XValidation:rule="!has(self.openbaoNamespace) || self.openbaoNamespace == ''",message="rate limit quotas exist only in the root namespace; scope them with path"
// +kubebuilder:validation:XValidation:rule="!has(self.role) || has(self.path)",message="role requires path to name an auth method mount"
// +kubebuilder:validation:XValidation:rule="double(self.rate) > 0.0",message="rate must be greater than 0"
// +kubebuilder:validation:XValidation:rule="!has(self.interval) || duration(self.interval) >= duration('1s')",message="interval must be at least 1s"

// RateLimitQuotaSpec defines a rate limit quota at sys/quotas/rate-limit/<name>. Quotas are managed in the root namespace and enforced per node.
type RateLimitQuotaSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Name of the quota, the <name> in sys/quotas/rate-limit/<name>. Default: metadata.name. Immutable. Example: team-a-kv
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^\w([\w.-]*\w)?$`
	// +optional
	Name string `json:"name,omitempty"`

	// Scope of the quota: an OpenBao namespace, a mount or a path below a mount, such as team-a/, team-a/kv or team-a/kv/app. Maps to `path`. Empty applies the quota globally. Example: team-a/kv
	// +kubebuilder:validation:MaxLength=512
	// +optional
	Path string `json:"path,omitempty"`

	// Limits the quota to logins with this role of the auth method mount named by path (path is required). Maps to `role`. Immutable: it cannot be added, changed or removed after creation. Example: app
	// +kubebuilder:validation:MaxLength=128
	// +optional
	Role string `json:"role,omitempty"`

	// Number of requests allowed per interval, as a positive decimal number in a string. Maps to `rate`. Example: "12.5"
	// +kubebuilder:validation:MaxLength=32
	// +kubebuilder:validation:Pattern=`^[0-9]+(\.[0-9]+)?$`
	// +required
	Rate string `json:"rate"`

	// Time window the rate applies to, as a duration of at least 1s. Maps to `interval` (seconds). Default: 1s. Example: 1m
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	Interval *metav1.Duration `json:"interval,omitempty"`

	// How long a client that exceeded the rate is blocked, as a duration. Maps to `block_interval` (seconds). Unset or 0 does not block. Example: 5m
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	BlockInterval *metav1.Duration `json:"blockInterval,omitempty"`

	// Whether the quota also applies to the child namespaces of path. Maps to `inheritable`. Default: true for a global quota (empty path), false otherwise. Example: true
	// +optional
	Inheritable *bool `json:"inheritable,omitempty"`
}

// RateLimitQuotaStatus is the observed state of a RateLimitQuota.
type RateLimitQuotaStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-sys
// +kubebuilder:printcolumn:name="Rate",type=string,JSONPath=`.spec.rate`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// RateLimitQuota manages an OpenBao rate limit quota at sys/quotas/rate-limit/<name>, which caps the request rate globally or for one namespace, mount, path or auth role. Quotas live in the root namespace (scope them with path) and are enforced per node. deletionPolicy defaults to Orphan; Delete removes the quota.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  name: team-a-kv
//	  path: team-a/kv
//	  rate: "100"
//	  interval: 1s
type RateLimitQuota struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// Desired state of the OpenBao rate limit quota.
	// +required
	Spec RateLimitQuotaSpec `json:"spec"`

	// Observed state reported by the operator: conditions, OpenBao path and ownership.
	// +optional
	Status RateLimitQuotaStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (q *RateLimitQuota) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &q.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (q *RateLimitQuota) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &q.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// RateLimitQuotaList contains a list of RateLimitQuota.
type RateLimitQuotaList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []RateLimitQuota `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RateLimitQuota{}, &RateLimitQuotaList{})
}
