//go:build integration

package livekit

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestStateRequiresCurrentConditions(t *testing.T) {
	for _, tc := range []struct {
		name      string
		readyGen  int64
		syncedGen int64
		synced    string
		observe   bool
		core      bool
		want      bool
	}{
		{name: "current", readyGen: 3, syncedGen: 3, synced: "True", want: true},
		{name: "missing ready generation", readyGen: 0, syncedGen: 3, synced: "True"},
		{name: "stale ready", readyGen: 2, syncedGen: 3, synced: "True"},
		{name: "stale synced", readyGen: 3, syncedGen: 2, synced: "True"},
		{name: "not synced", readyGen: 3, syncedGen: 3, synced: "False"},
		{name: "observe may report drift", readyGen: 3, syncedGen: 3, synced: "False", observe: true, want: true},
		{name: "core has only ready", readyGen: 3, core: true, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := map[string]any{}
			if !tc.core {
				spec["connectionRef"] = map[string]any{"name": "bao"}
			}
			if tc.observe {
				spec["managementPolicy"] = "Observe"
			}
			obj := &unstructured.Unstructured{Object: map[string]any{
				"metadata": map[string]any{"generation": int64(3)}, "spec": spec,
				"status": map[string]any{"observedGeneration": int64(3), "conditions": []any{
					map[string]any{"type": "Ready", "status": "True", "observedGeneration": tc.readyGen},
					map[string]any{"type": "Synced", "status": tc.synced, "observedGeneration": tc.syncedGen},
				}},
			}}
			if got := stateOf(obj).ready; got != tc.want {
				t.Errorf("ready=%v, want %v", got, tc.want)
			}
		})
	}
}
