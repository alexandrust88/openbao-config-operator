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
// +kubebuilder:validation:XValidation:rule="has(self.consulPolicies) || has(self.consulRoles) || has(self.serviceIdentities) || has(self.nodeIdentities)",message="set at least one of consulPolicies, consulRoles, serviceIdentities and nodeIdentities"

// spec is the desired role of a Consul secrets engine at `<mountPath>/roles/<name>`. Set at least one of consulPolicies, consulRoles, serviceIdentities and nodeIdentities.
type ConsulSecretsRoleSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path of the Consul secrets engine mount. Defaults to `consul`. Immutable. Example: `consul`.
	// +kubebuilder:default=consul
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// name is the role name in OpenBao. Defaults to metadata.name. Immutable. Example: `web`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+$`
	// +optional
	Name string `json:"name,omitempty"`

	// consulPolicies are the Consul ACL policies attached to generated tokens (`consul_policies`). Example: `[web-read]`.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=128
	// +listType=set
	// +optional
	ConsulPolicies []string `json:"consulPolicies,omitempty"`

	// consulRoles are the Consul ACL roles attached to generated tokens (`consul_roles`). Example: `[web]`.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=256
	// +listType=set
	// +optional
	ConsulRoles []string `json:"consulRoles,omitempty"`

	// serviceIdentities are Consul service identities, as `<service>` or `<service>:<dc1>,<dc2>` (`service_identities`). Example: `["web:dc1"]`.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	ServiceIdentities []string `json:"serviceIdentities,omitempty"`

	// nodeIdentities are Consul node identities, as `<node>:<datacenter>` (`node_identities`). Example: `["node-1:dc1"]`.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	NodeIdentities []string `json:"nodeIdentities,omitempty"`

	// consulNamespace is the Consul Enterprise namespace of generated tokens (`consul_namespace`). Example: `billing`.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	ConsulNamespace string `json:"consulNamespace,omitempty"`

	// partition is the Consul Enterprise admin partition of generated tokens (`partition`). Example: `default`.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	Partition string `json:"partition,omitempty"`

	// local makes generated tokens local to the current datacenter instead of replicated globally (`local`). Defaults to false.
	// +optional
	Local *bool `json:"local,omitempty"`

	// ttl is the default lease duration of generated tokens (`ttl`), a duration. Defaults to the mount's default lease TTL. Example: `1h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	TTL *metav1.Duration `json:"ttl,omitempty"`

	// maxTTL is the maximum lease duration of generated tokens (`max_ttl`), a duration. Defaults to the mount's max lease TTL. Example: `24h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	MaxTTL *metav1.Duration `json:"maxTTL,omitempty"`
}

// status is the observed state of the Consul secrets role.
type ConsulSecretsRoleStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-secrets
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ConsulSecretsRole manages a role of a Consul secrets engine (external plugin) at `<mount>/roles/<name>`: the policies, roles and identities of the Consul ACL tokens issued from `<mount>/creds/<name>`.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: consul
//	  name: web
//	  consulPolicies: [web-read]
//	  ttl: 1h
type ConsulSecretsRole struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec ConsulSecretsRoleSpec `json:"spec"`

	// +optional
	Status ConsulSecretsRoleStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *ConsulSecretsRole) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &o.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *ConsulSecretsRole) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// ConsulSecretsRoleList contains a list of ConsulSecretsRole.
type ConsulSecretsRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ConsulSecretsRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ConsulSecretsRole{}, &ConsulSecretsRoleList{})
}
