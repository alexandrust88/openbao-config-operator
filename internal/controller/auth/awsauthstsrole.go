package auth

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=awsauthstsroles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=awsauthstsroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=awsauthstsroles/finalizers,verbs=update

// SetupAWSAuthSTSRole registers the AWSAuthSTSRole controller with the manager.
func SetupAWSAuthSTSRole(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPluginKind(mgr, conns, opts, "awsauthstsrole-controller", AWSAuthSTSRoleAdapter{},
		func() *authv1alpha1.AWSAuthSTSRole { return &authv1alpha1.AWSAuthSTSRole{} })
}

// AWSAuthSTSRoleAdapter maps an AWSAuthSTSRole onto auth/<mountPath>/config/sts/<accountID>.
type AWSAuthSTSRoleAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.AWSAuthSTSRole]  = AWSAuthSTSRoleAdapter{}
	_ engine.DriftTier                              = AWSAuthSTSRoleAdapter{}
	_ engine.Observer[*authv1alpha1.AWSAuthSTSRole] = AWSAuthSTSRoleAdapter{}
)

// Path implements engine.Adapter.
func (AWSAuthSTSRoleAdapter) Path(o *authv1alpha1.AWSAuthSTSRole) string {
	return "auth/" + authMount(o.Spec.MountPath, "aws") + "/config/sts/" + o.Spec.AccountID
}

// Desired implements engine.Adapter.
func (AWSAuthSTSRoleAdapter) Desired(_ context.Context, _ engine.Resolver, o *authv1alpha1.AWSAuthSTSRole) (engine.Payload, error) {
	p := engine.Payload{"sts_role": o.Spec.STSRole}
	putString(p, "external_id", o.Spec.ExternalID)
	return p, nil
}

// Fields implements engine.Adapter.
func (AWSAuthSTSRoleAdapter) Fields() engine.FieldSpec { return engine.FieldSpec{} }

// DriftTier implements engine.DriftTier.
func (AWSAuthSTSRoleAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// Observe implements engine.Observer; it reports a missing auth method as ErrAuthMountMissing.
func (a AWSAuthSTSRoleAdapter) Observe(ctx context.Context, c openbao.Client, o *authv1alpha1.AWSAuthSTSRole) (engine.Payload, bool, error) {
	return observeExternalAuth(ctx, c, a.Path(o), authMount(o.Spec.MountPath, "aws"), "aws")
}
