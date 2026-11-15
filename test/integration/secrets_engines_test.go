//go:build integration

package integration

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 default algorithm, used only to check a TOTP code
	"crypto/x509"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
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

// TestIntegrationSecretsEngines runs the TransitKey, SSHCA, SSHRole and TOTPKey controllers against bao server -dev.
func TestIntegrationSecretsEngines(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e := secretsEnginesSetup(ctx, t)

	for mount, kind := range map[string]string{"transit": "transit", "ssh": "ssh", "itest-ssh-imported": "ssh", "totp": "totp"} {
		mustWrite(ctx, t, e.bao, "sys/mounts/"+mount, map[string]any{"type": kind})
	}

	t.Run("transitkey", func(t *testing.T) { testTransitKey(ctx, t, e) })
	t.Run("sshca", func(t *testing.T) { testSSHCA(ctx, t, e) })
	t.Run("sshca-imported", func(t *testing.T) { testSSHCAImported(ctx, t, e) })
	t.Run("sshrole", func(t *testing.T) { testSSHRole(ctx, t, e) })
	t.Run("totpkey", func(t *testing.T) { testTOTPKey(ctx, t, e) })
	t.Run("totpkey-imported", func(t *testing.T) { testTOTPKeyImported(ctx, t, e) })
}

func testTransitKey(ctx context.Context, t *testing.T, e env) {
	const path = "transit/keys/itest-app"
	key := &secretsv1alpha1.TransitKey{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-app", Namespace: namespace},
		Spec: secretsv1alpha1.TransitKeySpec{
			ResourceSpec:         secretsResourceSpec(commonv1alpha1.DeletionPolicyDelete),
			MountPath:            "transit",
			Type:                 "aes256-gcm96",
			AutoRotatePeriod:     &metav1.Duration{Duration: 24 * time.Hour},
			MinDecryptionVersion: ptr.To[int32](1),
			DeletionAllowed:      new(false),
			RotateNonce:          "first",
		},
	}
	mustCreate(ctx, t, e.k8s, key)
	want := map[string]any{"type": "aes256-gcm96", "auto_rotate_period": "86400", "latest_version": "1", "deletion_allowed": false}
	waitFor(t, "transit key created", func() error { return engineExpectFields(ctx, e.bao, path, want) })
	waitFor(t, "transit key ready", func() error { return expectReady(ctx, e.k8s, key) })

	mustWrite(ctx, t, e.bao, path+"/config", map[string]any{"auto_rotate_period": "48h"})
	waitFor(t, "transit config drift repaired", func() error { return engineExpectFields(ctx, e.bao, path, want) })
	mustWrite(ctx, t, e.bao, path+"/config", map[string]any{"auto_rotate_period": "1440m"})
	secretsExpectNoWrites(t, "TransitKey")

	if err := e.k8s.Get(ctx, client.ObjectKeyFromObject(key), key); err != nil {
		t.Fatal(err)
	}
	key.Spec.RotateNonce = "second"
	if err := e.k8s.Update(ctx, key); err != nil {
		t.Fatal(err)
	}
	want["latest_version"] = "2"
	waitFor(t, "transit key rotated once", func() error { return engineExpectFields(ctx, e.bao, path, want) })
	waitFor(t, "rotation recorded", func() error {
		if err := e.k8s.Get(ctx, client.ObjectKeyFromObject(key), key); err != nil {
			return err
		}
		if key.Status.RotatedNonce != "second" || key.Status.LatestVersion != 2 {
			return fmt.Errorf("status = %+v", key.Status)
		}
		return nil
	})
	stayFor(t, "no second rotation", 3*driftInterval, func() error { return engineExpectFields(ctx, e.bao, path, want) })

	if err := e.k8s.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "transit key delete guarded", func() error {
		return expectSynced(ctx, e.k8s, key, commonv1alpha1.ReasonDeleteGuarded, commonv1alpha1.AnnotationAllowDestructiveDelete)
	})
	if err := engineExpectFields(ctx, e.bao, path, want); err != nil {
		t.Fatalf("guarded transit key changed: %v", err)
	}
	patch := client.MergeFrom(key.DeepCopy())
	key.Annotations = secretsAllowDestructiveDelete
	if err := e.k8s.Patch(ctx, key, patch); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "transit key deleted", func() error { return expectAbsent(ctx, e.bao, path) })
	waitFor(t, "transit key resource gone", func() error { return expectGone(ctx, e.k8s, key) })
}

func testSSHCA(ctx context.Context, t *testing.T, e env) {
	const path = "ssh/config/ca"
	ca := &secretsv1alpha1.SSHCA{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-ca", Namespace: namespace},
		Spec: secretsv1alpha1.SSHCASpec{
			ResourceSpec: secretsResourceSpec(""),
			MountPath:    "ssh",
			KeyType:      "ssh-ed25519",
		},
	}
	mustCreate(ctx, t, e.k8s, ca)
	waitFor(t, "ssh ca ready", func() error { return expectReady(ctx, e.k8s, ca) })
	data, err := e.bao.Read(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(fmt.Sprint(data["public_key"]), "ssh-ed25519 ") || ca.Status.PublicKey != data["public_key"] {
		t.Errorf("status public key %q, server %q", ca.Status.PublicKey, data["public_key"])
	}
	secretsExpectNoWrites(t, "SSHCA")

	if err := e.k8s.Delete(ctx, ca); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "ssh ca resource gone", func() error { return expectGone(ctx, e.k8s, ca) })
	if err := engineExpectFields(ctx, e.bao, path, map[string]any{"public_key": data["public_key"]}); err != nil {
		t.Errorf("orphaned ca changed: %v", err)
	}
}

func testSSHCAImported(ctx context.Context, t *testing.T, e env) {
	const path = "itest-ssh-imported/config/ca"
	public, private := engineSSHKeyPair(t)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-ssh-ca", Namespace: namespace},
		Data:       map[string][]byte{"private": private},
	}
	mustCreate(ctx, t, e.k8s, secret)
	ca := &secretsv1alpha1.SSHCA{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-ca-imported", Namespace: namespace, Annotations: secretsAllowDestructiveDelete},
		Spec: secretsv1alpha1.SSHCASpec{
			ResourceSpec:        secretsResourceSpec(commonv1alpha1.DeletionPolicyDelete),
			MountPath:           "itest-ssh-imported",
			PublicKey:           public,
			PrivateKeySecretRef: &commonv1alpha1.SecretKeySelector{Name: "itest-ssh-ca", Key: "private"},
		},
	}
	mustCreate(ctx, t, e.k8s, ca)
	expectKey := func(want string) func() error {
		return func() error {
			data, err := e.bao.Read(ctx, path)
			if err != nil {
				return err
			}
			if got := strings.TrimSpace(fmt.Sprint(data["public_key"])); got != want {
				return fmt.Errorf("public key %q, want %q", got, want)
			}
			return nil
		}
	}
	waitFor(t, "ssh ca imported", expectKey(public))
	waitFor(t, "ssh ca ready", func() error { return expectReady(ctx, e.k8s, ca) })

	mustWrite(ctx, t, e.bao, path, map[string]any{"generate_signing_key": true, "key_type": "ssh-ed25519"})
	waitFor(t, "replaced ca restored", expectKey(public))
	secretsExpectNoWrites(t, "SSHCA")

	public2, private2 := engineSSHKeyPair(t)
	secret.Data["private"] = private2
	if err := e.k8s.Update(ctx, secret); err != nil {
		t.Fatal(err)
	}
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := e.k8s.Get(ctx, client.ObjectKeyFromObject(ca), ca); err != nil {
			return err
		}
		ca.Spec.PublicKey = public2
		return e.k8s.Update(ctx, ca)
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "rotated key pair imported", expectKey(public2))

	if err := e.k8s.Delete(ctx, ca); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "ssh ca resource gone", func() error { return expectGone(ctx, e.k8s, ca) })
	if _, err := e.bao.Read(ctx, path); err == nil {
		t.Error("deleted ca still has a default issuer")
	}
}

func testSSHRole(ctx context.Context, t *testing.T, e env) {
	const path = "ssh/roles/itest-users"
	role := &secretsv1alpha1.SSHRole{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-users", Namespace: namespace},
		Spec: secretsv1alpha1.SSHRoleSpec{
			ResourceSpec:          secretsResourceSpec(commonv1alpha1.DeletionPolicyDelete),
			MountPath:             "ssh",
			KeyType:               secretsv1alpha1.SSHRoleKeyTypeCA,
			AllowedUsers:          []string{"alice", "bob"},
			AllowUserCertificates: new(true),
			DefaultExtensions:     map[string]string{"permit-pty": ""},
			TTL:                   &metav1.Duration{Duration: time.Hour},
			MaxTTL:                &metav1.Duration{Duration: 24 * time.Hour},
		},
	}
	mustCreate(ctx, t, e.k8s, role)
	want := map[string]any{"allowed_users": "alice,bob", "ttl": "3600", "max_ttl": "86400", "allow_user_certificates": true}
	waitFor(t, "ssh role created", func() error { return engineExpectFields(ctx, e.bao, path, want) })
	waitFor(t, "ssh role ready", func() error { return expectReady(ctx, e.k8s, role) })

	mustWrite(ctx, t, e.bao, path, map[string]any{"key_type": "ca", "allow_user_certificates": true, "allowed_users": "*",
		"ttl": "1h", "max_ttl": "24h", "default_extensions": map[string]any{"permit-pty": ""}})
	waitFor(t, "ssh role drift repaired", func() error { return engineExpectFields(ctx, e.bao, path, want) })
	secretsExpectNoWrites(t, "SSHRole")

	if err := e.k8s.Delete(ctx, role); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "ssh role deleted", func() error { return expectAbsent(ctx, e.bao, path) })
	waitFor(t, "ssh role resource gone", func() error { return expectGone(ctx, e.k8s, role) })
}

func testTOTPKey(ctx context.Context, t *testing.T, e env) {
	const path = "totp/keys/itest-alice"
	key := &secretsv1alpha1.TOTPKey{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-alice", Namespace: namespace},
		Spec: secretsv1alpha1.TOTPKeySpec{
			ResourceSpec: secretsResourceSpec(""),
			MountPath:    "totp",
			Generate: &secretsv1alpha1.TOTPGenerate{
				QRSize:         ptr.To[int32](100),
				WriteSecretRef: &commonv1alpha1.LocalObjectReference{Name: "itest-alice-otpauth"},
			},
			Issuer:      "Example",
			AccountName: "alice@example.com",
			Digits:      ptr.To[int32](8),
		},
	}
	mustCreate(ctx, t, e.k8s, key)
	want := map[string]any{"issuer": "Example", "account_name": "alice@example.com", "digits": "8"}
	waitFor(t, "totp key generated", func() error { return engineExpectFields(ctx, e.bao, path, want) })
	waitFor(t, "totp key ready", func() error { return expectReady(ctx, e.k8s, key) })

	output := &corev1.Secret{}
	if err := e.k8s.Get(ctx, client.ObjectKey{Namespace: namespace, Name: "itest-alice-otpauth"}, output); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(output.Data["url"]), "otpauth://totp/") || len(output.Data["barcode"]) == 0 {
		t.Errorf("output Secret keys: url set %v, barcode set %v", len(output.Data["url"]) > 0, len(output.Data["barcode"]) > 0)
	}
	if !metav1.IsControlledBy(output, key) {
		t.Error("output Secret is not owned by the TOTPKey")
	}
	firstURL := string(output.Data["url"])
	if err := e.k8s.Get(ctx, client.ObjectKeyFromObject(key), key); err != nil {
		t.Fatal(err)
	}
	if fp := string(output.Data["fingerprint"]); fp == "" || fp != key.Status.ID {
		t.Errorf("output Secret fingerprint set %v, equal to status.id %v", fp != "", fp == key.Status.ID)
	}

	if err := e.bao.Delete(ctx, path); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "totp key regenerated and delivered", func() error {
		if err := e.k8s.Get(ctx, client.ObjectKey{Namespace: namespace, Name: "itest-alice-otpauth"}, output); err != nil {
			return err
		}
		if err := e.k8s.Get(ctx, client.ObjectKeyFromObject(key), key); err != nil {
			return err
		}
		if string(output.Data["url"]) == firstURL || string(output.Data["fingerprint"]) != key.Status.ID {
			return errors.New("output Secret does not hold the regenerated key")
		}
		return expectReady(ctx, e.k8s, key)
	})
	mustWrite(ctx, t, e.bao, path, map[string]any{"key": "KRSXG5CTMVRXEZLU", "issuer": "Changed", "account_name": "alice@example.com", "digits": 8})
	stayFor(t, "generated key never rewritten", 3*driftInterval, func() error {
		return engineExpectFields(ctx, e.bao, path, map[string]any{"issuer": "Changed"})
	})

	if err := e.k8s.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "totp key resource gone", func() error { return expectGone(ctx, e.k8s, key) })
	if _, err := e.bao.Read(ctx, path); err != nil {
		t.Errorf("orphaned key: %v", err)
	}
}

func testTOTPKeyImported(ctx context.Context, t *testing.T, e env) {
	const path = "totp/keys/itest-bob"
	seed := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-bob-seed", Namespace: namespace},
		Data:       map[string][]byte{"key": []byte("JBSWY3DPEHPK3PXP")},
	}
	mustCreate(ctx, t, e.k8s, seed)
	key := &secretsv1alpha1.TOTPKey{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-bob", Namespace: namespace, Annotations: secretsAllowDestructiveDelete},
		Spec: secretsv1alpha1.TOTPKeySpec{
			ResourceSpec: secretsResourceSpec(commonv1alpha1.DeletionPolicyDelete),
			MountPath:    "totp",
			KeySecretRef: &commonv1alpha1.SecretKeySelector{Name: "itest-bob-seed", Key: "key"},
			Issuer:       "Example",
			AccountName:  "bob",
			Period:       &metav1.Duration{Duration: time.Minute},
		},
	}
	mustCreate(ctx, t, e.k8s, key)
	want := map[string]any{"issuer": "Example", "account_name": "bob", "period": "60"}
	waitFor(t, "totp key imported", func() error { return engineExpectFields(ctx, e.bao, path, want) })
	waitFor(t, "totp key ready", func() error { return expectReady(ctx, e.k8s, key) })
	waitFor(t, "imported key generates its codes", func() error {
		return engineExpectTOTPCode(ctx, e.bao, "totp/code/itest-bob", "JBSWY3DPEHPK3PXP", time.Minute)
	})

	mustWrite(ctx, t, e.bao, path, map[string]any{"key": "JBSWY3DPEHPK3PXP", "issuer": "Intruder", "account_name": "bob", "period": 60})
	waitFor(t, "totp drift repaired", func() error { return engineExpectFields(ctx, e.bao, path, want) })
	secretsExpectNoWrites(t, "TOTPKey")

	seed.Data["key"] = []byte("KRSXG5CTMVRXEZLU")
	if err := e.k8s.Update(ctx, seed); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "rotated seed takes effect", func() error {
		return engineExpectTOTPCode(ctx, e.bao, "totp/code/itest-bob", "KRSXG5CTMVRXEZLU", time.Minute)
	})

	if err := e.k8s.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "totp key deleted", func() error { return expectAbsent(ctx, e.bao, path) })
	waitFor(t, "totp key resource gone", func() error { return expectGone(ctx, e.k8s, key) })
}

// engineExpectFields compares fields of a read, numbers through their string form.
func engineExpectFields(ctx context.Context, bao openbao.Client, path string, want map[string]any) error {
	data, err := bao.Read(ctx, path)
	if err != nil {
		return err
	}
	for key, value := range want {
		got := data[key]
		if s, ok := value.(string); ok {
			if fmt.Sprint(got) != s {
				return fmt.Errorf("%s = %v, want %s", key, got, s)
			}
			continue
		}
		if got != value {
			return fmt.Errorf("%s = %v, want %v", key, got, value)
		}
	}
	return nil
}

// engineExpectTOTPCode checks the code OpenBao generates against RFC 6238 (SHA1, 6 digits) for seed.
func engineExpectTOTPCode(ctx context.Context, bao openbao.Client, path, seed string, period time.Duration) error {
	data, err := bao.Read(ctx, path)
	if err != nil {
		return err
	}
	secret, err := base32.StdEncoding.DecodeString(seed)
	if err != nil {
		return err
	}
	counter := make([]byte, 8)
	binary.BigEndian.PutUint64(counter, uint64(time.Now().Unix()/int64(period.Seconds())))
	mac := hmac.New(sha1.New, secret)
	mac.Write(counter)
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	code := (binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff) % 1000000
	if want := fmt.Sprintf("%06d", code); data["code"] != want {
		return errors.New("code does not match the seed")
	}
	return nil
}

// engineSSHKeyPair returns a fresh ed25519 key pair as an authorized_keys line and a PKCS#8 PEM.
func engineSSHKeyPair(t *testing.T) (string, []byte) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	wire := func(b []byte) []byte { return append(binary.BigEndian.AppendUint32(nil, uint32(len(b))), b...) }
	blob := append(wire([]byte("ssh-ed25519")), wire(public)...)
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(blob), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func secretsEnginesSetup(ctx context.Context, t *testing.T) env {
	t.Helper()
	root := repoRoot(t)
	baoBin, assets := secretsPrerequisites(t, root)

	addr := startBao(t, baoBin)
	bao, err := openbao.NewPool().Client(ctx, "itest-secrets-engines", openbao.Config{
		Address: addr,
		Timeout: 10 * time.Second,
		Auth: openbao.TokenAuth{
			CredentialKey: "itest-secrets-engines",
			Token:         func(context.Context) (string, error) { return rootToken, nil },
		},
	})
	if err != nil {
		t.Fatalf("openbao client: %v", err)
	}

	crds := make([]string, 0, 4)
	for _, name := range []string{"transitkeys", "sshcas", "sshroles", "totpkeys"} {
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
		secrets.SetupTransitKey, secrets.SetupSSHCA, secrets.SetupSSHRole, secrets.SetupTOTPKey,
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
