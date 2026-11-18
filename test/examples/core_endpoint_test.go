package examples

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestCoreEndpointCEL checks the CEL rules of BaoEndpoint against envtest.
func TestCoreEndpointCEL(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	k8s := secretsStartEnv(t, root, []string{"core.obco.dev_baoendpoints.yaml"})
	ctx := context.Background()
	const head = "apiVersion: core.obco.dev/v1alpha1\nkind: BaoEndpoint\nmetadata: {name: x, namespace: default}\n"

	noData := secretsDecodeString(t, head+"spec: {connectionRef: {name: openbao}, path: sys/mounts/x}")
	if err := k8s.Create(ctx, noData, client.DryRunAll); !apierrors.IsInvalid(err) ||
		!strings.Contains(err.Error(), "data or dataSecretRef is required") {
		t.Errorf("without data: got %v, want invalid with the data rule", err)
	}

	obj := secretsDecodeString(t, head+"spec: {connectionRef: {name: openbao}, path: sys/mounts/x, data: {type: kv}}")
	if err := k8s.Create(ctx, obj); err != nil {
		t.Fatal(err)
	}
	changed := obj.DeepCopy()
	if err := unstructured.SetNestedField(changed.Object, "sys/mounts/y", "spec", "path"); err != nil {
		t.Fatal(err)
	}
	if err := k8s.Update(ctx, changed); !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), "path is immutable") {
		t.Errorf("path change: got %v, want invalid with %q", err, "path is immutable")
	}

	example, err := os.ReadFile(filepath.Join(root, "examples", "core", "baoendpoint.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	sample := secretsDecodeString(t, string(example))
	sample.SetNamespace("default")
	if err := k8s.Create(ctx, sample, client.DryRunAll); err != nil {
		t.Errorf("examples/core/baoendpoint.yaml: %v", err)
	}
}
