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

// Desired state of the Okta auth method configuration, written to auth/<mountPath>/config. deletionPolicy must be Orphan because the config endpoint cannot be deleted.
type OktaAuthConfigSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path of the Okta auth method, without the auth/ prefix. Defaults to okta. Immutable. Example: okta/example
	// +kubebuilder:default=okta
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// Okta organization name, the first part of https://<orgName>.okta.com. Required. Maps to `org_name`. Example: example
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +required
	OrgName string `json:"orgName"`

	// Secret key holding an Okta API token, needed to read the Okta groups of a user; without it OpenBao only checks credentials. Read from this Secret key; the value is never stored in status, is write-only in OpenBao and is tracked by a hash, so a change of the Secret is written again. Maps to `api_token`. Example: {name: okta-api, key: token}
	// +optional
	APITokenSecretRef *commonv1alpha1.SecretKeySelector `json:"apiTokenSecretRef,omitempty"`

	// Okta domain of the organization. OpenBao defaults to okta.com. Maps to `base_url`. Example: oktapreview.com
	// +kubebuilder:validation:MaxLength=256
	// +optional
	BaseURL string `json:"baseURL,omitempty"`

	// When true, OpenBao skips the MFA policy of the Okta organization at login; this also skips some account status checks. Defaults to false. Maps to `bypass_okta_mfa`. Example: false
	// +optional
	BypassOktaMFA *bool `json:"bypassOktaMFA,omitempty"`

	TokenFields `json:",inline"`
}

// Observed state of the Okta auth method configuration, written by the operator.
type OktaAuthConfigStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// OktaAuthConfig manages the configuration of an OpenBao Okta auth method at auth/<mountPath>/config. Use it to connect the method to an Okta organization so users log in with their Okta credentials, and to set the token settings of those logins. Needs the okta auth plugin enabled at auth/<mountPath>; there is one config per mount and it is never deleted.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  orgName: example
//	  baseURL: okta.com
//	  apiTokenSecretRef:
//	    name: okta-api
//	    key: token
type OktaAuthConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec OktaAuthConfigSpec `json:"spec"`

	// +optional
	Status OktaAuthConfigStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *OktaAuthConfig) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &o.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *OktaAuthConfig) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// OktaAuthConfigList contains a list of OktaAuthConfig.
type OktaAuthConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []OktaAuthConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&OktaAuthConfig{}, &OktaAuthConfigList{})
}
