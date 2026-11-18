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

// TestSecretsExamples applies examples/secrets with a server-side dry run and checks the CEL rules of the
// secrets kinds against envtest.
func TestSecretsExamples(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	k8s := secretsStartEnv(t, root, []string{
		"secrets.obco.dev_kvsecrets.yaml",
		"secrets.obco.dev_randomsecrets.yaml",
		"secrets.obco.dev_kvconfigs.yaml",
	})
	ctx := context.Background()
	if err := k8s.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "openbao-config"}}); err != nil {
		t.Fatal(err)
	}

	for _, file := range []string{"kvsecret.yaml", "randomsecret.yaml", "kvconfig.yaml"} {
		t.Run(file, func(t *testing.T) {
			for _, obj := range secretsDecode(t, filepath.Join(root, "examples", "secrets", file)) {
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
			{name: "kvsecret without data", wantErr: "set dataFrom or data", doc: `
apiVersion: secrets.obco.dev/v1alpha1
kind: KVSecret
metadata: {name: no-data, namespace: openbao-config}
spec: {connectionRef: {name: openbao}, mountPath: secret, path: app}`},
			{name: "kvsecret version 3", wantErr: "kvVersion", doc: `
apiVersion: secrets.obco.dev/v1alpha1
kind: KVSecret
metadata: {name: bad-version, namespace: openbao-config}
spec: {connectionRef: {name: openbao}, mountPath: secret, path: app, kvVersion: 3,
  dataFrom: [{secretRef: {name: s}}]}`},
			{name: "kvsecret leading slash", wantErr: "spec.path", doc: `
apiVersion: secrets.obco.dev/v1alpha1
kind: KVSecret
metadata: {name: bad-path, namespace: openbao-config}
spec: {connectionRef: {name: openbao}, mountPath: secret, path: /app, dataFrom: [{secretRef: {name: s}}]}`},
			{name: "randomsecret policy and generator", wantErr: "set at most one of passwordPolicyName and generator", doc: `
apiVersion: secrets.obco.dev/v1alpha1
kind: RandomSecret
metadata: {name: both, namespace: openbao-config}
spec: {connectionRef: {name: openbao}, mountPath: secret, path: app, passwordPolicyName: p, generator: {length: 16}}`},
			{name: "randomsecret short generator", wantErr: "spec.generator.length", doc: `
apiVersion: secrets.obco.dev/v1alpha1
kind: RandomSecret
metadata: {name: short, namespace: openbao-config}
spec: {connectionRef: {name: openbao}, mountPath: secret, path: app, generator: {length: 4}}`},
			{name: "kvconfig delete policy", wantErr: "deletionPolicy must be Orphan", doc: `
apiVersion: secrets.obco.dev/v1alpha1
kind: KVConfig
metadata: {name: delete, namespace: openbao-config}
spec: {connectionRef: {name: openbao}, mountPath: secret, deletionPolicy: Delete}`},
			{name: "kvconfig bad duration", wantErr: "must be -1, integer seconds or a duration", doc: `
apiVersion: secrets.obco.dev/v1alpha1
kind: KVConfig
metadata: {name: bad-duration, namespace: openbao-config}
spec: {connectionRef: {name: openbao}, mountPath: secret, deleteVersionAfter: 500ms}`},
			{name: "kvconfig negative seconds", wantErr: "must be -1, integer seconds or a duration", doc: `
apiVersion: secrets.obco.dev/v1alpha1
kind: KVConfig
metadata: {name: negative, namespace: openbao-config}
spec: {connectionRef: {name: openbao}, mountPath: secret, deleteVersionAfter: -5}`},
			{name: "kvconfig disabled", doc: `
apiVersion: secrets.obco.dev/v1alpha1
kind: KVConfig
metadata: {name: disabled, namespace: openbao-config}
spec: {connectionRef: {name: openbao}, mountPath: secret, deleteVersionAfter: -1}`},
		} {
			obj := secretsDecodeString(t, tt.doc)
			err := k8s.Create(ctx, obj, client.DryRunAll)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("%s: %v", tt.name, err)
			case tt.wantErr != "" && (!apierrors.IsInvalid(err) || !strings.Contains(err.Error(), tt.wantErr)):
				t.Errorf("%s: got %v, want invalid with %q", tt.name, err, tt.wantErr)
			}
		}
	})

	t.Run("immutable", func(t *testing.T) {
		docs := map[string]string{
			"KVSecret": `
apiVersion: secrets.obco.dev/v1alpha1
kind: KVSecret
metadata: {name: immutable, namespace: openbao-config}
spec: {connectionRef: {name: openbao}, mountPath: secret, path: app, dataFrom: [{secretRef: {name: s}}]}`,
			"RandomSecret": `
apiVersion: secrets.obco.dev/v1alpha1
kind: RandomSecret
metadata: {name: immutable, namespace: openbao-config}
spec: {connectionRef: {name: openbao}, mountPath: secret, path: app}`,
			"KVConfig": `
apiVersion: secrets.obco.dev/v1alpha1
kind: KVConfig
metadata: {name: immutable, namespace: openbao-config}
spec: {connectionRef: {name: openbao}, mountPath: secret}`,
		}
		changes := map[string]map[string]any{
			"KVSecret":     {"mountPath": "other", "path": "other", "kvVersion": int64(1)},
			"RandomSecret": {"mountPath": "other", "path": "other", "kvVersion": int64(1), "key": "other"},
			"KVConfig":     {"mountPath": "other"},
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
				err := k8s.Update(ctx, changed)
				if !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), field+" is immutable") {
					t.Errorf("%s spec.%s: got %v, want immutable", kind, field, err)
				}
			}
		}
	})
}

func secretsStartEnv(t *testing.T, root string, crds []string) client.Client {
	t.Helper()
	assets := testenv.RequireAssets(t, root)
	paths := make([]string, 0, len(crds))
	for _, crd := range crds {
		paths = append(paths, filepath.Join(root, "config", "crd", "bases", crd))
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

func secretsDecode(t *testing.T, path string) []*unstructured.Unstructured {
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
			t.Fatalf("decode %s: %v", path, err)
		}
		if len(obj.Object) > 0 {
			objs = append(objs, obj)
		}
	}
}

func secretsDecodeString(t *testing.T, doc string) *unstructured.Unstructured {
	t.Helper()
	obj := &unstructured.Unstructured{}
	if err := utilyaml.NewYAMLOrJSONDecoder(strings.NewReader(doc), 4096).Decode(&obj.Object); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return obj
}
