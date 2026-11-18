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

// The algorithm of a transit key.
// +kubebuilder:validation:Enum=aes128-gcm96;aes256-gcm96;chacha20-poly1305;xchacha20-poly1305;ecdsa-p256;ecdsa-p384;ecdsa-p521;ed25519;rsa-2048;rsa-3072;rsa-4096;mldsa-44;mldsa-65;mldsa-87;hmac;external-key
type TransitKeyType string

// +kubebuilder:validation:XValidation:rule="has(self.mountPath) == has(oldSelf.mountPath) && (!has(self.mountPath) || self.mountPath == oldSelf.mountPath)",message="mountPath is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.name) == has(oldSelf.name) && (!has(self.name) || self.name == oldSelf.name)",message="name is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.type) == has(oldSelf.type) && (!has(self.type) || self.type == oldSelf.type)",message="type is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.derived) == has(oldSelf.derived) && (!has(self.derived) || self.derived == oldSelf.derived)",message="derived is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.convergentEncryption) == has(oldSelf.convergentEncryption) && (!has(self.convergentEncryption) || self.convergentEncryption == oldSelf.convergentEncryption)",message="convergentEncryption is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.keySize) == has(oldSelf.keySize) && (!has(self.keySize) || self.keySize == oldSelf.keySize)",message="keySize is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.externalKeyRef) == has(oldSelf.externalKeyRef) && (!has(self.externalKeyRef) || self.externalKeyRef == oldSelf.externalKeyRef)",message="externalKeyRef is immutable"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.exportable) || !oldSelf.exportable || (has(self.exportable) && self.exportable)",message="exportable cannot be disabled once enabled"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.allowPlaintextBackup) || !oldSelf.allowPlaintextBackup || (has(self.allowPlaintextBackup) && self.allowPlaintextBackup)",message="allowPlaintextBackup cannot be disabled once enabled"
// +kubebuilder:validation:XValidation:rule="!has(self.convergentEncryption) || !self.convergentEncryption || (has(self.derived) && self.derived)",message="convergentEncryption requires derived"
// +kubebuilder:validation:XValidation:rule="!has(self.keySize) || self.type == 'hmac' || self.type == 'external-key'",message="keySize applies only to hmac and external-key keys"
// +kubebuilder:validation:XValidation:rule="(self.type == 'external-key') == has(self.externalKeyRef)",message="externalKeyRef is required for, and only allowed with, type external-key"
// +kubebuilder:validation:XValidation:rule="!has(self.autoRotatePeriod) || duration(self.autoRotatePeriod) == duration('0s') || (self.type != 'external-key' && duration(self.autoRotatePeriod) >= duration('1h'))",message="autoRotatePeriod must be 0 or at least 1h, and 0 for external-key keys"
// +kubebuilder:validation:XValidation:rule="!has(self.minEncryptionVersion) || !has(self.minDecryptionVersion) || self.minEncryptionVersion == 0 || self.minEncryptionVersion >= self.minDecryptionVersion",message="minEncryptionVersion must be 0 or at least minDecryptionVersion"

// spec is the desired named key of a transit secrets engine at `<mountPath>/keys/<name>`. Settings fixed at creation (type, derived, convergentEncryption, keySize, externalKeyRef) are immutable.
type TransitKeySpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// mountPath is the path of the transit secrets engine mount. Defaults to `transit`. Immutable. Example: `transit`.
	// +kubebuilder:default=transit
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// name is the key name in OpenBao. Defaults to metadata.name. Immutable. Example: `orders`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+$`
	// +optional
	Name string `json:"name,omitempty"`

	// type is the key algorithm (`type`), fixed at creation. Allowed: aes128-gcm96, aes256-gcm96, chacha20-poly1305, xchacha20-poly1305, ecdsa-p256, ecdsa-p384, ecdsa-p521, ed25519, rsa-2048, rsa-3072, rsa-4096, mldsa-44, mldsa-65, mldsa-87, hmac, external-key. Defaults to aes256-gcm96. Immutable.
	// +kubebuilder:default=aes256-gcm96
	// +optional
	Type TransitKeyType `json:"type,omitempty"`

	// derived enables key derivation, so every request must carry a context (`derived`). Defaults to false. Immutable.
	// +optional
	Derived *bool `json:"derived,omitempty"`

	// convergentEncryption makes the same plaintext, context and nonce produce the same ciphertext (`convergent_encryption`). Requires derived. Defaults to false. Immutable.
	// +optional
	ConvergentEncryption *bool `json:"convergentEncryption,omitempty"`

	// keySize is the key size in bytes for hmac and external-key keys only (`key_size`). 32-512. Immutable. Example: `32`.
	// +kubebuilder:validation:Minimum=32
	// +kubebuilder:validation:Maximum=512
	// +optional
	KeySize *int32 `json:"keySize,omitempty"`

	// externalKeyRef names the external key as `<config name>:<key name>` (`external_key_ref`), required for and only allowed with type external-key. Immutable. Example: `hsm:orders-key`.
	// +kubebuilder:validation:MaxLength=256
	// +optional
	ExternalKeyRef string `json:"externalKeyRef,omitempty"`

	// exportable allows every version of the key to be exported (`exportable`). Defaults to false. Once enabled it cannot be disabled.
	// +optional
	Exportable *bool `json:"exportable,omitempty"`

	// allowPlaintextBackup allows a plaintext backup of the key (`allow_plaintext_backup`). Defaults to false. Once enabled it cannot be disabled.
	// +optional
	AllowPlaintextBackup *bool `json:"allowPlaintextBackup,omitempty"`

	// autoRotatePeriod rotates the key automatically after this period (`auto_rotate_period`), a duration: 0 disables it, otherwise at least `1h`; must be 0 for external-key. Defaults to 0. Example: `720h`.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
	// +optional
	AutoRotatePeriod *metav1.Duration `json:"autoRotatePeriod,omitempty"`

	// minDecryptionVersion is the oldest key version allowed to decrypt or verify (`min_decryption_version`). Minimum 1. Defaults to 1.
	// +kubebuilder:validation:Minimum=1
	// +optional
	MinDecryptionVersion *int32 `json:"minDecryptionVersion,omitempty"`

	// minEncryptionVersion is the oldest key version allowed to encrypt or sign (`min_encryption_version`); 0 means the latest version. Must be 0 or at least minDecryptionVersion. Defaults to 0.
	// +kubebuilder:validation:Minimum=0
	// +optional
	MinEncryptionVersion *int32 `json:"minEncryptionVersion,omitempty"`

	// deletionAllowed allows the key to be deleted in OpenBao (`deletion_allowed`). Defaults to false; set automatically before the key is deleted under deletionPolicy Delete.
	// +optional
	DeletionAllowed *bool `json:"deletionAllowed,omitempty"`

	// rotateNonce rotates the key once (`<mountPath>/keys/<name>/rotate`) each time it changes to a new value. The first value seen when the key is created or adopted does not rotate it. Up to 63 characters. Example: `2026-q4`.
	// +kubebuilder:validation:MaxLength=63
	// +optional
	RotateNonce string `json:"rotateNonce,omitempty"`
}

// status is the observed state of the transit key.
type TransitKeyStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`

	// latestVersion is the latest version of the key.
	// +optional
	LatestVersion int64 `json:"latestVersion,omitempty"`

	// rotatedNonce is the last spec.rotateNonce that was applied.
	// +optional
	RotatedNonce string `json:"rotatedNonce,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=baotk,categories=obco;obco-secrets
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Version",type=integer,JSONPath=`.status.latestVersion`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// TransitKey manages a named key of a transit secrets engine at `<mount>/keys/<name>`, for encryption as a service, signing or HMAC; its key material never leaves OpenBao unless exportable. Change rotateNonce to rotate it once.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  mountPath: transit
//	  name: orders
//	  type: aes256-gcm96
//	  autoRotatePeriod: 720h
type TransitKey struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec TransitKeySpec `json:"spec"`

	// +optional
	Status TransitKeyStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (k *TransitKey) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &k.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (k *TransitKey) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &k.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// TransitKeyList contains a list of TransitKey.
type TransitKeyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []TransitKey `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TransitKey{}, &TransitKeyList{})
}
