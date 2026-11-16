//go:build integration

package nsmatrix

import (
	"path/filepath"
	"testing"
)

func TestNamespaceCatalogue(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	availability := nsmAvailability(t, filepath.Join(root, "docs", "kinds.md"))
	catalog := nsmLoadCatalog(t, root, filepath.Join(root, "config", "crd", "bases"))
	builtin, plugin := 0, 0
	for _, kind := range catalog.kinds {
		if availability[kind.Kind] == "" {
			t.Errorf("%s has no catalogue availability; it must not silently become builtin", kind.Kind)
		}
		if kind.Availability != availability[kind.Kind] {
			t.Errorf("%s availability = %q, catalogue says %q", kind.Kind, kind.Availability, availability[kind.Kind])
		}
		if kind.plugin() {
			plugin++
		} else {
			builtin++
		}
	}
	if builtin != 71 || plugin != 52 {
		t.Fatalf("namespace matrix inventory: %d builtin managed kinds, %d plugin kinds; want 71 and 52", builtin, plugin)
	}
	for kind := range nsmConnectionKinds {
		if availability[kind] != "builtin" {
			t.Errorf("harness-managed kind %s availability = %q, want builtin", kind, availability[kind])
		}
	}
	t.Logf("%d builtin CRDs (%d managed + %d harness-managed), %d plugin-gated CRDs",
		builtin+len(nsmConnectionKinds), builtin, len(nsmConnectionKinds), plugin)
}
