//go:build integration

package livekit

import (
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestKindClusterPrerequisites(t *testing.T) {
	root := repoRoot(t)
	objects := render(t, kustomizeBin(t, root), filepath.Join(root, "test", "livekit", "kind"))
	exc := loadExceptions(t)
	overlays := overlayDirs(t, kitDir(root))
	byName := make(map[string]*unstructured.Unstructured, len(objects))
	for _, obj := range objects {
		byName[obj.GetName()] = obj
	}
	for _, tc := range []struct {
		kind, name string
		absent     []string
	}{
		{"KubernetesAuthConfig", "root-min-k8s-config", []string{"kubernetesCACert", "tokenReviewerJWTSecretRef"}},
		{"KubernetesAuthConfig", "child-min-k8s-config", []string{"kubernetesCACert", "tokenReviewerJWTSecretRef"}},
		{"KubernetesSecretsConfig", "root-min-k8ss-config", []string{"kubernetesHost", "kubernetesCACert", "serviceAccountJWTSecretRef"}},
		{"KubernetesSecretsConfig", "child-min-k8ss-config", []string{"kubernetesHost", "kubernetesCACert", "serviceAccountJWTSecretRef"}},
		{"JWTAuthConfig", "root-full-jwt-k8s-config", nil},
		{"JWTAuthConfig", "child-full-jwt-k8s-config", nil},
		{"RaftAutopilot", "root-full-sys-autopilot", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obj := byName[tc.name]
			if obj == nil || obj.GetKind() != tc.kind {
				t.Fatalf("kind must apply %s/%s", tc.kind, tc.name)
			}
			label := obj.GetLabels()[labelOverlay]
			for _, e := range exc.NotReady {
				if e.Env != envDev && e.matches(overlays[label], tc.kind, strings.TrimPrefix(tc.name, label+"-")) {
					t.Fatalf("cluster prerequisite is excused on kind: %s", e.Reason)
				}
			}
			for _, field := range tc.absent {
				if _, found, err := unstructured.NestedFieldNoCopy(obj.Object, "spec", field); err != nil || found {
					t.Errorf("spec.%s must stay omitted to test OpenBao's in-cluster defaults", field)
				}
			}
			if tc.kind == "JWTAuthConfig" {
				provider, _, _ := unstructured.NestedString(obj.Object, "spec", "providerConfig", "provider")
				if provider != "kubernetes" {
					t.Fatalf("provider = %q, want kubernetes", provider)
				}
			}
		})
	}
}
