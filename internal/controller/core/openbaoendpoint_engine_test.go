package core

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	corev1alpha1 "github.com/alexandrust88/openbao-config-operator/api/core/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// writingBao is a fakeBao the engine can drive; it records the paths written.
type writingBao struct {
	fakeBao
	written []string
}

func (b *writingBao) Write(_ context.Context, path string, _ map[string]any) (map[string]any, error) {
	b.written = append(b.written, path)
	return nil, nil
}

func (b *writingBao) WithNamespace(string) openbao.Client { return b }

func (b *writingBao) Namespace() string { return "" }

type staticConns struct{ bao openbao.Client }

func (c staticConns) For(context.Context, string, commonv1alpha1.ConnectionReference) (openbao.Client, error) {
	return c.bao, nil
}

// TestEndpointDisableReadWritesSpecChangedDuringFailedPass: a failed pass advances status.observedGeneration; the
// spec change it could not write must still be written on the next pass instead of being taken as reconciled.
func TestEndpointDisableReadWritesSpecChangedDuringFailedPass(t *testing.T) {
	e := newEndpoint(`{"k":"v2"}`, func(s *corev1alpha1.OpenBaoEndpointSpec) { s.DisableRead = true })
	e.Generation = 2
	e.Status.ObservedGeneration = 2
	e.Status.Path = alicePath
	e.Status.ServerID = engine.ConnectionKey(testNamespace, e.Spec.ConnectionRef)
	e.Status.Conditions = []metav1.Condition{
		{Type: commonv1alpha1.ConditionReady, Status: metav1.ConditionTrue, Reason: commonv1alpha1.ReasonAvailable,
			ObservedGeneration: 1, LastTransitionTime: metav1.Now()},
		{Type: commonv1alpha1.ConditionSynced, Status: metav1.ConditionFalse,
			Reason: commonv1alpha1.ReasonConnectionNotReady, ObservedGeneration: 2, LastTransitionTime: metav1.Now()},
	}
	scheme := runtime.NewScheme()
	if err := errors.Join(corev1alpha1.AddToScheme(scheme), corev1.AddToScheme(scheme)); err != nil {
		t.Fatal(err)
	}
	k8s := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(e).WithObjects(e).
		WithIndex(e, engine.PathIndex, engine.PathIndexFunc).Build()
	bao := &writingBao{}
	r := engine.New(k8s, scheme, events.NewFakeRecorder(100), staticConns{bao}, OpenBaoEndpointAdapter{Secrets: k8s},
		func() *corev1alpha1.OpenBaoEndpoint { return &corev1alpha1.OpenBaoEndpoint{} }, engine.Options{})

	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(e)}); err != nil {
		t.Fatal(err)
	}
	if len(bao.written) != 1 || bao.written[0] != alicePath {
		t.Fatalf("writes = %v, want the changed spec written to %s", bao.written, alicePath)
	}
	got := &corev1alpha1.OpenBaoEndpoint{}
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(e), got); err != nil {
		t.Fatal(err)
	}
	if !meta.IsStatusConditionTrue(got.Status.Conditions, commonv1alpha1.ConditionReady) {
		t.Errorf("conditions = %+v, want Ready after the write", got.Status.Conditions)
	}
}
