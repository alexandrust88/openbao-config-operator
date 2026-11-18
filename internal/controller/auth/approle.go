package auth

import (
	"context"
	"errors"
	"fmt"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=auth.obco.dev,resources=approles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=approles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.obco.dev,resources=approles/finalizers,verbs=update

// SetupAppRole registers the AppRole controller with the manager.
func SetupAppRole(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*authv1alpha1.AppRole](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("approle-controller"),
		conns,
		AppRoleAdapter{},
		func() *authv1alpha1.AppRole { return &authv1alpha1.AppRole{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// AppRoleAdapter maps an AppRole onto auth/<mount>/role/<name>.
type AppRoleAdapter struct{}

var (
	_ engine.Adapter[*authv1alpha1.AppRole]      = AppRoleAdapter{}
	_ engine.DriftTier                           = AppRoleAdapter{}
	_ engine.Observer[*authv1alpha1.AppRole]     = AppRoleAdapter{}
	_ engine.StatusWriter[*authv1alpha1.AppRole] = AppRoleAdapter{}
)

// Path implements engine.Adapter.
func (AppRoleAdapter) Path(a *authv1alpha1.AppRole) string { return appRolePath(a) }

// Desired implements engine.Adapter. Unset fields are sent as the OpenBao default, so removing one resets it; an
// unset roleID leaves the generated role ID in place.
func (AppRoleAdapter) Desired(ctx context.Context, r engine.Resolver, a *authv1alpha1.AppRole) (engine.Payload, error) {
	s := a.Spec
	p := engine.Payload{
		"bind_secret_id":        s.BindSecretID == nil || *s.BindSecretID,
		"secret_id_bound_cidrs": builtinList(s.SecretIDBoundCIDRs),
		"secret_id_num_uses":    int64(0),
		"secret_id_ttl":         int64(0),
		"local_secret_ids":      builtinBool(s.LocalSecretIDs),
	}
	putString(p, "role_id", s.RoleID)
	credPutInt32(p, "secret_id_num_uses", s.SecretIDNumUses)
	putSeconds(p, "secret_id_ttl", s.SecretIDTTL)
	if err := putTokenFields(ctx, r, a, p, s.TokenFields); err != nil {
		return nil, err
	}
	putTokenFieldDefaults(p)
	return p, nil
}

// Fields implements engine.Adapter.
func (AppRoleAdapter) Fields() engine.FieldSpec {
	return MergeFieldSpecs(TokenFieldSpec, engine.FieldSpec{
		CreateOnly: []string{"local_secret_ids"},
		Sets:       []string{"secret_id_bound_cidrs"},
		Durations:  []string{"secret_id_ttl"},
	})
}

// DriftTier implements engine.DriftTier.
func (AppRoleAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// Observe implements engine.Observer. The role read omits role_id, so it is read from role-id and merged in.
func (AppRoleAdapter) Observe(ctx context.Context, c openbao.Client, a *authv1alpha1.AppRole) (engine.Payload, bool, error) {
	path := appRolePath(a)
	observed, err := c.Read(ctx, path)
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	roleID, err := c.Read(ctx, path+"/role-id")
	if err != nil {
		return nil, false, fmt.Errorf("read %s/role-id: %w", path, err)
	}
	observed["role_id"] = roleID["role_id"]
	return observed, true, nil
}

// WriteStatus implements engine.StatusWriter: the role ID goes to status.id.
func (AppRoleAdapter) WriteStatus(_ context.Context, _ openbao.Client, a *authv1alpha1.AppRole, observed engine.Payload) error {
	if roleID, ok := observed["role_id"].(string); ok {
		a.Status.ID = roleID
	}
	return nil
}

func appRolePath(a *authv1alpha1.AppRole) string {
	return "auth/" + authMount(a.Spec.MountPath, "approle") + "/role/" + authName(a.Spec.Name, a.Name)
}
