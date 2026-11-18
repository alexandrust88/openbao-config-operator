package identity

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	ctrl "sigs.k8s.io/controller-runtime"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	identityv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/identity/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
	"github.com/alexandrust88/openbao-config-operator/internal/refs"
)

const (
	membershipGroupKey    = "group_id"
	membershipEntitiesKey = "member_entity_ids"
	membershipGroupsKey   = "member_group_ids"
)

// +kubebuilder:rbac:groups=identity.obco.dev,resources=identitygroupmemberships,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=identity.obco.dev,resources=identitygroupmemberships/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=identity.obco.dev,resources=identitygroupmemberships/finalizers,verbs=update
// +kubebuilder:rbac:groups=identity.obco.dev,resources=identitygroups;identityentities,verbs=get;list;watch

// SetupIdentityGroupMembership registers the IdentityGroupMembership controller with the manager.
func SetupIdentityGroupMembership(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*identityv1alpha1.IdentityGroupMembership](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("identitygroupmembership-controller"),
		conns,
		IdentityGroupMembershipAdapter{Reader: mgr.GetClient(), Conns: conns},
		func() *identityv1alpha1.IdentityGroupMembership { return &identityv1alpha1.IdentityGroupMembership{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// IdentityGroupMembershipAdapter maps an IdentityGroupMembership onto the member lists of identity/group/id/<id>. Each
// resource claims the member IDs it selects and records them in status; a write adds the selected IDs and
// removes only IDs this resource claimed before, so members added in any other way are never touched. An ID
// that another live IdentityGroupMembership also claims in the same group is released without being removed.
//
// The payload is shaped for engine.Diff, which compares nested maps on the desired keys only:
// {"group_id": id, "member_entity_ids": {id: true (wanted) | false (claimed before, no longer wanted)},
// "member_group_ids": {...}}. The observed payload has the same shape, with true for IDs in the group.
type IdentityGroupMembershipAdapter struct {
	Reader refs.Reader
	Conns  engine.Connections
}

var (
	_ engine.Adapter[*identityv1alpha1.IdentityGroupMembership]      = IdentityGroupMembershipAdapter{}
	_ engine.PathResolver[*identityv1alpha1.IdentityGroupMembership] = IdentityGroupMembershipAdapter{}
	_ engine.Observer[*identityv1alpha1.IdentityGroupMembership]     = IdentityGroupMembershipAdapter{}
	_ engine.Writer[*identityv1alpha1.IdentityGroupMembership]       = IdentityGroupMembershipAdapter{}
	_ engine.Deleter[*identityv1alpha1.IdentityGroupMembership]      = IdentityGroupMembershipAdapter{}
	_ engine.StatusWriter[*identityv1alpha1.IdentityGroupMembership] = IdentityGroupMembershipAdapter{}
	_ engine.DriftTier                                               = IdentityGroupMembershipAdapter{}
	_ engine.SharedPath                                              = IdentityGroupMembershipAdapter{}
	_ engine.Singleton                                               = IdentityGroupMembershipAdapter{}
)

// membership is the resolved spec of an IdentityGroupMembership.
type membership struct {
	groupID  string
	entities []string
	groups   []string
}

// Path implements engine.Adapter.
func (IdentityGroupMembershipAdapter) Path(m *identityv1alpha1.IdentityGroupMembership) string {
	switch {
	case m.Status.GroupID != "":
		return "identity/group/id/" + m.Status.GroupID
	case m.Spec.GroupID != "":
		return "identity/group/id/" + m.Spec.GroupID
	case m.Spec.GroupName != "":
		return "identity/group/name/" + m.Spec.GroupName
	}
	return "identity/group"
}

// ResolvePath implements engine.PathResolver: the group by ID from the first reconcile on, so the recorded path
// does not move and never equals the identity/group/name path of the IdentityGroup resource that owns the group.
func (a IdentityGroupMembershipAdapter) ResolvePath(ctx context.Context, _ engine.Resolver, c openbao.Client,
	m *identityv1alpha1.IdentityGroupMembership) (string, error) {
	s := m.Spec
	groupID, err := refs.WithClient(a.Reader, c, m.Namespace).One(ctx, refs.Group, refs.Ref{Literal: s.GroupID,
		Name: s.GroupName, ObjectName: objectRefName(s.GroupRef), MatchLabels: selectorLabels(s.GroupSelector)})
	if err != nil {
		return "", err
	}
	return "identity/group/id/" + groupID, nil
}

// Desired implements engine.Adapter.
func (a IdentityGroupMembershipAdapter) Desired(ctx context.Context, _ engine.Resolver, m *identityv1alpha1.IdentityGroupMembership) (engine.Payload, error) {
	want, err := resolveMembership(ctx, refs.For(a.Reader, a.Conns, m), m)
	if err != nil {
		return nil, err
	}
	claimedEntities, claimedGroups, err := a.ownClaims(ctx, m, want)
	if err != nil {
		return nil, err
	}
	return engine.Payload{
		membershipGroupKey:    want.groupID,
		membershipEntitiesKey: claimMap(want.entities, claimedEntities),
		membershipGroupsKey:   claimMap(want.groups, claimedGroups),
	}, nil
}

// Fields implements engine.Adapter.
func (IdentityGroupMembershipAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{Partial: []string{"member_entity_ids", "member_group_ids"}}
}

// DriftTier implements engine.DriftTier: membership grants the group's policies.
func (IdentityGroupMembershipAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// SharedPath implements engine.SharedPath: several memberships, and the IdentityGroup itself, write to one group.
func (IdentityGroupMembershipAdapter) SharedPath() bool { return true }

// Singleton implements engine.Singleton: a membership is a claim on members, not an object of its own, so a
// selection whose members are all in the group already is taken over whatever the creationPolicy instead of
// being refused as an existing object.
func (IdentityGroupMembershipAdapter) Singleton() bool { return true }

// Observe implements engine.Observer. Once acquired, the membership exists while its group does, and missing
// members show as drift. Before that it exists only when every selected member is already in the group, so a
// partly present selection is created rather than refused by creationPolicy Create.
func (a IdentityGroupMembershipAdapter) Observe(ctx context.Context, c openbao.Client, m *identityv1alpha1.IdentityGroupMembership) (engine.Payload, bool, error) {
	want, err := resolveMembership(ctx, refs.WithClient(a.Reader, c, m.Namespace), m)
	if err != nil {
		return nil, false, err
	}
	group, err := readGroup(ctx, c, want.groupID)
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, false, fmt.Errorf("group %s not found: %w", want.groupID, engine.ErrReferenceNotReady)
	}
	if err != nil {
		return nil, false, err
	}
	claimedEntities, claimedGroups, err := a.ownClaims(ctx, m, want)
	if err != nil {
		return nil, false, err
	}
	entities := presence(slices.Concat(want.entities, claimedEntities), stringList(group[membershipEntitiesKey]))
	groups := presence(slices.Concat(want.groups, claimedGroups), stringList(group[membershipGroupsKey]))
	found := m.Status.Acquired != "" || (allPresent(want.entities, entities) && allPresent(want.groups, groups))
	return engine.Payload{
		membershipGroupKey:    want.groupID,
		membershipEntitiesKey: entities,
		membershipGroupsKey:   groups,
	}, found, nil
}

// Write implements engine.Writer. When the selected group changed, the members claimed in the previous
// group are removed from it first.
func (a IdentityGroupMembershipAdapter) Write(ctx context.Context, c openbao.Client, m *identityv1alpha1.IdentityGroupMembership,
	desired engine.Payload, _ bool) (engine.Payload, error) {
	groupID, _ := desired[membershipGroupKey].(string)
	entities, _ := desired[membershipEntitiesKey].(map[string]bool)
	groups, _ := desired[membershipGroupsKey].(map[string]bool)
	if previous := m.Status.GroupID; previous != "" && previous != groupID {
		if err := a.release(ctx, c, m); err != nil {
			return nil, err
		}
	}
	return nil, editMembers(ctx, c, groupID, entities, groups)
}

// Delete implements engine.Deleter: it removes the claimed members and keeps the group.
func (a IdentityGroupMembershipAdapter) Delete(ctx context.Context, c openbao.Client, m *identityv1alpha1.IdentityGroupMembership) error {
	return a.release(ctx, c, m)
}

// release removes the members claimed in status.groupID that no other IdentityGroupMembership claims.
func (a IdentityGroupMembershipAdapter) release(ctx context.Context, c openbao.Client, m *identityv1alpha1.IdentityGroupMembership) error {
	if m.Status.GroupID == "" {
		return nil
	}
	entities, groups, err := a.ownClaims(ctx, m, membership{groupID: m.Status.GroupID})
	if err != nil {
		return err
	}
	err = editMembers(ctx, c, m.Status.GroupID, claimMap(nil, entities), claimMap(nil, groups))
	if errors.Is(err, openbao.ErrNotFound) {
		return nil
	}
	return err
}

// WriteStatus implements engine.StatusWriter: the claimed members are the selected IDs found in the group. An
// observing membership claims nothing, so the claims Manage last recorded stay as they are.
func (IdentityGroupMembershipAdapter) WriteStatus(_ context.Context, _ openbao.Client, m *identityv1alpha1.IdentityGroupMembership, observed engine.Payload) error {
	if observing(m) {
		return nil
	}
	m.Status.GroupID, _ = observed[membershipGroupKey].(string)
	entities, _ := observed[membershipEntitiesKey].(map[string]bool)
	groups, _ := observed[membershipGroupsKey].(map[string]bool)
	m.Status.MemberEntityIDs = present(entities)
	m.Status.MemberGroupIDs = present(groups)
	return nil
}

func resolveMembership(ctx context.Context, r refs.Resolver, m *identityv1alpha1.IdentityGroupMembership) (membership, error) {
	s := m.Spec
	groupID, err := r.One(ctx, refs.Group, refs.Ref{Literal: s.GroupID, Name: s.GroupName,
		ObjectName: objectRefName(s.GroupRef), MatchLabels: selectorLabels(s.GroupSelector)})
	if err != nil {
		return membership{}, err
	}
	want := membership{groupID: groupID}
	entity := refs.Ref{Literal: s.EntityID, Name: s.EntityName, ObjectName: objectRefName(s.EntityRef),
		MatchLabels: selectorLabels(s.EntitySelector)}
	if !entity.IsZero() {
		want.entities, err = r.All(ctx, refs.Entity, entity)
		return want, err
	}
	want.groups, err = r.All(ctx, refs.Group, refs.Ref{Literal: s.MemberGroupID, Name: s.MemberGroupName,
		ObjectName: objectRefName(s.MemberGroupRef), MatchLabels: selectorLabels(s.MemberGroupSelector)})
	return want, err
}

// claims returns the member IDs this resource claimed in groupID before.
func claims(m *identityv1alpha1.IdentityGroupMembership, groupID string) (entities, groups []string) {
	if m.Status.GroupID != groupID {
		return nil, nil
	}
	return m.Status.MemberEntityIDs, m.Status.MemberGroupIDs
}

// ownClaims returns the IDs m claimed in want.groupID, without the IDs m no longer wants that another live
// managing IdentityGroupMembership on the same server claims in the same group: removing those would take away a member
// the other resource still grants, until its next drift check added it back.
func (a IdentityGroupMembershipAdapter) ownClaims(ctx context.Context, m *identityv1alpha1.IdentityGroupMembership,
	want membership) (entities, groups []string, err error) {
	entities, groups = claims(m, want.groupID)
	released := func(id string, wanted []string) bool { return !slices.Contains(wanted, id) }
	if !slices.ContainsFunc(entities, func(id string) bool { return released(id, want.entities) }) &&
		!slices.ContainsFunc(groups, func(id string) bool { return released(id, want.groups) }) {
		return entities, groups, nil
	}
	var list identityv1alpha1.IdentityGroupMembershipList
	if err := a.Reader.List(ctx, &list); err != nil {
		return nil, nil, fmt.Errorf("list GroupMemberships: %w", err)
	}
	var otherEntities, otherGroups []string
	for i := range list.Items {
		o := &list.Items[i]
		if (o.Namespace == m.Namespace && o.Name == m.Name) || !o.DeletionTimestamp.IsZero() || observing(o) ||
			o.Status.GroupID != want.groupID || o.Status.Namespace != m.Status.Namespace || o.Status.ServerID != m.Status.ServerID {
			continue
		}
		otherEntities = append(otherEntities, o.Status.MemberEntityIDs...)
		otherGroups = append(otherGroups, o.Status.MemberGroupIDs...)
	}
	keep := func(ids, wanted, others []string) []string {
		return slices.DeleteFunc(slices.Clone(ids), func(id string) bool {
			return released(id, wanted) && slices.Contains(others, id)
		})
	}
	return keep(entities, want.entities, otherEntities), keep(groups, want.groups, otherGroups), nil
}

// claimMap marks wanted IDs true and previously claimed IDs that are no longer wanted false.
func claimMap(wanted, claimed []string) map[string]bool {
	out := make(map[string]bool, len(wanted)+len(claimed))
	for _, id := range claimed {
		out[id] = false
	}
	for _, id := range wanted {
		out[id] = true
	}
	return out
}

func presence(ids, members []string) map[string]bool {
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = slices.Contains(members, id)
	}
	return out
}

func allPresent(ids []string, presence map[string]bool) bool {
	return !slices.ContainsFunc(ids, func(id string) bool { return !presence[id] })
}

func present(ids map[string]bool) []string {
	var out []string
	for id, in := range ids {
		if in {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

func stringList(value any) []string {
	raw, _ := value.([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func readGroup(ctx context.Context, c openbao.Client, groupID string) (map[string]any, error) {
	path := "identity/group/id/" + groupID
	group, err := c.Read(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if t, _ := group["type"].(string); t == string(identityv1alpha1.GroupTypeExternal) {
		return nil, fmt.Errorf("group %s is external; external groups get their members from group aliases, not from IdentityGroupMembership", groupID)
	}
	return group, nil
}

// groupLocks serialises the read-modify-write of one group's member lists, which OpenBao replaces whole
// and without compare-and-set. The manager runs a single leader, so an in-process lock is enough.
var groupLocks sync.Map

func lockGroup(c openbao.Client, groupID string) func() {
	v, _ := groupLocks.LoadOrStore(c.Namespace()+"\x00"+groupID, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// editMembers applies claims (true = add, false = remove) to the member lists of a group, writing only the
// lists that change.
func editMembers(ctx context.Context, c openbao.Client, groupID string, entities, groups map[string]bool) error {
	unlock := lockGroup(c, groupID)
	defer unlock()
	group, err := readGroup(ctx, c, groupID)
	if err != nil {
		return err
	}
	update := engine.Payload{}
	for key, edits := range map[string]map[string]bool{membershipEntitiesKey: entities, membershipGroupsKey: groups} {
		current := stringList(group[key])
		next := applyClaims(current, edits)
		if slices.Equal(current, next) {
			continue
		}
		update[key] = append([]string{}, next...)
	}
	if len(update) == 0 {
		return nil
	}
	path := "identity/group/id/" + groupID
	if _, err := c.Write(ctx, path, update); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func applyClaims(current []string, edits map[string]bool) []string {
	next := slices.DeleteFunc(slices.Clone(current), func(id string) bool {
		in, edited := edits[id]
		return edited && !in
	})
	var added []string
	for id, in := range edits {
		if in && !slices.Contains(next, id) {
			added = append(added, id)
		}
	}
	slices.Sort(added)
	return append(next, added...)
}

func observing(m commonv1alpha1.Managed) bool {
	return m.GetResourceSpec().ManagementPolicy == commonv1alpha1.ManagementPolicyObserve
}
