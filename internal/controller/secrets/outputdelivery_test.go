package secrets

import (
	"context"
	"errors"
	"maps"
	"strings"
	"sync/atomic"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

const outputTestSecret = "one-time-output"

// outputBao is a pkiFakeClient the engine can drive; a write to a key of created makes its reads appear.
type outputBao struct {
	*pkiFakeClient
	created map[string]map[string]map[string]any
}

func (b *outputBao) Write(ctx context.Context, path string, data map[string]any) (map[string]any, error) {
	response, err := b.pkiFakeClient.Write(ctx, path, data)
	maps.Copy(b.reads, b.created[path])
	return response, err
}

func (b *outputBao) List(context.Context, string) ([]string, error) { return nil, openbao.ErrNotFound }

func (b *outputBao) WithNamespace(string) openbao.Client { return b }

func (b *outputBao) Namespace() string { return "" }

type outputConns struct{ bao openbao.Client }

func (c outputConns) For(context.Context, string, commonv1alpha1.ConnectionReference) (openbao.Client, error) {
	return c.bao, nil
}

// outputCase is one kind that writes a value OpenBao returns once; generate is the write that produces it.
type outputCase struct {
	obj       engine.Object
	generate  string
	response  map[string]any
	created   map[string]map[string]any
	secret    string
	reconcile func(k8s client.Client, scheme *runtime.Scheme, bao openbao.Client, req ctrl.Request) error
}

func outputCases() map[string]outputCase {
	pkiKey := &secretsv1alpha1.PKIKey{
		ObjectMeta: metav1.ObjectMeta{Name: "signing", Namespace: "team"},
		Spec: secretsv1alpha1.PKIKeySpec{ResourceSpec: pkiConnection(), Type: secretsv1alpha1.PKIKeyCreationExported,
			KeyType: "ec", WriteSecretRef: &commonv1alpha1.LocalObjectReference{Name: outputTestSecret}},
	}
	issuer := fullPKIIssuer()
	issuer.Spec.Key.Source = secretsv1alpha1.PKIKeySourceExported
	issuer.Spec.WriteSecretRef = &commonv1alpha1.LocalObjectReference{Name: outputTestSecret}
	issuerID := goldenPKIIssuerRead()["issuer_id"].(string)
	issuerKeyID := goldenPKIIssuerRead()["key_id"].(string)
	totp := generatedTOTPKey()
	totp.Spec.Generate.WriteSecretRef.Name = outputTestSecret

	return map[string]outputCase{
		"PKIKey": {
			obj:      pkiKey,
			generate: "pki/keys/generate/exported",
			response: map[string]any{"key_id": "key-1", "key_type": "ec", "private_key": "PRIVATE-KEY-MATERIAL"},
			created:  map[string]map[string]any{"pki/key/key-1": {"key_id": "key-1", "key_name": "signing", "key_type": "ec"}},
			secret:   "PRIVATE-KEY-MATERIAL",
			reconcile: func(k8s client.Client, scheme *runtime.Scheme, bao openbao.Client, req ctrl.Request) error {
				return reconcileOnce(k8s, scheme, bao, PKIKeyAdapter{Secrets: k8s}, req,
					func() *secretsv1alpha1.PKIKey { return &secretsv1alpha1.PKIKey{} })
			},
		},
		"PKIIssuer": {
			obj:      issuer,
			generate: "pki/root/generate/exported",
			response: map[string]any{"issuer_id": issuerID, "key_id": issuerKeyID, "private_key": "PRIVATE-KEY-MATERIAL"},
			created: map[string]map[string]any{
				"pki/issuer/" + issuerID: goldenPKIIssuerRead(),
				"pki/issuer/root-a":      goldenPKIIssuerRead(),
			},
			secret: "PRIVATE-KEY-MATERIAL",
			reconcile: func(k8s client.Client, scheme *runtime.Scheme, bao openbao.Client, req ctrl.Request) error {
				return reconcileOnce(k8s, scheme, bao, PKIIssuerAdapter{Secrets: k8s}, req,
					func() *secretsv1alpha1.PKIIssuer { return &secretsv1alpha1.PKIIssuer{} })
			},
		},
		"TOTPKey": {
			obj:      totp,
			generate: "totp/keys/gen",
			response: map[string]any{"url": "otpauth://totp/SEED-MATERIAL", "barcode": "QR"},
			created:  map[string]map[string]any{"totp/keys/gen": {"account_name": "alice@example.com"}},
			secret:   "SEED-MATERIAL",
			reconcile: func(k8s client.Client, scheme *runtime.Scheme, bao openbao.Client, req ctrl.Request) error {
				return reconcileOnce(k8s, scheme, bao, TOTPKeyAdapter{Secrets: k8s}, req,
					func() *secretsv1alpha1.TOTPKey { return &secretsv1alpha1.TOTPKey{} })
			},
		},
	}
}

func reconcileOnce[T engine.Object](k8s client.Client, scheme *runtime.Scheme, bao openbao.Client, adapter engine.Adapter[T],
	req ctrl.Request, newObj func() T) error {
	r := engine.New(k8s, scheme, events.NewFakeRecorder(100), outputConns{bao}, adapter, newObj, engine.Options{})
	_, err := r.Reconcile(context.Background(), req)
	return err
}

func outputClient(t *testing.T, obj engine.Object, failSecretCreate *atomic.Bool, existing ...client.Object) (client.Client, *runtime.Scheme) {
	t.Helper()
	scheme := outputScheme(t)
	k8s := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(obj).
		WithObjects(append(existing, obj)...).
		WithIndex(obj, engine.PathIndex, engine.PathIndexFunc).
		WithInterceptorFuncs(interceptor.Funcs{Create: func(ctx context.Context, c client.WithWatch, o client.Object, opts ...client.CreateOption) error {
			if _, ok := o.(*corev1.Secret); ok && failSecretCreate.Load() {
				return errors.New("etcdserver: request timed out")
			}
			return c.Create(ctx, o, opts...)
		}}).Build()
	return k8s, scheme
}

func outputConditions(t *testing.T, k8s client.Client, obj engine.Object) (ready, synced *metav1.Condition) {
	t.Helper()
	got := obj.DeepCopyObject().(engine.Object)
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(obj), got); err != nil {
		t.Fatal(err)
	}
	conditions := got.GetResourceStatus().Conditions
	return meta.FindStatusCondition(conditions, commonv1alpha1.ConditionReady),
		meta.FindStatusCondition(conditions, commonv1alpha1.ConditionSynced)
}

func countWrites(bao *outputBao, path string) int {
	n := 0
	for _, w := range bao.writes {
		if w.path == path {
			n++
		}
	}
	return n
}

// TestOneTimeOutputRefusesForeignSecretBeforeGenerating: a Secret the resource does not control blocks the
// generation, instead of generating a value that can then never be delivered.
func TestOneTimeOutputRefusesForeignSecretBeforeGenerating(t *testing.T) {
	for name, tc := range outputCases() {
		t.Run(name, func(t *testing.T) {
			foreign := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: outputTestSecret, Namespace: tc.obj.GetNamespace()}}
			k8s, scheme := outputClient(t, tc.obj, &atomic.Bool{}, foreign)
			bao := &outputBao{pkiFakeClient: &pkiFakeClient{reads: map[string]map[string]any{},
				responses: map[string]map[string]any{tc.generate: tc.response}}, created: map[string]map[string]map[string]any{tc.generate: tc.created}}
			req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(tc.obj)}
			_ = tc.reconcile(k8s, scheme, bao, req)
			if len(bao.writes) != 0 {
				t.Fatalf("wrote %v to OpenBao although the output Secret belongs to someone else", bao.writePaths())
			}
			if _, synced := outputConditions(t, k8s, tc.obj); synced == nil || synced.Reason != commonv1alpha1.ReasonSecretNotOwned {
				t.Errorf("Synced = %+v, want reason %s", synced, commonv1alpha1.ReasonSecretNotOwned)
			}
		})
	}
}

// TestOneTimeOutputNotDeliveredIsNeverReady: the Secret write fails after OpenBao generated the value; later
// reconciles must not report Ready, must not generate again behind the user's back, and must not leak the value.
func TestOneTimeOutputNotDeliveredIsNeverReady(t *testing.T) {
	for name, tc := range outputCases() {
		t.Run(name, func(t *testing.T) {
			fail := &atomic.Bool{}
			fail.Store(true)
			k8s, scheme := outputClient(t, tc.obj, fail)
			bao := &outputBao{pkiFakeClient: &pkiFakeClient{reads: map[string]map[string]any{},
				responses: map[string]map[string]any{tc.generate: tc.response}}, created: map[string]map[string]map[string]any{tc.generate: tc.created}}
			req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(tc.obj)}
			if err := tc.reconcile(k8s, scheme, bao, req); err == nil {
				t.Fatal("first reconcile: want the Secret write error")
			}
			fail.Store(false)
			for range 3 {
				_ = tc.reconcile(k8s, scheme, bao, req)
			}
			ready, synced := outputConditions(t, k8s, tc.obj)
			if ready == nil || ready.Status == metav1.ConditionTrue {
				t.Fatalf("Ready = %+v, want False while the generated value was never delivered", ready)
			}
			if !strings.Contains(ready.Message, outputTestSecret) {
				t.Errorf("Ready message %q does not name the Secret", ready.Message)
			}
			for _, c := range []*metav1.Condition{ready, synced} {
				if c != nil && strings.Contains(c.Message, tc.secret) {
					t.Errorf("condition %s leaks the generated value", c.Type)
				}
			}
			if n := countWrites(bao, tc.generate); n != 1 {
				t.Errorf("generated %d times, want once", n)
			}
			secret := &corev1.Secret{}
			if err := k8s.Get(context.Background(), client.ObjectKey{Namespace: tc.obj.GetNamespace(), Name: outputTestSecret}, secret); err == nil {
				t.Errorf("output Secret exists without the value: %v", secret.Data)
			}
		})
	}
}

// TestOneTimeOutputDeliveredIsReady: the happy path still becomes Ready with the value in the owned Secret.
func TestOneTimeOutputDeliveredIsReady(t *testing.T) {
	for name, tc := range outputCases() {
		t.Run(name, func(t *testing.T) {
			k8s, scheme := outputClient(t, tc.obj, &atomic.Bool{})
			bao := &outputBao{pkiFakeClient: &pkiFakeClient{reads: map[string]map[string]any{},
				responses: map[string]map[string]any{tc.generate: tc.response}}, created: map[string]map[string]map[string]any{tc.generate: tc.created}}
			req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(tc.obj)}
			for range 2 {
				if err := tc.reconcile(k8s, scheme, bao, req); err != nil {
					t.Fatal(err)
				}
			}
			if ready, _ := outputConditions(t, k8s, tc.obj); ready == nil || ready.Status != metav1.ConditionTrue {
				t.Fatalf("Ready = %+v, want True", ready)
			}
			if n := countWrites(bao, tc.generate); n != 1 {
				t.Errorf("generated %d times, want once", n)
			}
		})
	}
}

// TestPKIKeyOutputMatchesCurrentKey: a Secret left over from an earlier key does not count as delivered, a Secret
// written before key_id existed does, and an adopted key expects no output.
func TestPKIKeyOutputMatchesCurrentKey(t *testing.T) {
	key := outputCases()["PKIKey"].obj.(*secretsv1alpha1.PKIKey)
	key.Status.ID = "key-2"
	bao := &outputBao{pkiFakeClient: &pkiFakeClient{reads: map[string]map[string]any{"pki/key/key-2": {"key_name": "signing"}}}}
	for name, tc := range map[string]struct {
		data     map[string][]byte
		acquired commonv1alpha1.AcquiredState
		wantErr  bool
	}{
		"stale":   {data: map[string][]byte{"private_key": []byte("x"), outputKeyIDKey: []byte("key-1")}, wantErr: true},
		"current": {data: map[string][]byte{"private_key": []byte("x"), outputKeyIDKey: []byte("key-2")}},
		"legacy":  {data: map[string][]byte{"private_key": []byte("x")}},
		"empty":   {data: map[string][]byte{outputKeyIDKey: []byte("key-2")}, wantErr: true},
		"adopted": {acquired: commonv1alpha1.AcquiredAdopted},
	} {
		t.Run(name, func(t *testing.T) {
			k := key.DeepCopy()
			k.Status.Acquired = tc.acquired
			var objects []client.Object
			if tc.data != nil {
				secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: outputTestSecret, Namespace: k.Namespace}, Data: tc.data}
				if err := ctrl.SetControllerReference(k, secret, outputScheme(t)); err != nil {
					t.Fatal(err)
				}
				objects = append(objects, secret)
			}
			reader := fake.NewClientBuilder().WithScheme(outputScheme(t)).WithObjects(objects...).Build()
			_, found, err := PKIKeyAdapter{Secrets: reader}.Observe(context.Background(), bao, k)
			if gotErr := errors.Is(err, engine.ErrOutputNotDelivered); gotErr != tc.wantErr || (!tc.wantErr && !found) {
				t.Errorf("found=%v err=%v, want error %v", found, err, tc.wantErr)
			}
		})
	}
}

func outputScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := secretsv1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}
