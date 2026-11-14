package access

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	accessv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/access/v1alpha1"
	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

type fakeConns struct{ bao openbao.Client }

func (c fakeConns) For(context.Context, string, commonv1alpha1.ConnectionReference) (openbao.Client, error) {
	return c.bao, nil
}

func setPolicy[T engine.Object](t *testing.T, c client.Client, obj T, policy commonv1alpha1.ManagementPolicy, edit func(T)) {
	t.Helper()
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(obj), obj); err != nil {
		t.Fatal(err)
	}
	obj.GetResourceSpec().ManagementPolicy = policy
	if edit != nil {
		edit(obj)
	}
	if err := c.Update(context.Background(), obj); err != nil {
		t.Fatal(err)
	}
}

func reconcileTimes[T engine.Object](t *testing.T, r *engine.Reconciler[T], c client.Client, obj T, n int) {
	t.Helper()
	for range n {
		if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(obj)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(obj), obj); err != nil {
		t.Fatal(err)
	}
}

// TestAccessGrantObserveKeepsChildren: a subject removed while observing is deleted once the grant manages again.
func TestAccessGrantObserveKeepsChildren(t *testing.T) {
	g := newGrant()
	g.Spec.ConnectionRef = commonv1alpha1.ConnectionReference{Name: "bao"}
	g.Generation = 1
	k8s := fake.NewClientBuilder().WithScheme(testScheme(t)).WithStatusSubresource(g).
		WithObjects(g, newLevel("read", readLevel), newLevel("write", writeLevel)).Build()
	f := newFakeBao()
	seedAuthTable(f)
	r := engine.New(k8s, testScheme(t), events.NewFakeRecorder(100), fakeConns{f}, AccessGrantAdapter{},
		func() *accessv1alpha1.AccessGrant { return &accessv1alpha1.AccessGrant{} }, engine.Options{})
	removed := "auth/kubernetes/role/grant_team_app_1"

	reconcileTimes(t, r, k8s, g, 2)
	if _, ok := f.store.data[removed]; !ok || len(g.Status.Children) != 3 {
		t.Fatalf("after create: role present=%v children=%v", ok, g.Status.Children)
	}

	setPolicy(t, k8s, g, commonv1alpha1.ManagementPolicyObserve, func(g *accessv1alpha1.AccessGrant) {
		g.Spec.Subjects = g.Spec.Subjects[:1]
	})
	reconcileTimes(t, r, k8s, g, 1)
	if len(g.Status.Children) != 3 {
		t.Fatalf("observe rewrote the children ledger: %v", g.Status.Children)
	}
	if _, ok := f.store.data[removed]; !ok {
		t.Fatal("observe deleted a role")
	}
	if c := meta.FindStatusCondition(g.Status.Conditions, commonv1alpha1.ConditionSynced); c == nil ||
		c.Reason != commonv1alpha1.ReasonDrifted {
		t.Errorf("observe Synced = %+v, want Drifted", c)
	}

	setPolicy(t, k8s, g, commonv1alpha1.ManagementPolicyManage, nil)
	reconcileTimes(t, r, k8s, g, 2)
	if _, ok := f.store.data[removed]; ok {
		t.Fatal("manage after observe left the removed subject's role active")
	}
	if len(g.Status.Children) != 2 {
		t.Errorf("children = %v, want policy and one role", g.Status.Children)
	}
}

// TestAccessLayoutObserveKeepsChildren: a level removed while observing is deleted once the layout manages again.
func TestAccessLayoutObserveKeepsChildren(t *testing.T) {
	l := newLayout()
	l.Spec.ConnectionRef = commonv1alpha1.ConnectionReference{Name: "bao"}
	l.Spec.AuthMounts = nil
	l.Generation = 1
	l.Annotations = map[string]string{accessv1alpha1.AllowDisruptiveUpdateAnnotation: "true"}
	k8s := fake.NewClientBuilder().WithScheme(testScheme(t)).WithStatusSubresource(l).
		WithObjects(l, newLevel("read", readLevel), newLevel("write", writeLevel)).Build()
	f := newFakeBao()
	r := engine.New(k8s, testScheme(t), events.NewFakeRecorder(100), fakeConns{f}, AccessLayoutAdapter{},
		func() *accessv1alpha1.AccessLayout { return &accessv1alpha1.AccessLayout{} }, engine.Options{})
	removed := "prod/sys/policies/acl/layout_team_platform_dev_acme"

	reconcileTimes(t, r, k8s, l, 2)
	if _, ok := f.store.data[removed]; !ok || len(l.Status.Children) != 2 {
		t.Fatalf("after create: policy present=%v children=%v", ok, l.Status.Children)
	}

	setPolicy(t, k8s, l, commonv1alpha1.ManagementPolicyObserve, func(l *accessv1alpha1.AccessLayout) {
		l.Spec.Roles[0].Levels = l.Spec.Roles[0].Levels[:1]
	})
	reconcileTimes(t, r, k8s, l, 1)
	if len(l.Status.Children) != 2 {
		t.Fatalf("observe rewrote the children ledger: %v", l.Status.Children)
	}

	setPolicy(t, k8s, l, commonv1alpha1.ManagementPolicyManage, nil)
	reconcileTimes(t, r, k8s, l, 2)
	if _, ok := f.store.data[removed]; ok {
		t.Fatal("manage after observe left the removed level's policy")
	}
	if len(l.Status.Children) != 1 {
		t.Errorf("children = %v, want one policy", l.Status.Children)
	}
}
