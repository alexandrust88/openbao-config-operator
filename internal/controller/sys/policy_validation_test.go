package sys

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/test/testenv"
)

const (
	testNamespace   = "team"
	policyNameField = "spec.name"
	rootPolicy      = "root"
	defaultPolicy   = "default"
)

func startPolicyEnvtest(t *testing.T) client.Client {
	t.Helper()
	root := filepath.Join("..", "..", "..")
	env := &envtest.Environment{
		BinaryAssetsDirectory: testenv.RequireAssets(t, root),
		ErrorIfCRDPathMissing: true,
		CRDDirectoryPaths:     []string{filepath.Join(root, "config", "crd", "bases", "sys.openbaoconfig.io_policies.yaml")},
	}
	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("start envtest: %v", err)
	}
	t.Cleanup(func() { _ = env.Stop() })
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := sysv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testNamespace}}
	if err := c.Create(context.Background(), ns); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestPolicyValidation(t *testing.T) {
	c := startPolicyEnvtest(t)
	tests := []struct {
		name, metaName, specName string
		deletionPolicy           commonv1alpha1.DeletionPolicy
		wantErr                  string
	}{
		{name: "metadata name", metaName: "reader"},
		{name: "lowercase spec name with segments", metaName: "a", specName: "team/app.reader-1"},
		{name: "dotted segment", metaName: "b", specName: "...x/.y"},
		{name: "uppercase", metaName: "c", specName: "Reader", wantErr: policyNameField},
		{name: "non-ascii uppercase", metaName: "d", specName: "lesérÄ", wantErr: policyNameField},
		{name: "whitespace", metaName: "e", specName: "reader ", wantErr: policyNameField},
		{name: "trailing slash", metaName: "f", specName: "team/", wantErr: policyNameField},
		{name: "empty segment", metaName: "g", specName: "team//app", wantErr: policyNameField},
		{name: "dot dot segment", metaName: "h", specName: "team/../root", wantErr: policyNameField},
		{name: rootPolicy, metaName: rootPolicy, wantErr: "cannot be changed"},
		{name: "response-wrapping", metaName: "i", specName: "response-wrapping", wantErr: "cannot be changed"},
		{name: "default with delete", metaName: defaultPolicy, deletionPolicy: commonv1alpha1.DeletionPolicyDelete, wantErr: "deletionPolicy to Orphan"},
		{name: "default with the defaulted policy", metaName: "k", specName: defaultPolicy},
		{name: "default with orphan", metaName: "j", specName: defaultPolicy, deletionPolicy: commonv1alpha1.DeletionPolicyOrphan},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newPolicy(tt.metaName, tt.specName, `path "kv/*" { capabilities = ["read"] }`)
			p.Spec.ConnectionRef = commonv1alpha1.ConnectionReference{Name: "bao"}
			p.Spec.DeletionPolicy = tt.deletionPolicy
			err := c.Create(context.Background(), p)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("rejected: %v", err)
			case tt.wantErr != "" && err == nil:
				t.Fatal("accepted")
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Fatalf("error %q does not mention %q", err, tt.wantErr)
			}
		})
	}
}
