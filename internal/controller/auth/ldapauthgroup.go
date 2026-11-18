package auth

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=auth.obco.dev,resources=ldapauthgroups,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=ldapauthgroups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=ldapauthgroups/finalizers,verbs=update

// SetupLDAPAuthGroup registers the LDAPAuthGroup controller with the manager.
func SetupLDAPAuthGroup(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPluginKind(mgr, conns, opts, "ldapauthgroup-controller", LDAPAuthGroupAdapter{},
		func() *authv1alpha1.LDAPAuthGroup { return &authv1alpha1.LDAPAuthGroup{} })
}

// LDAPAuthGroupAdapter maps a LDAPAuthGroup onto auth/<mountPath>/groups/<name> of an LDAP auth method.
type LDAPAuthGroupAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.LDAPAuthGroup]  = LDAPAuthGroupAdapter{}
	_ engine.DriftTier                             = LDAPAuthGroupAdapter{}
	_ engine.Observer[*authv1alpha1.LDAPAuthGroup] = LDAPAuthGroupAdapter{}
)

// Path implements engine.Adapter.
func (LDAPAuthGroupAdapter) Path(o *authv1alpha1.LDAPAuthGroup) string {
	return "auth/" + authMount(o.Spec.MountPath, "ldap") + "/groups/" + authName(o.Spec.Name, o.Name)
}

// Desired implements engine.Adapter.
func (LDAPAuthGroupAdapter) Desired(_ context.Context, _ engine.Resolver, o *authv1alpha1.LDAPAuthGroup) (engine.Payload, error) {
	p := engine.Payload{}
	putList(p, pluginPoliciesKey, o.Spec.Policies)
	return p, nil
}

// Fields implements engine.Adapter.
func (LDAPAuthGroupAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{Sets: []string{pluginPoliciesKey}}
}

// DriftTier implements engine.DriftTier.
func (LDAPAuthGroupAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// Observe implements engine.Observer. When the object is absent it checks that the auth method is enabled.
func (a LDAPAuthGroupAdapter) Observe(ctx context.Context, c openbao.Client, o *authv1alpha1.LDAPAuthGroup) (engine.Payload, bool, error) {
	return observeExternalAuth(ctx, c, a.Path(o), authMount(o.Spec.MountPath, "ldap"), "ldap")
}
