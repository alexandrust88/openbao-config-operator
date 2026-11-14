package examples

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestSysCoreExamples installs the OpenBaoNamespace, AuthMount, SecretsMount, PasswordPolicy, RateLimitQuota and
// Policy CRDs into envtest, applies examples/sys with a server-side dry run and checks their CEL rules.
func TestSysCoreExamples(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	k8s := secretsStartEnv(t, root, []string{
		"sys.openbaoconfig.io_openbaonamespaces.yaml",
		"sys.openbaoconfig.io_authmounts.yaml",
		"sys.openbaoconfig.io_secretsmounts.yaml",
		"sys.openbaoconfig.io_passwordpolicies.yaml",
		"sys.openbaoconfig.io_ratelimitquotas.yaml",
		"sys.openbaoconfig.io_policies.yaml",
	})
	ctx := context.Background()
	if err := k8s.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "openbao-config"}}); err != nil {
		t.Fatal(err)
	}

	for _, file := range []string{"openbaonamespace.yaml", "authmount.yaml", "secretsmount.yaml", "passwordpolicy.yaml",
		"ratelimitquota.yaml", "policy.yaml"} {
		t.Run(file, func(t *testing.T) {
			for _, obj := range secretsDecode(t, filepath.Join(root, "examples", "sys", file)) {
				if err := k8s.Create(ctx, obj, client.DryRunAll, client.FieldValidation("Strict")); err != nil {
					t.Errorf("%s %s: %v", obj.GetKind(), obj.GetName(), err)
				}
			}
		})
	}

	t.Run("validation", func(t *testing.T) {
		for _, tt := range []struct {
			name, doc, wantErr string
		}{
			{name: "reserved namespace name", wantErr: "reserved", doc: `
apiVersion: sys.openbaoconfig.io/v1alpha1
kind: OpenBaoNamespace
metadata: {name: reserved, namespace: openbao-config}
spec: {connectionRef: {name: openbao}, name: identity}`},
			{name: "reserved namespace metadata name", wantErr: "reserved", doc: `
apiVersion: sys.openbaoconfig.io/v1alpha1
kind: OpenBaoNamespace
metadata: {name: sys, namespace: openbao-config}
spec: {connectionRef: {name: openbao}}`},
			{name: "dot-dot namespace name", wantErr: "spec.name", doc: `
apiVersion: sys.openbaoconfig.io/v1alpha1
kind: OpenBaoNamespace
metadata: {name: dotdot, namespace: openbao-config}
spec: {connectionRef: {name: openbao}, name: ".."}`},
			{name: "root namespace name", wantErr: "spec.name", doc: `
apiVersion: sys.openbaoconfig.io/v1alpha1
kind: OpenBaoNamespace
metadata: {name: root-name, namespace: openbao-config}
spec: {connectionRef: {name: openbao}, name: root}`},
			{name: "nested namespace name", wantErr: "spec.name", doc: `
apiVersion: sys.openbaoconfig.io/v1alpha1
kind: OpenBaoNamespace
metadata: {name: nested, namespace: openbao-config}
spec: {connectionRef: {name: openbao}, name: a/b}`},
			{name: "lockout on kubernetes", wantErr: "userLockout is supported only", doc: `
apiVersion: sys.openbaoconfig.io/v1alpha1
kind: AuthMount
metadata: {name: lockout, namespace: openbao-config}
spec: {connectionRef: {name: openbao}, type: kubernetes, userLockout: {threshold: 3}}`},
			{name: "default ttl above max", wantErr: "defaultLeaseTTL must not exceed maxLeaseTTL", doc: `
apiVersion: sys.openbaoconfig.io/v1alpha1
kind: SecretsMount
metadata: {name: ttl, namespace: openbao-config}
spec: {connectionRef: {name: openbao}, type: kv, defaultLeaseTTL: 2h, maxLeaseTTL: 1h}`},
			{name: "kv alias with version option", wantErr: "not both", doc: `
apiVersion: sys.openbaoconfig.io/v1alpha1
kind: SecretsMount
metadata: {name: alias, namespace: openbao-config}
spec: {connectionRef: {name: openbao}, type: kv-v2, options: {version: "1"}}`},
			{name: "quota in a namespace", wantErr: "root namespace", doc: `
apiVersion: sys.openbaoconfig.io/v1alpha1
kind: RateLimitQuota
metadata: {name: ns, namespace: openbao-config}
spec: {connectionRef: {name: openbao}, namespace: team, rate: "10"}`},
			{name: "quota zero rate", wantErr: "rate must be greater than 0", doc: `
apiVersion: sys.openbaoconfig.io/v1alpha1
kind: RateLimitQuota
metadata: {name: zero, namespace: openbao-config}
spec: {connectionRef: {name: openbao}, rate: "0.0"}`},
			{name: "quota role without path", wantErr: "role requires path", doc: `
apiVersion: sys.openbaoconfig.io/v1alpha1
kind: RateLimitQuota
metadata: {name: role, namespace: openbao-config}
spec: {connectionRef: {name: openbao}, rate: "10", role: ci}`},
			{name: "quota short interval", wantErr: "interval must be at least 1s", doc: `
apiVersion: sys.openbaoconfig.io/v1alpha1
kind: RateLimitQuota
metadata: {name: short, namespace: openbao-config}
spec: {connectionRef: {name: openbao}, rate: "10", interval: 500ms}`},
		} {
			err := k8s.Create(ctx, secretsDecodeString(t, tt.doc), client.DryRunAll)
			if !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("%s: got %v, want invalid with %q", tt.name, err, tt.wantErr)
			}
		}
	})

	t.Run("namespace names", func(t *testing.T) {
		for _, name := range []string{"r", "x.", ".x", "roo", "rooT", "roots", "team.a_b-c"} {
			doc := "apiVersion: sys.openbaoconfig.io/v1alpha1\nkind: OpenBaoNamespace\nmetadata: {name: valid, namespace: openbao-config}\nspec: {connectionRef: {name: openbao}, name: \"" + name + "\"}"
			if err := k8s.Create(ctx, secretsDecodeString(t, doc), client.DryRunAll); err != nil {
				t.Errorf("name %q: %v", name, err)
			}
		}
	})

	t.Run("immutable", func(t *testing.T) {
		docs := map[string]string{
			"OpenBaoNamespace": `
apiVersion: sys.openbaoconfig.io/v1alpha1
kind: OpenBaoNamespace
metadata: {name: immutable, namespace: openbao-config}
spec: {connectionRef: {name: openbao}}`,
			"AuthMount": `
apiVersion: sys.openbaoconfig.io/v1alpha1
kind: AuthMount
metadata: {name: immutable, namespace: openbao-config}
spec: {connectionRef: {name: openbao}, type: userpass, local: false}`,
			"SecretsMount": `
apiVersion: sys.openbaoconfig.io/v1alpha1
kind: SecretsMount
metadata: {name: immutable, namespace: openbao-config}
spec: {connectionRef: {name: openbao}, type: kv, options: {version: "2"}}`,
			"PasswordPolicy": `
apiVersion: sys.openbaoconfig.io/v1alpha1
kind: PasswordPolicy
metadata: {name: immutable, namespace: openbao-config}
spec: {connectionRef: {name: openbao}, policy: "length = 20"}`,
			"RateLimitQuota": `
apiVersion: sys.openbaoconfig.io/v1alpha1
kind: RateLimitQuota
metadata: {name: immutable, namespace: openbao-config}
spec: {connectionRef: {name: openbao}, rate: "10"}`,
		}
		changes := map[string]map[string]any{
			"OpenBaoNamespace": {"name": "other"},
			"AuthMount":        {"path": "other", "type": "approle", "local": true, "sealWrap": true},
			"SecretsMount":     {"path": "other", "type": "pki"},
			"PasswordPolicy":   {"name": "other"},
			"RateLimitQuota":   {"name": "other"},
		}
		for kind, doc := range docs {
			obj := secretsDecodeString(t, doc)
			if err := k8s.Create(ctx, obj); err != nil {
				t.Fatalf("%s: %v", kind, err)
			}
			for field, value := range changes[kind] {
				changed := obj.DeepCopy()
				if err := unstructured.SetNestedField(changed.Object, value, "spec", field); err != nil {
					t.Fatal(err)
				}
				want := field + " is immutable"
				if err := k8s.Update(ctx, changed); !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), want) {
					t.Errorf("%s spec.%s: got %v, want %q", kind, field, err, want)
				}
			}
		}

		downgrade := secretsDecodeString(t, docs["SecretsMount"])
		current := &unstructured.Unstructured{}
		current.SetGroupVersionKind(downgrade.GroupVersionKind())
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(downgrade), current); err != nil {
			t.Fatal(err)
		}
		if err := unstructured.SetNestedStringMap(current.Object, map[string]string{"version": "1"}, "spec", "options"); err != nil {
			t.Fatal(err)
		}
		if err := k8s.Update(ctx, current); !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), "cannot be downgraded") {
			t.Errorf("kv downgrade: got %v, want it rejected", err)
		}
	})
}
