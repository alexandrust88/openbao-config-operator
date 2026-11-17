//go:build docs

package docs

import "testing"

// Validate the checked-in public GitOps configuration against the installed CRD schema.
func gitopsFixtures(t *testing.T) []source {
	t.Helper()
	sources := fileSources(t, root, "gitops/config", "kustomization.yaml")
	if len(sources) == 0 {
		t.Fatal("public GitOps configuration must not be empty")
	}
	return sources
}
