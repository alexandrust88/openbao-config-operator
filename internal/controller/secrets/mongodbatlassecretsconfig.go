package secrets

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// +kubebuilder:rbac:groups=secrets.obco.dev,resources=mongodbatlassecretsconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=mongodbatlassecretsconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=mongodbatlassecretsconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// SetupMongoDBAtlasSecretsConfig registers the MongoDBAtlasSecretsConfig controller with the manager.
func SetupMongoDBAtlasSecretsConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPlugin(mgr, conns, opts, "mongodbatlassecretsconfig", MongoDBAtlasSecretsConfigAdapter{},
		func() *secretsv1alpha1.MongoDBAtlasSecretsConfig { return &secretsv1alpha1.MongoDBAtlasSecretsConfig{} })
}

// MongoDBAtlasSecretsConfigAdapter maps a MongoDBAtlasSecretsConfig onto <mountPath>/config.
type MongoDBAtlasSecretsConfigAdapter struct{}

var (
	_ engine.Adapter[*secretsv1alpha1.MongoDBAtlasSecretsConfig]          = MongoDBAtlasSecretsConfigAdapter{}
	_ engine.SecretReferencer[*secretsv1alpha1.MongoDBAtlasSecretsConfig] = MongoDBAtlasSecretsConfigAdapter{}
)

// Path implements engine.Adapter.
func (MongoDBAtlasSecretsConfigAdapter) Path(c *secretsv1alpha1.MongoDBAtlasSecretsConfig) string {
	return pluginMountPath(c.Spec.MountPath, "mongodbatlas") + "/config"
}

// Desired implements engine.Adapter.
func (MongoDBAtlasSecretsConfigAdapter) Desired(ctx context.Context, r engine.Resolver, c *secretsv1alpha1.MongoDBAtlasSecretsConfig) (engine.Payload, error) {
	p := engine.Payload{"public_key": c.Spec.PublicKey}
	if err := putPluginSecret(ctx, r, c.Namespace, p, "private_key", &c.Spec.PrivateKeySecretRef); err != nil {
		return nil, err
	}
	return p, nil
}

// Fields implements engine.Adapter.
func (MongoDBAtlasSecretsConfigAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{WriteOnly: []string{"private_key"}}
}

// SecretRefs implements engine.SecretReferencer.
func (MongoDBAtlasSecretsConfigAdapter) SecretRefs(c *secretsv1alpha1.MongoDBAtlasSecretsConfig) []string {
	return pluginSecretNames(&c.Spec.PrivateKeySecretRef)
}
