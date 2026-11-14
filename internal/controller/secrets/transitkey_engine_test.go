package secrets

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

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
