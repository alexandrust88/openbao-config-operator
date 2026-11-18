package identity

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	identityv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/identity/v1alpha1"
	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

func aliasOwnershipClient(t *testing.T, objs ...client.Object) (client.Client, *runtime.Scheme) {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, identityv1alpha1.AddToScheme, sysv1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	k8s := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).WithStatusSubresource(objs...).
		WithIndex(&identityv1alpha1.IdentityEntityAlias{}, engine.PathIndex, engine.PathIndexFunc).
		WithIndex(&identityv1alpha1.IdentityGroupAlias{}, engine.PathIndex, engine.PathIndexFunc).Build()
	return k8s, scheme
}

func acquiredAlias[T commonv1alpha1.Managed](obj T, path, id string) T {
	status := obj.GetResourceStatus()
	status.ID, status.Path, status.Namespace = id, litTenant+"/"+path, litTenant
	status.Acquired = commonv1alpha1.AcquiredCreated
	return obj
}

// TestEntityAliasAdoptRefusesOwnedAlias: an Adopt resource without an ID must not take over, through the lookup,
// an alias another resource manages by its ID.
func TestEntityAliasAdoptRefusesOwnedAlias(t *testing.T) {
	owner := acquiredAlias(newEntityAlias(), "identity/entity-alias/id/"+idtAliasID, idtAliasID)
	owner.UID = types.UID("owner")
	intruder := newEntityAlias()
	intruder.Name, intruder.UID, intruder.Generation = "intruder", types.UID("intruder"), 1
	intruder.Spec.EntityRef, intruder.Spec.EntityID = nil, "another-entity"
	intruder.Spec.CreationPolicy = commonv1alpha1.CreationPolicyAdopt
	intruder.Spec.ConnectionRef = commonv1alpha1.ConnectionReference{Name: "bao"}
	k8s, scheme := aliasOwnershipClient(t, owner, intruder)

	bao := newIdtBao()
	bao.data["identity/entity-alias/id/"+idtAliasID] = aliasRead()
	bao.responses["identity/lookup/entity"] = map[string]any{"id": idtEntityID, litAliases: []any{aliasRead()}}
	conns := idtConns{client: bao}
	r := engine.New(k8s, scheme, events.NewFakeRecorder(100), conns, IdentityEntityAliasAdapter{Reader: k8s, Conns: conns},
		func() *identityv1alpha1.IdentityEntityAlias { return &identityv1alpha1.IdentityEntityAlias{} }, engine.Options{})
	_, _ = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(intruder)})

	if got := bao.data["identity/entity-alias/id/"+idtAliasID][litCanonicalID]; got != idtEntityID {
		t.Errorf("canonical_id = %v, the owned alias was taken over", got)
	}
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(intruder), intruder); err != nil {
		t.Fatal(err)
	}
	if intruder.Status.Acquired != "" || intruder.Status.ID != "" {
		t.Errorf("intruder status = %+v, want nothing acquired", intruder.Status.ResourceStatus)
	}
	if synced := meta.FindStatusCondition(intruder.Status.Conditions, commonv1alpha1.ConditionSynced); synced == nil ||
		synced.Reason != commonv1alpha1.ReasonConflict {
		t.Errorf("Synced = %+v, want reason %s", synced, commonv1alpha1.ReasonConflict)
	}

	intruder.Spec.ManagementPolicy = commonv1alpha1.ManagementPolicyObserve
	intruder.Generation++
	if err := k8s.Update(context.Background(), intruder); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(intruder)}); err != nil {
		t.Fatalf("observing an owned alias: %v", err)
	}
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(intruder), intruder); err != nil {
		t.Fatal(err)
	}
	intruder.Spec.ManagementPolicy = commonv1alpha1.ManagementPolicyManage
	if err := k8s.Update(context.Background(), intruder); err != nil {
		t.Fatal(err)
	}
	_, _ = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(intruder)})
	if got := bao.data["identity/entity-alias/id/"+idtAliasID][litCanonicalID]; got != idtEntityID {
		t.Errorf("after Observe -> Manage canonical_id = %v, the owned alias was taken over", got)
	}
}

// TestGroupAliasAdoptRefusesOwnedAlias: the same rule for group aliases.
func TestGroupAliasAdoptRefusesOwnedAlias(t *testing.T) {
	const aliasID = "1637fa8d"
	spec := identityv1alpha1.IdentityGroupAliasSpec{AliasName: litKcDevs, MountAccessor: idtAccessor}
	spec.ConnectionRef = commonv1alpha1.ConnectionReference{Name: "bao"}
	owner := &identityv1alpha1.IdentityGroupAlias{ObjectMeta: idtMeta("owner"), Spec: spec}
	owner.Spec.GroupID, owner.UID = idtExtGroupID, types.UID("owner")
	owner = acquiredAlias(owner, "identity/group-alias/id/"+aliasID, aliasID)
	intruder := &identityv1alpha1.IdentityGroupAlias{ObjectMeta: idtMeta("intruder"), Spec: spec}
	intruder.Spec.GroupID, intruder.UID, intruder.Generation = "another-group", types.UID("intruder"), 1
	intruder.Spec.CreationPolicy = commonv1alpha1.CreationPolicyCreateOrAdopt
	k8s, scheme := aliasOwnershipClient(t, owner, intruder)

	bao := newIdtBao()
	stored := groupLookup()["alias"].(map[string]any)
	bao.data["identity/group-alias/id/"+aliasID] = stored
	bao.responses["identity/lookup/group"] = groupLookup()
	conns := idtConns{client: bao}
	r := engine.New(k8s, scheme, events.NewFakeRecorder(100), conns, IdentityGroupAliasAdapter{Reader: k8s, Conns: conns},
		func() *identityv1alpha1.IdentityGroupAlias { return &identityv1alpha1.IdentityGroupAlias{} }, engine.Options{})
	_, _ = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(intruder)})

	if got := bao.data["identity/group-alias/id/"+aliasID][litCanonicalID]; got != idtExtGroupID {
		t.Errorf("canonical_id = %v, the owned alias was taken over", got)
	}
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(intruder), intruder); err != nil {
		t.Fatal(err)
	}
	if intruder.Status.Acquired != "" || intruder.Status.ID != "" {
		t.Errorf("intruder status = %+v, want nothing acquired", intruder.Status.ResourceStatus)
	}
}
