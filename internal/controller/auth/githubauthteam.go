package auth

import (
	"context"
	"strings"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=auth.obco.dev,resources=githubauthteams,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=githubauthteams/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=githubauthteams/finalizers,verbs=update

// SetupGitHubAuthTeam registers the GitHubAuthTeam controller with the manager.
func SetupGitHubAuthTeam(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPluginKind(mgr, conns, opts, "githubauthteam-controller", GitHubAuthTeamAdapter{},
		func() *authv1alpha1.GitHubAuthTeam { return &authv1alpha1.GitHubAuthTeam{} })
}

// GitHubAuthTeamAdapter maps a GitHubAuthTeam onto auth/<mountPath>/map/teams/<name> of a GitHub auth method.
type GitHubAuthTeamAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.GitHubAuthTeam]  = GitHubAuthTeamAdapter{}
	_ engine.DriftTier                              = GitHubAuthTeamAdapter{}
	_ engine.Observer[*authv1alpha1.GitHubAuthTeam] = GitHubAuthTeamAdapter{}
)

// Path implements engine.Adapter.
func (GitHubAuthTeamAdapter) Path(o *authv1alpha1.GitHubAuthTeam) string {
	return "auth/" + authMount(o.Spec.MountPath, "github") + "/map/teams/" + authName(o.Spec.Name, o.Name)
}

// Desired implements engine.Adapter.
func (GitHubAuthTeamAdapter) Desired(_ context.Context, _ engine.Resolver, o *authv1alpha1.GitHubAuthTeam) (engine.Payload, error) {
	return engine.Payload{"value": strings.Join(o.Spec.Policies, ",")}, nil
}

// Fields implements engine.Adapter.
func (GitHubAuthTeamAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{}
}

// DriftTier implements engine.DriftTier.
func (GitHubAuthTeamAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// Observe implements engine.Observer. OpenBao stores the policies of a team as one comma-separated string.
func (a GitHubAuthTeamAdapter) Observe(ctx context.Context, c openbao.Client, o *authv1alpha1.GitHubAuthTeam) (engine.Payload, bool, error) {
	observed, found, err := observeExternalAuth(ctx, c, a.Path(o), authMount(o.Spec.MountPath, "github"), "github")
	if found {
		normaliseGitHubMap(observed, o.Spec.Policies)
	}
	return observed, found, err
}
