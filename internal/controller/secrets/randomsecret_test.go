package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

func newRandomSecret() *secretsv1alpha1.RandomSecret {
	return &secretsv1alpha1.RandomSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "db-password", Namespace: "team"},
		Spec: secretsv1alpha1.RandomSecretSpec{
			MountPath: "kv",
			Path:      "team/db",
			KVVersion: 2,
			Key:       "password",
			Generator: &secretsv1alpha1.RandomSecretGenerator{Length: 24, Charset: "abcdef0123456789"},
		},
	}
}

func kvStored(data map[string]any, version int) map[string]map[string]any {
	return map[string]map[string]any{"kv/data/team/db": {
		"data":     data,
		"metadata": map[string]any{"version": json.Number(strconv.Itoa(version))},
	}}
}

func TestRandomSecretDesiredAndHash(t *testing.T) {
	a := RandomSecretAdapter{}
	s := newRandomSecret()
	empty, _ := a.Desired(context.Background(), nil, s)
	if len(empty) != 0 || engine.WriteOnlyHash(testHashKey, empty, a.Fields()) != "" {
		t.Errorf("no nonce and no output: desired %v", empty)
	}
	s.Spec.RefreshNonce = "1"
	withNonce, _ := a.Desired(context.Background(), nil, s)
	s.Spec.WriteSecretRef = &commonv1alpha1.LocalObjectReference{Name: "db-password"}
	withOutput, _ := a.Desired(context.Background(), nil, s)
	h1, h2 := engine.WriteOnlyHash(testHashKey, withNonce, a.Fields()), engine.WriteOnlyHash(testHashKey, withOutput, a.Fields())
	if h1 == "" || h1 == h2 {
		t.Errorf("hash must change with refreshNonce and writeSecretRef: %q %q", h1, h2)
	}
	s.Spec.Generator.Length = 64
	if again, _ := a.Desired(context.Background(), nil, s); engine.WriteOnlyHash(testHashKey, again, a.Fields()) != h2 {
		t.Error("a generator change must not change the hash")
	}
	if d := engine.Diff(withOutput, engine.Payload{}, a.Fields()); len(d) != 0 {
		t.Errorf("pseudo keys must never be compared: %v", d)
	}
}

func TestRandomSecretObserve(t *testing.T) {
	a := RandomSecretAdapter{}
	tests := []struct {
		name  string
		reads map[string]map[string]any
		found bool
	}{
		{name: "missing path", found: false},
		{name: "key absent", reads: kvStored(map[string]any{"user": "app"}, 1), found: false},
		{name: "key empty", reads: kvStored(map[string]any{"password": ""}, 1), found: false},
		{name: "key present", reads: kvStored(map[string]any{"password": "s3cr3t"}, 1), found: true},
	}
	for _, tt := range tests {
		observed, found, err := a.Observe(context.Background(), newKVFakeClient(tt.reads), newRandomSecret())
		if err != nil || found != tt.found {
			t.Errorf("%s: found=%v err=%v, want found=%v", tt.name, found, err, tt.found)
		}
		if len(observed) != 0 {
			t.Errorf("%s: observe must not return data: %v", tt.name, observed)
		}
	}
}

func TestRandomSecretWriteGeneratesOnce(t *testing.T) {
	a := RandomSecretAdapter{}
	tests := []struct {
		name       string
		stored     map[string]any
		acquired   commonv1alpha1.AcquiredState
		specNonce  string
		statNonce  string
		regenerate bool
	}{
		{name: "absent key", stored: map[string]any{"user": "app"}, regenerate: true},
		{name: "adoption keeps value", stored: map[string]any{"password": "keep-me"}, specNonce: "1"},
		{name: "output change keeps value", stored: map[string]any{"password": "keep-me"},
			acquired: commonv1alpha1.AcquiredCreated, specNonce: "1", statNonce: "1"},
		{name: "nonce change regenerates", stored: map[string]any{"password": "keep-me"},
			acquired: commonv1alpha1.AcquiredCreated, specNonce: "2", statNonce: "1", regenerate: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newRandomSecret()
			s.Spec.RefreshNonce = tt.specNonce
			s.Spec.WriteSecretRef = &commonv1alpha1.LocalObjectReference{Name: "db-password"}
			s.Status.Acquired = tt.acquired
			s.Status.RefreshNonce = tt.statNonce
			c := newKVFakeClient(kvStored(tt.stored, 4))
			resp, err := a.Write(context.Background(), c, s, nil, false)
			if !tt.regenerate {
				if !errors.Is(err, engine.ErrNothingWritten) || len(c.writes) != 0 {
					t.Fatalf("err = %v, writes %v, want ErrNothingWritten and no write", err, len(c.writes))
				}
				refreshed, err := a.RefreshOutputs(context.Background(), c, s, nil)
				if err != nil || refreshed["password"] != "keep-me" {
					t.Errorf("refreshed output = %v, %v, want the stored value", refreshed["password"] != nil, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			body, wrote := c.writes["kv/data/team/db"]
			if !wrote {
				t.Fatal("no write, want a generated value")
			}
			value := resp["password"].(string)
			data := body["data"].(map[string]any)
			if data["password"] != value || len(value) != 24 || strings.Trim(value, "abcdef0123456789") != "" {
				t.Errorf("generated value not stored or not matching the generator")
			}
			for k, v := range tt.stored {
				if k != "password" && data[k] != v {
					t.Errorf("foreign key %s not kept", k)
				}
			}
			if cas := body["options"].(map[string]any)["cas"]; cas != int64(4) {
				t.Errorf("cas = %v, want 4", cas)
			}
			if s.Status.RefreshNonce != tt.specNonce {
				t.Errorf("status nonce = %q", s.Status.RefreshNonce)
			}
		})
	}
}

func TestRandomSecretWritePasswordPolicy(t *testing.T) {
	s := newRandomSecret()
	s.Spec.Generator = nil
	s.Spec.PasswordPolicyName = "strong"
	c := newKVFakeClient(map[string]map[string]any{"sys/policies/password/strong/generate": {"password": "from-policy"}})
	resp, err := RandomSecretAdapter{}.Write(context.Background(), c, s, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if resp != nil {
		t.Errorf("no writeSecretRef: response must be empty")
	}
	if c.writes["kv/data/team/db"]["data"].(map[string]any)["password"] != "from-policy" {
		t.Error("policy value not stored")
	}

	s.Spec.PasswordPolicyName = "missing"
	_, err = RandomSecretAdapter{}.Write(context.Background(), newKVFakeClient(nil), s, nil, true)
	if !errors.Is(err, engine.ErrReferenceNotReady) {
		t.Errorf("missing policy: err = %v", err)
	}
}

func TestRandomSecretOutputs(t *testing.T) {
	a := RandomSecretAdapter{}
	s := newRandomSecret()
	if _, _, ok := a.Outputs(s, engine.Payload{"password": "x"}); ok {
		t.Error("no writeSecretRef must not write a Secret")
	}
	s.Spec.WriteSecretRef = &commonv1alpha1.LocalObjectReference{Name: "out"}
	name, data, ok := a.Outputs(s, engine.Payload{"password": "x"})
	if !ok || name != "out" || string(data["password"]) != "x" || len(data) != 1 {
		t.Errorf("outputs = %s %v %v", name, data, ok)
	}
}

func TestRandomSecretDeleteOwnedKeyOnly(t *testing.T) {
	a := RandomSecretAdapter{}
	c := newKVFakeClient(kvStored(map[string]any{"password": "x", "user": "app"}, 2))
	if err := a.Delete(context.Background(), c, newRandomSecret()); err != nil {
		t.Fatal(err)
	}
	if data := c.writes["kv/data/team/db"]["data"].(map[string]any); len(data) != 1 || data["user"] != "app" {
		t.Errorf("remaining data = %v", data)
	}

	c = newKVFakeClient(kvStored(map[string]any{"password": "x"}, 2))
	if err := a.Delete(context.Background(), c, newRandomSecret()); err != nil {
		t.Fatal(err)
	}
	if data := c.writes["kv/data/team/db"]["data"].(map[string]any); len(data) != 0 || slices.Contains(c.deletes, "kv/metadata/team/db") {
		t.Errorf("remaining data = %v, deletes = %v, want an empty version and the history kept", data, c.deletes)
	}
}

func TestRandomString(t *testing.T) {
	value, err := randomString(1000, "aab")
	if err != nil {
		t.Fatal(err)
	}
	if len(value) != 1000 || strings.Trim(value, "ab") != "" || !strings.Contains(value, "a") || !strings.Contains(value, "b") {
		t.Errorf("unexpected distribution or charset")
	}
	if _, err := randomString(8, "aaaa"); err == nil {
		t.Error("a single distinct character must be refused")
	}
	unicode, _ := randomString(16, "äöüßéèêëàâ")
	if len([]rune(unicode)) != 16 {
		t.Errorf("multi-byte charset length = %d runes", len([]rune(unicode)))
	}
}
