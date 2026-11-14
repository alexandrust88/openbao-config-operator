package auth

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// secretIDEngineBao lets the engine drive a credBao.
type secretIDEngineBao struct{ *credBao }

func (b secretIDEngineBao) List(context.Context, string) ([]string, error) {
	return nil, openbao.ErrNotFound
}

func (b secretIDEngineBao) WithNamespace(string) openbao.Client { return b }

func (b secretIDEngineBao) Namespace() string { return "" }

func reconcileSecretID(t *testing.T, s *authv1alpha1.AppRoleSecretID, bao *credBao, existing ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := authv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	k8s := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(s).WithObjects(append(existing, s)...).
		WithIndex(s, engine.PathIndex, engine.PathIndexFunc).Build()
	r := engine.New(k8s, scheme, events.NewFakeRecorder(100), enginev2Conns{secretIDEngineBao{bao}},
		AppRoleSecretIDAdapter{Now: func() time.Time { return secretIDCreated.Add(10 * time.Minute) }},
		func() *authv1alpha1.AppRoleSecretID { return &authv1alpha1.AppRoleSecretID{} }, engine.Options{})
	_, _ = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(s)})
	return k8s
}

// TestSecretIDObserveDestroysNothing: managementPolicy Observe must never destroy the retired secret IDs a
// previous Manage pass left in the status.
func TestSecretIDObserveDestroysNothing(t *testing.T) {
	s := newSecretID()
	s.Spec.ManagementPolicy = commonv1alpha1.ManagementPolicyObserve
	s.Status.ID, s.Status.RolePath = authLitNewAccessor, secretIDRole
	s.Status.RetiredAccessors = []string{secretIDAccessor}
	bao := secretIDBao(t, authLitNewAccessor, secretIDAccessor)

	k8s := reconcileSecretID(t, s, bao)
	if destroyed := bao.wrote(secretIDRole + "/secret-id-accessor/destroy"); len(destroyed) != 0 {
		t.Fatalf("Observe destroyed %d secret IDs", len(destroyed))
	}
	got := &authv1alpha1.AppRoleSecretID{}
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(s), got); err != nil {
		t.Fatal(err)
	}
	if synced := meta.FindStatusCondition(got.Status.Conditions, commonv1alpha1.ConditionSynced); synced == nil ||
		synced.Reason == commonv1alpha1.ReasonReconcileError {
		t.Errorf("Synced = %+v", synced)
	}
}

// TestSecretIDManageDestroysRetiredAfterDelivery: in Manage mode the retired secret IDs are still destroyed once the
// output Secret holds the current one.
func TestSecretIDManageDestroysRetiredAfterDelivery(t *testing.T) {
	s := newSecretID()
	s.Status.ID, s.Status.RolePath = authLitNewAccessor, secretIDRole
	s.Status.Path = secretIDRole + "/secret-id"
	s.Status.Acquired = commonv1alpha1.AcquiredCreated
	s.Status.RetiredAccessors = []string{secretIDAccessor}
	bao := secretIDBao(t, authLitNewAccessor, secretIDAccessor)
	output := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: authTestDemoLogin, Namespace: s.Namespace},
		Data:       map[string][]byte{"secret_id": []byte("delivered"), secretIDAccessorKey: []byte(authLitNewAccessor)},
	}

	reconcileSecretID(t, s, bao, output)
	destroyed := bao.wrote(secretIDRole + "/secret-id-accessor/destroy")
	if len(destroyed) != 1 || destroyed[0][secretIDAccessorKey] != secretIDAccessor {
		t.Fatalf("destroyed %v, want the retired accessor only", destroyed)
	}
	if created := bao.wrote(secretIDRole + "/secret-id"); len(created) != 0 {
		t.Errorf("generated %d secret IDs at rest", len(created))
	}
}

// TestSecretIDRefusesForeignSecretBeforeGenerating: a Secret the resource does not control blocks the generation,
// instead of generating a secret ID on every retry that can never be delivered.
func TestSecretIDRefusesForeignSecretBeforeGenerating(t *testing.T) {
	s := newSecretID()
	foreign := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: authTestDemoLogin, Namespace: s.Namespace}}
	bao := secretIDBao(t)

	k8s := reconcileSecretID(t, s, bao, foreign)
	if created := bao.wrote(secretIDRole + "/secret-id"); len(created) != 0 {
		t.Fatalf("generated %d secret IDs for a Secret that belongs to someone else", len(created))
	}
	got := &authv1alpha1.AppRoleSecretID{}
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(s), got); err != nil {
		t.Fatal(err)
	}
	if synced := meta.FindStatusCondition(got.Status.Conditions, commonv1alpha1.ConditionSynced); synced == nil ||
		synced.Reason != commonv1alpha1.ReasonSecretNotOwned {
		t.Errorf("Synced = %+v, want reason %s", synced, commonv1alpha1.ReasonSecretNotOwned)
	}
}

// TestSecretIDRoleRefClaimsResolvedPath: with roleRef every path the engine records, the claim before the first
// write included, is the referenced AppRole's real path, never one guessed from the reference name.
func TestSecretIDRoleRefClaimsResolvedPath(t *testing.T) {
	const realRole = "auth/ci-approle/role/builder"
	s := newSecretID()
	s.Spec.RoleName = ""
	s.Spec.RoleRef = &commonv1alpha1.LocalObjectReference{Name: authTestDemo}
	role := &authv1alpha1.AppRole{
		ObjectMeta: metav1.ObjectMeta{Name: authTestDemo, Namespace: s.Namespace},
		Spec:       authv1alpha1.AppRoleSpec{ResourceSpec: credConnection(), MountPath: "ci-approle", Name: "builder"},
		Status: authv1alpha1.AppRoleStatus{ResourceStatus: commonv1alpha1.ResourceStatus{Conditions: []metav1.Condition{
			{Type: commonv1alpha1.ConditionReady, Status: metav1.ConditionTrue, Reason: commonv1alpha1.ReasonAvailable,
				LastTransitionTime: metav1.Now()},
		}}},
	}
	bao := &credBao{
		reads: map[string]map[string]any{realRole: {authKeyBindSecretID: true}, realRole + "/role-id": {authLitRoleID: authLitRole}},
		write: func(path string, _ map[string]any) (map[string]any, error) {
			if path == realRole+"/secret-id" {
				return map[string]any{"secret_id": "new-secret", secretIDAccessorKey: authLitNewAccessor}, nil
			}
			return nil, &openbao.ResponseError{StatusCode: 404}
		},
	}
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := authv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	var recorded []string
	k8s := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(s).WithObjects(s, role).
		WithIndex(s, engine.PathIndex, engine.PathIndexFunc).
		WithInterceptorFuncs(interceptor.Funcs{SubResourcePatch: func(ctx context.Context, c client.Client, sub string,
			obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
			if got, ok := obj.(*authv1alpha1.AppRoleSecretID); ok && got.Status.Path != "" {
				recorded = append(recorded, got.Status.Path)
			}
			return c.SubResource(sub).Patch(ctx, obj, patch, opts...)
		}}).Build()
	r := engine.New(k8s, scheme, events.NewFakeRecorder(100), enginev2Conns{secretIDEngineBao{bao}},
		AppRoleSecretIDAdapter{}, func() *authv1alpha1.AppRoleSecretID { return &authv1alpha1.AppRoleSecretID{} },
		engine.Options{})
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(s)}); err != nil {
		t.Fatal(err)
	}
	if len(bao.wrote(realRole+"/secret-id")) != 1 {
		t.Fatalf("writes = %+v, want one secret ID under %s", bao.writes, realRole)
	}
	if len(recorded) == 0 {
		t.Fatal("no status path recorded")
	}
	for _, path := range recorded {
		if path != realRole+"/secret-id" {
			t.Errorf("recorded status.path %q, want %q", path, realRole+"/secret-id")
		}
	}
}
