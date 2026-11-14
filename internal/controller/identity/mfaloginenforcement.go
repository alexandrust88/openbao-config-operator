package identity

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	identityv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/identity/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
	"github.com/alexandrust88/openbao-config-operator/internal/refs"
)

// +kubebuilder:rbac:groups=identity.openbaoconfig.io,resources=mfaloginenforcements,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=identity.openbaoconfig.io,resources=mfaloginenforcements/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=identity.openbaoconfig.io,resources=mfaloginenforcements/finalizers,verbs=update
// +kubebuilder:rbac:groups=identity.openbaoconfig.io,resources=mfamethods;entities;groups,verbs=get;list;watch
// +kubebuilder:rbac:groups=sys.openbaoconfig.io,resources=authmounts,verbs=get;list;watch

// SetupMFALoginEnforcement registers the MFALoginEnforcement controller with the manager.
func SetupMFALoginEnforcement(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*identityv1alpha1.MFALoginEnforcement](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("mfaloginenforcement-controller"),
		conns,
		MFALoginEnforcementAdapter{Reader: mgr.GetClient(), Conns: conns},
		func() *identityv1alpha1.MFALoginEnforcement { return &identityv1alpha1.MFALoginEnforcement{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// MFALoginEnforcementAdapter maps an MFALoginEnforcement onto identity/mfa/login-enforcement/<name>. Reader and
// Conns resolve the method, mount, entity and group references.
type MFALoginEnforcementAdapter struct {
	Reader refs.Reader
	Conns  engine.Connections
}

var (
	_ engine.Adapter[*identityv1alpha1.MFALoginEnforcement]      = MFALoginEnforcementAdapter{}
	_ engine.StatusWriter[*identityv1alpha1.MFALoginEnforcement] = MFALoginEnforcementAdapter{}
)

// Path implements engine.Adapter.
func (MFALoginEnforcementAdapter) Path(e *identityv1alpha1.MFALoginEnforcement) string {
	return "identity/mfa/login-enforcement/" + oidcName(e.Spec.Name, e.Name)
}

// Desired implements engine.Adapter. The four target lists are always sent, so a target removed from the spec is
// removed in OpenBao.
func (a MFALoginEnforcementAdapter) Desired(ctx context.Context, _ engine.Resolver, e *identityv1alpha1.MFALoginEnforcement) (engine.Payload, error) {
	r := refs.For(a.Reader, a.Conns, e)
	lists := []struct {
		key     string
		target  refs.Target
		matches []refs.Ref
	}{
		{"mfa_method_ids", mfaMethodTarget, mfaMethodMatchRefs(e.Spec.MFAMethods)},
		{"auth_method_accessors", refs.AuthMount, authMountMatchRefs(e.Spec.AuthMounts)},
		{"identity_entity_ids", refs.Entity, entityMatchRefs(e.Spec.Entities)},
		{"identity_group_ids", refs.Group, groupMatchRefs(e.Spec.Groups)},
	}
	p := engine.Payload{}
	for _, l := range lists {
		values, err := resolveMatches(ctx, r, l.target, l.matches)
		if err != nil {
			return nil, err
		}
		p[l.key] = values
	}
	types := e.Spec.AuthMethodTypes
	if types == nil {
		types = []string{}
	}
	p["auth_method_types"] = types
	return p, nil
}

// Fields implements engine.Adapter.
func (MFALoginEnforcementAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{
		Sets: []string{"mfa_method_ids", "auth_method_accessors", "auth_method_types", "identity_entity_ids", "identity_group_ids"},
	}
}

// WriteStatus implements engine.StatusWriter: status.id is the enforcement ID.
func (MFALoginEnforcementAdapter) WriteStatus(_ context.Context, _ openbao.Client, e *identityv1alpha1.MFALoginEnforcement, observed engine.Payload) error {
	if id, ok := observed["id"].(string); ok && id != "" {
		e.Status.ID = id
	}
	return nil
}
