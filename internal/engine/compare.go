package engine

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

// Diff returns the sorted keys of desired whose value differs from observed.
//
// Only keys present in desired are compared, and WriteOnly, CreateOnly, ServerOwned and WriteOnce keys are skipped; Diff only runs on an
// existing object, which a CreateOnly key can no longer change. Observed keys are renamed
// by spec.Aliases first; the read name wins when observed holds both names. Values are compared as JSON, so numbers
// compare numerically whatever their Go type. Nested maps are compared exactly, so a key removed from the spec is a
// difference, except under the keys in spec.Partial, where they are compared on the desired keys only. Sets ignore order and duplicates, Durations
// compare in seconds. A nil value, an absent key, an empty list and an empty map are all equal to one
// another; any other value, including "", 0 and false, is not equal to absent.
func Diff(desired, observed Payload, spec FieldSpec) []string {
	observed = applyAliases(observed, spec.Aliases)
	var differing []string
	for key, want := range desired {
		if slices.Contains(spec.WriteOnly, key) || slices.Contains(spec.CreateOnly, key) ||
			slices.Contains(spec.ServerOwned, key) || slices.Contains(spec.WriteOnce, key) {
			continue
		}
		if !fieldEqual(key, want, observed[key], spec) {
			differing = append(differing, key)
		}
	}
	slices.Sort(differing)
	return differing
}

// WriteOnlyHash returns the HMAC-SHA256, keyed by key, of the canonical JSON of the write-only keys of
// desired, or "" when desired sets none of them. The key stops anyone who can read the status from
// brute-forcing low-entropy secret values offline.
func WriteOnlyHash(key []byte, desired Payload, spec FieldSpec) string {
	writeOnly := Payload{}
	for _, key := range spec.WriteOnly {
		if value, ok := desired[key]; ok {
			writeOnly[key] = value
		}
	}
	if len(writeOnly) == 0 {
		return ""
	}
	// encoding/json sorts map keys, which makes the encoding canonical.
	encoded, err := json.Marshal(writeOnly)
	if err != nil {
		return ""
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(encoded)
	return hex.EncodeToString(mac.Sum(nil))
}

// ObjectHashKey derives the key of one resource's status.writeOnlyHash from the operator's key, the resource's UID
// and its group and kind. Equal inputs of two resources then hash differently, so a hash cannot be matched against a
// guess hashed through another resource, such as an OpenBaoEndpoint in another tenant's namespace.
func ObjectHashKey(key []byte, uid types.UID, groupKind schema.GroupKind) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(uid))
	mac.Write([]byte{0})
	mac.Write([]byte(groupKind.String()))
	return mac.Sum(nil)
}

// StripCreateOnly returns a copy of desired without the CreateOnly keys.
func StripCreateOnly(desired Payload, spec FieldSpec) Payload {
	stripped := make(Payload, len(desired))
	for key, value := range desired {
		if !slices.Contains(spec.CreateOnly, key) {
			stripped[key] = value
		}
	}
	return stripped
}

// writePayload returns the payload of a create, or of an update: without CreateOnly keys, and without WriteOnce keys
// unless writeOnce. ServerOwned keys are never sent.
func writePayload(desired Payload, spec FieldSpec, create, writeOnce bool) Payload {
	payload := make(Payload, len(desired))
	for key, value := range desired {
		switch {
		case slices.Contains(spec.ServerOwned, key):
		case !create && slices.Contains(spec.CreateOnly, key):
		case !create && !writeOnce && slices.Contains(spec.WriteOnce, key):
		default:
			payload[key] = value
		}
	}
	return payload
}

func applyAliases(observed Payload, aliases map[string]string) Payload {
	if len(aliases) == 0 {
		return observed
	}
	renamed := maps.Clone(observed)
	for readKey, writeKey := range aliases {
		if value, ok := observed[readKey]; ok {
			renamed[writeKey] = value
		}
	}
	return renamed
}

func fieldEqual(key string, want, got any, spec FieldSpec) bool {
	want, wantOK := canonical(want)
	got, gotOK := canonical(got)
	if !wantOK || !gotOK {
		return false
	}
	if isEmpty(want) || isEmpty(got) {
		return isEmpty(want) && isEmpty(got)
	}
	if slices.Contains(spec.Durations, key) {
		wantSeconds, ok1 := seconds(want)
		gotSeconds, ok2 := seconds(got)
		if ok1 && ok2 {
			return wantSeconds == gotSeconds
		}
	}
	if slices.Contains(spec.Sets, key) {
		wantSet, wantList := setOf(want)
		gotSet, gotList := setOf(got)
		if wantList && gotList {
			return slices.Equal(wantSet, gotSet)
		}
	}
	return valueEqual(want, got, !partial(spec.Partial, key))
}

// partial reports whether key is listed in patterns, where an entry ending in "*" matches by prefix.
func partial(patterns []string, key string) bool {
	return slices.ContainsFunc(patterns, func(p string) bool {
		prefix, wildcard := strings.CutSuffix(p, "*")
		return p == key || wildcard && strings.HasPrefix(key, prefix)
	})
}

// valueEqual compares canonical values. Maps are compared on the keys of want only, and also on the keys of got
// that hold a non-empty value when exact is set.
func valueEqual(want, got any, exact bool) bool {
	if isEmpty(want) || isEmpty(got) {
		return isEmpty(want) && isEmpty(got)
	}
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return false
		}
		for key, value := range w {
			if !valueEqual(value, g[key], exact) {
				return false
			}
		}
		if exact {
			for key, value := range g {
				if _, ok := w[key]; !ok && !isEmpty(value) {
					return false
				}
			}
		}
		return true
	case []any:
		g, ok := got.([]any)
		if !ok || len(w) != len(g) {
			return false
		}
		for i := range w {
			if !valueEqual(w[i], g[i], exact) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(want, got)
	}
}

// canonical converts any JSON-encodable value to the generic form json.Unmarshal produces.
func canonical(value any) (any, bool) {
	switch value.(type) {
	case nil, string, bool, float64:
		return value, true
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, false
	}
	return decoded, true
}

func isEmpty(value any) bool {
	switch v := value.(type) {
	case nil:
		return true
	case []any:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	}
	return false
}

// seconds parses 30, 30.0, "30", "30s", "1h" and "1d" into seconds.
func seconds(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case string:
		v = strings.TrimSpace(v)
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			return n, true
		}
		if days, ok := strings.CutSuffix(v, "d"); ok {
			if n, err := strconv.ParseFloat(days, 64); err == nil {
				return n * 86400, true
			}
		}
		if d, err := time.ParseDuration(v); err == nil {
			return d.Seconds(), true
		}
	}
	return 0, false
}

// setOf returns the sorted, de-duplicated JSON encodings of the elements of a list.
func setOf(value any) ([]string, bool) {
	list, ok := value.([]any)
	if !ok {
		return nil, false
	}
	elements := make([]string, 0, len(list))
	for _, element := range list {
		encoded, err := json.Marshal(element)
		if err != nil {
			return nil, false
		}
		elements = append(elements, string(encoded))
	}
	slices.Sort(elements)
	return slices.Compact(elements), true
}
