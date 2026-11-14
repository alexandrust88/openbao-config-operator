package examples

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestIdentityCELRules installs every identity CRD into envtest and covers the CEL rules that the example tests do
// not: name immutability of the remaining kinds, the reserved metadata keys and the one-of rules of nested matches.
func TestIdentityCELRules(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	crds, err := filepath.Glob(filepath.Join(root, "config", "crd", "bases", "identity.openbaoconfig.io_*.yaml"))
	if err != nil || len(crds) != 14 {
		t.Fatalf("identity CRDs: %v %v", crds, err)
	}
	for i, crd := range crds {
		crds[i] = filepath.Base(crd)
	}
	k8s := secretsStartEnv(t, root, crds)
	ctx := context.Background()
	if err := k8s.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "openbao-config"}}); err != nil {
		t.Fatal(err)
	}

	t.Run("validation", func(t *testing.T) {
		const keys = "metadata keys are 1-128 characters"
		for _, tt := range []struct {
			name, doc, wantErr string
		}{
			{name: "entity marker key", wantErr: keys,
				doc: "kind: Entity\nmetadata: {name: m1}\nspec: {connectionRef: {name: openbao}, metadata: {openbaoconfig-uid: x}}"},
			{name: "group key with a dot", wantErr: keys,
				doc: "kind: Group\nmetadata: {name: m2}\nspec: {connectionRef: {name: openbao}, metadata: {team.name: x}}"},
			{name: "group vault key", wantErr: keys,
				doc: "kind: Group\nmetadata: {name: m3}\nspec: {connectionRef: {name: openbao}, metadata: {vault-x: x}}"},
			{name: "group root policy", wantErr: "policies cannot contain root",
				doc: "kind: Group\nmetadata: {name: p}\nspec: {connectionRef: {name: openbao}, policies: [root]}"},
			{name: "group alias with two groups", wantErr: "set exactly one of groupID, groupName, groupRef and groupSelector",
				doc: "kind: GroupAlias\nmetadata: {name: ga}\nspec: {connectionRef: {name: openbao}, aliasName: a, mountPath: oidc, groupID: g, groupName: other}"},
			{name: "provider client with two choices", wantErr: "set exactly one of clientID, clientName, clientRef and",
				doc: "kind: OIDCProvider\nmetadata: {name: p}\nspec: {connectionRef: {name: openbao}, allowedClients: [{clientID: a, clientName: b}]}"},
			{name: "assignment group with two choices", wantErr: "set exactly one of groupID, groupName, groupRef and groupSelector",
				doc: "kind: OIDCAssignment\nmetadata: {name: a}\nspec: {connectionRef: {name: openbao}, groups: [{groupID: a, groupName: b}]}"},
			{name: "enforcement method with two choices", wantErr: "set exactly one of methodID, methodName, methodRef and",
				doc: "kind: MFALoginEnforcement\nmetadata: {name: e1}\nspec: {connectionRef: {name: openbao}, authMethodTypes: [userpass], mfaMethods: [{methodID: a, methodName: b}]}"},
			{name: "enforcement mount with two choices", wantErr: "set exactly one of mountAccessor, mountPath, mountRef",
				doc: "kind: MFALoginEnforcement\nmetadata: {name: e2}\nspec: {connectionRef: {name: openbao}, mfaMethods: [{methodID: a}], authMounts: [{mountPath: a, mountAccessor: b}]}"},
			{name: "enforcement entity with two choices", wantErr: "set exactly one of entityID, entityName, entityRef and",
				doc: "kind: MFALoginEnforcement\nmetadata: {name: e3}\nspec: {connectionRef: {name: openbao}, mfaMethods: [{methodID: a}], entities: [{entityID: a, entityName: b}]}"},
			{name: "enforcement group with two choices", wantErr: "set exactly one of groupID, groupName, groupRef and groupSelector",
				doc: "kind: MFALoginEnforcement\nmetadata: {name: e4}\nspec: {connectionRef: {name: openbao}, mfaMethods: [{methodID: a}], groups: [{groupID: a, groupName: b}]}"},
		} {
			t.Run(tt.name, func(t *testing.T) {
				err := k8s.Create(ctx, oidcExamplesDoc(t, tt.doc), client.DryRunAll)
				if !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("got %v, want an Invalid error containing %q", err, tt.wantErr)
				}
			})
		}
		ok := oidcExamplesDoc(t, "kind: Entity\nmetadata: {name: ok}\nspec: {connectionRef: {name: openbao}, metadata: {team: a, a/b=c+d_e-f: x}}")
		if err := k8s.Create(ctx, ok, client.DryRunAll); err != nil {
			t.Errorf("valid metadata keys refused: %v", err)
		}
	})

	t.Run("name immutable", func(t *testing.T) {
		for _, doc := range []string{
			"kind: Group\nmetadata: {name: imm-group}\nspec: {connectionRef: {name: openbao}, name: a}",
			"kind: OIDCRole\nmetadata: {name: imm-role}\nspec: {connectionRef: {name: openbao}, name: a, key: k}",
			"kind: OIDCScope\nmetadata: {name: imm-scope}\nspec: {connectionRef: {name: openbao}, name: a}",
			"kind: OIDCProvider\nmetadata: {name: imm-provider}\nspec: {connectionRef: {name: openbao}, name: a}",
			"kind: OIDCAssignment\nmetadata: {name: imm-assignment}\nspec: {connectionRef: {name: openbao}, name: a}",
			"kind: OIDCClient\nmetadata: {name: imm-client}\nspec: {connectionRef: {name: openbao}, name: a}",
			"kind: MFALoginEnforcement\nmetadata: {name: imm-enforcement}\nspec: {connectionRef: {name: openbao}, name: a, authMethodTypes: [userpass], mfaMethods: [{methodID: m}]}",
		} {
			obj := oidcExamplesDoc(t, doc)
			if err := k8s.Create(ctx, obj); err != nil {
				t.Fatalf("%s: %v", obj.GetKind(), err)
			}
			changed := obj.DeepCopy()
			if err := unstructured.SetNestedField(changed.Object, "b", "spec", "name"); err != nil {
				t.Fatal(err)
			}
			if err := k8s.Update(ctx, changed); !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), "name is immutable") {
				t.Errorf("%s spec.name: got %v, want immutable", obj.GetKind(), err)
			}
		}
	})
}
