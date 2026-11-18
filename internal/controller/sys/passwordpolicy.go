package sys

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// +kubebuilder:rbac:groups=sys.obco.dev,resources=passwordpolicies,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=sys.obco.dev,resources=passwordpolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sys.obco.dev,resources=passwordpolicies/finalizers,verbs=update

// SetupPasswordPolicy registers the PasswordPolicy controller with the manager.
func SetupPasswordPolicy(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*sysv1alpha1.PasswordPolicy](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("passwordpolicy-controller"),
		conns,
		PasswordPolicyAdapter{},
		func() *sysv1alpha1.PasswordPolicy { return &sysv1alpha1.PasswordPolicy{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// PasswordPolicyAdapter maps a PasswordPolicy onto sys/policies/password/<name>.
type PasswordPolicyAdapter struct{}

var _ engine.Adapter[*sysv1alpha1.PasswordPolicy] = PasswordPolicyAdapter{}

// Path implements engine.Adapter.
func (PasswordPolicyAdapter) Path(p *sysv1alpha1.PasswordPolicy) string {
	return "sys/policies/password/" + nameOrDefault(p.Spec.Name, p.Name)
}

// Desired implements engine.Adapter. OpenBao stores the policy text verbatim.
func (PasswordPolicyAdapter) Desired(_ context.Context, _ engine.Resolver, p *sysv1alpha1.PasswordPolicy) (engine.Payload, error) {
	return engine.Payload{keyPolicy: p.Spec.Policy}, nil
}

// Fields implements engine.Adapter.
func (PasswordPolicyAdapter) Fields() engine.FieldSpec { return engine.FieldSpec{} }
