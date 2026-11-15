//go:build docs

// Package docs checks every manifest the documentation shows: the files under examples/ (except the live kit, which
// test/livekit covers) and examples-generated/, every YAML file or section a page includes with a --8<-- snippet
// line, and every fenced yaml block of docs/ with a top-level apiVersion and kind. Each manifest is created with a
// server-side dry run and strict field validation against envtest with every CRD installed.
package docs

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/alexandrust88/openbao-config-operator/test/testenv"
)

var root = filepath.Join("..", "..")

// thirdPartyGroups are API groups of other projects that pages show (admission policies, monitoring, GitOps). Their
// CRDs are not installed, so these manifests are only checked to be valid YAML with apiVersion and kind; any other
// group the API server does not serve fails.
var thirdPartyGroups = map[string]bool{
	"policies.kyverno.io":   true,
	"monitoring.coreos.com": true,
	"argoproj.io":           true,
}

func TestManifestsDryRun(t *testing.T) {
	c := collect(t, root)
	total := 0
	for _, s := range c.all() {
		total += len(s.manifests)
	}
	t.Logf("collected %d manifests: %d example files, %d generated files, %d included snippets, %d page fences (%d pages)",
		total, len(c.examples), len(c.generated), len(c.includes), len(c.fences), c.pages)
	if total == 0 || len(c.examples) == 0 || len(c.generated) == 0 || c.pages == 0 {
		t.Fatal("collected no manifests from examples/, examples-generated/ or docs/; the collection rules are broken")
	}

	assets := testenv.RequireAssets(t, root)
	env := &envtest.Environment{
		BinaryAssetsDirectory: assets,
		CRDDirectoryPaths:     []string{filepath.Join(root, "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := env.Stop(); err != nil {
			t.Error(err)
		}
	})
	k8s, err := client.New(cfg, client.Options{})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	namespaces := map[string]bool{}
	ensureNamespace := func(t *testing.T, name string) {
		if namespaces[name] {
			return
		}
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
		if err := k8s.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatalf("create namespace %s: %v", name, err)
		}
		namespaces[name] = true
	}

	for i, s := range c.all() {
		if len(s.manifests) == 0 && len(s.errs) == 0 {
			t.Logf("%s holds no manifest", s.name)
			continue
		}
		t.Run(s.name, func(t *testing.T) {
			for _, e := range s.errs {
				t.Error(e)
			}
			testNS := fmt.Sprintf("docs-%03d", i)
			for _, m := range s.manifests {
				dryRun(ctx, t, k8s, m, testNS, ensureNamespace)
			}
		})
	}
}

func dryRun(ctx context.Context, t *testing.T, k8s client.Client, m manifest, testNS string,
	ensureNamespace func(*testing.T, string)) {
	t.Helper()
	obj := &unstructured.Unstructured{Object: m.obj}
	what := fmt.Sprintf("%s: %s %s", m.at(), obj.GetKind(), obj.GetName())
	if thirdPartyGroups[obj.GroupVersionKind().Group] {
		t.Logf("%s: third-party kind, checked as YAML only", what)
		return
	}
	namespaced, err := k8s.IsObjectNamespaced(obj)
	if err != nil {
		if meta.IsNoMatchError(err) {
			t.Errorf("%s: the API server does not serve %s", what, obj.GetAPIVersion())
		} else {
			t.Errorf("%s: %v", what, err)
		}
		return
	}
	switch {
	case namespaced && obj.GetNamespace() == "":
		obj.SetNamespace(testNS)
		ensureNamespace(t, testNS)
	case namespaced:
		ensureNamespace(t, obj.GetNamespace())
	case obj.GetNamespace() != "":
		t.Errorf("%s: cluster-scoped kind sets metadata.namespace", what)
		return
	}
	err = k8s.Create(ctx, obj, client.DryRunAll, client.FieldValidation("Strict"))
	if apierrors.IsAlreadyExists(err) && obj.GetKind() == "Namespace" && obj.GetAPIVersion() == "v1" {
		return
	}
	if err != nil {
		t.Errorf("%s: %v", what, err)
	}
}

// TestSnippetReferences checks that every --8<-- include of a page, and of a file a page includes, names an existing
// file, and an existing section or line range of it.
func TestSnippetReferences(t *testing.T) {
	c := collect(t, root)
	queue := append([]snippetRef{}, c.snippets...)
	visited := map[string]bool{}
	checked := 0
	for len(queue) > 0 {
		ref := queue[0]
		queue = queue[1:]
		at := fmt.Sprintf("%s:%d: --8<-- %q", ref.from, ref.line, ref.target)
		if strings.Contains(ref.target, "://") {
			t.Errorf("%s: remote snippets are not allowed; include a file from the repository", at)
			continue
		}
		clean := path.Clean(ref.target)
		if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
			t.Errorf("%s: the path must be relative to the repository root", at)
			continue
		}
		checked++
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(clean)))
		if err != nil {
			t.Errorf("%s: %v", at, err)
			continue
		}
		text := string(data)
		switch {
		case ref.section != "":
			if _, _, err := section(text, ref.section); err != nil {
				t.Errorf("%s: section %q: %v", at, ref.section, err)
			}
		case ref.lines != "":
			if err := checkLines(ref.lines, strings.Count(text, "\n")+1); err != nil {
				t.Errorf("%s: %v", at, err)
			}
		}
		if !visited[clean] {
			visited[clean] = true
			queue = append(queue, snippetRefs(clean, text)...)
		}
	}
	t.Logf("checked %d snippet references from %d pages", checked, c.pages)
}

// TestVersionTokens checks where the @VERSION@ and @TAG@ tokens appear. The docs hook replaces them only in rendered
// pages, so a token in an example file reaches users and the release bundle unreplaced. In hack/docs/quickstart.sh,
// which CI runs as is, a token outside a docs section is reported as a warning.
func TestVersionTokens(t *testing.T) {
	token := regexp.MustCompile(`@(VERSION|TAG)@`)
	files := append(walk(t, root, "examples", ".yaml", ".yml", ".sh", ".hcl", ".json"),
		walk(t, root, "examples-generated", ".yaml", ".yml")...)
	for _, f := range files {
		for i, l := range strings.Split(readFile(t, root, f), "\n") {
			if token.MatchString(l) {
				t.Errorf("%s:%d: version token in an example file; write a concrete value", f, i+1)
			}
		}
	}

	const quickstart = "hack/docs/quickstart.sh"
	data, err := os.ReadFile(filepath.Join(root, quickstart))
	if os.IsNotExist(err) {
		t.Logf("%s does not exist", quickstart)
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	depth := 0
	for i, l := range strings.Split(string(data), "\n") {
		if m := sectionMarker.FindStringSubmatch(l); m != nil {
			if strings.EqualFold(m[1], "start") {
				depth++
			} else if depth > 0 {
				depth--
			}
			continue
		}
		if depth == 0 && token.MatchString(l) {
			t.Logf("warning: %s:%d: version token outside a docs section; the script runs it unreplaced", quickstart, i+1)
		}
	}
}

// TestNoDuplicatedManifests enforces the single-source rule: a page includes an example file with --8<-- instead of
// repeating it.
func TestNoDuplicatedManifests(t *testing.T) {
	c := collect(t, root)
	files := map[string]string{}
	for _, s := range append(append([]source{}, c.examples...), c.generated...) {
		if text := strings.TrimSpace(readFile(t, root, s.name)); text != "" {
			files[text] = s.name
		}
	}
	for _, f := range c.yamlFences {
		if name, ok := files[strings.TrimSpace(f.text)]; ok {
			t.Errorf("%s:%d: repeats %s; include it with --8<-- \"%s\"", f.file, f.line, name, name)
		}
	}
}
