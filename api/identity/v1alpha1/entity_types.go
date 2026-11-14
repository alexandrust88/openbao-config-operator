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

// spec is the desired identity entity, written at `identity/entity/name/<name>`.
type EntitySpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// name is the entity name in OpenBao (`name`). Defaults to metadata.name. No slashes or whitespace. Immutable. Example: `alice`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[^/\s]+$`
	// +optional
	Name string `json:"name,omitempty"`

	// policies are ACL policy names attached to the entity, added to the policies of every token tied to it (`policies`). Lower case; `root` is not allowed. Up to 64. Example: `[kv-reader]`.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=128
	// +kubebuilder:validation:items:Pattern=`^[^A-Z,\s]+$`
	// +kubebuilder:validation:XValidation:rule="!self.exists(p, p == 'root')",message="policies cannot contain root"
	// +listType=set
	// +optional
	Policies []string `json:"policies,omitempty"`

	// metadata is free-form key/value data attached to the entity (`metadata`), usable in ACL policy templates as `{{identity.entity.metadata.<key>}}`. Written as a whole: keys not listed, including keys added outside the operator, are removed. Up to 61 keys of `[a-zA-Z0-9=/+_-]`; the prefix `openbaoconfig-` holds the operator's ownership marker and is reserved. Example: `{team: billing}`.
	// +kubebuilder:validation:MaxProperties=61
	// +kubebuilder:validation:XValidation:rule="self.all(k, k.matches('^[a-zA-Z0-9=/+_-]{1,128}$') && !k.startsWith('openbaoconfig-') && !k.startsWith('vault-') && size(self[k]) <= 512)",message="metadata keys are 1-128 characters of [a-zA-Z0-9=/+_-] without the reserved prefixes openbaoconfig- and vault-, values at most 512 characters"
	// +optional
	Metadata map[string]string `json:"metadata,omitempty"`

	// disabled stops tokens tied to the entity from being used, without revoking them (`disabled`). Defaults to false.
	// +optional
	Disabled *bool `json:"disabled,omitempty"`
}

// status is the observed state of the entity; `id` holds the OpenBao entity ID.
type EntityStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Entity manages an OpenBao identity entity (one person or workload) at `identity/entity/name/<name>`. Logins are linked to it through EntityAlias objects. Deleting it in OpenBao (deletionPolicy Delete) also deletes its aliases.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  name: alice
//	  policies: [kv-reader]
//	  metadata:
//	    team: billing
type Entity struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec EntitySpec `json:"spec"`

	// +optional
	Status EntityStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (e *Entity) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &e.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (e *Entity) GetResourceStatus() *commonv1alpha1.ResourceStatus { return &e.Status.ResourceStatus }

// +kubebuilder:object:root=true

// EntityList contains a list of Entity.
type EntityList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Entity `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Entity{}, &EntityList{})
}
