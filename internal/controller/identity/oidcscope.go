package identity

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	identityv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/identity/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// +kubebuilder:rbac:groups=identity.obco.dev,resources=oidcscopes,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=identity.obco.dev,resources=oidcscopes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=identity.obco.dev,resources=oidcscopes/finalizers,verbs=update

// SetupOIDCScope registers the OIDCScope controller with the manager.
func SetupOIDCScope(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*identityv1alpha1.OIDCScope](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("oidcscope-controller"),
		conns,
		OIDCScopeAdapter{},
		func() *identityv1alpha1.OIDCScope { return &identityv1alpha1.OIDCScope{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// OIDCScopeAdapter maps an OIDCScope onto identity/oidc/scope/<name>.
type OIDCScopeAdapter struct{}

var _ engine.Adapter[*identityv1alpha1.OIDCScope] = OIDCScopeAdapter{}

// Path implements engine.Adapter.
func (OIDCScopeAdapter) Path(s *identityv1alpha1.OIDCScope) string {
	return "identity/oidc/scope/" + oidcName(s.Spec.Name, s.Name)
}

// Desired implements engine.Adapter.
func (OIDCScopeAdapter) Desired(_ context.Context, _ engine.Resolver, s *identityv1alpha1.OIDCScope) (engine.Payload, error) {
	return engine.Payload{"template": s.Spec.Template, "description": s.Spec.Description}, nil
}

// Fields implements engine.Adapter.
func (OIDCScopeAdapter) Fields() engine.FieldSpec { return engine.FieldSpec{} }
