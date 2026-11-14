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
// +kubebuilder:validation:XValidation:rule="!has(self.deletionPolicy) || self.deletionPolicy == 'Orphan'",message="the RabbitMQ configuration cannot be deleted; deletionPolicy must be Orphan"

// spec is the desired connection (`<mountPath>/config/connection`) and lease settings (`<mountPath>/config/lease`) of a RabbitMQ secrets engine. OpenBao cannot read the connection back, so connection settings are written when they change in the spec and are not checked for drift; lease settings are. deletionPolicy must be Orphan.
type RabbitMQConfigSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path of the RabbitMQ secrets engine mount, without leading or trailing slashes. Immutable. Example: `rabbitmq`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +required
	MountPath string `json:"mountPath"`

	// connectionURI is the URI of the RabbitMQ management API (`connection_uri`). Example: `http://rabbitmq.messaging.svc:15672`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="!self.matches('://[^/?#]*@')",message="connectionURI must not contain credentials (user:password@ or token@): use the secret reference fields"
	// +kubebuilder:validation:MaxLength=2048
	// +required
	ConnectionURI string `json:"connectionURI"`

	// username is a RabbitMQ management administrator (`username`). Example: `openbao`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +required
	Username string `json:"username"`

	// passwordSecretRef selects the Secret key holding the administrator password (`password`). Read from a Secret key; never stored in status. Write-only: a change of the Secret is written again. Example: `{name: rabbitmq-admin, key: password}`.
	// +required
	PasswordSecretRef commonv1alpha1.SecretKeySelector `json:"passwordSecretRef"`

	// verifyConnection makes OpenBao call the management API on every write (`verify_connection`). Defaults to true.
	// +optional
	VerifyConnection *bool `json:"verifyConnection,omitempty"`

	// passwordPolicy is the OpenBao password policy that generates passwords of dynamic users (`password_policy`). Empty uses the engine default. Example: `rabbitmq`.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	PasswordPolicy string `json:"passwordPolicy,omitempty"`

	// usernameTemplate is the template for the names of dynamic users (`username_template`). Example: `{{.RoleName}}-{{random 8}}`.
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	UsernameTemplate string `json:"usernameTemplate,omitempty"`

	// lease sets the lease durations of issued credentials (`<mountPath>/config/lease`). An omitted duration, or an omitted lease, is written as 0 (the system default).
	// +optional
	Lease *RabbitMQLease `json:"lease,omitempty"`
}

// Lease durations of a RabbitMQ secrets engine.
type RabbitMQLease struct {
	// ttl is the lease duration of issued credentials (`ttl`), a duration. 0 uses the system default. Example: `1h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	TTL *metav1.Duration `json:"ttl,omitempty"`

	// maxTTL is the maximum lease duration of issued credentials (`max_ttl`), a duration. 0 uses the system default. Example: `24h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	MaxTTL *metav1.Duration `json:"maxTTL,omitempty"`
}

// status is the observed state of the RabbitMQ configuration.
type RabbitMQConfigStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// RabbitMQConfig sets the connection (`<mount>/config/connection`) and lease settings (`<mount>/config/lease`) of a RabbitMQ secrets engine, which issues dynamic RabbitMQ users. A singleton per mount; it is never deleted.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: rabbitmq
//	  connectionURI: http://rabbitmq.messaging.svc:15672
//	  username: openbao
//	  passwordSecretRef: {name: rabbitmq-admin, key: password}
type RabbitMQConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec RabbitMQConfigSpec `json:"spec"`

	// +optional
	Status RabbitMQConfigStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (c *RabbitMQConfig) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &c.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (c *RabbitMQConfig) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &c.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// RabbitMQConfigList contains a list of RabbitMQConfig.
type RabbitMQConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []RabbitMQConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RabbitMQConfig{}, &RabbitMQConfigList{})
}
