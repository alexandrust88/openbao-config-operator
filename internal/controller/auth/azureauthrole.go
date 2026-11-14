package auth

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=azureauthroles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=azureauthroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=azureauthroles/finalizers,verbs=update

// SetupAzureAuthRole registers the AzureAuthRole controller with the manager.
func SetupAzureAuthRole(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPluginKind(mgr, conns, opts, "azureauthrole-controller", AzureAuthRoleAdapter{},
		func() *authv1alpha1.AzureAuthRole { return &authv1alpha1.AzureAuthRole{} })
}

// AzureAuthRoleAdapter maps an AzureAuthRole onto auth/<mountPath>/role/<name> of an Azure auth method.
type AzureAuthRoleAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.AzureAuthRole]  = AzureAuthRoleAdapter{}
	_ engine.DriftTier                             = AzureAuthRoleAdapter{}
	_ engine.Observer[*authv1alpha1.AzureAuthRole] = AzureAuthRoleAdapter{}
)

// Path implements engine.Adapter.
func (AzureAuthRoleAdapter) Path(o *authv1alpha1.AzureAuthRole) string {
	return "auth/" + authMount(o.Spec.MountPath, "azure") + "/role/" + authName(o.Spec.Name, o.Name)
}

// Desired implements engine.Adapter.
func (AzureAuthRoleAdapter) Desired(ctx context.Context, r engine.Resolver, o *authv1alpha1.AzureAuthRole) (engine.Payload, error) {
	s := o.Spec
	p := engine.Payload{}
	putList(p, "bound_service_principal_ids", s.BoundServicePrincipalIDs)
	putList(p, "bound_group_ids", s.BoundGroupIDs)
	putList(p, "bound_locations", s.BoundLocations)
	putList(p, "bound_subscription_ids", s.BoundSubscriptionIDs)
	putList(p, "bound_resource_groups", s.BoundResourceGroups)
	putList(p, "bound_scale_sets", s.BoundScaleSets)
	if err := putTokenFields(ctx, r, o, p, s.TokenFields); err != nil {
		return nil, err
	}
	return p, nil
}

// Fields implements engine.Adapter.
func (AzureAuthRoleAdapter) Fields() engine.FieldSpec {
	return MergeFieldSpecs(engine.FieldSpec{Sets: []string{
		"bound_service_principal_ids", "bound_group_ids", "bound_locations", "bound_subscription_ids",
		"bound_resource_groups", "bound_scale_sets",
	}}, TokenFieldSpec)
}

// DriftTier implements engine.DriftTier.
func (AzureAuthRoleAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// Observe implements engine.Observer; it reports a missing auth method as ErrAuthMountMissing.
func (a AzureAuthRoleAdapter) Observe(ctx context.Context, c openbao.Client, o *authv1alpha1.AzureAuthRole) (engine.Payload, bool, error) {
	return observeExternalAuth(ctx, c, a.Path(o), authMount(o.Spec.MountPath, "azure"), "azure")
}
