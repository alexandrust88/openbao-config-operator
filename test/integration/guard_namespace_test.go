//go:build integration

package integration

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
)

// TestIntegrationGuardedDeleteOrphansInTerminatingNamespace: a destructive delete already waiting for the annotation
// is orphaned once its Namespace starts terminating, with nothing but the Namespace change to trigger it. envtest
// runs no namespace controller, so the Namespace stays terminating and the resource is deleted by the test.
func TestIntegrationGuardedDeleteOrphansInTerminatingNamespace(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e := pkiSetup(ctx, t)
	mustWrite(ctx, t, e.bao, "sys/mounts/pki", map[string]any{"type": "pki"})

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "itest-terminating"}}
	mustCreate(ctx, t, e.k8s, ns)
	key := &secretsv1alpha1.PKIKey{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-guarded", Namespace: ns.Name},
		Spec: secretsv1alpha1.PKIKeySpec{
			ResourceSpec: pkiSpec(),
			Type:         secretsv1alpha1.PKIKeyCreationInternal,
			KeyType:      "ec",
			KeyBits:      new(int32(256)),
		},
	}
	key.Spec.DeletionPolicy = commonv1alpha1.DeletionPolicyDelete
	mustCreate(ctx, t, e.k8s, key)
	waitFor(t, "key ready", func() error { return expectReady(ctx, e.k8s, key) })

	if err := e.k8s.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "delete guarded", func() error {
		return expectSynced(ctx, e.k8s, key, commonv1alpha1.ReasonDeleteGuarded, commonv1alpha1.AnnotationAllowDestructiveDelete)
	})
	if err := e.k8s.Delete(ctx, ns); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "resource orphaned", func() error { return expectGone(ctx, e.k8s, key) })
	if _, err := e.bao.Read(ctx, "pki/key/itest-guarded"); err != nil {
		t.Fatalf("the orphaned key is not in OpenBao: %v", err)
	}
}
