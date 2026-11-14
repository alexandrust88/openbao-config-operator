package secrets

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=azuresecretsconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=azuresecretsconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=azuresecretsconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// SetupAzureSecretsConfig registers the AzureSecretsConfig controller with the manager.
func SetupAzureSecretsConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPlugin(mgr, conns, opts, "azuresecretsconfig", AzureSecretsConfigAdapter{},
		func() *secretsv1alpha1.AzureSecretsConfig { return &secretsv1alpha1.AzureSecretsConfig{} })
}

// AzureSecretsConfigAdapter maps an AzureSecretsConfig onto <mountPath>/config.
type AzureSecretsConfigAdapter struct{}

var (
	_ engine.Adapter[*secretsv1alpha1.AzureSecretsConfig]          = AzureSecretsConfigAdapter{}
	_ engine.SecretReferencer[*secretsv1alpha1.AzureSecretsConfig] = AzureSecretsConfigAdapter{}
	_ engine.Writer[*secretsv1alpha1.AzureSecretsConfig]           = AzureSecretsConfigAdapter{}
)

// Path implements engine.Adapter.
func (AzureSecretsConfigAdapter) Path(c *secretsv1alpha1.AzureSecretsConfig) string {
	return azureSecretsMount(c.Spec.MountPath) + "/config"
}

// Desired implements engine.Adapter.
func (AzureSecretsConfigAdapter) Desired(ctx context.Context, r engine.Resolver, c *secretsv1alpha1.AzureSecretsConfig) (engine.Payload, error) {
	s := c.Spec
	p := engine.Payload{"subscription_id": s.SubscriptionID, "tenant_id": s.TenantID}
	if err := putPluginSecret(ctx, r, c.Namespace, p, "client_id", s.ClientIDSecretRef); err != nil {
		return nil, err
	}
	if err := putPluginSecret(ctx, r, c.Namespace, p, "client_secret", s.ClientSecretSecretRef); err != nil {
		return nil, err
	}
	optString(p, "environment", string(s.Environment))
	optString(p, "password_policy", s.PasswordPolicy)
	optSeconds(p, "root_password_ttl", s.RootPasswordTTL)
	return p, nil
}

// Fields implements engine.Adapter.
func (AzureSecretsConfigAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{
		WriteOnly: []string{"client_id", "client_secret"},
		Durations: []string{"root_password_ttl"},
	}
}

// SecretRefs implements engine.SecretReferencer.
func (AzureSecretsConfigAdapter) SecretRefs(c *secretsv1alpha1.AzureSecretsConfig) []string {
	return pluginSecretNames(c.Spec.ClientIDSecretRef, c.Spec.ClientSecretSecretRef)
}

func azureSecretsMount(mountPath string) string { return pluginMountPath(mountPath, "azure") }

// Write implements engine.Writer.
func (a AzureSecretsConfigAdapter) Write(ctx context.Context, c openbao.Client, obj *secretsv1alpha1.AzureSecretsConfig,
	desired engine.Payload, _ bool) (engine.Payload, error) {
	return writeCloud(ctx, c, a.Path(obj), desired)
}
