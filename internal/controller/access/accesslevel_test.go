package access

import (
	"context"
	"slices"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	accessv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/access/v1alpha1"
	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
)

func reconcileLevel(t *testing.T, c client.Client, name string) {
	t.Helper()
	r := &AccessLevelReconciler{Client: c, FieldOwner: "test"}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKey{Namespace: "team", Name: name}}); err != nil {
		t.Fatal(err)
	}
}

func TestAccessLevelInUse(t *testing.T) {
	grant := newGrant()
	layout := newLayout()
	c := fakeClient(t, newLevel("read", readLevel), newLevel("write", writeLevel), newLevel("unused", readLevel), grant, layout)

	reconcileLevel(t, c, "write")
	var level accessv1alpha1.AccessLevel
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: "team", Name: "write"}, &level); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(level.Finalizers, accessv1alpha1.InUseFinalizer) {
		t.Errorf("finalizers = %v", level.Finalizers)
	}
	if !slices.Equal(level.Status.CitedBy, []string{"AccessGrant/app", "AccessLayout/platform"}) {
		t.Errorf("citedBy = %v", level.Status.CitedBy)
	}
	if !meta.IsStatusConditionTrue(level.Status.Conditions, commonv1alpha1.ConditionReady) {
		t.Errorf("conditions = %v", level.Status.Conditions)
	}

	if err := c.Delete(context.Background(), &level); err != nil {
		t.Fatal(err)
	}
	reconcileLevel(t, c, "write")
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: "team", Name: "write"}, &level); err != nil {
		t.Fatalf("cited level was deleted: %v", err)
	}
	if ready := meta.FindStatusCondition(level.Status.Conditions, commonv1alpha1.ConditionReady); ready == nil ||
		ready.Reason != accessv1alpha1.ReasonInUse {
		t.Errorf("ready = %+v", ready)
	}

	if err := c.Delete(context.Background(), grant); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(context.Background(), layout); err != nil {
		t.Fatal(err)
	}
	reconcileLevel(t, c, "write")
	err := c.Get(context.Background(), client.ObjectKey{Namespace: "team", Name: "write"}, &level)
	if !apierrors.IsNotFound(err) {
		t.Errorf("level kept after its citers left: %v", err)
	}
}

func TestCitedLevels(t *testing.T) {
	requests := citedLevels(context.Background(), newLayout())
	if len(requests) != 2 || requests[0].Name != "read" || requests[1].Name != "write" || requests[0].Namespace != "team" {
		t.Errorf("requests = %v", requests)
	}
}
