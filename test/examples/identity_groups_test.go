package examples

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestIdentityGroupExamples installs the Entity, Group, EntityAlias, GroupAlias and GroupMembership CRDs into
// envtest, applies their examples with a server-side dry run and checks the reference and immutability rules.
func TestIdentityGroupExamples(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	k8s := secretsStartEnv(t, root, []string{
		"identity.openbaoconfig.io_entities.yaml",
		"identity.openbaoconfig.io_groups.yaml",
		"identity.openbaoconfig.io_entityaliases.yaml",
		"identity.openbaoconfig.io_groupaliases.yaml",
		"identity.openbaoconfig.io_groupmemberships.yaml",
	})
	ctx := context.Background()
	if err := k8s.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "openbao-config"}}); err != nil {
		t.Fatal(err)
	}

	for _, file := range []string{"entity.yaml", "group.yaml", "entityalias.yaml", "groupalias.yaml", "groupmembership.yaml"} {
		t.Run(file, func(t *testing.T) {
			for _, obj := range secretsDecode(t, filepath.Join(root, "examples", "identity", file)) {
				if err := k8s.Create(ctx, obj, client.DryRunAll, client.FieldValidation("Strict")); err != nil {
					t.Errorf("%s %s: %v", obj.GetKind(), obj.GetName(), err)
				}
			}
		})
	}

	const head = "apiVersion: identity.openbaoconfig.io/v1alpha1\nmetadata: {name: x, namespace: openbao-config}\n"
	t.Run("validation", func(t *testing.T) {
		for _, tt := range []struct {
			name, doc, wantErr string
		}{
			{name: "root policy", wantErr: "policies cannot contain root",
				doc: "kind: Entity\nspec: {connectionRef: {name: openbao}, policies: [root]}"},
			{name: "upper case policy", wantErr: "spec.policies",
				doc: "kind: Group\nspec: {connectionRef: {name: openbao}, policies: [Reader]}"},
			{name: "group type", wantErr: "spec.type",
				doc: "kind: Group\nspec: {connectionRef: {name: openbao}, type: dynamic}"},
			{name: "alias without mount", wantErr: "set exactly one of mountAccessor",
				doc: "kind: EntityAlias\nspec: {connectionRef: {name: openbao}, aliasName: a, entityID: e}"},
			{name: "alias with two mounts", wantErr: "set exactly one of mountAccessor",
				doc: "kind: EntityAlias\nspec: {connectionRef: {name: openbao}, aliasName: a, entityID: e, mountPath: userpass, mountAccessor: auth_x}"},
			{name: "alias with two entities", wantErr: "set exactly one of entityID",
				doc: "kind: EntityAlias\nspec: {connectionRef: {name: openbao}, aliasName: a, mountPath: userpass, entityID: e, entityName: other}"},
			{name: "group alias without group", wantErr: "set exactly one of groupID",
				doc: "kind: GroupAlias\nspec: {connectionRef: {name: openbao}, aliasName: a, mountPath: oidc}"},
			{name: "empty selector", wantErr: "matchLabels",
				doc: "kind: GroupAlias\nspec: {connectionRef: {name: openbao}, aliasName: a, mountPath: oidc, groupSelector: {matchLabels: {}}}"},
			{name: "membership without member", wantErr: "set exactly one of entityID",
				doc: "kind: GroupMembership\nspec: {connectionRef: {name: openbao}, groupName: g}"},
			{name: "membership with entity and group member", wantErr: "set exactly one of entityID",
				doc: "kind: GroupMembership\nspec: {connectionRef: {name: openbao}, groupName: g, entityName: e, memberGroupName: m}"},
			{name: "membership without group", wantErr: "set exactly one of groupID",
				doc: "kind: GroupMembership\nspec: {connectionRef: {name: openbao}, entityName: e}"},
		} {
			t.Run(tt.name, func(t *testing.T) {
				err := k8s.Create(ctx, secretsDecodeString(t, head+tt.doc), client.DryRunAll)
				if !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("got %v, want an Invalid error containing %q", err, tt.wantErr)
				}
			})
		}
	})

	t.Run("immutability", func(t *testing.T) {
		for _, tt := range []struct {
			name, doc, field, value, wantErr string
		}{
			{name: "alias name", wantErr: "aliasName is immutable", field: "aliasName", value: "b",
				doc: "kind: EntityAlias\nspec: {connectionRef: {name: openbao}, aliasName: a, mountPath: userpass, entityID: e}"},
			{name: "alias mount", wantErr: "the auth mount reference is immutable", field: "mountPath", value: "ldap",
				doc: "kind: GroupAlias\nspec: {connectionRef: {name: openbao}, aliasName: a, mountPath: oidc, groupID: g}"},
			{name: "membership group", wantErr: "the group reference is immutable", field: "groupName", value: "other",
				doc: "kind: GroupMembership\nspec: {connectionRef: {name: openbao}, groupName: g, entityName: e}"},
			{name: "group type", wantErr: "type is immutable", field: "type", value: "external",
				doc: "kind: Group\nspec: {connectionRef: {name: openbao}, type: internal}"},
			{name: "entity name", wantErr: "name is immutable", field: "name", value: "other",
				doc: "kind: Entity\nspec: {connectionRef: {name: openbao}, name: first}"},
		} {
			t.Run(tt.name, func(t *testing.T) {
				obj := secretsDecodeString(t, head+tt.doc)
				obj.SetName("immutable-" + strings.ReplaceAll(tt.name, " ", "-"))
				if err := k8s.Create(ctx, obj); err != nil {
					t.Fatal(err)
				}
				spec := obj.Object["spec"].(map[string]any)
				spec[tt.field] = tt.value
				err := k8s.Update(ctx, obj)
				if !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("got %v, want an Invalid error containing %q", err, tt.wantErr)
				}
			})
		}

		obj := secretsDecodeString(t, head+"kind: EntityAlias\nspec: {connectionRef: {name: openbao}, aliasName: a, mountPath: userpass, entityID: e}")
		obj.SetName("mutable-entity")
		if err := k8s.Create(ctx, obj); err != nil {
			t.Fatal(err)
		}
		spec := obj.Object["spec"].(map[string]any)
		delete(spec, "entityID")
		spec["entityName"] = "moved"
		spec["customMetadata"] = map[string]any{"k": "v"}
		if err := k8s.Update(ctx, obj); err != nil {
			t.Errorf("moving an alias to another entity must be allowed: %v", err)
		}
	})
}
