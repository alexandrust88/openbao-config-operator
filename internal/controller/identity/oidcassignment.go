package identity

import (
	"context"
	"strings"

	ctrl "sigs.k8s.io/controller-runtime"

	identityv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/identity/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/refs"
)

// +kubebuilder:rbac:groups=identity.openbaoconfig.io,resources=oidcassignments,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=identity.openbaoconfig.io,resources=oidcassignments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=identity.openbaoconfig.io,resources=oidcassignments/finalizers,verbs=update
// +kubebuilder:rbac:groups=identity.openbaoconfig.io,resources=entities;groups,verbs=get;list;watch

// SetupOIDCAssignment registers the OIDCAssignment controller with the manager.
func SetupOIDCAssignment(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*identityv1alpha1.OIDCAssignment](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("oidcassignment-controller"),
		conns,
		OIDCAssignmentAdapter{Reader: mgr.GetClient(), Conns: conns},
		func() *identityv1alpha1.OIDCAssignment { return &identityv1alpha1.OIDCAssignment{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// OIDCAssignmentAdapter maps an OIDCAssignment onto identity/oidc/assignment/<name>. Reader and Conns resolve the
// entity and group references.
type OIDCAssignmentAdapter struct {
	Reader refs.Reader
	Conns  engine.Connections
}

var _ engine.Adapter[*identityv1alpha1.OIDCAssignment] = OIDCAssignmentAdapter{}

// Path implements engine.Adapter.
func (OIDCAssignmentAdapter) Path(a *identityv1alpha1.OIDCAssignment) string {
	return "identity/oidc/assignment/" + oidcName(a.Spec.Name, a.Name)
}

// Desired implements engine.Adapter. Both lists are always sent so that removed members are removed.
func (a OIDCAssignmentAdapter) Desired(ctx context.Context, _ engine.Resolver, o *identityv1alpha1.OIDCAssignment) (engine.Payload, error) {
	r := refs.For(a.Reader, a.Conns, o)
	entities, err := resolveMatches(ctx, r, refs.Entity, entityMatchRefs(o.Spec.Entities))
	if err != nil {
		return nil, err
	}
	groups, err := resolveMatches(ctx, r, refs.Group, groupMatchRefs(o.Spec.Groups))
	if err != nil {
		return nil, err
	}
	return engine.Payload{"entity_ids": lowercaseIDs(entities), "group_ids": lowercaseIDs(groups)}, nil
}

// Fields implements engine.Adapter.
func (OIDCAssignmentAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{Sets: []string{"entity_ids", "group_ids"}}
}

// lowercaseIDs lowercases the ID part of "<id>" or "<id>.<namespace id>", as OpenBao does when it stores them.
func lowercaseIDs(ids []string) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		bare, namespaceID, ok := strings.Cut(id, ".")
		if !ok {
			out[i] = strings.ToLower(id)
			continue
		}
		out[i] = strings.ToLower(bare) + "." + namespaceID
	}
	return out
}
