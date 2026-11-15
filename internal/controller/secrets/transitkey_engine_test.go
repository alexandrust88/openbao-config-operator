package secrets

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// rotatingTransitBao serves one transit key; the first read after a rotate fails.
type rotatingTransitBao struct {
	openbao.Client
	rotations, failReads int
}

func (b *rotatingTransitBao) Read(context.Context, string) (map[string]any, error) {
	if b.failReads > 0 {
		b.failReads--
		return nil, errors.New("connection reset by peer")
	}
	return transitGoldenRead(), nil
}

func (b *rotatingTransitBao) Write(_ context.Context, path string, _ map[string]any) (map[string]any, error) {
	if path == "transit/keys/app/rotate" {
		b.rotations++
		b.failReads = 1
	}
	return nil, nil
}

func (b *rotatingTransitBao) WithNamespace(string) openbao.Client { return b }

func (b *rotatingTransitBao) Namespace() string { return "" }

// TestTransitKeyRotatesOnceWhenReadBackFails: a rotate whose read-back fails is not repeated by the retry.
func TestTransitKeyRotatesOnceWhenReadBackFails(t *testing.T) {
	k := fullTransitKey()
	k.Spec.ConnectionRef = pkiConnection().ConnectionRef
	k.Spec.RotateNonce = "two"
	k.Generation = 1
	k.Status.Acquired, k.Status.Path = commonv1alpha1.AcquiredCreated, "transit/keys/app"
	k.Status.ServerID = engine.ConnectionKey(k.Namespace, k.Spec.ConnectionRef)
	k.Status.RotationNonce, k.Status.RotatedNonce = "one", "one"
	k.Status.Conditions = []metav1.Condition{
		{Type: commonv1alpha1.ConditionReady, Status: metav1.ConditionTrue, Reason: commonv1alpha1.ReasonAvailable,
			ObservedGeneration: 1, LastTransitionTime: metav1.Now()},
		{Type: commonv1alpha1.ConditionSynced, Status: metav1.ConditionTrue, Reason: commonv1alpha1.ReasonReconcileSuccess,
			ObservedGeneration: 1, LastTransitionTime: metav1.Now()},
	}
	k8s, scheme := outputClient(t, k, &atomic.Bool{})
	bao := &rotatingTransitBao{}
	r := engine.New(k8s, scheme, events.NewFakeRecorder(100), outputConns{bao}, TransitKeyAdapter{},
		func() *secretsv1alpha1.TransitKey { return &secretsv1alpha1.TransitKey{} }, engine.Options{})
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(k)}

	if _, err := r.Reconcile(context.Background(), req); err == nil {
		t.Fatal("first reconcile: want the read-back error")
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if bao.rotations != 1 {
		t.Errorf("rotations = %d, want 1", bao.rotations)
	}
}

// namespacedStubClient is an engineStubClient that the engine can scope to the recorded namespace on delete.
type namespacedStubClient struct{ *engineStubClient }

func (c namespacedStubClient) WithNamespace(string) openbao.Client { return c }

func (namespacedStubClient) Namespace() string { return "" }

// TestTransitKeyDeleteNeedsAnnotation: with deletionPolicy Delete the key, and its deletion_allowed, stay untouched
// until the destructive-delete annotation is set.
func TestTransitKeyDeleteNeedsAnnotation(t *testing.T) {
	k := fullTransitKey()
	k.Spec.ConnectionRef = pkiConnection().ConnectionRef
	k.Spec.DeletionPolicy = commonv1alpha1.DeletionPolicyDelete
	k.Finalizers = []string{engine.DefaultFinalizer}
	k.Status.Acquired, k.Status.Path = commonv1alpha1.AcquiredCreated, "transit/keys/app"
	k.Status.ServerID = engine.ConnectionKey(k.Namespace, k.Spec.ConnectionRef)
	k8s, scheme := outputClient(t, k, &atomic.Bool{})
	bao := &engineStubClient{data: map[string]map[string]any{"transit/keys/app": transitGoldenRead()}}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(k)}
	reconcile := func() error {
		return reconcileOnce(k8s, scheme, namespacedStubClient{bao}, TransitKeyAdapter{}, req,
			func() *secretsv1alpha1.TransitKey { return &secretsv1alpha1.TransitKey{} })
	}
	if err := k8s.Delete(context.Background(), k); err != nil {
		t.Fatal(err)
	}

	if err := reconcile(); err != nil {
		t.Fatal(err)
	}
	if len(bao.calls) != 0 {
		t.Errorf("calls without the annotation = %v, want none", bao.calls)
	}
	if _, synced := outputConditions(t, k8s, k); synced == nil || synced.Reason != commonv1alpha1.ReasonDeleteGuarded {
		t.Errorf("Synced = %+v, want reason %s", synced, commonv1alpha1.ReasonDeleteGuarded)
	}

	if err := k8s.Get(context.Background(), req.NamespacedName, k); err != nil {
		t.Fatal(err)
	}
	k.Annotations = map[string]string{commonv1alpha1.AnnotationAllowDestructiveDelete: "true"}
	if err := k8s.Update(context.Background(), k); err != nil {
		t.Fatal(err)
	}
	if err := reconcile(); err != nil {
		t.Fatal(err)
	}
	want := []engineStubCall{
		{op: "write", path: "transit/keys/app/config", data: map[string]any{"deletion_allowed": true}},
		{op: "delete", path: "transit/keys/app"},
	}
	if !reflect.DeepEqual(bao.calls, want) {
		t.Errorf("calls with the annotation = %v, want %v", bao.calls, want)
	}
	if err := k8s.Get(context.Background(), req.NamespacedName, k); !apierrors.IsNotFound(err) {
		t.Errorf("resource after the delete: %v, want gone", err)
	}
}
