package sys

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

const litPolicyPath = "sys/policies/acl/q"

// TestPolicyDriftDisabledFollowsReferencedAccessor: with driftDetection Disabled, a reconcile after the referenced
// AuthMount's accessor changed still renders and writes the policy with the new accessor.
func TestPolicyDriftDisabledFollowsReferencedAccessor(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := errors.Join(corev1.AddToScheme(scheme), sysv1alpha1.AddToScheme(scheme)); err != nil {
		t.Fatal(err)
	}
	mount := &sysv1alpha1.AuthMount{ObjectMeta: metav1.ObjectMeta{Name: litKubernetes, Namespace: testNamespace}}
	mount.Status.ID = "auth_kubernetes_new1"
	mount.Status.Conditions = []metav1.Condition{{Type: commonv1alpha1.ConditionReady, Status: metav1.ConditionTrue,
		Reason: commonv1alpha1.ReasonAvailable, LastTransitionTime: metav1.Now()}}
	p := newPolicy("q", "", `path "a/${accessor:k8s}" { capabilities = ["read"] }`)
	p.Spec.AccessorRefs = map[string]sysv1alpha1.PolicyAccessorRef{
		"k8s": {AuthMountRef: &commonv1alpha1.LocalObjectReference{Name: litKubernetes}},
	}
	p.Spec.DriftDetection = commonv1alpha1.DriftDetectionDisabled
	p.Generation = 1
	p.Status.Acquired, p.Status.Path = commonv1alpha1.AcquiredCreated, litPolicyPath
	p.Status.ServerID = engine.ConnectionKey(p.Namespace, p.Spec.ConnectionRef)
	p.Status.Conditions = []metav1.Condition{
		{Type: commonv1alpha1.ConditionReady, Status: metav1.ConditionTrue, Reason: commonv1alpha1.ReasonAvailable,
			ObservedGeneration: 1, LastTransitionTime: metav1.Now()},
		{Type: commonv1alpha1.ConditionSynced, Status: metav1.ConditionTrue, Reason: commonv1alpha1.ReasonReconcileSuccess,
			ObservedGeneration: 1, LastTransitionTime: metav1.Now()},
	}
	k8s := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(p, mount).WithObjects(p, mount).
		WithIndex(p, engine.PathIndex, engine.PathIndexFunc).Build()
	bao := newConfigClient(map[string]map[string]any{
		litPolicyPath: policyRead("q", `path "a/auth_kubernetes_old0" { capabilities = ["read"] }`),
	})
	conns := sysRevConns{bao}
	r := engine.New(k8s, scheme, events.NewFakeRecorder(100), conns, PolicyAdapter{Reader: k8s, Conns: conns},
		func() *sysv1alpha1.Policy { return &sysv1alpha1.Policy{} }, engine.Options{})
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(p)}
	reconcileWithAccessor := func(accessor string) {
		t.Helper()
		if _, err := r.Reconcile(context.Background(), req); err != nil {
			t.Fatal(err)
		}
		written, ok := bao.writes[litPolicyPath]
		if !ok || written[keyPolicy] != `path "a/`+accessor+`" { capabilities = ["read"] }` {
			t.Fatalf("written policy = %v, want the accessor %s", written[keyPolicy], accessor)
		}
		bao.data[litPolicyPath] = policyRead("q", written[keyPolicy].(string))
		delete(bao.writes, litPolicyPath)
	}

	reconcileWithAccessor("auth_kubernetes_new1")

	mount.Status.ID = "auth_kubernetes_new2"
	if err := k8s.Status().Update(context.Background(), mount); err != nil {
		t.Fatal(err)
	}
	reconcileWithAccessor("auth_kubernetes_new2")
}
