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

// AccessNamespaceSelector selects Kubernetes namespaces by label.
type AccessNamespaceSelector struct {
	// Labels a Kubernetes namespace must carry, all of them, for its ServiceAccounts to match; sent to the role as the `bound_service_account_namespace_selector` label selector. Example: {team: payments}
	// +kubebuilder:validation:MinProperties=1
	// +kubebuilder:validation:MaxProperties=16
	// +required
	MatchLabels map[string]string `json:"matchLabels"`
}

// +kubebuilder:validation:XValidation:rule="has(self.__namespace__) != has(self.namespaceSelector)",message="set exactly one of namespace and namespaceSelector"

// A set of Kubernetes ServiceAccounts that log in with one Kubernetes auth role of their own. Set exactly one of namespace and namespaceSelector.
type AccessSubject struct {
	// Name of the ServiceAccount; maps to the role's `bound_service_account_names`. A leading or trailing "*" matches several names. Example: app
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^\*?[a-z0-9.-]*\*?$`
	// +required
	Name string `json:"name"`

	// Kubernetes namespace of the ServiceAccount; maps to the role's `bound_service_account_namespaces`. A leading or trailing "*" matches several namespaces, which makes the subject broad: every rule path must then contain {sa.namespace}. Example: payments
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^\*?[a-z0-9-]*\*?$`
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// Selects the Kubernetes namespaces of the ServiceAccount by label instead of by name; maps to the role's `bound_service_account_namespace_selector`. This makes the subject broad: every rule path must then contain {sa.namespace}.
	// +optional
	NamespaceSelector *AccessNamespaceSelector `json:"namespaceSelector,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="(self.path.contains('{org}') || (has(self.metadataPath) && self.metadataPath.contains('{org}'))) == (has(self.organizations) && size(self.organizations) > 0)",message="a rule lists organizations exactly when its path or metadataPath uses {org}"

// Grants one AccessLevel on a pair of secret paths of a KV version 2 mount. A path is a list of segments separated by "/": a literal, "+" (exactly one segment), or one of the tokens {org}, {sa.namespace} and {sa.name}; the last segment may also be "*" or end with "*".
type AccessRule struct {
	AccessPath `json:",inline"`

	// Name of the AccessLevel, in the same Kubernetes namespace, granted on the paths. Example: reader
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +required
	Level string `json:"level"`

	// Values that {org} takes in path and metadataPath; the rule is rendered once per organization. Set exactly when a path uses {org}. Lowercase letters, digits and "-", at most 40 characters. Example: [acme, globex]
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:items:MaxLength=40
	// +kubebuilder:validation:items:Pattern=`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`
	// +listType=set
	// +optional
	Organizations []string `json:"organizations,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="(has(self.mountPath) ? 1 : 0) + (has(self.mountAccessor) ? 1 : 0) + (has(self.authMountRef) ? 1 : 0) == 1",message="set exactly one of mountPath, mountAccessor and authMountRef"
// +kubebuilder:validation:XValidation:rule="!self.subjects.exists(s, has(s.namespaceSelector) || (has(s.__namespace__) && s.__namespace__.contains('*'))) || self.rules.all(r, r.path.contains('{sa.namespace}') && (!has(r.metadataPath) || r.metadataPath.contains('{sa.namespace}')))",message="a subject with a namespace glob or a namespaceSelector requires {sa.namespace} in every rule path and metadataPath"

// AccessGrantSpec gives Kubernetes ServiceAccounts access to KV version 2 secrets. It writes one ACL policy, grant_<namespace>_<name>, rendered from the rules, and one role per subject on a Kubernetes auth method, grant_<namespace>_<name>_<index>, that issues tokens with that policy.
type AccessGrantSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path of the Kubernetes auth method the roles are created on, without the auth/ prefix. Set exactly one of mountPath, mountAccessor and authMountRef. Example: kubernetes
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// Accessor of the Kubernetes auth method the roles are created on, as listed by sys/auth. Set exactly one of mountPath, mountAccessor and authMountRef. Example: auth_kubernetes_1a2b3c4d
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +optional
	MountAccessor string `json:"mountAccessor,omitempty"`

	// AuthMount resource, in the same Kubernetes namespace, whose Kubernetes auth method the roles are created on; it must be Ready. Set exactly one of mountPath, mountAccessor and authMountRef.
	// +optional
	AuthMountRef *commonv1alpha1.LocalObjectReference `json:"authMountRef,omitempty"`

	// ServiceAccounts that get access, 1 to 8 entries. Each subject gets its own Kubernetes auth role, grant_<namespace>_<name>_<index>, because OpenBao matches the names and namespaces of a role as two independent lists.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=8
	// +required
	Subjects []AccessSubject `json:"subjects"`

	// Secret paths and the level granted on each, 1 to 8 entries, rendered into the ACL policy grant_<namespace>_<name>. In a path, {sa.namespace} and {sa.name} stand for the namespace and name of the ServiceAccount that logged in; OpenBao fills them in per request through identity templating.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=8
	// +required
	Rules []AccessRule `json:"rules"`

	// Lifetime of the tokens the roles issue, as a duration; maps to the role's `token_ttl` (sent in seconds). Default: 15m. Example: 1h
	// +kubebuilder:default="15m"
	// +optional
	TokenTTL *metav1.Duration `json:"tokenTTL,omitempty"`
}

// AccessGrantStatus is the observed state of an AccessGrant.
type AccessGrantStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`

	// Names of the Kubernetes auth roles, one per subject in spec order. A ServiceAccount logs in with the role of its subject. Example: [grant_payments_app-reader_0]
	// +optional
	Roles []string `json:"roles,omitempty"`

	// Every OpenBao object the grant owns: its ACL policy and Kubernetes auth roles. With deletionPolicy Delete, deleting the grant deletes exactly these.
	// +optional
	Children []AccessChild `json:"children,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AccessGrant gives Kubernetes ServiceAccounts access to KV version 2 secrets (the machine access model). It writes one ACL policy, sys/policies/acl/grant_<namespace>_<name>, rendered from rules that cite AccessLevels, and one role per subject on a Kubernetes auth method, auth/<mount>/role/grant_<namespace>_<name>_<index>, that issues tokens with that policy. With the default deletionPolicy Orphan the policy and roles stay when the grant is deleted; with Delete they are removed.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: kubernetes
//	  subjects:
//	    - name: app
//	      namespace: payments
//	  rules:
//	    - mount: secret
//	      path: payments/{sa.name}/*
//	      level: reader
type AccessGrant struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// Desired state: the auth method, the ServiceAccount subjects and the secret paths they get access to.
	// +required
	Spec AccessGrantSpec `json:"spec"`

	// Observed state reported by the operator: conditions, the role names to log in with and the owned OpenBao objects.
	// +optional
	Status AccessGrantStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (g *AccessGrant) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &g.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (g *AccessGrant) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &g.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// AccessGrantList contains a list of AccessGrant.
type AccessGrantList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []AccessGrant `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AccessGrant{}, &AccessGrantList{})
}
