package auth

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=auth.obco.dev,resources=cfauthroles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=cfauthroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=cfauthroles/finalizers,verbs=update

// SetupCFAuthRole registers the CFAuthRole controller with the manager.
func SetupCFAuthRole(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPluginKind(mgr, conns, opts, "cfauthrole-controller", CFAuthRoleAdapter{},
		func() *authv1alpha1.CFAuthRole { return &authv1alpha1.CFAuthRole{} })
}

// CFAuthRoleAdapter maps a CFAuthRole onto auth/<mountPath>/roles/<name> of a Cloud Foundry auth method.
type CFAuthRoleAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.CFAuthRole]  = CFAuthRoleAdapter{}
	_ engine.DriftTier                          = CFAuthRoleAdapter{}
	_ engine.Observer[*authv1alpha1.CFAuthRole] = CFAuthRoleAdapter{}
)

// Path implements engine.Adapter.
func (CFAuthRoleAdapter) Path(o *authv1alpha1.CFAuthRole) string {
	return "auth/" + authMount(o.Spec.MountPath, "cf") + "/roles/" + authName(o.Spec.Name, o.Name)
}

// Desired implements engine.Adapter.
func (CFAuthRoleAdapter) Desired(ctx context.Context, r engine.Resolver, o *authv1alpha1.CFAuthRole) (engine.Payload, error) {
	s := o.Spec
	p := engine.Payload{}
	putList(p, "bound_application_ids", s.BoundApplicationIDs)
	putList(p, "bound_space_ids", s.BoundSpaceIDs)
	putList(p, "bound_organization_ids", s.BoundOrganizationIDs)
	putList(p, "bound_instance_ids", s.BoundInstanceIDs)
	pluginPut(p, "disable_ip_matching", s.DisableIPMatching)
	if err := putTokenFields(ctx, r, o, p, s.TokenFields); err != nil {
		return nil, err
	}
	return p, nil
}

// Fields implements engine.Adapter.
func (CFAuthRoleAdapter) Fields() engine.FieldSpec {
	return MergeFieldSpecs(engine.FieldSpec{Sets: []string{
		"bound_application_ids", "bound_space_ids", "bound_organization_ids", "bound_instance_ids",
	}}, TokenFieldSpec)
}

// DriftTier implements engine.DriftTier.
func (CFAuthRoleAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// Observe implements engine.Observer; it reports a missing auth method as ErrAuthMountMissing.
func (a CFAuthRoleAdapter) Observe(ctx context.Context, c openbao.Client, o *authv1alpha1.CFAuthRole) (engine.Payload, bool, error) {
	return observeExternalAuth(ctx, c, a.Path(o), authMount(o.Spec.MountPath, "cf"), "cf")
}
