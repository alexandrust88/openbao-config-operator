//go:build integration

package integration

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	authcontroller "github.com/alexandrust88/openbao-config-operator/internal/controller/auth"
	"github.com/alexandrust88/openbao-config-operator/internal/controller/secrets"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// TestIntegrationConfigGaps reconciles the builtin configuration that previously had no typed kind.
func TestIntegrationConfigGaps(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e := gapsSetup(ctx, t)

	mustWrite(ctx, t, e.bao, "sys/mounts/gaps-transit", map[string]any{"type": "transit"})
	mustWrite(ctx, t, e.bao, "sys/mounts/gaps-pki", map[string]any{"type": "pki"})
	mustWrite(ctx, t, e.bao, "sys/mounts/gaps-kv", map[string]any{"type": "kv", "options": map[string]any{"version": "2"}})
	mustWrite(ctx, t, e.bao, "sys/auth/gaps-cert", map[string]any{"type": "cert"})

	t.Run("transit cache", func(t *testing.T) { gapsTransitCache(ctx, t, e) })
	t.Run("transit keys", func(t *testing.T) { gapsTransitKeys(ctx, t, e) })
	t.Run("pki cel", func(t *testing.T) { gapsPKICEL(ctx, t, e) })
	t.Run("pki key config", func(t *testing.T) { gapsPKIKeyConfig(ctx, t, e) })
	t.Run("kv metadata", func(t *testing.T) { gapsKVMetadata(ctx, t, e) })
	t.Run("cert crl", func(t *testing.T) { gapsCertCRL(ctx, t, e) })
}

func gapsTransitCache(ctx context.Context, t *testing.T, e env) {
	size := int32(100)
	cfg := &secretsv1alpha1.TransitCacheConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "gaps-cache", Namespace: namespace},
		Spec: secretsv1alpha1.TransitCacheConfigSpec{
			ResourceSpec: secretsAdoptSpec(commonv1alpha1.DeletionPolicyOrphan),
			MountPath:    "gaps-transit",
			Size:         &size,
		},
	}
	mustCreate(ctx, t, e.k8s, cfg)
	waitFor(t, "transit cache ready", func() error { return expectReady(ctx, e.k8s, cfg) })
	if err := secretsExpectFields(ctx, e.bao, "gaps-transit/cache-config", map[string]string{"size": "100"}); err != nil {
		t.Fatal(err)
	}
	secretsExpectNoWrites(t, "TransitCacheConfig")

	size = 50
	cfg.Spec.Size = &size
	if err := e.k8s.Update(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "transit cache resized", func() error {
		return secretsExpectFields(ctx, e.bao, "gaps-transit/cache-config", map[string]string{"size": "50"})
	})
	waitFor(t, "transit cache ready after resize", func() error { return expectReady(ctx, e.k8s, cfg) })
	secretsExpectNoWrites(t, "TransitCacheConfig")
}

func gapsTransitKeys(ctx context.Context, t *testing.T, e env) {
	cfg := &secretsv1alpha1.TransitKeyConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "gaps-keys", Namespace: namespace},
		Spec: secretsv1alpha1.TransitKeyConfigSpec{
			ResourceSpec:  secretsResourceSpec(commonv1alpha1.DeletionPolicyOrphan),
			MountPath:     "gaps-transit",
			DisableUpsert: new(true),
		},
	}
	mustCreate(ctx, t, e.k8s, cfg)
	waitFor(t, "transit key config ready", func() error { return expectReady(ctx, e.k8s, cfg) })
	if err := secretsExpectFields(ctx, e.bao, "gaps-transit/config/keys", map[string]string{"disable_upsert": "true"}); err != nil {
		t.Fatal(err)
	}
	secretsExpectNoWrites(t, "TransitKeyConfig")

	cfg.Spec.DisableUpsert = new(false)
	if err := e.k8s.Update(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "transit key config reset", func() error {
		return secretsExpectFields(ctx, e.bao, "gaps-transit/config/keys", map[string]string{"disable_upsert": "false"})
	})
	waitFor(t, "transit key config ready after reset", func() error { return expectReady(ctx, e.k8s, cfg) })
	secretsExpectNoWrites(t, "TransitKeyConfig")
}

func gapsPKICEL(ctx context.Context, t *testing.T, e env) {
	role := &secretsv1alpha1.PKICELRole{
		ObjectMeta: metav1.ObjectMeta{Name: "gaps-cel", Namespace: namespace},
		Spec: secretsv1alpha1.PKICELRoleSpec{
			ResourceSpec: secretsResourceSpec(commonv1alpha1.DeletionPolicyDelete),
			MountPath:    "gaps-pki",
			CELProgram:   gapsCELProgram("at least 1"),
		},
	}
	mustCreate(ctx, t, e.k8s, role)
	waitFor(t, "pki cel role ready", func() error { return expectReady(ctx, e.k8s, role) })
	if err := gapsExpectCEL(ctx, e.bao, "gaps-pki/cel/roles/gaps-cel", "at least 1"); err != nil {
		t.Fatal(err)
	}
	secretsExpectNoWrites(t, "PKICELRole")

	role.Spec.CELProgram = gapsCELProgram("at least 2")
	if err := e.k8s.Update(ctx, role); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "pki cel role updated", func() error {
		return gapsExpectCEL(ctx, e.bao, "gaps-pki/cel/roles/gaps-cel", "at least 2")
	})
	waitFor(t, "pki cel role ready after update", func() error { return expectReady(ctx, e.k8s, role) })
	secretsExpectNoWrites(t, "PKICELRole")

	if err := e.k8s.Delete(ctx, role); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "pki cel role deleted", func() error { return expectAbsent(ctx, e.bao, "gaps-pki/cel/roles/gaps-cel") })
}

func gapsPKIKeyConfig(ctx context.Context, t *testing.T, e env) {
	resp, err := e.bao.Write(ctx, "gaps-pki/keys/generate/internal", map[string]any{"key_name": "alpha", "key_type": "ec"})
	if err != nil {
		t.Fatal(err)
	}
	keyID := fmt.Sprint(resp["key_id"])
	if keyID == "" || keyID == "<nil>" {
		t.Fatalf("generated key id missing: %v", resp)
	}
	cfg := &secretsv1alpha1.PKIKeyConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "gaps-default-key", Namespace: namespace},
		Spec: secretsv1alpha1.PKIKeyConfigSpec{
			ResourceSpec: secretsAdoptSpec(commonv1alpha1.DeletionPolicyOrphan),
			MountPath:    "gaps-pki",
			DefaultKey:   "alpha",
		},
	}
	mustCreate(ctx, t, e.k8s, cfg)
	waitFor(t, "pki key config ready", func() error { return expectReady(ctx, e.k8s, cfg) })
	data, err := e.bao.Read(ctx, "gaps-pki/config/keys")
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(data["default"]); got != keyID && got != "alpha" {
		t.Fatalf("default key = %s, want %s or alpha", got, keyID)
	}
	// Generating the key can already make it the default, so the first reconcile may not write.
	before := secretsWrites(t, "PKIKeyConfig")
	stayFor(t, "PKIKeyConfig writes nothing at rest", 3*driftInterval, func() error {
		if now := secretsWrites(t, "PKIKeyConfig"); now != before {
			return fmt.Errorf("writes went from %v to %v", before, now)
		}
		return nil
	})
}

func gapsKVMetadata(ctx context.Context, t *testing.T, e env) {
	after := intstr.FromString("768h")
	meta := &secretsv1alpha1.KVSecretMetadata{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "gaps-meta",
			Namespace:   namespace,
			Annotations: secretsAllowDestructiveDelete,
		},
		Spec: secretsv1alpha1.KVSecretMetadataSpec{
			ResourceSpec:        secretsResourceSpec(commonv1alpha1.DeletionPolicyDelete),
			MountPath:           "gaps-kv",
			Path:                "team/app",
			MaxVersions:         new(int32(5)),
			CASRequired:         new(true),
			MetadataCASRequired: new(true),
			DeleteVersionAfter:  &after,
			CustomMetadata:      map[string]string{"owner": "team"},
		},
	}
	mustCreate(ctx, t, e.k8s, meta)
	waitFor(t, "kv metadata ready", func() error { return expectReady(ctx, e.k8s, meta) })
	if err := gapsExpectMetadata(ctx, e.bao, "gaps-kv/metadata/team/app", "5", "team"); err != nil {
		t.Fatal(err)
	}
	secretsExpectNoWrites(t, "KVSecretMetadata")

	meta.Spec.MaxVersions = new(int32(7))
	if err := e.k8s.Update(ctx, meta); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "kv metadata updated", func() error {
		return gapsExpectMetadata(ctx, e.bao, "gaps-kv/metadata/team/app", "7", "team")
	})
	waitFor(t, "kv metadata ready after update", func() error { return expectReady(ctx, e.k8s, meta) })
	secretsExpectNoWrites(t, "KVSecretMetadata")

	meta.Spec.CustomMetadata = map[string]string{"team": "app"}
	if err := e.k8s.Update(ctx, meta); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "removed custom metadata key", func() error {
		data, err := e.bao.Read(ctx, "gaps-kv/metadata/team/app")
		if err != nil {
			return err
		}
		custom, _ := data["custom_metadata"].(map[string]any)
		if _, ok := custom["owner"]; ok || fmt.Sprint(custom["team"]) != "app" {
			return fmt.Errorf("custom_metadata = %v", custom)
		}
		return nil
	})

	if err := e.k8s.Get(ctx, client.ObjectKeyFromObject(meta), meta); err != nil {
		t.Fatal(err)
	}
	meta.Spec.MaxVersions = nil
	meta.Spec.CASRequired = nil
	meta.Spec.MetadataCASRequired = nil
	meta.Spec.DeleteVersionAfter = nil
	meta.Spec.CustomMetadata = nil
	if err := e.k8s.Update(ctx, meta); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "metadata retained after omission", func() error { return expectReady(ctx, e.k8s, meta) })
	secretsExpectNoWrites(t, "KVSecretMetadata")
	stored, err := e.bao.Read(ctx, "gaps-kv/metadata/team/app")
	if err != nil {
		t.Fatal(err)
	}
	custom, _ := stored["custom_metadata"].(map[string]any)
	if fmt.Sprint(stored["max_versions"]) != "7" || stored["cas_required"] != true ||
		stored["metadata_cas_required"] != true || stored["delete_version_after"] != "768h0m0s" || custom["team"] != "app" {
		t.Fatal("omitting metadata settings changed stored values")
	}

	if err := e.k8s.Delete(ctx, meta); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "kv metadata deleted", func() error { return expectAbsent(ctx, e.bao, "gaps-kv/metadata/team/app") })
}

func gapsCertCRL(ctx context.Context, t *testing.T, e env) {
	crl := &authv1alpha1.CertAuthCRL{
		ObjectMeta: metav1.ObjectMeta{Name: "gaps-crl", Namespace: namespace},
		Spec: authv1alpha1.CertAuthCRLSpec{
			ResourceSpec: secretsResourceSpec(commonv1alpha1.DeletionPolicyDelete),
			MountPath:    "gaps-cert",
			Name:         "itest",
			CRL:          gapsCRLPEM(t),
		},
	}
	mustCreate(ctx, t, e.k8s, crl)
	waitFor(t, "cert crl ready", func() error { return expectReady(ctx, e.k8s, crl) })
	if _, err := e.bao.Read(ctx, "auth/gaps-cert/crls/itest"); err != nil {
		t.Fatal(err)
	}
	secretsExpectNoWrites(t, "CertAuthCRL")

	pemData := crl.Spec.CRL
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(pemData))
	}))
	t.Cleanup(server.Close)
	crl.Spec.CRL = ""
	crl.Spec.URL = server.URL
	if err := e.k8s.Update(ctx, crl); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "CRL fetched from local URL", func() error {
		data, err := e.bao.Read(ctx, "auth/gaps-cert/crls/itest")
		if err != nil {
			return err
		}
		cdp, _ := data["cdp"].(map[string]any)
		if cdp["url"] != server.URL {
			return fmt.Errorf("CRL URL not applied")
		}
		return expectReady(ctx, e.k8s, crl)
	})
	secretsExpectNoWrites(t, "CertAuthCRL")

	if err := e.k8s.Delete(ctx, crl); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "cert crl deleted", func() error {
		if err := e.k8s.Get(ctx, client.ObjectKeyFromObject(crl), crl); !apierrors.IsNotFound(err) {
			return fmt.Errorf("resource still present: %v", err)
		}
		_, err := e.bao.Read(ctx, "auth/gaps-cert/crls/itest")
		re, ok := errors.AsType[*openbao.ResponseError](err)
		if !ok || re.StatusCode != http.StatusBadRequest {
			return fmt.Errorf("crl read = %v", err)
		}
		return nil
	})
}

func gapsCELProgram(phrase string) secretsv1alpha1.PKICELProgram {
	return secretsv1alpha1.PKICELProgram{
		Expression: "success ? output : err",
		Variables: []secretsv1alpha1.PKICELVariable{
			{Name: "require_ip_sans", Expression: "size(request.ip_sans) > 0"},
			{Name: "success", Expression: "request.common_name == 'example.com' && require_ip_sans"},
			{Name: "cert", Expression: `CertTemplate{Subject: PKIX.Name{CommonName: request.common_name, Country: ["ZW", "US"]}}`},
			{Name: "output", Expression: "ValidationOutput{template: cert}"},
			{Name: "err", Expression: "'" + phrase + "'"},
		},
	}
}

func gapsExpectCEL(ctx context.Context, bao openbao.Client, path, phrase string) error {
	data, err := bao.Read(ctx, path)
	if err != nil {
		return err
	}
	prog, _ := data["cel_program"].(map[string]any)
	vars, _ := prog["variables"].([]any)
	for _, v := range vars {
		m, _ := v.(map[string]any)
		if m["name"] == "err" && m["expression"] == "'"+phrase+"'" {
			return nil
		}
	}
	return fmt.Errorf("cel program %v does not contain %s", prog, phrase)
}

func gapsExpectMetadata(ctx context.Context, bao openbao.Client, path, versions, owner string) error {
	data, err := bao.Read(ctx, path)
	if err != nil {
		return err
	}
	if fmt.Sprint(data["max_versions"]) != versions || fmt.Sprint(data["cas_required"]) != "true" || fmt.Sprint(data["metadata_cas_required"]) != "true" {
		return fmt.Errorf("metadata = %v", data)
	}
	if fmt.Sprint(data["delete_version_after"]) != "768h0m0s" {
		return fmt.Errorf("delete_version_after = %v", data["delete_version_after"])
	}
	custom, _ := data["custom_metadata"].(map[string]any)
	if fmt.Sprint(custom["owner"]) != owner {
		return fmt.Errorf("custom_metadata = %v", data["custom_metadata"])
	}
	return nil
}

func gapsCRLPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "gaps"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	crl, err := x509.CreateRevocationList(rand.Reader, &x509.RevocationList{
		Number:     big.NewInt(1),
		ThisUpdate: time.Now(),
		NextUpdate: time.Now().Add(time.Hour),
	}, cert, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "X509 CRL", Bytes: crl}))
}

func gapsSetup(ctx context.Context, t *testing.T) env {
	t.Helper()
	root := repoRoot(t)
	baoBin, assets := secretsPrerequisites(t, root)
	addr := startBao(t, baoBin)
	bao, err := openbao.NewPool().Client(ctx, "itest-gaps", openbao.Config{
		Address: addr,
		Timeout: 10 * time.Second,
		Auth: openbao.TokenAuth{
			CredentialKey: "itest-gaps",
			Token:         func(context.Context) (string, error) { return rootToken, nil },
		},
	})
	if err != nil {
		t.Fatalf("openbao client: %v", err)
	}

	crds := make([]string, 0, 6)
	for _, name := range []string{
		"auth.openbaoconfig.io_certauthcrls.yaml",
		"secrets.openbaoconfig.io_kvsecretmetadata.yaml",
		"secrets.openbaoconfig.io_pkicelroles.yaml",
		"secrets.openbaoconfig.io_pkikeyconfigs.yaml",
		"secrets.openbaoconfig.io_transitcacheconfigs.yaml",
		"secrets.openbaoconfig.io_transitkeyconfigs.yaml",
	} {
		crds = append(crds, filepath.Join(root, "config", "crd", "bases", name))
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
	for _, add := range []func(*runtime.Scheme) error{
		clientgoscheme.AddToScheme, secretsv1alpha1.AddToScheme, authv1alpha1.AddToScheme,
	} {
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
		secrets.SetupKVSecretMetadata,
		secrets.SetupPKICELRole,
		secrets.SetupPKIKeyConfig,
		secrets.SetupTransitCacheConfig,
		secrets.SetupTransitKeyConfig,
		authcontroller.SetupCertAuthCRL,
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
