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

// The type of Nomad ACL token a role issues: client or management.
// +kubebuilder:validation:Enum=client;management
type NomadTokenType string

const (
	NomadTokenTypeClient     NomadTokenType = "client"
	NomadTokenTypeManagement NomadTokenType = "management"
)

// +kubebuilder:validation:XValidation:rule="has(self.mountPath) == has(oldSelf.mountPath) && (!has(self.mountPath) || self.mountPath == oldSelf.mountPath)",message="mountPath is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.name) == has(oldSelf.name) && (!has(self.name) || self.name == oldSelf.name)",message="name is immutable"
// +kubebuilder:validation:XValidation:rule="(has(self.type) && self.type == 'management') ? !has(self.policies) : has(self.policies)",message="client roles need policies; management roles take none"

// spec is the desired role of the Nomad secrets plugin at `<mountPath>/role/<name>`. Client roles need policies; management roles take none.
type NomadSecretsRoleSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path the Nomad secrets plugin is mounted at. Defaults to `nomad`. Immutable. Example: `nomad`.
	// +kubebuilder:default=nomad
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// name is the role name in OpenBao. Defaults to metadata.name. Immutable. Example: `deployer`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_]([A-Za-z0-9_.-]*[A-Za-z0-9_])?$`
	// +optional
	Name string `json:"name,omitempty"`

	// type is the type of the issued token (`type`). Allowed: client, management. Defaults to client.
	// +kubebuilder:default=client
	// +optional
	Type NomadTokenType `json:"type,omitempty"`

	// global makes issued tokens global across Nomad regions (`global`). Defaults to false.
	// +optional
	Global *bool `json:"global,omitempty"`

	// policies lists the Nomad ACL policies attached to client tokens (`policies`); they must exist in Nomad. Example: `[deploy]`.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=128
	// +listType=set
	// +optional
	Policies []string `json:"policies,omitempty"`
}

// status is the observed state of the Nomad secrets role.
type NomadSecretsRoleStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-secrets
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// NomadSecretsRole manages a role of a Nomad secrets plugin mount (external plugin) at `<mount>/role/<name>`: the type and policies of the Nomad ACL tokens issued from `<mount>/creds/<name>`.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: nomad
//	  name: deployer
//	  policies: [deploy]
type NomadSecretsRole struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec NomadSecretsRoleSpec `json:"spec"`

	// +optional
	Status NomadSecretsRoleStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *NomadSecretsRole) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &o.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *NomadSecretsRole) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// NomadSecretsRoleList contains a list of NomadSecretsRole.
type NomadSecretsRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []NomadSecretsRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NomadSecretsRole{}, &NomadSecretsRoleList{})
}
