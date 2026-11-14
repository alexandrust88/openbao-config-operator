package auth

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=cfauthconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=cfauthconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=cfauthconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// SetupCFAuthConfig registers the CFAuthConfig controller with the manager.
func SetupCFAuthConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPluginKind(mgr, conns, opts, "cfauthconfig-controller", CFAuthConfigAdapter{},
		func() *authv1alpha1.CFAuthConfig { return &authv1alpha1.CFAuthConfig{} })
}

// CFAuthConfigAdapter maps a CFAuthConfig onto auth/<mountPath>/config of a Cloud Foundry auth method.
type CFAuthConfigAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.CFAuthConfig]          = CFAuthConfigAdapter{}
	_ engine.DriftTier                                    = CFAuthConfigAdapter{}
	_ engine.Observer[*authv1alpha1.CFAuthConfig]         = CFAuthConfigAdapter{}
	_ engine.SecretReferencer[*authv1alpha1.CFAuthConfig] = CFAuthConfigAdapter{}
)

// Path implements engine.Adapter.
func (CFAuthConfigAdapter) Path(o *authv1alpha1.CFAuthConfig) string {
	return "auth/" + authMount(o.Spec.MountPath, "cf") + "/config"
}

// Desired implements engine.Adapter.
func (CFAuthConfigAdapter) Desired(ctx context.Context, r engine.Resolver, o *authv1alpha1.CFAuthConfig) (engine.Payload, error) {
	s := o.Spec
	p := engine.Payload{"identity_ca_certificates": s.IdentityCACertificates, "cf_api_addr": s.CFAPIAddr}
	putString(p, "cf_username", s.CFUsername)
	if err := putCloudSecret(ctx, r, p, o.Namespace, "cf_password", s.CFPasswordSecretRef); err != nil {
		return nil, err
	}
	putList(p, "cf_api_trusted_certificates", s.CFAPITrustedCertificates)
	putString(p, "cf_api_mutual_tls_certificate", s.CFAPIMutualTLSCertificate)
	if err := putCloudSecret(ctx, r, p, o.Namespace, "cf_api_mutual_tls_key", s.CFAPIMutualTLSKeySecretRef); err != nil {
		return nil, err
	}
	pluginPut(p, "login_max_seconds_not_before", s.LoginMaxSecondsNotBefore)
	pluginPut(p, "login_max_seconds_not_after", s.LoginMaxSecondsNotAfter)
	return p, nil
}

// Fields implements engine.Adapter.
func (CFAuthConfigAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{
		WriteOnly: []string{"cf_password", "cf_api_mutual_tls_key"},
		Sets:      []string{"identity_ca_certificates", "cf_api_trusted_certificates"},
		Durations: []string{"login_max_seconds_not_before", "login_max_seconds_not_after"},
	}
}

// DriftTier implements engine.DriftTier.
func (CFAuthConfigAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// Observe implements engine.Observer; it reports a missing auth method as ErrAuthMountMissing.
func (a CFAuthConfigAdapter) Observe(ctx context.Context, c openbao.Client, o *authv1alpha1.CFAuthConfig) (engine.Payload, bool, error) {
	return observeExternalAuth(ctx, c, a.Path(o), authMount(o.Spec.MountPath, "cf"), "cf")
}

// SecretRefs implements engine.SecretReferencer.
func (CFAuthConfigAdapter) SecretRefs(o *authv1alpha1.CFAuthConfig) []string {
	return cloudSecretNames(o.Spec.CFPasswordSecretRef, o.Spec.CFAPIMutualTLSKeySecretRef)
}
