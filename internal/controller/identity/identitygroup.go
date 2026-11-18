package identity

import (
	"context"
	"errors"
	"fmt"

	ctrl "sigs.k8s.io/controller-runtime"

	identityv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/identity/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=identity.obco.dev,resources=identitygroups,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=identity.obco.dev,resources=identitygroups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=identity.obco.dev,resources=identitygroups/finalizers,verbs=update

// SetupIdentityGroup registers the IdentityGroup controller with the manager.
func SetupIdentityGroup(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*identityv1alpha1.IdentityGroup](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("identitygroup-controller"),
		conns,
		IdentityGroupAdapter{},
		func() *identityv1alpha1.IdentityGroup { return &identityv1alpha1.IdentityGroup{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// IdentityGroupAdapter maps an IdentityGroup onto identity/group/name/<name>. An update by name keeps the keys it does not
// send, so the member lists, owned by IdentityGroupMembership resources, are never written here.
type IdentityGroupAdapter struct{}

var (
	_ engine.Adapter[*identityv1alpha1.IdentityGroup]      = IdentityGroupAdapter{}
	_ engine.Observer[*identityv1alpha1.IdentityGroup]     = IdentityGroupAdapter{}
	_ engine.StatusWriter[*identityv1alpha1.IdentityGroup] = IdentityGroupAdapter{}
	_ engine.Marker[*identityv1alpha1.IdentityGroup]       = IdentityGroupAdapter{}
	_ engine.DriftTier                                     = IdentityGroupAdapter{}
)

// Path implements engine.Adapter.
func (IdentityGroupAdapter) Path(g *identityv1alpha1.IdentityGroup) string {
	return "identity/group/name/" + specOrObjectName(g.Spec.Name, g.Name)
}

// Desired implements engine.Adapter.
func (IdentityGroupAdapter) Desired(_ context.Context, _ engine.Resolver, g *identityv1alpha1.IdentityGroup) (engine.Payload, error) {
	return engine.Payload{
		"type":     stringOr(string(g.Spec.Type), string(identityv1alpha1.GroupTypeInternal)),
		"policies": listOrEmpty(g.Spec.Policies),
		"metadata": mapOrEmpty(g.Spec.Metadata),
	}, nil
}

// Fields implements engine.Adapter.
func (IdentityGroupAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{CreateOnly: []string{"type"}, Sets: []string{"policies"}}
}

// Observe implements engine.Observer. A group of another type is an error rather than drift, because
// OpenBao cannot change the type of a group and a write would repeat forever.
func (a IdentityGroupAdapter) Observe(ctx context.Context, c openbao.Client, g *identityv1alpha1.IdentityGroup) (engine.Payload, bool, error) {
	path := a.Path(g)
	observed, err := c.Read(ctx, path)
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	if got, _ := observed["type"].(string); g.Spec.Type != "" && got != string(g.Spec.Type) {
		return nil, false, fmt.Errorf("%s exists with type %q but spec.type is %q; the type of a group cannot change",
			path, got, g.Spec.Type)
	}
	return observed, true, nil
}

// Mark implements engine.Marker.
func (IdentityGroupAdapter) Mark(_ *identityv1alpha1.IdentityGroup, desired engine.Payload, m engine.OwnershipMarker) engine.Payload {
	return markMetadata(desired, m)
}

// MarkerOf implements engine.Marker.
func (IdentityGroupAdapter) MarkerOf(observed engine.Payload) (engine.OwnershipMarker, bool) {
	return metadataMarker(observed)
}

// DriftTier implements engine.DriftTier: group policies grant access.
func (IdentityGroupAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// WriteStatus implements engine.StatusWriter.
func (IdentityGroupAdapter) WriteStatus(_ context.Context, _ openbao.Client, g *identityv1alpha1.IdentityGroup, observed engine.Payload) error {
	g.Status.ID, _ = observed["id"].(string)
	return nil
}
