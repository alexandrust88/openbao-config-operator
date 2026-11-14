package secrets

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=consulsecretsconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=consulsecretsconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=consulsecretsconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// SetupConsulSecretsConfig registers the ConsulSecretsConfig controller with the manager.
func SetupConsulSecretsConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPlugin(mgr, conns, opts, "consulsecretsconfig", ConsulSecretsConfigAdapter{},
		func() *secretsv1alpha1.ConsulSecretsConfig { return &secretsv1alpha1.ConsulSecretsConfig{} })
}

// ConsulSecretsConfigAdapter maps a ConsulSecretsConfig onto <mountPath>/config/access.
type ConsulSecretsConfigAdapter struct{}

var (
	_ engine.Adapter[*secretsv1alpha1.ConsulSecretsConfig]          = ConsulSecretsConfigAdapter{}
	_ engine.SecretReferencer[*secretsv1alpha1.ConsulSecretsConfig] = ConsulSecretsConfigAdapter{}
	_ engine.Writer[*secretsv1alpha1.ConsulSecretsConfig]           = ConsulSecretsConfigAdapter{}
)

// Path implements engine.Adapter.
func (ConsulSecretsConfigAdapter) Path(c *secretsv1alpha1.ConsulSecretsConfig) string {
	return consulSecretsMount(c.Spec.MountPath) + "/config/access"
}

// Desired implements engine.Adapter.
func (ConsulSecretsConfigAdapter) Desired(ctx context.Context, r engine.Resolver, c *secretsv1alpha1.ConsulSecretsConfig) (engine.Payload, error) {
	s := c.Spec
	p := engine.Payload{"address": s.Address}
	optString(p, "scheme", s.Scheme)
	if err := putPluginSecret(ctx, r, c.Namespace, p, "token", &s.TokenSecretRef); err != nil {
		return nil, err
	}
	optString(p, "ca_cert", s.CACert)
	optString(p, "client_cert", s.ClientCert)
	if err := putPluginSecret(ctx, r, c.Namespace, p, "client_key", s.ClientKeySecretRef); err != nil {
		return nil, err
	}
	return p, nil
}

// Fields implements engine.Adapter. The read returns only address and scheme, so the certificates are hashed with
// the secrets instead of compared.
func (ConsulSecretsConfigAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{WriteOnly: []string{"token", "client_key", "ca_cert", "client_cert"}}
}

// SecretRefs implements engine.SecretReferencer.
func (ConsulSecretsConfigAdapter) SecretRefs(c *secretsv1alpha1.ConsulSecretsConfig) []string {
	return pluginSecretNames(&c.Spec.TokenSecretRef, c.Spec.ClientKeySecretRef)
}

func consulSecretsMount(mountPath string) string { return pluginMountPath(mountPath, "consul") }

// Write implements engine.Writer.
func (a ConsulSecretsConfigAdapter) Write(ctx context.Context, c openbao.Client, obj *secretsv1alpha1.ConsulSecretsConfig,
	desired engine.Payload, _ bool) (engine.Payload, error) {
	return writeCloud(ctx, c, a.Path(obj), desired)
}
