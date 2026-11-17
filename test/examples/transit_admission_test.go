package examples

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestTransitAdmissionPolicy(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	k8s := secretsStartEnv(t, root, []string{"secrets.openbaoconfig.io_transitkeys.yaml"})
	ctx := context.Background()
	for _, name := range []string{"guarded", "unrestricted"} {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
		if name == "guarded" {
			ns.Labels = map[string]string{"openbaoconfig.io/non-exportable-transit": "true"}
		}
		if err := k8s.Create(ctx, ns); err != nil {
			t.Fatal(err)
		}
	}
	for _, obj := range secretsDecode(t, filepath.Join(root, "examples/security/transit-key-policy.yaml")) {
		if err := k8s.Create(ctx, obj, client.FieldValidation("Strict")); err != nil {
			t.Fatal(err)
		}
	}
	key := func(namespace, name string) *unstructured.Unstructured {
		obj := secretsDecodeString(t, `apiVersion: secrets.openbaoconfig.io/v1alpha1
kind: TransitKey
metadata: {name: key}
spec: {connectionRef: {name: openbao}}`)
		obj.SetNamespace(namespace)
		obj.SetName(name)
		return obj
	}
	// Admission policies reach the API server's evaluator asynchronously.
	probe := key("guarded", "probe")
	if err := unstructured.SetNestedField(probe.Object, true, "spec", "exportable"); err != nil {
		t.Fatal(err)
	}
	err = wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 15*time.Second, true, func(ctx context.Context) (bool, error) {
		err := k8s.Create(ctx, probe.DeepCopy(), client.DryRunAll)
		if err != nil && strings.Contains(err.Error(), "openbao-transit-non-exportable") && strings.Contains(err.Error(), "must not be exportable") {
			return true, nil
		}
		return false, err
	})
	if err != nil {
		t.Fatalf("admission policy not enforced: %v", err)
	}
	for _, field := range []string{"exportable", "allowPlaintextBackup"} {
		t.Run(field, func(t *testing.T) {
			obj := key("guarded", strings.ToLower(field))
			if err := k8s.Create(ctx, obj); err != nil {
				t.Fatalf("omitted flag rejected: %v", err)
			}
			if err := unstructured.SetNestedField(obj.Object, false, "spec", field); err != nil {
				t.Fatal(err)
			}
			if err := k8s.Update(ctx, obj); err != nil {
				t.Fatalf("false flag rejected: %v", err)
			}
			if err := unstructured.SetNestedField(obj.Object, true, "spec", field); err != nil {
				t.Fatal(err)
			}
			if err := k8s.Update(ctx, obj); !apierrors.IsForbidden(err) {
				t.Fatalf("enabled flag update: got %v, want Forbidden", err)
			}
			obj.SetResourceVersion("")
			obj.SetUID("")
			obj.SetName("new-" + strings.ToLower(field))
			if err := k8s.Create(ctx, obj, client.DryRunAll); !apierrors.IsForbidden(err) {
				t.Fatalf("enabled flag create: got %v, want Forbidden", err)
			}
			obj.SetNamespace("unrestricted")
			if err := k8s.Create(ctx, obj, client.DryRunAll); err != nil {
				t.Fatalf("unselected namespace rejected: %v", err)
			}
		})
	}
}
