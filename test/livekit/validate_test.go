//go:build integration

package livekit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/alexandrust88/openbao-config-operator/test/testenv"
)

// TestServerSideValidation creates every object of every overlay as a server-side dry run against envtest with
// the real CRDs, so OpenAPI and CEL rejections show up without OpenBao. LIVEKIT_EXTRA_OVERLAYS adds overlay
// directories (comma separated), for example private environment overlays.
func TestServerSideValidation(t *testing.T) {
	ctrllog.SetLogger(logr.Discard())
	root := repoRoot(t)
	kit := kitDir(root)
	kustomize := kustomizeBin(t, root)
	assets := testenv.RequireAssets(t, root)
	c := startCluster(t, assets, filepath.Join(root, "config", "crd", "bases"), t.TempDir())
	ctx := context.Background()

	dirs := map[string]string{}
	for _, o := range overlays(t, kit) {
		dirs[o] = filepath.Join(kit, o)
	}
	for extra := range strings.SplitSeq(os.Getenv("LIVEKIT_EXTRA_OVERLAYS"), ",") {
		if extra = strings.TrimSpace(extra); extra != "" {
			dirs[extra] = extra
		}
	}
	created := map[string]bool{}
	for name, dir := range dirs {
		t.Run(name, func(t *testing.T) {
			for _, o := range render(t, kustomize, dir) {
				if ns := o.GetNamespace(); ns != "" && !created[ns] {
					if err := c.k8s.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}); err != nil {
						t.Fatal(err)
					}
					created[ns] = true
				}
				if err := c.k8s.Create(ctx, o, client.DryRunAll); err != nil {
					t.Errorf("%s/%s: %v", o.GetKind(), o.GetName(), err)
				}
			}
		})
	}
}
