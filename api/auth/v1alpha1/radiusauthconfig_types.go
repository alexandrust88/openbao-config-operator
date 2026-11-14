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
// +kubebuilder:validation:XValidation:rule="!has(self.deletionPolicy) || self.deletionPolicy == 'Orphan'",message="deletionPolicy must be Orphan: the config endpoint cannot be deleted"

// Desired state of the RADIUS auth method configuration at auth/<mountPath>/config. deletionPolicy must be Orphan.
type RADIUSAuthConfigSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path of the RADIUS auth method, without the auth/ prefix. Defaults to radius. Immutable. Example: radius
	// +kubebuilder:default=radius
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// Host name or IP address of the RADIUS server. Required. Maps to `host`. Example: radius.example.com
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +required
	Host string `json:"host"`

	// UDP port of the RADIUS server, 1 to 65535. OpenBao defaults to 1812. Maps to `port`. Example: 1812
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +optional
	Port *int32 `json:"port,omitempty"`

	// Secret key holding the RADIUS shared secret. Read from this Secret key; the value is never stored in status. Write-only: only a hash is kept in status.writeOnlyHash. Required. Maps to `secret`. Example: {name: radius, key: secret}
	// +required
	SecretSecretRef commonv1alpha1.SecretKeySelector `json:"secretSecretRef"`

	// Policies attached to tokens of users that authenticate but have no RADIUSAuthUser. Lower case names. Maps to `unregistered_user_policies`. Example: [guest]
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=128
	// +kubebuilder:validation:items:Pattern=`^[^A-Z,\s]+$`
	// +listType=set
	// +optional
	UnregisteredUserPolicies []string `json:"unregisteredUserPolicies,omitempty"`

	// Time to wait for a connection to the RADIUS server, as a duration (sent in seconds). OpenBao defaults to 10s. Maps to `dial_timeout`. Example: 5s
	// +optional
	DialTimeout *metav1.Duration `json:"dialTimeout,omitempty"`

	// Time to wait for a response from the RADIUS server, as a duration (sent in seconds). OpenBao defaults to 10s. Maps to `read_timeout`. Example: 5s
	// +optional
	ReadTimeout *metav1.Duration `json:"readTimeout,omitempty"`

	// NAS-Port attribute sent in RADIUS requests, 0 to 65535. OpenBao defaults to 10. Maps to `nas_port`. Example: 10
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=65535
	// +optional
	NASPort *int32 `json:"nasPort,omitempty"`

	// NAS-Identifier attribute sent in RADIUS requests. Unset sends none. Maps to `nas_identifier`. Example: openbao
	// +kubebuilder:validation:MaxLength=253
	// +optional
	NASIdentifier string `json:"nasIdentifier,omitempty"`

	TokenFields `json:",inline"`
}

// Observed state of the configuration, written by the operator. The shared secret never appears here.
type RADIUSAuthConfigStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// RADIUSAuthConfig manages the configuration of an OpenBao RADIUS auth method at auth/<mountPath>/config. Needs the RADIUS auth plugin. Use it to let users log in with credentials checked by a RADIUS server; map users to policies with RADIUSAuthUser.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  deletionPolicy: Orphan
//	  host: radius.example.com
//	  secretSecretRef:
//	    name: radius
//	    key: secret
//	  unregisteredUserPolicies: [guest]
type RADIUSAuthConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec RADIUSAuthConfigSpec `json:"spec"`

	// +optional
	Status RADIUSAuthConfigStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *RADIUSAuthConfig) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &o.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *RADIUSAuthConfig) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// RADIUSAuthConfigList contains a list of RADIUSAuthConfig.
type RADIUSAuthConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []RADIUSAuthConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RADIUSAuthConfig{}, &RADIUSAuthConfigList{})
}
