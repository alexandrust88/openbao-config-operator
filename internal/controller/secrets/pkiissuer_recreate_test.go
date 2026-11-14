package secrets

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
)

// TestPKIIssuerRecreatesIntermediateWithStaleID: an intermediate deleted out of band is recreated end to end,
// although status.id still names the deleted issuer.
func TestPKIIssuerRecreatesIntermediateWithStaleID(t *testing.T) {
	issuer := &secretsv1alpha1.PKIIssuer{
		ObjectMeta: metav1.ObjectMeta{Name: "int-a", Namespace: "team", Generation: 1},
		Spec: secretsv1alpha1.PKIIssuerSpec{
			ResourceSpec: pkiConnection(),
			MountPath:    "pki-int",
			Type:         secretsv1alpha1.PKIIssuerTypeIntermediate,
			Certificate: secretsv1alpha1.PKIIssuerCertificate{
				CommonName: "Example Intermediate", TTL: pkiDuration(43800 * time.Hour),
			},
			Signer: &secretsv1alpha1.PKIIssuerSigner{IssuerRef: &commonv1alpha1.LocalObjectReference{Name: "root"}},
		},
		Status: secretsv1alpha1.PKIIssuerStatus{ResourceStatus: commonv1alpha1.ResourceStatus{
			ID: "deleted-id", Acquired: commonv1alpha1.AcquiredCreated, Path: "pki-int/issuer/int-a",
		}},
	}
	k8s, scheme := outputClient(t, issuer, nil, pkiReadyIssuerObject("pki", "root-id"))
	bao := &outputBao{
		pkiFakeClient: &pkiFakeClient{reads: map[string]map[string]any{}, responses: map[string]map[string]any{
			"pki-int/intermediate/generate/internal": {"csr": "CSR", "key_id": "key-2"},
			"pki/issuer/root-id/sign-intermediate":   {"certificate": "INT\n", "ca_chain": []any{"INT", "ROOT"}},
			"pki-int/intermediate/set-signed":        {"mapping": map[string]any{"int-new": "key-2"}},
		}},
		created: map[string]map[string]map[string]any{"pki-int/intermediate/set-signed": {"pki-int/issuer/int-new": {
			"issuer_id": "int-new", "key_id": "key-2", "issuer_name": "int-a", "leaf_not_after_behavior": "err",
			"usage": "crl-signing,issuing-certificates,ocsp-signing,read-only", "revocation_signature_algorithm": "",
			"enable_aia_url_templating": false,
		}}},
	}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(issuer)}
	for range 3 {
		_ = reconcileOnce(k8s, scheme, bao, PKIIssuerAdapter{Secrets: k8s}, req,
			func() *secretsv1alpha1.PKIIssuer { return &secretsv1alpha1.PKIIssuer{} })
	}
	if n := countWrites(bao, "pki/issuer/root-id/sign-intermediate"); n != 1 {
		t.Fatalf("sign-intermediate writes = %d, want 1; writes = %v", n, bao.writePaths())
	}
	if n := countWrites(bao, "pki-int/intermediate/generate/internal"); n != 1 {
		t.Errorf("CSR generated %d times, want once", n)
	}
	got := &secretsv1alpha1.PKIIssuer{}
	if err := k8s.Get(context.Background(), req.NamespacedName, got); err != nil {
		t.Fatal(err)
	}
	if got.Status.ID != "int-new" || got.Status.CSR != "" {
		t.Errorf("status = id %q csr %q, want the recreated issuer", got.Status.ID, got.Status.CSR)
	}
}
