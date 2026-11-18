package auth

import (
	"context"
	"fmt"
	"slices"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=auth.obco.dev,resources=gcpauthroles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=gcpauthroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=gcpauthroles/finalizers,verbs=update

// SetupGCPAuthRole registers the GCPAuthRole controller with the manager.
func SetupGCPAuthRole(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPluginKind(mgr, conns, opts, "gcpauthrole-controller", GCPAuthRoleAdapter{},
		func() *authv1alpha1.GCPAuthRole { return &authv1alpha1.GCPAuthRole{} })
}

// GCPAuthRoleAdapter maps a GCPAuthRole onto auth/<mountPath>/role/<name> of a GCP auth method.
type GCPAuthRoleAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.GCPAuthRole]  = GCPAuthRoleAdapter{}
	_ engine.DriftTier                           = GCPAuthRoleAdapter{}
	_ engine.Observer[*authv1alpha1.GCPAuthRole] = GCPAuthRoleAdapter{}
)

// Path implements engine.Adapter.
func (GCPAuthRoleAdapter) Path(o *authv1alpha1.GCPAuthRole) string {
	return "auth/" + authMount(o.Spec.MountPath, "gcp") + "/role/" + authName(o.Spec.Name, o.Name)
}

// Desired implements engine.Adapter. boundLabels is written as "key:value" strings, the form OpenBao accepts.
func (GCPAuthRoleAdapter) Desired(ctx context.Context, r engine.Resolver, o *authv1alpha1.GCPAuthRole) (engine.Payload, error) {
	s := o.Spec
	p := engine.Payload{"type": s.Type}
	putList(p, "bound_service_accounts", s.BoundServiceAccounts)
	putList(p, "bound_projects", s.BoundProjects)
	pluginPut(p, "add_group_aliases", s.AddGroupAliases)
	putSeconds(p, "max_jwt_exp", s.MaxJWTExp)
	pluginPut(p, "allow_gce_inference", s.AllowGCEInference)
	putList(p, "bound_zones", s.BoundZones)
	putList(p, "bound_regions", s.BoundRegions)
	putList(p, "bound_instance_groups", s.BoundInstanceGroups)
	if s.BoundLabels != nil {
		p["bound_labels"] = gcpLabelList(s.BoundLabels)
	}
	if err := putTokenFields(ctx, r, o, p, s.TokenFields); err != nil {
		return nil, err
	}
	return p, nil
}

// Fields implements engine.Adapter. Older plugin versions return the role type as role_type.
func (GCPAuthRoleAdapter) Fields() engine.FieldSpec {
	return MergeFieldSpecs(engine.FieldSpec{
		Sets: []string{
			"bound_service_accounts", "bound_projects", "bound_zones", "bound_regions", "bound_instance_groups",
			"bound_labels",
		},
		Durations: []string{"max_jwt_exp"},
		Aliases:   map[string]string{"role_type": "type"},
	}, TokenFieldSpec)
}

// DriftTier implements engine.DriftTier.
func (GCPAuthRoleAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// Observe implements engine.Observer. The read returns bound_labels as a map; it is turned into the written
// "key:value" list. A missing auth method is reported as ErrAuthMountMissing.
func (a GCPAuthRoleAdapter) Observe(ctx context.Context, c openbao.Client, o *authv1alpha1.GCPAuthRole) (engine.Payload, bool, error) {
	observed, found, err := observeExternalAuth(ctx, c, a.Path(o), authMount(o.Spec.MountPath, "gcp"), "gcp")
	if !found || err != nil {
		return observed, found, err
	}
	if labels, ok := observed["bound_labels"].(map[string]any); ok {
		converted := make(map[string]string, len(labels))
		for key, value := range labels {
			converted[key] = fmt.Sprint(value)
		}
		observed["bound_labels"] = gcpLabelList(converted)
	}
	return observed, true, nil
}

func gcpLabelList(labels map[string]string) []string {
	list := make([]string, 0, len(labels))
	for key, value := range labels {
		list = append(list, key+":"+value)
	}
	slices.Sort(list)
	return list
}
