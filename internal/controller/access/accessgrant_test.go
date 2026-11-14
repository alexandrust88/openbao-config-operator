package access

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	accessv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/access/v1alpha1"
	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

const k8sAccessor = "auth_kubernetes_f63563f6"

func newGrant() *accessv1alpha1.AccessGrant {
	return &accessv1alpha1.AccessGrant{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "team"},
		Spec: accessv1alpha1.AccessGrantSpec{
			MountPath: "kubernetes",
			Subjects: []accessv1alpha1.AccessSubject{
				{Name: "app", Namespace: "team"},
				{Name: "*", NamespaceSelector: &accessv1alpha1.AccessNamespaceSelector{
					MatchLabels: map[string]string{"tier": "apps", "env": "dev"}}},
			},
			Rules: []accessv1alpha1.AccessRule{
				{AccessPath: accessv1alpha1.AccessPath{Mount: "secret", Path: "apps/{sa.namespace}/*"}, Level: "read"},
				{AccessPath: accessv1alpha1.AccessPath{Mount: "secret", Path: "shared/{org}/{sa.namespace}/*",
					MetadataPath: "shared/{org}/{sa.namespace}"}, Level: "write", Organizations: []string{"acme"}},
			},
			TokenTTL: &metav1.Duration{Duration: 15 * time.Minute},
		},
	}
}

// seedAuthTable writes the sys/auth table shape that OpenBao 2.7 returns.
func seedAuthTable(f fakeBao) {
	f.seed("sys/auth", map[string]any{
		"kubernetes/": map[string]any{"accessor": k8sAccessor, "type": "kubernetes"},
		"oidc/":       map[string]any{"accessor": "auth_jwt_882013d2", "type": "jwt"},
		"token/":      map[string]any{"accessor": "auth_token_54f69605", "type": "token"},
	})
}

func grantDesired(t *testing.T, g *accessv1alpha1.AccessGrant) engine.Payload {
	t.Helper()
	r := engine.NewResolver(fakeClient(t, newLevel("read", readLevel), newLevel("write", writeLevel)))
	desired, err := AccessGrantAdapter{}.Desired(context.Background(), r, g)
	if err != nil {
		t.Fatal(err)
	}
	return desired
}

func TestAccessGrantDesired(t *testing.T) {
	desired := grantDesired(t, newGrant())
	keys := slices.Sorted(func(yield func(string) bool) {
		for k := range desired {
			if !yield(k) {
				return
			}
		}
	})
	want := []string{"policy:grant_team_app", "role:grant_team_app_0", "role:grant_team_app_1", "stale"}
	if !slices.Equal(keys, want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
	if got := desired["role:grant_team_app_0"]; !reflect.DeepEqual(got, engine.Payload{
		"bound_service_account_names":              []any{"app"},
		"bound_service_account_namespaces":         []any{"team"},
		"bound_service_account_namespace_selector": "",
		"token_policies":                           []any{"grant_team_app"},
		"token_ttl":                                int64(900),
		"token_max_ttl":                            int64(0),
		"token_explicit_max_ttl":                   int64(0),
		"token_period":                             int64(0),
		"token_num_uses":                           int64(0),
		"token_bound_cidrs":                        []any{},
		"token_strictly_bind_ip":                   false,
		"token_no_default_policy":                  false,
		"token_type":                               "default",
		"alias_name_source":                        "serviceaccount_uid",
	}) {
		t.Errorf("role 0 = %v", got)
	}
	role1 := desired["role:grant_team_app_1"].(engine.Payload)
	if role1["bound_service_account_namespace_selector"] != `{"matchLabels":{"env":"dev","tier":"apps"}}` {
		t.Errorf("selector = %v", role1["bound_service_account_namespace_selector"])
	}
	policy := desired["policy:grant_team_app"].(string)
	for _, line := range []string{
		"# managed-by: openbao-config-operator owner: AccessGrant/team/app\n",
		`path "secret/data/apps/{{identity.entity.aliases.` + accessorPlaceholder + `.metadata.service_account_namespace}}/*"`,
		`path "secret/metadata/shared/acme/{{identity.entity.aliases.` + accessorPlaceholder + `.metadata.service_account_namespace}}"`,
	} {
		if !strings.Contains(policy, line) {
			t.Errorf("policy lacks %q:\n%s", line, policy)
		}
	}
}

func TestAccessGrantDesiredMissingLevel(t *testing.T) {
	r := engine.NewResolver(fakeClient(t, newLevel("read", readLevel)))
	_, err := AccessGrantAdapter{}.Desired(context.Background(), r, newGrant())
	if !errors.Is(err, engine.ErrReferenceNotReady) {
		t.Errorf("err = %v, want ErrReferenceNotReady", err)
	}
}

func TestAccessGrantDesiredDefaultTTL(t *testing.T) {
	g := newGrant()
	g.Spec.TokenTTL = nil
	if got := grantDesired(t, g)["role:grant_team_app_0"].(engine.Payload)["token_ttl"]; got != int64(900) {
		t.Errorf("token_ttl = %v, want the CRD default 900", got)
	}
}

// TestAccessGrantRoleTokenDrift checks that out-of-band changes that widen the issued tokens are repaired.
func TestAccessGrantRoleTokenDrift(t *testing.T) {
	g := newGrant()
	a := AccessGrantAdapter{}
	desired := grantDesired(t, g)
	for key, value := range map[string]any{
		"token_period": 86400, "token_max_ttl": 86400, "token_num_uses": 5, "token_no_default_policy": true,
		"token_bound_cidrs": []any{"10.0.0.0/8"}, "token_type": "batch", "alias_name_source": "serviceaccount_name",
	} {
		f := newFakeBao()
		seedGrantReads(t, f, desired)
		f.store.data["auth/kubernetes/role/grant_team_app_0"][key] = value
		observed, _, err := a.Observe(context.Background(), f, g)
		if err != nil {
			t.Fatal(err)
		}
		if diff := engine.Diff(desired, observed, a.Fields()); !slices.Equal(diff, []string{"role:grant_team_app_0"}) {
			t.Errorf("%s tampered: diff = %v", key, diff)
		}
	}
}

func readyAuthMount(name, namespace, accessor string) *sysv1alpha1.AuthMount {
	m := &sysv1alpha1.AuthMount{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "team"}}
	m.Status.ID = accessor
	m.Status.Namespace = namespace
	m.Status.Path = strings.TrimPrefix(namespace+"/sys/auth/"+name, "/")
	m.Status.Conditions = []metav1.Condition{{Type: commonv1alpha1.ConditionReady, Status: metav1.ConditionTrue,
		Reason: commonv1alpha1.ReasonAvailable, LastTransitionTime: metav1.Now()}}
	return m
}

func TestAccessGrantAuthMountRef(t *testing.T) {
	g := newGrant()
	g.Spec.MountPath = ""
	g.Spec.AuthMountRef = &commonv1alpha1.LocalObjectReference{Name: "kubernetes"}
	f := newFakeBao()
	seedAuthTable(f)

	for _, tt := range []struct {
		name    string
		objs    []client.Object
		wantErr string
	}{
		{name: "missing", wantErr: "reference not ready"},
		{name: "not ready", objs: []client.Object{&sysv1alpha1.AuthMount{
			ObjectMeta: metav1.ObjectMeta{Name: "kubernetes", Namespace: "team"}}}, wantErr: "reference not ready"},
		{name: "other OpenBao namespace", objs: []client.Object{readyAuthMount("kubernetes", "team-b", k8sAccessor)},
			wantErr: `OpenBao namespace "team-b"`},
		{name: "ready", objs: []client.Object{readyAuthMount("kubernetes", "", k8sAccessor)}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := AccessGrantAdapter{Reader: fakeClient(t, tt.objs...)}
			_, _, err := a.Observe(context.Background(), f, g)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Observe: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestAccessKindsDefaults(t *testing.T) {
	for name, a := range map[string]engine.DriftTier{"AccessGrant": AccessGrantAdapter{}, "AccessLayout": AccessLayoutAdapter{}} {
		if a.DriftTier() != engine.TierAccessCritical {
			t.Errorf("%s: tier %s, want AccessCritical", name, a.DriftTier())
		}
	}
}

// seedGrantReads stores the objects as OpenBao 2.7.2 reads them back after the operator wrote them.
func seedGrantReads(t *testing.T, f fakeBao, desired engine.Payload) {
	t.Helper()
	seedAuthTable(f)
	text := strings.ReplaceAll(desired["policy:grant_team_app"].(string), accessorPlaceholder, k8sAccessor)
	f.seed("sys/policies/acl/grant_team_app", map[string]any{"name": "grant_team_app", "policy": text, "version": 1,
		"cas_required": false, "allow_slashes_in_identity_templates": false, "allow_wildcards_in_identity_templates": false})
	f.seed("auth/kubernetes/role/grant_team_app_0", map[string]any{
		"alias_name_source": "serviceaccount_uid", "bound_service_account_names": []any{"app"},
		"bound_service_account_namespace_selector": "", "bound_service_account_namespaces": []any{"team"},
		"token_bound_cidrs": []any{}, "token_explicit_max_ttl": 0, "token_max_ttl": 0, "token_no_default_policy": false,
		"token_num_uses": 0, "token_period": 0, "token_policies": []any{"grant_team_app"}, "token_strictly_bind_ip": false,
		"token_ttl": 900, "token_type": "default",
	})
	f.seed("auth/kubernetes/role/grant_team_app_1", map[string]any{
		"alias_name_source": "serviceaccount_uid", "bound_service_account_names": []any{"*"},
		"bound_service_account_namespace_selector": `{"matchLabels":{"env":"dev","tier":"apps"}}`,
		"bound_service_account_namespaces":         []any{},
		"token_bound_cidrs":                        []any{}, "token_explicit_max_ttl": 0, "token_max_ttl": 0, "token_no_default_policy": false,
		"token_num_uses": 0, "token_period": 0, "token_policies": []any{"grant_team_app"}, "token_strictly_bind_ip": false,
		"token_ttl": 900, "token_type": "default",
	})
}

func TestAccessGrantObserveGoldenRead(t *testing.T) {
	g := newGrant()
	a := AccessGrantAdapter{}
	desired := grantDesired(t, g)
	f := newFakeBao()
	seedGrantReads(t, f, desired)

	observed, found, err := a.Observe(context.Background(), f, g)
	if err != nil || !found {
		t.Fatalf("Observe: found=%v err=%v", found, err)
	}
	if diff := engine.Diff(desired, observed, a.Fields()); len(diff) != 0 {
		t.Errorf("steady state differs: %v", diff)
	}

	f.store.data["auth/kubernetes/role/grant_team_app_0"]["token_policies"] = []any{"default"}
	observed, _, _ = a.Observe(context.Background(), f, g)
	if diff := engine.Diff(desired, observed, a.Fields()); !slices.Equal(diff, []string{"role:grant_team_app_0"}) {
		t.Errorf("tampered role: diff = %v", diff)
	}
}

func TestAccessGrantObserveAbsent(t *testing.T) {
	f := newFakeBao()
	seedAuthTable(f)
	_, found, err := AccessGrantAdapter{}.Observe(context.Background(), f, newGrant())
	if err != nil || found {
		t.Errorf("found=%v err=%v, want absent", found, err)
	}
}

func TestAccessGrantObserveMissingMount(t *testing.T) {
	g := newGrant()
	g.Spec.MountPath = "elsewhere"
	f := newFakeBao()
	seedAuthTable(f)
	if _, _, err := (AccessGrantAdapter{}).Observe(context.Background(), f, g); !errors.Is(err, engine.ErrReferenceNotReady) {
		t.Errorf("err = %v, want ErrReferenceNotReady", err)
	}
	g.Spec.MountPath = "oidc"
	if _, _, err := (AccessGrantAdapter{}).Observe(context.Background(), f, g); err == nil || !strings.Contains(err.Error(), "type jwt") {
		t.Errorf("wrong type: err = %v", err)
	}
}

func TestAccessGrantWriteCreatesAndRecords(t *testing.T) {
	g := newGrant()
	a := AccessGrantAdapter{}
	desired := grantDesired(t, g)
	f := newFakeBao()
	seedAuthTable(f)

	if _, err := a.Write(context.Background(), f, g, desired, true); err != nil {
		t.Fatal(err)
	}
	wantLog := []string{"write sys/policies/acl/grant_team_app", "write auth/kubernetes/role/grant_team_app_0",
		"write auth/kubernetes/role/grant_team_app_1"}
	if !slices.Equal(f.store.log, wantLog) {
		t.Errorf("log = %v, want %v", f.store.log, wantLog)
	}
	text := f.store.data["sys/policies/acl/grant_team_app"]["policy"].(string)
	if strings.Contains(text, accessorPlaceholder) || !strings.Contains(text, k8sAccessor) {
		t.Errorf("written policy does not carry the accessor:\n%s", text)
	}
	if len(g.Status.Children) != 3 {
		t.Errorf("children = %v", g.Status.Children)
	}

	f.store.log = nil
	if _, err := a.Write(context.Background(), f, g, desired, false); err != nil {
		t.Fatal(err)
	}
	if len(f.store.log) != 0 {
		t.Errorf("unchanged children rewritten: %v", f.store.log)
	}
}

func TestAccessGrantWriteDeletesDroppedSubject(t *testing.T) {
	g := newGrant()
	a := AccessGrantAdapter{}
	f := newFakeBao()
	seedGrantReads(t, f, grantDesired(t, g))
	g.Status.Children = sortedChildren(grantChildren(g, "kubernetes"))
	if err := a.WriteStatus(context.Background(), f, g, nil); err != nil {
		t.Fatal(err)
	}

	g.Spec.Subjects = g.Spec.Subjects[:1]
	desired := grantDesired(t, g)
	observed, _, err := a.Observe(context.Background(), f, g)
	if err != nil {
		t.Fatal(err)
	}
	if diff := engine.Diff(desired, observed, a.Fields()); !slices.Equal(diff, []string{"stale"}) {
		t.Fatalf("diff = %v, want [stale]", diff)
	}
	if _, err := a.Write(context.Background(), f, g, desired, false); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.store.log, []string{"delete auth/kubernetes/role/grant_team_app_1"}) {
		t.Errorf("log = %v", f.store.log)
	}
	if len(g.Status.Children) != 2 {
		t.Errorf("children = %v", g.Status.Children)
	}
}

func TestAccessGrantDeleteOrder(t *testing.T) {
	g := newGrant()
	a := AccessGrantAdapter{}
	f := newFakeBao()
	seedGrantReads(t, f, grantDesired(t, g))
	g.Status.Children = sortedChildren(grantChildren(g, "kubernetes"))
	if err := a.WriteStatus(context.Background(), f, g, nil); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(g.Status.Roles, []string{"grant_team_app_0", "grant_team_app_1"}) {
		t.Errorf("roles = %v", g.Status.Roles)
	}
	delete(f.store.data, "auth/kubernetes/role/grant_team_app_1")

	if err := a.Delete(context.Background(), f, g); err != nil {
		t.Fatal(err)
	}
	want := []string{"delete auth/kubernetes/role/grant_team_app_0", "delete auth/kubernetes/role/grant_team_app_1",
		"delete sys/policies/acl/grant_team_app"}
	if !slices.Equal(f.store.log, want) {
		t.Errorf("log = %v, want %v", f.store.log, want)
	}
	if len(g.Status.Children) != 0 {
		t.Errorf("children left: %v", g.Status.Children)
	}
}
