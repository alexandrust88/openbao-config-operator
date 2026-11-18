package examples

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestSysAuditExamples installs the Audit CRD into envtest, applies examples/sys/audit.yaml with a server-side dry
// run and checks the CEL rules.
func TestSysAuditExamples(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	k8s := sysConfigExamplesEnv(t, root, []string{"audit"})
	ctx := context.Background()
	if err := k8s.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "openbao-config"}}); err != nil {
		t.Fatal(err)
	}

	for _, obj := range sysConfigExamplesDecode(t, filepath.Join(root, "examples", "sys", "audit.yaml")) {
		if err := k8s.Create(ctx, obj, client.DryRunAll, client.FieldValidation("Strict")); err != nil {
			t.Errorf("%s %s: %v", obj.GetKind(), obj.GetName(), err)
		}
	}

	const meta = "apiVersion: sys.obco.dev/v1alpha1\nkind: Audit\nmetadata: {name: invalid, namespace: openbao-config}\n"
	for _, tt := range []struct {
		name, spec, wantErr string
	}{
		{name: "child namespace", wantErr: "root namespace",
			spec: "spec: {connectionRef: {name: openbao}, openbaoNamespace: team, type: file}"},
		{name: "unknown type", wantErr: "spec.type",
			spec: "spec: {connectionRef: {name: openbao}, type: kafka}"},
		{name: "option set twice", wantErr: "both options and optionsSecretRefs",
			spec: "spec: {connectionRef: {name: openbao}, type: http, options: {headers: x},\n  optionsSecretRefs: {headers: {name: s, key: k}}}"},
		{name: "path with empty segment", wantErr: "spec.path",
			spec: "spec: {connectionRef: {name: openbao}, type: file, path: a//b}"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := k8s.Create(ctx, sysConfigExamplesDecodeString(t, meta+tt.spec), client.DryRunAll)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}

	t.Run("immutable", func(t *testing.T) {
		audit := sysConfigExamplesDecodeString(t, `apiVersion: sys.obco.dev/v1alpha1
kind: Audit
metadata: {name: immutable, namespace: openbao-config}
spec: {connectionRef: {name: openbao}, type: file, path: file, description: d, options: {file_path: stdout},
  optionsSecretRefs: {prefix: {name: s, key: k}}}`)
		if err := k8s.Create(ctx, audit); err != nil {
			t.Fatal(err)
		}
		for field, value := range map[string]any{
			"path":              "other",
			"type":              "syslog",
			"description":       "other",
			"local":             true,
			"options":           map[string]any{"file_path": "discard"},
			"optionsSecretRefs": map[string]any{"prefix": map[string]any{"name": "s", "key": "other"}},
		} {
			changed := audit.DeepCopy()
			if err := unstructured.SetNestedField(changed.Object, value, "spec", field); err != nil {
				t.Fatal(err)
			}
			if err := k8s.Update(ctx, changed, client.DryRunAll); err == nil || !strings.Contains(err.Error(), field+" is immutable") {
				t.Errorf("update of spec.%s: err = %v", field, err)
			}
		}
		for _, field := range []string{"path", "description", "options", "optionsSecretRefs"} {
			changed := audit.DeepCopy()
			unstructured.RemoveNestedField(changed.Object, "spec", field)
			if err := k8s.Update(ctx, changed, client.DryRunAll); err == nil || !strings.Contains(err.Error(), field+" is immutable") {
				t.Errorf("removal of spec.%s: err = %v", field, err)
			}
		}
	})
}
