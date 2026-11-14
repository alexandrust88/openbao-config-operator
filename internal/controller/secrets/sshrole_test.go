package secrets

import (
	"context"
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

const (
	sshTestCIDR = "10.0.0.0/8"
)

func caSSHRole() *secretsv1alpha1.SSHRole {
	return &secretsv1alpha1.SSHRole{
		ObjectMeta: metav1.ObjectMeta{Name: "dev", Namespace: "team"},
		Spec: secretsv1alpha1.SSHRoleSpec{
			MountPath:                         "ssh",
			KeyType:                           secretsv1alpha1.SSHRoleKeyTypeCA,
			AllowedUsers:                      []string{"alice", "bob"},
			AllowUserCertificates:             new(true),
			AllowHostCertificates:             new(false),
			AllowBareDomains:                  new(false),
			AllowSubdomains:                   new(false),
			AllowUserKeyIDs:                   new(false),
			AllowEmptyPrincipals:              new(false),
			AllowCommasInIdentityTemplates:    new(false),
			AllowWildcardsInIdentityTemplates: new(false),
			AllowedUsersTemplate:              new(false),
			AllowedDomainsTemplate:            new(false),
			DefaultUserTemplate:               new(false),
			DefaultExtensionsTemplate:         new(false),
			DefaultExtensions:                 map[string]string{"permit-pty": ""},
			AllowedUserKeyLengths:             map[string][]int32{"rsa": {2048, 4096}},
			TTL:                               &metav1.Duration{Duration: time.Hour},
			MaxTTL:                            &metav1.Duration{Duration: 24 * time.Hour},
			NotBeforeDuration:                 &metav1.Duration{Duration: 30 * time.Second},
			AlgorithmSigner:                   "default",
			IssuerRef:                         "default",
		},
	}
}

// sshRoleGoldenRead is `bao read ssh/roles/dev` on OpenBao 2.7.2 for caSSHRole.
func sshRoleGoldenRead() map[string]any {
	return map[string]any{
		"algorithm_signer": "default", "allow_bare_domains": false, "allow_commas_in_identity_templates": false,
		"allow_empty_principals": false, "allow_host_certificates": false, "allow_subdomains": false,
		"allow_user_certificates": true, "allow_user_key_ids": false, "allow_wildcards_in_identity_templates": false,
		"allowed_critical_options": "", "allowed_domains": "", "allowed_domains_template": false,
		"allowed_extensions": "", "allowed_user_key_lengths": map[string]any{"rsa": []any{2048, 4096}},
		"allowed_users": "alice,bob", "allowed_users_template": false, "default_critical_options": map[string]any{},
		"default_extensions": map[string]any{"permit-pty": ""}, "default_extensions_template": false,
		"default_user": "", "default_user_template": false, "issuer_ref": "default", "key_id_format": "",
		"key_type": "ca", "max_ttl": 86400, "not_before_duration": 30, "ttl": 3600,
	}
}

func TestSSHRolePath(t *testing.T) {
	r := caSSHRole()
	if got := (SSHRoleAdapter{}).Path(r); got != "ssh/roles/dev" {
		t.Errorf("Path = %q", got)
	}
	r.Spec.Name = "ops@prod"
	if got := (SSHRoleAdapter{}).Path(r); got != "ssh/roles/ops@prod" {
		t.Errorf("Path = %q", got)
	}
}

func TestSSHRoleGoldenRead(t *testing.T) {
	a := SSHRoleAdapter{}
	desired, err := a.Desired(context.Background(), nil, caSSHRole())
	if err != nil {
		t.Fatal(err)
	}
	if desired["allowed_users"] != "alice,bob" || desired["ttl"] != int64(3600) {
		t.Errorf("Desired = %v", desired)
	}
	if diff := engine.Diff(desired, sshRoleGoldenRead(), a.Fields()); len(diff) != 0 {
		t.Errorf("steady state differs in %v", diff)
	}

	tampered := sshRoleGoldenRead()
	tampered["allowed_users"] = "alice,bob,mallory"
	tampered["allowed_user_key_lengths"] = map[string]any{"rsa": []any{1024}}
	if diff := engine.Diff(desired, tampered, a.Fields()); !slices.Equal(diff, []string{"allowed_user_key_lengths", "allowed_users"}) {
		t.Errorf("Diff = %v", diff)
	}
}

func TestSSHRoleOTPGoldenRead(t *testing.T) {
	a := SSHRoleAdapter{}
	r := &secretsv1alpha1.SSHRole{
		ObjectMeta: metav1.ObjectMeta{Name: "otp1"},
		Spec: secretsv1alpha1.SSHRoleSpec{
			MountPath:   "ssh",
			KeyType:     secretsv1alpha1.SSHRoleKeyTypeOTP,
			DefaultUser: "ubuntu",
			CIDRList:    []string{sshTestCIDR},
			Port:        ptr.To[int32](2222),
		},
	}
	desired, _ := a.Desired(context.Background(), nil, r)
	if _, ok := desired["allow_user_certificates"]; ok {
		t.Errorf("Desired(otp) = %v; unset certificate fields must be left out", desired)
	}
	read := map[string]any{"allowed_users": "", "cidr_list": sshTestCIDR, "default_user": "ubuntu",
		"exclude_cidr_list": "", "key_type": "otp", "port": 2222}
	if diff := engine.Diff(desired, read, a.Fields()); len(diff) != 0 {
		t.Errorf("steady state differs in %v", diff)
	}
}
