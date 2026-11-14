package auth

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=oktaauthusers,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=oktaauthusers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=oktaauthusers/finalizers,verbs=update

// SetupOktaAuthUser registers the OktaAuthUser controller with the manager.
func SetupOktaAuthUser(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPluginKind(mgr, conns, opts, "oktaauthuser-controller", OktaAuthUserAdapter{},
		func() *authv1alpha1.OktaAuthUser { return &authv1alpha1.OktaAuthUser{} })
}

// OktaAuthUserAdapter maps a OktaAuthUser onto auth/<mountPath>/users/<name> of an Okta auth method.
type OktaAuthUserAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.OktaAuthUser]  = OktaAuthUserAdapter{}
	_ engine.DriftTier                            = OktaAuthUserAdapter{}
	_ engine.Observer[*authv1alpha1.OktaAuthUser] = OktaAuthUserAdapter{}
)

// Path implements engine.Adapter.
func (OktaAuthUserAdapter) Path(o *authv1alpha1.OktaAuthUser) string {
	return "auth/" + authMount(o.Spec.MountPath, "okta") + "/users/" + authName(o.Spec.Name, o.Name)
}

// Desired implements engine.Adapter.
func (OktaAuthUserAdapter) Desired(_ context.Context, _ engine.Resolver, o *authv1alpha1.OktaAuthUser) (engine.Payload, error) {
	p := engine.Payload{}
	putList(p, "groups", o.Spec.Groups)
	putList(p, pluginPoliciesKey, o.Spec.Policies)
	return p, nil
}

// Fields implements engine.Adapter.
func (OktaAuthUserAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{Sets: []string{"groups", pluginPoliciesKey}}
}

// DriftTier implements engine.DriftTier.
func (OktaAuthUserAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// Observe implements engine.Observer. When the object is absent it checks that the auth method is enabled.
func (a OktaAuthUserAdapter) Observe(ctx context.Context, c openbao.Client, o *authv1alpha1.OktaAuthUser) (engine.Payload, bool, error) {
	return observeExternalAuth(ctx, c, a.Path(o), authMount(o.Spec.MountPath, "okta"), "okta")
}
