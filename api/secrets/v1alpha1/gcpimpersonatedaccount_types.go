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
// +kubebuilder:validation:XValidation:rule="self.serviceAccountEmail == oldSelf.serviceAccountEmail",message="serviceAccountEmail is immutable"

// spec is the desired impersonated account of a GCP secrets engine at `<mountPath>/impersonated-account/<name>`. OpenBao issues access tokens by impersonating an existing service account.
type GCPImpersonatedAccountSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path of the GCP secrets engine mount. Defaults to `gcp`. Immutable. Example: `gcp`.
	// +kubebuilder:default=gcp
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// name is the impersonated account name in OpenBao. Defaults to metadata.name. Immutable. Example: `reporting`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_-]+$`
	// +optional
	Name string `json:"name,omitempty"`

	// serviceAccountEmail is the email of the service account to impersonate (`service_account_email`). Immutable. Example: `reporting@my-project.iam.gserviceaccount.com`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=254
	// +required
	ServiceAccountEmail string `json:"serviceAccountEmail"`

	// tokenScopes are the OAuth scopes of issued access tokens (`token_scopes`). At least one. Example: `[https://www.googleapis.com/auth/cloud-platform]`.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:items:MaxLength=256
	// +listType=set
	// +required
	TokenScopes []string `json:"tokenScopes"`

	// ttl is the lifetime of issued access tokens (`ttl`), a duration. Defaults to the mount's default lease TTL. Example: `1h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	TTL *metav1.Duration `json:"ttl,omitempty"`
}

// status is the observed state of the GCP impersonated account.
type GCPImpersonatedAccountStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-secrets
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// GCPImpersonatedAccount manages an impersonated account of a GCP secrets engine (external plugin) at `<mount>/impersonated-account/<name>`: OpenBao issues access tokens by impersonating an existing service account, without keys.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: gcp
//	  name: reporting
//	  serviceAccountEmail: reporting@my-project.iam.gserviceaccount.com
//	  tokenScopes: [https://www.googleapis.com/auth/cloud-platform]
type GCPImpersonatedAccount struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec GCPImpersonatedAccountSpec `json:"spec"`

	// +optional
	Status GCPImpersonatedAccountStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *GCPImpersonatedAccount) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &o.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *GCPImpersonatedAccount) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// GCPImpersonatedAccountList contains a list of GCPImpersonatedAccount.
type GCPImpersonatedAccountList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GCPImpersonatedAccount `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GCPImpersonatedAccount{}, &GCPImpersonatedAccountList{})
}
