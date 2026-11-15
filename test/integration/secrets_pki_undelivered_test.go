//go:build integration

package integration

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
)

// TestIntegrationPKIUndeliveredKey has a quota refuse the output Secret after OpenBao generated an exported key,
// then checks that deleting the resource with deletionPolicy Delete removes the key and that a recreate works.
func TestIntegrationPKIUndeliveredKey(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e := pkiSetup(ctx, t)
	mustWrite(ctx, t, e.bao, "sys/mounts/pki", map[string]any{"type": "pki"})

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "itest-undelivered"}}
	mustCreate(ctx, t, e.k8s, ns)
	noSecrets := corev1.ResourceList{corev1.ResourceSecrets: resource.MustParse("0")}
	quota := &corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{Name: "no-secrets", Namespace: ns.Name},
		Spec:       corev1.ResourceQuotaSpec{Hard: noSecrets},
	}
	mustCreate(ctx, t, e.k8s, quota)
	// envtest runs no quota controller, so the status the admission plugin enforces is set here.
	quota.Status = corev1.ResourceQuotaStatus{Hard: noSecrets, Used: noSecrets}
	if err := e.k8s.Status().Update(ctx, quota); err != nil {
		t.Fatal(err)
	}

	newKey := func() *secretsv1alpha1.PKIKey {
		key := &secretsv1alpha1.PKIKey{
			ObjectMeta: metav1.ObjectMeta{Name: "itest-undelivered", Namespace: ns.Name, Annotations: secretsAllowDestructiveDelete},
			Spec: secretsv1alpha1.PKIKeySpec{
				ResourceSpec:   pkiSpec(),
				Type:           secretsv1alpha1.PKIKeyCreationExported,
				KeyType:        "ec",
				KeyBits:        new(int32(256)),
				WriteSecretRef: &commonv1alpha1.LocalObjectReference{Name: "itest-undelivered-out"},
			},
		}
		key.Spec.DeletionPolicy = commonv1alpha1.DeletionPolicyDelete
		return key
	}
	key := newKey()
	mustCreate(ctx, t, e.k8s, key)
	waitFor(t, "output not delivered", func() error {
		if err := e.k8s.Get(ctx, client.ObjectKeyFromObject(key), key); err != nil {
			return err
		}
		ready := meta.FindStatusCondition(key.Status.Conditions, commonv1alpha1.ConditionReady)
		if ready == nil || ready.Reason != commonv1alpha1.ReasonOutputNotDelivered {
			return fmt.Errorf("ready = %+v", ready)
		}
		return nil
	})
	if _, err := e.bao.Read(ctx, "pki/key/itest-undelivered"); err != nil {
		t.Fatalf("the generated key is not in OpenBao: %v", err)
	}

	if err := e.k8s.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "resource gone", func() error { return expectGone(ctx, e.k8s, key) })
	if err := pkiExpectMissing(ctx, e.bao, "pki/key/itest-undelivered"); err != nil {
		t.Fatalf("deleting with deletionPolicy Delete left the undelivered key: %v", err)
	}

	if err := e.k8s.Delete(ctx, quota); err != nil {
		t.Fatal(err)
	}
	key = newKey()
	mustCreate(ctx, t, e.k8s, key)
	waitFor(t, "recreated key ready", func() error { return expectReady(ctx, e.k8s, key) })
	secret := &corev1.Secret{}
	if err := e.k8s.Get(ctx, client.ObjectKey{Namespace: ns.Name, Name: "itest-undelivered-out"}, secret); err != nil {
		t.Fatal(err)
	}
	if len(secret.Data["private_key"]) == 0 {
		t.Error("the recreated key was not delivered")
	}
}
