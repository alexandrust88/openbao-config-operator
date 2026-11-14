package identity

import (
	"context"
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	identityv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/identity/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// TestGroupMembershipObserverOwnsNothing: an observing membership neither records the members it sees nor keeps a
// member alive when the only managing membership is deleted.
func TestGroupMembershipObserverOwnsNothing(t *testing.T) {
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, identityv1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	member := func(name string, policy commonv1alpha1.ManagementPolicy) *identityv1alpha1.GroupMembership {
		m := newMembership(identityv1alpha1.GroupMembershipStatus{})
		m.Name, m.Generation = name, 1
		m.Spec.ConnectionRef = commonv1alpha1.ConnectionReference{Name: "bao"}
		m.Spec.ManagementPolicy = policy
		m.Spec.DeletionPolicy = commonv1alpha1.DeletionPolicyDelete
		return m
	}
	manager := member("manager", commonv1alpha1.ManagementPolicyManage)
	observer := member("observer", commonv1alpha1.ManagementPolicyObserve)
	labelled := idtMeta(litAlice)
	labelled.Labels = map[string]string{litTeam: "a"}
	k8s := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(manager, observer).WithObjects(manager, observer,
		idtReady(&identityv1alpha1.Group{ObjectMeta: idtMeta(litDevs)}, idtGroupID),
		idtReady(&identityv1alpha1.Entity{ObjectMeta: labelled}, litEAlice)).Build()
	bao := newIdtBao()
	bao.data["identity/group/id/"+idtGroupID] = groupRead()
	r := engine.New(k8s, scheme, events.NewFakeRecorder(100), idtConns{client: bao},
		GroupMembershipAdapter{Reader: k8s, Conns: idtConns{client: bao}},
		func() *identityv1alpha1.GroupMembership { return &identityv1alpha1.GroupMembership{} }, engine.Options{})
	ctx := context.Background()
	reconcile := func(m *identityv1alpha1.GroupMembership, n int) {
		t.Helper()
		for range n {
			if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(m)}); err != nil {
				t.Fatal(err)
			}
		}
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(m), m); client.IgnoreNotFound(err) != nil {
			t.Fatal(err)
		}
	}
	members := func() []string { return stringList(bao.data["identity/group/id/"+idtGroupID][litMemberEntityIDs]) }

	reconcile(manager, 3)
	if !slices.Contains(members(), litEAlice) || !slices.Equal(manager.Status.MemberEntityIDs, []string{litEAlice}) {
		t.Fatalf("after manage: members=%v status=%+v", members(), manager.Status)
	}
	reconcile(observer, 2)
	if len(observer.Status.MemberEntityIDs) != 0 {
		t.Fatalf("observer recorded members it does not own: %v", observer.Status.MemberEntityIDs)
	}

	if err := k8s.Delete(ctx, manager); err != nil {
		t.Fatal(err)
	}
	reconcile(manager, 1)
	if slices.Contains(members(), litEAlice) {
		t.Fatalf("deleting the only managing membership kept %s: members=%v", litEAlice, members())
	}
}

// TestGroupMembershipOwnClaimsCountsManagersOnly: only live managing memberships on the same server and OpenBao
// namespace keep a released member in the group.
func TestGroupMembershipOwnClaimsCountsManagersOnly(t *testing.T) {
	ctx := context.Background()
	status := identityv1alpha1.GroupMembershipStatus{GroupID: idtGroupID, MemberEntityIDs: []string{litEAlice}}
	status.ServerID, status.Namespace = "cluster-a", "team"
	other := func(name string, edit func(*identityv1alpha1.GroupMembership)) *identityv1alpha1.GroupMembership {
		o := newMembership(status)
		o.Name = name
		edit(o)
		return o
	}
	for name, o := range map[string]*identityv1alpha1.GroupMembership{
		"observer": other("o", func(o *identityv1alpha1.GroupMembership) {
			o.Spec.ManagementPolicy = commonv1alpha1.ManagementPolicyObserve
		}),
		"other server":    other("o", func(o *identityv1alpha1.GroupMembership) { o.Status.ServerID = "cluster-b" }),
		"other namespace": other("o", func(o *identityv1alpha1.GroupMembership) { o.Status.Namespace = "prod" }),
		"deleting": other("o", func(o *identityv1alpha1.GroupMembership) {
			o.Finalizers, o.DeletionTimestamp = []string{"x"}, new(metav1.Now())
		}),
	} {
		t.Run(name, func(t *testing.T) {
			a := GroupMembershipAdapter{Reader: idtReader(t, o)}
			entities, _, err := a.ownClaims(ctx, newMembership(status), membership{groupID: idtGroupID})
			if err != nil || !slices.Equal(entities, []string{litEAlice}) {
				t.Errorf("claims = %v, %v; want %s released", entities, err, litEAlice)
			}
		})
	}
	a := GroupMembershipAdapter{Reader: idtReader(t, other("o", func(*identityv1alpha1.GroupMembership) {}))}
	if entities, _, _ := a.ownClaims(ctx, newMembership(status), membership{groupID: idtGroupID}); len(entities) != 0 {
		t.Errorf("a live manager's member was released: %v", entities)
	}
}
