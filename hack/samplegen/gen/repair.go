package gen

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

const (
	searchBudget = 20000
	maxAlts      = 3
)

var (
	selfRef        = regexp.MustCompile(`\bself((?:\.\??[A-Za-z_][A-Za-z0-9_]*)+)`)
	celEscaped     = regexp.MustCompile(`^__([a-z]+)__$`)
	celLiteral     = regexp.MustCompile(`'((?:[^'\\]|\\.)*)'|"((?:[^"\\]|\\.)*)"|\b(\d+)\b`)
	celUnescapeMap = strings.NewReplacer("__underscores__", "__", "__dot__", ".", "__dash__", "-", "__slash__", "/")
)

type option struct {
	remove bool
	value  any
}

type mutation struct {
	path    []string
	options []option
}

// repair makes v satisfy the x-kubernetes-validations of s. Rules are fixed one at a time in schema order, each by
// changing as few of the fields it references as possible, preferring later fields so the field a rule names first
// (the first branch of a one-of) survives, and never breaking a rule fixed before.
func (g *generator) repair(s *apiextensions.JSONSchemaProps, v any, name string, path *field.Path) any {
	c, err := newChecker(s, false)
	if err != nil {
		g.warn(path, "%v", err)
		return v
	}
	errs := c.errors(v)
	failing, matched := failingRules(s.XValidations, errs)
	if len(failing) == 0 {
		return v
	}
	allowed := len(errs) - matched
	// tolerated holds the rules still allowed to fail; every other node rule must keep passing.
	tolerated := map[string]bool{}
	for _, r := range failing {
		tolerated[r.Rule] = true
	}
	accept := func(candidate any) bool {
		e := c.errors(candidate)
		f, m := failingRules(s.XValidations, e)
		return len(e)-m <= allowed && !slices.ContainsFunc(f, func(r apiextensions.ValidationRule) bool {
			return !tolerated[r.Rule]
		})
	}
	obj, isObj := v.(map[string]any)
	if !isObj || len(s.Properties) == 0 {
		clear(tolerated)
		if fixed, ok := g.searchValue(s, name, accept); ok {
			return fixed
		}
		g.warn(path, "no value satisfies %q", ruleTexts(failing))
		return v
	}
	for i, r := range failing {
		delete(tolerated, r.Rule)
		if accept(obj) {
			continue
		}
		fixed, ok := g.searchObject(s, obj, []apiextensions.ValidationRule{r}, accept)
		if !ok {
			fixed, ok = g.searchObject(s, obj, failing[:i+1], accept)
		}
		if !ok {
			g.warn(path, "no value satisfies %q", r.Rule)
			tolerated[r.Rule] = true
			continue
		}
		obj = fixed.(map[string]any)
	}
	return obj
}

func ruleTexts(rules []apiextensions.ValidationRule) []string {
	out := make([]string, len(rules))
	for i, r := range rules {
		out[i] = r.Rule
	}
	return out
}

func failingRules(rules apiextensions.ValidationRules, errs field.ErrorList) ([]apiextensions.ValidationRule, int) {
	var failing []apiextensions.ValidationRule
	for _, r := range rules {
		if slices.ContainsFunc(errs, func(e *field.Error) bool { return ruleMatches(r, e) }) {
			failing = append(failing, r)
		}
	}
	matched := 0
	for _, e := range errs {
		if slices.ContainsFunc(failing, func(r apiextensions.ValidationRule) bool { return ruleMatches(r, e) }) {
			matched++
		}
	}
	return failing, matched
}

func ruleMatches(r apiextensions.ValidationRule, e *field.Error) bool {
	return (r.Message != "" && strings.Contains(e.Detail, r.Message)) || strings.Contains(e.Detail, r.Rule)
}

func (g *generator) searchValue(s *apiextensions.JSONSchemaProps, name string, accept func(any) bool) (any, bool) {
	var candidates []any
	switch {
	case s.Type == "array" && s.Items != nil && s.Items.Schema != nil && isScalar(s.Items.Schema):
		for _, item := range g.candidates(s.Items.Schema, name, name) {
			candidates = append(candidates, []any{item})
		}
	case s.Type == "object" && s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil &&
		isScalar(s.AdditionalProperties.Schema):
		for _, item := range g.candidates(s.AdditionalProperties.Schema, name, name) {
			candidates = append(candidates, map[string]any{"example": item})
		}
	case s.Type == "":
		candidates = []any{placeholder, []any{placeholder}, int64(1), true}
	}
	for _, v := range candidates {
		if accept(v) {
			return v, true
		}
	}
	return nil, false
}

func (g *generator) searchObject(
	s *apiextensions.JSONSchemaProps, obj map[string]any, rules []apiextensions.ValidationRule, accept func(any) bool,
) (any, bool) {
	muts := g.mutations(s, obj, referencedPaths(rules), ruleLiterals(rules))
	budget := searchBudget
	for k := 1; k <= len(muts); k++ {
		combos := combinations(len(muts), k)
		slices.Reverse(combos)
		for _, combo := range combos {
			chosen := make([]option, len(combo))
			if v, ok := tryOptions(obj, muts, combo, chosen, 0, accept, &budget); ok {
				return v, true
			}
			if budget <= 0 {
				return nil, false
			}
		}
	}
	return nil, false
}

func (g *generator) mutations(
	s *apiextensions.JSONSchemaProps, obj map[string]any, paths [][]string, literals []any,
) []mutation {
	var muts []mutation
	for _, p := range paths {
		sub, required, ok := resolve(s, p)
		if !ok {
			continue
		}
		cur, present := lookup(obj, p)
		if !present {
			continue
		}
		var opts []option
		for _, alt := range g.alternatives(sub, cur, p, literals) {
			opts = append(opts, option{value: alt})
		}
		if !required && sub.Default == nil {
			opts = append(opts, option{remove: true})
		}
		if len(opts) > 0 {
			muts = append(muts, mutation{path: p, options: opts})
		}
	}
	return muts
}

func (g *generator) alternatives(s *apiextensions.JSONSchemaProps, cur any, p []string, literals []any) []any {
	if !isScalar(s) {
		return nil
	}
	c, err := newChecker(s, false)
	if err != nil {
		return nil
	}
	parent := ""
	if len(p) > 1 {
		parent = p[len(p)-2]
	}
	candidates := g.candidates(s, p[len(p)-1], parent)
	limit := len(candidates)
	if len(s.Enum) == 0 {
		limit = len(literals) + maxAlts
		candidates = append(slices.Clone(literals), candidates...)
	}
	var out []any
	for _, v := range candidates {
		if len(out) == limit {
			break
		}
		if v != cur && !slices.Contains(out, v) && len(c.errors(v)) == 0 {
			out = append(out, v)
		}
	}
	return out
}

func tryOptions(
	obj map[string]any, muts []mutation, combo []int, chosen []option, i int, accept func(any) bool, budget *int,
) (any, bool) {
	if i == len(combo) {
		*budget--
		candidate := deepCopy(obj).(map[string]any)
		for j, idx := range combo {
			apply(candidate, muts[idx].path, chosen[j])
		}
		return candidate, accept(candidate)
	}
	for _, opt := range muts[combo[i]].options {
		chosen[i] = opt
		if v, ok := tryOptions(obj, muts, combo, chosen, i+1, accept, budget); ok || *budget <= 0 {
			return v, ok
		}
	}
	return nil, false
}

// referencedPaths lists every property path (and its prefixes) the rules read through self, in order of appearance.
func referencedPaths(rules []apiextensions.ValidationRule) [][]string {
	seen := map[string]bool{}
	var out [][]string
	for _, r := range rules {
		for _, m := range selfRef.FindAllStringSubmatch(r.Rule, -1) {
			parts := strings.Split(strings.ReplaceAll(strings.TrimPrefix(m[1], "."), "?", ""), ".")
			for i := range parts {
				p := make([]string, i+1)
				for j := range p {
					p[j] = unescapeProperty(parts[j])
				}
				key := strings.Join(p, "\x00")
				if !seen[key] {
					seen[key] = true
					out = append(out, p)
				}
			}
		}
	}
	return out
}

// ruleLiterals returns the string and integer literals of the rules: the values a rule compares fields against.
func ruleLiterals(rules []apiextensions.ValidationRule) []any {
	var out []any
	for _, r := range rules {
		for _, m := range celLiteral.FindAllStringSubmatch(r.Rule, -1) {
			var v any
			switch {
			case m[3] != "":
				n, err := strconv.ParseInt(m[3], 10, 64)
				if err != nil {
					continue
				}
				v = n
			case m[1] != "":
				v = m[1]
			default:
				v = m[2]
			}
			if !slices.Contains(out, v) {
				out = append(out, v)
			}
		}
	}
	return out
}

func unescapeProperty(s string) string {
	if m := celEscaped.FindStringSubmatch(s); m != nil && m[1] != "underscores" && m[1] != "dot" &&
		m[1] != "dash" && m[1] != "slash" {
		return m[1]
	}
	return celUnescapeMap.Replace(s)
}

func resolve(s *apiextensions.JSONSchemaProps, p []string) (*apiextensions.JSONSchemaProps, bool, bool) {
	cur := s
	required := false
	for _, name := range p {
		child, ok := cur.Properties[name]
		if !ok {
			return nil, false, false
		}
		required = slices.Contains(cur.Required, name)
		cur = &child
	}
	return cur, required, true
}

func lookup(obj map[string]any, p []string) (any, bool) {
	var cur any = obj
	for _, name := range p {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[name]; !ok {
			return nil, false
		}
	}
	return cur, true
}

func apply(obj map[string]any, p []string, opt option) {
	parent, ok := lookup(obj, p[:len(p)-1])
	m, isMap := parent.(map[string]any)
	if !ok || !isMap {
		return
	}
	last := p[len(p)-1]
	if _, exists := m[last]; !exists {
		return
	}
	if opt.remove {
		delete(m, last)
		return
	}
	m[last] = opt.value
}

func combinations(n, k int) [][]int {
	var out [][]int
	var rec func(start int, cur []int)
	rec = func(start int, cur []int) {
		if len(cur) == k {
			out = append(out, slices.Clone(cur))
			return
		}
		for i := start; i < n; i++ {
			rec(i+1, append(cur, i))
		}
	}
	rec(0, nil)
	return out
}

func isScalar(s *apiextensions.JSONSchemaProps) bool {
	switch s.Type {
	case "string", "integer", "number", "boolean":
		return true
	}
	return s.XIntOrString
}

func deepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = deepCopy(e)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = deepCopy(e)
		}
		return out
	}
	return v
}

func (g *generator) secretDocs(root *apiextensions.JSONSchemaProps, obj map[string]any) []map[string]any {
	spec, ok := root.Properties["spec"]
	if !ok {
		return nil
	}
	data := map[string]map[string]any{}
	collectSecrets(&spec, obj["spec"], "spec", data)
	docs := make([]map[string]any, 0, len(data))
	for _, name := range sortedKeys(data) {
		docs = append(docs, map[string]any{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata":   map[string]any{nameKey: name, "namespace": SampleNamespace},
			"type":       "Opaque",
			"stringData": data[name],
		})
	}
	return docs
}

func collectSecrets(s *apiextensions.JSONSchemaProps, v any, name string, out map[string]map[string]any) {
	switch t := v.(type) {
	case map[string]any:
		_, hasName := s.Properties[nameKey]
		_, hasKey := s.Properties["key"]
		if isSecretRefName(name) && hasName && hasKey {
			secret, _ := t[nameKey].(string)
			key, _ := t["key"].(string)
			if secret != "" && key != "" {
				if out[secret] == nil {
					out[secret] = map[string]any{}
				}
				out[secret][key] = fakeSecret
			}
			return
		}
		for key, child := range t {
			if p, ok := s.Properties[key]; ok {
				collectSecrets(&p, child, key, out)
			} else if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
				collectSecrets(s.AdditionalProperties.Schema, child, name, out)
			}
		}
	case []any:
		if s.Items != nil && s.Items.Schema != nil {
			for _, e := range t {
				collectSecrets(s.Items.Schema, e, name, out)
			}
		}
	}
}
