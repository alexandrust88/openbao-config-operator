// Package samplegen checks that every generated sample passes server-side validation, CEL included.
package samplegen

import (
	"context"
	"path/filepath"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/alexandrust88/openbao-config-operator/hack/samplegen/gen"
	"github.com/alexandrust88/openbao-config-operator/test/testenv"
)

var crdDir = filepath.Join("..", "..", "config", "crd", "bases")

func TestSamplesPassServerSideValidation(t *testing.T) {
	assets := testenv.RequireAssets(t, filepath.Join("..", ".."))
	samples, err := gen.Generate(crdDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) == 0 {
		t.Fatalf("no CRDs found in %s", crdDir)
	}

	env := &envtest.Environment{BinaryAssetsDirectory: assets}
	cfg, err := env.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := env.Stop(); err != nil {
			t.Error(err)
		}
	})
	installCRDs(t, env)

	ctx := context.Background()
	k8s, err := client.New(cfg, client.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: gen.SampleNamespace}}
	if err := k8s.Create(ctx, ns); err != nil {
		t.Fatal(err)
	}

	passed := 0
	for _, s := range samples {
		if t.Run(s.File(), func(t *testing.T) { dryRunSample(ctx, t, k8s, s) }) {
			passed++
		}
	}
	t.Logf("%d of %d samples pass server-side dry-run validation", passed, len(samples))
}

// installCRDs installs each CRD on its own so one CRD the API server rejects only fails its own sample.
func installCRDs(t *testing.T, env *envtest.Environment) {
	files, err := filepath.Glob(filepath.Join(crdDir, "*.openbaoconfig.io_*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if _, err := envtest.InstallCRDs(env.Config, envtest.CRDInstallOptions{Paths: []string{f}}); err != nil {
			t.Errorf("install %s: %v", filepath.Base(f), err)
		}
	}
}

func dryRunSample(ctx context.Context, t *testing.T, k8s client.Client, s gen.Sample) {
	for _, s := range s.Warnings {
		t.Logf("generator warning: %s", s)
	}
	for _, doc := range s.Docs {
		obj := &unstructured.Unstructured{Object: runtime.DeepCopyJSON(doc)}
		if err := k8s.Create(ctx, obj, client.DryRunAll); err != nil {
			t.Errorf("%s %s: %v", obj.GetKind(), obj.GetName(), err)
		}
	}
}
