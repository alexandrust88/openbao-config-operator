package auth

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// userpassRead is `bao read -format=json auth/userpass/users/alice | jq .data` from OpenBao 2.7.2.
const userpassRead = `{"token_bound_cidrs":[],"token_explicit_max_ttl":0,"token_max_ttl":0,
"token_no_default_policy":false,"token_num_uses":0,"token_period":0,"token_policies":["p"],
"token_strictly_bind_ip":false,"token_ttl":0,"token_type":"default"}`

func newUserpassUser() *authv1alpha1.UserpassUser {
	return &authv1alpha1.UserpassUser{
		ObjectMeta: metav1.ObjectMeta{Name: "alice", Namespace: pluginTestNamespace},
		Spec: authv1alpha1.UserpassUserSpec{
			ResourceSpec:      credConnection(),
			PasswordSecretRef: &commonv1alpha1.SecretKeySelector{Name: authTestAliceLogin, Key: authLitPassword},
			TokenFields:       authv1alpha1.TokenFields{TokenPolicies: []string{"p"}},
		},
	}
}

func TestUserpassUserDesired(t *testing.T) {
	u := newUserpassUser()
	adapter := UserpassUserAdapter{}
	if got := adapter.Path(u); got != "auth/userpass/users/alice" {
		t.Errorf("path %q", got)
	}
	r := credResolver{secrets: map[string]map[string][]byte{authTestAliceLogin: {authLitPassword: []byte("not-a-real-password")}}}
	desired, err := adapter.Desired(context.Background(), r, u)
	if err != nil {
		t.Fatal(err)
	}
	if desired[authLitPassword] != "not-a-real-password" {
		t.Error("password not resolved from the Secret")
	}
	if _, ok := desired["password_hash"]; ok {
		t.Error("password_hash sent without passwordHashSecretRef")
	}
	if got := adapter.SecretRefs(u); !slices.Equal(got, []string{authTestAliceLogin}) {
		t.Errorf("SecretRefs = %v", got)
	}

	fields := adapter.Fields()
	credExpectDiff(t, desired, credJSON(t, userpassRead), fields)
	hash := engine.WriteOnlyHash([]byte("k"), desired, fields)
	r.secrets[authTestAliceLogin][authLitPassword] = []byte("rotated")
	rotated, _ := adapter.Desired(context.Background(), r, u)
	if engine.WriteOnlyHash([]byte("k"), rotated, fields) == hash {
		t.Error("a new password must change the write-only hash")
	}
}

func TestUserpassUserMissingSecret(t *testing.T) {
	_, err := UserpassUserAdapter{}.Desired(context.Background(), credResolver{}, newUserpassUser())
	if !errors.Is(err, engine.ErrReferenceNotReady) {
		t.Errorf("err = %v, want ErrReferenceNotReady", err)
	}
	if err != nil && strings.Contains(err.Error(), "not-a-real-password") {
		t.Error("error leaks the password")
	}
}

func TestUserpassUserPasswordHash(t *testing.T) {
	u := newUserpassUser()
	u.Spec.PasswordSecretRef = nil
	u.Spec.PasswordHashSecretRef = &commonv1alpha1.SecretKeySelector{Name: authTestAliceLogin, Key: "bcrypt"}
	r := credResolver{secrets: map[string]map[string][]byte{authTestAliceLogin: {"bcrypt": []byte("$2a$10$fakefakefake")}}}
	desired, err := UserpassUserAdapter{}.Desired(context.Background(), r, u)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := desired[authLitPassword]; ok || desired["password_hash"] != "$2a$10$fakefakefake" {
		t.Errorf("desired = %v", desired)
	}
}
