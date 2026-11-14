package secrets

import (
	"context"
	"maps"
	"slices"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

func newKVConfig(deleteAfter intstr.IntOrString) *secretsv1alpha1.KVConfig {
	return &secretsv1alpha1.KVConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "kv", Namespace: "team"},
		Spec: secretsv1alpha1.KVConfigSpec{
			MountPath:           "team/kv",
			MaxVersions:         new(int32(5)),
			CASRequired:         new(true),
			MetadataCASRequired: new(false),
			DeleteVersionAfter:  &deleteAfter,
		},
	}
}

func TestKVConfigPathAndDesired(t *testing.T) {
	a := KVConfigAdapter{}
	c := newKVConfig(intstr.FromString("768h"))
	if got := a.Path(c); got != "team/kv/config" {
		t.Errorf("path = %q", got)
	}
	desired, err := a.Desired(context.Background(), nil, c)
	if err != nil {
		t.Fatal(err)
	}
	want := engine.Payload{"max_versions": int64(5), "cas_required": true, "metadata_cas_required": false,
		"delete_version_after": int64(768 * 3600)}
	if len(desired) != len(want) {
		t.Fatalf("desired = %v", desired)
	}
	for k, v := range want {
		if desired[k] != v {
			t.Errorf("%s = %v (%T), want %v", k, desired[k], desired[k], v)
		}
	}

	empty, _ := a.Desired(context.Background(), nil, &secretsv1alpha1.KVConfig{Spec: secretsv1alpha1.KVConfigSpec{MountPath: "kv"}})
	wantEmpty := engine.Payload{"max_versions": int64(0), "cas_required": false, "metadata_cas_required": false,
		"delete_version_after": int64(0)}
	if !maps.Equal(empty, wantEmpty) {
		t.Errorf("unset fields must carry the server defaults: %v", empty)
	}
}

func TestKVConfigDurationForms(t *testing.T) {
	for _, tt := range []struct {
		in   intstr.IntOrString
		want int64
	}{
		{intstr.FromInt32(3600), 3600},
		{intstr.FromInt32(-1), -1},
		{intstr.FromString("-1"), -1},
		{intstr.FromString("90"), 90},
		{intstr.FromString("1h30m"), 5400},
		{intstr.FromInt32(0), 0},
	} {
		got, err := durationSeconds(tt.in)
		if err != nil || got != tt.want {
			t.Errorf("%s: got %d err %v, want %d", tt.in.String(), got, err, tt.want)
		}
	}
	if _, err := durationSeconds(intstr.FromString("soon")); err == nil {
		t.Error("an invalid duration must fail")
	}
}

func TestKVConfigSteadyStateAgainstGoldenRead(t *testing.T) {
	a := KVConfigAdapter{}
	tests := []struct {
		name        string
		deleteAfter intstr.IntOrString
		golden      map[string]any
		differing   []string
	}{
		{
			// Captured from bao server -dev 2.7.2 after writing max_versions=5 cas_required=true delete_version_after=768h.
			name: "duration", deleteAfter: intstr.FromString("768h"),
			golden: map[string]any{"cas_required": true, "delete_version_after": "768h0m0s", "max_versions": 5.0, "metadata_cas_required": false},
		},
		{
			name: "disabled", deleteAfter: intstr.FromInt32(-1),
			golden: map[string]any{"cas_required": true, "delete_version_after": "-1s", "max_versions": 5.0, "metadata_cas_required": false},
		},
		{
			name: "drift", deleteAfter: intstr.FromString("768h"),
			golden:    map[string]any{"cas_required": false, "delete_version_after": "0s", "max_versions": 10.0, "metadata_cas_required": false},
			differing: []string{"cas_required", "delete_version_after", "max_versions"},
		},
	}
	for _, tt := range tests {
		desired, err := a.Desired(context.Background(), nil, newKVConfig(tt.deleteAfter))
		if err != nil {
			t.Fatal(err)
		}
		if d := engine.Diff(desired, tt.golden, a.Fields()); !slices.Equal(d, tt.differing) {
			t.Errorf("%s: differing %v, want %v", tt.name, d, tt.differing)
		}
	}
}

// TestSecretsConfigKindsAreSingletons pins the engine config endpoints that always read back with defaults, so the
// engine adopts them whatever the creationPolicy.
func TestSecretsConfigKindsAreSingletons(t *testing.T) {
	for _, s := range []engine.Singleton{KVConfigAdapter{}, PKIConfigAdapter{}, PKIACMEConfigAdapter{}, PKIAutoTidyAdapter{}} {
		if !s.Singleton() {
			t.Errorf("%T must be a singleton", s)
		}
	}
}
