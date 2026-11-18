package secrets

import (
	"context"
	"errors"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=secrets.obco.dev,resources=githubsecretsconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=githubsecretsconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=githubsecretsconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// SetupGitHubSecretsConfig registers the GitHubSecretsConfig controller with the manager.
func SetupGitHubSecretsConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPlugin(mgr, conns, opts, "githubsecretsconfig", GitHubSecretsConfigAdapter{},
		func() *secretsv1alpha1.GitHubSecretsConfig { return &secretsv1alpha1.GitHubSecretsConfig{} })
}

// GitHubSecretsConfigAdapter maps a GitHubSecretsConfig onto <mountPath>/config.
type GitHubSecretsConfigAdapter struct{}

var (
	_ engine.Adapter[*secretsv1alpha1.GitHubSecretsConfig]          = GitHubSecretsConfigAdapter{}
	_ engine.Observer[*secretsv1alpha1.GitHubSecretsConfig]         = GitHubSecretsConfigAdapter{}
	_ engine.SecretReferencer[*secretsv1alpha1.GitHubSecretsConfig] = GitHubSecretsConfigAdapter{}
)

// Path implements engine.Adapter.
func (GitHubSecretsConfigAdapter) Path(c *secretsv1alpha1.GitHubSecretsConfig) string {
	return pluginMountPath(c.Spec.MountPath, "github") + "/config"
}

// Desired implements engine.Adapter.
func (GitHubSecretsConfigAdapter) Desired(ctx context.Context, r engine.Resolver, c *secretsv1alpha1.GitHubSecretsConfig) (engine.Payload, error) {
	p := engine.Payload{"app_id": c.Spec.AppID}
	optString(p, "base_url", c.Spec.BaseURL)
	optBool(p, "exclude_repository_metadata", c.Spec.ExcludeRepositoryMetadata)
	if err := putPluginSecret(ctx, r, c.Namespace, p, "prv_key", &c.Spec.PrivateKeySecretRef); err != nil {
		return nil, err
	}
	return p, nil
}

// Fields implements engine.Adapter.
func (GitHubSecretsConfigAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{WriteOnly: []string{"prv_key"}}
}

// SecretRefs implements engine.SecretReferencer.
func (GitHubSecretsConfigAdapter) SecretRefs(c *secretsv1alpha1.GitHubSecretsConfig) []string {
	return pluginSecretNames(&c.Spec.PrivateKeySecretRef)
}

// Observe implements engine.Observer. The plugin answers a read of an unconfigured mount with its defaults instead
// of a 404, so a config without a private key is reported as absent.
func (a GitHubSecretsConfigAdapter) Observe(ctx context.Context, c openbao.Client, cfg *secretsv1alpha1.GitHubSecretsConfig) (engine.Payload, bool, error) {
	observed, err := c.Read(ctx, a.Path(cfg))
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if key, _ := observed["prv_key"].(string); key == "" {
		return nil, false, nil
	}
	return observed, true, nil
}
