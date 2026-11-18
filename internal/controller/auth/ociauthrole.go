package auth

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=auth.obco.dev,resources=ociauthroles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=ociauthroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=ociauthroles/finalizers,verbs=update

// SetupOCIAuthRole registers the OCIAuthRole controller with the manager.
func SetupOCIAuthRole(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPluginKind(mgr, conns, opts, "ociauthrole-controller", OCIAuthRoleAdapter{},
		func() *authv1alpha1.OCIAuthRole { return &authv1alpha1.OCIAuthRole{} })
}

// OCIAuthRoleAdapter maps an OCIAuthRole onto auth/<mountPath>/role/<name> of an OCI auth method.
type OCIAuthRoleAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.OCIAuthRole]  = OCIAuthRoleAdapter{}
	_ engine.DriftTier                           = OCIAuthRoleAdapter{}
	_ engine.Observer[*authv1alpha1.OCIAuthRole] = OCIAuthRoleAdapter{}
)

// Path implements engine.Adapter.
func (OCIAuthRoleAdapter) Path(o *authv1alpha1.OCIAuthRole) string {
	return "auth/" + authMount(o.Spec.MountPath, "oci") + "/role/" + authName(o.Spec.Name, o.Name)
}

// Desired implements engine.Adapter.
func (OCIAuthRoleAdapter) Desired(ctx context.Context, r engine.Resolver, o *authv1alpha1.OCIAuthRole) (engine.Payload, error) {
	p := engine.Payload{}
	putList(p, "ocid_list", o.Spec.OCIDList)
	if err := putTokenFields(ctx, r, o, p, o.Spec.TokenFields); err != nil {
		return nil, err
	}
	return p, nil
}

// Fields implements engine.Adapter.
func (OCIAuthRoleAdapter) Fields() engine.FieldSpec {
	return MergeFieldSpecs(engine.FieldSpec{Sets: []string{"ocid_list"}}, TokenFieldSpec)
}

// DriftTier implements engine.DriftTier.
func (OCIAuthRoleAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// Observe implements engine.Observer; it reports a missing auth method as ErrAuthMountMissing.
func (a OCIAuthRoleAdapter) Observe(ctx context.Context, c openbao.Client, o *authv1alpha1.OCIAuthRole) (engine.Payload, bool, error) {
	return observeExternalAuth(ctx, c, a.Path(o), authMount(o.Spec.MountPath, "oci"), "oci")
}
