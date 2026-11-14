package importer

import (
	"context"
	"maps"
	"reflect"
	"strings"
	"testing"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// fakeBao serves fixed reads and lists of the root namespace; every other path is absent. Writes fail the test.
type fakeBao struct {
	t     *testing.T
	reads map[string]engine.Payload
	lists map[string][]string
}

func (f *fakeBao) Read(_ context.Context, path string) (map[string]any, error) {
	if data, ok := f.reads[path]; ok {
		return maps.Clone(data), nil
	}
	return nil, &openbao.ResponseError{StatusCode: 404}
}

func (f *fakeBao) Write(_ context.Context, path string, _ map[string]any) (map[string]any, error) {
	f.t.Errorf("unexpected write to %s", path)
	return nil, nil
}

func (f *fakeBao) Delete(_ context.Context, path string) error {
	f.t.Errorf("unexpected delete of %s", path)
	return nil
}

func (f *fakeBao) List(_ context.Context, path string) ([]string, error) {
	if keys, ok := f.lists[path]; ok {
		return keys, nil
	}
	return nil, &openbao.ResponseError{StatusCode: 404}
}

func (f *fakeBao) WithNamespace(string) openbao.Client { return f }

func (f *fakeBao) Namespace() string { return "" }

func newFakeBao(t *testing.T) *fakeBao {
	mountConfig := map[string]any{"default_lease_ttl": 0.0, "force_no_cache": false, "max_lease_ttl": 0.0,
		"token_type": "default-service"}
	return &fakeBao{
		t: t,
		reads: map[string]engine.Payload{
			"sys/auth": {
				"kubernetes/": map[string]any{"accessor": "auth_kubernetes_1", "config": mountConfig, litDescription: "",
					"external_entropy_access": false, "local": false, "options": nil, "plugin_version": "",
					"seal_wrap": false, litType: litKubernetes},
				"token/": map[string]any{"accessor": "auth_token_1", "config": mountConfig, litType: "token"},
			},
			"sys/mounts": {"cubbyhole/": map[string]any{litType: "cubbyhole"}},
			"sys/policies/acl/app": {"allow_slashes_in_identity_templates": false,
				"allow_wildcards_in_identity_templates": false, "cas_required": false, litName: litApp,
				litPolicy: "path \"secret/*\" { capabilities = [\"read\"] }\n", "version": 1.0},
			"auth/kubernetes/config": {"disable_iss_validation": true, "disable_local_ca_jwt": false, "issuer": "",
				"kubernetes_ca_cert": "", "kubernetes_host": litK8sHost, "pem_keys": []any{},
				"token_reviewer_jwt_set": true},
			"auth/kubernetes/role/app": kubernetesRoleRead,
		},
		lists: map[string][]string{
			"sys/policies/acl":     {"root", litApp},
			"auth/kubernetes/role": {litApp},
		},
	}
}

func TestImport(t *testing.T) {
	conn := commonv1alpha1.ConnectionReference{Kind: commonv1alpha1.ConnectionKindNamespaced, Name: "openbao"}
	result, err := Import(t.Context(), newFakeBao(t), Options{Connection: conn, Namespace: litObco})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Notes) != 0 {
		t.Errorf("notes = %q", result.Notes)
	}
	got := make([]string, 0, len(result.Objects))
	for _, obj := range result.Objects {
		got = append(got, obj.GetObjectKind().GroupVersionKind().Kind+"/"+obj.GetName())
		spec := obj.(engine.Object).GetResourceSpec()
		if obj.GetNamespace() != litObco || spec.ConnectionRef != conn || spec.Namespace != "" ||
			spec.CreationPolicy != commonv1alpha1.CreationPolicyAdopt ||
			spec.ManagementPolicy != commonv1alpha1.ManagementPolicyObserve {
			t.Errorf("%s has common fields %s %+v", obj.GetName(), obj.GetNamespace(), spec)
		}
	}
	want := []string{"KubernetesAuthConfig/kubernetes", "KubernetesAuthRole/kubernetes-app", "AuthMount/kubernetes",
		"Policy/app"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("objects = %v, want %v", got, want)
	}

	cfg := result.Objects[0].(*authv1alpha1.KubernetesAuthConfig)
	wantRef := &commonv1alpha1.SecretKeySelector{Name: litKubernetes, Key: litTokenReviewerJWT}
	if !reflect.DeepEqual(cfg.Spec.TokenReviewerJWTSecretRef, wantRef) || cfg.Spec.KubernetesHost != litK8sHost {
		t.Errorf("KubernetesAuthConfig spec = %+v", cfg.Spec)
	}
	if cfg.Spec.DisableLocalCAJWT != nil {
		t.Errorf("disableLocalCAJWT false was kept although it is the default")
	}
	wantPlaceholders := []Placeholder{{Object: "KubernetesAuthConfig/kubernetes", Field: "spec.tokenReviewerJWTSecretRef",
		Secret: litKubernetes, Key: litTokenReviewerJWT}}
	if !reflect.DeepEqual(result.Placeholders, wantPlaceholders) {
		t.Errorf("placeholders = %+v", result.Placeholders)
	}

	role := result.Objects[1].(*authv1alpha1.KubernetesAuthRole)
	if role.Spec.TokenNoDefaultPolicy != nil || role.Spec.TokenTTL == nil || role.Spec.Name != litApp {
		t.Errorf("KubernetesAuthRole spec = %+v", role.Spec)
	}
	if policy := result.Objects[3].(*sysv1alpha1.Policy); policy.Spec.CASRequired {
		t.Errorf("Policy spec = %+v", policy.Spec)
	}
}

func TestImportReportsDrift(t *testing.T) {
	bao := newFakeBao(t)
	read := maps.Clone(kubernetesRoleRead)
	read["token_ttl"] = "soon"
	bao.reads["auth/kubernetes/role/app"] = read
	result, err := Import(t.Context(), bao, Options{Namespace: litObco})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"KubernetesAuthRole/kubernetes-app: Observe will report drift on token_ttl",
		"KubernetesAuthRole/kubernetes-app: spec.tokenTTL not imported, its OpenBao value has an unexpected type",
	}
	if !reflect.DeepEqual(result.Notes, want) {
		t.Errorf("notes = %q, want %q", result.Notes, want)
	}
}
