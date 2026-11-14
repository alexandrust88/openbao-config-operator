package identity

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	identityv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/identity/v1alpha1"
	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

const (
	idtEntityID   = "65fe55e0-e545-1fc1-66e2-d078a06a1079"
	idtGroupID    = "802074ec-1355-acc7-639a-6bee8112cf24"
	idtExtGroupID = "f76b44c5-8da1-4fbc-955b-121981593bb4"
	idtAccessor   = "auth_userpass_c3870657"
	idtAliasID    = "10bd4e9e-526c-a57c-5094-b3d3228bb7c5"
)

// idtBao is an in-memory OpenBao: writes merge into the stored path, except paths with a canned response.
type idtBao struct {
	openbao.Client
	data      map[string]map[string]any
	responses map[string]map[string]any
	writes    []string
	deletes   []string
}

func newIdtBao() *idtBao {
	return &idtBao{
		data: map[string]map[string]any{
			"sys/auth": {
				"token/":    map[string]any{litAccessor: "auth_token_0", litType: "token"},
				"userpass/": map[string]any{litAccessor: idtAccessor, litType: litUserpass},
			},
		},
		responses: map[string]map[string]any{},
	}
}

func (b *idtBao) Read(_ context.Context, path string) (map[string]any, error) {
	if d, ok := b.data[path]; ok {
		return d, nil
	}
	return nil, openbao.ErrNotFound
}

func (b *idtBao) Write(_ context.Context, path string, data map[string]any) (map[string]any, error) {
	b.writes = append(b.writes, path)
	if resp, ok := b.responses[path]; ok {
		return resp, nil
	}
	encoded, _ := json.Marshal(data)
	var decoded map[string]any
	_ = json.Unmarshal(encoded, &decoded)
	if b.data[path] == nil {
		b.data[path] = map[string]any{}
	}
	maps.Copy(b.data[path], decoded)
	return nil, nil
}

func (b *idtBao) Delete(_ context.Context, path string) error {
	b.deletes = append(b.deletes, path)
	if _, ok := b.data[path]; !ok {
		return openbao.ErrNotFound
	}
	delete(b.data, path)
	return nil
}

func (b *idtBao) Namespace() string { return litTenant }

type idtConns struct{ client openbao.Client }

func (c idtConns) For(context.Context, string, commonv1alpha1.ConnectionReference) (openbao.Client, error) {
	return c.client, nil
}

func idtReader(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{identityv1alpha1.AddToScheme, sysv1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).
		WithIndex(&identityv1alpha1.EntityAlias{}, engine.PathIndex, engine.PathIndexFunc).
		WithIndex(&identityv1alpha1.GroupAlias{}, engine.PathIndex, engine.PathIndexFunc).Build()
}

func idtReady[T commonv1alpha1.Managed](obj T, id string) T {
	status := obj.GetResourceStatus()
	status.ID = id
	status.Conditions = []metav1.Condition{{Type: commonv1alpha1.ConditionReady, Status: metav1.ConditionTrue,
		Reason: commonv1alpha1.ReasonAvailable, LastTransitionTime: metav1.Now()}}
	return obj
}

func idtMeta(name string) metav1.ObjectMeta { return metav1.ObjectMeta{Name: name, Namespace: litTeam} }

func idtNoDiff(t *testing.T, desired, observed engine.Payload, fields engine.FieldSpec) {
	t.Helper()
	if d := engine.Diff(desired, observed, fields); len(d) != 0 {
		t.Errorf("unexpected diff %v", d)
	}
}

func idtDiff(t *testing.T, desired, observed engine.Payload, fields engine.FieldSpec, want ...string) {
	t.Helper()
	if d := engine.Diff(desired, observed, fields); !slices.Equal(d, want) {
		t.Errorf("diff = %v, want %v", d, want)
	}
}

// entityRead is `bao read identity/entity/name/alice` on OpenBao 2.7.2.
func entityRead() engine.Payload {
	return engine.Payload{
		litAliases: []any{}, litCreationTime: "2026-11-13T22:34:44.63231Z", "direct_group_ids": []any{},
		"disabled": false, litGroupIDs: []any{}, "id": idtEntityID, "inherited_group_ids": []any{},
		litLastUpdateTime: "2026-11-13T22:34:44.63231Z", "merged_entity_ids": nil,
		litMetadata: map[string]any{litTeam: "a"}, litName: litAlice, litNamespaceID: litRoot,
		litPolicies: []any{litReader, litWriter},
	}
}

func TestEntity(t *testing.T) {
	a := EntityAdapter{}
	e := &identityv1alpha1.Entity{ObjectMeta: idtMeta(litAlice), Spec: identityv1alpha1.EntitySpec{
		Policies: []string{litWriter, litReader}, Metadata: map[string]string{litTeam: "a"}, Disabled: new(false),
	}}
	if got := a.Path(e); got != "identity/entity/name/alice" {
		t.Errorf("path = %q", got)
	}
	desired, err := a.Desired(context.Background(), nil, e)
	if err != nil {
		t.Fatal(err)
	}
	want := engine.Payload{litPolicies: []string{litWriter, litReader}, litMetadata: map[string]string{litTeam: "a"}, "disabled": false}
	if d := engine.Diff(want, desired, engine.FieldSpec{}); len(d) != 0 || len(desired) != len(want) {
		t.Errorf("desired = %v", desired)
	}
	idtNoDiff(t, desired, entityRead(), a.Fields())

	changed := entityRead()
	changed["disabled"] = true
	changed[litPolicies] = []any{litReader}
	idtDiff(t, desired, changed, a.Fields(), "disabled", litPolicies)

	bare := &identityv1alpha1.Entity{ObjectMeta: idtMeta("x"), Spec: identityv1alpha1.EntitySpec{Name: "svc@team"}}
	if got := a.Path(bare); got != "identity/entity/name/svc@team" {
		t.Errorf("spec name path = %q", got)
	}
	defaults, _ := a.Desired(context.Background(), nil, bare)
	// `bao read identity/entity/name/e0` of an entity written with no fields, OpenBao 2.7.2.
	empty := engine.Payload{"policies": []any{}, "metadata": nil, "disabled": false}
	if len(defaults) != 3 {
		t.Errorf("an empty spec must send every field with its default: %v", defaults)
	}
	idtNoDiff(t, defaults, empty, a.Fields())
	idtDiff(t, defaults, engine.Payload{"policies": []any{litReader}, "metadata": map[string]any{"k": "v"}, "disabled": true},
		a.Fields(), "disabled", "metadata", "policies")

	if err := a.WriteStatus(context.Background(), nil, e, entityRead()); err != nil || e.Status.ID != idtEntityID {
		t.Errorf("status id = %q, %v", e.Status.ID, err)
	}
}

// groupRead is `bao read identity/group/name/devs` on OpenBao 2.7.2, with members added by others.
func groupRead() map[string]any {
	return map[string]any{
		"alias": map[string]any{}, litCreationTime: "2026-11-13T22:34:44.884307Z", "id": idtGroupID,
		litLastUpdateTime: "2026-11-13T22:34:44.884307Z", litMemberEntityIDs: []any{litOtherEntity},
		"member_group_ids": nil, litMetadata: map[string]any{litTeam: "a"}, "modify_index": 1, litName: litDevs,
		litNamespaceID: litRoot, "parent_group_ids": nil, litPolicies: []any{litReader}, litType: litInternal,
	}
}

func TestGroup(t *testing.T) {
	a := GroupAdapter{}
	g := &identityv1alpha1.Group{ObjectMeta: idtMeta(litDevs), Spec: identityv1alpha1.GroupSpec{
		Type: identityv1alpha1.GroupTypeInternal, Policies: []string{litReader}, Metadata: map[string]string{litTeam: "a"},
	}}
	desired, err := a.Desired(context.Background(), nil, g)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{litMemberEntityIDs, "member_group_ids"} {
		if _, ok := desired[key]; ok {
			t.Errorf("group payload must never carry %s", key)
		}
	}
	if desired[litType] != litInternal {
		t.Errorf("type = %v", desired[litType])
	}
	if _, ok := engine.StripCreateOnly(desired, a.Fields())[litType]; ok {
		t.Error("type must be stripped from updates")
	}

	bao := newIdtBao()
	bao.data["identity/group/name/devs"] = groupRead()
	observed, found, err := a.Observe(context.Background(), bao, g)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	idtNoDiff(t, desired, observed, a.Fields())

	ext := g.DeepCopy()
	ext.Spec.Type = identityv1alpha1.GroupTypeExternal
	if _, _, err := a.Observe(context.Background(), bao, ext); err == nil || !strings.Contains(err.Error(), "cannot change") {
		t.Errorf("type mismatch: err = %v", err)
	}
	if _, found, err := a.Observe(context.Background(), bao, &identityv1alpha1.Group{ObjectMeta: idtMeta("gone")}); found || err != nil {
		t.Errorf("absent: found=%v err=%v", found, err)
	}
	if err := a.WriteStatus(context.Background(), nil, g, observed); err != nil || g.Status.ID != idtGroupID {
		t.Errorf("status id = %q", g.Status.ID)
	}
}

// aliasRead is the alias entry of `bao write identity/lookup/entity` on OpenBao 2.7.2.
func aliasRead() map[string]any {
	return map[string]any{
		litCanonicalID: idtEntityID, litCreationTime: "2026-11-13T22:34:47.386482Z",
		"custom_metadata": map[string]any{"k": "v"}, "id": idtAliasID, litLastUpdateTime: "2026-11-13T22:34:47.386482Z",
		"local": false, "merged_from_canonical_ids": nil, litMetadata: nil, litMountAccessor: idtAccessor,
		"mount_path": "auth/userpass/", "mount_type": litUserpass, litName: litAlice,
	}
}

func newEntityAlias() *identityv1alpha1.EntityAlias {
	return &identityv1alpha1.EntityAlias{ObjectMeta: idtMeta("alice-userpass"), Spec: identityv1alpha1.EntityAliasSpec{
		AliasName: litAlice, MountPath: litUserpass,
		EntityRef:      &commonv1alpha1.LocalObjectReference{Name: litAlice},
		CustomMetadata: map[string]string{"k": "v"},
	}}
}

func TestEntityAliasDesired(t *testing.T) {
	bao := newIdtBao()
	entity := idtReady(&identityv1alpha1.Entity{ObjectMeta: idtMeta(litAlice)}, idtEntityID)
	a := EntityAliasAdapter{Reader: idtReader(t, entity), Conns: idtConns{client: bao}}

	desired, err := a.Desired(context.Background(), nil, newEntityAlias())
	if err != nil {
		t.Fatal(err)
	}
	want := engine.Payload{litName: litAlice, litMountAccessor: idtAccessor, litCanonicalID: idtEntityID,
		"custom_metadata": map[string]string{"k": "v"}}
	if d := engine.Diff(want, desired, engine.FieldSpec{}); len(d) != 0 || len(desired) != len(want) {
		t.Errorf("desired = %v", desired)
	}

	literal := newEntityAlias()
	literal.Spec.MountPath, literal.Spec.MountAccessor = "", "auth_x_1"
	literal.Spec.EntityRef, literal.Spec.EntityID = nil, "e-1"
	if d, err := a.Desired(context.Background(), nil, literal); err != nil || d[litMountAccessor] != "auth_x_1" || d[litCanonicalID] != "e-1" {
		t.Errorf("literal refs: %v, %v", d, err)
	}

	byName := newEntityAlias()
	byName.Spec.EntityRef, byName.Spec.EntityName = nil, litAlice
	bao.data["identity/entity/name/alice"] = entityRead()
	if d, err := a.Desired(context.Background(), nil, byName); err != nil || d[litCanonicalID] != idtEntityID {
		t.Errorf("entity by name: %v, %v", d, err)
	}

	missingMount := newEntityAlias()
	missingMount.Spec.MountPath = "ldap"
	if _, err := a.Desired(context.Background(), nil, missingMount); !errors.Is(err, engine.ErrReferenceNotReady) {
		t.Errorf("missing mount: err = %v", err)
	}
	notReady := EntityAliasAdapter{Reader: idtReader(t, &identityv1alpha1.Entity{ObjectMeta: idtMeta(litAlice)}), Conns: idtConns{client: bao}}
	if _, err := notReady.Desired(context.Background(), nil, newEntityAlias()); !errors.Is(err, engine.ErrReferenceNotReady) {
		t.Errorf("entity not ready: err = %v", err)
	}
}

func TestEntityAliasObserveDelete(t *testing.T) {
	bao := newIdtBao()
	entity := idtReady(&identityv1alpha1.Entity{ObjectMeta: idtMeta(litAlice)}, idtEntityID)
	a := EntityAliasAdapter{Reader: idtReader(t, entity), Conns: idtConns{client: bao}}
	ea := newEntityAlias()
	ctx := context.Background()

	if _, found, err := a.Observe(ctx, bao, ea); found || err != nil {
		t.Fatalf("lookup 204: found=%v err=%v", found, err)
	}
	other := aliasRead()
	other[litMountAccessor] = "auth_oidc_9"
	bao.responses["identity/lookup/entity"] = map[string]any{"id": idtEntityID, litAliases: []any{other, aliasRead()}}
	observed, found, err := a.Observe(ctx, bao, ea)
	if err != nil || !found || observed["id"] != idtAliasID {
		t.Fatalf("lookup: found=%v err=%v observed=%v", found, err, observed)
	}
	desired, _ := a.Desired(ctx, nil, ea)
	idtNoDiff(t, desired, observed, a.Fields())
	moved := aliasRead()
	moved[litCanonicalID] = "another-entity"
	idtDiff(t, desired, moved, a.Fields(), litCanonicalID)

	if err := a.WriteStatus(ctx, bao, ea, observed); err != nil || ea.Status.ID != idtAliasID {
		t.Fatalf("status id = %q", ea.Status.ID)
	}
	bao.data["identity/entity-alias/id/"+idtAliasID] = aliasRead()
	bao.writes = nil
	if _, found, _ := a.Observe(ctx, bao, ea); !found || len(bao.writes) != 0 {
		t.Errorf("read by id: found=%v, lookups %v", found, bao.writes)
	}
	if a.Path(ea) != "identity/entity-alias/id/"+idtAliasID {
		t.Errorf("path with an id = %q, want the alias by id", a.Path(ea))
	}
	if got := a.Path(newEntityAlias()); got != entityAliasCollection {
		t.Errorf("path before create = %q, want the collection", got)
	}
	ga := &identityv1alpha1.GroupAlias{}
	ga.Status.ID = "g1"
	if got := (GroupAliasAdapter{}).Path(ga); got != "identity/group-alias/id/g1" {
		t.Errorf("group alias path = %q", got)
	}

	if err := a.Delete(ctx, bao, ea); err != nil {
		t.Fatal(err)
	}
	if want := []string{"identity/entity-alias/id/" + idtAliasID}; !slices.Equal(bao.deletes, want) {
		t.Errorf("deletes = %v", bao.deletes)
	}
	if err := a.Delete(ctx, bao, ea); err != nil {
		t.Errorf("second delete: %v", err)
	}
	bao.deletes = nil
	delete(bao.responses, "identity/lookup/entity")
	if err := a.Delete(ctx, bao, newEntityAlias()); err != nil || len(bao.deletes) != 0 {
		t.Errorf("delete without id and alias: err=%v deletes=%v", err, bao.deletes)
	}
}

func TestEntityAliasWrite(t *testing.T) {
	ctx := context.Background()
	bao := newIdtBao()
	entity := idtReady(&identityv1alpha1.Entity{ObjectMeta: idtMeta(litAlice)}, idtEntityID)
	a := EntityAliasAdapter{Reader: idtReader(t, entity), Conns: idtConns{client: bao}}
	ea := newEntityAlias()
	desired, err := a.Desired(ctx, nil, ea)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Write(ctx, bao, ea, desired, true); err != nil || !slices.Equal(bao.writes, []string{entityAliasCollection}) {
		t.Fatalf("create: err=%v writes=%v", err, bao.writes)
	}

	bao.writes = nil
	bao.responses["identity/lookup/entity"] = map[string]any{"id": idtEntityID, litAliases: []any{aliasRead()}}
	if _, err := a.Write(ctx, bao, ea, desired, false); err != nil {
		t.Fatal(err)
	}
	if want := []string{"identity/lookup/entity", "identity/entity-alias/id/" + idtAliasID}; !slices.Equal(bao.writes, want) {
		t.Errorf("adopted update writes = %v, want %v", bao.writes, want)
	}

	bao.writes = nil
	ea.Status.ID = idtAliasID
	bao.data["identity/entity-alias/id/"+idtAliasID] = aliasRead()
	if _, err := a.Write(ctx, bao, ea, desired, false); err != nil {
		t.Fatal(err)
	}
	if want := []string{"identity/entity-alias/id/" + idtAliasID}; !slices.Equal(bao.writes, want) {
		t.Errorf("update writes = %v, want %v", bao.writes, want)
	}

	delete(bao.responses, "identity/lookup/entity")
	if _, err := a.Write(ctx, bao, newEntityAlias(), desired, false); err == nil {
		t.Error("an update without an alias to update must fail")
	}
}

// groupLookup is `bao write identity/lookup/group alias_name=kc-devs ...` on OpenBao 2.7.2, trimmed to alias.
func groupLookup() map[string]any {
	return map[string]any{"id": idtExtGroupID, litType: "external", "alias": map[string]any{
		litCanonicalID: idtExtGroupID, litCreationTime: "2026-11-13T22:35:11.215811Z", "id": "1637fa8d",
		litLastUpdateTime: "2026-11-13T22:35:11.215959Z", "merged_from_canonical_ids": nil, litMetadata: nil,
		litMountAccessor: idtAccessor, "mount_path": "auth/userpass/", "mount_type": litUserpass, litName: litKcDevs,
	}}
}

func TestGroupAlias(t *testing.T) {
	bao := newIdtBao()
	group := &identityv1alpha1.Group{ObjectMeta: metav1.ObjectMeta{Name: "ext", Namespace: litTeam, Labels: map[string]string{"idp": "keycloak"}}}
	a := GroupAliasAdapter{Reader: idtReader(t, idtReady(group, idtExtGroupID)), Conns: idtConns{client: bao}}
	ga := &identityv1alpha1.GroupAlias{ObjectMeta: idtMeta(litKcDevs), Spec: identityv1alpha1.GroupAliasSpec{
		AliasName: litKcDevs, MountSelector: nil, MountAccessor: idtAccessor,
		GroupSelector: &commonv1alpha1.LabelSelector{MatchLabels: map[string]string{"idp": "keycloak"}},
	}}
	ctx := context.Background()
	desired, err := a.Desired(ctx, nil, ga)
	if err != nil {
		t.Fatal(err)
	}
	want := engine.Payload{litName: litKcDevs, litMountAccessor: idtAccessor, litCanonicalID: idtExtGroupID}
	if d := engine.Diff(want, desired, engine.FieldSpec{}); len(d) != 0 || len(desired) != len(want) {
		t.Errorf("desired = %v", desired)
	}

	if _, found, err := a.Observe(ctx, bao, ga); found || err != nil {
		t.Fatalf("absent: found=%v err=%v", found, err)
	}
	if _, err := a.Write(ctx, bao, ga, desired, true); err != nil || bao.writes[len(bao.writes)-1] != "identity/group-alias" {
		t.Fatalf("create: %v %v", err, bao.writes)
	}

	bao.responses["identity/lookup/group"] = groupLookup()
	observed, found, err := a.Observe(ctx, bao, ga)
	if err != nil || !found {
		t.Fatalf("lookup: found=%v err=%v", found, err)
	}
	idtNoDiff(t, desired, observed, a.Fields())
	if _, err := a.Write(ctx, bao, ga, desired, false); err != nil || bao.writes[len(bao.writes)-1] != "identity/group-alias/id/1637fa8d" {
		t.Fatalf("update: %v %v", err, bao.writes)
	}

	bao.data["identity/group-alias/id/1637fa8d"] = map[string]any{"id": "1637fa8d"}
	if err := a.Delete(ctx, bao, ga); err != nil || !slices.Equal(bao.deletes, []string{"identity/group-alias/id/1637fa8d"}) {
		t.Errorf("delete: %v %v", err, bao.deletes)
	}
}

func newMembership(status identityv1alpha1.GroupMembershipStatus) *identityv1alpha1.GroupMembership {
	return &identityv1alpha1.GroupMembership{
		ObjectMeta: idtMeta("devs-alice"),
		Spec: identityv1alpha1.GroupMembershipSpec{
			GroupRef:       &commonv1alpha1.LocalObjectReference{Name: litDevs},
			EntitySelector: &commonv1alpha1.LabelSelector{MatchLabels: map[string]string{litTeam: "a"}},
		},
		Status: status,
	}
}

func membershipFixture(t *testing.T) (GroupMembershipAdapter, *idtBao) {
	t.Helper()
	bao := newIdtBao()
	bao.data["identity/group/id/"+idtGroupID] = groupRead()
	labelled := func(name string) metav1.ObjectMeta {
		m := idtMeta(name)
		m.Labels = map[string]string{litTeam: "a"}
		return m
	}
	reader := idtReader(t,
		idtReady(&identityv1alpha1.Group{ObjectMeta: idtMeta(litDevs)}, idtGroupID),
		idtReady(&identityv1alpha1.Entity{ObjectMeta: labelled(litAlice)}, litEAlice),
		idtReady(&identityv1alpha1.Entity{ObjectMeta: labelled("bob")}, litEBob),
	)
	return GroupMembershipAdapter{Reader: reader, Conns: idtConns{client: bao}}, bao
}

func TestGroupMembershipLifecycle(t *testing.T) {
	a, bao := membershipFixture(t)
	ctx := context.Background()
	m := newMembership(identityv1alpha1.GroupMembershipStatus{})
	members := func() []string { return stringList(bao.data["identity/group/id/"+idtGroupID][litMemberEntityIDs]) }

	desired, err := a.Desired(ctx, nil, m)
	if err != nil {
		t.Fatal(err)
	}
	observed, found, err := a.Observe(ctx, bao, m)
	if err != nil || found {
		t.Fatalf("before create: found=%v err=%v", found, err)
	}
	idtDiff(t, desired, observed, a.Fields(), membershipEntitiesKey)

	if _, err := a.Write(ctx, bao, m, desired, true); err != nil {
		t.Fatal(err)
	}
	if got, want := members(), []string{litOtherEntity, litEAlice, litEBob}; !slices.Equal(got, want) {
		t.Fatalf("members = %v, want %v", got, want)
	}
	if bao.data["identity/group/id/"+idtGroupID][litPolicies] == nil {
		t.Error("the membership write must keep the other group keys")
	}
	observed, found, err = a.Observe(ctx, bao, m)
	if err != nil || !found {
		t.Fatalf("after create: found=%v err=%v", found, err)
	}
	idtNoDiff(t, desired, observed, a.Fields())
	if err := a.WriteStatus(ctx, bao, m, observed); err != nil {
		t.Fatal(err)
	}
	if m.Status.GroupID != idtGroupID || !slices.Equal(m.Status.MemberEntityIDs, []string{litEAlice, litEBob}) {
		t.Fatalf("status = %+v", m.Status)
	}
	if a.Path(m) != "identity/group/id/"+idtGroupID {
		t.Errorf("path = %q", a.Path(m))
	}

	bob := &identityv1alpha1.Entity{}
	if err := a.Reader.Get(ctx, client.ObjectKey{Namespace: litTeam, Name: "bob"}, bob); err != nil {
		t.Fatal(err)
	}
	bob.Labels = nil
	if err := a.Reader.(client.Client).Update(ctx, bob); err != nil {
		t.Fatal(err)
	}
	desired, _ = a.Desired(ctx, nil, m)
	observed, _, _ = a.Observe(ctx, bao, m)
	idtDiff(t, desired, observed, a.Fields(), membershipEntitiesKey)
	if _, err := a.Write(ctx, bao, m, desired, false); err != nil {
		t.Fatal(err)
	}
	if got, want := members(), []string{litOtherEntity, litEAlice}; !slices.Equal(got, want) {
		t.Fatalf("after deselect: members = %v, want %v", got, want)
	}
	observed, _, _ = a.Observe(ctx, bao, m)
	idtNoDiff(t, desired, observed, a.Fields())
	_ = a.WriteStatus(ctx, bao, m, observed)

	if err := a.Delete(ctx, bao, m); err != nil {
		t.Fatal(err)
	}
	if got, want := members(), []string{litOtherEntity}; !slices.Equal(got, want) {
		t.Fatalf("after delete: members = %v, want %v", got, want)
	}
	delete(bao.data, "identity/group/id/"+idtGroupID)
	if err := a.Delete(ctx, bao, m); err != nil {
		t.Errorf("delete with group gone: %v", err)
	}
}

func TestGroupMembershipRules(t *testing.T) {
	ctx := context.Background()

	t.Run("external group", func(t *testing.T) {
		a, bao := membershipFixture(t)
		bao.data["identity/group/id/"+idtGroupID][litType] = "external"
		if _, _, err := a.Observe(ctx, bao, newMembership(identityv1alpha1.GroupMembershipStatus{})); err == nil ||
			!strings.Contains(err.Error(), "external") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("group missing", func(t *testing.T) {
		a, bao := membershipFixture(t)
		delete(bao.data, "identity/group/id/"+idtGroupID)
		if _, _, err := a.Observe(ctx, bao, newMembership(identityv1alpha1.GroupMembershipStatus{})); !errors.Is(err, engine.ErrReferenceNotReady) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("member group by name", func(t *testing.T) {
		a, bao := membershipFixture(t)
		bao.data["identity/group/name/child"] = map[string]any{"id": "g-child"}
		m := newMembership(identityv1alpha1.GroupMembershipStatus{})
		m.Spec.EntitySelector, m.Spec.MemberGroupName = nil, "child"
		desired, err := a.Desired(ctx, nil, m)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.Write(ctx, bao, m, desired, true); err != nil {
			t.Fatal(err)
		}
		group := bao.data["identity/group/id/"+idtGroupID]
		if got := stringList(group["member_group_ids"]); !slices.Equal(got, []string{"g-child"}) {
			t.Errorf("member groups = %v", got)
		}
		if got := stringList(group[litMemberEntityIDs]); !slices.Equal(got, []string{litOtherEntity}) {
			t.Errorf("entities touched: %v", got)
		}
	})

	t.Run("claims follow a group change", func(t *testing.T) {
		a, bao := membershipFixture(t)
		bao.data["identity/group/id/old"] = map[string]any{litType: litInternal, litMemberEntityIDs: []any{"x", litEAlice}}
		m := newMembership(identityv1alpha1.GroupMembershipStatus{GroupID: "old", MemberEntityIDs: []string{litEAlice}})
		desired, err := a.Desired(ctx, nil, m)
		if err != nil {
			t.Fatal(err)
		}
		if claims := desired[membershipEntitiesKey].(map[string]bool); len(claims) != 2 || !claims[litEAlice] {
			t.Errorf("claims in the new group = %v", claims)
		}
		if _, err := a.Write(ctx, bao, m, desired, false); err != nil {
			t.Fatal(err)
		}
		if got := stringList(bao.data["identity/group/id/old"][litMemberEntityIDs]); !slices.Equal(got, []string{"x"}) {
			t.Errorf("old group members = %v", got)
		}
	})

	t.Run("empty selection exists", func(t *testing.T) {
		a, bao := membershipFixture(t)
		m := newMembership(identityv1alpha1.GroupMembershipStatus{})
		m.Spec.EntitySelector.MatchLabels = map[string]string{litTeam: litNobody}
		desired, _ := a.Desired(ctx, nil, m)
		observed, found, err := a.Observe(ctx, bao, m)
		if err != nil || !found {
			t.Fatalf("found=%v err=%v", found, err)
		}
		idtNoDiff(t, desired, observed, a.Fields())
	})
}

func TestGroupMembershipResolvePath(t *testing.T) {
	ctx := context.Background()
	a, bao := membershipFixture(t)
	bao.data["identity/group/name/devs"] = map[string]any{"id": idtGroupID}
	byRef := newMembership(identityv1alpha1.GroupMembershipStatus{})
	byName := newMembership(identityv1alpha1.GroupMembershipStatus{})
	byName.Spec.GroupRef, byName.Spec.GroupName = nil, litDevs
	for name, m := range map[string]*identityv1alpha1.GroupMembership{"ref": byRef, "name": byName} {
		if got, err := a.ResolvePath(ctx, nil, bao, m); err != nil || got != "identity/group/id/"+idtGroupID {
			t.Errorf("%s: path %q err %v, want the group by ID", name, got, err)
		}
	}
	byName.Spec.GroupName = litMissing
	if _, err := a.ResolvePath(ctx, nil, bao, byName); !errors.Is(err, engine.ErrReferenceNotReady) {
		t.Errorf("missing group: err = %v", err)
	}
}

func TestGroupMembershipFound(t *testing.T) {
	ctx := context.Background()
	a, bao := membershipFixture(t)
	bao.data["identity/group/id/"+idtGroupID][litMemberEntityIDs] = []any{litEAlice}
	m := newMembership(identityv1alpha1.GroupMembershipStatus{})
	if _, found, err := a.Observe(ctx, bao, m); err != nil || found {
		t.Errorf("partly present before acquire: found=%v err=%v, want absent so that it is created", found, err)
	}
	bao.data["identity/group/id/"+idtGroupID][litMemberEntityIDs] = []any{litEAlice, litEBob}
	if _, found, err := a.Observe(ctx, bao, m); err != nil || !found {
		t.Errorf("fully present before acquire: found=%v err=%v", found, err)
	}
	bao.data["identity/group/id/"+idtGroupID][litMemberEntityIDs] = []any{}
	m.Status.Acquired = commonv1alpha1.AcquiredCreated
	desired, _ := a.Desired(ctx, nil, m)
	observed, found, err := a.Observe(ctx, bao, m)
	if err != nil || !found {
		t.Fatalf("acquired with members removed: found=%v err=%v", found, err)
	}
	idtDiff(t, desired, observed, a.Fields(), membershipEntitiesKey)
}

func TestGroupMembershipSharedClaims(t *testing.T) {
	ctx := context.Background()
	shared := func(t *testing.T) (GroupMembershipAdapter, *idtBao, *identityv1alpha1.GroupMembership) {
		t.Helper()
		bao := newIdtBao()
		bao.data["identity/group/id/"+idtGroupID] = map[string]any{litType: litInternal,
			litMemberEntityIDs: []any{litOtherEntity, litEAlice, litEBob}}
		labelled := idtMeta(litAlice)
		labelled.Labels = map[string]string{litTeam: "a"}
		claimed := identityv1alpha1.GroupMembershipStatus{GroupID: idtGroupID, MemberEntityIDs: []string{litEAlice}}
		claimed.Namespace = litTenant
		other := &identityv1alpha1.GroupMembership{ObjectMeta: idtMeta("other-alice"), Status: claimed}
		reader := idtReader(t,
			idtReady(&identityv1alpha1.Group{ObjectMeta: idtMeta(litDevs)}, idtGroupID),
			idtReady(&identityv1alpha1.Entity{ObjectMeta: labelled}, litEAlice),
			other,
		)
		status := identityv1alpha1.GroupMembershipStatus{GroupID: idtGroupID, MemberEntityIDs: []string{litEAlice, litEBob}}
		status.Namespace = litTenant
		return GroupMembershipAdapter{Reader: reader, Conns: idtConns{client: bao}}, bao, newMembership(status)
	}
	members := func(bao *idtBao) []string {
		return stringList(bao.data["identity/group/id/"+idtGroupID][litMemberEntityIDs])
	}

	t.Run("delete keeps a member another membership claims", func(t *testing.T) {
		a, bao, m := shared(t)
		if err := a.Delete(ctx, bao, m); err != nil {
			t.Fatal(err)
		}
		if got, want := members(bao), []string{litOtherEntity, litEAlice}; !slices.Equal(got, want) {
			t.Errorf("members = %v, want %v", got, want)
		}
	})

	t.Run("deselect releases a shared member without a write loop", func(t *testing.T) {
		a, bao, m := shared(t)
		m.Spec.EntitySelector.MatchLabels = map[string]string{litTeam: litNobody}
		desired, err := a.Desired(ctx, nil, m)
		if err != nil {
			t.Fatal(err)
		}
		observed, _, err := a.Observe(ctx, bao, m)
		if err != nil {
			t.Fatal(err)
		}
		idtDiff(t, desired, observed, a.Fields(), membershipEntitiesKey)
		if _, err := a.Write(ctx, bao, m, desired, false); err != nil {
			t.Fatal(err)
		}
		if got, want := members(bao), []string{litOtherEntity, litEAlice}; !slices.Equal(got, want) {
			t.Fatalf("members = %v, want %v", got, want)
		}
		observed, _, _ = a.Observe(ctx, bao, m)
		idtNoDiff(t, desired, observed, a.Fields())
		if err := a.WriteStatus(ctx, bao, m, observed); err != nil || len(m.Status.MemberEntityIDs) != 0 {
			t.Errorf("claims after release = %v, %v", m.Status.MemberEntityIDs, err)
		}
	})

	t.Run("a deleting membership does not hold a claim", func(t *testing.T) {
		a, bao, m := shared(t)
		other := &identityv1alpha1.GroupMembership{}
		if err := a.Reader.Get(ctx, client.ObjectKey{Namespace: litTeam, Name: "other-alice"}, other); err != nil {
			t.Fatal(err)
		}
		other.Finalizers = []string{"test"}
		if err := a.Reader.(client.Client).Update(ctx, other); err != nil {
			t.Fatal(err)
		}
		if err := a.Reader.(client.Client).Delete(ctx, other); err != nil {
			t.Fatal(err)
		}
		if err := a.Delete(ctx, bao, m); err != nil {
			t.Fatal(err)
		}
		if got, want := members(bao), []string{litOtherEntity}; !slices.Equal(got, want) {
			t.Errorf("members = %v, want %v", got, want)
		}
	})
}

func TestApplyClaims(t *testing.T) {
	got := applyClaims([]string{"a", "b", "c"}, map[string]bool{"b": false, "d": true, "a": true, "z": false})
	if want := []string{"a", "c", "d"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
