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

func newTransitCacheConfig(size *int32) *secretsv1alpha1.TransitCacheConfig {
	return &secretsv1alpha1.TransitCacheConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "transit-cache", Namespace: "team"},
		Spec: secretsv1alpha1.TransitCacheConfigSpec{
			MountPath: "transit",
			Size:      size,
		},
	}
}

func TestTransitCacheConfigPathAndDesired(t *testing.T) {
	a := TransitCacheConfigAdapter{}
	size := int32(100)
	c := newTransitCacheConfig(&size)
	if got := a.Path(c); got != "transit/cache-config" {
		t.Errorf("path = %q", got)
	}
	desired, err := a.Desired(context.Background(), nil, c)
	if err != nil {
		t.Fatal(err)
	}
	want := engine.Payload{"size": int64(100)}
	if len(desired) != len(want) {
		t.Fatalf("desired = %v", desired)
	}
	for k, v := range want {
		if desired[k] != v {
			t.Errorf("%s = %v (%T), want %v", k, desired[k], desired[k], v)
		}
	}

	empty, err := a.Desired(context.Background(), nil, &secretsv1alpha1.TransitCacheConfig{Spec: secretsv1alpha1.TransitCacheConfigSpec{MountPath: "transit"}})
	if err != nil {
		t.Fatal(err)
	}
	wantEmpty := engine.Payload{"size": int64(0)}
	if !maps.Equal(empty, wantEmpty) {
		t.Errorf("unset fields must carry the server defaults: %v", empty)
	}
}

func TestTransitCacheConfigSteadyStateAgainstGoldenRead(t *testing.T) {
	a := TransitCacheConfigAdapter{}
	tests := []struct {
		name      string
		size      *int32
		golden    map[string]any
		differing []string
	}{
		{
			name:   "default",
			size:   nil,
			golden: map[string]any{"size": 0.0},
		},
		{
			name:      "drift",
			size:      nil,
			golden:    map[string]any{"size": 1000.0},
			differing: []string{"size"},
		},
	}
	for _, tt := range tests {
		desired, err := a.Desired(context.Background(), nil, newTransitCacheConfig(tt.size))
		if err != nil {
			t.Fatal(err)
		}
		if d := engine.Diff(desired, tt.golden, a.Fields()); !slices.Equal(d, tt.differing) {
			t.Errorf("%s: differing %v, want %v", tt.name, d, tt.differing)
		}
	}
}
