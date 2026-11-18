package identity

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	identityv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/identity/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
	"github.com/alexandrust88/openbao-config-operator/internal/refs"
)

const entityAliasCollection = "identity/entity-alias"

// +kubebuilder:rbac:groups=identity.obco.dev,resources=identityentityaliases,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=identity.obco.dev,resources=identityentityaliases/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=identity.obco.dev,resources=identityentityaliases/finalizers,verbs=update
// +kubebuilder:rbac:groups=identity.obco.dev,resources=identityentities,verbs=get;list;watch
// +kubebuilder:rbac:groups=sys.obco.dev,resources=authmounts,verbs=get;list;watch

// SetupIdentityEntityAlias registers the IdentityEntityAlias controller with the manager.
func SetupIdentityEntityAlias(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*identityv1alpha1.IdentityEntityAlias](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("identityentityalias-controller"),
		conns,
		IdentityEntityAliasAdapter{Reader: mgr.GetClient(), Conns: conns},
		func() *identityv1alpha1.IdentityEntityAlias { return &identityv1alpha1.IdentityEntityAlias{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// IdentityEntityAliasAdapter maps an IdentityEntityAlias onto identity/entity-alias. Reads, updates and deletes go by the alias
// ID recorded in status.id, or by a lookup on (mount accessor, name) when there is none. Updates use
// identity/entity-alias/id/<id>: an update on the collection would clear custom_metadata that the spec does
// not set, and would create a second alias after an out-of-band rename.
type IdentityEntityAliasAdapter struct {
	Reader refs.Reader
	Conns  engine.Connections
}

var (
	_ engine.Adapter[*identityv1alpha1.IdentityEntityAlias]      = IdentityEntityAliasAdapter{}
	_ engine.Observer[*identityv1alpha1.IdentityEntityAlias]     = IdentityEntityAliasAdapter{}
	_ engine.Writer[*identityv1alpha1.IdentityEntityAlias]       = IdentityEntityAliasAdapter{}
	_ engine.Deleter[*identityv1alpha1.IdentityEntityAlias]      = IdentityEntityAliasAdapter{}
	_ engine.StatusWriter[*identityv1alpha1.IdentityEntityAlias] = IdentityEntityAliasAdapter{}
)

// Path implements engine.Adapter: the alias by ID once known, the collection it is created in before.
func (IdentityEntityAliasAdapter) Path(ea *identityv1alpha1.IdentityEntityAlias) string {
	return aliasPath(entityAliasCollection, ea.Status.ID)
}

// Desired implements engine.Adapter.
func (a IdentityEntityAliasAdapter) Desired(ctx context.Context, _ engine.Resolver, ea *identityv1alpha1.IdentityEntityAlias) (engine.Payload, error) {
	r := refs.For(a.Reader, a.Conns, ea)
	accessor, err := r.One(ctx, refs.AuthMount, entityAliasMount(ea))
	if err != nil {
		return nil, err
	}
	entityID, err := r.One(ctx, refs.Entity, refs.Ref{Literal: ea.Spec.EntityID, Name: ea.Spec.EntityName,
		ObjectName: objectRefName(ea.Spec.EntityRef), MatchLabels: selectorLabels(ea.Spec.EntitySelector)})
	if err != nil {
		return nil, err
	}
	return engine.Payload{aliasNameKey: ea.Spec.AliasName, aliasAccessorKey: accessor, aliasCanonicalIDKey: entityID,
		"custom_metadata": mapOrEmpty(ea.Spec.CustomMetadata)}, nil
}

// Fields implements engine.Adapter.
func (IdentityEntityAliasAdapter) Fields() engine.FieldSpec { return engine.FieldSpec{} }

// Observe implements engine.Observer.
func (a IdentityEntityAliasAdapter) Observe(ctx context.Context, c openbao.Client, ea *identityv1alpha1.IdentityEntityAlias) (engine.Payload, bool, error) {
	alias, found, err := readAlias(ctx, c, entityAliasCollection, ea.Status.ID)
	if err != nil {
		return nil, false, err
	}
	if found {
		if ea.Status.Acquired == "" {
			if err := checkAliasOwner(ctx, a.Reader, c, ea, &identityv1alpha1.IdentityEntityAliasList{}, entityAliasCollection, ea.Status.ID); err != nil {
				return nil, false, err
			}
		}
		return alias, true, nil
	}
	accessor, err := refs.WithClient(a.Reader, c, ea.Namespace).One(ctx, refs.AuthMount, entityAliasMount(ea))
	if err != nil {
		return nil, false, err
	}
	key := aliasKey{accessor: accessor, name: ea.Spec.AliasName}
	entity, err := lookup(ctx, c, "identity/lookup/entity", key)
	if err != nil || entity == nil {
		return nil, false, err
	}
	aliases, _ := entity["aliases"].([]any)
	for _, raw := range aliases {
		if alias, ok := raw.(map[string]any); ok && key.matches(alias) {
			id, _ := alias["id"].(string)
			if err := checkAliasOwner(ctx, a.Reader, c, ea, &identityv1alpha1.IdentityEntityAliasList{}, entityAliasCollection, id); err != nil {
				return nil, false, err
			}
			return alias, true, nil
		}
	}
	return nil, false, nil
}

// Write implements engine.Writer.
func (a IdentityEntityAliasAdapter) Write(ctx context.Context, c openbao.Client, ea *identityv1alpha1.IdentityEntityAlias,
	desired engine.Payload, create bool) (engine.Payload, error) {
	return writeAlias(ctx, c, entityAliasCollection, desired, create, func() (string, error) {
		return aliasIDFrom(func() (engine.Payload, bool, error) { return a.Observe(ctx, c, ea) }, ea.Spec.AliasName)
	})
}

// Delete implements engine.Deleter.
func (a IdentityEntityAliasAdapter) Delete(ctx context.Context, c openbao.Client, ea *identityv1alpha1.IdentityEntityAlias) error {
	id := ea.Status.ID
	if id == "" {
		alias, found, err := a.Observe(ctx, c, ea)
		if err != nil || !found {
			return err
		}
		id, _ = alias["id"].(string)
	}
	return deleteAlias(ctx, c, entityAliasCollection, id)
}

// WriteStatus implements engine.StatusWriter.
func (IdentityEntityAliasAdapter) WriteStatus(_ context.Context, _ openbao.Client, ea *identityv1alpha1.IdentityEntityAlias, observed engine.Payload) error {
	ea.Status.ID, _ = observed["id"].(string)
	return nil
}

func entityAliasMount(ea *identityv1alpha1.IdentityEntityAlias) refs.Ref {
	return authMountRef(ea.Spec.MountAccessor, ea.Spec.MountPath, ea.Spec.MountRef, ea.Spec.MountSelector)
}
