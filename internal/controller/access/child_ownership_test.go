package access

import (
	"context"
	"path"
	"reflect"
	"slices"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	accessv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/access/v1alpha1"
	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

func reconcileOnce[T engine.Object](t *testing.T, r *engine.Reconciler[T], c client.Client, obj T) {
	t.Helper()
	_, _ = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(obj)})
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(obj), obj); client.IgnoreNotFound(err) != nil {
		t.Fatal(err)
	}
}

func recorded(children []accessv1alpha1.AccessChild, p string) bool {
	return slices.ContainsFunc(children, func(c accessv1alpha1.AccessChild) bool { return path.Join(c.Namespace, c.Path) == p })
}

func notSynced(obj engine.Object) bool {
	c := meta.FindStatusCondition(obj.GetResourceStatus().Conditions, commonv1alpha1.ConditionSynced)
	return c != nil && c.Status == metav1.ConditionFalse
}

// TestAccessGrantRefusesForeignChild: a subject added after acquisition whose role already exists is refused under
// creationPolicy Create, updated but never recorded under Adopt, and left in place when the grant is deleted.
func TestAccessGrantRefusesForeignChild(t *testing.T) {
	g := newGrant()
	g.Spec.ConnectionRef = commonv1alpha1.ConnectionReference{Name: "bao"}
	g.Spec.DeletionPolicy = commonv1alpha1.DeletionPolicyDelete
	g.Spec.Subjects = g.Spec.Subjects[:1]
	g.Generation = 1
	k8s := fake.NewClientBuilder().WithScheme(testScheme(t)).WithStatusSubresource(g).
		WithObjects(g, newLevel("read", readLevel), newLevel("write", writeLevel)).Build()
	f := newFakeBao()
	seedAuthTable(f)
	r := engine.New(k8s, testScheme(t), events.NewFakeRecorder(100), fakeConns{f}, AccessGrantAdapter{},
		func() *accessv1alpha1.AccessGrant { return &accessv1alpha1.AccessGrant{} }, engine.Options{})
	reconcileTimes(t, r, k8s, g, 2)

	foreign := "auth/kubernetes/role/grant_team_app_1"
	original := map[string]any{"bound_service_account_names": []any{"other"}, "token_policies": []any{"other"}}
	f.seed(foreign, original)
	setPolicy(t, k8s, g, commonv1alpha1.ManagementPolicyManage, func(g *accessv1alpha1.AccessGrant) {
		g.Spec.Subjects = newGrant().Spec.Subjects
		g.Generation++
	})
	reconcileOnce(t, r, k8s, g)
	if !reflect.DeepEqual(f.store.data[foreign], original) || recorded(g.Status.Children, foreign) || !notSynced(g) {
		t.Fatalf("Create: role=%v children=%v conditions=%v", f.store.data[foreign], g.Status.Children, g.Status.Conditions)
	}

	setPolicy(t, k8s, g, commonv1alpha1.ManagementPolicyManage, func(g *accessv1alpha1.AccessGrant) {
		g.Spec.CreationPolicy = commonv1alpha1.CreationPolicyAdopt
	})
	reconcileOnce(t, r, k8s, g)
	if reflect.DeepEqual(f.store.data[foreign], original) || recorded(g.Status.Children, foreign) {
		t.Fatalf("Adopt: role=%v children=%v", f.store.data[foreign], g.Status.Children)
	}

	if err := k8s.Delete(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	reconcileOnce(t, r, k8s, g)
	if _, ok := f.store.data[foreign]; !ok {
		t.Error("deleting the grant deleted an adopted role")
	}
	if _, ok := f.store.data["auth/kubernetes/role/grant_team_app_0"]; ok {
		t.Error("deleting the grant left the role it created")
	}
}

// TestAccessLayoutRefusesForeignChild: the same rule for a layout policy that exists before the layout plans it.
func TestAccessLayoutRefusesForeignChild(t *testing.T) {
	l := newLayout()
	l.Spec.ConnectionRef = commonv1alpha1.ConnectionReference{Name: "bao"}
	l.Spec.DeletionPolicy = commonv1alpha1.DeletionPolicyDelete
	l.Spec.AuthMounts = nil
	l.Spec.Roles[0].Levels = l.Spec.Roles[0].Levels[:1]
	l.Generation = 1
	k8s := fake.NewClientBuilder().WithScheme(testScheme(t)).WithStatusSubresource(l).
		WithObjects(l, newLevel("read", readLevel), newLevel("write", writeLevel)).Build()
	f := newFakeBao()
	r := engine.New(k8s, testScheme(t), events.NewFakeRecorder(100), fakeConns{f}, AccessLayoutAdapter{},
		func() *accessv1alpha1.AccessLayout { return &accessv1alpha1.AccessLayout{} }, engine.Options{})
	reconcileTimes(t, r, k8s, l, 2)

	foreign := "prod/sys/policies/acl/layout_team_platform_dev_acme"
	original := map[string]any{"policy": `path "other/*" { capabilities = ["read"] }`}
	f.store.data[foreign] = original
	setPolicy(t, k8s, l, commonv1alpha1.ManagementPolicyManage, func(l *accessv1alpha1.AccessLayout) {
		l.Spec.Roles = newLayout().Spec.Roles
		l.Generation++
	})
	reconcileOnce(t, r, k8s, l)
	if !reflect.DeepEqual(f.store.data[foreign], original) || recorded(l.Status.Children, foreign) || !notSynced(l) {
		t.Fatalf("Create: policy=%v children=%v conditions=%v", f.store.data[foreign], l.Status.Children, l.Status.Conditions)
	}

	setPolicy(t, k8s, l, commonv1alpha1.ManagementPolicyManage, func(l *accessv1alpha1.AccessLayout) {
		l.Spec.CreationPolicy = commonv1alpha1.CreationPolicyCreateOrAdopt
	})
	reconcileOnce(t, r, k8s, l)
	if reflect.DeepEqual(f.store.data[foreign], original) || recorded(l.Status.Children, foreign) {
		t.Fatalf("CreateOrAdopt: policy=%v children=%v", f.store.data[foreign], l.Status.Children)
	}

	if err := k8s.Delete(context.Background(), l); err != nil {
		t.Fatal(err)
	}
	reconcileOnce(t, r, k8s, l)
	if _, ok := f.store.data[foreign]; !ok {
		t.Error("deleting the layout deleted an adopted policy")
	}
	if _, ok := f.store.data["sys/policies/acl/layout_team_platform_dev_acme"]; ok {
		t.Error("deleting the layout left the policy it created")
	}
}
