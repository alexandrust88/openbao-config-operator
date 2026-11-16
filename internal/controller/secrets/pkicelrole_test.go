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

func newPKICELRole() *secretsv1alpha1.PKICELRole {
	return &secretsv1alpha1.PKICELRole{
		ObjectMeta: metav1.ObjectMeta{Name: "strict-cn", Namespace: "team"},
		Spec: secretsv1alpha1.PKICELRoleSpec{
			MountPath: "pki-int",
			Name:      "cel-policy",
			CELProgram: secretsv1alpha1.PKICELProgram{
				Expression: `request.common_name == "example.com"`,
				Variables: []secretsv1alpha1.PKICELVariable{
					{Name: "is_example", Expression: `request.common_name.endsWith(".example.com")`},
				},
			},
		},
	}
}

func TestPKICELRolePathAndDesired(t *testing.T) {
	a := PKICELRoleAdapter{}
	r := newPKICELRole()
	if got := a.Path(r); got != "pki-int/cel/roles/cel-policy" {
		t.Errorf("path = %q", got)
	}
	desired, err := a.Desired(context.Background(), nil, r)
	if err != nil {
		t.Fatal(err)
	}
	want := engine.Payload{
		"cel_program": map[string]any{
			"expression": `request.common_name == "example.com"`,
			"variables": []any{
				map[string]any{"name": "is_example", "expression": `request.common_name.endsWith(".example.com")`},
			},
		},
	}
	if d := engine.Diff(desired, want, a.Fields()); len(d) != 0 {
		t.Errorf("desired differs on %v: %v", d, desired)
	}

	defaultMount := &secretsv1alpha1.PKICELRole{
		ObjectMeta: metav1.ObjectMeta{Name: "strict-cn"},
		Spec: secretsv1alpha1.PKICELRoleSpec{
			CELProgram: secretsv1alpha1.PKICELProgram{Expression: "true"},
		},
	}
	if got := a.Path(defaultMount); got != "pki/cel/roles/strict-cn" {
		t.Errorf("default path = %q", got)
	}
	emptyVars, err := a.Desired(context.Background(), nil, defaultMount)
	if err != nil {
		t.Fatal(err)
	}
	cel, _ := emptyVars["cel_program"].(map[string]any)
	vars, _ := cel["variables"].([]any)
	if vars == nil || len(vars) != 0 {
		t.Errorf("variables = %v, want empty slice", vars)
	}
}

func TestPKICELRoleSteadyStateAgainstGoldenRead(t *testing.T) {
	a := PKICELRoleAdapter{}
	desired, err := a.Desired(context.Background(), nil, &secretsv1alpha1.PKICELRole{
		Spec: secretsv1alpha1.PKICELRoleSpec{
			CELProgram: secretsv1alpha1.PKICELProgram{
				Expression: `request.common_name == "example.com"`,
				Variables: []secretsv1alpha1.PKICELVariable{
					{Name: "is_example", Expression: `request.common_name.endsWith(".example.com")`},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	golden := map[string]any{
		"cel_program": map[string]any{
			"expression": `request.common_name == "example.com"`,
			"variables": []any{
				map[string]any{"name": "is_example", "expression": `request.common_name.endsWith(".example.com")`},
			},
		},
	}
	if d := engine.Diff(desired, golden, a.Fields()); len(d) != 0 {
		t.Errorf("differing %v, want none", d)
	}

	withExtra := maps.Clone(golden)
	if d := engine.Diff(desired, withExtra, a.Fields()); len(d) != 0 {
		t.Errorf("identical payloads must not differ: %v", d)
	}
	extraVar := maps.Clone(golden)
	prog := extraVar["cel_program"].(map[string]any)
	prog["variables"] = append(prog["variables"].([]any), map[string]any{"name": "drift", "expression": "false"})
	if d := engine.Diff(desired, extraVar, a.Fields()); !slices.Equal(d, []string{"cel_program"}) {
		t.Errorf("differing %v, want [cel_program]", d)
	}
}
