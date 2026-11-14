package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"slices"
	"testing"
)

const (
	keyPassword      = "password"
	keyPeriod        = "period"
	keyTokenPeriod   = "token_period"
	keyType          = "type"
	keyPolicies      = "policies"
	keyTTL           = "ttl"
	keyJWT           = "jwt"
	keyPolicy        = "policy"
	keySecretID      = "secret_id"
	testNamespace    = "tenant"
	testPolicyText   = `path "*" {}`
	credsSecret      = "creds"
	outputSecretName = "p-out"
	keyPartial       = "partial"
)

func TestDiff(t *testing.T) {
	spec := FieldSpec{
		WriteOnly:   []string{keyPassword},
		CreateOnly:  []string{keyType},
		Aliases:     map[string]string{keyTokenPeriod: keyPeriod},
		Sets:        []string{keyPolicies},
		Durations:   []string{keyTTL, keyPeriod},
		Partial:     []string{keyPartial},
		ServerOwned: []string{v2Accessor},
		WriteOnce:   []string{v2BindPass},
	}
	tests := []struct {
		name     string
		desired  Payload
		observed Payload
		want     []string
	}{
		{"equal strings", Payload{"a": "x"}, Payload{"a": "x"}, nil},
		{"different strings", Payload{"a": "x"}, Payload{"a": "y"}, []string{"a"}},
		{"only desired keys compared", Payload{"a": "x"}, Payload{"a": "x", "extra": 1}, nil},
		{"absent observed key differs", Payload{"a": "x"}, Payload{}, []string{"a"}},
		{"sorted output", Payload{"b": 1, "a": 1}, Payload{}, []string{"a", "b"}},
		{"write-only skipped", Payload{keyPassword: "s3cr3t"}, Payload{}, nil},
		{"create-only skipped", Payload{keyType: "kv"}, Payload{keyType: "kv-v2"}, nil},
		{"int vs float64", Payload{"n": 3}, Payload{"n": float64(3)}, nil},
		{"json.Number vs int64", Payload{"n": json.Number("3")}, Payload{"n": int64(3)}, nil},
		{"numbers differ", Payload{"n": 3}, Payload{"n": 3.5}, []string{"n"}},
		{"number vs numeric string differs outside durations", Payload{"n": 3}, Payload{"n": "3"}, []string{"n"}},
		{"alias applied", Payload{keyPeriod: 60}, Payload{keyTokenPeriod: 60}, nil},
		{"alias wins over stale write key", Payload{keyPeriod: 60}, Payload{keyTokenPeriod: 60, keyPeriod: 0}, nil},
		{"duration string vs seconds", Payload{keyTTL: "1h"}, Payload{keyTTL: 3600}, nil},
		{"duration seconds string", Payload{keyTTL: "3600"}, Payload{keyTTL: float64(3600)}, nil},
		{"duration 30s vs json.Number", Payload{keyTTL: "30s"}, Payload{keyTTL: json.Number("30")}, nil},
		{"duration days", Payload{keyTTL: "1d"}, Payload{keyTTL: 86400}, nil},
		{"duration differs", Payload{keyTTL: "1h"}, Payload{keyTTL: 60}, []string{keyTTL}},
		{"duration through alias", Payload{keyPeriod: "1m"}, Payload{keyTokenPeriod: 60}, nil},
		{"set order ignored", Payload{keyPolicies: []string{"a", "b"}}, Payload{keyPolicies: []any{"b", "a"}}, nil},
		{"set duplicates ignored", Payload{keyPolicies: []string{"a", "a"}}, Payload{keyPolicies: []any{"a"}}, nil},
		{"set differs", Payload{keyPolicies: []string{"a"}}, Payload{keyPolicies: []any{"a", "b"}}, []string{keyPolicies}},
		{"list order matters outside sets", Payload{"l": []string{"a", "b"}}, Payload{"l": []any{"b", "a"}}, []string{"l"}},
		{"nested map extra key differs", Payload{"m": map[string]any{"a": 1}}, Payload{"m": map[string]any{"a": 1.0, "b": 2}}, []string{"m"}},
		{"nested map removed key differs", Payload{"m": map[string]any{}}, Payload{"m": map[string]any{"tier": "x"}}, []string{"m"}},
		{"nested map extra empty key equal", Payload{"m": map[string]any{"a": 1}}, Payload{"m": map[string]any{"a": 1, "b": nil, "c": []any{}}}, nil},
		{"deeper map extra key differs", Payload{"m": map[string]any{"n": map[string]any{"a": 1}}}, Payload{"m": map[string]any{"n": map[string]any{"a": 1, "b": 2}}}, []string{"m"}},
		{"map in list extra key differs", Payload{"l": []any{map[string]any{"a": 1}}}, Payload{"l": []any{map[string]any{"a": 1, "b": 2}}}, []string{"l"}},
		{"partial map on desired keys", Payload{keyPartial: map[string]any{"a": 1}}, Payload{keyPartial: map[string]any{"a": 1.0, "b": 2}}, nil},
		{"partial map value differs", Payload{keyPartial: map[string]any{"a": 1}}, Payload{keyPartial: map[string]any{"a": 2}}, []string{keyPartial}},
		{"server-owned skipped", Payload{v2Accessor: "x"}, Payload{v2Accessor: "y"}, nil},
		{"write-once skipped", Payload{v2BindPass: "x"}, Payload{}, nil},
		{"nested map differs", Payload{"m": map[string]string{"a": "x"}}, Payload{"m": map[string]any{"a": "y"}}, []string{"m"}},
		{"nested map vs scalar", Payload{"m": map[string]any{"a": 1}}, Payload{"m": "x"}, []string{"m"}},
		{"nil equals absent", Payload{"a": nil}, Payload{}, nil},
		{"empty list equals absent", Payload{"a": []string{}}, Payload{}, nil},
		{"empty list equals nil", Payload{"a": []string{}}, Payload{"a": nil}, nil},
		{"empty map equals empty list", Payload{"a": map[string]any{}}, Payload{"a": []any{}}, nil},
		{"empty set equals absent", Payload{keyPolicies: []string{}}, Payload{}, nil},
		{"empty string differs from absent", Payload{"a": ""}, Payload{}, []string{"a"}},
		{"zero differs from absent", Payload{"a": 0}, Payload{}, []string{"a"}},
		{"false differs from absent", Payload{"a": false}, Payload{}, []string{"a"}},
		{"nil desired vs value differs", Payload{"a": nil}, Payload{"a": "x"}, []string{"a"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Diff(tt.desired, tt.observed, spec); !slices.Equal(got, tt.want) {
				t.Fatalf("Diff() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDiffDoesNotMutateObserved(t *testing.T) {
	observed := Payload{keyTokenPeriod: 60}
	Diff(Payload{keyPeriod: 60}, observed, FieldSpec{Aliases: map[string]string{keyTokenPeriod: keyPeriod}})
	if _, ok := observed[keyPeriod]; ok {
		t.Fatal("Diff added an alias key to observed")
	}
}

func TestWriteOnlyHash(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	spec := FieldSpec{WriteOnly: []string{keyPassword, keyJWT}}
	tests := []struct {
		name  string
		a, b  Payload
		equal bool
	}{
		{"other keys ignored", Payload{keyPassword: "x", keyPolicy: "a"}, Payload{keyPassword: "x", keyPolicy: "b"}, true},
		{"value change", Payload{keyPassword: "x"}, Payload{keyPassword: "y"}, false},
		{"key order irrelevant", Payload{keyPassword: "x", keyJWT: "j"}, Payload{keyJWT: "j", keyPassword: "x"}, true},
		{"added write-only key", Payload{keyPassword: "x"}, Payload{keyPassword: "x", keyJWT: "j"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, b := WriteOnlyHash(key, tt.a, spec), WriteOnlyHash(key, tt.b, spec)
			if a == "" || b == "" {
				t.Fatal("hash is empty")
			}
			if (a == b) != tt.equal {
				t.Fatalf("hash equality = %v, want %v", a == b, tt.equal)
			}
		})
	}
	if got := WriteOnlyHash(key, Payload{keyPolicy: "a"}, spec); got != "" {
		t.Fatalf("WriteOnlyHash without write-only keys = %q, want empty", got)
	}
	if got := WriteOnlyHash(key, Payload{keyPassword: "x"}, FieldSpec{}); got != "" {
		t.Fatalf("WriteOnlyHash without a spec = %q, want empty", got)
	}
}

func TestWriteOnlyHashIsKeyed(t *testing.T) {
	spec := FieldSpec{WriteOnly: []string{keyPassword}}
	desired := Payload{keyPassword: "hunter2"}
	a := WriteOnlyHash([]byte("0123456789abcdef0123456789abcdef"), desired, spec)
	b := WriteOnlyHash([]byte("fedcba9876543210fedcba9876543210"), desired, spec)
	if a == b {
		t.Fatal("hashes under different keys are equal")
	}
	unsalted := sha256.Sum256([]byte(`{"password":"hunter2"}`))
	if a == hex.EncodeToString(unsalted[:]) {
		t.Fatal("hash is an unkeyed sha256 of the write-only inputs")
	}
}

func TestStripCreateOnly(t *testing.T) {
	desired := Payload{keyType: "kv", "description": "d"}
	got := StripCreateOnly(desired, FieldSpec{CreateOnly: []string{keyType}})
	if _, ok := got[keyType]; ok || got["description"] != "d" {
		t.Fatalf("StripCreateOnly() = %v", got)
	}
	if _, ok := desired[keyType]; !ok {
		t.Fatal("StripCreateOnly mutated its input")
	}
}

func TestDiffPartialPrefix(t *testing.T) {
	const keyMeta = "meta"
	spec := FieldSpec{Partial: []string{"role:*"}}
	desired := Payload{"role:a": map[string]any{"ttl": 1}, keyMeta: map[string]any{"a": "x"}}
	observed := Payload{"role:a": map[string]any{"ttl": 1, "server_default": true}, keyMeta: map[string]any{"a": "x", "b": "y"}}
	if got := Diff(desired, observed, spec); !slices.Equal(got, []string{keyMeta}) {
		t.Fatalf("Diff() = %v, want [meta]", got)
	}
}

func TestWritePayload(t *testing.T) {
	spec := FieldSpec{CreateOnly: []string{keyType}, ServerOwned: []string{v2Accessor}, WriteOnce: []string{v2BindPass}}
	desired := Payload{keyType: "kv", v2Accessor: "x", v2BindPass: "s", keyPolicy: testPolicyText}
	keys := func(p Payload) []string { return slices.Sorted(maps.Keys(p)) }
	if got := keys(writePayload(desired, spec, true, false)); !slices.Equal(got, []string{v2BindPass, keyPolicy, keyType}) {
		t.Fatalf("create = %v", got)
	}
	if got := keys(writePayload(desired, spec, false, false)); !slices.Equal(got, []string{keyPolicy}) {
		t.Fatalf("update = %v", got)
	}
	if got := keys(writePayload(desired, spec, false, true)); !slices.Equal(got, []string{v2BindPass, keyPolicy}) {
		t.Fatalf("update with a new nonce = %v", got)
	}
}

func TestDiffSetKeyWithScalarValue(t *testing.T) {
	spec := FieldSpec{Sets: []string{"s"}}
	if got := Diff(Payload{"s": "a"}, Payload{"s": "a"}, spec); len(got) != 0 {
		t.Errorf("a scalar under a set key equal to itself differs: %v", got)
	}
	if got := Diff(Payload{"s": "a"}, Payload{"s": "b"}, spec); !slices.Equal(got, []string{"s"}) {
		t.Errorf("different scalars under a set key: diff %v, want [s]", got)
	}
}
