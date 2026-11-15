package secrets

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// kvStoreBao serves KV v2 reads from what was written, so the engine can drive a RandomSecret end to end.
type kvStoreBao struct {
	openbao.Client
	data    map[string]map[string]any
	writes  int
	deletes []string
}

func (b *kvStoreBao) Read(_ context.Context, path string) (map[string]any, error) {
	data, ok := b.data[path]
	if !ok {
		return nil, openbao.ErrNotFound
	}
	return map[string]any{"data": data, "metadata": map[string]any{"version": json.Number("1")}}, nil
}

func (b *kvStoreBao) Write(_ context.Context, path string, body map[string]any) (map[string]any, error) {
	b.writes++
	b.data[path], _ = body["data"].(map[string]any)
	return nil, nil
}

func (b *kvStoreBao) Delete(_ context.Context, path string) error {
	b.deletes = append(b.deletes, path)
	return nil
}

func (b *kvStoreBao) List(context.Context, string) ([]string, error) { return nil, openbao.ErrNotFound }

func (b *kvStoreBao) WithNamespace(string) openbao.Client { return b }

func (b *kvStoreBao) Namespace() string { return "" }

// TestRandomSecretRedeliversOutput: the KV write succeeded but the output Secret write failed, and later the
// output Secret is deleted. Both times the stored value is delivered again without a write to OpenBao, and the
// resource is not Ready while the Secret is missing.
func TestRandomSecretRedeliversOutput(t *testing.T) {
	s := newRandomSecret()
	s.Spec.ConnectionRef = pkiConnection().ConnectionRef
	s.Spec.WriteSecretRef = &commonv1alpha1.LocalObjectReference{Name: outputTestSecret}
	fail := &atomic.Bool{}
	fail.Store(true)
	k8s, scheme := outputClient(t, s, fail)
	bao := &kvStoreBao{data: map[string]map[string]any{}}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(s)}
	r := engine.New(k8s, scheme, events.NewFakeRecorder(100), outputConns{bao}, RandomSecretAdapter{},
		func() *secretsv1alpha1.RandomSecret { return &secretsv1alpha1.RandomSecret{} }, engine.Options{})
	reconcile := func() error {
		_, err := r.Reconcile(context.Background(), req)
		return err
	}

	if err := reconcile(); err == nil {
		t.Fatal("first reconcile: want the Secret write error")
	}
	if ready, _ := outputConditions(t, k8s, s); ready != nil && ready.Status == metav1.ConditionTrue {
		t.Fatalf("Ready = %+v while the output Secret is missing", ready)
	}
	stored := bao.data["kv/data/team/db"]["password"]
	fail.Store(false)
	_ = reconcile()
	assertDelivered(t, k8s, s, stored)
	if bao.writes != 1 {
		t.Fatalf("OpenBao writes = %d, want only the first generation", bao.writes)
	}

	if err := k8s.Delete(context.Background(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: outputTestSecret,
		Namespace: s.Namespace}}); err != nil {
		t.Fatal(err)
	}
	_ = reconcile()
	assertDelivered(t, k8s, s, stored)
	if bao.writes != 1 {
		t.Errorf("OpenBao writes = %d, want the deleted Secret restored without a new value", bao.writes)
	}
}

func assertDelivered(t *testing.T, k8s client.Client, s *secretsv1alpha1.RandomSecret, want any) {
	t.Helper()
	secret := &corev1.Secret{}
	if err := k8s.Get(context.Background(), client.ObjectKey{Namespace: s.Namespace, Name: outputTestSecret}, secret); err != nil {
		t.Fatalf("output Secret: %v", err)
	}
	if string(secret.Data["password"]) != want {
		t.Error("output Secret does not hold the stored value")
	}
	if ready, _ := outputConditions(t, k8s, s); ready == nil || ready.Status != metav1.ConditionTrue {
		t.Errorf("Ready = %+v, want True once delivered", ready)
	}
}

// TestRandomSecretDeletePurgesOwnedPath: a RandomSecret that created its KV v2 path and allows destructive deletes
// removes the path with its history once its key was the last one.
func TestRandomSecretDeletePurgesOwnedPath(t *testing.T) {
	s := newRandomSecret()
	s.Status.Acquired, s.Status.PathCreated = commonv1alpha1.AcquiredCreated, true
	s.Annotations = map[string]string{commonv1alpha1.AnnotationAllowDestructiveDelete: "true"}
	bao := &kvStoreBao{data: map[string]map[string]any{"kv/data/team/db": {"password": "x"}}}
	if err := (RandomSecretAdapter{}).Delete(context.Background(), bao, s); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(bao.deletes, "kv/metadata/team/db") {
		t.Errorf("deletes = %v, want the metadata of the owned path", bao.deletes)
	}

	s.Annotations = nil
	bao = &kvStoreBao{data: map[string]map[string]any{"kv/data/team/db": {"password": "x"}}}
	if err := (RandomSecretAdapter{}).Delete(context.Background(), bao, s); err != nil {
		t.Fatal(err)
	}
	if len(bao.deletes) != 0 {
		t.Errorf("deletes = %v without the destructive annotation, want the history kept", bao.deletes)
	}
}

// TestRandomSecretNewOutputSecretIsNoWrite: naming an output Secret for a stored value delivers it without counting
// an OpenBao write, so a second pass that sees the change again writes nothing either.
func TestRandomSecretNewOutputSecretIsNoWrite(t *testing.T) {
	s := newRandomSecret()
	s.Spec.ConnectionRef = pkiConnection().ConnectionRef
	k8s, scheme := outputClient(t, s, &atomic.Bool{})
	bao := &kvStoreBao{data: map[string]map[string]any{"kv/data/team/db": {"password": "stored"}}}
	recorder := events.NewFakeRecorder(100)
	r := engine.New(k8s, scheme, recorder, outputConns{bao}, RandomSecretAdapter{},
		func() *secretsv1alpha1.RandomSecret { return &secretsv1alpha1.RandomSecret{} }, engine.Options{})
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(s)}
	s.Spec.CreationPolicy = commonv1alpha1.CreationPolicyAdopt
	if err := k8s.Update(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	drain(recorder)

	if err := k8s.Get(context.Background(), req.NamespacedName, s); err != nil {
		t.Fatal(err)
	}
	s.Spec.WriteSecretRef = &commonv1alpha1.LocalObjectReference{Name: outputTestSecret}
	if err := k8s.Update(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	assertDelivered(t, k8s, s, "stored")
	for _, event := range drain(recorder) {
		if strings.Contains(event, "Updated") {
			t.Errorf("event %q: delivering a stored value is no OpenBao write", event)
		}
	}
}

func drain(recorder *events.FakeRecorder) []string {
	var out []string
	for {
		select {
		case event := <-recorder.Events:
			out = append(out, event)
		default:
			return out
		}
	}
}
