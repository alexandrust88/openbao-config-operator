package auth

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=ociauthconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=ociauthconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=ociauthconfigs/finalizers,verbs=update

// SetupOCIAuthConfig registers the OCIAuthConfig controller with the manager.
func SetupOCIAuthConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPluginKind(mgr, conns, opts, "ociauthconfig-controller", OCIAuthConfigAdapter{},
		func() *authv1alpha1.OCIAuthConfig { return &authv1alpha1.OCIAuthConfig{} })
}

// OCIAuthConfigAdapter maps an OCIAuthConfig onto auth/<mountPath>/config of an OCI auth method.
type OCIAuthConfigAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.OCIAuthConfig]  = OCIAuthConfigAdapter{}
	_ engine.DriftTier                             = OCIAuthConfigAdapter{}
	_ engine.Observer[*authv1alpha1.OCIAuthConfig] = OCIAuthConfigAdapter{}
)

// Path implements engine.Adapter.
func (OCIAuthConfigAdapter) Path(o *authv1alpha1.OCIAuthConfig) string {
	return "auth/" + authMount(o.Spec.MountPath, "oci") + "/config"
}

// Desired implements engine.Adapter.
func (OCIAuthConfigAdapter) Desired(_ context.Context, _ engine.Resolver, o *authv1alpha1.OCIAuthConfig) (engine.Payload, error) {
	return engine.Payload{"home_tenancy_id": o.Spec.HomeTenancyID}, nil
}

// Fields implements engine.Adapter.
func (OCIAuthConfigAdapter) Fields() engine.FieldSpec { return engine.FieldSpec{} }

// DriftTier implements engine.DriftTier.
func (OCIAuthConfigAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// Observe implements engine.Observer; it reports a missing auth method as ErrAuthMountMissing.
func (a OCIAuthConfigAdapter) Observe(ctx context.Context, c openbao.Client, o *authv1alpha1.OCIAuthConfig) (engine.Payload, bool, error) {
	return observeExternalAuth(ctx, c, a.Path(o), authMount(o.Spec.MountPath, "oci"), "oci")
}
