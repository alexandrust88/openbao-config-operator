package secrets

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=githubsecretsroles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=githubsecretsroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=githubsecretsroles/finalizers,verbs=update

// SetupGitHubSecretsRole registers the GitHubSecretsRole controller with the manager.
func SetupGitHubSecretsRole(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPlugin(mgr, conns, opts, "githubsecretsrole", GitHubSecretsRoleAdapter{},
		func() *secretsv1alpha1.GitHubSecretsRole { return &secretsv1alpha1.GitHubSecretsRole{} })
}

// GitHubSecretsRoleAdapter maps a GitHubSecretsRole onto <mountPath>/permissionset/<name>.
type GitHubSecretsRoleAdapter struct{}

var _ engine.Adapter[*secretsv1alpha1.GitHubSecretsRole] = GitHubSecretsRoleAdapter{}

// Path implements engine.Adapter.
func (GitHubSecretsRoleAdapter) Path(r *secretsv1alpha1.GitHubSecretsRole) string {
	return pluginMountPath(r.Spec.MountPath, "github") + "/permissionset/" + specName(r.Spec.Name, r.Name)
}

// Desired implements engine.Adapter.
func (GitHubSecretsRoleAdapter) Desired(_ context.Context, _ engine.Resolver, r *secretsv1alpha1.GitHubSecretsRole) (engine.Payload, error) {
	p := engine.Payload{}
	if r.Spec.InstallationID != nil {
		p["installation_id"] = *r.Spec.InstallationID
	}
	optString(p, "org_name", r.Spec.OrganizationName)
	putPluginList(p, "repositories", r.Spec.Repositories)
	putPluginList(p, "repository_ids", r.Spec.RepositoryIDs)
	optBool(p, "skip_inaccessible_repositories", r.Spec.SkipInaccessibleRepositories)
	if r.Spec.Permissions != nil {
		p["permissions"] = r.Spec.Permissions
	}
	return p, nil
}

// Fields implements engine.Adapter.
func (GitHubSecretsRoleAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{Sets: []string{"repositories", "repository_ids"}}
}
