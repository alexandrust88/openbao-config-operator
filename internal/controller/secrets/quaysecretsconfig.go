package secrets

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// +kubebuilder:rbac:groups=secrets.obco.dev,resources=quaysecretsconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=quaysecretsconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=quaysecretsconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// SetupQuaySecretsConfig registers the QuaySecretsConfig controller with the manager.
func SetupQuaySecretsConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPlugin(mgr, conns, opts, "quaysecretsconfig", QuaySecretsConfigAdapter{},
		func() *secretsv1alpha1.QuaySecretsConfig { return &secretsv1alpha1.QuaySecretsConfig{} })
}

// QuaySecretsConfigAdapter maps a QuaySecretsConfig onto <mountPath>/config.
type QuaySecretsConfigAdapter struct{}

var (
	_ engine.Adapter[*secretsv1alpha1.QuaySecretsConfig]          = QuaySecretsConfigAdapter{}
	_ engine.SecretReferencer[*secretsv1alpha1.QuaySecretsConfig] = QuaySecretsConfigAdapter{}
)

// Path implements engine.Adapter.
func (QuaySecretsConfigAdapter) Path(c *secretsv1alpha1.QuaySecretsConfig) string {
	return pluginMountPath(c.Spec.MountPath, "quay") + "/config"
}

// Desired implements engine.Adapter.
func (QuaySecretsConfigAdapter) Desired(ctx context.Context, r engine.Resolver, c *secretsv1alpha1.QuaySecretsConfig) (engine.Payload, error) {
	p := engine.Payload{"url": c.Spec.URL}
	optString(p, "ca_certificate", c.Spec.CACertificate)
	optBool(p, "disable_ssl_verification", c.Spec.DisableSSLVerification)
	if err := putPluginSecret(ctx, r, c.Namespace, p, "token", &c.Spec.TokenSecretRef); err != nil {
		return nil, err
	}
	return p, nil
}

// Fields implements engine.Adapter.
func (QuaySecretsConfigAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{WriteOnly: []string{"token"}}
}

// SecretRefs implements engine.SecretReferencer.
func (QuaySecretsConfigAdapter) SecretRefs(c *secretsv1alpha1.QuaySecretsConfig) []string {
	return pluginSecretNames(&c.Spec.TokenSecretRef)
}
