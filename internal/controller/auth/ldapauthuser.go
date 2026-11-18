package auth

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=auth.obco.dev,resources=ldapauthusers,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=ldapauthusers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=ldapauthusers/finalizers,verbs=update

// SetupLDAPAuthUser registers the LDAPAuthUser controller with the manager.
func SetupLDAPAuthUser(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPluginKind(mgr, conns, opts, "ldapauthuser-controller", LDAPAuthUserAdapter{},
		func() *authv1alpha1.LDAPAuthUser { return &authv1alpha1.LDAPAuthUser{} })
}

// LDAPAuthUserAdapter maps a LDAPAuthUser onto auth/<mountPath>/users/<name> of an LDAP auth method.
type LDAPAuthUserAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.LDAPAuthUser]  = LDAPAuthUserAdapter{}
	_ engine.DriftTier                            = LDAPAuthUserAdapter{}
	_ engine.Observer[*authv1alpha1.LDAPAuthUser] = LDAPAuthUserAdapter{}
)

// Path implements engine.Adapter.
func (LDAPAuthUserAdapter) Path(o *authv1alpha1.LDAPAuthUser) string {
	return "auth/" + authMount(o.Spec.MountPath, "ldap") + "/users/" + authName(o.Spec.Name, o.Name)
}

// Desired implements engine.Adapter.
func (LDAPAuthUserAdapter) Desired(_ context.Context, _ engine.Resolver, o *authv1alpha1.LDAPAuthUser) (engine.Payload, error) {
	p := engine.Payload{}
	putList(p, "groups", o.Spec.Groups)
	putList(p, pluginPoliciesKey, o.Spec.Policies)
	return p, nil
}

// Fields implements engine.Adapter.
func (LDAPAuthUserAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{Sets: []string{"groups", pluginPoliciesKey}}
}

// DriftTier implements engine.DriftTier.
func (LDAPAuthUserAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// Observe implements engine.Observer. OpenBao returns the groups of an LDAP user as one comma-separated string,
// lower-cased unless the auth method sets case_sensitive_names; groups that differ only in case are reported as
// desired.
func (a LDAPAuthUserAdapter) Observe(ctx context.Context, c openbao.Client, o *authv1alpha1.LDAPAuthUser) (engine.Payload, bool, error) {
	observed, found, err := observeExternalAuth(ctx, c, a.Path(o), authMount(o.Spec.MountPath, "ldap"), "ldap")
	if found {
		pluginCommaKeyToList(observed, "groups")
		if groups, ok := observed["groups"].([]string); ok && pluginSameSet(pluginLower(groups), pluginLower(o.Spec.Groups)) {
			observed["groups"] = o.Spec.Groups
		}
	}
	return observed, found, err
}
