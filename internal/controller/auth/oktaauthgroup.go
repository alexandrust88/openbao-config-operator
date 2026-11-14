package auth

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=oktaauthgroups,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=oktaauthgroups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=oktaauthgroups/finalizers,verbs=update

// SetupOktaAuthGroup registers the OktaAuthGroup controller with the manager.
func SetupOktaAuthGroup(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPluginKind(mgr, conns, opts, "oktaauthgroup-controller", OktaAuthGroupAdapter{},
		func() *authv1alpha1.OktaAuthGroup { return &authv1alpha1.OktaAuthGroup{} })
}

// OktaAuthGroupAdapter maps a OktaAuthGroup onto auth/<mountPath>/groups/<name> of an Okta auth method.
type OktaAuthGroupAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.OktaAuthGroup]  = OktaAuthGroupAdapter{}
	_ engine.DriftTier                             = OktaAuthGroupAdapter{}
	_ engine.Observer[*authv1alpha1.OktaAuthGroup] = OktaAuthGroupAdapter{}
)

// Path implements engine.Adapter.
func (OktaAuthGroupAdapter) Path(o *authv1alpha1.OktaAuthGroup) string {
	return "auth/" + authMount(o.Spec.MountPath, "okta") + "/groups/" + authName(o.Spec.Name, o.Name)
}

// Desired implements engine.Adapter.
func (OktaAuthGroupAdapter) Desired(_ context.Context, _ engine.Resolver, o *authv1alpha1.OktaAuthGroup) (engine.Payload, error) {
	p := engine.Payload{}
	putList(p, pluginPoliciesKey, o.Spec.Policies)
	return p, nil
}

// Fields implements engine.Adapter.
func (OktaAuthGroupAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{Sets: []string{pluginPoliciesKey}}
}

// DriftTier implements engine.DriftTier.
func (OktaAuthGroupAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// Observe implements engine.Observer. When the object is absent it checks that the auth method is enabled.
func (a OktaAuthGroupAdapter) Observe(ctx context.Context, c openbao.Client, o *authv1alpha1.OktaAuthGroup) (engine.Payload, bool, error) {
	return observeExternalAuth(ctx, c, a.Path(o), authMount(o.Spec.MountPath, "okta"), "okta")
}
