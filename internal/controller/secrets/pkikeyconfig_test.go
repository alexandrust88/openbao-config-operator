package secrets

import (
	"context"
	"maps"
	"slices"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

func newPKIKeyConfig() *secretsv1alpha1.PKIKeyConfig {
	return &secretsv1alpha1.PKIKeyConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "pki-keys", Namespace: "team"},
		Spec: secretsv1alpha1.PKIKeyConfigSpec{
			MountPath:  "pki-int",
			DefaultKey: "example-key",
		},
	}
}

func TestPKIKeyConfigPathAndDesired(t *testing.T) {
	a := PKIKeyConfigAdapter{}
	k := newPKIKeyConfig()
	if got := a.Path(k); got != "pki-int/config/keys" {
		t.Errorf("path = %q", got)
	}
	desired, err := a.Desired(context.Background(), nil, k)
	if err != nil {
		t.Fatal(err)
	}
	want := engine.Payload{"default": "example-key"}
	if !maps.Equal(desired, want) {
		t.Errorf("desired = %v, want %v", desired, want)
	}
}

func TestPKIKeyConfigSteadyStateAgainstGoldenRead(t *testing.T) {
	a := PKIKeyConfigAdapter{}
	desired, err := a.Desired(context.Background(), nil, newPKIKeyConfig())
	if err != nil {
		t.Fatal(err)
	}
	if d := engine.Diff(desired, map[string]any{"default": "example-key"}, a.Fields()); len(d) != 0 {
		t.Errorf("differing %v, want none", d)
	}
	drift := map[string]any{"default": "other-key"}
	if d := engine.Diff(desired, drift, a.Fields()); !slices.Equal(d, []string{"default"}) {
		t.Errorf("differing %v, want [default]", d)
	}
}

func TestPkiKeyConfigObserved(t *testing.T) {
	const defaultKey = "example-key"
	const keyID = "7f3a2b1c"
	tests := []struct {
		name   string
		config engine.Payload
		key    engine.Payload
		found  bool
		want   engine.Payload
	}{
		{
			name:   "name in config",
			config: engine.Payload{"default": defaultKey},
			want:   engine.Payload{"default": defaultKey},
		},
		{
			name:   "id in config resolved via key read",
			config: engine.Payload{"default": keyID},
			key:    engine.Payload{"id": keyID, "name": defaultKey},
			found:  true,
			want:   engine.Payload{"default": defaultKey},
		},
		{
			name:   "key_id in config",
			config: engine.Payload{"default": keyID},
			key:    engine.Payload{"key_id": keyID},
			found:  true,
			want:   engine.Payload{"default": defaultKey},
		},
		{
			name:   "unrelated id leaves config",
			config: engine.Payload{"default": "other-id"},
			key:    engine.Payload{"id": keyID},
			found:  true,
			want:   engine.Payload{"default": "other-id"},
		},
		{
			name:   "missing key leaves config",
			config: engine.Payload{"default": keyID},
			found:  false,
			want:   engine.Payload{"default": keyID},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pkiKeyConfigObserved(tt.config, tt.key, tt.found, defaultKey)
			if !maps.Equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
