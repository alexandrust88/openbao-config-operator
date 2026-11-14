//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
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
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// TestIntegrationPKI builds a root CA, an intermediate signed by it in another mount, the mount settings, a
// role, the ACME and auto-tidy settings and a standalone key against `bao server -dev`.
func TestIntegrationPKI(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e := pkiSetup(ctx, t)

	mustWrite(ctx, t, e.bao, "sys/mounts/pki", map[string]any{"type": "pki", "config": map[string]any{"max_lease_ttl": "87600h"}})
	mustWrite(ctx, t, e.bao, "sys/mounts/pki-int", map[string]any{"type": "pki", "config": map[string]any{"max_lease_ttl": "43800h"}})

	root := pkiTestRoot(ctx, t, e)
	intermediate := pkiTestIntermediate(ctx, t, e)
	pkiTestConfig(ctx, t, e)
	role := pkiTestRole(ctx, t, e)
	pkiTestACME(ctx, t, e)
	pkiTestAutoTidy(ctx, t, e)
	pkiTestKey(ctx, t, e)

	t.Run("delete", func(t *testing.T) {
		if err := e.k8s.Delete(ctx, role); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "role deleted", func() error { return expectAbsent(ctx, e.bao, "pki-int/roles/itest-web") })

		if err := e.k8s.Delete(ctx, intermediate); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "intermediate deleted", func() error { return pkiExpectMissing(ctx, e.bao, "pki-int/issuer/itest-int") })
		waitFor(t, "intermediate key deleted", func() error {
			return pkiExpectMissing(ctx, e.bao, "pki-int/key/"+intermediate.Status.KeyID)
		})

		if err := e.k8s.Delete(ctx, root); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "root resource gone", func() error { return expectGone(ctx, e.k8s, root) })
		if _, err := e.bao.Read(ctx, "pki/issuer/itest-root"); err != nil {
			t.Errorf("orphaned root issuer: %v", err)
		}
	})
}

func pkiTestRoot(ctx context.Context, t *testing.T, e env) *secretsv1alpha1.PKIIssuer {
	root := &secretsv1alpha1.PKIIssuer{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-root", Namespace: namespace},
		Spec: secretsv1alpha1.PKIIssuerSpec{
			ResourceSpec: pkiSpec(),
			Type:         secretsv1alpha1.PKIIssuerTypeRoot,
			Key:          &secretsv1alpha1.PKIIssuerKey{Name: "itest-root-key", Type: "ec", Bits: new(int32(256))},
			Certificate: secretsv1alpha1.PKIIssuerCertificate{
				CommonName: "itest root", Organization: []string{"itest"}, TTL: &metav1.Duration{Duration: 8760 * time.Hour},
			},
			LeafNotAfterBehavior: "truncate",
			Usage:                []string{"issuing-certificates", "crl-signing", "ocsp-signing"},
		},
	}
	t.Run("root", func(t *testing.T) {
		mustCreate(ctx, t, e.k8s, root)
		waitFor(t, "root ready", func() error { return expectReady(ctx, e.k8s, root) })
		waitFor(t, "root generated", func() error {
			return pkiExpectFields(ctx, e.bao, "pki/issuer/itest-root", map[string]any{"leaf_not_after_behavior": "truncate"})
		})
		if root.Status.ID == "" || root.Status.KeyID == "" {
			t.Fatalf("status = %+v", root.Status)
		}

		mustWrite(ctx, t, e.bao, "pki/issuer/itest-root", map[string]any{"issuer_name": "itest-root", "leaf_not_after_behavior": "permit"})
		waitFor(t, "root drift repaired", func() error {
			return pkiExpectFields(ctx, e.bao, "pki/issuer/itest-root", map[string]any{"leaf_not_after_behavior": "truncate"})
		})

		byID := "pki/issuer/" + root.Status.ID
		mustWrite(ctx, t, e.bao, byID, map[string]any{"leaf_not_after_behavior": "permit"})
		if err := pkiExpectFields(ctx, e.bao, byID, map[string]any{"issuer_name": ""}); err != nil {
			t.Fatalf("out-of-band write without issuer_name did not clear the name: %v", err)
		}
		waitFor(t, "root name and setting repaired", func() error {
			return pkiExpectFields(ctx, e.bao, "pki/issuer/itest-root",
				map[string]any{"issuer_name": "itest-root", "leaf_not_after_behavior": "truncate"})
		})
		pkiExpectNoWrites(t, "PKIIssuer", func() error {
			return pkiExpectFields(ctx, e.bao, "pki/issuer/itest-root", map[string]any{"leaf_not_after_behavior": "truncate"})
		})
	})
	return root
}

func pkiTestIntermediate(ctx context.Context, t *testing.T, e env) *secretsv1alpha1.PKIIssuer {
	intermediate := &secretsv1alpha1.PKIIssuer{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-int", Namespace: namespace},
		Spec: secretsv1alpha1.PKIIssuerSpec{
			ResourceSpec: pkiSpec(),
			MountPath:    "pki-int",
			Type:         secretsv1alpha1.PKIIssuerTypeIntermediate,
			Key: &secretsv1alpha1.PKIIssuerKey{
				Source: secretsv1alpha1.PKIKeySourceExported, Type: "ec", Bits: new(int32(256)), PrivateKeyFormat: "pkcs8",
			},
			Certificate:    secretsv1alpha1.PKIIssuerCertificate{CommonName: "itest intermediate", TTL: &metav1.Duration{Duration: 4380 * time.Hour}},
			Signer:         &secretsv1alpha1.PKIIssuerSigner{IssuerRef: &commonv1alpha1.LocalObjectReference{Name: "itest-root"}},
			WriteSecretRef: &commonv1alpha1.LocalObjectReference{Name: "itest-int-key"},
		},
	}
	intermediate.Spec.DeletionPolicy = commonv1alpha1.DeletionPolicyDelete
	t.Run("intermediate", func(t *testing.T) {
		mustCreate(ctx, t, e.k8s, intermediate)
		waitFor(t, "intermediate ready", func() error { return expectReady(ctx, e.k8s, intermediate) })
		if intermediate.Status.ID == "" || intermediate.Status.KeyID == "" || intermediate.Status.CSR != "" {
			t.Fatalf("status = %+v", intermediate.Status)
		}
		data, err := e.bao.Read(ctx, "pki-int/issuer/itest-int")
		if err != nil {
			t.Fatal(err)
		}
		if chain, _ := data["ca_chain"].([]any); len(chain) < 2 || data["issuer_id"] != intermediate.Status.ID {
			t.Errorf("intermediate issuer: id %v, chain length %d", data["issuer_id"], len(chain))
		}
		secret := &corev1.Secret{}
		if err := e.k8s.Get(ctx, client.ObjectKey{Namespace: namespace, Name: "itest-int-key"}, secret); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(secret.Data["private_key"]), "PRIVATE KEY") {
			t.Error("output Secret has no private key")
		}
		pkiExpectNoWrites(t, "PKIIssuer", func() error { return expectReady(ctx, e.k8s, intermediate) })
	})
	return intermediate
}

func pkiTestConfig(ctx context.Context, t *testing.T, e env) {
	cfg := &secretsv1alpha1.PKIConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-pki-int", Namespace: namespace},
		Spec: secretsv1alpha1.PKIConfigSpec{
			ResourceSpec: pkiSpec(),
			MountPath:    "pki-int",
			Cluster:      &secretsv1alpha1.PKIClusterConfig{Path: "http://127.0.0.1:8200/v1/pki-int"},
			URLs:         &secretsv1alpha1.PKIURLsConfig{IssuingCertificates: []string{"http://127.0.0.1:8200/v1/pki-int/ca"}},
			CRL:          &secretsv1alpha1.PKICRLConfig{Expiry: &metav1.Duration{Duration: 48 * time.Hour}},
			Issuers: &secretsv1alpha1.PKIIssuersConfig{
				Default: secretsv1alpha1.PKIIssuerReference{IssuerRef: &commonv1alpha1.LocalObjectReference{Name: "itest-int"}},
			},
		},
	}
	t.Run("config", func(t *testing.T) {
		mustCreate(ctx, t, e.k8s, cfg)
		waitFor(t, "config ready", func() error { return expectReady(ctx, e.k8s, cfg) })
		waitFor(t, "cluster written", func() error {
			return pkiExpectFields(ctx, e.bao, "pki-int/config/cluster", map[string]any{"path": "http://127.0.0.1:8200/v1/pki-int"})
		})
		waitFor(t, "urls written", func() error {
			return pkiExpectFields(ctx, e.bao, "pki-int/config/urls", map[string]any{"issuing_certificates": []any{"http://127.0.0.1:8200/v1/pki-int/ca"}})
		})

		mustWrite(ctx, t, e.bao, "pki-int/config/crl", map[string]any{"expiry": "72h"})
		waitFor(t, "crl drift repaired", func() error { return pkiExpectCRLExpiry(ctx, e.bao, 48*time.Hour) })

		mustWrite(ctx, t, e.bao, "pki-int/config/crl", map[string]any{"expiry": "2880m"})
		pkiExpectNoWrites(t, "PKIConfig", func() error {
			return pkiExpectFields(ctx, e.bao, "pki-int/config/crl", map[string]any{"expiry": "2880m"})
		})

		if err := e.k8s.Delete(ctx, cfg); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "config resource gone", func() error { return expectGone(ctx, e.k8s, cfg) })
		if err := pkiExpectCRLExpiry(ctx, e.bao, 48*time.Hour); err != nil {
			t.Errorf("orphaned config: %v", err)
		}
	})
}

func pkiTestRole(ctx context.Context, t *testing.T, e env) *secretsv1alpha1.PKIRole {
	role := &secretsv1alpha1.PKIRole{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-web", Namespace: namespace},
		Spec: secretsv1alpha1.PKIRoleSpec{
			ResourceSpec:    pkiSpec(),
			MountPath:       "pki-int",
			Issuer:          &secretsv1alpha1.PKIIssuerReference{IssuerRef: &commonv1alpha1.LocalObjectReference{Name: "itest-int"}},
			AllowedDomains:  []string{"example.com", "example.org"},
			AllowSubdomains: new(true),
			MaxTTL:          &metav1.Duration{Duration: 72 * time.Hour},
			KeyType:         "ec",
			KeyBits:         new(int32(256)),
		},
	}
	role.Spec.DeletionPolicy = commonv1alpha1.DeletionPolicyDelete
	t.Run("role", func(t *testing.T) {
		intermediate := &secretsv1alpha1.PKIIssuer{}
		if err := e.k8s.Get(ctx, client.ObjectKey{Namespace: namespace, Name: "itest-int"}, intermediate); err != nil {
			t.Fatal(err)
		}
		mustCreate(ctx, t, e.k8s, role)
		want := map[string]any{"issuer_ref": intermediate.Status.ID, "allowed_domains": []any{"example.com", "example.org"}, "max_ttl": float64(259200)}
		waitFor(t, "role created", func() error { return pkiExpectFields(ctx, e.bao, "pki-int/roles/itest-web", want) })
		waitFor(t, "role ready", func() error { return expectReady(ctx, e.k8s, role) })

		body := map[string]any{"issuer_ref": intermediate.Status.ID, "allowed_domains": []any{"evil.example"},
			"allow_subdomains": true, "max_ttl": "72h", "key_type": "ec", "key_bits": 256}
		mustWrite(ctx, t, e.bao, "pki-int/roles/itest-web", body)
		waitFor(t, "role drift repaired", func() error { return pkiExpectFields(ctx, e.bao, "pki-int/roles/itest-web", want) })

		body["allowed_domains"], body["max_ttl"] = []any{"example.org", "example.com"}, "4320m"
		mustWrite(ctx, t, e.bao, "pki-int/roles/itest-web", body)
		pkiExpectNoWrites(t, "PKIRole", func() error {
			return pkiExpectFields(ctx, e.bao, "pki-int/roles/itest-web", map[string]any{"allowed_domains": []any{"example.org", "example.com"}})
		})
	})
	return role
}

func pkiTestACME(ctx context.Context, t *testing.T, e env) {
	acme := &secretsv1alpha1.PKIACMEConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-acme", Namespace: namespace},
		Spec: secretsv1alpha1.PKIACMEConfigSpec{
			ResourceSpec:           pkiSpec(),
			MountPath:              "pki-int",
			Enabled:                new(true),
			AllowedIssuers:         []string{"*"},
			AllowedRoles:           []string{"itest-web"},
			DefaultDirectoryPolicy: "role:itest-web",
			EABPolicy:              "not-required",
		},
	}
	want := map[string]any{"enabled": true, "allowed_roles": []any{"itest-web"}, "default_directory_policy": "role:itest-web"}
	t.Run("acme", func(t *testing.T) {
		mustCreate(ctx, t, e.k8s, acme)
		waitFor(t, "acme written", func() error { return pkiExpectFields(ctx, e.bao, "pki-int/config/acme", want) })
		waitFor(t, "acme ready", func() error { return expectReady(ctx, e.k8s, acme) })

		mustWrite(ctx, t, e.bao, "pki-int/config/acme", map[string]any{"enabled": false})
		waitFor(t, "acme drift repaired", func() error { return pkiExpectFields(ctx, e.bao, "pki-int/config/acme", want) })
		pkiExpectNoWrites(t, "PKIACMEConfig", func() error { return pkiExpectFields(ctx, e.bao, "pki-int/config/acme", want) })

		if err := e.k8s.Delete(ctx, acme); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "acme resource gone", func() error { return expectGone(ctx, e.k8s, acme) })
		if err := pkiExpectFields(ctx, e.bao, "pki-int/config/acme", want); err != nil {
			t.Errorf("orphaned acme config: %v", err)
		}
	})
}

func pkiTestAutoTidy(ctx context.Context, t *testing.T, e env) {
	tidy := &secretsv1alpha1.PKIAutoTidy{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-tidy", Namespace: namespace},
		Spec: secretsv1alpha1.PKIAutoTidySpec{
			ResourceSpec:     pkiSpec(),
			MountPath:        "pki-int",
			Enabled:          new(true),
			IntervalDuration: &metav1.Duration{Duration: time.Hour},
			TidyCertStore:    new(true),
			SafetyBuffer:     &metav1.Duration{Duration: 24 * time.Hour},
			PauseDuration:    &metav1.Duration{Duration: time.Second},
		},
	}
	want := map[string]any{"enabled": true, "interval_duration": float64(3600), "safety_buffer": float64(86400), "pause_duration": "1s"}
	t.Run("autotidy", func(t *testing.T) {
		mustCreate(ctx, t, e.k8s, tidy)
		waitFor(t, "auto-tidy written", func() error { return pkiExpectFields(ctx, e.bao, "pki-int/config/auto-tidy", want) })
		waitFor(t, "auto-tidy ready", func() error { return expectReady(ctx, e.k8s, tidy) })

		mustWrite(ctx, t, e.bao, "pki-int/config/auto-tidy", map[string]any{"interval_duration": 7200})
		waitFor(t, "auto-tidy drift repaired", func() error { return pkiExpectFields(ctx, e.bao, "pki-int/config/auto-tidy", want) })
		pkiExpectNoWrites(t, "PKIAutoTidy", func() error { return pkiExpectFields(ctx, e.bao, "pki-int/config/auto-tidy", want) })

		if err := e.k8s.Delete(ctx, tidy); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "auto-tidy resource gone", func() error { return expectGone(ctx, e.k8s, tidy) })
		if err := pkiExpectFields(ctx, e.bao, "pki-int/config/auto-tidy", want); err != nil {
			t.Errorf("orphaned auto-tidy: %v", err)
		}
	})
}

func pkiTestKey(ctx context.Context, t *testing.T, e env) {
	key := &secretsv1alpha1.PKIKey{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-key", Namespace: namespace},
		Spec: secretsv1alpha1.PKIKeySpec{
			ResourceSpec:   pkiSpec(),
			Type:           secretsv1alpha1.PKIKeyCreationExported,
			KeyType:        "ec",
			KeyBits:        new(int32(384)),
			WriteSecretRef: &commonv1alpha1.LocalObjectReference{Name: "itest-key-out"},
		},
	}
	key.Spec.DeletionPolicy = commonv1alpha1.DeletionPolicyDelete
	t.Run("key", func(t *testing.T) {
		mustCreate(ctx, t, e.k8s, key)
		waitFor(t, "key ready", func() error { return expectReady(ctx, e.k8s, key) })
		if err := pkiExpectFields(ctx, e.bao, "pki/key/itest-key", map[string]any{"key_type": "ec", "key_id": key.Status.ID}); err != nil {
			t.Fatal(err)
		}
		secret := &corev1.Secret{}
		if err := e.k8s.Get(ctx, client.ObjectKey{Namespace: namespace, Name: "itest-key-out"}, secret); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(secret.Data["private_key"]), "PRIVATE KEY") {
			t.Error("output Secret has no private key")
		}

		mustWrite(ctx, t, e.bao, "pki/key/"+key.Status.ID, map[string]any{"key_name": "renamed"})
		waitFor(t, "key name repaired", func() error {
			return pkiExpectFields(ctx, e.bao, "pki/key/"+key.Status.ID, map[string]any{"key_name": "itest-key"})
		})
		pkiExpectNoWrites(t, "PKIKey", func() error { return expectReady(ctx, e.k8s, key) })

		if err := e.k8s.Delete(ctx, key); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "key deleted", func() error { return pkiExpectMissing(ctx, e.bao, "pki/key/itest-key") })
	})
}

func pkiSpec() commonv1alpha1.ResourceSpec {
	return commonv1alpha1.ResourceSpec{ConnectionRef: commonv1alpha1.ConnectionReference{Name: "dev"}}
}

// pkiExpectNoWrites asserts that the kind writes nothing for three drift intervals while check keeps passing.
func pkiExpectNoWrites(t *testing.T, kind string, check func() error) {
	t.Helper()
	before := writesFor(t, kind)
	stayFor(t, kind+" at rest", 3*driftInterval, check)
	if after := writesFor(t, kind); after != before {
		t.Errorf("%s wrote %v times at rest", kind, after-before)
	}
}

func pkiExpectFields(ctx context.Context, bao openbao.Client, path string, want map[string]any) error {
	data, err := bao.Read(ctx, path)
	if err != nil {
		return err
	}
	for key, value := range want {
		got := data[key]
		if n, ok := got.(interface{ Float64() (float64, error) }); ok {
			got, _ = n.Float64()
		}
		if !reflect.DeepEqual(got, value) {
			return fmt.Errorf("%s: %s = %#v, want %#v", path, key, got, value)
		}
	}
	return nil
}

func pkiExpectCRLExpiry(ctx context.Context, bao openbao.Client, want time.Duration) error {
	data, err := bao.Read(ctx, "pki-int/config/crl")
	if err != nil {
		return err
	}
	raw, _ := data["expiry"].(string)
	if got, err := time.ParseDuration(raw); err != nil || got != want {
		return fmt.Errorf("crl expiry = %q, want %s", raw, want)
	}
	return nil
}

// pkiExpectMissing accepts the 500 "unable to find PKI ... for reference" that OpenBao returns for a missing
// issuer or key, as well as a 404.
func pkiExpectMissing(ctx context.Context, bao openbao.Client, path string) error {
	_, err := bao.Read(ctx, path)
	if errors.Is(err, openbao.ErrNotFound) {
		return nil
	}
	var response *openbao.ResponseError
	if errors.As(err, &response) && strings.Contains(strings.Join(response.Errors, " "), "unable to find PKI") {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("%s still exists", path)
}

func pkiSetup(ctx context.Context, t *testing.T) env {
	t.Helper()
	root := repoRoot(t)
	baoBin, assets := secretsPrerequisites(t, root)

	addr := startBao(t, baoBin)
	bao, err := openbao.NewPool().Client(ctx, "itest-pki", openbao.Config{
		Address: addr,
		Timeout: 10 * time.Second,
		Auth: openbao.TokenAuth{
			CredentialKey: "itest-pki",
			Token:         func(context.Context) (string, error) { return rootToken, nil },
		},
	})
	if err != nil {
		t.Fatalf("openbao client: %v", err)
	}

	crds := make([]string, 0, 6)
	for _, name := range []string{"pkiissuers", "pkikeys", "pkiroles", "pkiconfigs", "pkiacmeconfigs", "pkiautotidies"} {
		crds = append(crds, filepath.Join(root, "config", "crd", "bases", "secrets.openbaoconfig.io_"+name+".yaml"))
	}
	testEnv := &envtest.Environment{
		BinaryAssetsDirectory: assets,
		CRDInstallOptions:     envtest.CRDInstallOptions{Paths: crds, ErrorIfPathMissing: true},
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
	conns := staticConnections{client: bao}
	opts := itestEngineOptions()
	for _, setupKind := range []func(ctrl.Manager, engine.Connections, engine.Options) error{
		secrets.SetupPKIIssuer, secrets.SetupPKIKey, secrets.SetupPKIRole, secrets.SetupPKIConfig,
		secrets.SetupPKIACMEConfig, secrets.SetupPKIAutoTidy,
	} {
		if err := setupKind(mgr, conns, opts); err != nil {
			t.Fatal(err)
		}
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
	return env{k8s: mgr.GetClient(), bao: bao}
}
