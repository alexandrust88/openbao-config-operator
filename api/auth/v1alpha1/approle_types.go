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
// +kubebuilder:validation:XValidation:rule="has(self.localSecretIDs) == has(oldSelf.localSecretIDs) && (!has(self.localSecretIDs) || self.localSecretIDs == oldSelf.localSecretIDs)",message="localSecretIDs is immutable"
// +kubebuilder:validation:XValidation:rule="!has(self.bindSecretID) || self.bindSecretID || has(self.secretIDBoundCIDRs) || has(self.tokenBoundCIDRs)",message="bindSecretID false requires secretIDBoundCIDRs or tokenBoundCIDRs"

// Desired state of an AppRole role, written to auth/<mountPath>/role/<name>. Setting bindSecretID to false requires secretIDBoundCIDRs or tokenBoundCIDRs.
type AppRoleSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path of the AppRole auth method, without the auth/ prefix. Defaults to approle. Immutable. Example: approle/ci
	// +kubebuilder:default=approle
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// Name of the role in OpenBao. Defaults to metadata.name. OpenBao stores role names in lower case. Immutable. Example: ci-deploy
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[a-z0-9_]([a-z0-9_.-]*[a-z0-9_])?$`
	// +optional
	Name string `json:"name,omitempty"`

	// Custom role ID that clients send at login. Unset lets OpenBao generate a UUID; the effective role ID is reported in status.id. Not a secret. Maps to `role_id`. Example: ci-deploy-role
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +optional
	RoleID string `json:"roleID,omitempty"`

	// Requires a secret ID at login. OpenBao defaults to true; false requires secretIDBoundCIDRs or tokenBoundCIDRs. Maps to `bind_secret_id`. Example: true
	// +optional
	BindSecretID *bool `json:"bindSecretID,omitempty"`

	// Client CIDR blocks or IP addresses allowed to log in with a secret ID of this role. Unset allows any address. Maps to `secret_id_bound_cidrs`. Example: [10.20.0.0/16]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=64
	// +kubebuilder:validation:XValidation:rule="self.all(c, isCIDR(c) || isIP(c))",message="secretIDBoundCIDRs entries must be CIDR blocks or IP addresses"
	// +listType=set
	// +optional
	SecretIDBoundCIDRs []string `json:"secretIDBoundCIDRs,omitempty"`

	// Number of logins a secret ID allows before it expires; 0 or unset means unlimited. Maps to `secret_id_num_uses`. Example: 10
	// +kubebuilder:validation:Minimum=0
	// +optional
	SecretIDNumUses *int32 `json:"secretIDNumUses,omitempty"`

	// Lifetime of secret IDs generated for this role, as a duration. 0 or unset means they do not expire. Maps to `secret_id_ttl`. Example: 720h
	// +optional
	SecretIDTTL *metav1.Duration `json:"secretIDTTL,omitempty"`

	// Makes secret IDs of this role local to the cluster (not replicated). Can only be set when the role is created. Defaults to false. Immutable. Maps to `local_secret_ids`. Example: true
	// +optional
	LocalSecretIDs *bool `json:"localSecretIDs,omitempty"`

	TokenFields `json:",inline"`
}

// Observed state of the role, written by the operator. status.id holds the role ID.
type AppRoleStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=baoapr,categories=obco;obco-auth
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AppRole manages a role of an OpenBao AppRole auth method at auth/<mountPath>/role/<name>. Use it for machines and CI jobs outside Kubernetes that log in with a role ID and a secret ID; create secret IDs with AppRoleSecretID.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: approle
//	  secretIDTTL: 720h
//	  tokenPolicies: [ci-deploy]
//	  tokenTTL: 20m
type AppRole struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec AppRoleSpec `json:"spec"`

	// +optional
	Status AppRoleStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (a *AppRole) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &a.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (a *AppRole) GetResourceStatus() *commonv1alpha1.ResourceStatus { return &a.Status.ResourceStatus }

// +kubebuilder:object:root=true

// AppRoleList contains a list of AppRole.
type AppRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []AppRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AppRole{}, &AppRoleList{})
}
