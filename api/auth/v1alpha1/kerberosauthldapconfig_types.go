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

// Desired state of the LDAP group lookup of a Kerberos auth method, written to auth/<mountPath>/config/ldap. Token fields set the defaults of tokens issued at login. deletionPolicy must be Orphan.
type KerberosAuthLDAPConfigSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path of the Kerberos auth method, without the auth/ prefix. Defaults to kerberos. Immutable. Example: kerberos
	// +kubebuilder:default=kerberos
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	LDAPConnectionFields `json:",inline"`

	TokenFields `json:",inline"`
}

// Observed state of the LDAP configuration, written by the operator.
type KerberosAuthLDAPConfigStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-auth
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// KerberosAuthLDAPConfig manages the LDAP configuration of an OpenBao Kerberos auth method at auth/<mountPath>/config/ldap. Use it to look up the LDAP groups of Kerberos users, so KerberosAuthGroup mappings apply; one per mount.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: kerberos
//	  url: ldaps://dc1.example.com
//	  bindDN: cn=openbao,ou=Services,dc=example,dc=org
//	  bindPassSecretRef: {name: ldap-bind, key: password}
//	  userDN: ou=People,dc=example,dc=org
//	  groupDN: ou=Groups,dc=example,dc=org
//	  deletionPolicy: Orphan
type KerberosAuthLDAPConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec KerberosAuthLDAPConfigSpec `json:"spec"`

	// +optional
	Status KerberosAuthLDAPConfigStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *KerberosAuthLDAPConfig) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &o.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *KerberosAuthLDAPConfig) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// KerberosAuthLDAPConfigList contains a list of KerberosAuthLDAPConfig.
type KerberosAuthLDAPConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []KerberosAuthLDAPConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&KerberosAuthLDAPConfig{}, &KerberosAuthLDAPConfigList{})
}
