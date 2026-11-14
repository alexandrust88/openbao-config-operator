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

// How a PKI key is created: internal, exported, kms or import.
// +kubebuilder:validation:Enum=internal;exported;kms;import
type PKIKeyCreation string

const (
	PKIKeyCreationInternal PKIKeyCreation = "internal"
	PKIKeyCreationExported PKIKeyCreation = "exported"
	PKIKeyCreationKMS      PKIKeyCreation = "kms"
	PKIKeyCreationImport   PKIKeyCreation = "import"
)

// +kubebuilder:validation:XValidation:rule="has(self.mountPath) == has(oldSelf.mountPath) && (!has(self.mountPath) || self.mountPath == oldSelf.mountPath)",message="mountPath is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.name) == has(oldSelf.name) && (!has(self.name) || self.name == oldSelf.name)",message="name is immutable"
// +kubebuilder:validation:XValidation:rule="self.type == oldSelf.type",message="type is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.keyType) == has(oldSelf.keyType) && (!has(self.keyType) || self.keyType == oldSelf.keyType)",message="keyType is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.keyBits) == has(oldSelf.keyBits) && (!has(self.keyBits) || self.keyBits == oldSelf.keyBits)",message="keyBits is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.externalKeyRef) == has(oldSelf.externalKeyRef) && (!has(self.externalKeyRef) || self.externalKeyRef == oldSelf.externalKeyRef)",message="externalKeyRef is immutable"
// +kubebuilder:validation:XValidation:rule="(self.type == 'internal' || self.type == 'exported') || (!has(self.keyType) && !has(self.keyBits))",message="keyType and keyBits apply only to type internal or exported"
// +kubebuilder:validation:XValidation:rule="(self.type == 'kms') == has(self.externalKeyRef)",message="externalKeyRef is required with type kms and allowed only there"
// +kubebuilder:validation:XValidation:rule="(self.type == 'import') == has(self.pemBundleSecretRef)",message="pemBundleSecretRef is required with type import and allowed only there"
// +kubebuilder:validation:XValidation:rule="(self.type == 'exported') == has(self.writeSecretRef)",message="writeSecretRef is required with type exported and allowed only there"
// +kubebuilder:validation:XValidation:rule="self.?name.orValue('') != 'default'",message="default is a reserved key name"

// spec is the desired private key of a PKI secrets engine, at `<mountPath>/key/<name>`. The key is generated or imported once; afterwards only its name is reconciled. Issuers reference it through PKIIssuer key.existing.
type PKIKeySpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path of the PKI secrets engine mount. Defaults to `pki`. Immutable. Example: `pki-int`.
	// +kubebuilder:default=pki
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// name is the key name in OpenBao (`key_name`). Defaults to metadata.name; `default` is reserved. Immutable. Example: `int-2026`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_]([A-Za-z0-9_.-]*[A-Za-z0-9_])?$`
	// +optional
	Name string `json:"name,omitempty"`

	// type is how the key is created: `internal` (generated, never leaves OpenBao), `exported` (generated and written once to writeSecretRef), `kms` (backed by an OpenBao external key) or `import` (read from pemBundleSecretRef). Defaults to internal. Immutable.
	// +kubebuilder:default=internal
	// +optional
	Type PKIKeyCreation `json:"type,omitempty"`

	// keyType is the algorithm of a generated key (`key_type`), type internal or exported only. Defaults to rsa. Immutable. Example: `ec`.
	// +optional
	KeyType PKIKeyType `json:"keyType,omitempty"`

	// keyBits is the size of a generated key (`key_bits`): 2048, 3072, 4096 or 8192 for rsa; 224, 256, 384 or 521 for ec; 44, 65 or 87 for mldsa; unused for ed25519. Defaults to 2048 for rsa and 256 for ec. Immutable. Example: `384`.
	// +kubebuilder:validation:Enum=44;65;87;224;256;384;521;2048;3072;4096;8192
	// +optional
	KeyBits *int32 `json:"keyBits,omitempty"`

	// externalKeyRef is the OpenBao external key used with type kms (`external_key_ref`). Required with kms and allowed only there. Immutable. Example: `hsm-pki-key`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	// +optional
	ExternalKeyRef string `json:"externalKeyRef,omitempty"`

	// pemBundleSecretRef selects a Secret key holding the PEM private key to import with type import (`pem_bundle`). Read from a Secret key; never stored in status. Sent only when the key is created; keep the Secret while the resource exists. Example: `{name: int-ca-key, key: tls.key}`.
	// +optional
	PEMBundleSecretRef *commonv1alpha1.SecretKeySelector `json:"pemBundleSecretRef,omitempty"`

	// writeSecretRef names the Secret that receives the private key once, with type exported; required there and allowed only there. The Secret is owned by this resource and holds the keys `private_key` and `key_type`; never stored in status. Example: `{name: int-ca-key}`.
	// +optional
	WriteSecretRef *commonv1alpha1.LocalObjectReference `json:"writeSecretRef,omitempty"`
}

// status is the observed state of the PKI key; `id` holds the OpenBao key ID.
type PKIKeyStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=openbao
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// PKIKey manages a private key of a PKI secrets engine at `<mount>/key/<name>`, generated in OpenBao, backed by an external key, or imported from a Secret. Use it to create a CA key before its issuer, or to keep a key across issuer renewals.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: pki-int
//	  name: int-2026
//	  type: internal
//	  keyType: ec
//	  keyBits: 384
type PKIKey struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec PKIKeySpec `json:"spec"`

	// +optional
	Status PKIKeyStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (k *PKIKey) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &k.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (k *PKIKey) GetResourceStatus() *commonv1alpha1.ResourceStatus { return &k.Status.ResourceStatus }

// +kubebuilder:object:root=true

// PKIKeyList contains a list of PKIKey.
type PKIKeyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []PKIKey `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PKIKey{}, &PKIKeyList{})
}
