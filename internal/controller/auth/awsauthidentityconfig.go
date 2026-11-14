package auth

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=awsauthidentityconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=awsauthidentityconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=awsauthidentityconfigs/finalizers,verbs=update

// SetupAWSAuthIdentityConfig registers the AWSAuthIdentityConfig controller with the manager.
func SetupAWSAuthIdentityConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPluginKind(mgr, conns, opts, "awsauthidentityconfig-controller", AWSAuthIdentityConfigAdapter{},
		func() *authv1alpha1.AWSAuthIdentityConfig { return &authv1alpha1.AWSAuthIdentityConfig{} })
}

// AWSAuthIdentityConfigAdapter maps an AWSAuthIdentityConfig onto auth/<mountPath>/config/identity.
type AWSAuthIdentityConfigAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.AWSAuthIdentityConfig]  = AWSAuthIdentityConfigAdapter{}
	_ engine.DriftTier                                     = AWSAuthIdentityConfigAdapter{}
	_ engine.Observer[*authv1alpha1.AWSAuthIdentityConfig] = AWSAuthIdentityConfigAdapter{}
)

// Path implements engine.Adapter.
func (AWSAuthIdentityConfigAdapter) Path(o *authv1alpha1.AWSAuthIdentityConfig) string {
	return "auth/" + authMount(o.Spec.MountPath, "aws") + "/config/identity"
}

// Desired implements engine.Adapter.
func (AWSAuthIdentityConfigAdapter) Desired(_ context.Context, _ engine.Resolver, o *authv1alpha1.AWSAuthIdentityConfig) (engine.Payload, error) {
	p := engine.Payload{}
	putString(p, "iam_alias", o.Spec.IAMAlias)
	putList(p, "iam_metadata", o.Spec.IAMMetadata)
	putString(p, "ec2_alias", o.Spec.EC2Alias)
	putList(p, "ec2_metadata", o.Spec.EC2Metadata)
	return p, nil
}

// Fields implements engine.Adapter.
func (AWSAuthIdentityConfigAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{Sets: []string{"iam_metadata", "ec2_metadata"}}
}

// DriftTier implements engine.DriftTier.
func (AWSAuthIdentityConfigAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// Observe implements engine.Observer; it reports a missing auth method as ErrAuthMountMissing.
func (a AWSAuthIdentityConfigAdapter) Observe(ctx context.Context, c openbao.Client, o *authv1alpha1.AWSAuthIdentityConfig) (engine.Payload, bool, error) {
	return observeExternalAuth(ctx, c, a.Path(o), authMount(o.Spec.MountPath, "aws"), "aws")
}
