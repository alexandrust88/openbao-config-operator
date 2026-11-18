package secrets

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=secrets.obco.dev,resources=gcpsecretsconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=gcpsecretsconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=gcpsecretsconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// SetupGCPSecretsConfig registers the GCPSecretsConfig controller with the manager.
func SetupGCPSecretsConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPlugin(mgr, conns, opts, "gcpsecretsconfig", GCPSecretsConfigAdapter{},
		func() *secretsv1alpha1.GCPSecretsConfig { return &secretsv1alpha1.GCPSecretsConfig{} })
}

// GCPSecretsConfigAdapter maps a GCPSecretsConfig onto <mountPath>/config.
type GCPSecretsConfigAdapter struct{}

var (
	_ engine.Adapter[*secretsv1alpha1.GCPSecretsConfig]          = GCPSecretsConfigAdapter{}
	_ engine.SecretReferencer[*secretsv1alpha1.GCPSecretsConfig] = GCPSecretsConfigAdapter{}
	_ engine.Writer[*secretsv1alpha1.GCPSecretsConfig]           = GCPSecretsConfigAdapter{}
)

// Path implements engine.Adapter.
func (GCPSecretsConfigAdapter) Path(c *secretsv1alpha1.GCPSecretsConfig) string {
	return gcpSecretsMount(c.Spec.MountPath) + "/config"
}

// Desired implements engine.Adapter.
func (GCPSecretsConfigAdapter) Desired(ctx context.Context, r engine.Resolver, c *secretsv1alpha1.GCPSecretsConfig) (engine.Payload, error) {
	p := engine.Payload{}
	if err := putPluginSecret(ctx, r, c.Namespace, p, "credentials", c.Spec.CredentialsSecretRef); err != nil {
		return nil, err
	}
	optSeconds(p, "ttl", c.Spec.TTL)
	optSeconds(p, "max_ttl", c.Spec.MaxTTL)
	return p, nil
}

// Fields implements engine.Adapter.
func (GCPSecretsConfigAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{
		WriteOnly: []string{"credentials"},
		Durations: []string{"ttl", "max_ttl"},
	}
}

// SecretRefs implements engine.SecretReferencer.
func (GCPSecretsConfigAdapter) SecretRefs(c *secretsv1alpha1.GCPSecretsConfig) []string {
	return pluginSecretNames(c.Spec.CredentialsSecretRef)
}

func gcpSecretsMount(mountPath string) string { return pluginMountPath(mountPath, "gcp") }

// Write implements engine.Writer.
func (a GCPSecretsConfigAdapter) Write(ctx context.Context, c openbao.Client, obj *secretsv1alpha1.GCPSecretsConfig,
	desired engine.Payload, _ bool) (engine.Payload, error) {
	return writeCloud(ctx, c, a.Path(obj), desired)
}
