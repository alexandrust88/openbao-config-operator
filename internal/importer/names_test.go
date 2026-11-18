package importer

import (
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"

	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
)

func TestObjectName(t *testing.T) {
	tests := []struct {
		parts []string
		want  string
	}{
		{[]string{"", litKubernetes, litApp}, "kubernetes-app"},
		{[]string{"team-a/sub", "Secret", "app/config"}, "team-a-sub-secret-app-config"},
		{[]string{"allow_all"}, "allow-all"},
		{[]string{"..", "__"}, "imported"},
		{[]string{"a.b", "-c-"}, "a.b-c"},
		{[]string{"team..read"}, "team.read"},
		{[]string{".team.read."}, "team.read"},
		{[]string{"-team-.-read-"}, "team.read"},
		{[]string{"a.-b-.c"}, "a.b.c"},
		{[]string{"team", ".read"}, "team.read"},
		{[]string{"Team.READ"}, "team.read"},
		{[]string{"équipe.lire"}, "quipe.lire"},
		{[]string{"日本.ü"}, "imported"},
	}
	for _, tt := range tests {
		if got := objectName(tt.parts...); got != tt.want {
			t.Errorf("objectName(%q) = %q, want %q", tt.parts, got, tt.want)
		}
	}
	long := objectName(strings.Repeat("a", 300))
	other := objectName(strings.Repeat("a", 299) + "b")
	if len(long) > maxNameLength || long == other {
		t.Errorf("long names %q and %q are not shortened uniquely", long, other)
	}
	for _, name := range []string{long, objectName("x/Y_z")} {
		assertValidName(t, name)
	}

	longLabel := strings.Repeat("a", 64) + ".b"
	if got := objectName(longLabel); got == objectName(strings.Repeat("a", 65)+".b") {
		t.Errorf("long labels are not shortened uniquely: %q", got)
	}
	for _, in := range []string{
		strings.Repeat("a", 254),
		strings.Repeat("a.", 127),
		strings.Repeat("ab.", 100),
		strings.Repeat("a", 62) + "-" + strings.Repeat("b", 300),
		longLabel,
		strings.Repeat(strings.Repeat("x", 63)+".", 5),
	} {
		assertValidName(t, objectName(in))
	}
}

func TestUniqueSuffixKeepsLabelsValid(t *testing.T) {
	for _, name := range []string{
		strings.Repeat("a", 63),
		"x." + strings.Repeat("a", 63),
		objectName(strings.Repeat("a.", 150)),
		objectName(strings.Repeat("a", 300)),
	} {
		assertValidName(t, withSuffix(name, "-12"))
	}
}

func TestPrepareCollision(t *testing.T) {
	s := &scope{importer: &importer{used: map[string]map[string]bool{}}}
	gvk := schema.GroupVersionKind{Group: "sys.obco.dev", Version: "v1alpha1", Kind: "ACLPolicy"}
	policies := []string{"team..read", "team.read", "Team.Read", strings.Repeat("a", 63), strings.Repeat("A", 63)}
	names := make([]string, 0, len(policies))
	for _, policy := range policies {
		obj := &sysv1alpha1.ACLPolicy{}
		s.prepare(obj, gvk, policy)
		names = append(names, obj.GetName())
		assertValidName(t, obj.GetName())
	}
	want := []string{"team.read", "team.read-2", "team.read-3", strings.Repeat("a", 63), strings.Repeat("a", 61) + "-2"}
	if !slices.Equal(names, want) {
		t.Errorf("names = %q, want %q", names, want)
	}
}

func FuzzObjectName(f *testing.F) {
	for _, seed := range []string{"team..read", ".-a-.", "Ünïcødé", strings.Repeat("a.", 140), strings.Repeat("b", 300)} {
		f.Add(seed, "")
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		assertValidName(t, objectName(a, b))
	})
}

func assertValidName(t *testing.T, name string) {
	t.Helper()
	if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
		t.Errorf("%q: %v", name, errs)
	}
	if len(name) > maxNameLength {
		t.Errorf("%q is longer than %d", name, maxNameLength)
	}
	for label := range strings.SplitSeq(name, ".") {
		if errs := validation.IsDNS1123Label(label); len(errs) > 0 {
			t.Errorf("%q label %q: %v", name, label, errs)
		}
	}
}

func TestWriteOnlyKey(t *testing.T) {
	tests := []struct {
		field     string
		writeOnly []string
		want      string
	}{
		{"tokenReviewerJWTSecretRef", []string{litTokenReviewerJWT}, litTokenReviewerJWT},
		{"passwordSecretRef", nil, litPassword},
		{"oidcClientSecretSecretRef", nil, "oidc_client_secret"},
		{"tlsCertificateKeySecretRef", nil, "tls_certificate_key"},
		{"pemJSONSecretRef", nil, "pem_json"},
	}
	for _, tt := range tests {
		if got := writeOnlyKey(tt.field, tt.writeOnly); got != tt.want {
			t.Errorf("writeOnlyKey(%q) = %q, want %q", tt.field, got, tt.want)
		}
	}
}
