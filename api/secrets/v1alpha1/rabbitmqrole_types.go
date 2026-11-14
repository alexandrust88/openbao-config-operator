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

// Permission regular expressions of a generated user on one virtual host.
type RabbitMQVHostPermission struct {
	// configure is the regular expression of resources the user may configure. Example: `^app\..*`.
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	Configure string `json:"configure,omitempty"`

	// write is the regular expression of resources the user may write to. Example: `.*`.
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	Write string `json:"write,omitempty"`

	// read is the regular expression of resources the user may read from. Example: `.*`.
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	Read string `json:"read,omitempty"`
}

// Topic permission regular expressions of a generated user on one exchange.
type RabbitMQTopicPermission struct {
	// write is the regular expression of routing keys the user may publish with. Example: `^orders\..*`.
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	Write string `json:"write,omitempty"`

	// read is the regular expression of routing keys the user may consume with. Example: `.*`.
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	Read string `json:"read,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="self.mountPath == oldSelf.mountPath",message="mountPath is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.name) == has(oldSelf.name) && (!has(self.name) || self.name == oldSelf.name)",message="name is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.tags) || has(self.vhosts)",message="set tags or vhosts"

// spec is the desired role of a RabbitMQ secrets engine at `<mountPath>/roles/<name>`. Set tags or vhosts.
type RabbitMQRoleSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path of the RabbitMQ secrets engine mount, without leading or trailing slashes. Immutable. Example: `rabbitmq`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +required
	MountPath string `json:"mountPath"`

	// name is the role name in OpenBao. Defaults to metadata.name. Immutable. Example: `orders-app`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^\w([\w.-]*\w)?$`
	// +optional
	Name string `json:"name,omitempty"`

	// tags are the RabbitMQ user tags of generated users (`tags`, comma-joined). Up to 32. Example: `[management]`.
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=64
	// +kubebuilder:validation:items:Pattern=`^[^,]+$`
	// +optional
	Tags []string `json:"tags,omitempty"`

	// vhosts maps virtual host names to the permissions of generated users (`vhosts`, written as JSON). Up to 64. Example: `{/: {configure: ".*", write: ".*", read: ".*"}}`.
	// +kubebuilder:validation:MaxProperties=64
	// +optional
	VHosts map[string]RabbitMQVHostPermission `json:"vhosts,omitempty"`

	// vhostTopics maps virtual host names to exchange names to the topic permissions of generated users (`vhost_topics`, written as JSON). Up to 64. Example: `{/: {amq.topic: {write: ".*", read: ".*"}}}`.
	// +kubebuilder:validation:MaxProperties=64
	// +optional
	VHostTopics map[string]map[string]RabbitMQTopicPermission `json:"vhostTopics,omitempty"`
}

// status is the observed state of the RabbitMQ role.
type RabbitMQRoleStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// RabbitMQRole manages a role of a RabbitMQ secrets engine at `<mount>/roles/<name>`: the tags and virtual host permissions of the dynamic users issued from `<mount>/creds/<name>`.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: rabbitmq
//	  name: orders-app
//	  vhosts:
//	    /: {configure: ".*", write: ".*", read: ".*"}
type RabbitMQRole struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec RabbitMQRoleSpec `json:"spec"`

	// +optional
	Status RabbitMQRoleStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (r *RabbitMQRole) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &r.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (r *RabbitMQRole) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &r.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// RabbitMQRoleList contains a list of RabbitMQRole.
type RabbitMQRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []RabbitMQRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RabbitMQRole{}, &RabbitMQRoleList{})
}
