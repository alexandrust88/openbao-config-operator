package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// TestPKIConfigWritesOnlyDifferingSections runs the obco-pki-root sample against the reads of a fresh PKI mount on
// OpenBao 2.7.2: urls and crl already hold the values the sample sends, so only config/issuers may be written, and
// nothing at all while the default issuer is missing.
func TestPKIConfigWritesOnlyDifferingSections(t *testing.T) {
	ctx := context.Background()
	raw, err := os.ReadFile(filepath.Join("testdata", "pki", "pkiconfig.read.json"))
	if err != nil {
		t.Fatal(err)
	}
	reads := map[string]map[string]any{}
	if err := json.Unmarshal(raw, &reads); err != nil {
		t.Fatal(err)
	}
	a := PKIConfigAdapter{}
	cfg := &secretsv1alpha1.PKIConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "obco-pki-root", Namespace: "team"},
		Spec: secretsv1alpha1.PKIConfigSpec{ResourceSpec: pkiConnection(), MountPath: "pki",
			Issuers: &secretsv1alpha1.PKIIssuersConfig{Default: secretsv1alpha1.PKIIssuerReference{IssuerName: "root-2026"}}},
	}
	desired, err := a.Desired(ctx, pkiFakeResolver{}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	bao := &pkiFakeClient{reads: reads}
	observed, _, err := a.Observe(ctx, bao, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if diff := engine.Diff(desired, observed, a.Fields()); !slices.Equal(diff, []string{"issuers.default"}) {
		t.Fatalf("Diff = %v, want only issuers.default", diff)
	}

	if _, err := a.Write(ctx, bao, cfg, desired, false); !errors.Is(err, engine.ErrReferenceNotReady) {
		t.Errorf("Write with a missing default issuer: %v, want ErrReferenceNotReady", err)
	}
	if len(bao.writes) != 0 {
		t.Errorf("writes while the default issuer is missing: %v", bao.writePaths())
	}

	const id = "83e2fb6a-1b2a-5611-fc7e-41fd2d2cc042"
	reads["pki/issuer/root-2026"] = map[string]any{"issuer_id": id, "issuer_name": "root-2026"}
	if _, err := a.Write(ctx, bao, cfg, desired, false); err != nil {
		t.Fatal(err)
	}
	if got := bao.writePaths(); !slices.Equal(got, []string{"pki/config/issuers"}) {
		t.Errorf("writes = %v, want only pki/config/issuers", got)
	}

	reads["pki/config/issuers"] = map[string]any{"default": id, "default_follows_latest_issuer": false}
	observed, _, err = a.Observe(ctx, bao, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if diff := engine.Diff(desired, observed, a.Fields()); len(diff) != 0 {
		t.Errorf("settled state differs on %v", diff)
	}
}
