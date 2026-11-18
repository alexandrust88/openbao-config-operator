package core

import (
	"context"
	"errors"
	"maps"
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	corev1alpha1 "github.com/alexandrust88/openbao-config-operator/api/core/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

type fakeBao struct {
	openbao.Client
	data    map[string]map[string]any
	deleted []string
}

func (f *fakeBao) Read(_ context.Context, path string) (map[string]any, error) {
	if d, ok := f.data[path]; ok {
		return d, nil
	}
	return nil, openbao.ErrNotFound
}

func (f *fakeBao) Delete(_ context.Context, path string) error {
	f.deleted = append(f.deleted, path)
	if _, ok := f.data[path]; !ok {
		return openbao.ErrNotFound
	}
	return nil
}

const testNamespace = "team"

func newEndpoint(data string, mutate func(*corev1alpha1.BaoEndpointSpec)) *corev1alpha1.BaoEndpoint {
	e := &corev1alpha1.BaoEndpoint{
		ObjectMeta: metav1.ObjectMeta{Name: "e", Namespace: testNamespace, Generation: 1},
		Spec:       corev1alpha1.BaoEndpointSpec{Path: alicePath},
		Status: corev1alpha1.BaoEndpointStatus{ResourceStatus: commonv1alpha1.ResourceStatus{
			ObservedGeneration: 1, Acquired: commonv1alpha1.AcquiredCreated,
			Conditions: []metav1.Condition{
				{Type: commonv1alpha1.ConditionReady, Status: metav1.ConditionTrue, Reason: commonv1alpha1.ReasonAvailable, ObservedGeneration: 1},
				{Type: commonv1alpha1.ConditionSynced, Status: metav1.ConditionTrue, Reason: commonv1alpha1.ReasonReconcileSuccess, ObservedGeneration: 1},
			},
		}},
	}
	if data != "" {
		e.Spec.Data = &runtime.RawExtension{Raw: []byte(data)}
	}
	if mutate != nil {
		mutate(&e.Spec)
	}
	return e
}

func newAdapter(t *testing.T, secretJSON string) BaoEndpointAdapter {
	t.Helper()
	builder := fake.NewClientBuilder()
	if secretJSON != "" {
		builder = builder.WithObjects(&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: testNamespace},
			Data:       map[string][]byte{"payload": []byte(secretJSON)},
		})
	}
	return BaoEndpointAdapter{Secrets: builder.Build()}
}

func withSecret(s *corev1alpha1.BaoEndpointSpec) {
	s.DataSecretRef = &commonv1alpha1.SecretKeySelector{Name: "creds", Key: "payload"}
}

func TestEndpointDesiredMergesSecretOverData(t *testing.T) {
	a := newAdapter(t, `{"password":"s3cret","token_ttl":"2h"}`)
	e := newEndpoint(`{"token_ttl":"1h","token_policies":["a","b"]}`, withSecret)

	desired, err := a.Desired(context.Background(), engine.NewResolver(a.Secrets), e)
	if err != nil {
		t.Fatal(err)
	}
	want := engine.Payload{keyPassword: "s3cret", keyTokenTTL: "2h", keyTokenPolicies: []any{"a", "b"}}
	if engine.Diff(want, desired, engine.FieldSpec{}) != nil || len(desired) != len(want) {
		t.Errorf("desired = %v, want %v", desired, want)
	}

	fields, err := a.FieldsFor(context.Background(), engine.NewResolver(a.Secrets), e)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(fields.WriteOnly, []string{keyPassword, keyTokenTTL}) {
		t.Errorf("write-only = %v", fields.WriteOnly)
	}
}

func TestEndpointDesiredRejectsNonObjectSecret(t *testing.T) {
	a := newAdapter(t, `["not","an","object"]`)
	e := newEndpoint("", withSecret)
	if _, err := a.Desired(context.Background(), engine.NewResolver(a.Secrets), e); err == nil {
		t.Fatal("want error for a non-object secret")
	}
}

func TestEndpointDesiredMissingSecretIsReferenceNotReady(t *testing.T) {
	a := newAdapter(t, "")
	e := newEndpoint("", withSecret)
	_, err := a.Desired(context.Background(), engine.NewResolver(a.Secrets), e)
	if !errors.Is(err, engine.ErrReferenceNotReady) {
		t.Fatalf("err = %v, want ErrReferenceNotReady", err)
	}
}

func TestEndpointObserve(t *testing.T) {
	stored := map[string]any{keyTokenTTL: 3600, keyTokenPolicies: []any{"a"}}
	tests := []struct {
		name     string
		secret   string
		data     string
		mutate   func(*corev1alpha1.BaoEndpointSpec)
		wantDiff []string
	}{
		{name: "in sync", data: policiesA},
		{name: "drift", data: `{"token_policies":["a","b"]}`, wantDiff: []string{keyTokenPolicies}},
		{name: "absent key is drift", data: `{"token_policies":["a"],"token_type":"batch"}`, wantDiff: []string{"token_type"}},
		{
			name: "ignoreAbsentFields", data: `{"token_policies":["a"],"token_type":"batch"}`,
			mutate: func(s *corev1alpha1.BaoEndpointSpec) { s.IgnoreAbsentFields = true },
		},
		{
			name: "writeOnlyFields", data: `{"token_policies":["a"],"password":"x"}`,
			mutate: func(s *corev1alpha1.BaoEndpointSpec) { s.WriteOnlyFields = []string{keyPassword} },
		},
		{name: "secret keys are write-only", secret: `{"password":"x"}`, data: policiesA, mutate: withSecret},
		{
			name: "readPath", data: policiesA,
			mutate: func(s *corev1alpha1.BaoEndpointSpec) {
				s.Path = "auth/userpass/users/alice/password"
				s.ReadPath = alicePath
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newAdapter(t, tt.secret)
			e := newEndpoint(tt.data, tt.mutate)
			bao := &fakeBao{data: map[string]map[string]any{alicePath: maps.Clone(stored)}}

			observed, found, err := a.Observe(context.Background(), bao, e)
			if err != nil || !found {
				t.Fatalf("found=%v err=%v", found, err)
			}
			desired, err := a.Desired(context.Background(), engine.NewResolver(a.Secrets), e)
			if err != nil {
				t.Fatal(err)
			}
			fields, err := a.FieldsFor(context.Background(), engine.NewResolver(a.Secrets), e)
			if err != nil {
				t.Fatal(err)
			}
			if got := engine.Diff(desired, observed, fields); !slices.Equal(got, tt.wantDiff) {
				t.Errorf("diff = %v, want %v", got, tt.wantDiff)
			}
		})
	}
}

func TestEndpointObserveDisableRead(t *testing.T) {
	disable := func(s *corev1alpha1.BaoEndpointSpec) { s.DisableRead = true }
	bao := &fakeBao{}
	a := newAdapter(t, "")

	e := newEndpoint(`{"k":"v"}`, disable)
	e.Status.Acquired = ""
	if _, found, err := a.Observe(context.Background(), bao, e); err != nil || found {
		t.Errorf("not acquired: found=%v err=%v, want absent", found, err)
	}

	e = newEndpoint(`{"k":"v"}`, disable)
	observed, found, err := a.Observe(context.Background(), bao, e)
	desired, _ := a.Desired(context.Background(), engine.NewResolver(a.Secrets), e)
	if err != nil || !found || engine.Diff(desired, observed, engine.FieldSpec{}) != nil {
		t.Errorf("reconciled: found=%v err=%v observed=%v", found, err, observed)
	}

	e.Generation = 2
	e.Status.ObservedGeneration = 2
	observed, _, _ = a.Observe(context.Background(), bao, e)
	if engine.Diff(desired, observed, engine.FieldSpec{}) == nil {
		t.Error("spec change after a failed pass: want a diff so the engine writes")
	}
}

func TestEndpointDelete(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*corev1alpha1.BaoEndpointSpec)
		want   []string
	}{
		{name: "path", want: []string{alicePath}},
		{name: "deletePath", mutate: func(s *corev1alpha1.BaoEndpointSpec) { s.DeletePath = "auth/userpass/users/bob" }, want: []string{"auth/userpass/users/bob"}},
		{name: "disableDelete", mutate: func(s *corev1alpha1.BaoEndpointSpec) { s.DisableDelete = true }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bao := &fakeBao{}
			if err := (BaoEndpointAdapter{}).Delete(context.Background(), bao, newEndpoint(`{}`, tt.mutate)); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(bao.deleted, tt.want) {
				t.Errorf("deleted %v, want %v", bao.deleted, tt.want)
			}
		})
	}
}
