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

// +kubebuilder:validation:XValidation:rule="has(self.roleName) != has(self.roleRef)",message="set exactly one of roleName and roleRef"
// +kubebuilder:validation:XValidation:rule="!has(self.mountPath) || has(self.roleName)",message="mountPath is only used with roleName; with roleRef the mount is taken from the AppRole"
// +kubebuilder:validation:XValidation:rule="!has(self.creationPolicy) || self.creationPolicy != 'Adopt'",message="a secret ID cannot be adopted; use Create or CreateOrAdopt"
// +kubebuilder:validation:XValidation:rule="has(self.mountPath) == has(oldSelf.mountPath) && (!has(self.mountPath) || self.mountPath == oldSelf.mountPath)",message="mountPath is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.roleName) == has(oldSelf.roleName) && (!has(self.roleName) || self.roleName == oldSelf.roleName)",message="roleName is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.roleRef) == has(oldSelf.roleRef) && (!has(self.roleRef) || self.roleRef == oldSelf.roleRef)",message="roleRef is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.metadata) == has(oldSelf.metadata) && (!has(self.metadata) || self.metadata == oldSelf.metadata)",message="metadata is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.cidrList) == has(oldSelf.cidrList) && (!has(self.cidrList) || self.cidrList == oldSelf.cidrList)",message="cidrList is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.tokenBoundCIDRs) == has(oldSelf.tokenBoundCIDRs) && (!has(self.tokenBoundCIDRs) || self.tokenBoundCIDRs == oldSelf.tokenBoundCIDRs)",message="tokenBoundCIDRs is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.ttl) == has(oldSelf.ttl) && (!has(self.ttl) || self.ttl == oldSelf.ttl)",message="ttl is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.numUses) == has(oldSelf.numUses) && (!has(self.numUses) || self.numUses == oldSelf.numUses)",message="numUses is immutable"
// +kubebuilder:validation:XValidation:rule="self.writeSecretRef == oldSelf.writeSecretRef",message="writeSecretRef is immutable"

// Desired state of a secret ID of an AppRole role. Set exactly one of roleName and roleRef. The secret ID is written to the Secret named by writeSecretRef and never stored in status. A secret ID cannot be adopted: use creationPolicy Create or CreateOrAdopt.
type AppRoleSecretIDSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path of the AppRole auth method, without the auth/ prefix. Only used with roleName; defaults to approle. With roleRef the mount comes from the AppRole. Immutable. Example: approle
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// Name of the AppRole role in OpenBao. Set exactly one of roleName and roleRef. Immutable. Example: ci-deploy
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[a-z0-9_]([a-z0-9_.-]*[a-z0-9_])?$`
	// +optional
	RoleName string `json:"roleName,omitempty"`

	// AppRole resource in this Kubernetes namespace whose mount and role name are used. The AppRole must be Ready and use the same connectionRef and namespace. Set exactly one of roleName and roleRef. Immutable. Example: {name: ci-deploy}
	// +optional
	RoleRef *commonv1alpha1.LocalObjectReference `json:"roleRef,omitempty"`

	// Key-value metadata attached to the secret ID and to tokens issued with it. At most 32 entries. Immutable. Maps to `metadata` (sent as a JSON string). Example: {team: platform}
	// +kubebuilder:validation:MaxProperties=32
	// +optional
	Metadata map[string]string `json:"metadata,omitempty"`

	// CIDR blocks or IP addresses allowed to log in with this secret ID; must be a subset of the role's secretIDBoundCIDRs. Immutable. Maps to `cidr_list`. Example: [10.20.1.0/24]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=64
	// +kubebuilder:validation:XValidation:rule="self.all(c, isCIDR(c) || isIP(c))",message="cidrList entries must be CIDR blocks or IP addresses"
	// +listType=set
	// +optional
	CIDRList []string `json:"cidrList,omitempty"`

	// CIDR blocks or IP addresses allowed to use tokens issued with this secret ID; must be a subset of the role's tokenBoundCIDRs. Immutable. Maps to `token_bound_cidrs`. Example: [10.20.1.0/24]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=64
	// +kubebuilder:validation:XValidation:rule="self.all(c, isCIDR(c) || isIP(c))",message="tokenBoundCIDRs entries must be CIDR blocks or IP addresses"
	// +listType=set
	// +optional
	TokenBoundCIDRs []string `json:"tokenBoundCIDRs,omitempty"`

	// Lifetime of the secret ID, as a duration. Must not exceed the role's secretIDTTL; defaults to it. Immutable. Maps to `ttl`. Example: 168h
	// +optional
	TTL *metav1.Duration `json:"ttl,omitempty"`

	// Number of logins the secret ID allows. Must not exceed the role's secretIDNumUses; defaults to it. 0 means unlimited. Immutable. Maps to `num_uses`. Example: 5
	// +kubebuilder:validation:Minimum=0
	// +optional
	NumUses *int32 `json:"numUses,omitempty"`

	// Secret in this Kubernetes namespace, owned by this resource, that receives the keys role_id, secret_id and secret_id_accessor. A missing Secret or an empty secret_id key makes the operator generate a new secret ID. Required. Immutable. Example: {name: ci-deploy-approle}
	// +required
	WriteSecretRef commonv1alpha1.LocalObjectReference `json:"writeSecretRef"`

	// Automatic replacement of the secret ID before it expires. Enabled by default; only applies to secret IDs with a TTL. Example: {renewBefore: 24h}
	// +optional
	Rotation *SecretIDRotation `json:"rotation,omitempty"`
}

// Automatic replacement of an expiring secret ID.
type SecretIDRotation struct {
	// Turns rotation on or off. Defaults to true. Example: false
	// +kubebuilder:default=true
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// How long before expiry a new secret ID is generated: a duration or a percentage (1-99%) of the TTL. Defaults to a third of the TTL. Expiry is checked every drift interval, so keep this longer than the drift interval. The old secret ID is destroyed once the Secret holds the new one. Example: 25%
	// +kubebuilder:validation:MaxLength=32
	// +kubebuilder:validation:Pattern=`^(([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+|[1-9][0-9]?%)$`
	// +optional
	RenewBefore string `json:"renewBefore,omitempty"`
}

// Observed state of the secret ID, written by the operator. status.id holds the secret ID accessor, never the secret ID.
type AppRoleSecretIDStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`

	// OpenBao path of the role the secret ID belongs to, relative to the OpenBao namespace. Example: auth/approle/role/ci-deploy
	// +optional
	RolePath string `json:"rolePath,omitempty"`

	// Time the current secret ID expires; unset when it does not expire. Example: "2026-12-14T10:00:00Z"
	// +optional
	ExpirationTime *metav1.Time `json:"expirationTime,omitempty"`

	// Accessors of replaced secret IDs, destroyed once the Secret holds the new secret ID. Example: [6f1c2e7a-0b9d-4c55-9a51-2f0e8f3d1b2c]
	// +listType=set
	// +optional
	RetiredAccessors []string `json:"retiredAccessors,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Expires",type=date,JSONPath=`.status.expirationTime`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AppRoleSecretID generates a secret ID for an OpenBao AppRole role at auth/<mountPath>/role/<role>/secret-id and writes it, with the role ID, to a Kubernetes Secret. Use it to hand AppRole credentials to a workload; the operator rotates the secret ID before it expires.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  roleRef:
//	    name: ci-deploy
//	  ttl: 720h
//	  writeSecretRef:
//	    name: ci-deploy-approle
type AppRoleSecretID struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec AppRoleSecretIDSpec `json:"spec"`

	// +optional
	Status AppRoleSecretIDStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (a *AppRoleSecretID) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &a.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (a *AppRoleSecretID) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &a.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// AppRoleSecretIDList contains a list of AppRoleSecretID.
type AppRoleSecretIDList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []AppRoleSecretID `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AppRoleSecretID{}, &AppRoleSecretIDList{})
}
