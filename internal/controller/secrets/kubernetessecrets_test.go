package secrets

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

const (
	k8sTestRoleType = "Role"
)

func newKubernetesSecretsConfig() *secretsv1alpha1.KubernetesSecretsConfig {
	return &secretsv1alpha1.KubernetesSecretsConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "k8s", Namespace: "team"},
		Spec: secretsv1alpha1.KubernetesSecretsConfigSpec{
			MountPath:                  "kubernetes",
			KubernetesHost:             "https://k8s.example:6443",
			KubernetesCACert:           "ca-pem",
			ServiceAccountJWTSecretRef: &commonv1alpha1.SecretKeySelector{Name: "openbao-sa", Key: "token"},
			DisableLocalCAJWT:          new(true),
		},
	}
}

func TestKubernetesSecretsConfig(t *testing.T) {
	a := KubernetesSecretsConfigAdapter{}
	cfg := newKubernetesSecretsConfig()
	if got := a.Path(cfg); got != "kubernetes/config" {
		t.Errorf("path = %q", got)
	}
	resolver := credResolver{"openbao-sa": {"token": "fake-jwt"}}
	desired, err := a.Desired(context.Background(), resolver, cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := engine.Payload{
		"kubernetes_host":      "https://k8s.example:6443",
		"kubernetes_ca_cert":   "ca-pem",
		"disable_local_ca_jwt": true,
		"service_account_jwt":  "fake-jwt",
	}
	if !reflect.DeepEqual(desired, want) {
		t.Errorf("desired =\n%#v\nwant\n%#v", desired, want)
	}
	// GET kubernetes/config on OpenBao 2.7.2: the token is never returned.
	read := map[string]any{"disable_local_ca_jwt": true, "kubernetes_ca_cert": "ca-pem", "kubernetes_host": "https://k8s.example:6443"}
	if got := engine.Diff(desired, read, a.Fields()); len(got) != 0 {
		t.Errorf("differing = %v", got)
	}
	read["kubernetes_host"] = "https://other:6443"
	if got := engine.Diff(desired, read, a.Fields()); !slices.Equal(got, []string{"kubernetes_host"}) {
		t.Errorf("differing = %v, want kubernetes_host", got)
	}
	key := []byte("test-key")
	rotated, _ := a.Desired(context.Background(), credResolver{"openbao-sa": {"token": "rotated"}}, cfg)
	if engine.WriteOnlyHash(key, desired, a.Fields()) == engine.WriteOnlyHash(key, rotated, a.Fields()) {
		t.Error("a rotated token must change the write-only hash")
	}
	if _, err := a.Desired(context.Background(), credResolver{}, cfg); !errors.Is(err, engine.ErrReferenceNotReady) {
		t.Errorf("missing secret: err = %v", err)
	}
	if got := a.SecretRefs(cfg); !slices.Equal(got, []string{"openbao-sa"}) {
		t.Errorf("SecretRefs = %v", got)
	}
}

func TestKubernetesSecretsRole(t *testing.T) {
	a := KubernetesSecretsRoleAdapter{}
	r := &secretsv1alpha1.KubernetesSecretsRole{
		ObjectMeta: metav1.ObjectMeta{Name: "sa", Namespace: "team"},
		Spec: secretsv1alpha1.KubernetesSecretsRoleSpec{
			MountPath:                   "kubernetes",
			AllowedKubernetesNamespaces: []string{"b", "a"},
			TokenDefaultTTL:             &metav1.Duration{Duration: time.Hour},
			TokenDefaultAudiences:       []string{"x", "y"},
			ServiceAccountName:          "app",
			ExtraLabels:                 map[string]string{"team": "a"},
		},
	}
	if got := a.Path(r); got != "kubernetes/roles/sa" {
		t.Errorf("path = %q", got)
	}
	desired, err := a.Desired(context.Background(), nil, r)
	if err != nil {
		t.Fatal(err)
	}
	want := engine.Payload{
		"allowed_kubernetes_namespaces": []string{"b", "a"},
		"token_default_ttl":             int64(3600),
		"token_default_audiences":       []string{"x", "y"},
		"service_account_name":          "app",
		"extra_labels":                  map[string]string{"team": "a"},

		"allowed_kubernetes_namespace_selector": "", "token_max_ttl": int64(0), "kubernetes_role_name": "",
		"kubernetes_role_type": k8sTestRoleType, "generated_role_rules": "", "name_template": "",
		"extra_annotations": map[string]string{},
	}
	if !reflect.DeepEqual(desired, want) {
		t.Errorf("desired =\n%#v\nwant\n%#v", desired, want)
	}
	// GET kubernetes/roles/sa on OpenBao 2.7.2; namespaces come back sorted.
	read := map[string]any{
		"allowed_kubernetes_namespace_selector": "", "allowed_kubernetes_namespaces": []any{"a", "b"},
		"extra_annotations": nil, "extra_labels": map[string]any{"team": "a"}, "generated_role_rules": "",
		"kubernetes_role_name": "", "kubernetes_role_type": k8sTestRoleType, "name": "sa", "name_template": "",
		"service_account_name": "app", "token_default_audiences": []any{"x", "y"}, "token_default_ttl": 3600.0, "token_max_ttl": 0.0,
	}
	if got := engine.Diff(desired, read, a.Fields()); len(got) != 0 {
		t.Errorf("differing = %v", got)
	}
	read["allowed_kubernetes_namespaces"] = []any{"a"}
	if got := engine.Diff(desired, read, a.Fields()); !slices.Equal(got, []string{"allowed_kubernetes_namespaces"}) {
		t.Errorf("differing = %v, want allowed_kubernetes_namespaces", got)
	}
}
