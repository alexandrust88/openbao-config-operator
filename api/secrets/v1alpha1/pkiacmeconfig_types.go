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
// +kubebuilder:validation:XValidation:rule="!has(self.deletionPolicy) || self.deletionPolicy == 'Orphan'",message="the ACME configuration cannot be deleted; deletionPolicy must be Orphan"
// +kubebuilder:validation:XValidation:rule="!has(self.allowedRoles) || !('*' in self.allowedRoles) || size(self.allowedRoles) == 1",message="'*' in allowedRoles must be the only entry"
// +kubebuilder:validation:XValidation:rule="!has(self.allowedIssuers) || !('*' in self.allowedIssuers) || size(self.allowedIssuers) == 1",message="'*' in allowedIssuers must be the only entry"

// spec is the desired ACME server configuration of a PKI secrets engine at `<mountPath>/config/acme`. Enabling ACME requires the mount's cluster path (PKIConfig cluster.path). Fields left unset keep the server value. deletionPolicy must be Orphan.
type PKIACMEConfigSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path of the PKI secrets engine mount. Defaults to `pki`. Immutable. Example: `pki-int`.
	// +kubebuilder:default=pki
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// enabled turns on the ACME endpoints of the mount (`enabled`). Defaults to false.
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// allowedIssuers are the issuer names or IDs ACME may issue from (`allowed_issuers`); `["*"]` allows every issuer and must be the only entry. Defaults to `["*"]`. Example: `[int-2026]`.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=128
	// +kubebuilder:validation:items:Pattern=`^[^,]+$`
	// +listType=set
	// +optional
	AllowedIssuers []string `json:"allowedIssuers,omitempty"`

	// allowedRoles are the roles ACME clients may use (`allowed_roles`); `["*"]` allows every role and must be the only entry. Defaults to `["*"]`. Example: `[web-server]`.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=128
	// +kubebuilder:validation:items:Pattern=`^[^,]+$`
	// +listType=set
	// +optional
	AllowedRoles []string `json:"allowedRoles,omitempty"`

	// allowRoleExtKeyUsage applies the role's extended key usages to ACME certificates instead of server authentication only (`allow_role_ext_key_usage`). Defaults to false.
	// +optional
	AllowRoleExtKeyUsage *bool `json:"allowRoleExtKeyUsage,omitempty"`

	// defaultDirectoryPolicy controls the default ACME directory (`default_directory_policy`): `forbid`, `sign-verbatim`, or `role:<name>` to issue through that role. Defaults to sign-verbatim. Example: `role:web-server`.
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^(forbid|sign-verbatim|role:[A-Za-z0-9_]([A-Za-z0-9_.-]*[A-Za-z0-9_])?)$`
	// +optional
	DefaultDirectoryPolicy string `json:"defaultDirectoryPolicy,omitempty"`

	// dnsResolver is the IP address and optional port of a DNS server used for ACME challenges (`dns_resolver`). Empty uses the system resolver. Example: `10.0.0.53:53`.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	DNSResolver *string `json:"dnsResolver,omitempty"`

	// eabPolicy controls external account binding (`eab_policy`). Allowed: not-required, new-account-required, always-required. Defaults to not-required.
	// +kubebuilder:validation:Enum=not-required;new-account-required;always-required
	// +optional
	EABPolicy string `json:"eabPolicy,omitempty"`
}

// status is the observed state of the ACME configuration.
type PKIACMEConfigStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// PKIACMEConfig sets the ACME server configuration of a PKI secrets engine at `<mount>/config/acme`, so ACME clients such as cert-manager or certbot can request certificates. A singleton per mount; it is never deleted.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: pki-int
//	  enabled: true
//	  allowedRoles: [web-server]
//	  defaultDirectoryPolicy: role:web-server
type PKIACMEConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec PKIACMEConfigSpec `json:"spec"`

	// +optional
	Status PKIACMEConfigStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (c *PKIACMEConfig) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &c.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (c *PKIACMEConfig) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &c.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// PKIACMEConfigList contains a list of PKIACMEConfig.
type PKIACMEConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []PKIACMEConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PKIACMEConfig{}, &PKIACMEConfigList{})
}
