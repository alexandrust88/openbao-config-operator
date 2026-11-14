package importer

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

var (
	durationType     = reflect.TypeFor[metav1.Duration]()
	resourceSpecType = reflect.TypeFor[commonv1alpha1.ResourceSpec]()
	secretRefType    = reflect.TypeFor[commonv1alpha1.SecretKeySelector]()
	localRefType     = reflect.TypeFor[commonv1alpha1.LocalObjectReference]()
)

// setField is a spec field the mapper filled from the read.
type setField struct {
	value reflect.Value
	path  string
}

// specField is one JSON field of a spec struct, with inline structs flattened.
type specField struct {
	value    reflect.Value
	name     string
	optional bool
}

// norm folds an API key or a JSON field name to a comparable form: "token_ttl", "tokenTTL" and "urls.ocsp_servers"
// become "tokenttl" and "urlsocspservers".
func norm(key string) string {
	return strings.ToLower(strings.NewReplacer("_", "", ".", "", "-", "").Replace(key))
}

// fields returns the JSON fields of the struct v, flattening inline structs and leaving out ResourceSpec.
func fields(v reflect.Value) []specField {
	var out []specField
	t := v.Type()
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		name, opts, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" {
			if f.Type != resourceSpecType && f.Type.Kind() == reflect.Struct {
				out = append(out, fields(v.Field(i))...)
			}
			continue
		}
		if name == "" || name == "-" {
			continue
		}
		optional := strings.Contains(opts, "omitempty") || f.Type.Kind() == reflect.Pointer
		out = append(out, specField{value: v.Field(i), name: name, optional: optional})
	}
	return out
}

// fill sets the fields of spec from observed, matching keys by norm. Fields named in skip, references to
// Kubernetes objects and Secret selectors are left alone. A nested struct is filled from a map value or from
// keys prefixed with its name, so "user_lockout_threshold" fills userLockout.threshold. It returns the fields it
// set and the JSON paths of matching keys it could not convert.
func fill(spec reflect.Value, observed engine.Payload, skip []string) (set []setField, unmapped []string) {
	index := make(map[string]any, len(observed))
	for key, value := range observed {
		index[norm(key)] = value
	}
	skipped := make(map[string]bool, len(skip))
	for _, name := range skip {
		skipped[name] = true
	}
	fillStruct(spec, index, "", "", skipped, &set, &unmapped)
	return set, unmapped
}

func fillStruct(v reflect.Value, index map[string]any, prefix, path string, skip map[string]bool,
	set *[]setField, unmapped *[]string) {
	for _, f := range fields(v) {
		if path == "" && skip[f.name] {
			continue
		}
		base := baseType(f.value.Type())
		if base == secretRefType || base == localRefType || isRefSlice(f.value.Type()) {
			continue
		}
		key := prefix + norm(f.name)
		fieldPath := joinPath(path, f.name)
		if raw, ok := index[key]; ok {
			if converted, ok := convert(raw, f.value.Type()); ok {
				f.value.Set(converted)
				*set = append(*set, setField{value: f.value, path: fieldPath})
			} else if !isEmpty(raw) {
				*unmapped = append(*unmapped, fieldPath)
			}
			continue
		}
		if base.Kind() != reflect.Struct || base == durationType {
			continue
		}
		target := f.value
		if target.Kind() == reflect.Pointer {
			if target.IsNil() {
				target = reflect.New(base)
			}
			target = target.Elem()
		}
		before := len(*set)
		fillStruct(target, index, key, fieldPath, skip, set, unmapped)
		if len(*set) > before && f.value.Kind() == reflect.Pointer && f.value.IsNil() {
			f.value.Set(target.Addr())
		}
	}
}

func joinPath(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

func baseType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

func isRefSlice(t reflect.Type) bool {
	if t.Kind() != reflect.Slice {
		return false
	}
	elem := baseType(t.Elem())
	return elem == secretRefType || elem == localRefType
}

// convert turns a value read from OpenBao into a value of type t. Durations accept seconds or Go duration strings;
// a string holding JSON fills a struct or map; a comma-separated string fills a list of strings. Empty values are
// not converted, so the spec leaves them unset.
func convert(raw any, t reflect.Type) (reflect.Value, bool) {
	if isEmpty(raw) {
		return reflect.Value{}, false
	}
	base := baseType(t)
	target := reflect.New(base)
	switch base {
	case durationType:
		secs, ok := durationSeconds(raw)
		if !ok {
			return reflect.Value{}, false
		}
		target.Elem().Set(reflect.ValueOf(metav1.Duration{Duration: time.Duration(secs * float64(time.Second))}))
	default:
		encoded, ok := encodeFor(raw, base)
		if !ok || json.Unmarshal(encoded, target.Interface()) != nil {
			return reflect.Value{}, false
		}
	}
	if t.Kind() == reflect.Pointer {
		return target, true
	}
	return target.Elem(), true
}

func encodeFor(raw any, base reflect.Type) ([]byte, bool) {
	if base.Kind() == reflect.String {
		switch v := raw.(type) {
		case float64:
			raw = strconv.FormatFloat(v, 'f', -1, 64)
		case bool:
			raw = strconv.FormatBool(v)
		case json.Number:
			raw = v.String()
		}
	}
	if s, ok := raw.(string); ok {
		switch {
		case base.Kind() == reflect.Struct || base.Kind() == reflect.Map:
			return []byte(s), json.Valid([]byte(s))
		case base.Kind() == reflect.Slice && base.Elem().Kind() == reflect.String:
			parts := strings.Split(s, ",")
			for i := range parts {
				parts[i] = strings.TrimSpace(parts[i])
			}
			raw = parts
		}
	}
	encoded, err := json.Marshal(raw)
	return encoded, err == nil
}

func durationSeconds(raw any) (float64, bool) {
	switch v := raw.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f, true
		}
		d, err := time.ParseDuration(v)
		return d.Seconds(), err == nil
	}
	return 0, false
}

func isEmpty(raw any) bool {
	switch v := raw.(type) {
	case nil:
		return true
	case string:
		return v == ""
	case []any:
		return len(v) == 0
	case []string:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	}
	return false
}

// isZero reports whether a filled field holds a value OpenBao reports for "unset": false, 0, a zero duration or
// an empty string, list or map.
func isZero(v reflect.Value) bool {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return true
		}
		v = v.Elem()
	}
	if v.Type() == durationType {
		return v.Interface().(metav1.Duration).Duration == 0
	}
	switch v.Kind() {
	case reflect.Slice, reflect.Map:
		return v.Len() == 0
	case reflect.Struct:
		return false
	}
	return v.IsZero()
}

// dropEmptyStructs sets every pointer-to-struct field of v whose struct is entirely zero back to nil.
func dropEmptyStructs(v reflect.Value) {
	for _, f := range fields(v) {
		if f.value.Kind() != reflect.Pointer || f.value.IsNil() || f.value.Elem().Kind() != reflect.Struct ||
			f.value.Type().Elem() == durationType {
			continue
		}
		dropEmptyStructs(f.value.Elem())
		if f.value.Elem().IsZero() {
			f.value.Set(reflect.Zero(f.value.Type()))
		}
	}
}
