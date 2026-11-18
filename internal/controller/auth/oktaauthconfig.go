package auth

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=auth.obco.dev,resources=oktaauthconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=oktaauthconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=oktaauthconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// SetupOktaAuthConfig registers the OktaAuthConfig controller with the manager.
func SetupOktaAuthConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPluginKind(mgr, conns, opts, "oktaauthconfig-controller", OktaAuthConfigAdapter{},
		func() *authv1alpha1.OktaAuthConfig { return &authv1alpha1.OktaAuthConfig{} })
}

// OktaAuthConfigAdapter maps a OktaAuthConfig onto auth/<mountPath>/config of an Okta auth method.
type OktaAuthConfigAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.OktaAuthConfig]          = OktaAuthConfigAdapter{}
	_ engine.DriftTier                                      = OktaAuthConfigAdapter{}
	_ engine.SecretReferencer[*authv1alpha1.OktaAuthConfig] = OktaAuthConfigAdapter{}
)

// Path implements engine.Adapter.
func (OktaAuthConfigAdapter) Path(o *authv1alpha1.OktaAuthConfig) string {
	return "auth/" + authMount(o.Spec.MountPath, "okta") + "/config"
}

// Desired implements engine.Adapter.
func (OktaAuthConfigAdapter) Desired(ctx context.Context, r engine.Resolver, o *authv1alpha1.OktaAuthConfig) (engine.Payload, error) {
	p := engine.Payload{"org_name": o.Spec.OrgName}
	if ref := o.Spec.APITokenSecretRef; ref != nil {
		token, err := authSecret(ctx, r, o.Namespace, *ref)
		if err != nil {
			return nil, err
		}
		p["api_token"] = token
	}
	putString(p, "base_url", o.Spec.BaseURL)
	pluginPut(p, "bypass_okta_mfa", o.Spec.BypassOktaMFA)
	if err := putTokenFields(ctx, r, o, p, o.Spec.TokenFields); err != nil {
		return nil, err
	}
	return p, nil
}

// Fields implements engine.Adapter.
func (OktaAuthConfigAdapter) Fields() engine.FieldSpec {
	return MergeFieldSpecs(engine.FieldSpec{WriteOnly: []string{"api_token"}}, TokenFieldSpec)
}

// DriftTier implements engine.DriftTier.
func (OktaAuthConfigAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// SecretRefs implements engine.SecretReferencer.
func (OktaAuthConfigAdapter) SecretRefs(o *authv1alpha1.OktaAuthConfig) []string {
	if o.Spec.APITokenSecretRef == nil {
		return nil
	}
	return []string{o.Spec.APITokenSecretRef.Name}
}

// Observe implements engine.Observer. When the object is absent it checks that the auth method is enabled.
func (a OktaAuthConfigAdapter) Observe(ctx context.Context, c openbao.Client, o *authv1alpha1.OktaAuthConfig) (engine.Payload, bool, error) {
	return observeExternalAuth(ctx, c, a.Path(o), authMount(o.Spec.MountPath, "okta"), "okta")
}
