package openbao

import (
	pathpkg "path"
	"slices"
	"strings"
	"testing"
)

var fuzzPathSeeds = []string{
	"", "/", "//", "///", "/a/", "a/", "sys/policies/acl/x", "sys/mounts/", "secret/data/a.b", "kv/..x/y",
	"secret/../sys/policies/acl/x", "..", ".", "kv/..", "a/./b", "a//b", "../x", "kv/../", "/k8s-a/", "team/k8s",
	"auth", "auth/", "auth/kubernetes", "/auth/kubernetes", "team/../k8s", "team//k8s", "team-a/../team-b",
	"./team-a", "root", "root/team-a", `..\x`, "%2e%2e/x", "a/\x00/b",
}

func uncleanSegment(s string) bool {
	return s == "" || s == "." || s == ".."
}

func FuzzValidatePath(f *testing.F) {
	for _, s := range fuzzPathSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, p string) {
		c := CanonicalPath(p)
		if CanonicalPath(c) != c {
			t.Fatalf("CanonicalPath not idempotent: %q -> %q -> %q", p, c, CanonicalPath(c))
		}
		if ValidatePath(p) != nil {
			return
		}
		if err := ValidatePath(c); err != nil {
			t.Fatalf("ValidatePath(%q) accepted, but its canonical form %q is rejected: %v", p, c, err)
		}
		if c == "" {
			return
		}
		if strings.HasPrefix(c, "/") || strings.HasSuffix(c, "/") {
			t.Fatalf("canonical path %q of %q has a leading or trailing slash", c, p)
		}
		if slices.ContainsFunc(strings.Split(c, "/"), uncleanSegment) {
			t.Fatalf("accepted path %q has an empty, . or .. segment in %q", p, c)
		}
		if pathpkg.Clean("/"+c) != "/"+c {
			t.Fatalf("accepted path %q would be rewritten by path.Clean to %q", p, pathpkg.Clean("/"+c))
		}
	})
}

func FuzzLoginPath(f *testing.F) {
	for _, s := range fuzzPathSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, mount string) {
		got, err := loginPath(mount, "kubernetes")
		if err != nil {
			return
		}
		m, ok := strings.CutPrefix(got, "auth/")
		if !ok {
			t.Fatalf("loginPath(%q) = %q, not under auth/", mount, got)
		}
		m, ok = strings.CutSuffix(m, "/login")
		if !ok || m == "" {
			t.Fatalf("loginPath(%q) = %q, not auth/<mount>/login", mount, got)
		}
		if m == "auth" || strings.HasPrefix(m, "auth/") {
			t.Fatalf("loginPath(%q) = %q, mount starts with auth/", mount, got)
		}
		if slices.ContainsFunc(strings.Split(got, "/"), uncleanSegment) || pathpkg.Clean("/"+got) != "/"+got {
			t.Fatalf("loginPath(%q) = %q, not a clean path", mount, got)
		}
	})
}

var fuzzNamespaceSeeds = []string{
	"", "team-a", "team-a/apps", "..", "../team-b", "team-a/../team-b", "./team-a", "/team-b", "team-a/",
	"team-a//apps", "root", "root/team-a", "/team/", "team//a", "../other", "a/root", "invalid..segment", ".../x",
}

func FuzzValidateNamespace(f *testing.F) {
	for _, s := range fuzzNamespaceSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, ns string) {
		if ValidateNamespace(ns) != nil || ns == "" {
			return
		}
		segments := strings.Split(ns, "/")
		if slices.ContainsFunc(segments, uncleanSegment) || segments[0] == "root" {
			t.Fatalf("ValidateNamespace accepted %q", ns)
		}
		if got := joinNamespace("", ns); got != ns {
			t.Fatalf("accepted namespace %q is not canonical: joinNamespace gives %q", ns, got)
		}
	})
}

func FuzzJoinNamespace(f *testing.F) {
	for _, base := range []string{"", "tenant", "tenant/team", "/tenant/", ".."} {
		for _, rel := range fuzzNamespaceSeeds {
			f.Add(base, rel)
		}
	}
	f.Fuzz(func(t *testing.T, base, rel string) {
		b := joinNamespace(base, "")
		got := joinNamespace(base, rel)
		if joinNamespace(got, "") != got {
			t.Fatalf("joinNamespace(%q, %q) = %q is not canonical", base, rel, got)
		}
		if b != "" && got != b && !strings.HasPrefix(got, b+"/") {
			t.Fatalf("joinNamespace(%q, %q) = %q leaves the base %q", base, rel, got, b)
		}
		if got != "" && slices.ContainsFunc(strings.Split(got, "/"), uncleanSegment) {
			t.Fatalf("joinNamespace(%q, %q) = %q has an empty, . or .. segment", base, rel, got)
		}
		if joinNamespace(b, rel) != got {
			t.Fatalf("joinNamespace(%q, %q) depends on the spelling of the base %q", base, rel, b)
		}
		if ValidateNamespace(rel) == nil && rel != "" && b != "" && got != b+"/"+rel {
			t.Fatalf("joinNamespace(%q, %q) = %q, want %q", base, rel, got, b+"/"+rel)
		}
	})
}
