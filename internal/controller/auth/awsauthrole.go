package auth

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=auth.obco.dev,resources=awsauthroles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=awsauthroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=awsauthroles/finalizers,verbs=update

// SetupAWSAuthRole registers the AWSAuthRole controller with the manager.
func SetupAWSAuthRole(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPluginKind(mgr, conns, opts, "awsauthrole-controller", AWSAuthRoleAdapter{},
		func() *authv1alpha1.AWSAuthRole { return &authv1alpha1.AWSAuthRole{} })
}

// AWSAuthRoleAdapter maps an AWSAuthRole onto auth/<mountPath>/role/<name> of an AWS auth method.
type AWSAuthRoleAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.AWSAuthRole]  = AWSAuthRoleAdapter{}
	_ engine.DriftTier                           = AWSAuthRoleAdapter{}
	_ engine.Observer[*authv1alpha1.AWSAuthRole] = AWSAuthRoleAdapter{}
)

var awsAuthRoleSets = []string{
	"bound_ami_id", "bound_account_id", "bound_region", "bound_vpc_id", "bound_subnet_id", "bound_iam_role_arn",
	"bound_iam_instance_profile_arn", "bound_ec2_instance_id", "bound_iam_principal_arn",
}

// Path implements engine.Adapter.
func (AWSAuthRoleAdapter) Path(o *authv1alpha1.AWSAuthRole) string {
	return "auth/" + authMount(o.Spec.MountPath, "aws") + "/role/" + authName(o.Spec.Name, o.Name)
}

// Desired implements engine.Adapter.
func (AWSAuthRoleAdapter) Desired(ctx context.Context, r engine.Resolver, o *authv1alpha1.AWSAuthRole) (engine.Payload, error) {
	s := o.Spec
	authType := s.AuthType
	if authType == "" {
		authType = "iam"
	}
	p := engine.Payload{"auth_type": authType}
	putList(p, "bound_ami_id", s.BoundAMIIDs)
	putList(p, "bound_account_id", s.BoundAccountIDs)
	putList(p, "bound_region", s.BoundRegions)
	putList(p, "bound_vpc_id", s.BoundVPCIDs)
	putList(p, "bound_subnet_id", s.BoundSubnetIDs)
	putList(p, "bound_iam_role_arn", s.BoundIAMRoleARNs)
	putList(p, "bound_iam_instance_profile_arn", s.BoundIAMInstanceProfileARNs)
	putList(p, "bound_ec2_instance_id", s.BoundEC2InstanceIDs)
	putList(p, "bound_iam_principal_arn", s.BoundIAMPrincipalARNs)
	putString(p, "role_tag", s.RoleTag)
	putString(p, "inferred_entity_type", s.InferredEntityType)
	putString(p, "inferred_aws_region", s.InferredAWSRegion)
	pluginPut(p, "resolve_aws_unique_ids", s.ResolveAWSUniqueIDs)
	pluginPut(p, "allow_instance_migration", s.AllowInstanceMigration)
	pluginPut(p, "disallow_reauthentication", s.DisallowReauthentication)
	if err := putTokenFields(ctx, r, o, p, s.TokenFields); err != nil {
		return nil, err
	}
	return p, nil
}

// Fields implements engine.Adapter.
func (AWSAuthRoleAdapter) Fields() engine.FieldSpec {
	return MergeFieldSpecs(engine.FieldSpec{Sets: awsAuthRoleSets}, TokenFieldSpec)
}

// DriftTier implements engine.DriftTier.
func (AWSAuthRoleAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// Observe implements engine.Observer; it reports a missing auth method as ErrAuthMountMissing.
func (a AWSAuthRoleAdapter) Observe(ctx context.Context, c openbao.Client, o *authv1alpha1.AWSAuthRole) (engine.Payload, bool, error) {
	return observeExternalAuth(ctx, c, a.Path(o), authMount(o.Spec.MountPath, "aws"), "aws")
}
