package engine

import (
	"slices"
	"strings"
	"testing"

	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// FuzzAbsPath checks the absolute path that keys the ownership index and the create lock: PathIndexFunc applies
// CanonicalPath to the recorded status.path, so a path the reconciler builds must be a fixed point of it.
func FuzzAbsPath(f *testing.F) {
	for _, ns := range []string{"", testNamespace, "tenant/team", "/tenant/", "root", "a/../b"} {
		for _, p := range []string{"sys/policies/acl/x", "/a/", "secret/data/a.b", "kv/..x/y", "a//b", "../x", "/"} {
			f.Add(ns, p)
		}
	}
	f.Fuzz(func(t *testing.T, ns, p string) {
		if openbao.ValidateNamespace(ns) != nil || openbao.ValidatePath(p) != nil {
			return
		}
		if got := strings.Join(namespaceSegments(ns), "/"); got != ns {
			t.Fatalf("namespaceSegments(%q) joins to %q", ns, got)
		}
		c := openbao.CanonicalPath(p)
		if c == "" {
			return
		}
		abs := joinPath(ns, c)
		if openbao.CanonicalPath(abs) != abs {
			t.Fatalf("joinPath(%q, %q) = %q is not canonical, the ownership index would miss it", ns, c, abs)
		}
		if slices.ContainsFunc(strings.Split(abs, "/"), func(s string) bool { return s == "" || s == "." || s == ".." }) {
			t.Fatalf("joinPath(%q, %q) = %q has an empty, . or .. segment", ns, c, abs)
		}
		if ns != "" && !strings.HasPrefix(abs, ns+"/") {
			t.Fatalf("joinPath(%q, %q) = %q is outside the namespace", ns, c, abs)
		}
		if first, rest, ok := strings.Cut(c, "/"); ok {
			nested := strings.TrimPrefix(ns+"/"+first, "/")
			if got := joinPath(nested, rest); got != abs {
				t.Fatalf("namespace %q path %q gives %q, but namespace %q path %q gives %q", ns, c, abs, nested,
					rest, got)
			}
		}
	})
}
