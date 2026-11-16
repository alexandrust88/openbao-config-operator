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

func newTransitKeyConfig(disableUpsert *bool) *secretsv1alpha1.TransitKeyConfig {
	return &secretsv1alpha1.TransitKeyConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "transit-keys", Namespace: "team"},
		Spec: secretsv1alpha1.TransitKeyConfigSpec{
			MountPath:     "transit",
			DisableUpsert: disableUpsert,
		},
	}
}

func TestTransitKeyConfigPathAndDesired(t *testing.T) {
	a := TransitKeyConfigAdapter{}
	disable := true
	c := newTransitKeyConfig(&disable)
	if got := a.Path(c); got != "transit/config/keys" {
		t.Errorf("path = %q", got)
	}
	desired, err := a.Desired(context.Background(), nil, c)
	if err != nil {
		t.Fatal(err)
	}
	want := engine.Payload{"disable_upsert": true}
	if len(desired) != len(want) {
		t.Fatalf("desired = %v", desired)
	}
	for k, v := range want {
		if desired[k] != v {
			t.Errorf("%s = %v (%T), want %v", k, desired[k], desired[k], v)
		}
	}

	empty, err := a.Desired(context.Background(), nil, &secretsv1alpha1.TransitKeyConfig{Spec: secretsv1alpha1.TransitKeyConfigSpec{MountPath: "transit"}})
	if err != nil {
		t.Fatal(err)
	}
	wantEmpty := engine.Payload{"disable_upsert": false}
	if !maps.Equal(empty, wantEmpty) {
		t.Errorf("unset fields must carry the server defaults: %v", empty)
	}
	if !a.Singleton() {
		t.Error("TransitKeyConfig must be a singleton")
	}
}

func TestTransitKeyConfigSteadyStateAgainstGoldenRead(t *testing.T) {
	a := TransitKeyConfigAdapter{}
	tests := []struct {
		name          string
		disableUpsert *bool
		golden        map[string]any
		differing     []string
	}{
		{
			name:          "default",
			disableUpsert: nil,
			golden:        map[string]any{"disable_upsert": false},
		},
		{
			name:          "drift",
			disableUpsert: nil,
			golden:        map[string]any{"disable_upsert": true},
			differing:     []string{"disable_upsert"},
		},
	}
	for _, tt := range tests {
		desired, err := a.Desired(context.Background(), nil, newTransitKeyConfig(tt.disableUpsert))
		if err != nil {
			t.Fatal(err)
		}
		if d := engine.Diff(desired, tt.golden, a.Fields()); !slices.Equal(d, tt.differing) {
			t.Errorf("%s: differing %v, want %v", tt.name, d, tt.differing)
		}
	}
}
