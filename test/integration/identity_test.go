//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	identityv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/identity/v1alpha1"
	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/controller/identity"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
	"github.com/alexandrust88/openbao-config-operator/test/testenv"
)

// TestIntegrationIdentity runs IdentityEntity, IdentityGroup, IdentityEntityAlias, IdentityGroupAlias and IdentityGroupMembership against envtest and
// bao server -dev.
func TestIntegrationIdentity(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e := identitySetup(ctx, t)

	t.Run("entity", func(t *testing.T) { testEntity(ctx, t, e) })
	t.Run("groupmembership", func(t *testing.T) { testGroupMembership(ctx, t, e) })
	t.Run("entityalias", func(t *testing.T) { testEntityAlias(ctx, t, e) })
	t.Run("groupalias", func(t *testing.T) { testGroupAlias(ctx, t, e) })
}

func identitySpec() commonv1alpha1.ResourceSpec {
	return commonv1alpha1.ResourceSpec{ConnectionRef: commonv1alpha1.ConnectionReference{Name: "dev"}}
}

func identityRevDeleteSpec() commonv1alpha1.ResourceSpec {
	spec := identitySpec()
	spec.DeletionPolicy = commonv1alpha1.DeletionPolicyDelete
	return spec
}

func testEntity(ctx context.Context, t *testing.T, e env) {
	const path = "identity/entity/name/itest-alice"
	entity := &identityv1alpha1.IdentityEntity{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-alice", Namespace: namespace},
		Spec: identityv1alpha1.IdentityEntitySpec{
			ResourceSpec: identitySpec(),
			Policies:     []string{"reader", "writer"},
			Metadata:     map[string]string{"team": "a"},
		},
	}
	mustCreate(ctx, t, e.k8s, entity)
	waitFor(t, "entity created", func() error { return identityExpectPolicies(ctx, e.bao, path, "reader", "writer") })
	waitFor(t, "entity ready", func() error { return identityExpectID(ctx, e.k8s, entity) })

	changed := &identityv1alpha1.IdentityEntity{}
	if err := e.k8s.Get(ctx, client.ObjectKeyFromObject(entity), changed); err != nil {
		t.Fatal(err)
	}
	changed.Spec.Name = "other"
	if err := e.k8s.Update(ctx, changed); !apierrors.IsInvalid(err) {
		t.Errorf("update of spec.name: got %v, want it rejected as immutable", err)
	}

	mustWrite(ctx, t, e.bao, path, map[string]any{"policies": []string{"reader"}})
	waitFor(t, "entity drift repaired", func() error { return identityExpectPolicies(ctx, e.bao, path, "reader", "writer") })
	mustWrite(ctx, t, e.bao, path, map[string]any{"policies": []string{"writer", "reader"}})
	identityExpectNoWrites(t, "IdentityEntity")

	if err := e.k8s.Delete(ctx, entity); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "entity resource gone", func() error { return expectGone(ctx, e.k8s, entity) })
	if _, err := e.bao.Read(ctx, path); err != nil {
		t.Errorf("orphaned entity: %v", err)
	}
}

func testGroupMembership(ctx context.Context, t *testing.T, e env) {
	group := &identityv1alpha1.IdentityGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-devs", Namespace: namespace},
		Spec:       identityv1alpha1.IdentityGroupSpec{ResourceSpec: identitySpec(), Policies: []string{"reader"}},
	}
	mustCreate(ctx, t, e.k8s, group)
	waitFor(t, "group ready", func() error { return identityExpectID(ctx, e.k8s, group) })
	groupPath := "identity/group/id/" + group.Status.ID

	members := make([]*identityv1alpha1.IdentityEntity, 0, 2)
	for _, name := range []string{"itest-m1", "itest-m2"} {
		m := &identityv1alpha1.IdentityEntity{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: map[string]string{"itest-team": "devs"}},
			Spec:       identityv1alpha1.IdentityEntitySpec{ResourceSpec: identitySpec()},
		}
		mustCreate(ctx, t, e.k8s, m)
		waitFor(t, name+" ready", func() error { return identityExpectID(ctx, e.k8s, m) })
		members = append(members, m)
	}
	outsider, err := e.bao.Write(ctx, "identity/entity", map[string]any{"name": "itest-outsider"})
	if err != nil {
		t.Fatal(err)
	}
	outsiderID := outsider["id"].(string)
	mustWrite(ctx, t, e.bao, groupPath, map[string]any{"member_entity_ids": []string{outsiderID}})

	membership := &identityv1alpha1.IdentityGroupMembership{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-devs-members", Namespace: namespace},
		Spec: identityv1alpha1.IdentityGroupMembershipSpec{
			ResourceSpec:   identityRevDeleteSpec(),
			GroupRef:       &commonv1alpha1.LocalObjectReference{Name: "itest-devs"},
			EntitySelector: &commonv1alpha1.LabelSelector{MatchLabels: map[string]string{"itest-team": "devs"}},
		},
	}
	mustCreate(ctx, t, e.k8s, membership)
	all := []string{outsiderID, members[0].Status.ID, members[1].Status.ID}
	waitFor(t, "members added", func() error { return identityExpectMembers(ctx, e.bao, groupPath, all...) })
	waitFor(t, "membership ready", func() error { return expectReady(ctx, e.k8s, membership) })

	mustWrite(ctx, t, e.bao, groupPath, map[string]any{"member_entity_ids": []string{outsiderID, members[1].Status.ID}})
	waitFor(t, "removed member restored", func() error { return identityExpectMembers(ctx, e.bao, groupPath, all...) })
	identityExpectNoWrites(t, "IdentityGroupMembership")

	patched := group.DeepCopy()
	patched.Spec.Policies = []string{"reader", "auditor"}
	if err := e.k8s.Patch(ctx, patched, client.MergeFrom(group)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "group policies updated", func() error { return identityExpectPolicies(ctx, e.bao, groupPath, "auditor", "reader") })
	if err := identityExpectMembers(ctx, e.bao, groupPath, all...); err != nil {
		t.Errorf("an IdentityGroup write must keep members: %v", err)
	}

	relabelled := members[1].DeepCopy()
	relabelled.Labels = nil
	if err := e.k8s.Patch(ctx, relabelled, client.MergeFrom(members[1])); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "deselected member removed", func() error {
		return identityExpectMembers(ctx, e.bao, groupPath, outsiderID, members[0].Status.ID)
	})

	child := &identityv1alpha1.IdentityGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-child", Namespace: namespace},
		Spec:       identityv1alpha1.IdentityGroupSpec{ResourceSpec: identitySpec()},
	}
	mustCreate(ctx, t, e.k8s, child)
	waitFor(t, "child group ready", func() error { return identityExpectID(ctx, e.k8s, child) })
	nested := &identityv1alpha1.IdentityGroupMembership{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-devs-child", Namespace: namespace},
		Spec: identityv1alpha1.IdentityGroupMembershipSpec{
			ResourceSpec:    identityRevDeleteSpec(),
			GroupName:       "itest-devs",
			MemberGroupName: "itest-child",
		},
	}
	mustCreate(ctx, t, e.k8s, nested)
	waitFor(t, "member group added", func() error { return identityExpectMemberGroups(ctx, e.bao, groupPath, child.Status.ID) })
	if err := e.k8s.Delete(ctx, nested); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "member group removed", func() error { return identityExpectMemberGroups(ctx, e.bao, groupPath) })

	shared := &identityv1alpha1.IdentityGroupMembership{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-devs-m1", Namespace: namespace},
		Spec: identityv1alpha1.IdentityGroupMembershipSpec{
			ResourceSpec: identityRevDeleteSpec(),
			GroupRef:     &commonv1alpha1.LocalObjectReference{Name: "itest-devs"},
			EntityID:     members[0].Status.ID,
		},
	}
	mustCreate(ctx, t, e.k8s, shared)
	waitFor(t, "shared membership claims its member", func() error { return identityRevExpectClaim(ctx, e.k8s, shared, members[0].Status.ID) })

	if err := e.k8s.Delete(ctx, membership); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "membership resource gone", func() error { return expectGone(ctx, e.k8s, membership) })
	stayFor(t, "member claimed by another membership kept", 3*time.Second, func() error {
		return identityExpectMembers(ctx, e.bao, groupPath, outsiderID, members[0].Status.ID)
	})
	if err := e.k8s.Delete(ctx, shared); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "claimed members removed", func() error { return identityExpectMembers(ctx, e.bao, groupPath, outsiderID) })
	waitFor(t, "shared membership resource gone", func() error { return expectGone(ctx, e.k8s, shared) })

	external := &identityv1alpha1.IdentityGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-external", Namespace: namespace},
		Spec:       identityv1alpha1.IdentityGroupSpec{ResourceSpec: identitySpec(), Type: identityv1alpha1.GroupTypeExternal},
	}
	mustCreate(ctx, t, e.k8s, external)
	waitFor(t, "external group ready", func() error { return identityExpectID(ctx, e.k8s, external) })
	refused := &identityv1alpha1.IdentityGroupMembership{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-external-members", Namespace: namespace},
		Spec: identityv1alpha1.IdentityGroupMembershipSpec{
			ResourceSpec: identitySpec(),
			GroupRef:     &commonv1alpha1.LocalObjectReference{Name: "itest-external"},
			EntityID:     outsiderID,
		},
	}
	mustCreate(ctx, t, e.k8s, refused)
	waitFor(t, "external group refused", func() error { return identityExpectNotSynced(ctx, e.k8s, refused, "external") })
}

func testEntityAlias(ctx context.Context, t *testing.T, e env) {
	mustWrite(ctx, t, e.bao, "sys/auth/itest-userpass", map[string]any{"type": "userpass"})
	accessor := identityAccessor(ctx, t, e.bao, "itest-userpass")
	entity := &identityv1alpha1.IdentityEntity{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-bob", Namespace: namespace},
		Spec:       identityv1alpha1.IdentityEntitySpec{ResourceSpec: identitySpec()},
	}
	mustCreate(ctx, t, e.k8s, entity)
	waitFor(t, "entity ready", func() error { return identityExpectID(ctx, e.k8s, entity) })

	alias := &identityv1alpha1.IdentityEntityAlias{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-bob-userpass", Namespace: namespace},
		Spec: identityv1alpha1.IdentityEntityAliasSpec{
			ResourceSpec:   identityRevDeleteSpec(),
			AliasName:      "bob",
			MountPath:      "itest-userpass",
			EntityRef:      &commonv1alpha1.LocalObjectReference{Name: "itest-bob"},
			CustomMetadata: map[string]string{"source": "itest"},
		},
	}
	mustCreate(ctx, t, e.k8s, alias)
	waitFor(t, "alias ready", func() error { return identityExpectID(ctx, e.k8s, alias) })
	aliasPath := "identity/entity-alias/id/" + alias.Status.ID
	want := map[string]any{"name": "bob", "mount_accessor": accessor, "canonical_id": entity.Status.ID}
	if err := identityExpectFields(ctx, e.bao, aliasPath, want); err != nil {
		t.Fatal(err)
	}

	changed := &identityv1alpha1.IdentityEntityAlias{}
	if err := e.k8s.Get(ctx, client.ObjectKeyFromObject(alias), changed); err != nil {
		t.Fatal(err)
	}
	changed.Spec.MountPath, changed.Spec.MountAccessor = "", accessor
	if err := e.k8s.Update(ctx, changed); !apierrors.IsInvalid(err) {
		t.Errorf("update of the mount reference: got %v, want it rejected as immutable", err)
	}

	mustWrite(ctx, t, e.bao, aliasPath, map[string]any{"custom_metadata": map[string]string{"source": "intruder"}})
	waitFor(t, "alias drift repaired", func() error {
		return identityExpectFields(ctx, e.bao, aliasPath, map[string]any{"custom_metadata": map[string]any{"source": "itest"}})
	})
	identityExpectNoWrites(t, "IdentityEntityAlias")

	carol := &identityv1alpha1.IdentityEntity{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-carol", Namespace: namespace},
		Spec:       identityv1alpha1.IdentityEntitySpec{ResourceSpec: identitySpec()},
	}
	mustCreate(ctx, t, e.k8s, carol)
	second := &identityv1alpha1.IdentityEntityAlias{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-carol-userpass", Namespace: namespace},
		Spec: identityv1alpha1.IdentityEntityAliasSpec{ResourceSpec: identitySpec(), AliasName: "carol",
			MountPath: "itest-userpass", EntityRef: &commonv1alpha1.LocalObjectReference{Name: "itest-carol"}},
	}
	mustCreate(ctx, t, e.k8s, second)
	waitFor(t, "second alias on the server ready", func() error { return expectReady(ctx, e.k8s, second) })

	if err := e.k8s.Delete(ctx, alias); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "alias deleted", func() error { return expectAbsent(ctx, e.bao, aliasPath) })
	waitFor(t, "alias resource gone", func() error { return expectGone(ctx, e.k8s, alias) })
}

func testGroupAlias(ctx context.Context, t *testing.T, e env) {
	mustWrite(ctx, t, e.bao, "sys/auth/itest-oidc", map[string]any{"type": "userpass"})
	accessor := identityAccessor(ctx, t, e.bao, "itest-oidc")
	mount := &sysv1alpha1.AuthMount{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-oidc", Namespace: namespace},
		Spec:       sysv1alpha1.AuthMountSpec{ResourceSpec: identitySpec(), Type: "userpass"},
	}
	mustCreate(ctx, t, e.k8s, mount)
	serverID, _, err := engine.ClusterIDOf(ctx, e.bao)
	if err != nil {
		t.Fatal(err)
	}
	mount.Status.ID, mount.Status.ServerID = accessor, serverID
	meta.SetStatusCondition(&mount.Status.Conditions, metav1.Condition{
		Type: commonv1alpha1.ConditionReady, Status: metav1.ConditionTrue, Reason: commonv1alpha1.ReasonAvailable,
		ObservedGeneration: mount.Generation,
	})
	if err := e.k8s.Status().Update(ctx, mount); err != nil {
		t.Fatal(err)
	}

	group := &identityv1alpha1.IdentityGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-kc-devs", Namespace: namespace},
		Spec:       identityv1alpha1.IdentityGroupSpec{ResourceSpec: identitySpec(), Type: identityv1alpha1.GroupTypeExternal},
	}
	mustCreate(ctx, t, e.k8s, group)
	waitFor(t, "external group ready", func() error { return identityExpectID(ctx, e.k8s, group) })

	alias := &identityv1alpha1.IdentityGroupAlias{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-kc-devs", Namespace: namespace},
		Spec: identityv1alpha1.IdentityGroupAliasSpec{
			ResourceSpec: identityRevDeleteSpec(),
			AliasName:    "kc-devs",
			MountRef:     &commonv1alpha1.LocalObjectReference{Name: "itest-oidc"},
			GroupRef:     &commonv1alpha1.LocalObjectReference{Name: "itest-kc-devs"},
		},
	}
	mustCreate(ctx, t, e.k8s, alias)
	waitFor(t, "group alias ready", func() error { return identityExpectID(ctx, e.k8s, alias) })
	aliasPath := "identity/group-alias/id/" + alias.Status.ID
	want := map[string]any{"name": "kc-devs", "mount_accessor": accessor, "canonical_id": group.Status.ID}
	if err := identityExpectFields(ctx, e.bao, aliasPath, want); err != nil {
		t.Fatal(err)
	}
	identityExpectNoWrites(t, "IdentityGroupAlias")

	if err := e.k8s.Delete(ctx, alias); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "group alias deleted", func() error { return expectAbsent(ctx, e.bao, aliasPath) })
	waitFor(t, "group alias resource gone", func() error { return expectGone(ctx, e.k8s, alias) })
}

func identitySetup(ctx context.Context, t *testing.T) env {
	t.Helper()
	ctrl.SetLogger(zap.New(zap.WriteTo(os.Stderr), zap.UseDevMode(true)))
	root := repoRoot(t)

	baoBin := testenv.RequireBao(t)
	assets := testenv.RequireAssets(t, root)

	addr := startBao(t, baoBin)
	bao, err := openbao.NewPool().Client(ctx, "itest-identity", openbao.Config{
		Address: addr,
		Timeout: 10 * time.Second,
		Auth: openbao.TokenAuth{
			CredentialKey: "itest-identity",
			Token:         func(context.Context) (string, error) { return rootToken, nil },
		},
	})
	if err != nil {
		t.Fatalf("openbao client: %v", err)
	}

	crds := append(make([]string, 0, 6), filepath.Join(root, "config", "crd", "bases", "sys.obco.dev_authmounts.yaml"))
	for _, name := range []string{"identityentities", "identitygroups", "identityentityaliases", "identitygroupaliases", "identitygroupmemberships"} {
		crds = append(crds, filepath.Join(root, "config", "crd", "bases", "identity.obco.dev_"+name+".yaml"))
	}
	testEnv := &envtest.Environment{
		BinaryAssetsDirectory: assets,
		CRDInstallOptions:     envtest.CRDInstallOptions{Paths: crds, ErrorIfPathMissing: true},
	}
	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("envtest: %v", err)
	}
	t.Cleanup(func() {
		if err := testEnv.Stop(); err != nil {
			t.Logf("stop envtest: %v", err)
		}
	})

	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, identityv1alpha1.AddToScheme, sysv1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	skipNameValidation := true
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		Controller:             config.Controller{SkipNameValidation: &skipNameValidation},
	})
	if err != nil {
		t.Fatal(err)
	}
	conns := staticConnections{client: bao}
	opts := itestEngineOptions()
	for _, setupKind := range []func(ctrl.Manager, engine.Connections, engine.Options) error{
		identity.SetupIdentityEntity, identity.SetupIdentityGroup, identity.SetupIdentityEntityAlias, identity.SetupIdentityGroupAlias,
		identity.SetupIdentityGroupMembership,
	} {
		if err := setupKind(mgr, conns, opts); err != nil {
			t.Fatal(err)
		}
	}

	mgrCtx, stopMgr := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- mgr.Start(mgrCtx) }()
	t.Cleanup(func() {
		stopMgr()
		if err := <-done; err != nil {
			t.Logf("manager: %v", err)
		}
	})
	return env{k8s: mgr.GetClient(), bao: bao}
}

func identityExpectID(ctx context.Context, k8s client.Client, obj engine.Object) error {
	if err := expectReady(ctx, k8s, obj); err != nil {
		return err
	}
	if obj.GetResourceStatus().ID == "" {
		return errors.New("status.id not set")
	}
	return nil
}

func identityExpectNotSynced(ctx context.Context, k8s client.Client, obj engine.Object, message string) error {
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
		return err
	}
	synced := meta.FindStatusCondition(obj.GetResourceStatus().Conditions, commonv1alpha1.ConditionSynced)
	if synced == nil || synced.Status != metav1.ConditionFalse || !strings.Contains(synced.Message, message) {
		return fmt.Errorf("synced condition = %+v, want False containing %q", synced, message)
	}
	return nil
}

func identityExpectPolicies(ctx context.Context, bao openbao.Client, path string, want ...string) error {
	data, err := bao.Read(ctx, path)
	if err != nil {
		return err
	}
	return identityExpectList(data, "policies", want)
}

func identityRevExpectClaim(ctx context.Context, k8s client.Client, m *identityv1alpha1.IdentityGroupMembership, id string) error {
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(m), m); err != nil {
		return err
	}
	if !slices.Contains(m.Status.MemberEntityIDs, id) {
		return fmt.Errorf("claimed entities %v, want %s", m.Status.MemberEntityIDs, id)
	}
	return nil
}

func identityExpectMembers(ctx context.Context, bao openbao.Client, path string, want ...string) error {
	data, err := bao.Read(ctx, path)
	if err != nil {
		return err
	}
	return identityExpectList(data, "member_entity_ids", want)
}

func identityExpectMemberGroups(ctx context.Context, bao openbao.Client, path string, want ...string) error {
	data, err := bao.Read(ctx, path)
	if err != nil {
		return err
	}
	return identityExpectList(data, "member_group_ids", want)
}

func identityExpectList(data map[string]any, key string, want []string) error {
	raw, _ := data[key].([]any)
	got := make([]string, 0, len(raw))
	for _, v := range raw {
		got = append(got, fmt.Sprint(v))
	}
	slices.Sort(got)
	want = slices.Sorted(slices.Values(want))
	if !slices.Equal(got, want) {
		return fmt.Errorf("%s = %v, want %v", key, got, want)
	}
	return nil
}

func identityExpectFields(ctx context.Context, bao openbao.Client, path string, want map[string]any) error {
	data, err := bao.Read(ctx, path)
	if err != nil {
		return err
	}
	for key, value := range want {
		if fmt.Sprint(data[key]) != fmt.Sprint(value) {
			return fmt.Errorf("%s = %v, want %v", key, data[key], value)
		}
	}
	return nil
}

func identityAccessor(ctx context.Context, t *testing.T, bao openbao.Client, mount string) string {
	t.Helper()
	mounts, err := bao.Read(ctx, "sys/auth")
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := mounts[mount+"/"].(map[string]any)
	accessor, _ := entry["accessor"].(string)
	if accessor == "" {
		t.Fatalf("no accessor for auth mount %s", mount)
	}
	return accessor
}

func identityExpectNoWrites(t *testing.T, kind string) {
	t.Helper()
	time.Sleep(driftInterval)
	before := identityWrites(t, kind)
	time.Sleep(3 * driftInterval)
	if after := identityWrites(t, kind); after != before {
		t.Errorf("%s: %v writes at rest, want none", kind, after-before)
	}
}

func identityWrites(t *testing.T, kind string) float64 {
	t.Helper()
	families, err := metrics.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "obco_writes_total" {
			continue
		}
		for _, m := range family.GetMetric() {
			for _, label := range m.GetLabel() {
				if label.GetName() == "kind" && label.GetValue() == kind {
					return m.GetCounter().GetValue()
				}
			}
		}
	}
	return 0
}
