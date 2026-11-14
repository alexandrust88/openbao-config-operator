//go:build integration

package integration

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/controller/secrets"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// TestIntegrationPKIConfigWrites applies the two PKIConfig samples to mounts whose default issuer does not exist
// yet and counts every successful write from create to settled: none while the issuer is missing, then one per
// section that differs from the server.
func TestIntegrationPKIConfigWrites(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	k8s, bao, writes := pkiConfigWritesSetup(ctx, t)

	mustWrite(ctx, t, bao, "sys/mounts/cfg-root", map[string]any{"type": "pki"})
	mustWrite(ctx, t, bao, "sys/mounts/cfg-int", map[string]any{"type": "pki"})

	rootStyle := &secretsv1alpha1.PKIConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-cfg-root", Namespace: namespace},
		Spec: secretsv1alpha1.PKIConfigSpec{
			ResourceSpec: pkiSpec(),
			MountPath:    "cfg-root",
			Issuers:      &secretsv1alpha1.PKIIssuersConfig{Default: secretsv1alpha1.PKIIssuerReference{IssuerName: "root-2026"}},
		},
	}
	intStyle := &secretsv1alpha1.PKIConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-cfg-int", Namespace: namespace},
		Spec: secretsv1alpha1.PKIConfigSpec{
			ResourceSpec: pkiSpec(),
			MountPath:    "cfg-int",
			Cluster: &secretsv1alpha1.PKIClusterConfig{
				Path: "https://openbao.example.com/v1/cfg-int", AIAPath: "http://openbao.example.com/v1/cfg-int",
			},
			URLs: &secretsv1alpha1.PKIURLsConfig{
				IssuingCertificates:        []string{"{{cluster_aia_path}}/issuer/{{issuer_id}}/der"},
				CRLDistributionPoints:      []string{"{{cluster_aia_path}}/issuer/{{issuer_id}}/crl/der"},
				DeltaCRLDistributionPoints: []string{"{{cluster_aia_path}}/issuer/{{issuer_id}}/crl/delta/der"},
				OCSPServers:                []string{"{{cluster_path}}/ocsp"},
				EnableTemplating:           new(true),
			},
			CRL: &secretsv1alpha1.PKICRLConfig{
				Expiry: &metav1.Duration{Duration: 72 * time.Hour}, AutoRebuild: new(true), EnableDelta: new(true),
				DeltaRebuildInterval: &metav1.Duration{Duration: 15 * time.Minute},
			},
			Issuers: &secretsv1alpha1.PKIIssuersConfig{
				Default: secretsv1alpha1.PKIIssuerReference{IssuerName: "int-2026"}, DefaultFollowsLatestIssuer: new(false),
			},
		},
	}
	before := writesFor(t, "PKIConfig")
	mustCreate(ctx, t, k8s, rootStyle)
	mustCreate(ctx, t, k8s, intStyle)
	for _, cfg := range []*secretsv1alpha1.PKIConfig{rootStyle, intStyle} {
		waitFor(t, cfg.Name+" waits for its issuer", func() error { return pkiExpectSynced(ctx, k8s, cfg, commonv1alpha1.ReasonReferenceNotReady) })
	}
	time.Sleep(3 * time.Second)
	if got := writes.paths(); len(got) != 0 {
		t.Errorf("writes while the default issuer is missing: %v", got)
	}

	mustWrite(ctx, t, bao, "cfg-root/root/generate/internal", map[string]any{"common_name": "root", "issuer_name": "root-2026", "key_type": "ec"})
	mustWrite(ctx, t, bao, "cfg-int/root/generate/internal", map[string]any{"common_name": "first", "issuer_name": "first", "key_type": "ec"})
	mustWrite(ctx, t, bao, "cfg-int/root/generate/internal", map[string]any{"common_name": "int", "issuer_name": "int-2026", "key_type": "ec"})
	for _, cfg := range []*secretsv1alpha1.PKIConfig{rootStyle, intStyle} {
		waitFor(t, cfg.Name+" ready", func() error { return expectReady(ctx, k8s, cfg) })
	}
	stayFor(t, "PKIConfig at rest", 3*driftInterval, func() error { return expectReady(ctx, k8s, intStyle) })

	want := []string{"cfg-int/config/cluster", "cfg-int/config/urls", "cfg-int/config/crl", "cfg-int/config/issuers"}
	if got := writes.paths(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("writes from create to settled = %v, want %v", got, want)
	}
	if got := writesFor(t, "PKIConfig") - before; got != 1 {
		t.Errorf("openbao_writes_total{kind=PKIConfig} grew by %v, want 1", got)
	}
}

func pkiExpectSynced(ctx context.Context, k8s client.Client, cfg *secretsv1alpha1.PKIConfig, reason string) error {
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(cfg), cfg); err != nil {
		return err
	}
	synced := meta.FindStatusCondition(cfg.Status.Conditions, commonv1alpha1.ConditionSynced)
	if synced == nil || synced.Reason != reason {
		return fmt.Errorf("synced = %+v, want reason %s", synced, reason)
	}
	return nil
}

// pkiWriteLog wraps a client and records the path of every successful write, as a file audit device would.
type pkiWriteLog struct {
	openbao.Client
	mu      *sync.Mutex
	written *[]string
}

func (l pkiWriteLog) Write(ctx context.Context, path string, data map[string]any) (map[string]any, error) {
	response, err := l.Client.Write(ctx, path, data)
	if err == nil {
		l.mu.Lock()
		*l.written = append(*l.written, path)
		l.mu.Unlock()
	}
	return response, err
}

func (l pkiWriteLog) WithNamespace(ns string) openbao.Client {
	return pkiWriteLog{Client: l.Client.WithNamespace(ns), mu: l.mu, written: l.written}
}

func (l pkiWriteLog) paths() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), *l.written...)
}

func pkiConfigWritesSetup(ctx context.Context, t *testing.T) (client.Client, openbao.Client, pkiWriteLog) {
	t.Helper()
	root := repoRoot(t)
	baoBin, assets := secretsPrerequisites(t, root)
	addr := startBao(t, baoBin)
	bao, err := openbao.NewPool().Client(ctx, "itest-pki-config", openbao.Config{
		Address: addr,
		Timeout: 10 * time.Second,
		Auth: openbao.TokenAuth{
			CredentialKey: "itest-pki-config",
			Token:         func(context.Context) (string, error) { return rootToken, nil },
		},
	})
	if err != nil {
		t.Fatalf("openbao client: %v", err)
	}
	writes := pkiWriteLog{Client: bao, mu: &sync.Mutex{}, written: &[]string{}}

	testEnv := &envtest.Environment{
		BinaryAssetsDirectory: assets,
		CRDInstallOptions: envtest.CRDInstallOptions{ErrorIfPathMissing: true, Paths: []string{
			filepath.Join(root, "config", "crd", "bases", "secrets.openbaoconfig.io_pkiconfigs.yaml"),
		}},
	}
	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("envtest: %v", err)
	}
	t.Cleanup(func() {
		if err := testEnv.Stop(); err != nil {
			t.Logf("stop envtest: %v", err)
		}
	})
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, secretsv1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		Controller:             config.Controller{SkipNameValidation: new(true)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := secrets.SetupPKIConfig(mgr, staticConnections{client: writes}, itestEngineOptions()); err != nil {
		t.Fatal(err)
	}
	mgrCtx, stopMgr := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- mgr.Start(mgrCtx) }()
	t.Cleanup(func() {
		stopMgr()
		if err := <-done; err != nil {
			t.Logf("manager: %v", err)
		}
	})
	return mgr.GetClient(), bao, writes
}
