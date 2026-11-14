package engine

import (
	"encoding/hex"
	"encoding/json"
	"maps"
	"math"
	"slices"
	"testing"
)

var fuzzPayloadSeeds = []string{
	`{}`,
	`{"a":"x","n":3,"f":3.5,"b":false,"z":0,"e":"","nil":null}`,
	`{"ttl":"1h","period":60,"max_ttl":"1d","x":"30s","y":"3600"}`,
	`{"policies":["a","b","a"],"l":["b","a"],"empty":[],"m":{}}`,
	`{"m":{"a":1,"b":null,"c":[]},"partial":{"a":1},"deep":{"n":{"a":[{"b":2}]}}}`,
	`{"password":"s3cr3t","jwt":"j","type":"kv","secret_id":"s","accessor":"x"}`,
	`{"big":9007199254740993,"neg":-1e300,"tiny":5e-324,"u":"é日"}`,
}

// fuzzSpec gives every key of payload, in sorted order, one FieldSpec role chosen by three bits of sel. Only lists
// get the Sets role: fieldEqual never matches a set key holding another type, not even to itself.
func fuzzSpec(payload Payload, sel uint64) FieldSpec {
	var spec FieldSpec
	for i, key := range slices.Sorted(maps.Keys(payload)) {
		switch (sel >> (3 * (i % 21))) & 7 {
		case 1:
			if _, ok := payload[key].([]any); ok {
				spec.Sets = append(spec.Sets, key)
			}
		case 2:
			spec.Durations = append(spec.Durations, key)
		case 3:
			spec.Partial = append(spec.Partial, key)
		case 4:
			spec.WriteOnly = append(spec.WriteOnly, key)
		case 5:
			spec.CreateOnly = append(spec.CreateOnly, key)
		case 6:
			spec.ServerOwned = append(spec.ServerOwned, key)
		case 7:
			spec.WriteOnce = append(spec.WriteOnce, key)
		}
	}
	return spec
}

func decodePayload(raw []byte) (Payload, bool) {
	var payload Payload
	if json.Unmarshal(raw, &payload) != nil || payload == nil {
		return nil, false
	}
	return payload, true
}

// typed rebuilds a decoded JSON value with the Go types adapters use: int64, []string and map[string]string.
func typed(value any) any {
	switch v := value.(type) {
	case float64:
		if v == math.Trunc(v) && math.Abs(v) <= 1<<53 && !math.Signbit(v) {
			return int64(v)
		}
	case []any:
		strs := make([]string, 0, len(v))
		out := make([]any, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok {
				strs = append(strs, s)
			}
			out = append(out, typed(e))
		}
		if len(strs) == len(v) && len(v) > 0 {
			return strs
		}
		return out
	case map[string]any:
		strs := make(map[string]string, len(v))
		out := make(map[string]any, len(v))
		for k, e := range v {
			if s, ok := e.(string); ok {
				strs[k] = s
			}
			out[k] = typed(e)
		}
		if len(strs) == len(v) && len(v) > 0 {
			return strs
		}
		return out
	}
	return value
}

func typedPayload(payload Payload) Payload {
	out := make(Payload, len(payload))
	for k, v := range payload {
		out[k] = typed(v)
	}
	return out
}

func skipped(spec FieldSpec, key string) bool {
	return slices.Contains(spec.WriteOnly, key) || slices.Contains(spec.CreateOnly, key) ||
		slices.Contains(spec.ServerOwned, key) || slices.Contains(spec.WriteOnce, key)
}

func FuzzDiff(f *testing.F) {
	for i, seed := range fuzzPayloadSeeds {
		f.Add([]byte(seed), uint64(i)*0x9249249249249249)
	}
	f.Fuzz(func(t *testing.T, raw []byte, sel uint64) {
		desired, ok := decodePayload(raw)
		if !ok {
			return
		}
		spec := fuzzSpec(desired, sel)
		if got := Diff(desired, desired, spec); len(got) > 0 {
			t.Fatalf("Diff(x, x) = %v for %s under %+v", got, raw, spec)
		}
		typedDesired := typedPayload(desired)
		if got := Diff(typedDesired, desired, spec); len(got) > 0 {
			t.Fatalf("Diff(typed, decoded) = %v for %s under %+v", got, raw, spec)
		}
		if got := Diff(desired, typedDesired, spec); len(got) > 0 {
			t.Fatalf("Diff(decoded, typed) = %v for %s under %+v", got, raw, spec)
		}

		var want []string
		for key, value := range desired {
			if !skipped(spec, key) && !isEmpty(value) {
				want = append(want, key)
			}
		}
		slices.Sort(want)
		if got := Diff(desired, Payload{}, spec); !slices.Equal(got, want) {
			t.Fatalf("Diff(x, {}) = %v, want %v for %s under %+v", got, want, raw, spec)
		}

		observed, aliases := Payload{}, map[string]string{}
		for key, value := range desired {
			readKey := "read:" + key
			if _, taken := desired[readKey]; taken {
				observed[key] = value
				continue
			}
			observed[readKey] = value
			aliases[readKey] = key
		}
		spec.Aliases = aliases
		if got := Diff(desired, observed, spec); len(got) > 0 {
			t.Fatalf("Diff(x, renamed x) = %v for %s under %+v", got, raw, spec)
		}
	})
}

func FuzzWriteOnlyHash(f *testing.F) {
	for i, seed := range fuzzPayloadSeeds {
		f.Add([]byte("0123456789abcdef0123456789abcdef"), []byte(seed), uint64(i)*0x4924924924924924)
	}
	f.Fuzz(func(t *testing.T, key, raw []byte, sel uint64) {
		desired, ok := decodePayload(raw)
		if !ok {
			return
		}
		spec := fuzzSpec(desired, sel)
		spec.WriteOnly = append(spec.WriteOnly, "absent-write-only")
		h := WriteOnlyHash(key, desired, spec)
		if again := WriteOnlyHash(key, maps.Clone(desired), spec); again != h {
			t.Fatalf("WriteOnlyHash is not deterministic: %q != %q", h, again)
		}
		if typedHash := WriteOnlyHash(key, typedPayload(desired), spec); typedHash != h {
			t.Fatalf("WriteOnlyHash depends on Go types: %q != %q for %s", h, typedHash, raw)
		}

		writeOnly := Payload{}
		for _, k := range spec.WriteOnly {
			if v, ok := desired[k]; ok {
				writeOnly[k] = v
			}
		}
		if len(writeOnly) == 0 {
			if h != "" {
				t.Fatalf("WriteOnlyHash = %q without write-only keys", h)
			}
			return
		}
		if decoded, err := hex.DecodeString(h); err != nil || len(decoded) != 32 {
			t.Fatalf("WriteOnlyHash = %q, want 64 hex characters", h)
		}

		others := maps.Clone(writeOnly)
		others["not-write-only"] = string(raw)
		if got := WriteOnlyHash(key, others, spec); got != h && !slices.Contains(spec.WriteOnly, "not-write-only") {
			t.Fatalf("WriteOnlyHash changed with a key outside WriteOnly: %q != %q", got, h)
		}

		otherKey := []byte("x")
		if len(key) > 0 {
			otherKey = slices.Clone(key)
			otherKey[0] ^= 1
		}
		if WriteOnlyHash(otherKey, desired, spec) == h {
			t.Fatalf("WriteOnlyHash ignores the key")
		}

		for k, v := range writeOnly {
			changed := maps.Clone(desired)
			changed[k] = []any{v}
			if WriteOnlyHash(key, changed, spec) == h {
				t.Fatalf("WriteOnlyHash ignores a change of write-only key %q", k)
			}
		}
	})
}
