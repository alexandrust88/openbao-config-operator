//go:build integration

package livekit

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// exceptions are the merged files of test/livekit/exceptions.
type exceptions struct {
	// FullFields lists, per kind, spec fields that no full example sets, with the reason.
	FullFields map[string]map[string]string `json:"fullFields"`
	// EnumValues lists, per kind and spec field (dotted like fullFields), enum values that no example uses, with the
	// reason.
	EnumValues map[string]map[string]map[string]string `json:"enumValues"`
	// OneOfBranches lists, per kind and exactly-one-of rule (the dotted path of the object the rule validates:
	// "spec", "spec.auth", "spec.members[]"), branches that no example sets, with the reason.
	OneOfBranches map[string]map[string]map[string]string `json:"oneOfBranches"`
	// NotReady lists objects that do not reach Ready in the end-to-end run, with the reason.
	NotReady []notReadyException `json:"notReady"`
}

// envDev scopes a notReady entry to bao server -dev: the kind e2e (OpenBao in Kubernetes on raft) requires Ready.
const envDev = "dev"

type notReadyException struct {
	Kind string `json:"kind"`
	// Name is a glob on metadata.name without the overlay prefix; empty matches every name.
	Name string `json:"name"`
	// Overlays are globs on the overlay (<mode>/<variant>); empty matches every overlay.
	Overlays []string `json:"overlays"`
	// Env is empty (excused everywhere) or envDev.
	Env    string `json:"env"`
	Reason string `json:"reason"`
}

func (e notReadyException) matches(overlay, kind, name string) bool {
	if e.Kind != kind {
		return false
	}
	if e.Name != "" {
		if ok, _ := path.Match(e.Name, name); !ok {
			return false
		}
	}
	if len(e.Overlays) == 0 {
		return true
	}
	for _, o := range e.Overlays {
		if ok, _ := path.Match(o, overlay); ok {
			return true
		}
	}
	return false
}

// loadExceptions merges every file in test/livekit/exceptions; a kind's fullFields may come from several files.
func loadExceptions(t testing.TB) exceptions {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("exceptions", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	all := exceptions{
		FullFields:    map[string]map[string]string{},
		EnumValues:    map[string]map[string]map[string]string{},
		OneOfBranches: map[string]map[string]map[string]string{},
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var e exceptions
		if err := yaml.UnmarshalStrict(raw, &e); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for kind, fields := range e.FullFields {
			if all.FullFields[kind] == nil {
				all.FullFields[kind] = map[string]string{}
			}
			for field, reason := range fields {
				if reason == "" {
					t.Fatalf("%s: fullFields %s.%s has no reason", f, kind, field)
				}
				all.FullFields[kind][field] = reason
			}
		}
		mergeValueReasons(t, f, "enumValues", all.EnumValues, e.EnumValues)
		mergeValueReasons(t, f, "oneOfBranches", all.OneOfBranches, e.OneOfBranches)
		for _, n := range e.NotReady {
			if n.Kind == "" || n.Reason == "" {
				t.Fatalf("%s: notReady entry without kind or reason: %+v", f, n)
			}
			if n.Env != "" && n.Env != envDev {
				t.Fatalf("%s: notReady %s env %q is neither empty nor %q", f, n.Kind, n.Env, envDev)
			}
		}
		all.NotReady = append(all.NotReady, e.NotReady...)
	}
	return all
}

// TestNotReadyEnv fails when a notReady entry limited to bao server -dev (env: dev) matches no object that the kind
// e2e applies (test/livekit/kind) and no entry without env excuses there, since nothing would then prove the object
// Ready where OpenBao runs in Kubernetes.
func TestNotReadyEnv(t *testing.T) {
	root := repoRoot(t)
	kit := kitDir(root)
	exc := loadExceptions(t)
	overlayOf := overlayDirs(t, kit)
	objs := render(t, kustomizeBin(t, root), filepath.Join(root, "test", "livekit", "kind"))
	for _, e := range exc.NotReady {
		if e.Env != envDev {
			continue
		}
		applied := slices.ContainsFunc(objs, func(o *unstructured.Unstructured) bool {
			label := o.GetLabels()[labelOverlay]
			overlay, name := overlayOf[label], strings.TrimPrefix(o.GetName(), label+"-")
			if !e.matches(overlay, o.GetKind(), name) {
				return false
			}
			return !slices.ContainsFunc(exc.NotReady, func(other notReadyException) bool {
				return other.Env != envDev && other.matches(overlay, o.GetKind(), name)
			})
		})
		if !applied {
			t.Errorf("notReady %s %q has env: dev but matches no object that test/livekit/kind applies (none, or each "+
				"excused by an entry without env); add its overlay there or drop env", e.Kind, e.Name)
		}
	}
}

// overlayDirs maps each overlay label (live.obco.dev/overlay) to its overlay, <mode>/<variant>.
func overlayDirs(t testing.TB, kit string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, o := range overlays(t, kit) {
		for _, l := range readKustomization(t, filepath.Join(kit, o)).Labels {
			if label := l.Pairs[labelOverlay]; label != "" {
				out[label] = strings.TrimPrefix(o, "overlays/")
			}
		}
	}
	return out
}

func mergeValueReasons(t testing.TB, file, key string, dst, src map[string]map[string]map[string]string) {
	t.Helper()
	for kind, paths := range src {
		if dst[kind] == nil {
			dst[kind] = map[string]map[string]string{}
		}
		for p, values := range paths {
			if dst[kind][p] == nil {
				dst[kind][p] = map[string]string{}
			}
			for v, reason := range values {
				if reason == "" {
					t.Fatalf("%s: %s %s %s %s has no reason", file, key, kind, p, v)
				}
				dst[kind][p][v] = reason
			}
		}
	}
}

// TestRender builds every overlay and checks the naming contract: references resolve inside the build or name
// a declared input, OpenBao names carry the overlay prefix, and outputs are written to prefixed Secrets.
func TestRender(t *testing.T) {
	root := repoRoot(t)
	kit := kitDir(root)
	kustomize := kustomizeBin(t, root)
	p := params(t, kit)
	comps := components(t, kit)
	compDirs := make([]string, 0, len(comps))
	for _, c := range comps {
		compDirs = append(compDirs, c.dir)
	}
	inputs := map[string]bool{}
	for _, in := range inputSecrets(t, compDirs...) {
		inputs[in.secret] = true
	}
	selfNamed := selfNamedFields(t, kit)
	for _, overlay := range overlays(t, kit) {
		t.Run(overlay, func(t *testing.T) {
			objs := render(t, kustomize, filepath.Join(kit, overlay))
			names := map[string]bool{}
			for _, o := range objs {
				names[o.GetName()] = true
			}
			for _, o := range objs {
				if !isOperatorKind(o) {
					continue
				}
				prefix := o.GetLabels()[labelOverlay] + "-"
				if prefix == "-" {
					t.Errorf("%s/%s: no %s label", o.GetKind(), o.GetName(), labelOverlay)
					continue
				}
				id := o.GetKind() + "/" + o.GetName()
				if !strings.HasPrefix(o.GetName(), prefix) {
					t.Errorf("%s: name lacks the overlay prefix %q", id, prefix)
				}
				for _, r := range refs(o.Object[keySpec], "") {
					checkRef(t, id, r, prefix, names, inputs, p)
				}
				if coreKinds[o.GetKind()] {
					continue
				}
				for _, field := range selfNamed[o.GetKind()] {
					if v, ok, _ := unstructured.NestedString(o.Object, keySpec, field); ok && v != o.GetName() {
						t.Errorf("%s: spec.%s %q must equal metadata.name, so the overlay prefix reaches OpenBao", id, field, v)
					}
				}
				for _, mp := range mountPaths(o.Object[keySpec]) {
					if !strings.HasPrefix(mp, prefix) {
						t.Errorf("%s: mountPath %q names no mount of this overlay (want prefix %q)", id, mp, prefix)
					}
				}
			}
		})
	}
}

type ref struct{ field, name string }

// refs returns every name under a field ending in Ref or Refs, with the field name.
func refs(v any, field string) []ref {
	var out []ref
	switch x := v.(type) {
	case map[string]any:
		if name, ok := x[keyName].(string); ok && (strings.HasSuffix(field, "Ref") || strings.HasSuffix(field, "Refs")) {
			out = append(out, ref{field: field, name: name})
		}
		for k, child := range x {
			out = append(out, refs(child, k)...)
		}
	case []any:
		for _, child := range x {
			out = append(out, refs(child, field)...)
		}
	}
	return out
}

// mountPaths returns every string value of a mountPath field below v.
func mountPaths(v any) []string {
	var out []string
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			if s, ok := child.(string); ok && k == "mountPath" {
				out = append(out, s)
			} else {
				out = append(out, mountPaths(child)...)
			}
		}
	case []any:
		for _, child := range x {
			out = append(out, mountPaths(child)...)
		}
	}
	return out
}

func isInputSecretRef(field string) bool {
	return field != "writeSecretRef" && (strings.Contains(field, "Secret") || strings.Contains(field, "secret"))
}

func checkRef(t *testing.T, id string, r ref, prefix string, names, inputs map[string]bool, p map[string]string) {
	t.Helper()
	switch {
	case r.field == "serviceAccountRef":
		if !slices.Contains([]string{p["rootServiceAccount"], p["tenantServiceAccount"], p["childServiceAccount"]}, r.name) {
			t.Errorf("%s: serviceAccountRef %q is not a ServiceAccount from the live-params literals", id, r.name)
		}
	case r.field == "configMapRef":
		if r.name != p["caConfigMap"] && !names[r.name] {
			t.Errorf("%s: configMapRef %q is neither caConfigMap nor in the build", id, r.name)
		}
	case r.field == "writeSecretRef":
		if !strings.HasPrefix(r.name, prefix) {
			t.Errorf("%s: writeSecretRef %q lacks the overlay prefix %q; name it after its resource", id, r.name, prefix)
		}
	case isInputSecretRef(r.field):
		if !inputs[r.name] {
			t.Errorf("%s: %s %q is not declared in any input-secrets.txt", id, r.field, r.name)
		}
	default:
		if !names[r.name] {
			t.Errorf("%s: %s %q is not in the build (a reference kustomize did not rename? see base/nameref.yaml)",
				id, r.field, r.name)
		}
	}
}

// selfNamedFields reads base/nameref.yaml and returns, per kind, the spec fields that name the object itself in
// OpenBao (a fieldSpec of the kind under its own referent entry).
func selfNamedFields(t testing.TB, kit string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, nr := range nameRefConfig(t, kit) {
		for _, fs := range nr.FieldSpecs {
			if fs.Kind == nr.Kind && strings.Count(fs.Path, "/") == 1 {
				out[nr.Kind] = append(out[nr.Kind], strings.TrimPrefix(fs.Path, "spec/"))
			}
		}
	}
	return out
}

type nameRef struct {
	Kind       string `json:"kind"`
	FieldSpecs []struct {
		Kind string `json:"kind"`
		Path string `json:"path"`
	} `json:"fieldSpecs"`
}

func nameRefConfig(t testing.TB, kit string) []nameRef {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(kit, "base", "nameref.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		NameReference []nameRef `json:"nameReference"`
	}
	if err := yaml.UnmarshalStrict(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg.NameReference
}

// TestNameRefs fails when a reference field of a built-in CRD is missing from base/nameref.yaml, so a namePrefix
// would leave it pointing at the unprefixed name. Input Secrets and ServiceAccounts live outside the build.
func TestNameRefs(t *testing.T) {
	root := repoRoot(t)
	kit := kitDir(root)
	covered := map[string]bool{}
	for _, nr := range nameRefConfig(t, kit) {
		for _, fs := range nr.FieldSpecs {
			covered[fs.Kind+" "+fs.Path] = true
		}
	}
	for _, k := range sortedKinds(crdKinds(t, root)) {
		if !k.Builtin {
			continue
		}
		for _, p := range refPaths(k.Spec, "spec") {
			field := p[strings.LastIndex(strings.TrimSuffix(p, "/name"), "/")+1 : len(p)-len("/name")]
			if field == "serviceAccountRef" || isInputSecretRef(field) {
				continue
			}
			if !covered[k.Kind+" "+p] && !covered[" "+p] {
				t.Errorf("%s: %s is not in examples/live/base/nameref.yaml", k.Kind, p)
			}
		}
	}
}

// refPaths returns the kustomize field paths (slash separated, lists traversed implicitly) of every name under a
// property ending in Ref or Refs.
func refPaths(schema map[string]any, prefix string) []string {
	var out []string
	props, _ := schema["properties"].(map[string]any)
	for k, raw := range props {
		v, _ := raw.(map[string]any)
		if v["type"] == "array" {
			v, _ = v["items"].(map[string]any)
		}
		p := prefix + "/" + k
		sub, _ := v["properties"].(map[string]any)
		if _, ok := sub[keyName]; ok && (strings.HasSuffix(k, "Ref") || strings.HasSuffix(k, "Refs")) {
			out = append(out, p+"/name")
		}
		out = append(out, refPaths(v, p)...)
	}
	return out
}

func sortedKinds(m map[string]*kindInfo) []*kindInfo {
	out := make([]*kindInfo, 0, len(m))
	for _, k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}

// TestCoverage fails when a built-in kind has no minimal or no full example in the kit, and keeps the coverage
// table in examples/live/coverage.md current (LIVEKIT_UPDATE=1 rewrites it).
func TestCoverage(t *testing.T) {
	root := repoRoot(t)
	kit := kitDir(root)
	kinds := crdKinds(t, root)
	cover := map[string]map[string][]string{}
	for _, c := range components(t, kit) {
		for _, d := range c.docs {
			if !isOperatorKind(d) {
				continue
			}
			if cover[d.GetKind()] == nil {
				cover[d.GetKind()] = map[string][]string{}
			}
			if !slices.Contains(cover[d.GetKind()][c.variant], c.scenario) {
				cover[d.GetKind()][c.variant] = append(cover[d.GetKind()][c.variant], c.scenario)
			}
		}
	}
	exc := loadExceptions(t)
	fullDocs, allDocs := kitDocs(t, kit, variantFull), kitDocs(t, kit, "")
	knobs := knobValues(allDocs)
	var b strings.Builder
	b.WriteString("# Live kit coverage\n\nGenerated by `make test-livekit` (`LIVEKIT_UPDATE=1` rewrites it). " +
		"Every built-in kind, the components that hold its minimal and full examples, the spec fields its full " +
		"examples set, the enum values its examples use, and its entries in test/livekit/exceptions.\n\n" +
		"| kind | group | minimal | full | fields | enum values | exceptions |\n" +
		"|------|-------|---------|------|--------|-------------|------------|\n")
	var n, fieldsUsed, fieldsTotal, enumUsed, enumTotal, excTotal int
	for _, k := range sortedKinds(kinds) {
		if !k.Builtin {
			continue
		}
		n++
		mins, fulls := cover[k.Kind][variantMin], cover[k.Kind][variantFull]
		if len(mins) == 0 {
			t.Errorf("%s: no minimal example in examples/live/components/*/minimal", k.Kind)
		}
		if len(fulls) == 0 {
			t.Errorf("%s: no full example in examples/live/components/*/full", k.Kind)
		}
		set := fieldsSet(k, fullDocs[k.Kind])
		fu := 0
		for _, ok := range set {
			if ok {
				fu++
			}
		}
		uses := enumUses(k, allDocs[k.Kind], knobs)
		eu := 0
		for _, u := range uses {
			if u.used {
				eu++
			}
		}
		ex := exceptionCount(exc, k.Kind)
		fieldsUsed += fu
		fieldsTotal += len(set)
		enumUsed += eu
		enumTotal += len(uses)
		excTotal += ex
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %d/%d | %d/%d | %d |\n", k.Kind, strings.SplitN(k.Group, ".", 2)[0],
			strings.Join(mins, ", "), strings.Join(fulls, ", "), fu, len(set), eu, len(uses), ex)
	}
	fmt.Fprintf(&b, "\n%d built-in kinds: %d/%d spec fields set by a full example, %d/%d enum values used by an "+
		"example, %d exceptions.\n", n, fieldsUsed, fieldsTotal, enumUsed, enumTotal, excTotal)
	out := filepath.Join(kit, "coverage.md")
	if os.Getenv("LIVEKIT_UPDATE") != "" {
		if err := os.WriteFile(out, []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(out)
	if err != nil || string(got) != b.String() {
		t.Errorf("examples/live/coverage.md is stale; run LIVEKIT_UPDATE=1 make test-livekit")
	}
}

// exceptionCount counts the fullFields, enumValues, oneOfBranches and notReady entries of a kind.
func exceptionCount(exc exceptions, kind string) int {
	n := len(exc.FullFields[kind])
	for _, values := range exc.EnumValues[kind] {
		n += len(values)
	}
	for _, branches := range exc.OneOfBranches[kind] {
		n += len(branches)
	}
	for _, e := range exc.NotReady {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

// TestFullFields fails when a spec field of a built-in kind is set by none of the kind's full examples, unless
// an exceptions file names it with a reason. One-of alternatives are covered by further documents of the kind.
func TestFullFields(t *testing.T) {
	root := repoRoot(t)
	kit := kitDir(root)
	exc := loadExceptions(t)
	docs := kitDocs(t, kit, variantFull)
	for _, k := range sortedKinds(crdKinds(t, root)) {
		if !k.Builtin {
			continue
		}
		set := fieldsSet(k, docs[k.Kind])
		var missing []string
		for p, ok := range set {
			_, excused := exc.FullFields[k.Kind][p]
			switch {
			case ok && excused:
				t.Errorf("%s: an exceptions file excuses spec.%s, but a full example sets it", k.Kind, p)
			case !ok && !excused:
				missing = append(missing, p)
			}
		}
		for p := range exc.FullFields[k.Kind] {
			if _, known := set[p]; !known && p != "openbaoNamespace" {
				t.Errorf("%s: an exceptions file names spec.%s, which the CRD does not have", k.Kind, p)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			t.Errorf("%s: %d spec fields set by no full example: %s", k.Kind, len(missing), strings.Join(missing, ", "))
		}
	}
	for kind := range exc.FullFields {
		if docs[kind] == nil {
			t.Errorf("an exceptions file names %s, which has no full example", kind)
		}
	}
}

// kitDocs returns the documents of the kit's components by kind, of one variant or, for "", of both.
func kitDocs(t testing.TB, kit, variant string) map[string][]*unstructured.Unstructured {
	t.Helper()
	docs := map[string][]*unstructured.Unstructured{}
	for _, c := range components(t, kit) {
		if variant != "" && c.variant != variant {
			continue
		}
		for _, d := range c.docs {
			docs[d.GetKind()] = append(docs[d.GetKind()], d)
		}
	}
	return docs
}

// fieldsSet returns, for every spec leaf of a kind except namespace, whether one of docs sets it.
func fieldsSet(k *kindInfo, docs []*unstructured.Unstructured) map[string]bool {
	out := map[string]bool{}
	for _, p := range leafPaths(k.Spec, "") {
		if p == "openbaoNamespace" {
			continue
		}
		out[p] = slices.ContainsFunc(docs, func(d *unstructured.Unstructured) bool {
			return hasPath(d.Object[keySpec], strings.Split(p, "."))
		})
	}
	return out
}

// policyKnobs are the operator's lifecycle fields and the connection kind, which the engine handles alike for every
// kind; the lifecycle and connection-auth components and the conformance suite exercise them. A value of one counts
// as used by every kind once any document of the kit uses it.
var policyKnobs = []string{"creationPolicy", "deletionPolicy", "managementPolicy", "driftDetection", "connectionRef.kind"}

// TestEnumValues fails when a value of an enum spec field of a built-in kind is used by no example of the kind,
// minimal or full, unless an exceptions file names it under enumValues or excuses the whole field under fullFields.
// The policyKnobs follow the rule above.
func TestEnumValues(t *testing.T) {
	root := repoRoot(t)
	kit := kitDir(root)
	exc := loadExceptions(t)
	kinds := crdKinds(t, root)
	docs := kitDocs(t, kit, "")
	knobs := knobValues(docs)
	var gaps []string
	for _, k := range sortedKinds(kinds) {
		if !k.Builtin {
			continue
		}
		known := map[string]bool{}
		var missing []string
		for _, u := range enumUses(k, docs[k.Kind], knobs) {
			known[u.path+"="+u.value] = true
			_, excused := exc.EnumValues[k.Kind][u.path][u.value]
			if _, whole := exc.FullFields[k.Kind][u.path]; whole {
				if excused {
					t.Errorf("%s: enumValues excuses spec.%s=%s, but fullFields already excuses the field", k.Kind, u.path, u.value)
				}
				continue
			}
			switch {
			case u.used && excused:
				t.Errorf("%s: an exceptions file excuses spec.%s=%s, but an example uses it", k.Kind, u.path, u.value)
			case !u.used && !excused:
				missing = append(missing, "spec."+u.path+"="+u.value)
			}
		}
		for p, values := range exc.EnumValues[k.Kind] {
			for v := range values {
				if !known[p+"="+v] {
					t.Errorf("%s: an exceptions file names spec.%s=%s, which is no enum value of the CRD", k.Kind, p, v)
				}
			}
		}
		if len(missing) > 0 {
			gaps = append(gaps, fmt.Sprintf("%s: %s", k.Kind, strings.Join(missing, ", ")))
		}
	}
	checkExceptionKinds(t, "enumValues", exc.EnumValues, kinds)
	reportGaps(t, "enum values used by no example", gaps)
}

// TestOneOfBranches fails when a branch of an exactly-one-of rule of a built-in kind is set by no example of the
// kind, minimal or full, unless an exceptions file names it under oneOfBranches.
func TestOneOfBranches(t *testing.T) {
	root := repoRoot(t)
	kit := kitDir(root)
	exc := loadExceptions(t)
	kinds := crdKinds(t, root)
	docs := kitDocs(t, kit, "")
	var gaps []string
	for _, k := range sortedKinds(kinds) {
		if !k.Builtin {
			continue
		}
		known := map[string]bool{}
		var missing []string
		for _, rule := range oneOfRules(t, k) {
			for _, b := range rule.branches {
				known[rule.path+"="+b] = true
				set := branchSet(docs[k.Kind], rule.path, b)
				_, excused := exc.OneOfBranches[k.Kind][rule.path][b]
				switch {
				case set && excused:
					t.Errorf("%s: an exceptions file excuses branch %s of %s, but an example sets it", k.Kind, b, rule.path)
				case !set && !excused:
					missing = append(missing, rule.path+"."+b)
				}
			}
		}
		for p, branches := range exc.OneOfBranches[k.Kind] {
			for b := range branches {
				if !known[p+"="+b] {
					t.Errorf("%s: an exceptions file names branch %s of %s, which is no exactly-one-of rule of the CRD", k.Kind, b, p)
				}
			}
		}
		if len(missing) > 0 {
			gaps = append(gaps, fmt.Sprintf("%s: %s", k.Kind, strings.Join(missing, ", ")))
		}
	}
	checkExceptionKinds(t, "oneOfBranches", exc.OneOfBranches, kinds)
	reportGaps(t, "exactly-one-of branches set by no example", gaps)
}

func checkExceptionKinds(t *testing.T, key string, excused map[string]map[string]map[string]string, kinds map[string]*kindInfo) {
	t.Helper()
	for kind := range excused {
		if k := kinds[kind]; k == nil || !k.Builtin {
			t.Errorf("an exceptions file names %s under %s, which is no built-in kind", kind, key)
		}
	}
}

func reportGaps(t *testing.T, what string, gaps []string) {
	t.Helper()
	if len(gaps) > 0 {
		t.Errorf("%d kinds have %s; add an example, or an exception with the reason to the owning component's "+
			"exceptions file:\n%s", len(gaps), what, strings.Join(gaps, "\n"))
	}
}

// walkSchema calls visit for a schema and every property, list item and map value schema below it, with the dotted
// path from the root: "[]" marks list items, "{}" map values.
func walkSchema(schema map[string]any, prefix string, visit func(p string, schema map[string]any)) {
	visit(prefix, schema)
	props, _ := schema["properties"].(map[string]any)
	for k, raw := range props {
		child, _ := raw.(map[string]any)
		p := k
		if prefix != "" {
			p = prefix + "." + k
		}
		walkSchema(child, p, visit)
	}
	if items, ok := schema["items"].(map[string]any); ok {
		walkSchema(items, prefix+"[]", visit)
	}
	if values, ok := schema["additionalProperties"].(map[string]any); ok {
		walkSchema(values, prefix+"{}", visit)
	}
}

// valuesAt returns every value at a path below v, split at dots: "[]" after a segment takes every item of the list,
// "{}" every value of the map.
func valuesAt(v any, segs []string) []any {
	if len(segs) == 0 {
		return []any{v}
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	var children []any
	if list, ok := strings.CutSuffix(segs[0], "[]"); ok {
		children, _ = m[list].([]any)
	} else if field, ok := strings.CutSuffix(segs[0], "{}"); ok {
		entries, _ := m[field].(map[string]any)
		for _, e := range entries {
			children = append(children, e)
		}
	} else if child, ok := m[segs[0]]; ok {
		children = []any{child}
	}
	var out []any
	for _, c := range children {
		out = append(out, valuesAt(c, segs[1:])...)
	}
	return out
}

// usedValues returns the values that docs give a dotted spec field, counting the items of a list one by one.
func usedValues(docs []*unstructured.Unstructured, field string) map[string]bool {
	out := map[string]bool{}
	for _, d := range docs {
		for _, v := range valuesAt(d.Object[keySpec], strings.Split(field, ".")) {
			if items, ok := v.([]any); ok {
				for _, item := range items {
					out[fmt.Sprint(item)] = true
				}
				continue
			}
			out[fmt.Sprint(v)] = true
		}
	}
	return out
}

func knobValues(docs map[string][]*unstructured.Unstructured) map[string]map[string]bool {
	var all []*unstructured.Unstructured
	for _, kindDocs := range docs {
		all = append(all, kindDocs...)
	}
	out := map[string]map[string]bool{}
	for _, knob := range policyKnobs {
		out[knob] = usedValues(all, knob)
	}
	return out
}

// enumUse is one value of an enum spec field, dotted like leafPaths, and whether an example uses it.
type enumUse struct {
	path, value string
	used        bool
}

func enumUses(k *kindInfo, docs []*unstructured.Unstructured, knobs map[string]map[string]bool) []enumUse {
	var out []enumUse
	walkSchema(k.Spec, "", func(p string, s map[string]any) {
		values, ok := s["enum"].([]any)
		if !ok {
			return
		}
		// leafPaths names a list of scalars without "[]", and so do the exceptions.
		p = strings.TrimSuffix(p, "[]")
		used := usedValues(docs, p)
		for _, v := range values {
			value := fmt.Sprint(v)
			out = append(out, enumUse{path: p, value: value, used: used[value] || knobs[p][value]})
		}
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].path != out[j].path {
			return out[i].path < out[j].path
		}
		return out[i].value < out[j].value
	})
	return out
}

// oneOfRule is an exactly-one-of rule: the object it validates ("spec", "spec.auth") and the fields it chooses from.
type oneOfRule struct {
	path     string
	branches []string
}

var (
	// The message controller-gen writes for the ExactlyOneOf marker.
	markerExactlyOne = regexp.MustCompile(`^exactly one of the fields in \[(.+)\] must be set$`)
	// The hand-written message, "set exactly one of a, b and c"; an item may describe its field ("a kubernetes
	// providerConfig"), whose name is the last word.
	setExactlyOne = regexp.MustCompile(`^set exactly one of (.+)$`)
)

var (
	// A count of has() tests compared with 1: [has(self.a), ...].filter(x, x).size() == 1 or
	// (has(self.a) ? 1 : 0) + ... == 1.
	celCountIsOne = regexp.MustCompile(`has\(.*(\.size\(\)|\?\s*1\s*:\s*0\s*\))\s*==\s*1\b`)
	// Exactly one of two fields: has(self.a) != has(self.b).
	celOneOfTwo      = regexp.MustCompile(`^\s*has\([^()]+\)\s*!=\s*has\([^()]+\)\s*$`)
	msgExactlyOneish = regexp.MustCompile(`(?i)^(set )?exactly one\b`)
)

// exactlyOneShape reports whether a CEL rule looks like an exactly-one-of rule, by its expression or its message.
func exactlyOneShape(rule map[string]any) bool {
	text, _ := rule["rule"].(string)
	msg, _ := rule["message"].(string)
	return celCountIsOne.MatchString(text) || celOneOfTwo.MatchString(text) || msgExactlyOneish.MatchString(msg)
}

// oneOfRules reads the exactly-one-of rules of a kind's spec from the messages of its CEL rules; a branch that is
// no field of the validated object fails t, so a reworded message cannot hide a rule.
func oneOfRules(t *testing.T, k *kindInfo) []oneOfRule {
	t.Helper()
	var out []oneOfRule
	walkSchema(k.Spec, "", func(p string, s map[string]any) {
		object := keySpec
		if p != "" {
			object += "." + p
		}
		rules, _ := s["x-kubernetes-validations"].([]any)
		props, _ := s["properties"].(map[string]any)
		for _, raw := range rules {
			rule, _ := raw.(map[string]any)
			msg, _ := rule["message"].(string)
			var branches []string
			if m := markerExactlyOne.FindStringSubmatch(msg); m != nil {
				branches = strings.Fields(m[1])
			} else if m := setExactlyOne.FindStringSubmatch(msg); m != nil {
				for item := range strings.SplitSeq(strings.ReplaceAll(m[1], " and ", ", "), ", ") {
					words := strings.Fields(item)
					branches = append(branches, words[len(words)-1])
				}
			} else {
				if exactlyOneShape(rule) {
					t.Errorf("%s: the CEL rule on %s has the exactly-one-of shape, but its message matches neither "+
						"recognised pattern, so its branches are not checked: %q", k.Kind, object, msg)
				}
				continue
			}
			for _, b := range branches {
				if _, ok := props[b]; !ok {
					t.Errorf("%s: the one-of rule on %s names %q, which is no field there: %q", k.Kind, object, b, msg)
				}
			}
			out = append(out, oneOfRule{path: object, branches: branches})
		}
	})
	sort.SliceStable(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

// branchSet reports whether one of docs sets field b of object ("spec" or "spec.<dotted>").
func branchSet(docs []*unstructured.Unstructured, object, b string) bool {
	segs := append(strings.Split(object, ".")[1:], b)
	return slices.ContainsFunc(docs, func(d *unstructured.Unstructured) bool {
		return len(valuesAt(d.Object[keySpec], segs)) > 0
	})
}

// leafPaths returns the dotted paths of every leaf below a schema; a list of objects adds "[]" to its segment and a
// map or a list of scalars is a leaf.
func leafPaths(schema map[string]any, prefix string) []string {
	var out []string
	props, _ := schema["properties"].(map[string]any)
	for k, raw := range props {
		v, _ := raw.(map[string]any)
		p := k
		if prefix != "" {
			p = prefix + "." + k
		}
		if v["type"] == "array" {
			items, _ := v["items"].(map[string]any)
			if _, ok := items["properties"]; ok {
				out = append(out, leafPaths(items, p+"[]")...)
				continue
			}
		}
		if _, ok := v["properties"]; ok {
			out = append(out, leafPaths(v, p)...)
			continue
		}
		out = append(out, p)
	}
	return out
}

func hasPath(v any, segs []string) bool {
	if len(segs) == 0 {
		return true
	}
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	seg := segs[0]
	if list, ok := strings.CutSuffix(seg, "[]"); ok {
		items, _ := m[list].([]any)
		for _, item := range items {
			if hasPath(item, segs[1:]) {
				return true
			}
		}
		return false
	}
	child, ok := m[seg]
	return ok && hasPath(child, segs[1:])
}
