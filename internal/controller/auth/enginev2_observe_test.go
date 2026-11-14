package auth

import (
	"context"
	"maps"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// enginev2Bao serves fixed reads and counts writes.
type enginev2Bao struct {
	reads  map[string]map[string]any
	writes int
}

func (b *enginev2Bao) Read(_ context.Context, path string) (map[string]any, error) {
	if d, ok := b.reads[path]; ok {
		return maps.Clone(d), nil
	}
	return nil, openbao.ErrNotFound
}

func (b *enginev2Bao) Write(context.Context, string, map[string]any) (map[string]any, error) {
	b.writes++
	return nil, nil
}

func (b *enginev2Bao) Delete(context.Context, string) error { return nil }

func (b *enginev2Bao) List(context.Context, string) ([]string, error) {
	return nil, openbao.ErrNotFound
}

func (b *enginev2Bao) WithNamespace(string) openbao.Client { return b }

func (b *enginev2Bao) Namespace() string { return "" }

type enginev2Conns struct{ bao openbao.Client }

func (c enginev2Conns) For(context.Context, string, commonv1alpha1.ConnectionReference) (openbao.Client, error) {
	return c.bao, nil
}

// TestEngineV2ObserveImportedKubernetesAuthConfig: an imported resource names a token reviewer JWT Secret that does
// not exist yet; observing it compares the readable fields only.
func TestEngineV2ObserveImportedKubernetesAuthConfig(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := authv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	cfg := &authv1alpha1.KubernetesAuthConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "imported", Namespace: "team", Generation: 1},
		Spec: authv1alpha1.KubernetesAuthConfigSpec{
			ResourceSpec: commonv1alpha1.ResourceSpec{
				ConnectionRef:    commonv1alpha1.ConnectionReference{Name: "bao"},
				CreationPolicy:   commonv1alpha1.CreationPolicyAdopt,
				ManagementPolicy: commonv1alpha1.ManagementPolicyObserve,
			},
			KubernetesHost:            "https://kubernetes.default.svc",
			TokenReviewerJWTSecretRef: &commonv1alpha1.SecretKeySelector{Name: "reviewer-jwt", Key: "token"},
		},
	}
	k8s := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(cfg).WithObjects(cfg).Build()
	bao := &enginev2Bao{reads: map[string]map[string]any{"auth/kubernetes/config": {
		"kubernetes_host": "https://kubernetes.default.svc", "kubernetes_ca_cert": "", "pem_keys": []any{},
		"disable_local_ca_jwt": false, "disable_iss_validation": true, "issuer": "", tokenReviewerJWTSet: true,
	}}}
	r := engine.New(k8s, scheme, events.NewFakeRecorder(10), enginev2Conns{bao}, KubernetesAuthConfigAdapter{},
		func() *authv1alpha1.KubernetesAuthConfig { return &authv1alpha1.KubernetesAuthConfig{} }, engine.Options{})
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cfg)}); err != nil {
		t.Fatal(err)
	}
	got := &authv1alpha1.KubernetesAuthConfig{}
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(cfg), got); err != nil {
		t.Fatal(err)
	}
	for _, conditionType := range []string{commonv1alpha1.ConditionReady, commonv1alpha1.ConditionSynced} {
		c := meta.FindStatusCondition(got.Status.Conditions, conditionType)
		if c == nil || c.Status != metav1.ConditionTrue || c.Reason != commonv1alpha1.ReasonObserved {
			t.Fatalf("%s = %+v, want True/Observed", conditionType, c)
		}
	}
	if bao.writes != 0 {
		t.Fatalf("observe wrote %d times", bao.writes)
	}
}
