package connection

import (
	"encoding/base64"
	"encoding/json"
	"slices"
	"testing"
	"unicode/utf8"
)

func fakeJWT(payload []byte) string {
	return "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

// FuzzJWTAudiences checks that the aud claim read before a token file leaves the pod is the one the token carries,
// as a string or a list of strings, so an API server audience cannot slip past notAPIServerAudience.
func FuzzJWTAudiences(f *testing.F) {
	for _, aud := range builtinAPIServerAudiences {
		f.Add(aud, "openbao", false)
		f.Add(aud, "", true)
	}
	f.Add("", "", false)
	f.Add("a\"b", " ", true)
	f.Fuzz(func(t *testing.T, a, b string, list bool) {
		if !utf8.ValidString(a) || !utf8.ValidString(b) {
			return
		}
		var claim any = a
		want := []string{a}
		if list {
			claim = []string{a, b}
			want = []string{a, b}
		}
		payload, err := json.Marshal(map[string]any{"aud": claim, "sub": "system:serviceaccount:ns:sa"})
		if err != nil {
			t.Fatal(err)
		}
		got, ok := jwtAudiences(fakeJWT(payload))
		if !slices.Equal(got, want) || ok != (list || a != "") {
			t.Fatalf("jwtAudiences(%s) = %q, %v; want %q", payload, got, ok, want)
		}
	})
}

// FuzzJWTAudiencesRaw feeds arbitrary claim sets and compares jwtAudiences with a case-sensitive read of "aud", the
// way JWT validators read it.
func FuzzJWTAudiencesRaw(f *testing.F) {
	for _, seed := range []string{
		`{"aud":"https://kubernetes.default.svc"}`,
		`{"aud":["rke2","openbao"]}`,
		`{"aud":["x",1]}`,
		`{"aud":null}`,
		`{"aud":"a","aud":"k3s"}`,
		`{}`,
		`[]`,
		`not json`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, payload []byte) {
		got, ok := jwtAudiences(fakeJWT(payload))
		var claims map[string]json.RawMessage
		if json.Unmarshal(payload, &claims) != nil {
			return
		}
		var want []string
		var one string
		var wantOK bool
		if err := json.Unmarshal(claims["aud"], &one); err == nil {
			want, wantOK = []string{one}, one != ""
		} else if json.Unmarshal(claims["aud"], &want) == nil {
			wantOK = len(want) > 0
		} else {
			return
		}
		if !slices.Equal(got, want) || ok != wantOK {
			t.Fatalf("jwtAudiences(%s) = %q, %v; want %q, %v", payload, got, ok, want, wantOK)
		}
	})
}
