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

// Desired state of an Azure auth role, written to auth/<mountPath>/role/<name>. A login must match every bound list that is set.
type AzureAuthRoleSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path of the Azure auth method, without the auth/ prefix. Defaults to azure. Immutable. Example: azure
	// +kubebuilder:default=azure
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// Name of the role in OpenBao, used in the login request. Defaults to metadata.name. Immutable. Example: app
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_]([A-Za-z0-9_.-]*[A-Za-z0-9_])?$`
	// +optional
	Name string `json:"name,omitempty"`

	// Service principal (managed identity) IDs allowed to log in; "*" allows any. Maps to `bound_service_principal_ids`. Example: [33333333-3333-3333-3333-333333333333]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	BoundServicePrincipalIDs []string `json:"boundServicePrincipalIDs,omitempty"`

	// Microsoft Entra ID group IDs; the login identity must be a member of one of them. Maps to `bound_group_ids`. Example: [44444444-4444-4444-4444-444444444444]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	BoundGroupIDs []string `json:"boundGroupIDs,omitempty"`

	// Azure regions the logging-in virtual machine must run in. Maps to `bound_locations`. Example: [westeurope]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	BoundLocations []string `json:"boundLocations,omitempty"`

	// Subscription IDs the logging-in resource must belong to. Maps to `bound_subscription_ids`. Example: [22222222-2222-2222-2222-222222222222]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	BoundSubscriptionIDs []string `json:"boundSubscriptionIDs,omitempty"`

	// Resource group names the logging-in resource must belong to. Maps to `bound_resource_groups`. Example: [app-rg]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	BoundResourceGroups []string `json:"boundResourceGroups,omitempty"`

	// Virtual machine scale set names the logging-in virtual machine must belong to. Maps to `bound_scale_sets`. Example: [app-vmss]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	BoundScaleSets []string `json:"boundScaleSets,omitempty"`

	TokenFields `json:",inline"`
}

// Observed state of the role, written by the operator.
type AzureAuthRoleStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AzureAuthRole manages a role of an OpenBao Azure auth method at auth/<mountPath>/role/<name>. Use it to let Azure workloads log in with a managed identity token and receive an OpenBao token with the listed policies. The Azure auth plugin must be enabled at mountPath (AuthMount) and configured (AzureAuthConfig).
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: azure
//	  boundSubscriptionIDs: [22222222-2222-2222-2222-222222222222]
//	  boundResourceGroups: [app-rg]
//	  tokenPolicies: [app-read]
//	  tokenTTL: 1h
type AzureAuthRole struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec AzureAuthRoleSpec `json:"spec"`

	// +optional
	Status AzureAuthRoleStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *AzureAuthRole) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &o.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *AzureAuthRole) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// AzureAuthRoleList contains a list of AzureAuthRole.
type AzureAuthRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []AzureAuthRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AzureAuthRole{}, &AzureAuthRoleList{})
}
