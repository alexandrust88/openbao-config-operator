package auth

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=auth.obco.dev,resources=azureauthconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=azureauthconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=azureauthconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// SetupAzureAuthConfig registers the AzureAuthConfig controller with the manager.
func SetupAzureAuthConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPluginKind(mgr, conns, opts, "azureauthconfig-controller", AzureAuthConfigAdapter{},
		func() *authv1alpha1.AzureAuthConfig { return &authv1alpha1.AzureAuthConfig{} })
}

// AzureAuthConfigAdapter maps an AzureAuthConfig onto auth/<mountPath>/config of an Azure auth method.
type AzureAuthConfigAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.AzureAuthConfig]          = AzureAuthConfigAdapter{}
	_ engine.DriftTier                                       = AzureAuthConfigAdapter{}
	_ engine.Observer[*authv1alpha1.AzureAuthConfig]         = AzureAuthConfigAdapter{}
	_ engine.SecretReferencer[*authv1alpha1.AzureAuthConfig] = AzureAuthConfigAdapter{}
)

// Path implements engine.Adapter.
func (AzureAuthConfigAdapter) Path(o *authv1alpha1.AzureAuthConfig) string {
	return "auth/" + authMount(o.Spec.MountPath, "azure") + "/config"
}

// Desired implements engine.Adapter.
func (AzureAuthConfigAdapter) Desired(ctx context.Context, r engine.Resolver, o *authv1alpha1.AzureAuthConfig) (engine.Payload, error) {
	s := o.Spec
	p := engine.Payload{"tenant_id": s.TenantID, "resource": s.Resource}
	putString(p, "environment", s.Environment)
	putString(p, "client_id", s.ClientID)
	if err := putCloudSecret(ctx, r, p, o.Namespace, "client_secret", s.ClientSecretSecretRef); err != nil {
		return nil, err
	}
	pluginPut(p, "max_retries", s.MaxRetries)
	putSeconds(p, "retry_delay", s.RetryDelay)
	putSeconds(p, "max_retry_delay", s.MaxRetryDelay)
	return p, nil
}

// Fields implements engine.Adapter.
func (AzureAuthConfigAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{WriteOnly: []string{"client_secret"}, Durations: []string{"retry_delay", "max_retry_delay"}}
}

// DriftTier implements engine.DriftTier.
func (AzureAuthConfigAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// Observe implements engine.Observer. The read returns retry_delay and max_retry_delay in nanoseconds; they are
// converted to the seconds that are written. A missing auth method is reported as ErrAuthMountMissing.
func (a AzureAuthConfigAdapter) Observe(ctx context.Context, c openbao.Client, o *authv1alpha1.AzureAuthConfig) (engine.Payload, bool, error) {
	observed, found, err := observeExternalAuth(ctx, c, a.Path(o), authMount(o.Spec.MountPath, "azure"), "azure")
	if !found || err != nil {
		return observed, found, err
	}
	cloudNanosToSeconds(observed, "retry_delay", "max_retry_delay")
	return observed, true, nil
}

// SecretRefs implements engine.SecretReferencer.
func (AzureAuthConfigAdapter) SecretRefs(o *authv1alpha1.AzureAuthConfig) []string {
	return cloudSecretNames(o.Spec.ClientSecretSecretRef)
}
