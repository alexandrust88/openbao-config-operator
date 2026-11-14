package auth

import (
	"context"
	"strings"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=githubauthconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=githubauthconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=githubauthconfigs/finalizers,verbs=update

// SetupGitHubAuthConfig registers the GitHubAuthConfig controller with the manager.
func SetupGitHubAuthConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPluginKind(mgr, conns, opts, "githubauthconfig-controller", GitHubAuthConfigAdapter{},
		func() *authv1alpha1.GitHubAuthConfig { return &authv1alpha1.GitHubAuthConfig{} })
}

// GitHubAuthConfigAdapter maps a GitHubAuthConfig onto auth/<mountPath>/config of a GitHub auth method.
type GitHubAuthConfigAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.GitHubAuthConfig]  = GitHubAuthConfigAdapter{}
	_ engine.DriftTier                                = GitHubAuthConfigAdapter{}
	_ engine.Observer[*authv1alpha1.GitHubAuthConfig] = GitHubAuthConfigAdapter{}
)

// Path implements engine.Adapter.
func (GitHubAuthConfigAdapter) Path(o *authv1alpha1.GitHubAuthConfig) string {
	return "auth/" + authMount(o.Spec.MountPath, "github") + "/config"
}

// Desired implements engine.Adapter. base_url gets the trailing slash OpenBao appends on write.
func (GitHubAuthConfigAdapter) Desired(ctx context.Context, r engine.Resolver, o *authv1alpha1.GitHubAuthConfig) (engine.Payload, error) {
	p := engine.Payload{"organization": o.Spec.Organization}
	pluginPut(p, "organization_id", o.Spec.OrganizationID)
	if o.Spec.BaseURL != "" {
		p["base_url"] = strings.TrimSuffix(o.Spec.BaseURL, "/") + "/"
	}
	if err := putTokenFields(ctx, r, o, p, o.Spec.TokenFields); err != nil {
		return nil, err
	}
	return p, nil
}

// Fields implements engine.Adapter.
func (GitHubAuthConfigAdapter) Fields() engine.FieldSpec {
	return TokenFieldSpec
}

// DriftTier implements engine.DriftTier.
func (GitHubAuthConfigAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// Observe implements engine.Observer. When the object is absent it checks that the auth method is enabled.
func (a GitHubAuthConfigAdapter) Observe(ctx context.Context, c openbao.Client, o *authv1alpha1.GitHubAuthConfig) (engine.Payload, bool, error) {
	return observeExternalAuth(ctx, c, a.Path(o), authMount(o.Spec.MountPath, "github"), "github")
}
