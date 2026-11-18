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

// MountTokenType is the default type of token an auth method issues.
// +kubebuilder:validation:Enum=default-service;default-batch;service;batch
type MountTokenType string

const (
	MountTokenTypeDefaultService MountTokenType = "default-service"
	MountTokenTypeDefaultBatch   MountTokenType = "default-batch"
	MountTokenTypeService        MountTokenType = "service"
	MountTokenTypeBatch          MountTokenType = "batch"
)

// UserLockoutConfig configures the lockout of users after failed logins.
type UserLockoutConfig struct {
	// Number of failed logins after which a user is locked out. Maps to `lockout_threshold`. Minimum 0. Example: 5
	// +kubebuilder:validation:Minimum=0
	// +optional
	Threshold *int32 `json:"threshold,omitempty"`

	// How long a locked-out user stays locked out, as a duration. Maps to `lockout_duration`. Example: 15m
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	Duration *metav1.Duration `json:"duration,omitempty"`

	// Time after which the failed-login counter is reset, as a duration. Maps to `lockout_counter_reset_duration`. Example: 30m
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	CounterResetDuration *metav1.Duration `json:"counterResetDuration,omitempty"`

	// Turns user lockout off for this mount. Maps to `lockout_disable`. Default: false. Example: true
	// +optional
	Disable *bool `json:"disable,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="has(self.path) == has(oldSelf.path) && (!has(self.path) || self.path == oldSelf.path)",message="path is immutable"
// +kubebuilder:validation:XValidation:rule="self.type == oldSelf.type",message="type is immutable"
// +kubebuilder:validation:XValidation:rule="!has(self.userLockout) || self.type in ['approle', 'ldap', 'userpass']",message="userLockout is supported only for the approle, ldap and userpass auth methods"

// AuthMountSpec defines an auth method enabled at sys/auth/<path>.
type AuthMountSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path the auth method is enabled at, without the auth/ prefix and without slashes at either end. Default: metadata.name. Immutable: it cannot be added, changed or removed after creation. Example: kubernetes-prod
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	Path string `json:"path,omitempty"`

	// Auth method type: a built-in method such as kubernetes, jwt, approle or userpass, or the name of a registered plugin. Maps to `type`. Immutable. Example: kubernetes
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +required
	Type string `json:"type"`

	MountFields `json:",inline"`

	// Default type of token the auth method issues. Maps to `token_type`. Allowed values: default-service, default-batch, service, batch. Default: default-service. Example: service
	// +optional
	TokenType MountTokenType `json:"tokenType,omitempty"`

	// Lockout of users after repeated failed logins. Maps to `user_lockout_config`. Only for the approle, ldap and userpass auth methods; unset uses the server defaults.
	// +optional
	UserLockout *UserLockoutConfig `json:"userLockout,omitempty"`
}

// AuthMountStatus is the observed state of an AuthMount. status.id holds the mount accessor.
type AuthMountStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=baoam,categories=obco;obco-sys
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AuthMount enables and tunes an OpenBao auth method at sys/auth/<path> (logins then go to auth/<path>/login). Use it before the auth kinds that configure the method, such as KubernetesAuthConfig and its roles. status.id holds the mount accessor. The default deletionPolicy Orphan keeps the mount; Delete disables it, which revokes every token it issued.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  type: kubernetes
//	  path: kubernetes
//	  description: Kubernetes workload logins
//	  defaultLeaseTTL: 1h
type AuthMount struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// Desired state of the auth method mount.
	// +required
	Spec AuthMountSpec `json:"spec"`

	// Observed state reported by the operator; id holds the mount accessor.
	// +optional
	Status AuthMountStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (m *AuthMount) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &m.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (m *AuthMount) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &m.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// AuthMountList contains a list of AuthMount.
type AuthMountList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []AuthMount `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AuthMount{}, &AuthMountList{})
}
