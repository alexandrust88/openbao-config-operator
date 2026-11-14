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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// TestSysConfigExamples installs the sys singleton, UIHeader, AuditRequestHeader, Workflow and Plugin CRDs into
// envtest, applies their examples/sys files with a server-side dry run and checks their CEL rules.
func TestSysConfigExamples(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	kinds := []string{"corsconfig", "uiheader", "logger", "auditrequestheader", "quotaconfig", "keyrotationconfig",
		"raftautopilot", "workflow", "plugin"}
	k8s := sysConfigExamplesEnv(t, root, kinds)
	ctx := context.Background()
	if err := k8s.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "openbao-config"}}); err != nil {
		t.Fatal(err)
	}

	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			for _, obj := range sysConfigExamplesDecode(t, filepath.Join(root, "examples", "sys", kind+".yaml")) {
				if err := k8s.Create(ctx, obj, client.DryRunAll, client.FieldValidation("Strict")); err != nil {
					t.Errorf("%s %s: %v", obj.GetKind(), obj.GetName(), err)
				}
			}
		})
	}

	t.Run("validation", func(t *testing.T) {
		const meta = "apiVersion: sys.openbaoconfig.io/v1alpha1\nmetadata: {name: invalid, namespace: openbao-config}\n"
		for _, tt := range []struct {
			name, doc, wantErr string
		}{
			{name: "cors in a child namespace", wantErr: "root namespace", doc: `kind: CORSConfig
spec: {connectionRef: {name: openbao}, namespace: team, allowedOrigins: [https://a.example.com]}`},
			{name: "cors wildcard with others", wantErr: "only entry", doc: `kind: CORSConfig
spec: {connectionRef: {name: openbao}, allowedOrigins: ["*", https://a.example.com]}`},
			{name: "x-vault ui header", wantErr: "X-Vault-* headers cannot be set", doc: `kind: UIHeader
spec: {connectionRef: {name: openbao}, name: X-Vault-Token, values: [x]}`},
			{name: "unknown log level", wantErr: "spec.level", doc: `kind: Logger
spec: {connectionRef: {name: openbao}, level: verbose}`},
			{name: "short rotation interval", wantErr: "at least 24h", doc: `kind: KeyRotationConfig
spec: {connectionRef: {name: openbao}, interval: 1h}`},
			{name: "cleanup without quorum", wantErr: "minQuorum of at least 3", doc: `kind: RaftAutopilot
spec: {connectionRef: {name: openbao}, cleanupDeadServers: true}`},
			{name: "short dead server threshold", wantErr: "at least 1m", doc: `kind: RaftAutopilot
spec: {connectionRef: {name: openbao}, deadServerLastContactThreshold: 30s}`},
			{name: "deleting a versioned plugin", wantErr: "deletionPolicy Orphan", doc: `kind: Plugin
spec: {connectionRef: {name: openbao}, deletionPolicy: Delete, type: auth, command: p, version: v1.0.0,
  sha256: 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef}`},
			{name: "plugin command with parent reference", wantErr: "'..'", doc: `kind: Plugin
spec: {connectionRef: {name: openbao}, type: auth, command: a..b,
  sha256: 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef}`},
		} {
			t.Run(tt.name, func(t *testing.T) {
				obj := sysConfigExamplesDecodeString(t, meta+tt.doc)
				err := k8s.Create(ctx, obj, client.DryRunAll)
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("err = %v, want it to contain %q", err, tt.wantErr)
				}
			})
		}
	})

	t.Run("defaults only", func(t *testing.T) {
		const meta = "apiVersion: sys.openbaoconfig.io/v1alpha1\nmetadata: {name: defaults, namespace: openbao-config}\n"
		for _, doc := range []string{"kind: KeyRotationConfig\nspec: {connectionRef: {name: openbao}}",
			"kind: RaftAutopilot\nspec: {connectionRef: {name: openbao}, minQuorum: 0}"} {
			if err := k8s.Create(ctx, sysConfigExamplesDecodeString(t, meta+doc), client.DryRunAll); err != nil {
				t.Errorf("%s: %v", doc, err)
			}
		}
	})

	t.Run("x-vault ui header from metadata.name", func(t *testing.T) {
		obj := sysConfigExamplesDecodeString(t, `apiVersion: sys.openbaoconfig.io/v1alpha1
kind: UIHeader
metadata: {name: x-vault-token, namespace: openbao-config}
spec: {connectionRef: {name: openbao}, values: [x]}`)
		if err := k8s.Create(ctx, obj, client.DryRunAll); err == nil || !strings.Contains(err.Error(), "X-Vault-* headers cannot be set") {
			t.Errorf("err = %v, want the X-Vault-* rule", err)
		}
	})

	t.Run("immutable", func(t *testing.T) {
		plugin := sysConfigExamplesDecodeString(t, `apiVersion: sys.openbaoconfig.io/v1alpha1
kind: Plugin
metadata: {name: immutable, namespace: openbao-config}
spec: {connectionRef: {name: openbao}, type: auth, name: ldap, command: p, version: v1.0.0,
  sha256: 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef}`)
		if err := k8s.Create(ctx, plugin); err != nil {
			t.Fatal(err)
		}
		for field, value := range map[string]string{"type": "secret", "name": "other", "version": "v2.0.0"} {
			changed := plugin.DeepCopy()
			if err := unstructured.SetNestedField(changed.Object, value, "spec", field); err != nil {
				t.Fatal(err)
			}
			if err := k8s.Update(ctx, changed, client.DryRunAll); err == nil || !strings.Contains(err.Error(), field+" is immutable") {
				t.Errorf("update of spec.%s: err = %v", field, err)
			}
		}
	})
}

func sysConfigExamplesEnv(t *testing.T, root string, kinds []string) client.Client {
	t.Helper()
	assets := testenv.RequireAssets(t, root)
	crds := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		crds = append(crds, filepath.Join(root, "config", "crd", "bases", "sys.openbaoconfig.io_"+kind+"s.yaml"))
	}
	testEnv := &envtest.Environment{
		BinaryAssetsDirectory: assets,
		CRDInstallOptions:     envtest.CRDInstallOptions{Paths: crds, ErrorIfPathMissing: true},
	}
	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("envtest: %v", err)
	}
	t.Cleanup(func() {
		if err := testEnv.Stop(); err != nil {
			t.Logf("stop envtest: %v", err)
		}
	})
	k8s, err := client.New(cfg, client.Options{Scheme: clientgoscheme.Scheme})
	if err != nil {
		t.Fatal(err)
	}
	return k8s
}

func sysConfigExamplesDecode(t *testing.T, path string) []*unstructured.Unstructured {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoder := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(raw), 4096)
	var objs []*unstructured.Unstructured
	for {
		obj := &unstructured.Unstructured{}
		if err := decoder.Decode(&obj.Object); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if len(obj.Object) > 0 {
			objs = append(objs, obj)
		}
	}
	if len(objs) == 0 {
		t.Fatalf("%s holds no objects", path)
	}
	return objs
}

func sysConfigExamplesDecodeString(t *testing.T, doc string) *unstructured.Unstructured {
	t.Helper()
	obj := &unstructured.Unstructured{}
	if err := utilyaml.NewYAMLOrJSONDecoder(strings.NewReader(doc), 4096).Decode(&obj.Object); err != nil {
		t.Fatal(err)
	}
	return obj
}
