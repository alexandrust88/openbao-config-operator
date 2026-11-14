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
// +kubebuilder:validation:XValidation:rule="!has(self.deletionPolicy) || self.deletionPolicy == 'Orphan'",message="deletionPolicy must be Orphan: an auth method config has no delete operation"

// Desired state of the GCP auth method configuration, written to auth/<mountPath>/config. deletionPolicy must be Orphan because the config has no delete operation.
type GCPAuthConfigSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path of the GCP auth method, without the auth/ prefix. Defaults to gcp. Immutable. Example: gcp/prod
	// +kubebuilder:default=gcp
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// Secret key holding the JSON key file of the Google service account OpenBao uses to verify logins. Read from this Secret key; the value is never stored in status, is write-only in OpenBao and is tracked by a hash, so a change of the Secret is written again. Unset makes OpenBao use the application default credentials of its environment. Maps to `credentials`. Example: {name: gcp-auth-sa, key: credentials.json}
	// +optional
	CredentialsSecretRef *commonv1alpha1.SecretKeySelector `json:"credentialsSecretRef,omitempty"`

	// Value used as the entity alias name of iam logins: role_id (the role's unique ID) or unique_id (the service account's unique ID). OpenBao defaults to role_id. Maps to `iam_alias`. Example: unique_id
	// +kubebuilder:validation:Enum=role_id;unique_id
	// +optional
	IAMAlias string `json:"iamAlias,omitempty"`

	// Metadata fields added to tokens and audit logs from iam logins, listed explicitly (the value default is rejected because OpenBao reads it back expanded). Unset keeps the server's value, which starts as the plugin's default set. Maps to `iam_metadata`. Example: [project_id, role, service_account_email]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:XValidation:rule="!self.exists(m, m == 'default')",message="list the metadata fields explicitly instead of default, which OpenBao reads back as the expanded list"
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	IAMMetadata []string `json:"iamMetadata,omitempty"`

	// Value used as the entity alias name of gce logins: role_id (the role's unique ID) or instance_id (the instance's unique ID). OpenBao defaults to role_id. Maps to `gce_alias`. Example: instance_id
	// +kubebuilder:validation:Enum=role_id;instance_id
	// +optional
	GCEAlias string `json:"gceAlias,omitempty"`

	// Instance metadata fields added to tokens and audit logs from gce logins, listed explicitly (the value default is rejected because OpenBao reads it back expanded). Unset keeps the server's value, which starts as the plugin's default set. Maps to `gce_metadata`. Example: [instance_name, project_id, zone]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:XValidation:rule="!self.exists(m, m == 'default')",message="list the metadata fields explicitly instead of default, which OpenBao reads back as the expanded list"
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	GCEMetadata []string `json:"gceMetadata,omitempty"`

	// Overrides for the Google API endpoints OpenBao calls, for example for Private Google Access. Unset uses the public endpoints. Maps to `custom_endpoint`. Example: {api: "https://www.googleapis.com", iam: "https://iam.googleapis.com"}
	// +optional
	CustomEndpoint *GCPAuthCustomEndpoint `json:"customEndpoint,omitempty"`
}

// Overrides for Google API endpoints; each value has the form scheme://host:port. Unset entries use the public endpoint.
type GCPAuthCustomEndpoint struct {
	// Replacement for the Google API endpoint https://www.googleapis.com. Maps to `custom_endpoint.api`. Example: https://private.googleapis.com
	// +kubebuilder:validation:MaxLength=2048
	// +optional
	API string `json:"api,omitempty"`

	// Replacement for the IAM endpoint https://iam.googleapis.com. Maps to `custom_endpoint.iam`. Example: https://iam-private.example.com:443
	// +kubebuilder:validation:MaxLength=2048
	// +optional
	IAM string `json:"iam,omitempty"`

	// Replacement for the Cloud Resource Manager endpoint https://cloudresourcemanager.googleapis.com. Maps to `custom_endpoint.crm`. Example: https://crm-private.example.com:443
	// +kubebuilder:validation:MaxLength=2048
	// +optional
	CRM string `json:"crm,omitempty"`

	// Replacement for the Compute Engine endpoint https://compute.googleapis.com. Maps to `custom_endpoint.compute`. Example: https://compute-private.example.com:443
	// +kubebuilder:validation:MaxLength=2048
	// +optional
	Compute string `json:"compute,omitempty"`
}

// Observed state of the GCP auth method configuration, written by the operator.
type GCPAuthConfigStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// GCPAuthConfig manages the configuration of an OpenBao GCP auth method at auth/<mountPath>/config. Use it to set the Google credentials and alias/metadata settings that GCP iam and gce logins use. Needs the gcp auth plugin registered and enabled at auth/<mountPath>; there is one config per mount and it is never deleted.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: gcp
//	  credentialsSecretRef:
//	    name: gcp-auth-sa
//	    key: credentials.json
//	  iamAlias: unique_id
type GCPAuthConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec GCPAuthConfigSpec `json:"spec"`

	// +optional
	Status GCPAuthConfigStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *GCPAuthConfig) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &o.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *GCPAuthConfig) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// GCPAuthConfigList contains a list of GCPAuthConfig.
type GCPAuthConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GCPAuthConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GCPAuthConfig{}, &GCPAuthConfigList{})
}
