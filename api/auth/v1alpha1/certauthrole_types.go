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

// Desired state of a trusted certificate role, written to auth/<mountPath>/certs/<name>.
type CertAuthRoleSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path of the cert auth method, without the auth/ prefix. Defaults to cert. Immutable. Example: cert
	// +kubebuilder:default=cert
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// Name of the role in OpenBao, used as the cert role at login. Defaults to metadata.name. OpenBao stores role names in lower case. Immutable. Example: web
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[a-z0-9_]([a-z0-9_.-]*[a-z0-9_])?$`
	// +optional
	Name string `json:"name,omitempty"`

	// PEM-encoded CA certificate (or a single client certificate) that presented client certificates must chain to. Public data, not a secret. Required. Maps to `certificate`. Example: "-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----"
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=262144
	// +kubebuilder:validation:XValidation:rule="self.contains('-----BEGIN CERTIFICATE-----')",message="certificate must be PEM encoded"
	// +required
	Certificate string `json:"certificate"`

	// Name shown in token display names and audit logs. OpenBao defaults it to the role name. Maps to `display_name`. Example: web-clients
	// +kubebuilder:validation:MaxLength=256
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// Common names the client certificate may carry; globs are supported. Unset allows any. Maps to `allowed_common_names`. Example: ["*.svc.example.com"]
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:items:MaxLength=256
	// +listType=set
	// +optional
	AllowedCommonNames []string `json:"allowedCommonNames,omitempty"`

	// DNS subject alternative names the client certificate may carry; globs are supported. Unset allows any. Maps to `allowed_dns_sans`. Example: ["app.example.com"]
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:items:MaxLength=256
	// +listType=set
	// +optional
	AllowedDNSSANs []string `json:"allowedDNSSANs,omitempty"`

	// Email subject alternative names the client certificate may carry; globs are supported. Unset allows any. Maps to `allowed_email_sans`. Example: ["*@example.com"]
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:items:MaxLength=256
	// +listType=set
	// +optional
	AllowedEmailSANs []string `json:"allowedEmailSANs,omitempty"`

	// URI subject alternative names the client certificate may carry; globs are supported. Unset allows any. Maps to `allowed_uri_sans`. Example: ["spiffe://example.org/ns/team-a/*"]
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	AllowedURISANs []string `json:"allowedURISANs,omitempty"`

	// Organizational units (OU) the client certificate may carry. Unset allows any. Maps to `allowed_organizational_units`. Example: [platform]
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:items:MaxLength=256
	// +listType=set
	// +optional
	AllowedOrganizationalUnits []string `json:"allowedOrganizationalUnits,omitempty"`

	// Extensions, formatted as "oid:value", that the client certificate must all carry; globs are supported in the value. Maps to `required_extensions`. Example: ["1.2.3.4.5:team-a"]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=512
	// +listType=set
	// +optional
	RequiredExtensions []string `json:"requiredExtensions,omitempty"`

	// Extension OIDs whose values are copied into the token and alias metadata when present. Maps to `allowed_metadata_extensions`. Example: ["1.2.3.4.5"]
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=256
	// +listType=set
	// +optional
	AllowedMetadataExtensions []string `json:"allowedMetadataExtensions,omitempty"`

	// Checks the revocation status of client certificates with OCSP at login. Defaults to false. Maps to `ocsp_enabled`. Example: true
	// +optional
	OCSPEnabled *bool `json:"ocspEnabled,omitempty"`

	// PEM bundle of additional CA certificates needed to talk to OCSP servers and verify their responses. Maps to `ocsp_ca_certificates`. Example: "-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----"
	// +kubebuilder:validation:MaxLength=262144
	// +optional
	OCSPCACertificates string `json:"ocspCACertificates,omitempty"`

	// OCSP server URLs, queried in order, that replace those in the certificate's authority information access. Maps to `ocsp_servers_override`. Example: ["http://ocsp.example.com"]
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=512
	// +optional
	OCSPServersOverride []string `json:"ocspServersOverride,omitempty"`

	// When true, login proceeds if no OCSP server gives an answer; when false (default) it fails. Maps to `ocsp_fail_open`. Example: false
	// +optional
	OCSPFailOpen *bool `json:"ocspFailOpen,omitempty"`

	// When true, every OCSP server is queried and the certificate is valid only if all agree; when false (default) the first answer is used. Maps to `ocsp_query_all_servers`. Example: true
	// +optional
	OCSPQueryAllServers *bool `json:"ocspQueryAllServers,omitempty"`

	TokenFields `json:",inline"`
}

// Observed state of the role, written by the operator.
type CertAuthRoleStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// CertAuthRole manages a trusted certificate role of an OpenBao TLS certificate auth method at auth/<mountPath>/certs/<name>. Use it to let clients log in with a TLS client certificate issued by the given CA, optionally restricted by names, SANs and extensions.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  certificate: |
//	    -----BEGIN CERTIFICATE-----
//	    ...
//	    -----END CERTIFICATE-----
//	  allowedCommonNames: ["*.svc.example.com"]
//	  tokenPolicies: [app-read]
type CertAuthRole struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec CertAuthRoleSpec `json:"spec"`

	// +optional
	Status CertAuthRoleStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (c *CertAuthRole) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &c.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (c *CertAuthRole) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &c.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// CertAuthRoleList contains a list of CertAuthRole.
type CertAuthRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []CertAuthRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CertAuthRole{}, &CertAuthRoleList{})
}
