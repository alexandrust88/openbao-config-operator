package auth

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
)

// tokenRoleRead is `bao read -format=json auth/token/roles/demo | jq .data` from OpenBao 2.7.2, after a write
// without token_type and with token_num_uses=0 (which the read then omits).
const tokenRoleRead = `{"allowed_entity_aliases":null,"allowed_policies":["a","b"],"allowed_policies_glob":[],
"disallowed_policies":[],"disallowed_policies_glob":[],"explicit_max_ttl":0,"name":"demo","orphan":false,
"path_suffix":"sfx-1","period":0,"renewable":true,"token_bound_cidrs":["10.1.2.3","10.0.0.0/8"],
"token_explicit_max_ttl":0,"token_no_default_policy":false,"token_period":3600,"token_type":"default-service"}`

func newTokenRole() *authv1alpha1.TokenRole {
	return &authv1alpha1.TokenRole{
		ObjectMeta: metav1.ObjectMeta{Name: authTestDemo, Namespace: pluginTestNamespace},
		Spec: authv1alpha1.TokenRoleSpec{
			ResourceSpec:    credConnection(),
			AllowedPolicies: []string{"b", "a"},
			Orphan:          new(false),
			Renewable:       new(true),
			PathSuffix:      "sfx-1",
			TokenBaseFields: authv1alpha1.TokenBaseFields{
				TokenPeriod:     credDuration(time.Hour),
				TokenBoundCIDRs: []string{cloudCIDR, authTestHostCIDR},
				TokenNumUses:    new(int32(0)),
				TokenType:       authv1alpha1.TokenTypeDefault,
			},
		},
	}
}

func TestTokenRoleDesired(t *testing.T) {
	r := newTokenRole()
	if got := (TokenRoleAdapter{}).Path(r); got != "auth/token/roles/demo" {
		t.Errorf("path %q", got)
	}
	desired, err := TokenRoleAdapter{}.Desired(context.Background(), nil, r)
	if err != nil {
		t.Fatal(err)
	}
	if desired[keyTokenType] != "default-service" {
		t.Errorf("token_type = %v, want default-service", desired[keyTokenType])
	}
	for _, key := range []string{keyTokenTTL, keyTokenMaxTTL, keyTokenPolicies, keyTokenStrictlyBindIP} {
		if _, ok := desired[key]; ok {
			t.Errorf("token role payload has %s, which token roles do not accept", key)
		}
	}

	r.Spec.TokenType = authv1alpha1.TokenTypeBatch
	desired, _ = TokenRoleAdapter{}.Desired(context.Background(), nil, r)
	if desired[keyTokenType] != "batch" {
		t.Errorf("token_type = %v, want batch", desired[keyTokenType])
	}
}

func TestTokenRoleSteadyState(t *testing.T) {
	r := newTokenRole()
	adapter := TokenRoleAdapter{}
	bao := &credBao{reads: map[string]map[string]any{"auth/token/roles/demo": credJSON(t, tokenRoleRead)}}
	observed, found, err := adapter.Observe(context.Background(), bao, r)
	if err != nil || !found {
		t.Fatalf("Observe: found=%v err=%v", found, err)
	}
	desired, _ := adapter.Desired(context.Background(), nil, r)
	credExpectDiff(t, desired, observed, adapter.Fields())

	observed["allowed_policies"] = []any{"a"}
	credExpectDiff(t, desired, observed, adapter.Fields(), "allowed_policies")
}
