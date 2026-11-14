package examples

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexandrust88/openbao-config-operator/test/testenv"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// TestIdentityOIDCExamples installs the OIDC and MFA CRDs into envtest, applies examples/identity with a
// server-side dry run and checks their CEL rules.
func TestIdentityOIDCExamples(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	kinds := []string{"oidcconfig", "oidckey", "oidcrole", "oidcscope", "oidcassignment", "oidcclient", "oidcprovider",
		"mfamethod", "mfaloginenforcement"}
	k8s := oidcExamplesEnv(t, root, kinds)
	ctx := context.Background()
	if err := k8s.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "openbao-config"}}); err != nil {
		t.Fatal(err)
	}

	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			for _, obj := range oidcExamplesDecode(t, filepath.Join(root, "examples", "identity", kind+".yaml")) {
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
			{name: "mfa method without type", wantErr: "set exactly one of totp, duo, okta and pingID", doc: `
kind: MFAMethod
metadata: {name: none}
spec: {connectionRef: {name: openbao}}`},
			{name: "mfa method with two types", wantErr: "set exactly one of totp, duo, okta and pingID", doc: `
kind: MFAMethod
metadata: {name: two}
spec: {connectionRef: {name: openbao}, totp: {issuer: a}, okta: {orgName: o, apiTokenSecretRef: {name: s, key: k}}}`},
			{name: "public client credentials", wantErr: "clientCredentials requires clientType confidential", doc: `
kind: OIDCClient
metadata: {name: public}
spec: {connectionRef: {name: openbao}, clientType: public, clientCredentials: true}`},
			{name: "reserved scope", wantErr: "openid is reserved", doc: `
kind: OIDCScope
metadata: {name: openid}
spec: {connectionRef: {name: openbao}}`},
			{name: "reserved scope by spec name", wantErr: "openid is reserved", doc: `
kind: OIDCScope
metadata: {name: scope}
spec: {connectionRef: {name: openbao}, name: openid}`},
			{name: "builtin assignment", wantErr: "allow_all is built in", doc: `
kind: OIDCAssignment
metadata: {name: a}
spec: {connectionRef: {name: openbao}, name: allow_all}`},
			{name: "entity match with two choices", wantErr: "set exactly one of entityID, entityName, entityRef and entitySelector", doc: `
kind: OIDCAssignment
metadata: {name: two}
spec: {connectionRef: {name: openbao}, entities: [{entityID: e1, entityName: alice}]}`},
			{name: "enforcement without targets", wantErr: "set at least one of authMounts", doc: `
kind: MFALoginEnforcement
metadata: {name: none}
spec: {connectionRef: {name: openbao}, mfaMethods: [{methodID: x}]}`},
			{name: "provider issuer with path", wantErr: "spec.issuer", doc: `
kind: OIDCProvider
metadata: {name: p}
spec: {connectionRef: {name: openbao}, issuer: "https://bao.example.com/v1/identity"}`},
			{name: "role template not json", wantErr: "spec.template", doc: `
kind: OIDCRole
metadata: {name: r}
spec: {connectionRef: {name: openbao}, key: k, template: eyJhIjoiYiJ9}`},
		} {
			err := k8s.Create(ctx, oidcExamplesDoc(t, tt.doc), client.DryRunAll)
			if !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("%s: got %v, want invalid with %q", tt.name, err, tt.wantErr)
			}
		}
	})

	t.Run("immutable", func(t *testing.T) {
		for _, tt := range []struct {
			doc, field string
			value      any
		}{
			{doc: "kind: OIDCClient\nmetadata: {name: imm}\nspec: {connectionRef: {name: openbao}}", field: "key", value: "other"},
			{doc: "kind: OIDCClient\nmetadata: {name: imm2}\nspec: {connectionRef: {name: openbao}}", field: "clientType", value: "public"},
			{doc: "kind: OIDCKey\nmetadata: {name: imm}\nspec: {connectionRef: {name: openbao}}", field: "name", value: "other"},
			{doc: "kind: MFAMethod\nmetadata: {name: imm}\nspec: {connectionRef: {name: openbao}, name: a, totp: {issuer: a}}", field: "name", value: "b"},
		} {
			obj := oidcExamplesDoc(t, tt.doc)
			if err := k8s.Create(ctx, obj); err != nil {
				t.Fatalf("%s: %v", obj.GetName(), err)
			}
			changed := obj.DeepCopy()
			if err := unstructured.SetNestedField(changed.Object, tt.value, "spec", tt.field); err != nil {
				t.Fatal(err)
			}
			if err := k8s.Update(ctx, changed); !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), tt.field+" is immutable") {
				t.Errorf("%s spec.%s: got %v, want immutable", obj.GetKind(), tt.field, err)
			}
		}

		mfa := oidcExamplesDoc(t, "kind: MFAMethod\nmetadata: {name: type}\nspec: {connectionRef: {name: openbao}, totp: {issuer: a}}")
		if err := k8s.Create(ctx, mfa); err != nil {
			t.Fatal(err)
		}
		changed := mfa.DeepCopy()
		unstructured.RemoveNestedField(changed.Object, "spec", "totp")
		if err := unstructured.SetNestedStringMap(changed.Object, map[string]string{"orgName": "o"}, "spec", "okta"); err != nil {
			t.Fatal(err)
		}
		if err := unstructured.SetNestedStringMap(changed.Object, map[string]string{"name": "s", "key": "k"}, "spec", "okta", "apiTokenSecretRef"); err != nil {
			t.Fatal(err)
		}
		if err := k8s.Update(ctx, changed); !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), "method type is immutable") {
			t.Errorf("mfa type change: got %v, want immutable", err)
		}
	})
}

func oidcExamplesEnv(t *testing.T, root string, kinds []string) client.Client {
	t.Helper()
	assets := testenv.RequireAssets(t, root)
	paths := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		plural := kind + "s"
		paths = append(paths, filepath.Join(root, "config", "crd", "bases", "identity.openbaoconfig.io_"+plural+".yaml"))
	}
	env := &envtest.Environment{
		BinaryAssetsDirectory: assets,
		CRDInstallOptions:     envtest.CRDInstallOptions{Paths: paths, ErrorIfPathMissing: true},
	}
	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("envtest: %v", err)
	}
	t.Cleanup(func() {
		if err := env.Stop(); err != nil {
			t.Logf("stop envtest: %v", err)
		}
	})
	k8s, err := client.New(cfg, client.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return k8s
}

func oidcExamplesDecode(t *testing.T, path string) []*unstructured.Unstructured {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoder := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(raw), 4096)
	var objs []*unstructured.Unstructured
	for {
		obj := &unstructured.Unstructured{}
		err := decoder.Decode(&obj.Object)
		if errors.Is(err, io.EOF) {
			return objs
		}
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if len(obj.Object) > 0 {
			objs = append(objs, obj)
		}
	}
}

// oidcExamplesDoc decodes an identity resource written without apiVersion and namespace.
func oidcExamplesDoc(t *testing.T, doc string) *unstructured.Unstructured {
	t.Helper()
	obj := &unstructured.Unstructured{}
	if err := utilyaml.NewYAMLOrJSONDecoder(strings.NewReader(doc), 4096).Decode(&obj.Object); err != nil {
		t.Fatal(err)
	}
	obj.SetAPIVersion("identity.openbaoconfig.io/v1alpha1")
	obj.SetNamespace("openbao-config")
	return obj
}
