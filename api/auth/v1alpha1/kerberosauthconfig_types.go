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

// Desired state of a Kerberos auth method configuration, written to auth/<mountPath>/config. deletionPolicy must be Orphan.
type KerberosAuthConfigSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Path of the Kerberos auth method, without the auth/ prefix. Defaults to kerberos. Immutable. Example: kerberos
	// +kubebuilder:default=kerberos
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// Kerberos service account of OpenBao, matching both the keytab entry and the LDAP service account. Required. Maps to `service_account`. Example: openbao_svc
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +required
	ServiceAccount string `json:"serviceAccount"`

	// Secret key holding the keytab file as raw bytes, for example created with kubectl create secret generic openbao-keytab --from-file=keytab=openbao.keytab; the operator base64-encodes it. The keytab must hold an entry for serviceAccount. Read from this Secret key; the value is never stored in status. OpenBao never returns it, so it is compared only through status.writeOnlyHash and a Secret change forces a write. Required. Maps to `keytab`. Example: {name: openbao-keytab, key: keytab}
	// +required
	KeytabSecretRef commonv1alpha1.SecretKeySelector `json:"keytabSecretRef"`

	// When true, instance names are removed from service principal names when parsing the keytab, so foo/localhost@EXAMPLE.COM matches as foo@EXAMPLE.COM. Defaults to false. Maps to `remove_instance_name`. Example: true
	// +optional
	RemoveInstanceName *bool `json:"removeInstanceName,omitempty"`

	// When true, every LDAP group found for the user at login is added as an identity group alias. Defaults to false. Maps to `add_group_aliases`. Example: true
	// +optional
	AddGroupAliases *bool `json:"addGroupAliases,omitempty"`
}

// Observed state of the auth method configuration, written by the operator.
type KerberosAuthConfigStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-auth
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// KerberosAuthConfig manages the configuration of an OpenBao Kerberos auth method at auth/<mountPath>/config. Use it to set the keytab and service account OpenBao uses to verify SPNEGO logins; one per mount.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: kerberos
//	  serviceAccount: openbao_svc
//	  keytabSecretRef: {name: openbao-keytab, key: keytab}
//	  deletionPolicy: Orphan
type KerberosAuthConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec KerberosAuthConfigSpec `json:"spec"`

	// +optional
	Status KerberosAuthConfigStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (o *KerberosAuthConfig) GetResourceSpec() *commonv1alpha1.ResourceSpec {
	return &o.Spec.ResourceSpec
}

// GetResourceStatus implements commonv1alpha1.Managed.
func (o *KerberosAuthConfig) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &o.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// KerberosAuthConfigList contains a list of KerberosAuthConfig.
type KerberosAuthConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []KerberosAuthConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&KerberosAuthConfig{}, &KerberosAuthConfigList{})
}
