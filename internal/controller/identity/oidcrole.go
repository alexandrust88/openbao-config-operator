package identity

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	identityv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/identity/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=identity.obco.dev,resources=oidcroles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=identity.obco.dev,resources=oidcroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=identity.obco.dev,resources=oidcroles/finalizers,verbs=update

// SetupOIDCRole registers the OIDCRole controller with the manager.
func SetupOIDCRole(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*identityv1alpha1.OIDCRole](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("oidcrole-controller"),
		conns,
		OIDCRoleAdapter{},
		func() *identityv1alpha1.OIDCRole { return &identityv1alpha1.OIDCRole{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// OIDCRoleAdapter maps an OIDCRole onto identity/oidc/role/<name>.
type OIDCRoleAdapter struct{}

var (
	_ engine.Adapter[*identityv1alpha1.OIDCRole]      = OIDCRoleAdapter{}
	_ engine.StatusWriter[*identityv1alpha1.OIDCRole] = OIDCRoleAdapter{}
)

// Path implements engine.Adapter.
func (OIDCRoleAdapter) Path(r *identityv1alpha1.OIDCRole) string {
	return "identity/oidc/role/" + oidcName(r.Spec.Name, r.Name)
}

// Desired implements engine.Adapter.
func (OIDCRoleAdapter) Desired(_ context.Context, _ engine.Resolver, r *identityv1alpha1.OIDCRole) (engine.Payload, error) {
	p := engine.Payload{"key": r.Spec.Key, "template": r.Spec.Template, "ttl": secondsOr(r.Spec.TTL, oidcDefaultTTL)}
	if r.Spec.ClientID != "" {
		p["client_id"] = r.Spec.ClientID
	}
	return p, nil
}

// Fields implements engine.Adapter.
func (OIDCRoleAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{Durations: []string{"ttl"}}
}

// WriteStatus implements engine.StatusWriter: status.id is the role's client ID.
func (OIDCRoleAdapter) WriteStatus(_ context.Context, _ openbao.Client, r *identityv1alpha1.OIDCRole, observed engine.Payload) error {
	if id, ok := observed["client_id"].(string); ok {
		r.Status.ID = id
	}
	return nil
}
