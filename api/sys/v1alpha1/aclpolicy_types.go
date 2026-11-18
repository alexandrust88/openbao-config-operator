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

// ACLPolicySpec defines an ACL policy at sys/policies/acl/<name>.
type ACLPolicySpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Name of the policy, the <name> in sys/policies/acl/<name>. Default: metadata.name. OpenBao lowercases and path-cleans policy names, so the name must be lowercase, without whitespace, and without a leading or trailing slash or an empty, "." or ".." segment. root and response-wrapping cannot be managed. Immutable. Example: app-read
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^([^/.\s\p{Lu}][^/\s\p{Lu}]*|\.[^/.\s\p{Lu}][^/\s\p{Lu}]*|\.\.[^/\s\p{Lu}]+)(/([^/.\s\p{Lu}][^/\s\p{Lu}]*|\.[^/.\s\p{Lu}][^/\s\p{Lu}]*|\.\.[^/\s\p{Lu}]+))*$`
	// +optional
	Name string `json:"name,omitempty"`

	// ACL policy document in HCL or JSON. Maps to `policy`. `${accessor:<name>}` is replaced by the accessor of the auth mount that accessorRefs names; OpenBao `{{identity...}}` templates are sent unchanged. Example: path "secret/data/app/*" { capabilities = ["read"] }
	// +kubebuilder:validation:MinLength=1
	// +required
	Policy string `json:"policy"`

	// Auth mounts whose accessors the policy uses, by placeholder name: `${accessor:<name>}` in policy becomes the accessor of the mount. The policy is rewritten when the mount is recreated with a new accessor, and waits while a mount is missing. At most 16 entries; names are 1-63 characters of [a-z0-9_-]. Example: {k8s: {authMountRef: {name: kubernetes}}}
	// +kubebuilder:validation:MaxProperties=16
	// +kubebuilder:validation:XValidation:rule="self.all(k, k.matches('^[a-z0-9_-]{1,63}$'))",message="accessorRefs names are 1-63 characters of [a-z0-9_-]"
	// +optional
	AccessorRefs map[string]ACLPolicyAccessorRef `json:"accessorRefs,omitempty"`

	// Whether every change of the policy must pass the current version as check-and-set value; the operator sends it on each write. Maps to `cas_required`. Default: false. Example: true
	// +optional
	CASRequired bool `json:"casRequired,omitempty"`

	// Whether identity templates in path names may expand to values that contain the glob characters * and +, which would otherwise make the path match more than intended. Maps to `allow_wildcards_in_identity_templates`. Default: false. Example: false
	// +optional
	AllowWildcardsInIdentityTemplates bool `json:"allowWildcardsInIdentityTemplates,omitempty"`

	// Whether identity templates in path names may expand to values that contain a slash, which is blocked by default because it adds path segments. Maps to `allow_slashes_in_identity_templates`. Default: false. Example: false
	// +optional
	AllowSlashesInIdentityTemplates bool `json:"allowSlashesInIdentityTemplates,omitempty"`
}

// The auth mount whose accessor replaces one placeholder. Set exactly one field.
// +kubebuilder:validation:ExactlyOneOf=authMountRef;mountPath
type ACLPolicyAccessorRef struct {
	// AuthMount in this namespace; its accessor is taken from the AuthMount status. Example: {name: kubernetes}
	// +optional
	AuthMountRef *commonv1alpha1.LocalObjectReference `json:"authMountRef,omitempty"`

	// Path of the auth mount in the policy's OpenBao namespace, without the auth/ prefix; its accessor is read from sys/auth. Example: kubernetes
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`
}

// ACLPolicyStatus is the observed state of an ACLPolicy. status.id is the policy name as OpenBao stores it.
type ACLPolicyStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=baopol,categories=obco;obco-sys
// +kubebuilder:validation:XValidation:rule="!((has(self.spec.name) ? self.spec.name : self.metadata.name) in ['root', 'response-wrapping'])",message="the root and response-wrapping policies cannot be changed"
// +kubebuilder:validation:XValidation:rule="(has(self.spec.name) ? self.spec.name : self.metadata.name) != 'default' || (has(self.spec.deletionPolicy) && self.spec.deletionPolicy == 'Orphan')",message="the default policy cannot be deleted; set deletionPolicy to Orphan"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ACLPolicy manages an OpenBao ACL policy at sys/policies/acl/<name>, which grants capabilities on paths to the tokens it is attached to. The root and response-wrapping policies cannot be managed, and the default policy only with deletionPolicy Orphan. deletionPolicy defaults to Orphan; Delete removes the policy.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  name: app-read
//	  policy: |
//	    path "secret/data/app/*" {
//	      capabilities = ["read"]
//	    }
type ACLPolicy struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// Desired state of the OpenBao ACL policy.
	// +required
	Spec ACLPolicySpec `json:"spec"`

	// Observed state reported by the operator: conditions, OpenBao path and ownership.
	// +optional
	Status ACLPolicyStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (p *ACLPolicy) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &p.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (p *ACLPolicy) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &p.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// ACLPolicyList contains a list of ACLPolicy.
type ACLPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ACLPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ACLPolicy{}, &ACLPolicyList{})
}
