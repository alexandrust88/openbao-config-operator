package auth

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=auth.obco.dev,resources=gcpauthconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=gcpauthconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=gcpauthconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// SetupGCPAuthConfig registers the GCPAuthConfig controller with the manager.
func SetupGCPAuthConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPluginKind(mgr, conns, opts, "gcpauthconfig-controller", GCPAuthConfigAdapter{},
		func() *authv1alpha1.GCPAuthConfig { return &authv1alpha1.GCPAuthConfig{} })
}

// GCPAuthConfigAdapter maps a GCPAuthConfig onto auth/<mountPath>/config of a GCP auth method.
type GCPAuthConfigAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.GCPAuthConfig]          = GCPAuthConfigAdapter{}
	_ engine.DriftTier                                     = GCPAuthConfigAdapter{}
	_ engine.Observer[*authv1alpha1.GCPAuthConfig]         = GCPAuthConfigAdapter{}
	_ engine.SecretReferencer[*authv1alpha1.GCPAuthConfig] = GCPAuthConfigAdapter{}
)

// Path implements engine.Adapter.
func (GCPAuthConfigAdapter) Path(o *authv1alpha1.GCPAuthConfig) string {
	return "auth/" + authMount(o.Spec.MountPath, "gcp") + "/config"
}

// Desired implements engine.Adapter.
func (GCPAuthConfigAdapter) Desired(ctx context.Context, r engine.Resolver, o *authv1alpha1.GCPAuthConfig) (engine.Payload, error) {
	s := o.Spec
	p := engine.Payload{}
	if err := putCloudSecret(ctx, r, p, o.Namespace, "credentials", s.CredentialsSecretRef); err != nil {
		return nil, err
	}
	putString(p, "iam_alias", s.IAMAlias)
	putList(p, "iam_metadata", s.IAMMetadata)
	putString(p, "gce_alias", s.GCEAlias)
	putList(p, "gce_metadata", s.GCEMetadata)
	if e := s.CustomEndpoint; e != nil {
		endpoints := engine.Payload{}
		putString(endpoints, "api", e.API)
		putString(endpoints, "iam", e.IAM)
		putString(endpoints, "crm", e.CRM)
		putString(endpoints, "compute", e.Compute)
		p["custom_endpoint"] = endpoints
	}
	return p, nil
}

// Fields implements engine.Adapter.
func (GCPAuthConfigAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{WriteOnly: []string{"credentials"}, Sets: []string{"iam_metadata", "gce_metadata"}}
}

// DriftTier implements engine.DriftTier.
func (GCPAuthConfigAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// Observe implements engine.Observer; it reports a missing auth method as ErrAuthMountMissing.
func (a GCPAuthConfigAdapter) Observe(ctx context.Context, c openbao.Client, o *authv1alpha1.GCPAuthConfig) (engine.Payload, bool, error) {
	return observeExternalAuth(ctx, c, a.Path(o), authMount(o.Spec.MountPath, "gcp"), "gcp")
}

// SecretRefs implements engine.SecretReferencer.
func (GCPAuthConfigAdapter) SecretRefs(o *authv1alpha1.GCPAuthConfig) []string {
	return cloudSecretNames(o.Spec.CredentialsSecretRef)
}
