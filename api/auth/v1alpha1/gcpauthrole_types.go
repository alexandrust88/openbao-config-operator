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
// +kubebuilder:validation:XValidation:rule="self.type == oldSelf.type",message="type is immutable"
// +kubebuilder:validation:XValidation:rule="self.type != 'iam' || has(self.boundServiceAccounts)",message="an iam role requires boundServiceAccounts"
// +kubebuilder:validation:XValidation:rule="self.type == 'iam' || (!has(self.maxJWTExp) && !has(self.allowGCEInference))",message="maxJWTExp and allowGCEInference apply only to type iam"
// +kubebuilder:validation:XValidation:rule="self.type == 'gce' || (!has(self.boundZones) && !has(self.boundRegions) && !has(self.boundInstanceGroups) && !has(self.boundLabels))",message="boundZones, boundRegions, boundInstanceGroups and boundLabels apply only to type gce"

// Desired state of a GCP auth role, written to auth/<mountPath>/role/<name>. An iam role requires boundServiceAccounts; maxJWTExp and allowGCEInference apply only to iam roles; boundZones, boundRegions, boundInstanceGroups and boundLabels apply only to gce roles.
type GCPAuthRoleSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path of the GCP auth method, without the auth/ prefix. Defaults to gcp. Immutable. Example: gcp/prod
	// +kubebuilder:default=gcp
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// Name of the role in OpenBao, used in the login request. Defaults to metadata.name. Immutable. Example: app
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_]([A-Za-z0-9_.-]*[A-Za-z0-9_])?$`
	// +optional
	Name string `json:"name,omitempty"`

	// Login type the role allows: iam (signed service account JWTs) or gce (Compute Engine instance identity tokens). Required. Immutable. Maps to `type`. Example: iam
	// +kubebuilder:validation:Enum=iam;gce
	// +kubebuilder:validation:MaxLength=3
	// +required
	Type string `json:"type"`

	// Service account emails or unique IDs allowed to log in; "*" allows any service account. Required for type iam; for gce it restricts the instance's service account. Maps to `bound_service_accounts`. Example: [app@my-project.iam.gserviceaccount.com]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	BoundServiceAccounts []string `json:"boundServiceAccounts,omitempty"`

	// Google Cloud project IDs whose service accounts or instances may log in. Unset allows any project. Maps to `bound_projects`. Example: [my-project]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	BoundProjects []string `json:"boundProjects,omitempty"`

	// When true, logins get group aliases for the project, folders and organization of the service account or instance. Defaults to false. Maps to `add_group_aliases`. Example: true
	// +optional
	AddGroupAliases *bool `json:"addGroupAliases,omitempty"`

	// Maximum time from login until the expiry of the JWT an iam login presents, as a duration; a JWT that expires later is rejected. iam roles only. OpenBao defaults to 15m. Maps to `max_jwt_exp`. Example: 30m
	// +optional
	MaxJWTExp *metav1.Duration `json:"maxJWTExp,omitempty"`

	// When true, Compute Engine instances can log in to this iam role with the service account inferred from their instance identity token. iam roles only. Defaults to false. Maps to `allow_gce_inference`. Example: true
	// +optional
	AllowGCEInference *bool `json:"allowGCEInference,omitempty"`

	// Zones a Compute Engine instance must run in to log in; with boundInstanceGroups the groups are treated as zonal. gce roles only. Maps to `bound_zones`. Example: [europe-west3-a, europe-west3-b]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	BoundZones []string `json:"boundZones,omitempty"`

	// Regions a Compute Engine instance must run in to log in; ignored when boundZones is set. gce roles only. Maps to `bound_regions`. Example: [europe-west3]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	BoundRegions []string `json:"boundRegions,omitempty"`

	// Instance groups a Compute Engine instance must belong to; requires boundZones or boundRegions. gce roles only. Maps to `bound_instance_groups`. Example: [web-servers]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	BoundInstanceGroups []string `json:"boundInstanceGroups,omitempty"`

	// Labels a Compute Engine instance must carry, all of them, as key/value pairs (sent as "key:value" strings). GCP labels are not access controlled, so combine them with other bounds. gce roles only. Maps to `bound_labels`. Example: {env: prod, team: payments}
	// +kubebuilder:validation:MaxProperties=64
	// +optional
	BoundLabels map[string]string `json:"boundLabels,omitempty"`

	TokenFields `json:",inline"`
}

// Observed state of the role, written by the operator.
type GCPAuthRoleStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// GCPAuthRole manages a role of an OpenBao GCP auth method at auth/<mountPath>/role/<name>. Use it to let Google service accounts (type iam) or Compute Engine instances (type gce) log in and receive tokens with the listed policies. Needs the gcp auth plugin registered and enabled at auth/<mountPath>.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  type: iam
//	  boundServiceAccounts: [app@my-project.iam.gserviceaccount.com]
//	  boundProjects: [my-project]
//	  tokenPolicies: [app-read]
//	  tokenTTL: 1h
type GCPAuthRole struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec GCPAuthRoleSpec `json:"spec"`

	// +optional
	Status GCPAuthRoleStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *GCPAuthRole) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &o.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *GCPAuthRole) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// GCPAuthRoleList contains a list of GCPAuthRole.
type GCPAuthRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GCPAuthRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GCPAuthRole{}, &GCPAuthRoleList{})
}
