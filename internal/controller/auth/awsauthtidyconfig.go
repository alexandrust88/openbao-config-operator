package auth

import (
	"context"
	"fmt"
	"strings"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=auth.obco.dev,resources=awsauthtidyconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=awsauthtidyconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=awsauthtidyconfigs/finalizers,verbs=update

// SetupAWSAuthTidyConfig registers the AWSAuthTidyConfig controller with the manager.
func SetupAWSAuthTidyConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPluginKind(mgr, conns, opts, "awsauthtidyconfig-controller", AWSAuthTidyConfigAdapter{},
		func() *authv1alpha1.AWSAuthTidyConfig { return &authv1alpha1.AWSAuthTidyConfig{} })
}

// awsTidySections maps the payload key prefix of each tidy section to its path under auth/<mountPath>/.
var awsTidySections = []struct{ prefix, path string }{
	{"identity_accesslist_", "config/tidy/identity-accesslist"},
	{"roletag_denylist_", "config/tidy/roletag-denylist"},
}

// AWSAuthTidyConfigAdapter maps an AWSAuthTidyConfig onto the two tidy singletons of an AWS auth method. The
// payload holds both sections with prefixed keys; Observe and Write split them by path.
type AWSAuthTidyConfigAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.AWSAuthTidyConfig]  = AWSAuthTidyConfigAdapter{}
	_ engine.DriftTier                                 = AWSAuthTidyConfigAdapter{}
	_ engine.Observer[*authv1alpha1.AWSAuthTidyConfig] = AWSAuthTidyConfigAdapter{}
	_ engine.Writer[*authv1alpha1.AWSAuthTidyConfig]   = AWSAuthTidyConfigAdapter{}
)

// Path implements engine.Adapter. It names the parent of both tidy paths.
func (AWSAuthTidyConfigAdapter) Path(o *authv1alpha1.AWSAuthTidyConfig) string {
	return "auth/" + authMount(o.Spec.MountPath, "aws") + "/config/tidy"
}

// Desired implements engine.Adapter.
func (AWSAuthTidyConfigAdapter) Desired(_ context.Context, _ engine.Resolver, o *authv1alpha1.AWSAuthTidyConfig) (engine.Payload, error) {
	p := engine.Payload{}
	for i, s := range []*authv1alpha1.AWSAuthTidySettings{o.Spec.IdentityAccessList, o.Spec.RoleTagDenyList} {
		if s == nil {
			continue
		}
		prefix := awsTidySections[i].prefix
		putSeconds(p, prefix+"safety_buffer", s.SafetyBuffer)
		pluginPut(p, prefix+"disable_periodic_tidy", s.DisablePeriodicTidy)
	}
	return p, nil
}

// Fields implements engine.Adapter.
func (AWSAuthTidyConfigAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{Durations: []string{"identity_accesslist_safety_buffer", "roletag_denylist_safety_buffer"}}
}

// DriftTier implements engine.DriftTier.
func (AWSAuthTidyConfigAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// Observe implements engine.Observer: both sections read into one prefixed payload; found when either exists.
func (a AWSAuthTidyConfigAdapter) Observe(ctx context.Context, c openbao.Client, o *authv1alpha1.AWSAuthTidyConfig) (engine.Payload, bool, error) {
	mount := authMount(o.Spec.MountPath, "aws")
	observed := engine.Payload{}
	found := false
	for _, section := range awsTidySections {
		data, ok, err := observeExternalAuth(ctx, c, "auth/"+mount+"/"+section.path, mount, "aws")
		if err != nil {
			return nil, false, err
		}
		found = found || ok
		for key, value := range data {
			observed[section.prefix+key] = value
		}
	}
	if !found {
		return nil, false, nil
	}
	return observed, true, nil
}

// Write implements engine.Writer: each section with a desired key is written to its own path.
func (AWSAuthTidyConfigAdapter) Write(ctx context.Context, c openbao.Client, o *authv1alpha1.AWSAuthTidyConfig,
	desired engine.Payload, _ bool) (engine.Payload, error) {
	mount := authMount(o.Spec.MountPath, "aws")
	for _, section := range awsTidySections {
		data := engine.Payload{}
		for key, value := range desired {
			if field, ok := strings.CutPrefix(key, section.prefix); ok {
				data[field] = value
			}
		}
		if len(data) == 0 {
			continue
		}
		path := "auth/" + mount + "/" + section.path
		if _, err := c.Write(ctx, path, data); err != nil {
			return nil, fmt.Errorf("write %s: %w", path, err)
		}
	}
	return nil, nil
}
