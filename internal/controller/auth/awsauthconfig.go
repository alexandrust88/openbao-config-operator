package auth

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=auth.obco.dev,resources=awsauthconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=awsauthconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=awsauthconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// SetupAWSAuthConfig registers the AWSAuthConfig controller with the manager.
func SetupAWSAuthConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPluginKind(mgr, conns, opts, "awsauthconfig-controller", AWSAuthConfigAdapter{},
		func() *authv1alpha1.AWSAuthConfig { return &authv1alpha1.AWSAuthConfig{} })
}

// AWSAuthConfigAdapter maps an AWSAuthConfig onto auth/<mountPath>/config/client of an AWS auth method.
type AWSAuthConfigAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.AWSAuthConfig]          = AWSAuthConfigAdapter{}
	_ engine.DriftTier                                     = AWSAuthConfigAdapter{}
	_ engine.Observer[*authv1alpha1.AWSAuthConfig]         = AWSAuthConfigAdapter{}
	_ engine.SecretReferencer[*authv1alpha1.AWSAuthConfig] = AWSAuthConfigAdapter{}
)

// Path implements engine.Adapter.
func (AWSAuthConfigAdapter) Path(o *authv1alpha1.AWSAuthConfig) string {
	return "auth/" + authMount(o.Spec.MountPath, "aws") + "/config/client"
}

// Desired implements engine.Adapter. The access key is compared with the read; the secret key is write-only.
func (AWSAuthConfigAdapter) Desired(ctx context.Context, r engine.Resolver, o *authv1alpha1.AWSAuthConfig) (engine.Payload, error) {
	s := o.Spec
	p := engine.Payload{}
	if err := putCloudSecret(ctx, r, p, o.Namespace, "access_key", s.AccessKeySecretRef); err != nil {
		return nil, err
	}
	if err := putCloudSecret(ctx, r, p, o.Namespace, "secret_key", s.SecretKeySecretRef); err != nil {
		return nil, err
	}
	putString(p, "endpoint", s.EC2Endpoint)
	putString(p, "iam_endpoint", s.IAMEndpoint)
	putString(p, "sts_endpoint", s.STSEndpoint)
	putString(p, "sts_region", s.STSRegion)
	pluginPut(p, "use_sts_region_from_client", s.UseSTSRegionFromClient)
	putString(p, "iam_server_id_header_value", s.IAMServerIDHeaderValue)
	putList(p, "allowed_sts_header_values", s.AllowedSTSHeaderValues)
	pluginPut(p, "max_retries", s.MaxRetries)
	return p, nil
}

// Fields implements engine.Adapter.
func (AWSAuthConfigAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{WriteOnly: []string{"secret_key"}, Sets: []string{"allowed_sts_header_values"}}
}

// DriftTier implements engine.DriftTier.
func (AWSAuthConfigAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// Observe implements engine.Observer; it reports a missing auth method as ErrAuthMountMissing.
func (a AWSAuthConfigAdapter) Observe(ctx context.Context, c openbao.Client, o *authv1alpha1.AWSAuthConfig) (engine.Payload, bool, error) {
	return observeExternalAuth(ctx, c, a.Path(o), authMount(o.Spec.MountPath, "aws"), "aws")
}

// SecretRefs implements engine.SecretReferencer.
func (AWSAuthConfigAdapter) SecretRefs(o *authv1alpha1.AWSAuthConfig) []string {
	return cloudSecretNames(o.Spec.AccessKeySecretRef, o.Spec.SecretKeySecretRef)
}
