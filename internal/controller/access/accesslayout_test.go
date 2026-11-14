package access

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	accessv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/access/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

func newLayout() *accessv1alpha1.AccessLayout {
	return &accessv1alpha1.AccessLayout{
		ObjectMeta: metav1.ObjectMeta{Name: "platform", Namespace: "team"},
		Spec: accessv1alpha1.AccessLayoutSpec{
			AuthMounts:    []string{"oidc"},
			Organizations: []string{"acme"},
			Paths:         []accessv1alpha1.AccessPath{{Mount: "orgs", Path: "{org}/+/*", MetadataPath: "{org}/*"}},
			Roles: []accessv1alpha1.AccessRole{{Name: "dev", Levels: []accessv1alpha1.AccessRoleLevel{
				{Level: "write"},
				{Namespace: "prod", Level: "read"},
			}}},
		},
	}
}

func layoutDesired(t *testing.T, l *accessv1alpha1.AccessLayout) (engine.Payload, error) {
	t.Helper()
	r := engine.NewResolver(fakeClient(t, newLevel("read", readLevel), newLevel("write", writeLevel)))
	return AccessLayoutAdapter{}.Desired(context.Background(), r, l)
}

func TestAccessLayoutDesired(t *testing.T) {
	desired, err := layoutDesired(t, newLayout())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"group:layout_team_platform_dev_acme_oidc", "group:prod/layout_team_platform_dev_acme_oidc",
		"policy:layout_team_platform_dev_acme", "policy:prod/layout_team_platform_dev_acme", "stale",
	}
	if keys := slices.Sorted(maps.Keys(desired)); !slices.Equal(keys, want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
	group := desired["group:prod/layout_team_platform_dev_acme_oidc"].(map[string]any)
	if alias := group["alias"].(map[string]any); alias["name"] != "acme_dev" || alias["mount"] != "oidc" {
		t.Errorf("alias = %v", alias)
	}
	if group["type"] != "external" || !slices.Equal(group["policies"].([]any), []any{"layout_team_platform_dev_acme"}) {
		t.Errorf("group = %v", group)
	}
	prod := desired["policy:prod/layout_team_platform_dev_acme"].(string)
	wantProd := "# managed-by: openbao-config-operator owner: AccessLayout/team/platform\n" +
		"path \"orgs/data/acme/+/*\" {\n  capabilities = [\"read\"]\n}\n" +
		"path \"orgs/metadata/acme/*\" {\n  capabilities = [\"read\", \"list\"]\n}\n"
	if prod != wantProd {
		t.Errorf("prod policy:\n%s\nwant:\n%s", prod, wantProd)
	}
}

func TestAccessLayoutAliasTemplate(t *testing.T) {
	l := newLayout()
	l.Spec.Organizations = nil
	l.Spec.Paths[0] = accessv1alpha1.AccessPath{Mount: "kv", Path: "apps/*"}
	cell := layoutCells(l)[0]
	if got := layoutAlias(l, cell); got != "dev" {
		t.Errorf("default alias = %q", got)
	}
	l.Spec.GroupAliasTemplate = "vault-{role}"
	if got := layoutAlias(l, cell); got != "vault-dev" {
		t.Errorf("templated alias = %q", got)
	}
}

func TestAccessLayoutDisruptiveGuard(t *testing.T) {
	l := newLayout()
	l.Status.Children = sortedChildren(layoutChildren(l))
	l.Spec.Roles[0].Levels = l.Spec.Roles[0].Levels[:1]

	_, err := layoutDesired(t, l)
	if !errors.Is(err, engine.ErrInvalidSpec) || !strings.Contains(err.Error(), accessv1alpha1.AllowDisruptiveUpdateAnnotation) ||
		!strings.Contains(err.Error(), "prod/identity/group/name/layout_team_platform_dev_acme_oidc") {
		t.Fatalf("err = %v, want the disruptive update refusal naming the group", err)
	}

	l.Annotations = map[string]string{accessv1alpha1.AllowDisruptiveUpdateAnnotation: "true"}
	if _, err := layoutDesired(t, l); err != nil {
		t.Errorf("with annotation: %v", err)
	}
}

// groupRead is the identity/group/name read shape of OpenBao 2.7.2 for an external group with an alias.
func groupRead(id, policy, aliasName string) map[string]any {
	return map[string]any{
		"alias": map[string]any{"canonical_id": id, "creation_time": "2026-11-13T22:24:13.508315Z", "id": "alias-" + id,
			"last_update_time": "2026-11-13T22:24:13.50835Z", "merged_from_canonical_ids": nil, "metadata": nil,
			"mount_accessor": "auth_jwt_117b8d2a", "mount_path": "auth/oidc/", "mount_type": "jwt", "name": aliasName},
		"creation_time": "2026-11-13T22:24:13.163444Z", "id": id, "last_update_time": "2026-11-13T22:24:13.508345Z",
		"member_entity_ids": nil, "member_group_ids": nil,
		"metadata":     map[string]any{"managed-by": "openbao-config-operator", "owner": "AccessLayout/team/platform"},
		"modify_index": 2, "name": "g", "namespace_id": "root", "parent_group_ids": nil, "policies": []any{policy},
		"type": "external",
	}
}

func seedLayoutReads(f fakeBao, desired engine.Payload) {
	for _, ns := range []string{"", "prod"} {
		scoped := f.WithNamespace(ns).(fakeBao)
		scoped.seed("sys/auth", map[string]any{"oidc/": map[string]any{"accessor": "auth_jwt_" + ns, "type": "jwt"}})
		key := childKey(policyChild(ns, "layout_team_platform_dev_acme"))
		scoped.seed("sys/policies/acl/layout_team_platform_dev_acme", map[string]any{"policy": desired[key]})
		scoped.seed("identity/group/name/layout_team_platform_dev_acme_oidc",
			groupRead("gid-"+ns, "layout_team_platform_dev_acme", "acme_dev"))
	}
}

func TestAccessLayoutObserveGoldenRead(t *testing.T) {
	l := newLayout()
	a := AccessLayoutAdapter{}
	desired, err := layoutDesired(t, l)
	if err != nil {
		t.Fatal(err)
	}
	f := newFakeBao()
	seedLayoutReads(f, desired)

	observed, found, err := a.Observe(context.Background(), f, l)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if diff := engine.Diff(desired, observed, a.Fields()); len(diff) != 0 {
		t.Errorf("steady state differs: %v", diff)
	}

	alias := f.store.data["prod/identity/group/name/layout_team_platform_dev_acme_oidc"]["alias"].(map[string]any)
	alias["name"] = "renamed"
	observed, _, _ = a.Observe(context.Background(), f, l)
	if diff := engine.Diff(desired, observed, a.Fields()); !slices.Equal(diff, []string{"group:prod/layout_team_platform_dev_acme_oidc"}) {
		t.Errorf("diff = %v", diff)
	}
}

func TestAccessLayoutWriteCreates(t *testing.T) {
	l := newLayout()
	a := AccessLayoutAdapter{}
	desired, err := layoutDesired(t, l)
	if err != nil {
		t.Fatal(err)
	}
	f := newFakeBao()
	for _, ns := range []string{"", "prod"} {
		f.WithNamespace(ns).(fakeBao).seed("sys/auth",
			map[string]any{"oidc/": map[string]any{"accessor": "auth_jwt_" + ns, "type": "jwt"}})
	}
	aliases := map[string]map[string]any{}
	f.store.onWrite = func(key string, data map[string]any) map[string]any {
		if strings.HasSuffix(key, "identity/group-alias") {
			aliases[key] = data
			return map[string]any{"id": "alias"}
		}
		f.store.data[key] = data
		if strings.Contains(key, "identity/group/name/") {
			return map[string]any{"id": "gid:" + key}
		}
		return nil
	}

	if _, err := a.Write(context.Background(), f, l, desired, true); err != nil {
		t.Fatal(err)
	}
	wantLog := []string{
		"write sys/policies/acl/layout_team_platform_dev_acme",
		"write prod/sys/policies/acl/layout_team_platform_dev_acme",
		"write identity/group/name/layout_team_platform_dev_acme_oidc",
		"write identity/group-alias",
		"write prod/identity/group/name/layout_team_platform_dev_acme_oidc",
		"write prod/identity/group-alias",
	}
	if !slices.Equal(f.store.log, wantLog) {
		t.Errorf("log = %v\nwant %v", f.store.log, wantLog)
	}
	if got := aliases["prod/identity/group-alias"]; got["mount_accessor"] != "auth_jwt_prod" || got["name"] != "acme_dev" ||
		got["canonical_id"] != "gid:prod/identity/group/name/layout_team_platform_dev_acme_oidc" {
		t.Errorf("prod alias = %v", got)
	}
	if len(l.Status.Children) != 4 {
		t.Errorf("children = %v", l.Status.Children)
	}
}

func TestAccessLayoutWriteUpdatesAliasByID(t *testing.T) {
	l := newLayout()
	a := AccessLayoutAdapter{}
	desired, err := layoutDesired(t, l)
	if err != nil {
		t.Fatal(err)
	}
	f := newFakeBao()
	seedLayoutReads(f, desired)
	f.store.data["prod/identity/group/name/layout_team_platform_dev_acme_oidc"]["alias"].(map[string]any)["name"] = "old"

	if _, err := a.Write(context.Background(), f, l, desired, false); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.store.log, []string{"write prod/identity/group-alias/id/alias-gid-prod"}) {
		t.Errorf("log = %v", f.store.log)
	}
}

func TestAccessLayoutDeleteOrder(t *testing.T) {
	l := newLayout()
	f := newFakeBao()
	desired, err := layoutDesired(t, l)
	if err != nil {
		t.Fatal(err)
	}
	seedLayoutReads(f, desired)
	l.Status.Children = sortedChildren(layoutChildren(l))
	if err := (AccessLayoutAdapter{}).Delete(context.Background(), f, l); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"delete identity/group/name/layout_team_platform_dev_acme_oidc",
		"delete prod/identity/group/name/layout_team_platform_dev_acme_oidc",
		"delete sys/policies/acl/layout_team_platform_dev_acme",
		"delete prod/sys/policies/acl/layout_team_platform_dev_acme",
	}
	if !slices.Equal(f.store.log, want) {
		t.Errorf("log = %v\nwant %v", f.store.log, want)
	}
}
