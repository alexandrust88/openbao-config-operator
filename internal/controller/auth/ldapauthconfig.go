package auth

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=auth.obco.dev,resources=ldapauthconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=ldapauthconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=ldapauthconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// SetupLDAPAuthConfig registers the LDAPAuthConfig controller with the manager.
func SetupLDAPAuthConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPluginKind(mgr, conns, opts, "ldapauthconfig-controller", LDAPAuthConfigAdapter{},
		func() *authv1alpha1.LDAPAuthConfig { return &authv1alpha1.LDAPAuthConfig{} })
}

// LDAPAuthConfigAdapter maps a LDAPAuthConfig onto auth/<mountPath>/config of an LDAP auth method.
type LDAPAuthConfigAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.LDAPAuthConfig]          = LDAPAuthConfigAdapter{}
	_ engine.DriftTier                                      = LDAPAuthConfigAdapter{}
	_ engine.SecretReferencer[*authv1alpha1.LDAPAuthConfig] = LDAPAuthConfigAdapter{}
)

// Path implements engine.Adapter.
func (LDAPAuthConfigAdapter) Path(o *authv1alpha1.LDAPAuthConfig) string {
	return "auth/" + authMount(o.Spec.MountPath, "ldap") + "/config"
}

// Desired implements engine.Adapter.
func (LDAPAuthConfigAdapter) Desired(ctx context.Context, r engine.Resolver, o *authv1alpha1.LDAPAuthConfig) (engine.Payload, error) {
	p := engine.Payload{}
	if err := putLDAPConnection(ctx, r, o.Namespace, p, o.Spec.LDAPConnectionFields); err != nil {
		return nil, err
	}
	if err := putTokenFields(ctx, r, o, p, o.Spec.TokenFields); err != nil {
		return nil, err
	}
	return p, nil
}

// Fields implements engine.Adapter.
func (LDAPAuthConfigAdapter) Fields() engine.FieldSpec {
	return MergeFieldSpecs(ldapConnectionFieldSpec, TokenFieldSpec)
}

// DriftTier implements engine.DriftTier.
func (LDAPAuthConfigAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// SecretRefs implements engine.SecretReferencer.
func (LDAPAuthConfigAdapter) SecretRefs(o *authv1alpha1.LDAPAuthConfig) []string {
	return ldapConnectionSecretRefs(o.Spec.LDAPConnectionFields)
}

// Observe implements engine.Observer. When the object is absent it checks that the auth method is enabled.
func (a LDAPAuthConfigAdapter) Observe(ctx context.Context, c openbao.Client, o *authv1alpha1.LDAPAuthConfig) (engine.Payload, bool, error) {
	return observeExternalAuth(ctx, c, a.Path(o), authMount(o.Spec.MountPath, "ldap"), "ldap")
}
