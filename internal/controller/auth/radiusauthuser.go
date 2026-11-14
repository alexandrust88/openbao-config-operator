package auth

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=radiusauthusers,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=radiusauthusers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=radiusauthusers/finalizers,verbs=update

// SetupRADIUSAuthUser registers the RADIUSAuthUser controller with the manager.
func SetupRADIUSAuthUser(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPluginKind(mgr, conns, opts, "radiusauthuser-controller", RADIUSAuthUserAdapter{},
		func() *authv1alpha1.RADIUSAuthUser { return &authv1alpha1.RADIUSAuthUser{} })
}

// RADIUSAuthUserAdapter maps a RADIUSAuthUser onto auth/<mountPath>/users/<name> of a RADIUS auth method.
type RADIUSAuthUserAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.RADIUSAuthUser]  = RADIUSAuthUserAdapter{}
	_ engine.DriftTier                              = RADIUSAuthUserAdapter{}
	_ engine.Observer[*authv1alpha1.RADIUSAuthUser] = RADIUSAuthUserAdapter{}
)

// Path implements engine.Adapter.
func (RADIUSAuthUserAdapter) Path(o *authv1alpha1.RADIUSAuthUser) string {
	return "auth/" + authMount(o.Spec.MountPath, "radius") + "/users/" + authName(o.Spec.Name, o.Name)
}

// Desired implements engine.Adapter.
func (RADIUSAuthUserAdapter) Desired(_ context.Context, _ engine.Resolver, o *authv1alpha1.RADIUSAuthUser) (engine.Payload, error) {
	p := engine.Payload{}
	putList(p, pluginPoliciesKey, o.Spec.Policies)
	return p, nil
}

// Fields implements engine.Adapter.
func (RADIUSAuthUserAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{Sets: []string{pluginPoliciesKey}}
}

// DriftTier implements engine.DriftTier.
func (RADIUSAuthUserAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// Observe implements engine.Observer. When the object is absent it checks that the auth method is enabled.
func (a RADIUSAuthUserAdapter) Observe(ctx context.Context, c openbao.Client, o *authv1alpha1.RADIUSAuthUser) (engine.Payload, bool, error) {
	return observeExternalAuth(ctx, c, a.Path(o), authMount(o.Spec.MountPath, "radius"), "radius")
}
