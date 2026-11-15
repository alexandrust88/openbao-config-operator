//go:build conformance

package conformance

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestConformanceInventory validates coverage before starting any server or applying a kind filter.
func TestConformanceInventory(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	catalog := confLoadCatalog(t, root, filepath.Join(root, "config/crd/bases"))
	exceptions := confLoadExceptions(t, root)
	kinds := map[string]*confKind{}
	builtin, plugins := 0, 0
	for _, k := range catalog.kinds {
		kinds[k.Kind] = k
		if k.plugin() {
			plugins++
			continue
		}
		builtin++
		if len(k.own(true)) == 0 {
			t.Errorf("%s: builtin has no executable conformance example or fixture", k.Kind)
		}
	}
	for key := range exceptions {
		kind, _, _ := strings.Cut(key, "/")
		if kinds[kind] == nil {
			t.Errorf("%s: exception names no managed CRD in the conformance catalogue", key)
		}
	}
	t.Logf("%d managed CRDs: %d builtin fixtures, %d plugin exclusions; %d core server/connection kinds exercised by livekit",
		len(catalog.kinds), builtin, plugins, len(confConnectionKinds))
}
