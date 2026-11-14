package secrets

import (
	"context"
	"testing"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
)

// TestTransitKeyObserveDoesNotConsumeRotation: a rotateNonce set while observing is applied once the key is
// managed again.
func TestTransitKeyObserveDoesNotConsumeRotation(t *testing.T) {
	k := fullTransitKey()
	k.Generation = 1
	k.Spec.ResourceSpec = pkiConnection()
	k.Spec.ManagementPolicy = commonv1alpha1.ManagementPolicyObserve
	k.Spec.RotateNonce = "two"
	k.Status.RotatedNonce, k.Status.RotationNonce = "one", "one"
	k.Status.Acquired, k.Status.Path = commonv1alpha1.AcquiredCreated, "transit/keys/app"
	k8s, scheme := outputClient(t, k, nil)
	bao := &outputBao{pkiFakeClient: &pkiFakeClient{reads: map[string]map[string]any{"transit/keys/app": transitGoldenRead()}}}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(k)}
	reconcile := func() *secretsv1alpha1.TransitKey {
		t.Helper()
		if err := reconcileOnce(k8s, scheme, bao, TransitKeyAdapter{}, req,
			func() *secretsv1alpha1.TransitKey { return &secretsv1alpha1.TransitKey{} }); err != nil {
			t.Fatal(err)
		}
		got := &secretsv1alpha1.TransitKey{}
		if err := k8s.Get(context.Background(), req.NamespacedName, got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	if got := reconcile(); got.Status.RotatedNonce != "one" {
		t.Fatalf("observe recorded rotation %q that never happened", got.Status.RotatedNonce)
	}
	got := reconcile()
	got.Spec.ManagementPolicy = commonv1alpha1.ManagementPolicyManage
	if err := k8s.Update(context.Background(), got); err != nil {
		t.Fatal(err)
	}
	reconcile()
	if n := countWrites(bao, "transit/keys/app/rotate"); n != 1 {
		t.Errorf("rotate writes = %d, want 1; writes = %v", n, bao.writePaths())
	}
}

// TestPKIIssuerObserveKeepsKeyID: observing never records a key the issuer resource would later delete.
func TestPKIIssuerObserveKeepsKeyID(t *testing.T) {
	issuer := fullPKIIssuer()
	issuer.Generation = 1
	issuer.Spec.ManagementPolicy = commonv1alpha1.ManagementPolicyObserve
	read := goldenPKIIssuerRead()
	id := read["issuer_id"].(string)
	issuer.Status.ID, issuer.Status.KeyID = id, "key-created-by-us"
	k8s, scheme := outputClient(t, issuer, nil)
	bao := &outputBao{pkiFakeClient: &pkiFakeClient{reads: map[string]map[string]any{"pki/issuer/" + id: read}}}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(issuer)}
	if err := reconcileOnce(k8s, scheme, bao, PKIIssuerAdapter{Secrets: k8s}, req,
		func() *secretsv1alpha1.PKIIssuer { return &secretsv1alpha1.PKIIssuer{} }); err != nil {
		t.Fatal(err)
	}
	got := &secretsv1alpha1.PKIIssuer{}
	if err := k8s.Get(context.Background(), req.NamespacedName, got); err != nil {
		t.Fatal(err)
	}
	if got.Status.KeyID != "key-created-by-us" || got.Status.ID != id {
		t.Errorf("status = id %q key %q, want the id observed and the key ledger kept", got.Status.ID, got.Status.KeyID)
	}
}
