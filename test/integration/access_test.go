//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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

	accessv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/access/v1alpha1"
	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/controller/access"
	"github.com/alexandrust88/openbao-config-operator/internal/controller/auth"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
	"github.com/alexandrust88/openbao-config-operator/test/testenv"
)

func TestAccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e := setupAccess(ctx, t)

	mustWrite(ctx, t, e.bao, "sys/auth/kubernetes", map[string]any{"type": "kubernetes"})
	mustWrite(ctx, t, e.bao, "sys/auth/oidc", map[string]any{"type": "jwt"})
	mustWrite(ctx, t, e.bao, "sys/namespaces/team-a", map[string]any{})
	mustWrite(ctx, t, e.bao.WithNamespace("team-a"), "sys/auth/oidc", map[string]any{"type": "jwt"})

	read := &accessv1alpha1.AccessLevel{
		ObjectMeta: metav1.ObjectMeta{Name: "read", Namespace: namespace},
		Spec: accessv1alpha1.AccessLevelSpec{Data: []accessv1alpha1.AccessCapability{"read"},
			Metadata: []accessv1alpha1.AccessCapability{"read", "list"}},
	}
	write := &accessv1alpha1.AccessLevel{
		ObjectMeta: metav1.ObjectMeta{Name: "write", Namespace: namespace},
		Spec: accessv1alpha1.AccessLevelSpec{Data: []accessv1alpha1.AccessCapability{"create", "read", "update"},
			Metadata: []accessv1alpha1.AccessCapability{"read", "list"}, Versions: true},
	}
	mustCreate(ctx, t, e.k8s, read)
	mustCreate(ctx, t, e.k8s, write)
	waitFor(t, "levels ready", func() error {
		for _, level := range []*accessv1alpha1.AccessLevel{read, write} {
			if err := e.k8s.Get(ctx, client.ObjectKeyFromObject(level), level); err != nil {
				return err
			}
			if !meta.IsStatusConditionTrue(level.Status.Conditions, commonv1alpha1.ConditionReady) ||
				!slices.Contains(level.Finalizers, accessv1alpha1.InUseFinalizer) {
				return fmt.Errorf("%s not ready: %+v", level.Name, level.Status.Conditions)
			}
		}
		return nil
	})

	t.Run("accessgrant", func(t *testing.T) { testAccessGrant(ctx, t, e, read) })
	t.Run("accesslayout", func(t *testing.T) { testAccessLayout(ctx, t, e) })
	t.Run("child ownership", func(t *testing.T) { testChildOwnership(ctx, t, e) })
}

// testChildOwnership: a grant never adopts a role that a standalone KubernetesAuthRole owns, and a newer standalone
// role never adopts a role that a grant created.
func testChildOwnership(ctx context.Context, t *testing.T, e env) {
	const (
		ownedName = "grant_default_itest-owned_0"
		ownedRole = "auth/kubernetes/role/" + ownedName
		grantRole = "auth/kubernetes/role/grant_default_itest-first_0"
	)
	standaloneRole := func(name, roleName string, policy commonv1alpha1.CreationPolicy) *authv1alpha1.KubernetesAuthRole {
		return &authv1alpha1.KubernetesAuthRole{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec: authv1alpha1.KubernetesAuthRoleSpec{
				ResourceSpec: commonv1alpha1.ResourceSpec{ConnectionRef: commonv1alpha1.ConnectionReference{Name: "dev"},
					CreationPolicy: policy},
				Name:                          roleName,
				BoundServiceAccountNames:      []string{"other"},
				BoundServiceAccountNamespaces: []string{"other"},
				TokenFields:                   authv1alpha1.TokenFields{TokenPolicies: []string{"standalone"}},
			},
		}
	}
	accessGrant := func(name string, policy commonv1alpha1.CreationPolicy) *accessv1alpha1.AccessGrant {
		return &accessv1alpha1.AccessGrant{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec: accessv1alpha1.AccessGrantSpec{
				ResourceSpec: commonv1alpha1.ResourceSpec{ConnectionRef: commonv1alpha1.ConnectionReference{Name: "dev"},
					CreationPolicy: policy},
				MountPath: "kubernetes",
				Subjects:  []accessv1alpha1.AccessSubject{{Name: "app", Namespace: "apps"}},
				Rules: []accessv1alpha1.AccessRule{{
					AccessPath: accessv1alpha1.AccessPath{Mount: "secret", Path: "apps/*"}, Level: "write",
				}},
			},
		}
	}
	conflicted := func(obj engine.Object) func() error {
		return func() error {
			if err := e.k8s.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
				return err
			}
			synced := meta.FindStatusCondition(obj.GetResourceStatus().Conditions, commonv1alpha1.ConditionSynced)
			if synced == nil || synced.Reason != commonv1alpha1.ReasonConflict {
				return fmt.Errorf("synced = %+v, want reason Conflict", synced)
			}
			return nil
		}
	}

	standalone := standaloneRole("itest-standalone", ownedName, "")
	mustCreate(ctx, t, e.k8s, standalone)
	waitFor(t, "standalone role ready", func() error { return expectReady(ctx, e.k8s, standalone) })
	adopter := accessGrant("itest-owned", commonv1alpha1.CreationPolicyAdopt)
	mustCreate(ctx, t, e.k8s, adopter)
	waitFor(t, "adopting grant conflicts", conflicted(adopter))
	stayFor(t, "standalone role kept", 3*driftInterval, func() error {
		return expectRolePolicy(ctx, e.bao, ownedRole, "standalone")
	})

	first := accessGrant("itest-first", "")
	mustCreate(ctx, t, e.k8s, first)
	waitFor(t, "grant ready", func() error { return expectReady(ctx, e.k8s, first) })
	takeover := standaloneRole("itest-takeover", "grant_default_itest-first_0", commonv1alpha1.CreationPolicyCreateOrAdopt)
	mustCreate(ctx, t, e.k8s, takeover)
	waitFor(t, "newer standalone role conflicts", conflicted(takeover))
	stayFor(t, "grant role kept", 3*driftInterval, func() error {
		return expectRolePolicy(ctx, e.bao, grantRole, "grant_default_itest-first")
	})
}

func testAccessGrant(ctx context.Context, t *testing.T, e env, read *accessv1alpha1.AccessLevel) {
	const (
		policyPath = "sys/policies/acl/grant_default_itest-grant"
		role0      = "auth/kubernetes/role/grant_default_itest-grant_0"
		role1      = "auth/kubernetes/role/grant_default_itest-grant_1"
	)
	accessor := authAccessor(ctx, t, e.bao, "kubernetes")
	grant := &accessv1alpha1.AccessGrant{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-grant", Namespace: namespace},
		Spec: accessv1alpha1.AccessGrantSpec{
			ResourceSpec: commonv1alpha1.ResourceSpec{ConnectionRef: commonv1alpha1.ConnectionReference{Name: "dev"},
				DeletionPolicy: commonv1alpha1.DeletionPolicyDelete},
			MountPath: "kubernetes",
			Subjects: []accessv1alpha1.AccessSubject{
				{Name: "app", Namespace: "apps-*"},
				{Name: "*", NamespaceSelector: &accessv1alpha1.AccessNamespaceSelector{MatchLabels: map[string]string{"tier": "apps"}}},
			},
			Rules: []accessv1alpha1.AccessRule{{
				AccessPath: accessv1alpha1.AccessPath{Mount: "secret", Path: "apps/{sa.namespace}/*"},
				Level:      "read",
			}},
		},
	}
	mustCreate(ctx, t, e.k8s, grant)

	templated := `path "secret/data/apps/{{identity.entity.aliases.` + accessor + `.metadata.service_account_namespace}}/*"`
	waitFor(t, "grant written", func() error {
		if err := expectPolicyContains(ctx, e.bao, policyPath, templated); err != nil {
			return err
		}
		if err := expectRolePolicy(ctx, e.bao, role0, "grant_default_itest-grant"); err != nil {
			return err
		}
		return expectRolePolicy(ctx, e.bao, role1, "grant_default_itest-grant")
	})
	waitFor(t, "grant ready", func() error { return expectReady(ctx, e.k8s, grant) })
	if want := []string{"grant_default_itest-grant_0", "grant_default_itest-grant_1"}; !slices.Equal(grant.Status.Roles, want) {
		t.Errorf("status.roles = %v, want %v", grant.Status.Roles, want)
	}

	mustWrite(ctx, t, e.bao, role0, map[string]any{"token_policies": []any{"default"}, "token_period": 86400})
	waitFor(t, "role drift repaired", func() error {
		if err := expectRolePolicy(ctx, e.bao, role0, "grant_default_itest-grant"); err != nil {
			return err
		}
		data, err := e.bao.Read(ctx, role0)
		if err != nil {
			return err
		}
		if fmt.Sprint(data["token_period"]) != "0" {
			return fmt.Errorf("%s token_period = %v, want 0", role0, data["token_period"])
		}
		return nil
	})

	before := writesFor(t, "AccessGrant")
	stayFor(t, "grant steady", 3*driftInterval, func() error {
		if got := writesFor(t, "AccessGrant"); got != before {
			return fmt.Errorf("writes rose from %v to %v at rest", before, got)
		}
		return nil
	})

	patchLevel(ctx, t, e.k8s, read, func(s *accessv1alpha1.AccessLevelSpec) {
		s.Data = []accessv1alpha1.AccessCapability{"read", "list"}
	})
	waitFor(t, "level change re-rendered", func() error {
		return expectPolicyContains(ctx, e.bao, policyPath, templated+" {\n  capabilities = [\"read\", \"list\"]")
	})

	if err := e.k8s.Delete(ctx, read); err != nil {
		t.Fatal(err)
	}
	levelInUse := func() error {
		if err := e.k8s.Get(ctx, client.ObjectKeyFromObject(read), read); err != nil {
			return err
		}
		if ready := meta.FindStatusCondition(read.Status.Conditions, commonv1alpha1.ConditionReady); ready == nil ||
			ready.Reason != accessv1alpha1.ReasonInUse {
			return fmt.Errorf("ready = %+v, want reason InUse", ready)
		}
		return nil
	}
	waitFor(t, "cited level reports InUse", levelInUse)
	stayFor(t, "cited level kept", 3*driftInterval, levelInUse)

	if err := e.k8s.Get(ctx, client.ObjectKeyFromObject(grant), grant); err != nil {
		t.Fatal(err)
	}
	grant.Spec.ManagementPolicy = commonv1alpha1.ManagementPolicyObserve
	grant.Spec.Subjects = grant.Spec.Subjects[:1]
	if err := e.k8s.Update(ctx, grant); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "observe reports the dropped subject as drift", func() error {
		if err := e.k8s.Get(ctx, client.ObjectKeyFromObject(grant), grant); err != nil {
			return err
		}
		synced := meta.FindStatusCondition(grant.Status.Conditions, commonv1alpha1.ConditionSynced)
		if grant.Status.ObservedGeneration != grant.Generation || synced == nil || synced.Reason != commonv1alpha1.ReasonDrifted {
			return fmt.Errorf("synced = %+v", synced)
		}
		return nil
	})
	if _, err := e.bao.Read(ctx, role1); err != nil || len(grant.Status.Children) != 3 {
		t.Fatalf("observe changed ownership: read %s err=%v children=%v", role1, err, grant.Status.Children)
	}
	grant.Spec.ManagementPolicy = commonv1alpha1.ManagementPolicyManage
	if err := e.k8s.Update(ctx, grant); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "dropped subject's role deleted", func() error { return expectAbsent(ctx, e.bao, role1) })

	if err := e.k8s.Delete(ctx, grant); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "grant children deleted", func() error {
		if err := expectAbsent(ctx, e.bao, role0); err != nil {
			return err
		}
		return expectAbsent(ctx, e.bao, policyPath)
	})
	waitFor(t, "grant resource gone", func() error { return expectGone(ctx, e.k8s, grant) })
	waitFor(t, "released level gone", func() error { return expectGone(ctx, e.k8s, read) })
}

func testAccessLayout(ctx context.Context, t *testing.T, e env) {
	const (
		policyPath = "sys/policies/acl/layout_default_itest-layout_dev"
		groupPath  = "identity/group/name/layout_default_itest-layout_dev_oidc"
	)
	teamA := e.bao.WithNamespace("team-a")
	layout := &accessv1alpha1.AccessLayout{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-layout", Namespace: namespace},
		Spec: accessv1alpha1.AccessLayoutSpec{
			ResourceSpec: commonv1alpha1.ResourceSpec{ConnectionRef: commonv1alpha1.ConnectionReference{Name: "dev"},
				DeletionPolicy: commonv1alpha1.DeletionPolicyDelete},
			AuthMounts:         []string{"oidc"},
			GroupAliasTemplate: "team-{role}",
			Paths:              []accessv1alpha1.AccessPath{{Mount: "secret", Path: "teams/+/*", MetadataPath: "teams/*"}},
			Roles: []accessv1alpha1.AccessRole{{Name: "dev", Levels: []accessv1alpha1.AccessRoleLevel{
				{Level: "write"},
				{Namespace: "team-a", Level: "write"},
			}}},
		},
	}
	mustCreate(ctx, t, e.k8s, layout)

	rootAccessor := authAccessor(ctx, t, e.bao, "oidc")
	teamAccessor := authAccessor(ctx, t, teamA, "oidc")
	waitFor(t, "layout written", func() error {
		for _, c := range []struct {
			bao      openbao.Client
			accessor string
		}{{e.bao, rootAccessor}, {teamA, teamAccessor}} {
			if err := expectPolicyContains(ctx, c.bao, policyPath, `path "secret/destroy/teams/+/*"`); err != nil {
				return err
			}
			if err := expectGroupAlias(ctx, c.bao, groupPath, "team-dev", c.accessor); err != nil {
				return err
			}
		}
		return nil
	})
	waitFor(t, "layout ready", func() error { return expectReady(ctx, e.k8s, layout) })

	group, err := teamA.Read(ctx, groupPath)
	if err != nil {
		t.Fatal(err)
	}
	alias := group["alias"].(map[string]any)
	mustWrite(ctx, t, teamA, "identity/group-alias/id/"+alias["id"].(string),
		map[string]any{"name": "intruder", "mount_accessor": teamAccessor, "canonical_id": group["id"]})
	waitFor(t, "alias drift repaired", func() error { return expectGroupAlias(ctx, teamA, groupPath, "team-dev", teamAccessor) })

	before := writesFor(t, "AccessLayout")
	stayFor(t, "layout steady", 3*driftInterval, func() error {
		if got := writesFor(t, "AccessLayout"); got != before {
			return fmt.Errorf("writes rose from %v to %v at rest", before, got)
		}
		return nil
	})

	if err := e.k8s.Get(ctx, client.ObjectKeyFromObject(layout), layout); err != nil {
		t.Fatal(err)
	}
	layout.Spec.Roles[0].Levels = layout.Spec.Roles[0].Levels[:1]
	if err := e.k8s.Update(ctx, layout); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "removal blocked", func() error {
		if err := e.k8s.Get(ctx, client.ObjectKeyFromObject(layout), layout); err != nil {
			return err
		}
		synced := meta.FindStatusCondition(layout.Status.Conditions, commonv1alpha1.ConditionSynced)
		if synced == nil || synced.Status != metav1.ConditionFalse ||
			!strings.Contains(synced.Message, accessv1alpha1.AllowDisruptiveUpdateAnnotation) {
			return fmt.Errorf("synced = %+v, want the disruptive update refusal", synced)
		}
		return nil
	})
	stayFor(t, "blocked removal deletes nothing", 2*driftInterval, func() error {
		_, err := teamA.Read(ctx, groupPath)
		return err
	})

	layout.Annotations = map[string]string{accessv1alpha1.AllowDisruptiveUpdateAnnotation: "true"}
	if err := e.k8s.Update(ctx, layout); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "allowed removal applied", func() error {
		if err := expectAbsent(ctx, teamA, groupPath); err != nil {
			return err
		}
		return expectAbsent(ctx, teamA, policyPath)
	})

	if err := e.k8s.Delete(ctx, layout); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "layout children deleted", func() error {
		if err := expectAbsent(ctx, e.bao, groupPath); err != nil {
			return err
		}
		return expectAbsent(ctx, e.bao, policyPath)
	})
	waitFor(t, "layout resource gone", func() error { return expectGone(ctx, e.k8s, layout) })
}

func setupAccess(ctx context.Context, t *testing.T) env {
	t.Helper()
	ctrl.SetLogger(zap.New(zap.WriteTo(os.Stderr), zap.UseDevMode(true)))
	root := repoRoot(t)

	baoBin := testenv.RequireBao(t)
	assets := testenv.RequireAssets(t, root)

	addr := startBao(t, baoBin)
	bao, err := openbao.NewPool().Client(ctx, "itest-access", openbao.Config{
		Address: addr,
		Timeout: 10 * time.Second,
		Auth: openbao.TokenAuth{
			CredentialKey: "itest-access",
			Token:         func(context.Context) (string, error) { return rootToken, nil },
		},
	})
	if err != nil {
		t.Fatalf("openbao client: %v", err)
	}

	crds := make([]string, 0, 4)
	for _, plural := range []string{"accesslevels", "accessgrants", "accesslayouts"} {
		crds = append(crds, filepath.Join(root, "config", "crd", "bases", "access.openbaoconfig.io_"+plural+".yaml"))
	}
	crds = append(crds, filepath.Join(root, "config", "crd", "bases", "auth.openbaoconfig.io_kubernetesauthroles.yaml"))
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
	for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, accessv1alpha1.AddToScheme,
		authv1alpha1.AddToScheme} {
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
	for _, setup := range []func(ctrl.Manager, engine.Connections, engine.Options) error{
		access.SetupAccessLevel, access.SetupAccessGrant, access.SetupAccessLayout, auth.SetupKubernetesAuthRole,
	} {
		if err := setup(mgr, conns, opts); err != nil {
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

func authAccessor(ctx context.Context, t *testing.T, bao openbao.Client, mount string) string {
	t.Helper()
	table, err := bao.Read(ctx, "sys/auth")
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := table[mount+"/"].(map[string]any)
	accessor, _ := entry["accessor"].(string)
	if accessor == "" {
		t.Fatalf("no accessor for auth mount %s", mount)
	}
	return accessor
}

func writesFor(t *testing.T, kind string) float64 {
	t.Helper()
	families, err := metrics.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "openbao_writes_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == "kind" && label.GetValue() == kind {
					return metric.GetCounter().GetValue()
				}
			}
		}
	}
	return 0
}

func patchLevel(ctx context.Context, t *testing.T, k8s client.Client, level *accessv1alpha1.AccessLevel,
	mutate func(*accessv1alpha1.AccessLevelSpec)) {
	t.Helper()
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(level), level); err != nil {
		t.Fatal(err)
	}
	mutate(&level.Spec)
	if err := k8s.Update(ctx, level); err != nil {
		t.Fatal(err)
	}
}

func expectPolicyContains(ctx context.Context, bao openbao.Client, path, want string) error {
	data, err := bao.Read(ctx, path)
	if err != nil {
		return err
	}
	text, _ := data["policy"].(string)
	if !strings.Contains(text, want) {
		return fmt.Errorf("policy %s lacks %q:\n%s", path, want, text)
	}
	return nil
}

func expectRolePolicy(ctx context.Context, bao openbao.Client, path, policy string) error {
	data, err := bao.Read(ctx, path)
	if err != nil {
		return err
	}
	if got := fmt.Sprint(data["token_policies"]); got != "["+policy+"]" {
		return fmt.Errorf("%s token_policies = %s, want [%s]", path, got, policy)
	}
	return nil
}

func expectGroupAlias(ctx context.Context, bao openbao.Client, path, aliasName, accessor string) error {
	data, err := bao.Read(ctx, path)
	if err != nil {
		return err
	}
	alias, _ := data["alias"].(map[string]any)
	if alias["name"] != aliasName || alias["mount_accessor"] != accessor || data["type"] != "external" {
		return fmt.Errorf("group %s: type %v alias %v, want alias %s on %s", path, data["type"], alias, aliasName, accessor)
	}
	return nil
}
