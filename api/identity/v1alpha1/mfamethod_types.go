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

// The hash algorithm of TOTP codes: SHA1, SHA256 or SHA512.
// +kubebuilder:validation:Enum=SHA1;SHA256;SHA512
type TOTPAlgorithm string

// Settings of a TOTP (time-based one-time password) login MFA method.
type MFATOTP struct {
	// issuer is the organisation name shown in authenticator apps (`issuer`). Example: `Example Corp`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +required
	Issuer string `json:"issuer"`

	// period is the time step of a code (`period`), a duration. Defaults to `30s`. Example: `30s`.
	// +optional
	Period *metav1.Duration `json:"period,omitempty"`

	// keySize is the size in bytes of generated keys (`key_size`). 1-1024. Defaults to 20.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=1024
	// +optional
	KeySize *int32 `json:"keySize,omitempty"`

	// qrSize is the pixel size of generated QR codes; 0 disables them (`qr_size`). 0-4096. Defaults to 200.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=4096
	// +optional
	QRSize *int32 `json:"qrSize,omitempty"`

	// algorithm is the hash algorithm of codes (`algorithm`). Allowed: SHA1, SHA256, SHA512. Defaults to SHA1.
	// +optional
	Algorithm TOTPAlgorithm `json:"algorithm,omitempty"`

	// digits is the number of digits in a code (`digits`). Allowed: 6, 8. Defaults to 6.
	// +kubebuilder:validation:Enum=6;8
	// +optional
	Digits *int32 `json:"digits,omitempty"`

	// skew is the number of periods before or after the current one that are accepted (`skew`). Allowed: 0, 1. Defaults to 1.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1
	// +optional
	Skew *int32 `json:"skew,omitempty"`

	// maxValidationAttempts is the number of failed validations allowed before a user is locked out (`max_validation_attempts`). Minimum 1. Defaults to 5.
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxValidationAttempts *int32 `json:"maxValidationAttempts,omitempty"`
}

// Settings of a Duo login MFA method.
type MFADuo struct {
	// secretKeySecretRef selects the Duo secret key (`secret_key`). Read from a Secret key; never stored in status. Write-only: a change is detected through the Secret, not read back. Example: `{name: duo, key: secret-key}`.
	// +required
	SecretKeySecretRef commonv1alpha1.SecretKeySelector `json:"secretKeySecretRef"`

	// integrationKeySecretRef selects the Duo integration key (`integration_key`). Read from a Secret key; never stored in status. Write-only. Example: `{name: duo, key: integration-key}`.
	// +required
	IntegrationKeySecretRef commonv1alpha1.SecretKeySelector `json:"integrationKeySecretRef"`

	// apiHostname is the Duo API host name (`api_hostname`). Example: `api-1a2b3c4d.duosecurity.com`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +required
	APIHostname string `json:"apiHostname"`

	// pushInfo is extra information sent with Duo push notifications, as URL-encoded key=value pairs (`push_info`). Example: `from=openbao&env=prod`.
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	PushInfo string `json:"pushInfo,omitempty"`

	// usePasscode reminds users that they can enter a passcode; it does not enforce one (`use_passcode`). Defaults to false.
	// +optional
	UsePasscode *bool `json:"usePasscode,omitempty"`

	// usernameFormat maps an identity to the Duo user name (`username_format`). Empty uses the alias name as is. Example: `{{alias.name}}@example.com`.
	// +kubebuilder:validation:MaxLength=255
	// +optional
	UsernameFormat string `json:"usernameFormat,omitempty"`
}

// Settings of an Okta login MFA method.
type MFAOkta struct {
	// orgName is the Okta organisation name (`org_name`). Example: `example`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +required
	OrgName string `json:"orgName"`

	// apiTokenSecretRef selects the Okta API token (`api_token`). Read from a Secret key; never stored in status. Write-only: a change is detected through the Secret, not read back. Example: `{name: okta, key: token}`.
	// +required
	APITokenSecretRef commonv1alpha1.SecretKeySelector `json:"apiTokenSecretRef"`

	// baseURL is the base domain of the Okta API (`base_url`). Defaults to `okta.com`. Example: `oktapreview.com`.
	// +kubebuilder:validation:MaxLength=255
	// +optional
	BaseURL string `json:"baseURL,omitempty"`

	// primaryEmail matches the user name against the primary email of the Okta account only (`primary_email`). OpenBao does not return it on read, so it is treated as write-only. Defaults to false.
	// +optional
	PrimaryEmail *bool `json:"primaryEmail,omitempty"`

	// usernameFormat maps an identity to the Okta user name (`username_format`). Empty uses the entity name as is. Example: `{{entity.name}}@example.com`.
	// +kubebuilder:validation:MaxLength=255
	// +optional
	UsernameFormat string `json:"usernameFormat,omitempty"`
}

// Settings of a PingID login MFA method.
type MFAPingID struct {
	// settingsFileSecretRef selects the PingID settings file for third-party clients, as downloaded from Ping Identity, not base64-encoded (`settings_file_base64`, encoded by the operator). Read from a Secret key; never stored in status. Write-only. Example: `{name: pingid, key: pingid.properties}`.
	// +required
	SettingsFileSecretRef commonv1alpha1.SecretKeySelector `json:"settingsFileSecretRef"`

	// usernameFormat maps an identity to the PingID user name (`username_format`). Empty uses the alias name as is. Example: `{{alias.name}}`.
	// +kubebuilder:validation:MaxLength=255
	// +optional
	UsernameFormat string `json:"usernameFormat,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="has(self.name) == has(oldSelf.name) && (!has(self.name) || self.name == oldSelf.name)",message="name is immutable"
// +kubebuilder:validation:XValidation:rule="[has(self.totp), has(self.duo), has(self.okta), has(self.pingID)].filter(x, x).size() == 1",message="set exactly one of totp, duo, okta and pingID"
// +kubebuilder:validation:XValidation:rule="has(self.totp) == has(oldSelf.totp) && has(self.duo) == has(oldSelf.duo) && has(self.okta) == has(oldSelf.okta) && has(self.pingID) == has(oldSelf.pingID)",message="the method type is immutable"

// spec is the desired login MFA method at `identity/mfa/method/<type>/<id>`. OpenBao assigns the method ID, reported in `status.id`; the method is found again by its name. Set exactly one of totp, duo, okta and pingID; the type is immutable.
type MFAMethodSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// name is the method name in OpenBao (`method_name`), unique within the OpenBao namespace. Defaults to metadata.name. Immutable. Example: `totp-main`.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +optional
	Name string `json:"name,omitempty"`

	// totp configures a time-based one-time password method. Immutable presence: the method type cannot change.
	// +optional
	TOTP *MFATOTP `json:"totp,omitempty"`

	// duo configures a Duo method. Immutable presence: the method type cannot change.
	// +optional
	Duo *MFADuo `json:"duo,omitempty"`

	// okta configures an Okta method. Immutable presence: the method type cannot change.
	// +optional
	Okta *MFAOkta `json:"okta,omitempty"`

	// pingID configures a PingID method. Immutable presence: the method type cannot change.
	// +optional
	PingID *MFAPingID `json:"pingID,omitempty"`
}

// status is the observed state of the MFA method; `id` holds the OpenBao method ID.
type MFAMethodStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-identity
// +kubebuilder:printcolumn:name="Method ID",type=string,JSONPath=`.status.id`,priority=1
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// MFAMethod manages a login MFA method (TOTP, Duo, Okta or PingID) at `identity/mfa/method/<type>/<id>`. MFALoginEnforcement objects reference it to require a second factor at login.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  name: totp-main
//	  totp:
//	    issuer: Example Corp
//	    period: 30s
type MFAMethod struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec MFAMethodSpec `json:"spec"`

	// +optional
	Status MFAMethodStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (m *MFAMethod) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &m.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (m *MFAMethod) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &m.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// MFAMethodList contains a list of MFAMethod.
type MFAMethodList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []MFAMethod `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MFAMethod{}, &MFAMethodList{})
}
