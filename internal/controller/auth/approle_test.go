package auth

import (
	"context"
	"reflect"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// appRoleRead is `bao read -format=json auth/approle/role/demo | jq .data` from OpenBao 2.7.2.
const appRoleRead = `{"bind_secret_id":true,"local_secret_ids":false,"secret_id_bound_cidrs":["10.0.0.0/8"],
"secret_id_num_uses":0,"secret_id_ttl":7200,"token_bound_cidrs":["10.0.0.0/8","10.1.2.3"],
"token_explicit_max_ttl":0,"token_max_ttl":0,"token_no_default_policy":false,"token_num_uses":0,"token_period":0,
"token_policies":["a","b"],"token_strictly_bind_ip":false,"token_ttl":3600,"token_type":"default"}`

func newAppRole() *authv1alpha1.AppRole {
	return &authv1alpha1.AppRole{
		ObjectMeta: metav1.ObjectMeta{Name: authTestDemo, Namespace: pluginTestNamespace},
		Spec: authv1alpha1.AppRoleSpec{
			ResourceSpec:       credConnection(),
			RoleID:             authTestRoleID,
			BindSecretID:       new(true),
			SecretIDBoundCIDRs: []string{cloudCIDR},
			SecretIDNumUses:    new(int32(0)),
			SecretIDTTL:        credDuration(2 * time.Hour),
			LocalSecretIDs:     new(false),
			TokenFields: authv1alpha1.TokenFields{
				TokenBaseFields: authv1alpha1.TokenBaseFields{
					TokenBoundCIDRs: []string{authTestHostCIDR, cloudCIDR},
					TokenNumUses:    new(int32(0)),
					TokenType:       authv1alpha1.TokenTypeDefault,
				},
				TokenTTL:      credDuration(time.Hour),
				TokenPolicies: []string{"b", "a"},
			},
		},
	}
}

func TestAppRolePath(t *testing.T) {
	a := newAppRole()
	if got := (AppRoleAdapter{}).Path(a); got != "auth/approle/role/demo" {
		t.Errorf("default path: %q", got)
	}
	a.Spec.MountPath = "team/approle"
	a.Spec.Name = "svc"
	if got := (AppRoleAdapter{}).Path(a); got != "auth/team/approle/role/svc" {
		t.Errorf("custom path: %q", got)
	}
}

func TestAppRoleDesired(t *testing.T) {
	got, err := AppRoleAdapter{}.Desired(context.Background(), nil, newAppRole())
	if err != nil {
		t.Fatal(err)
	}
	want := engine.Payload{
		authLitRoleID:           authTestRoleID,
		authKeyBindSecretID:     true,
		"secret_id_bound_cidrs": []string{cloudCIDR},
		"secret_id_num_uses":    int64(0),
		"secret_id_ttl":         int64(7200),
		"local_secret_ids":      false,
		keyTokenBoundCIDRs:      []string{authTestHostIP, cloudCIDR},
		keyTokenNumUses:         int64(0),
		keyTokenType:            authTestDefault,
		keyTokenTTL:             int64(3600),
		keyTokenPolicies:        []string{"b", "a"},
		keyTokenExplicitMaxTTL:  int64(0),
		keyTokenMaxTTL:          int64(0),
		keyTokenNoDefaultPolicy: false,
		keyTokenPeriod:          int64(0),
		keyTokenStrictlyBindIP:  false,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Desired =\n%v\nwant\n%v", got, want)
	}
}

func TestAppRoleObserveAndSteadyState(t *testing.T) {
	a := newAppRole()
	adapter := AppRoleAdapter{}
	bao := &credBao{reads: map[string]map[string]any{
		"auth/approle/role/demo":         credJSON(t, appRoleRead),
		"auth/approle/role/demo/role-id": {authLitRoleID: authTestRoleID},
	}}
	observed, found, err := adapter.Observe(context.Background(), bao, a)
	if err != nil || !found {
		t.Fatalf("Observe: found=%v err=%v", found, err)
	}
	desired, _ := adapter.Desired(context.Background(), nil, a)
	credExpectDiff(t, desired, observed, adapter.Fields())

	observed[keyTokenTTL] = 60
	credExpectDiff(t, desired, observed, adapter.Fields(), keyTokenTTL)

	if err := adapter.WriteStatus(context.Background(), bao, a, observed); err != nil || a.Status.ID != authTestRoleID {
		t.Errorf("status.id = %q, err %v", a.Status.ID, err)
	}

	_, found, err = adapter.Observe(context.Background(), &credBao{}, a)
	if err != nil || found {
		t.Errorf("absent role: found=%v err=%v", found, err)
	}
}

func TestAppRoleFieldsLocalSecretIDsCreateOnly(t *testing.T) {
	desired, _ := AppRoleAdapter{}.Desired(context.Background(), nil, newAppRole())
	update := engine.StripCreateOnly(desired, AppRoleAdapter{}.Fields())
	if _, ok := update["local_secret_ids"]; ok {
		t.Error("local_secret_ids sent on update")
	}
}
