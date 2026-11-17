package main

import (
	"encoding/json"
	"os/exec"
	"testing"

	"github.com/google/cel-go/cel"
)

func TestFluxHealthExpressions(t *testing.T) {
	script := "import flux,json; h=type('H',(),{'operator_values':{}})(); " +
		"failed=flux.manifests(h)[0][2]['spec']['healthCheckExprs'][0]['failed']; " +
		"print(json.dumps({'current':flux.health_expression(True),'failed':failed}))"
	output, err := exec.Command("python3", "-c", script).Output()
	if err != nil {
		t.Fatal(err)
	}
	var expressions map[string]string
	if err := json.Unmarshal(output, &expressions); err != nil {
		t.Fatal(err)
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
