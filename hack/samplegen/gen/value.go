package gen

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	celconfig "k8s.io/apiserver/pkg/apis/cel"
)

const (
	nameKey       = "name"
	placeholder   = "value"
	exampleCIDR   = "10.0.0.0/8"
	exampleIP     = "127.0.0.1"
	exampleEmail  = "user@example.com"
	exampleURL    = "https://openbao.example.com:8200"
	examplePolicy = "path \"secret/data/example/*\" {\n  capabilities = [\"read\"]\n}\n"
	exampleCert   = "-----BEGIN CERTIFICATE-----\nZmFrZS1ub3QtYS1jZXJ0aWZpY2F0ZQ==\n-----END CERTIFICATE-----\n"
	examplePubKey = "-----BEGIN PUBLIC KEY-----\nZmFrZS1ub3QtYS1rZXk=\n-----END PUBLIC KEY-----\n"
)

var genericStrings = []string{
	"example", placeholder, "1h", exampleURL, exampleCIDR, exampleIP, "default", "a", "1",
	exampleEmail, "openbao.example.com",
}

var formatStrings = map[string]string{
	"duration":  "1h",
	"date-time": "2026-01-01T00:00:00Z",
	"date":      "2026-01-01",
	"uri":       exampleURL,
	"url":       exampleURL,
	"email":     exampleEmail,
	"hostname":  "openbao.example.com",
	"ipv4":      exampleIP,
	"ipv6":      "::1",
	"cidr":      exampleCIDR,
	"uuid":      "00000000-0000-4000-8000-000000000000",
	"byte":      "ZXhhbXBsZQ==",
	"password":  fakeSecret,
}

type generator struct {
	kind     string
	warnings []string
}

func (g *generator) warn(path *field.Path, format string, args ...any) {
	g.warnings = append(g.warnings, path.String()+": "+fmt.Sprintf(format, args...))
}

// value returns a value for every property of s. name is the property name, parent the name of the enclosing
// property; both steer placeholders.
func (g *generator) value(s *apiextensions.JSONSchemaProps, name, parent string, path *field.Path) any {
	var v any
	switch {
	case s.Type == "object" || len(s.Properties) > 0:
		v = g.object(s, name, path)
	case s.Type == "array":
		v = g.array(s, name, path)
	case s.Type == "" && !s.XIntOrString:
		v = map[string]any{"key": placeholder}
	default:
		return g.scalar(s, name, parent, path)
	}
	if len(s.XValidations) > 0 {
		v = g.repair(s, v, name, path)
	}
	return v
}

func (g *generator) object(s *apiextensions.JSONSchemaProps, name string, path *field.Path) map[string]any {
	obj := map[string]any{}
	switch {
	case len(s.Properties) > 0:
		for _, key := range sortedKeys(s.Properties) {
			child := s.Properties[key]
			obj[key] = g.value(&child, key, name, path.Child(key))
		}
	case s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil:
		obj["example"] = g.value(s.AdditionalProperties.Schema, name, name, path.Key("example"))
	case s.XPreserveUnknownFields != nil && *s.XPreserveUnknownFields:
		obj["key"] = placeholder
	}
	return obj
}

func (g *generator) array(s *apiextensions.JSONSchemaProps, name string, path *field.Path) []any {
	n := 1
	if s.MinItems != nil && *s.MinItems > 1 {
		n = int(*s.MinItems)
	}
	if s.Items == nil || s.Items.Schema == nil {
		return []any{}
	}
	items := s.Items.Schema
	list := make([]any, 0, n)
	for i := range n {
		item := g.value(items, name, name, path.Index(i))
		if str, ok := item.(string); ok && i > 0 {
			item = distinctString(items, str, i)
		}
		list = append(list, item)
	}
	return list
}

func distinctString(s *apiextensions.JSONSchemaProps, v string, i int) any {
	if len(s.Enum) > 0 {
		return s.Enum[i%len(s.Enum)]
	}
	return fmt.Sprintf("%s-%d", v, i)
}

func (g *generator) scalar(s *apiextensions.JSONSchemaProps, name, parent string, path *field.Path) any {
	if s.Default != nil {
		return normalize(*s.Default)
	}
	c, err := newChecker(s, false)
	if err != nil {
		g.warn(path, "%v", err)
		return nil
	}
	candidates := g.candidates(s, name, parent)
	for _, v := range candidates {
		if len(c.errors(v)) == 0 {
			return v
		}
	}
	g.warn(path, "no candidate value passes validation: %v", c.errors(candidates[0]).ToAggregate())
	return candidates[0]
}

func (g *generator) candidates(s *apiextensions.JSONSchemaProps, name, parent string) []any {
	if len(s.Enum) > 0 {
		out := make([]any, len(s.Enum))
		for i, e := range s.Enum {
			out[i] = normalize(e)
		}
		return out
	}
	switch s.Type {
	case "boolean":
		return []any{true, false}
	case "integer", "number":
		return numberCandidates(s)
	case "string":
		return toAny(g.stringCandidates(s, name, parent))
	}
	return append([]any{int64(1)}, toAny(g.stringCandidates(s, name, parent))...)
}

func numberCandidates(s *apiextensions.JSONSchemaProps) []any {
	out := []any{int64(1)}
	if s.Minimum != nil {
		m := int64(math.Ceil(*s.Minimum))
		if s.ExclusiveMinimum && float64(m) == *s.Minimum {
			m++
		}
		out = append(out, m)
	}
	if s.Maximum != nil {
		m := int64(math.Floor(*s.Maximum))
		if s.ExclusiveMaximum && float64(m) == *s.Maximum {
			m--
		}
		out = append(out, m)
	}
	return append(out, int64(0), int64(10), int64(60), int64(3600))
}

func (g *generator) stringCandidates(s *apiextensions.JSONSchemaProps, name, parent string) []string {
	var out []string
	if v, ok := formatStrings[s.Format]; ok {
		out = append(out, v)
	}
	if v := g.refString(name, parent); v != "" {
		out = append(out, v)
	}
	if v := namedString(name); v != "" {
		out = append(out, v)
	}
	out = append(out, genericStrings...)
	if s.Pattern != "" {
		for _, reps := range []int{0, 1, 2, 4, 8, 16, 32, 64, 128, 256} {
			if v, err := fromPattern(s.Pattern, reps); err == nil {
				out = append(out, v)
			}
		}
	}
	if s.MinLength != nil {
		for i, v := range out {
			if pad := int(*s.MinLength) - len(v); pad > 0 {
				out[i] = v + strings.Repeat("x", pad)
			}
		}
	}
	return out
}

// refString names the objects that *Ref fields point at: the sample connection, a Secret appended to the
// sample, or the sample of another kind.
func (g *generator) refString(name, parent string) string {
	if !strings.HasSuffix(parent, "Ref") && !strings.HasSuffix(parent, "Refs") {
		return ""
	}
	switch {
	case name == "namespace":
		return SampleNamespace
	case name == "key":
		return placeholder
	case name != nameKey:
		return ""
	case parent == "connectionRef":
		return connectionName
	case isSecretRefName(parent):
		return secretName(g.kind, parent)
	}
	return SampleName
}

func namedString(name string) string {
	n := strings.ToLower(name)
	contains := func(subs ...string) bool {
		for _, sub := range subs {
			if strings.Contains(n, sub) {
				return true
			}
		}
		return false
	}
	switch {
	case strings.HasSuffix(n, "ref"):
		return ""
	case n == "policy":
		return examplePolicy
	case contains("pubkey", "publickey"):
		return examplePubKey
	case contains("pem", "certificate", "cabundle") || strings.HasSuffix(n, "cert"):
		return exampleCert
	case contains("url", "uri", "address", "endpoint", "issuer") || strings.HasSuffix(n, "host") ||
		strings.HasSuffix(n, "addr"):
		return exampleURL
	case contains("ttl", "duration", "period", "interval", "timeout", "leeway", "maxage", "lifetime", "delay", "lease",
		"buffer") || strings.HasSuffix(n, "exp"):
		return "1h"
	case strings.HasSuffix(n, "arn"):
		return "arn:aws:iam::123456789012:policy/example"
	case strings.HasSuffix(n, "dn"):
		return "cn=example,dc=example,dc=com"
	case contains("cidr"):
		return exampleCIDR
	case strings.HasSuffix(n, "ip") || strings.HasSuffix(n, "ips"):
		return exampleIP
	case contains("email"):
		return exampleEmail
	case contains("polic"):
		return "default"
	case n == "operator":
		return "In"
	}
	return ""
}

func isSecretRefName(name string) bool {
	return strings.HasSuffix(strings.ToLower(name), "secretref")
}

func secretName(kind, property string) string {
	return strings.ToLower(kind) + "-" + kebab(property)
}

func kebab(s string) string {
	runes := []rune(s)
	var b strings.Builder
	for i, r := range runes {
		upper := unicode.IsUpper(r)
		if i > 0 && upper {
			prevLower := !unicode.IsUpper(runes[i-1])
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if prevLower || nextLower {
				b.WriteByte('-')
			}
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// checker validates a value against a schema node the way the API server does: OpenAPI then CEL.
type checker struct {
	openapi    validation.SchemaValidator
	cel        *cel.Validator
	structural *schema.Structural
}

func newChecker(s *apiextensions.JSONSchemaProps, resourceRoot bool) (*checker, error) {
	sv, _, err := validation.NewSchemaValidator(s)
	if err != nil {
		return nil, err
	}
	st, err := schema.NewStructural(s)
	if err != nil {
		return nil, err
	}
	return &checker{openapi: sv, cel: cel.NewValidator(st, resourceRoot, celconfig.PerCallLimit), structural: st}, nil
}

func (c *checker) errors(v any) field.ErrorList {
	errs := validation.ValidateCustomResource(nil, v, c.openapi)
	if c.cel != nil {
		celErrs, _ := c.cel.Validate(context.Background(), nil, c.structural, v, nil, celconfig.RuntimeCELCostBudget)
		errs = append(errs, celErrs...)
	}
	return errs
}

func (g *generator) validateRoot(root *apiextensions.JSONSchemaProps, obj map[string]any) error {
	c, err := newChecker(root, true)
	if err != nil {
		return err
	}
	for _, e := range c.errors(obj) {
		g.warnings = append(g.warnings, e.Error())
	}
	return nil
}

// normalize turns JSON numbers decoded as float64 into int64 where integral, as the API server does.
func normalize(v any) any {
	switch t := v.(type) {
	case float64:
		if t == math.Trunc(t) {
			return int64(t)
		}
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = normalize(e)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = normalize(e)
		}
		return out
	}
	return v
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func toAny[T any](in []T) []any {
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}
