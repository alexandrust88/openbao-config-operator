package auth

import (
	"context"
	"strings"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=auth.obco.dev,resources=githubauthusers,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=githubauthusers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=githubauthusers/finalizers,verbs=update

// SetupGitHubAuthUser registers the GitHubAuthUser controller with the manager.
func SetupGitHubAuthUser(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPluginKind(mgr, conns, opts, "githubauthuser-controller", GitHubAuthUserAdapter{},
		func() *authv1alpha1.GitHubAuthUser { return &authv1alpha1.GitHubAuthUser{} })
}

// GitHubAuthUserAdapter maps a GitHubAuthUser onto auth/<mountPath>/map/users/<name> of a GitHub auth method.
type GitHubAuthUserAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.GitHubAuthUser]  = GitHubAuthUserAdapter{}
	_ engine.DriftTier                              = GitHubAuthUserAdapter{}
	_ engine.Observer[*authv1alpha1.GitHubAuthUser] = GitHubAuthUserAdapter{}
)

// Path implements engine.Adapter.
func (GitHubAuthUserAdapter) Path(o *authv1alpha1.GitHubAuthUser) string {
	return "auth/" + authMount(o.Spec.MountPath, "github") + "/map/users/" + authName(o.Spec.Name, o.Name)
}

// Desired implements engine.Adapter.
func (GitHubAuthUserAdapter) Desired(_ context.Context, _ engine.Resolver, o *authv1alpha1.GitHubAuthUser) (engine.Payload, error) {
	return engine.Payload{"value": strings.Join(o.Spec.Policies, ",")}, nil
}

// Fields implements engine.Adapter.
func (GitHubAuthUserAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{}
}

// DriftTier implements engine.DriftTier.
func (GitHubAuthUserAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// Observe implements engine.Observer. OpenBao stores the policies of a user as one comma-separated string.
func (a GitHubAuthUserAdapter) Observe(ctx context.Context, c openbao.Client, o *authv1alpha1.GitHubAuthUser) (engine.Payload, bool, error) {
	observed, found, err := observeExternalAuth(ctx, c, a.Path(o), authMount(o.Spec.MountPath, "github"), "github")
	if found {
		normaliseGitHubMap(observed, o.Spec.Policies)
	}
	return observed, found, err
}
