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

// +kubebuilder:validation:XValidation:rule="self.mountPath == oldSelf.mountPath",message="mountPath is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.name) == has(oldSelf.name) && (!has(self.name) || self.name == oldSelf.name)",message="name is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.allowedKubernetesNamespaces) || has(self.allowedKubernetesNamespaceSelector)",message="set allowedKubernetesNamespaces or allowedKubernetesNamespaceSelector"
// +kubebuilder:validation:XValidation:rule="[has(self.serviceAccountName), has(self.kubernetesRoleName), has(self.generatedRoleRules)].filter(x, x).size() == 1",message="set exactly one of serviceAccountName, kubernetesRoleName and generatedRoleRules"
// +kubebuilder:validation:XValidation:rule="!has(self.tokenDefaultTTL) || !has(self.tokenMaxTTL) || duration(self.tokenMaxTTL) == duration('0s') || duration(self.tokenDefaultTTL) <= duration(self.tokenMaxTTL)",message="tokenDefaultTTL must not exceed tokenMaxTTL"

// spec is the desired role of a Kubernetes secrets engine at `<mountPath>/roles/<name>`. A role issues a token for an existing service account (serviceAccountName), binds a generated service account to an existing Role or ClusterRole (kubernetesRoleName), or generates the whole chain (generatedRoleRules); set exactly one.
type KubernetesSecretsRoleSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path of the Kubernetes secrets engine mount, without leading or trailing slashes. Immutable. Example: `kubernetes`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +required
	MountPath string `json:"mountPath"`

	// name is the role name in OpenBao, lower case. Defaults to metadata.name. Immutable. Example: `ci-deployer`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[a-z0-9_]([a-z0-9_.-]*[a-z0-9_])?$`
	// +optional
	Name string `json:"name,omitempty"`

	// allowedKubernetesNamespaces lists the namespaces credentials may be generated in (`allowed_kubernetes_namespaces`); `*` allows all. Set this or allowedKubernetesNamespaceSelector. Up to 256. Example: `[apps-dev]`.
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:items:MaxLength=63
	// +listType=set
	// +optional
	AllowedKubernetesNamespaces []string `json:"allowedKubernetesNamespaces,omitempty"`

	// allowedKubernetesNamespaceSelector is a label selector, as JSON or YAML, for the namespaces credentials may be generated in (`allowed_kubernetes_namespace_selector`). With allowedKubernetesNamespaces both conditions must hold. Example: `{"matchLabels": {"team": "billing"}}`.
	// +kubebuilder:validation:MaxLength=8192
	// +optional
	AllowedKubernetesNamespaceSelector string `json:"allowedKubernetesNamespaceSelector,omitempty"`

	// tokenDefaultTTL is the default lifetime of generated tokens (`token_default_ttl`), a duration. Must not exceed tokenMaxTTL. Defaults to the mount's default lease TTL. Example: `1h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	TokenDefaultTTL *metav1.Duration `json:"tokenDefaultTTL,omitempty"`

	// tokenMaxTTL is the maximum lifetime of generated tokens (`token_max_ttl`), a duration. Defaults to the mount's max lease TTL. Example: `24h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	TokenMaxTTL *metav1.Duration `json:"tokenMaxTTL,omitempty"`

	// tokenDefaultAudiences are the default audiences of generated tokens (`token_default_audiences`). Empty uses the cluster's default audience. Up to 32. Example: `[https://kubernetes.default.svc]`.
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:items:MaxLength=256
	// +listType=set
	// +optional
	TokenDefaultAudiences []string `json:"tokenDefaultAudiences,omitempty"`

	// serviceAccountName is an existing service account to generate tokens for (`service_account_name`). Example: `deployer`.
	// +kubebuilder:validation:MaxLength=253
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// kubernetesRoleName is an existing Role or ClusterRole that generated service accounts are bound to (`kubernetes_role_name`). Example: `edit`.
	// +kubebuilder:validation:MaxLength=253
	// +optional
	KubernetesRoleName string `json:"kubernetesRoleName,omitempty"`

	// kubernetesRoleType is the kind of the referenced or generated role (`kubernetes_role_type`). Allowed: Role, ClusterRole. Defaults to Role.
	// +kubebuilder:validation:Enum=Role;ClusterRole
	// +optional
	KubernetesRoleType string `json:"kubernetesRoleType,omitempty"`

	// generatedRoleRules are the rules, as JSON or YAML, of the Role or ClusterRole that OpenBao generates (`generated_role_rules`). Example: `{"rules": [{"apiGroups": [""], "resources": ["pods"], "verbs": ["list"]}]}`.
	// +kubebuilder:validation:MaxLength=65536
	// +optional
	GeneratedRoleRules string `json:"generatedRoleRules,omitempty"`

	// nameTemplate is the template for the names of generated objects (`name_template`). Example: `{{.RoleName}}-{{random 8}}`.
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	NameTemplate string `json:"nameTemplate,omitempty"`

	// extraLabels are added to every generated object (`extra_labels`). Up to 64. Example: `{managed-by: openbao}`.
	// +kubebuilder:validation:MaxProperties=64
	// +optional
	ExtraLabels map[string]string `json:"extraLabels,omitempty"`

	// extraAnnotations are added to every generated object (`extra_annotations`). Up to 64. Example: `{owner: billing}`.
	// +kubebuilder:validation:MaxProperties=64
	// +optional
	ExtraAnnotations map[string]string `json:"extraAnnotations,omitempty"`
}

// status is the observed state of the Kubernetes secrets role.
type KubernetesSecretsRoleStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// KubernetesSecretsRole manages a role of a Kubernetes secrets engine at `<mount>/roles/<name>`, from which OpenBao issues short-lived Kubernetes service account tokens (`<mount>/creds/<name>`). Use it to hand out scoped cluster access without long-lived tokens.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: kubernetes
//	  name: ci-deployer
//	  allowedKubernetesNamespaces: [apps-dev]
//	  kubernetesRoleName: edit
//	  kubernetesRoleType: ClusterRole
type KubernetesSecretsRole struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec KubernetesSecretsRoleSpec `json:"spec"`

	// +optional
	Status KubernetesSecretsRoleStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (r *KubernetesSecretsRole) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &r.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (r *KubernetesSecretsRole) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &r.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// KubernetesSecretsRoleList contains a list of KubernetesSecretsRole.
type KubernetesSecretsRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []KubernetesSecretsRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&KubernetesSecretsRole{}, &KubernetesSecretsRoleList{})
}
