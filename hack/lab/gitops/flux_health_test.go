package main

import (
	"os"
	"testing"

	"github.com/google/cel-go/cel"
	"sigs.k8s.io/yaml"
)

func TestFluxHealthExpressions(t *testing.T) {
	data, err := os.ReadFile("../../../gitops/flux/bootstrap/kustomizations.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var roots struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Health []map[string]string `json:"healthCheckExprs"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := yaml.Unmarshal(data, &roots); err != nil {
		t.Fatal(err)
	}
	var expressions map[string]string
	for _, root := range roots.Items {
		if root.Metadata.Name == "config" && len(root.Spec.Health) > 0 {
			expressions = map[string]string{"current": root.Spec.Health[0]["current"], "failed": root.Spec.Health[0]["failed"]}
		}
	}
	if len(expressions) != 2 {
		t.Fatal("Missing checked-in Flux health expressions")
	}
	env, err := cel.NewEnv(cel.Variable("status", cel.DynType), cel.Variable("metadata", cel.DynType))
	if err != nil {
		t.Fatal(err)
	}
	for name, expression := range expressions {
		ast, issues := env.Compile(expression)
		if issues.Err() != nil {
			t.Fatalf("%s: %v", name, issues.Err())
		}
		program, err := env.Program(ast)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			name                  string
			generation            int
			ready, synced, reason string
			current, failed       bool
		}{
			{"healthy", 2, "True", "True", "Reconciled", true, false},
			{"stale", 1, "True", "True", "Reconciled", false, false},
			{"unsynced", 2, "True", "False", "Reconciled", false, false},
			{"pending", 2, "False", "False", "ReferenceNotReady", false, false},
			{"rejected", 2, "False", "False", "ReconcileError", false, true},
			{"update-rejected", 2, "True", "False", "ReconcileError", false, true},
		} {
			conditions := []map[string]any{
				{"type": "Ready", "status": tc.ready, "observedGeneration": tc.generation, "reason": tc.reason},
				{"type": "Synced", "status": tc.synced, "observedGeneration": tc.generation, "reason": tc.reason},
			}
			result, _, err := program.Eval(map[string]any{
				"metadata": map[string]any{"generation": 2},
				"status":   map[string]any{"conditions": conditions},
			})
			if err != nil {
				t.Fatal(err)
			}
			want := tc.current
			if name == "failed" {
				want = tc.failed
			}
			if result.Value() != want {
				t.Errorf("%s/%s: got %v want %v", name, tc.name, result, want)
			}
		}
	}
}
