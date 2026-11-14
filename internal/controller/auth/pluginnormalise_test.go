package auth

import (
	"context"
	"reflect"
	"slices"
	"testing"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

const (
	pluginGroupsKey  = "groups"
	pluginRADIUSPath = "auth/radius/config"
)

// pluginWriteBao records the last write.
type pluginWriteBao struct {
	openbao.Client
	path string
	data map[string]any
}

func (b *pluginWriteBao) Write(_ context.Context, path string, data map[string]any) (map[string]any, error) {
	b.path, b.data = path, data
	return nil, nil
}

func TestPluginLDAPConnectionLowerCase(t *testing.T) {
	o := &authv1alpha1.LDAPAuthConfig{Spec: authv1alpha1.LDAPAuthConfigSpec{LDAPConnectionFields: authv1alpha1.LDAPConnectionFields{
		URL:      "LDAPS://DC1.Example.org,ldaps://dc2.example.org",
		UserAttr: "sAMAccountName",
	}}}
	desired, err := (LDAPAuthConfigAdapter{}).Desired(context.Background(), nil, o)
	if err != nil {
		t.Fatal(err)
	}
	// OpenBao lower-cases url and userattr on write.
	observed := engine.Payload{"url": "ldaps://dc1.example.org,ldaps://dc2.example.org", "userattr": "samaccountname"}
	if d := engine.Diff(desired, observed, (LDAPAuthConfigAdapter{}).Fields()); len(d) != 0 {
		t.Errorf("steady state differs on %v", d)
	}
}

func TestPluginRADIUSWriteJoinsUnregisteredPolicies(t *testing.T) {
	a := RADIUSAuthConfigAdapter{}
	o := &authv1alpha1.RADIUSAuthConfig{Spec: authv1alpha1.RADIUSAuthConfigSpec{
		Host:                     authLitRadius,
		SecretSecretRef:          commonv1alpha1.SecretKeySelector{Name: "s", Key: "k"},
		UnregisteredUserPolicies: []string{authTestGuest, authLitReaders},
	}}
	desired := engine.Payload{"host": authLitRadius, "secret": "x", radiusUnregisteredPolicies: []string{authTestGuest, authLitReaders}}
	b := &pluginWriteBao{}
	if _, err := a.Write(context.Background(), b, o, desired, true); err != nil {
		t.Fatal(err)
	}
	if b.path != pluginRADIUSPath || b.data[radiusUnregisteredPolicies] != "guest,readers" {
		t.Errorf("wrote %s %v, want the policies as one comma-separated string", b.path, b.data[radiusUnregisteredPolicies])
	}
	if _, ok := desired[radiusUnregisteredPolicies].([]string); !ok {
		t.Error("Write must not mutate desired")
	}
	observed := engine.Payload{"host": authLitRadius, radiusUnregisteredPolicies: []any{authLitReaders, authTestGuest}}
	if d := engine.Diff(desired, observed, a.Fields()); len(d) != 0 {
		t.Errorf("list read back differs on %v", d)
	}
}

func TestPluginGitHubBaseURLTrailingSlash(t *testing.T) {
	for _, in := range []string{"https://github.example.org/api/v3", "https://github.example.org/api/v3/"} {
		o := &authv1alpha1.GitHubAuthConfig{Spec: authv1alpha1.GitHubAuthConfigSpec{Organization: "org", BaseURL: in}}
		desired, err := (GitHubAuthConfigAdapter{}).Desired(context.Background(), nil, o)
		if err != nil {
			t.Fatal(err)
		}
		if desired["base_url"] != "https://github.example.org/api/v3/" {
			t.Errorf("%s: base_url = %v, want the trailing slash OpenBao stores", in, desired["base_url"])
		}
	}
}

func TestPluginLDAPUserGroupsCaseFolded(t *testing.T) {
	a := LDAPAuthUserAdapter{}
	o := &authv1alpha1.LDAPAuthUser{Spec: authv1alpha1.LDAPAuthUserSpec{Name: "jdoe", Groups: []string{"Ops", "Auditors"}}}
	desired, err := a.Desired(context.Background(), nil, o)
	if err != nil {
		t.Fatal(err)
	}
	bao := pluginBao{data: map[string]map[string]any{a.Path(o): {pluginGroupsKey: "auditors,ops"}}, mounts: []string{"ldap"}}
	observed, _, err := a.Observe(context.Background(), bao, o)
	if err != nil {
		t.Fatal(err)
	}
	if d := engine.Diff(desired, observed, a.Fields()); len(d) != 0 {
		t.Errorf("lower-cased groups differ on %v", d)
	}

	bao.data[a.Path(o)] = map[string]any{pluginGroupsKey: "auditors,platform"}
	observed, _, err = a.Observe(context.Background(), bao, o)
	if err != nil {
		t.Fatal(err)
	}
	if d := engine.Diff(desired, observed, a.Fields()); !slices.Equal(d, []string{pluginGroupsKey}) {
		t.Errorf("changed group: diff = %v", d)
	}
	if !reflect.DeepEqual(observed[pluginGroupsKey], []string{"auditors", "platform"}) {
		t.Errorf("observed groups = %v", observed[pluginGroupsKey])
	}
}
