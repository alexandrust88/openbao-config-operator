package secrets

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// +kubebuilder:rbac:groups=secrets.obco.dev,resources=pkicelroles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=pkicelroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=pkicelroles/finalizers,verbs=update

// SetupPKICELRole registers the PKICELRole controller with the manager.
func SetupPKICELRole(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*secretsv1alpha1.PKICELRole](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("pkicelrole-controller"),
		conns,
		PKICELRoleAdapter{},
		func() *secretsv1alpha1.PKICELRole { return &secretsv1alpha1.PKICELRole{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// PKICELRoleAdapter maps a PKICELRole onto <mount>/cel/roles/<name>.
type PKICELRoleAdapter struct{}

var _ engine.Adapter[*secretsv1alpha1.PKICELRole] = PKICELRoleAdapter{}

func pkiCELRoleName(specName, metaName string) string {
	if specName != "" {
		return specName
	}
	return metaName
}

// Path implements engine.Adapter.
func (PKICELRoleAdapter) Path(r *secretsv1alpha1.PKICELRole) string {
	return pkiMount(r.Spec.MountPath) + "/cel/roles/" + pkiCELRoleName(r.Spec.Name, r.Name)
}

// Desired implements engine.Adapter.
func (PKICELRoleAdapter) Desired(_ context.Context, _ engine.Resolver, r *secretsv1alpha1.PKICELRole) (engine.Payload, error) {
	variables := make([]any, len(r.Spec.CELProgram.Variables))
	for i, v := range r.Spec.CELProgram.Variables {
		variables[i] = map[string]any{"name": v.Name, "expression": v.Expression}
	}
	return engine.Payload{
		"cel_program": map[string]any{
			"expression": r.Spec.CELProgram.Expression,
			"variables":  variables,
		},
	}, nil
}

// Fields implements engine.Adapter.
func (PKICELRoleAdapter) Fields() engine.FieldSpec { return engine.FieldSpec{} }
