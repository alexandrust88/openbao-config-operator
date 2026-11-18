package auth

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=auth.obco.dev,resources=kerberosauthldapconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=kerberosauthldapconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=kerberosauthldapconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// SetupKerberosAuthLDAPConfig registers the KerberosAuthLDAPConfig controller with the manager.
func SetupKerberosAuthLDAPConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPluginKind(mgr, conns, opts, "kerberosauthldapconfig-controller", KerberosAuthLDAPConfigAdapter{},
		func() *authv1alpha1.KerberosAuthLDAPConfig { return &authv1alpha1.KerberosAuthLDAPConfig{} })
}

// KerberosAuthLDAPConfigAdapter maps a KerberosAuthLDAPConfig onto auth/<mountPath>/config/ldap of a Kerberos auth method.
type KerberosAuthLDAPConfigAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.KerberosAuthLDAPConfig]          = KerberosAuthLDAPConfigAdapter{}
	_ engine.DriftTier                                              = KerberosAuthLDAPConfigAdapter{}
	_ engine.SecretReferencer[*authv1alpha1.KerberosAuthLDAPConfig] = KerberosAuthLDAPConfigAdapter{}
)

// Path implements engine.Adapter.
func (KerberosAuthLDAPConfigAdapter) Path(o *authv1alpha1.KerberosAuthLDAPConfig) string {
	return "auth/" + authMount(o.Spec.MountPath, "kerberos") + "/config/ldap"
}

// Desired implements engine.Adapter.
func (KerberosAuthLDAPConfigAdapter) Desired(ctx context.Context, r engine.Resolver, o *authv1alpha1.KerberosAuthLDAPConfig) (engine.Payload, error) {
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
func (KerberosAuthLDAPConfigAdapter) Fields() engine.FieldSpec {
	return MergeFieldSpecs(ldapConnectionFieldSpec, TokenFieldSpec)
}

// DriftTier implements engine.DriftTier.
func (KerberosAuthLDAPConfigAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// SecretRefs implements engine.SecretReferencer.
func (KerberosAuthLDAPConfigAdapter) SecretRefs(o *authv1alpha1.KerberosAuthLDAPConfig) []string {
	return ldapConnectionSecretRefs(o.Spec.LDAPConnectionFields)
}

// Observe implements engine.Observer. When the object is absent it checks that the auth method is enabled.
func (a KerberosAuthLDAPConfigAdapter) Observe(ctx context.Context, c openbao.Client, o *authv1alpha1.KerberosAuthLDAPConfig) (engine.Payload, bool, error) {
	return observeExternalAuth(ctx, c, a.Path(o), authMount(o.Spec.MountPath, "kerberos"), "kerberos")
}
