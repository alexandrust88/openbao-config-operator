package identity

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	identityv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/identity/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
	"github.com/alexandrust88/openbao-config-operator/internal/refs"
)

const groupAliasCollection = "identity/group-alias"

// +kubebuilder:rbac:groups=identity.obco.dev,resources=identitygroupaliases,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=identity.obco.dev,resources=identitygroupaliases/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=identity.obco.dev,resources=identitygroupaliases/finalizers,verbs=update
// +kubebuilder:rbac:groups=identity.obco.dev,resources=identitygroups,verbs=get;list;watch
// +kubebuilder:rbac:groups=sys.obco.dev,resources=authmounts,verbs=get;list;watch

// SetupIdentityGroupAlias registers the IdentityGroupAlias controller with the manager.
func SetupIdentityGroupAlias(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*identityv1alpha1.IdentityGroupAlias](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("identitygroupalias-controller"),
		conns,
		IdentityGroupAliasAdapter{Reader: mgr.GetClient(), Conns: conns},
		func() *identityv1alpha1.IdentityGroupAlias { return &identityv1alpha1.IdentityGroupAlias{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// IdentityGroupAliasAdapter maps an IdentityGroupAlias onto identity/group-alias. An existing group alias can only be updated
// by ID, so the writer creates on the collection and updates on identity/group-alias/id/<id>. OpenBao itself
// refuses an alias on an internal group.
type IdentityGroupAliasAdapter struct {
	Reader refs.Reader
	Conns  engine.Connections
}

var (
	_ engine.Adapter[*identityv1alpha1.IdentityGroupAlias]      = IdentityGroupAliasAdapter{}
	_ engine.Observer[*identityv1alpha1.IdentityGroupAlias]     = IdentityGroupAliasAdapter{}
	_ engine.Writer[*identityv1alpha1.IdentityGroupAlias]       = IdentityGroupAliasAdapter{}
	_ engine.Deleter[*identityv1alpha1.IdentityGroupAlias]      = IdentityGroupAliasAdapter{}
	_ engine.StatusWriter[*identityv1alpha1.IdentityGroupAlias] = IdentityGroupAliasAdapter{}
)

// Path implements engine.Adapter: the alias by ID once known, the collection it is created in before.
func (IdentityGroupAliasAdapter) Path(ga *identityv1alpha1.IdentityGroupAlias) string {
	return aliasPath(groupAliasCollection, ga.Status.ID)
}

// Desired implements engine.Adapter.
func (a IdentityGroupAliasAdapter) Desired(ctx context.Context, _ engine.Resolver, ga *identityv1alpha1.IdentityGroupAlias) (engine.Payload, error) {
	r := refs.For(a.Reader, a.Conns, ga)
	accessor, err := r.One(ctx, refs.AuthMount, groupAliasMount(ga))
	if err != nil {
		return nil, err
	}
	groupID, err := r.One(ctx, refs.Group, refs.Ref{Literal: ga.Spec.GroupID, Name: ga.Spec.GroupName,
		ObjectName: objectRefName(ga.Spec.GroupRef), MatchLabels: selectorLabels(ga.Spec.GroupSelector)})
	if err != nil {
		return nil, err
	}
	return engine.Payload{aliasNameKey: ga.Spec.AliasName, aliasAccessorKey: accessor, aliasCanonicalIDKey: groupID}, nil
}

// Fields implements engine.Adapter.
func (IdentityGroupAliasAdapter) Fields() engine.FieldSpec { return engine.FieldSpec{} }

// Observe implements engine.Observer.
func (a IdentityGroupAliasAdapter) Observe(ctx context.Context, c openbao.Client, ga *identityv1alpha1.IdentityGroupAlias) (engine.Payload, bool, error) {
	alias, found, err := readAlias(ctx, c, groupAliasCollection, ga.Status.ID)
	if err != nil {
		return nil, false, err
	}
	if found {
		if ga.Status.Acquired == "" {
			if err := checkAliasOwner(ctx, a.Reader, c, ga, &identityv1alpha1.IdentityGroupAliasList{}, groupAliasCollection, ga.Status.ID); err != nil {
				return nil, false, err
			}
		}
		return alias, true, nil
	}
	accessor, err := refs.WithClient(a.Reader, c, ga.Namespace).One(ctx, refs.AuthMount, groupAliasMount(ga))
	if err != nil {
		return nil, false, err
	}
	key := aliasKey{accessor: accessor, name: ga.Spec.AliasName}
	group, err := lookup(ctx, c, "identity/lookup/group", key)
	if err != nil || group == nil {
		return nil, false, err
	}
	if alias, ok := group["alias"].(map[string]any); ok && key.matches(alias) {
		id, _ := alias["id"].(string)
		if err := checkAliasOwner(ctx, a.Reader, c, ga, &identityv1alpha1.IdentityGroupAliasList{}, groupAliasCollection, id); err != nil {
			return nil, false, err
		}
		return alias, true, nil
	}
	return nil, false, nil
}

// Write implements engine.Writer.
func (a IdentityGroupAliasAdapter) Write(ctx context.Context, c openbao.Client, ga *identityv1alpha1.IdentityGroupAlias,
	desired engine.Payload, create bool) (engine.Payload, error) {
	return writeAlias(ctx, c, groupAliasCollection, desired, create, func() (string, error) {
		return aliasIDFrom(func() (engine.Payload, bool, error) { return a.Observe(ctx, c, ga) }, ga.Spec.AliasName)
	})
}

// Delete implements engine.Deleter.
func (a IdentityGroupAliasAdapter) Delete(ctx context.Context, c openbao.Client, ga *identityv1alpha1.IdentityGroupAlias) error {
	id := ga.Status.ID
	if id == "" {
		alias, found, err := a.Observe(ctx, c, ga)
		if err != nil || !found {
			return err
		}
		id, _ = alias["id"].(string)
	}
	return deleteAlias(ctx, c, groupAliasCollection, id)
}

// WriteStatus implements engine.StatusWriter.
func (IdentityGroupAliasAdapter) WriteStatus(_ context.Context, _ openbao.Client, ga *identityv1alpha1.IdentityGroupAlias, observed engine.Payload) error {
	ga.Status.ID, _ = observed["id"].(string)
	return nil
}

func groupAliasMount(ga *identityv1alpha1.IdentityGroupAlias) refs.Ref {
	return authMountRef(ga.Spec.MountAccessor, ga.Spec.MountPath, ga.Spec.MountRef, ga.Spec.MountSelector)
}
