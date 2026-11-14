package secrets

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=terraformcloudsecretsconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=terraformcloudsecretsconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=terraformcloudsecretsconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// SetupTerraformCloudSecretsConfig registers the TerraformCloudSecretsConfig controller with the manager.
func SetupTerraformCloudSecretsConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPlugin(mgr, conns, opts, "terraformcloudsecretsconfig", TerraformCloudSecretsConfigAdapter{},
		func() *secretsv1alpha1.TerraformCloudSecretsConfig {
			return &secretsv1alpha1.TerraformCloudSecretsConfig{}
		})
}

// TerraformCloudSecretsConfigAdapter maps a TerraformCloudSecretsConfig onto <mountPath>/config.
type TerraformCloudSecretsConfigAdapter struct{}

var (
	_ engine.Adapter[*secretsv1alpha1.TerraformCloudSecretsConfig]          = TerraformCloudSecretsConfigAdapter{}
	_ engine.SecretReferencer[*secretsv1alpha1.TerraformCloudSecretsConfig] = TerraformCloudSecretsConfigAdapter{}
)

// Path implements engine.Adapter.
func (TerraformCloudSecretsConfigAdapter) Path(c *secretsv1alpha1.TerraformCloudSecretsConfig) string {
	return pluginMountPath(c.Spec.MountPath, "terraform") + "/config"
}

// Desired implements engine.Adapter.
func (TerraformCloudSecretsConfigAdapter) Desired(ctx context.Context, r engine.Resolver, c *secretsv1alpha1.TerraformCloudSecretsConfig) (engine.Payload, error) {
	p := engine.Payload{}
	optString(p, "address", c.Spec.Address)
	optString(p, "base_path", c.Spec.BasePath)
	optSeconds(p, "explicit_max_ttl", c.Spec.ExplicitMaxTTL)
	if err := putPluginSecret(ctx, r, c.Namespace, p, "token", &c.Spec.TokenSecretRef); err != nil {
		return nil, err
	}
	return p, nil
}

// Fields implements engine.Adapter.
func (TerraformCloudSecretsConfigAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{WriteOnly: []string{"token"}, Durations: []string{"explicit_max_ttl"}}
}

// SecretRefs implements engine.SecretReferencer.
func (TerraformCloudSecretsConfigAdapter) SecretRefs(c *secretsv1alpha1.TerraformCloudSecretsConfig) []string {
	return pluginSecretNames(&c.Spec.TokenSecretRef)
}
