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

// AliasNameSource selects the value OpenBao uses as the entity alias name of a Kubernetes login.
// +kubebuilder:validation:Enum=serviceaccount_uid;serviceaccount_name
type AliasNameSource string

const (
	AliasNameSourceServiceAccountUID  AliasNameSource = "serviceaccount_uid"
	AliasNameSourceServiceAccountName AliasNameSource = "serviceaccount_name"
)

// NamespaceLabelSelector selects Kubernetes namespaces by their labels. OpenBao supports matchLabels only.
// +kubebuilder:validation:XValidation:rule="!has(self.matchExpressions) || size(self.matchExpressions) == 0",message="matchExpressions is not supported by OpenBao; use matchLabels"
type NamespaceLabelSelector struct {
	// Labels a namespace must carry, all of them, to match. Required, at least one. Example: {team: a}
	// +kubebuilder:validation:MinProperties=1
	// +kubebuilder:validation:MaxProperties=64
	// +required
	MatchLabels map[string]string `json:"matchLabels"`

	// Not supported by OpenBao: a selector that sets it is rejected. Use matchLabels.
	// +optional
	MatchExpressions []metav1.LabelSelectorRequirement `json:"matchExpressions,omitempty"`
}

// Desired state of a Kubernetes auth role, written to auth/<mountPath>/role/<name>. At least one of boundServiceAccountNamespaces and boundServiceAccountNamespaceSelector is required.
//
// +kubebuilder:validation:XValidation:rule="has(self.mountPath) == has(oldSelf.mountPath) && (!has(self.mountPath) || self.mountPath == oldSelf.mountPath)",message="mountPath is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.name) == has(oldSelf.name) && (!has(self.name) || self.name == oldSelf.name)",message="name is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.boundServiceAccountNamespaces) || has(self.boundServiceAccountNamespaceSelector)",message="set boundServiceAccountNamespaces, boundServiceAccountNamespaceSelector or both"
type KubernetesAuthRoleSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path of the Kubernetes auth method, without the auth/ prefix. Defaults to kubernetes. Immutable. Example: kubernetes/prod-cluster
	// +kubebuilder:default=kubernetes
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// Name of the role in OpenBao, used in the login request. Defaults to metadata.name. Immutable. Example: app
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[a-z0-9_]([a-z0-9_.-]*[a-z0-9_])?$`
	// +optional
	Name string `json:"name,omitempty"`

	// ServiceAccount names allowed to log in with this role. "*" alone allows every name and cannot be combined with other names. Required. Maps to `bound_service_account_names`. Example: [app, worker]
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="size(self) == 1 || !self.exists(n, n == '*')",message="'*' cannot be combined with other names"
	// +listType=set
	// +required
	BoundServiceAccountNames []string `json:"boundServiceAccountNames"`

	// Kubernetes namespaces allowed to log in with this role. "*" alone allows every namespace and cannot be combined with others. A login is allowed when its namespace is listed here or matches boundServiceAccountNamespaceSelector. Maps to `bound_service_account_namespaces`. Example: [team-a, team-b]
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=63
	// +kubebuilder:validation:XValidation:rule="size(self) == 1 || !self.exists(n, n == '*')",message="'*' cannot be combined with other namespaces"
	// +listType=set
	// +optional
	BoundServiceAccountNamespaces []string `json:"boundServiceAccountNamespaces,omitempty"`

	// Label selector for the Kubernetes namespaces allowed to log in, evaluated by OpenBao at login time. OpenBao supports matchLabels only. Maps to `bound_service_account_namespace_selector` (sent as JSON). Example: {matchLabels: {team: a}}
	// +optional
	BoundServiceAccountNamespaceSelector *NamespaceLabelSelector `json:"boundServiceAccountNamespaceSelector,omitempty"`

	// Audience claim the ServiceAccount JWT must carry. Unset skips the audience check. Maps to `audience`. Example: openbao
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	// +optional
	Audience string `json:"audience,omitempty"`

	// Value used as the entity alias name of a login: serviceaccount_uid (the ServiceAccount UID) or serviceaccount_name (<namespace>/<name>). OpenBao defaults to serviceaccount_uid. Maps to `alias_name_source`. Example: serviceaccount_name
	// +optional
	AliasNameSource AliasNameSource `json:"aliasNameSource,omitempty"`

	TokenFields `json:",inline"`
}

// Observed state of the role, written by the operator.
type KubernetesAuthRoleStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=baok8sr,categories=obco;obco-auth
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// KubernetesAuthRole manages a role of an OpenBao Kubernetes auth method at auth/<mountPath>/role/<name>. Use it to let pods log in with their ServiceAccount token and receive an OpenBao token with the listed policies.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: kubernetes
//	  boundServiceAccountNames: [app]
//	  boundServiceAccountNamespaces: [team-a]
//	  tokenPolicies: [app-read]
//	  tokenTTL: 1h
type KubernetesAuthRole struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec KubernetesAuthRoleSpec `json:"spec"`

	// +optional
	Status KubernetesAuthRoleStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (k *KubernetesAuthRole) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &k.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (k *KubernetesAuthRole) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &k.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// KubernetesAuthRoleList contains a list of KubernetesAuthRole.
type KubernetesAuthRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []KubernetesAuthRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&KubernetesAuthRole{}, &KubernetesAuthRoleList{})
}
