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
)

// TestEndpointCreatesMountAgainstDevServer: reading sys/mounts/<path> of a mount that does not exist yet is a 400
// "No secret engine mount at", which the Endpoint takes for absent, so the mount is created and then kept in sync
// by reading it; any other 400 stays an error.
func TestEndpointCreatesMountAgainstDevServer(t *testing.T) {
	address := startDevBao(t)
	bao := injectedRootClient(t, address)
	ctx := context.Background()

	e := &corev1alpha1.OpenBaoEndpoint{
		ObjectMeta: metav1.ObjectMeta{Name: "kv-mount", Namespace: testNamespace, Generation: 1},
		Spec: corev1alpha1.OpenBaoEndpointSpec{
			Path: "sys/mounts/endpoint-kv",
			Data: &runtime.RawExtension{Raw: []byte(`{"type":"kv","description":"through the raw API","options":{"version":"2"}}`)},
		},
	}
	scheme := runtime.NewScheme()
	if err := errors.Join(corev1alpha1.AddToScheme(scheme), corev1.AddToScheme(scheme)); err != nil {
		t.Fatal(err)
	}
	k8s := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(e).WithObjects(e).
		WithIndex(e, engine.PathIndex, engine.PathIndexFunc).Build()
	r := engine.New(k8s, scheme, events.NewFakeRecorder(100), staticConns{bao}, OpenBaoEndpointAdapter{Secrets: k8s},
		func() *corev1alpha1.OpenBaoEndpoint { return &corev1alpha1.OpenBaoEndpoint{} }, engine.Options{})
	for range 2 {
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(e)}); err != nil {
			t.Fatal(err)
		}
	}
	got := &corev1alpha1.OpenBaoEndpoint{}
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(e), got); err != nil {
		t.Fatal(err)
	}
	if !meta.IsStatusConditionTrue(got.Status.Conditions, commonv1alpha1.ConditionReady) ||
		got.Status.Acquired != commonv1alpha1.AcquiredCreated {
		t.Fatalf("status = %+v, want Ready and created", got.Status)
	}
	if mount, err := bao.Read(ctx, "sys/mounts/endpoint-kv"); err != nil || mount["type"] != "kv" {
		t.Fatalf("mount = %v, err = %v", mount, err)
	}

	tune := got.DeepCopy()
	tune.Spec.ReadPath = "sys/mounts/missing/tune"
	if _, found, err := (OpenBaoEndpointAdapter{Secrets: k8s}).Observe(ctx, bao, tune); err == nil || found {
		t.Errorf("tune of a missing mount: found=%v err=%v, want an error", found, err)
	}
}
