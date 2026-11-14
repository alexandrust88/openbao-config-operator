package secrets

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

const (
	totpTestAlgorithm = "SHA1"
	totpTestIssuer    = "Example"
	totpTestSeed      = "JBSWY3DPEHPK3PXP"
)

func generatedTOTPKey() *secretsv1alpha1.TOTPKey {
	return &secretsv1alpha1.TOTPKey{
		ObjectMeta: metav1.ObjectMeta{Name: "gen", Namespace: "team"},
		Spec: secretsv1alpha1.TOTPKeySpec{
			MountPath: "totp",
			Generate: &secretsv1alpha1.TOTPGenerate{
				KeySize:        ptr.To[int32](20),
				Exported:       new(true),
				QRSize:         ptr.To[int32](200),
				WriteSecretRef: &commonv1alpha1.LocalObjectReference{Name: "gen-otpauth"},
			},
			Issuer:      totpTestIssuer,
			AccountName: "alice@example.com",
			Period:      &metav1.Duration{Duration: 30 * time.Second},
			Algorithm:   totpTestAlgorithm,
			Digits:      ptr.To[int32](6),
			Skew:        ptr.To[int32](1),
		},
	}
}

func importedTOTPKey() *secretsv1alpha1.TOTPKey {
	return &secretsv1alpha1.TOTPKey{
		ObjectMeta: metav1.ObjectMeta{Name: "imp", Namespace: "team"},
		Spec: secretsv1alpha1.TOTPKeySpec{
			MountPath:    "totp",
			KeySecretRef: &commonv1alpha1.SecretKeySelector{Name: "totp-seed", Key: "key"},
			Issuer:       "Ex",
			AccountName:  "bob",
			Period:       &metav1.Duration{Duration: time.Minute},
			Algorithm:    "SHA256",
			Digits:       ptr.To[int32](8),
			Skew:         ptr.To[int32](0),
		},
	}
}

func TestTOTPKeyDesired(t *testing.T) {
	a := TOTPKeyAdapter{}
	got, err := a.Desired(context.Background(), engineStubResolver{}, generatedTOTPKey())
	if err != nil {
		t.Fatal(err)
	}
	want := engine.Payload{
		"generate": true, "key_size": int64(20), "exported": true, "qr_size": int64(200), "issuer": totpTestIssuer,
		"account_name": "alice@example.com", "period": int64(30), "algorithm": totpTestAlgorithm, "digits": int64(6), "skew": int64(1),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Desired(generate) = %v, want %v", got, want)
	}

	r := engineStubResolver{"totp-seed/key": totpTestSeed}
	got, _ = a.Desired(context.Background(), r, importedTOTPKey())
	if got["key"] != totpTestSeed || got["generate"] != nil {
		t.Errorf("Desired(import) = %v", got)
	}

	byURL := &secretsv1alpha1.TOTPKey{Spec: secretsv1alpha1.TOTPKeySpec{
		MountPath:    "totp",
		URLSecretRef: &commonv1alpha1.SecretKeySelector{Name: "totp-url", Key: "url"},
	}}
	_, err = a.Desired(context.Background(), engineStubResolver{}, byURL)
	if !errors.Is(err, engine.ErrReferenceNotReady) {
		t.Errorf("missing Secret: err = %v, want ErrReferenceNotReady", err)
	}
	got, _ = a.Desired(context.Background(), engineStubResolver{"totp-url/url": "otpauth://totp/Ex:bob?secret=X"}, byURL)
	if !reflect.DeepEqual(got, engine.Payload{"url": "otpauth://totp/Ex:bob?secret=X"}) {
		t.Errorf("Desired(url) = %v", got)
	}
}

func TestTOTPKeyGoldenRead(t *testing.T) {
	a := TOTPKeyAdapter{}
	r := engineStubResolver{"totp-seed/key": totpTestSeed}

	gen := generatedTOTPKey()
	desired, _ := a.Desired(context.Background(), r, gen)
	c := &engineStubClient{data: map[string]map[string]any{"totp/keys/gen": {
		"account_name": "alice@example.com", "algorithm": totpTestAlgorithm, "digits": 6, "issuer": totpTestIssuer, "period": 30,
	}}}
	observed, found, err := a.Observe(context.Background(), c, gen)
	if err != nil || !found {
		t.Fatalf("found %v, err %v", found, err)
	}
	if diff := engine.Diff(desired, observed, a.Fields()); len(diff) != 0 {
		t.Errorf("generated key differs in %v; rewriting it would replace the key", diff)
	}
	c.data["totp/keys/gen"]["issuer"] = "Changed"
	observed, _, _ = a.Observe(context.Background(), c, gen)
	if diff := engine.Diff(desired, observed, a.Fields()); len(diff) != 0 {
		t.Errorf("a generated key must never be rewritten, Diff = %v", diff)
	}

	imp := importedTOTPKey()
	desired, _ = a.Desired(context.Background(), r, imp)
	c.data["totp/keys/imp"] = map[string]any{"account_name": "bob", "algorithm": "SHA256", "digits": 8, "issuer": "Ex", "period": 60}
	observed, _, _ = a.Observe(context.Background(), c, imp)
	if diff := engine.Diff(desired, observed, a.Fields()); len(diff) != 0 {
		t.Errorf("imported key differs in %v", diff)
	}
	c.data["totp/keys/imp"]["digits"] = 6
	observed, _, _ = a.Observe(context.Background(), c, imp)
	if diff := engine.Diff(desired, observed, a.Fields()); !slices.Equal(diff, []string{"digits"}) {
		t.Errorf("Diff = %v, want digits", diff)
	}

	rotated, _ := a.Desired(context.Background(), engineStubResolver{"totp-seed/key": "KRSXG5CTMVRXEZLU"}, imp)
	if engine.WriteOnlyHash([]byte("k"), desired, a.Fields()) == engine.WriteOnlyHash([]byte("k"), rotated, a.Fields()) {
		t.Error("a new key must change the write-only hash")
	}
}

func TestTOTPKeyOutputs(t *testing.T) {
	a := TOTPKeyAdapter{HashKey: []byte("k")}
	response := engine.Payload{"url": "otpauth://totp/Example:alice@example.com?secret=FAKE", "barcode": "iVBORw0KGgo="}
	name, data, ok := a.Outputs(generatedTOTPKey(), response)
	if !ok || name != "gen-otpauth" || string(data["url"]) != response["url"] || string(data["barcode"]) != "iVBORw0KGgo=" {
		t.Errorf("Outputs = %q %v %v", name, data, ok)
	}
	if fp := string(data[totpFingerprintKey]); fp == "" || strings.Contains(fp, "FAKE") ||
		fp == (TOTPKeyAdapter{HashKey: []byte("other")}).fingerprint(response["url"].(string)) {
		t.Errorf("fingerprint %q must be set, keyed and not contain the URL", fp)
	}
	if _, _, ok := a.Outputs(generatedTOTPKey(), nil); ok {
		t.Error("an empty response must not write a Secret")
	}
	if _, _, ok := a.Outputs(importedTOTPKey(), response); ok {
		t.Error("an imported key has no outputs")
	}
	if refs := a.SecretRefs(importedTOTPKey()); !slices.Equal(refs, []string{"totp-seed"}) {
		t.Errorf("SecretRefs = %v", refs)
	}
	if path := a.Path(importedTOTPKey()); !strings.HasSuffix(path, "totp/keys/imp") {
		t.Errorf("Path = %q", path)
	}
}

// TestTOTPKeyRegeneratedKeyNotDeliveredIsNotReady: a delivered key is deleted outside the operator and generated
// again, and the Secret write fails; the URL of the old key left in the Secret must not count as delivered.
func TestTOTPKeyRegeneratedKeyNotDeliveredIsNotReady(t *testing.T) {
	const path = "totp/keys/gen"
	k := generatedTOTPKey()
	k.Spec.Generate.WriteSecretRef.Name = outputTestSecret
	fail := &atomic.Bool{}
	failSecret := func(o client.Object) error {
		if _, ok := o.(*corev1.Secret); ok && fail.Load() {
			return errors.New("etcdserver: request timed out")
		}
		return nil
	}
	scheme := outputScheme(t)
	k8s := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(k).WithObjects(k).
		WithIndex(k, engine.PathIndex, engine.PathIndexFunc).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, o client.Object, opts ...client.CreateOption) error {
				if err := failSecret(o); err != nil {
					return err
				}
				return c.Create(ctx, o, opts...)
			},
			Update: func(ctx context.Context, c client.WithWatch, o client.Object, opts ...client.UpdateOption) error {
				if err := failSecret(o); err != nil {
					return err
				}
				return c.Update(ctx, o, opts...)
			},
		}).Build()
	bao := &outputBao{pkiFakeClient: &pkiFakeClient{reads: map[string]map[string]any{},
		responses: map[string]map[string]any{path: {"url": "otpauth://totp/FIRST-SEED", "barcode": "QR"}}},
		created: map[string]map[string]map[string]any{path: {path: {"account_name": "alice@example.com"}}}}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(k)}
	reconcile := func() error {
		return reconcileOnce(k8s, scheme, bao, TOTPKeyAdapter{Secrets: k8s}, req,
			func() *secretsv1alpha1.TOTPKey { return &secretsv1alpha1.TOTPKey{} })
	}
	regenerate := func(url string) {
		delete(bao.reads, path)
		bao.responses[path] = map[string]any{"url": url, "barcode": "QR"}
	}
	outputURL := func() string {
		secret := &corev1.Secret{}
		if err := k8s.Get(context.Background(), client.ObjectKey{Namespace: k.Namespace, Name: outputTestSecret}, secret); err != nil {
			t.Fatal(err)
		}
		return string(secret.Data["url"])
	}

	for range 2 {
		if err := reconcile(); err != nil {
			t.Fatal(err)
		}
	}
	if ready, _ := outputConditions(t, k8s, k); ready == nil || ready.Status != metav1.ConditionTrue {
		t.Fatalf("Ready = %+v, want True after the first delivery", ready)
	}

	regenerate("otpauth://totp/SECOND-SEED")
	fail.Store(true)
	if err := reconcile(); err == nil {
		t.Fatal("regenerating reconcile: want the Secret write error")
	}
	fail.Store(false)
	for range 3 {
		_ = reconcile()
	}
	ready, synced := outputConditions(t, k8s, k)
	if ready == nil || ready.Status == metav1.ConditionTrue || ready.Reason != commonv1alpha1.ReasonOutputNotDelivered {
		t.Fatalf("Ready = %+v, want False with reason %s while the Secret holds the old key", ready,
			commonv1alpha1.ReasonOutputNotDelivered)
	}
	for _, c := range []*metav1.Condition{ready, synced} {
		if c != nil && strings.Contains(c.Message, "SEED") {
			t.Errorf("condition %s leaks the otpauth URL", c.Type)
		}
	}
	if n := countWrites(bao, path); n != 2 {
		t.Errorf("generated %d times, want twice", n)
	}
	if got := outputURL(); got != "otpauth://totp/FIRST-SEED" {
		t.Errorf("Secret url = %q, want the first URL untouched", got)
	}

	regenerate("otpauth://totp/THIRD-SEED")
	for range 2 {
		if err := reconcile(); err != nil {
			t.Fatal(err)
		}
	}
	if ready, _ := outputConditions(t, k8s, k); ready == nil || ready.Status != metav1.ConditionTrue {
		t.Fatalf("Ready = %+v, want True once the new key is delivered", ready)
	}
	if got := outputURL(); got != "otpauth://totp/THIRD-SEED" {
		t.Errorf("Secret url = %q, want the third URL", got)
	}
}

// TestTOTPKeyOutputMatchesCurrentKey: a Secret holding an earlier key's fingerprint does not count as delivered, and a
// Secret written before the fingerprint existed does.
func TestTOTPKeyOutputMatchesCurrentKey(t *testing.T) {
	k := generatedTOTPKey()
	k.Spec.Generate.WriteSecretRef.Name = outputTestSecret
	k.Status.Path, k.Status.ID = "totp/keys/gen", "fp-2"
	bao := &outputBao{pkiFakeClient: &pkiFakeClient{reads: map[string]map[string]any{"totp/keys/gen": {"issuer": totpTestIssuer}}}}
	for name, tc := range map[string]struct {
		data    map[string][]byte
		wantErr bool
	}{
		"stale":   {data: map[string][]byte{"url": []byte("x"), totpFingerprintKey: []byte("fp-1")}, wantErr: true},
		"current": {data: map[string][]byte{"url": []byte("x"), totpFingerprintKey: []byte("fp-2")}},
		"legacy":  {data: map[string][]byte{"url": []byte("x")}},
	} {
		t.Run(name, func(t *testing.T) {
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: outputTestSecret, Namespace: k.Namespace}, Data: tc.data}
			if err := ctrl.SetControllerReference(k, secret, outputScheme(t)); err != nil {
				t.Fatal(err)
			}
			reader := fake.NewClientBuilder().WithScheme(outputScheme(t)).WithObjects(secret).Build()
			_, found, err := TOTPKeyAdapter{Secrets: reader}.Observe(context.Background(), bao, k)
			if gotErr := errors.Is(err, engine.ErrOutputNotDelivered); gotErr != tc.wantErr || (!tc.wantErr && !found) {
				t.Errorf("found=%v err=%v, want error %v", found, err, tc.wantErr)
			}
		})
	}
}
