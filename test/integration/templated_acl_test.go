//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	baoapi "github.com/openbao/openbao/api/v2"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	accessv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/access/v1alpha1"
	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	identityv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/identity/v1alpha1"
	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/controller/access"
	"github.com/alexandrust88/openbao-config-operator/internal/controller/auth"
	"github.com/alexandrust88/openbao-config-operator/internal/controller/identity"
	"github.com/alexandrust88/openbao-config-operator/internal/controller/sys"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

const (
	taclRootConn  = "dev"
	taclTeamConn  = "team"
	taclTeamNS    = "tacl-team"
	taclKVMount   = "apps"
	taclAuthMount = "kubernetes"
	taclReviewer  = "tacl-reviewer"
	taclSystemNS  = "tacl-system"
	taclAppSA     = "app"
)

var taclAppNamespaces = []string{"tacl-a", "tacl-b"}

// taclConnections maps a connection name to a client; "team" is a connection based in a child namespace.
type taclConnections map[string]openbao.Client

func (c taclConnections) For(_ context.Context, _ string,
	ref commonv1alpha1.ConnectionReference) (openbao.Client, error) {
	conn, ok := c[ref.Name]
	if !ok {
		return nil, fmt.Errorf("connection %s: %w", ref.Name, engine.ErrConnectionNotReady)
	}
	return conn, nil
}

type taclEnv struct {
	env
	addr string
	kube *rest.Config
	sas  map[string]*corev1.ServiceAccount
}

// TestTemplatedACL proves that identity-templated ACL policies managed by the operator grant each real
// ServiceAccount, logged in through the Kubernetes auth method, exactly its own paths.
func TestTemplatedACL(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e := taclSetup(ctx, t)

	t.Run("examples", func(t *testing.T) { testTACLExamples(ctx, t, e) })
	taclKubernetes(ctx, t, &e)
	t.Run("serviceaccount-and-identity", func(t *testing.T) { testTACLRoot(ctx, t, e) })
	t.Run("accessgrant", func(t *testing.T) { testTACLAccessGrant(ctx, t, e) })
	t.Run("child-namespace", func(t *testing.T) { testTACLChildNamespace(ctx, t, e) })
	t.Run("steady-state", func(t *testing.T) { testTACLSteadyState(ctx, t, e) })
}

// testTACLSteadyState checks that none of the kinds above writes to OpenBao once it has converged, and that
// the operator creates no tokens or entities at rest.
func testTACLSteadyState(ctx context.Context, t *testing.T, e taclEnv) {
	counted := []string{"auth/token/accessors", "identity/entity/id", "identity/entity-alias/id"}
	counts := map[string]int{}
	for _, path := range counted {
		counts[path] = taclCount(ctx, t, e.bao, path)
	}
	kinds := []string{"AuthMount", "SecretsMount", "ACLPolicy", "BaoNamespace", "KubernetesAuthConfig",
		"KubernetesAuthRole", "IdentityEntity", "IdentityEntityAlias", "IdentityGroup", "IdentityGroupMembership", "AccessGrant"}
	before := map[string]float64{}
	for _, kind := range kinds {
		if before[kind] = writesFor(t, kind); before[kind] == 0 {
			t.Errorf("no %s write was counted", kind)
		}
	}
	stayFor(t, "no writes at rest", 3*driftInterval, func() error {
		for _, kind := range kinds {
			if got := writesFor(t, kind); got != before[kind] {
				return fmt.Errorf("%s writes rose from %v to %v at rest", kind, before[kind], got)
			}
		}
		return nil
	})
	for _, path := range counted {
		if got := taclCount(ctx, t, e.bao, path); got != counts[path] {
			t.Errorf("%s: %d entries at rest, was %d", path, got, counts[path])
		}
	}
}

func taclCount(ctx context.Context, t *testing.T, bao openbao.Client, path string) int {
	t.Helper()
	keys, err := bao.List(ctx, path)
	if err != nil {
		t.Fatalf("list %s: %v", path, err)
	}
	return len(keys)
}

func testTACLExamples(ctx context.Context, t *testing.T, e taclEnv) {
	files, err := filepath.Glob(filepath.Join(repoRoot(t), "examples", "policies", "templated", "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no templated policy examples: %v", err)
	}
	for _, file := range files {
		for _, obj := range taclDecode(t, file) {
			namespaced, err := e.k8s.IsObjectNamespaced(obj)
			if err != nil {
				t.Fatalf("%s: %s: %v", filepath.Base(file), obj.GetKind(), err)
			}
			if namespaced {
				obj.SetNamespace(namespace)
			}
			if err := e.k8s.Create(ctx, obj, client.DryRunAll); err != nil {
				t.Errorf("%s: %s %s rejected: %v", filepath.Base(file), obj.GetKind(), obj.GetName(), err)
			}
		}
	}
}

func testTACLRoot(ctx context.Context, t *testing.T, e taclEnv) {
	accessor := taclBackend(ctx, t, e, "tacl", taclRootConn)
	saPolicy := taclPolicy(taclRootConn, "tacl-sa", "", taclRW(taclSAPath(accessor)))
	entityPolicy := taclPolicy(taclRootConn, "tacl-entity", "",
		taclRW("apps/data/entity/{{identity.entity.name}}/*")+taclRW("apps/data/entity-id/{{identity.entity.id}}/*"))
	groupPolicy := taclPolicy(taclRootConn, "tacl-group", "",
		taclRW("apps/data/group/{{identity.groups.names.tacl-devs.id}}/*"))
	role := taclRole(taclRootConn, "tacl-apps", "tacl-sa", "tacl-entity", "tacl-group")

	alice := &identityv1alpha1.IdentityEntity{
		ObjectMeta: metav1.ObjectMeta{Name: "tacl-alice", Namespace: namespace},
		Spec:       identityv1alpha1.IdentityEntitySpec{ResourceSpec: taclSpec(taclRootConn)},
	}
	aliceAlias := &identityv1alpha1.IdentityEntityAlias{
		ObjectMeta: metav1.ObjectMeta{Name: "tacl-alice-k8s", Namespace: namespace},
		Spec: identityv1alpha1.IdentityEntityAliasSpec{
			ResourceSpec: taclSpec(taclRootConn),
			AliasName:    string(e.sas["tacl-a"].UID),
			MountRef:     &commonv1alpha1.LocalObjectReference{Name: "tacl-kubernetes"},
			EntityRef:    &commonv1alpha1.LocalObjectReference{Name: "tacl-alice"},
		},
	}
	devs := &identityv1alpha1.IdentityGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "tacl-devs", Namespace: namespace},
		Spec:       identityv1alpha1.IdentityGroupSpec{ResourceSpec: taclSpec(taclRootConn)},
	}
	membership := &identityv1alpha1.IdentityGroupMembership{
		ObjectMeta: metav1.ObjectMeta{Name: "tacl-devs-alice", Namespace: namespace},
		Spec: identityv1alpha1.IdentityGroupMembershipSpec{
			ResourceSpec: taclSpec(taclRootConn),
			GroupRef:     &commonv1alpha1.LocalObjectReference{Name: "tacl-devs"},
			EntityRef:    &commonv1alpha1.LocalObjectReference{Name: "tacl-alice"},
		},
	}
	for _, obj := range []engine.Object{saPolicy, entityPolicy, groupPolicy, role, alice, aliceAlias, devs, membership} {
		mustCreate(ctx, t, e.k8s, obj)
		waitFor(t, obj.GetName()+" ready", func() error { return expectReady(ctx, e.k8s, obj) })
	}

	a := taclLogin(ctx, t, e, "", "tacl-a", "tacl-apps")
	b := taclLogin(ctx, t, e, "", "tacl-b", "tacl-apps")

	taclAllowed(ctx, t, a, "apps/data/sa/tacl-a/app/db")
	taclAllowed(ctx, t, b, "apps/data/sa/tacl-b/app/db")
	taclDenied(ctx, t, a, "apps/data/sa/tacl-b/app/db")
	taclDenied(ctx, t, b, "apps/data/sa/tacl-a/app/db")
	taclDenied(ctx, t, a, "apps/data/sa/tacl-a/other/db")
	taclDenied(ctx, t, a, "apps/data/shared/db")
	taclDenied(ctx, t, b, "apps/data/shared/db")

	taclAllowed(ctx, t, a, "apps/data/entity/tacl-alice/db")
	taclDenied(ctx, t, b, "apps/data/entity/tacl-alice/db")
	bEntity := taclEntity(ctx, t, e, b)
	taclAllowed(ctx, t, b, "apps/data/entity/"+bEntity["name"].(string)+"/db")
	taclAllowed(ctx, t, a, "apps/data/entity-id/"+alice.Status.ID+"/db")
	taclDenied(ctx, t, b, "apps/data/entity-id/"+alice.Status.ID+"/db")
	taclAllowed(ctx, t, b, "apps/data/entity-id/"+bEntity["id"].(string)+"/db")

	taclAllowed(ctx, t, a, "apps/data/group/"+devs.Status.ID+"/db")
	taclDenied(ctx, t, b, "apps/data/group/"+devs.Status.ID+"/db")

	taclDeleteDenied(ctx, t, a, "apps/data/sa/tacl-a/app/db")
	updated := saPolicy.DeepCopy()
	updated.Spec.Policy = strings.Replace(saPolicy.Spec.Policy, `"update"]`, `"update", "delete"]`, 1)
	if err := e.k8s.Patch(ctx, updated, client.MergeFrom(saPolicy)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "policy update written", func() error {
		return expectPolicyContains(ctx, e.bao, "sys/policies/acl/tacl-sa", `"update", "delete"]`)
	})
	fresh := taclLogin(ctx, t, e, "", "tacl-a", "tacl-apps")
	if _, err := fresh.Logical().DeleteWithContext(ctx, "apps/data/sa/tacl-a/app/db"); err != nil {
		t.Errorf("delete with the added capability: %v", err)
	}
	taclDeleteDenied(ctx, t, b, "apps/data/sa/tacl-a/app/db")
}

func testTACLAccessGrant(ctx context.Context, t *testing.T, e taclEnv) {
	level := &accessv1alpha1.AccessLevel{
		ObjectMeta: metav1.ObjectMeta{Name: "tacl-rw", Namespace: namespace},
		Spec: accessv1alpha1.AccessLevelSpec{
			Data:     []accessv1alpha1.AccessCapability{"create", "read", "update"},
			Metadata: []accessv1alpha1.AccessCapability{"read", "list"},
		},
	}
	grant := &accessv1alpha1.AccessGrant{
		ObjectMeta: metav1.ObjectMeta{Name: "tacl-grant", Namespace: namespace},
		Spec: accessv1alpha1.AccessGrantSpec{
			ResourceSpec: taclSpec(taclRootConn),
			AuthMountRef: &commonv1alpha1.LocalObjectReference{Name: "tacl-kubernetes"},
			Subjects:     []accessv1alpha1.AccessSubject{{Name: taclAppSA, Namespace: "tacl-*"}},
			Rules: []accessv1alpha1.AccessRule{{
				AccessPath: accessv1alpha1.AccessPath{Mount: taclKVMount, Path: "grant/{sa.namespace}/{sa.name}/*",
					MetadataPath: "grant/{sa.namespace}/{sa.name}/*"},
				Level: "tacl-rw",
			}},
		},
	}
	mustCreate(ctx, t, e.k8s, level)
	mustCreate(ctx, t, e.k8s, grant)
	waitFor(t, "grant ready", func() error { return expectReady(ctx, e.k8s, grant) })
	if len(grant.Status.Roles) != 1 {
		t.Fatalf("status.roles = %v, want one role", grant.Status.Roles)
	}

	a := taclLogin(ctx, t, e, "", "tacl-a", grant.Status.Roles[0])
	b := taclLogin(ctx, t, e, "", "tacl-b", grant.Status.Roles[0])
	taclAllowed(ctx, t, a, "apps/data/grant/tacl-a/app/db")
	taclAllowed(ctx, t, b, "apps/data/grant/tacl-b/app/db")
	taclDenied(ctx, t, a, "apps/data/grant/tacl-b/app/db")
	taclDenied(ctx, t, b, "apps/data/grant/tacl-a/app/db")
	taclDenied(ctx, t, a, "apps/data/grant/tacl-a/other/db")
	taclDenied(ctx, t, a, "apps/data/sa/tacl-a/app/db")
	if keys, err := a.Logical().ListWithContext(ctx, "apps/metadata/grant/tacl-a/app/"); err != nil || keys == nil {
		t.Errorf("list own metadata: %v, %v", keys, err)
	}
	if _, err := a.Logical().ListWithContext(ctx, "apps/metadata/grant/tacl-b/app/"); !taclForbidden(err) {
		t.Errorf("list of another ServiceAccount's metadata: got %v, want 403", err)
	}
}

func testTACLChildNamespace(ctx context.Context, t *testing.T, e taclEnv) {
	ns := &sysv1alpha1.BaoNamespace{
		ObjectMeta: metav1.ObjectMeta{Name: taclTeamNS, Namespace: namespace},
		Spec:       sysv1alpha1.BaoNamespaceSpec{ResourceSpec: taclSpec(taclRootConn)},
	}
	mustCreate(ctx, t, e.k8s, ns)
	waitFor(t, "namespace ready", func() error { return expectReady(ctx, e.k8s, ns) })

	accessor := taclBackend(ctx, t, e, "tacl-team", taclTeamConn)
	saPolicy := taclPolicy(taclTeamConn, "tacl-team-sa", "", taclRW(taclSAPath(accessor)))
	saPolicy.Spec.Name = "tacl-sa"
	idPolicy := taclPolicy(taclRootConn, "tacl-team-entity-id", taclTeamNS,
		taclRW("apps/data/entity-id/{{identity.entity.id}}/*"))
	idPolicy.Spec.Name = "tacl-entity-id"
	role := taclRole(taclTeamConn, "tacl-team-apps", "tacl-sa", "tacl-entity-id")
	role.Spec.Name = "apps"
	for _, obj := range []engine.Object{saPolicy, idPolicy, role} {
		mustCreate(ctx, t, e.k8s, obj)
		waitFor(t, obj.GetName()+" ready", func() error { return expectReady(ctx, e.k8s, obj) })
	}
	if got := idPolicy.Status.Namespace; strings.Trim(got, "/") != taclTeamNS {
		t.Errorf("status.openbaoNamespace of the spec.openbaoNamespace policy = %q, want %s", got, taclTeamNS)
	}
	if _, err := e.bao.Read(ctx, "sys/policies/acl/tacl-entity-id"); !errors.Is(err, openbao.ErrNotFound) {
		t.Errorf("child-namespace policy leaked into the root namespace: %v", err)
	}

	a := taclLogin(ctx, t, e, taclTeamNS, "tacl-a", "apps")
	b := taclLogin(ctx, t, e, taclTeamNS, "tacl-b", "apps")
	taclAllowed(ctx, t, a, "apps/data/sa/tacl-a/app/db")
	taclAllowed(ctx, t, b, "apps/data/sa/tacl-b/app/db")
	taclDenied(ctx, t, a, "apps/data/sa/tacl-b/app/db")
	taclDenied(ctx, t, b, "apps/data/sa/tacl-a/app/db")
	taclDenied(ctx, t, a, "apps/data/shared/db")

	aEntity := taclEntity(ctx, t, e, a)
	taclAllowed(ctx, t, a, "apps/data/entity-id/"+aEntity["id"].(string)+"/db")
	taclDenied(ctx, t, b, "apps/data/entity-id/"+aEntity["id"].(string)+"/db")
}

// taclBackend enables the Kubernetes auth method and a KV v2 engine through conn, configures the auth
// method against the envtest API server and returns the accessor of the auth mount.
func taclBackend(ctx context.Context, t *testing.T, e taclEnv, prefix, conn string) string {
	t.Helper()
	if len(e.kube.CAData) == 0 {
		t.Fatal("envtest rest config has no CA data")
	}
	authMount := &sysv1alpha1.AuthMount{
		ObjectMeta: metav1.ObjectMeta{Name: prefix + "-kubernetes", Namespace: namespace},
		Spec:       sysv1alpha1.AuthMountSpec{ResourceSpec: taclSpec(conn), Path: taclAuthMount, Type: taclAuthMount},
	}
	kv := &sysv1alpha1.SecretsMount{
		ObjectMeta: metav1.ObjectMeta{Name: prefix + "-apps", Namespace: namespace},
		Spec:       sysv1alpha1.SecretsMountSpec{ResourceSpec: taclSpec(conn), Path: taclKVMount, Type: "kv-v2"},
	}
	authConfig := &authv1alpha1.KubernetesAuthConfig{
		ObjectMeta: metav1.ObjectMeta{Name: prefix + "-k8s", Namespace: namespace},
		Spec: authv1alpha1.KubernetesAuthConfigSpec{
			ResourceSpec:              taclSpec(conn),
			MountPath:                 taclAuthMount,
			KubernetesHost:            taclKubeHost(e.kube),
			KubernetesCACert:          string(e.kube.CAData),
			TokenReviewerJWTSecretRef: &commonv1alpha1.SecretKeySelector{Name: taclReviewer, Key: "token"},
			DisableLocalCAJWT:         new(true),
		},
	}
	for _, obj := range []engine.Object{authMount, kv, authConfig} {
		mustCreate(ctx, t, e.k8s, obj)
		waitFor(t, obj.GetName()+" ready", func() error { return expectReady(ctx, e.k8s, obj) })
	}
	if !strings.HasPrefix(authMount.Status.ID, "auth_kubernetes_") {
		t.Fatalf("AuthMount status.id = %q, want the mount accessor", authMount.Status.ID)
	}
	return authMount.Status.ID
}

// taclKubernetes creates the two application namespaces with their ServiceAccount, and a token reviewer
// ServiceAccount bound to system:auth-delegator whose TokenRequest token is stored in a Secret.
func taclKubernetes(ctx context.Context, t *testing.T, e *taclEnv) {
	t.Helper()
	e.sas = map[string]*corev1.ServiceAccount{}
	for _, ns := range append([]string{taclSystemNS}, taclAppNamespaces...) {
		mustCreate(ctx, t, e.k8s, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
	}
	for _, ns := range taclAppNamespaces {
		sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: taclAppSA, Namespace: ns}}
		mustCreate(ctx, t, e.k8s, sa)
		e.sas[ns] = sa
	}
	reviewer := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: taclReviewer, Namespace: taclSystemNS}}
	mustCreate(ctx, t, e.k8s, reviewer)
	mustCreate(ctx, t, e.k8s, &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: taclReviewer},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "system:auth-delegator"},
		Subjects:   []rbacv1.Subject{{Kind: rbacv1.ServiceAccountKind, Name: taclReviewer, Namespace: taclSystemNS}},
	})
	mustCreate(ctx, t, e.k8s, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: taclReviewer, Namespace: namespace},
		Data:       map[string][]byte{"token": []byte(taclToken(ctx, t, e.k8s, reviewer))},
	})
}

func taclPolicy(conn, name, baoNamespace, text string) *sysv1alpha1.ACLPolicy {
	p := &sysv1alpha1.ACLPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       sysv1alpha1.ACLPolicySpec{ResourceSpec: taclSpec(conn), Policy: text},
	}
	p.Spec.Namespace = baoNamespace
	return p
}

// taclRW renders an ACL block that grants create, read and update on path.
func taclRW(path string) string {
	return `path "` + path + `" {` + "\n  capabilities = [\"create\", \"read\", \"update\"]\n}\n"
}

// taclSAPath is apps/data/sa/<namespace>/<name>/* of the ServiceAccount that logged in through the auth
// mount with this accessor.
func taclSAPath(accessor string) string {
	alias := "{{identity.entity.aliases." + accessor + ".metadata."
	return "apps/data/sa/" + alias + "service_account_namespace}}/" + alias + "service_account_name}}/*"
}

func taclRole(conn, name string, policies ...string) *authv1alpha1.KubernetesAuthRole {
	return &authv1alpha1.KubernetesAuthRole{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: authv1alpha1.KubernetesAuthRoleSpec{
			ResourceSpec:                  taclSpec(conn),
			MountPath:                     taclAuthMount,
			BoundServiceAccountNames:      []string{taclAppSA},
			BoundServiceAccountNamespaces: taclAppNamespaces,
			TokenFields: authv1alpha1.TokenFields{
				TokenTTL:      &metav1.Duration{Duration: 10 * time.Minute},
				TokenPolicies: policies,
			},
		},
	}
}

func taclSpec(conn string) commonv1alpha1.ResourceSpec {
	return commonv1alpha1.ResourceSpec{ConnectionRef: commonv1alpha1.ConnectionReference{Name: conn}}
}

func taclKubeHost(cfg *rest.Config) string {
	if strings.Contains(cfg.Host, "://") {
		return cfg.Host
	}
	return "https://" + cfg.Host
}

// taclToken issues a short-lived token for sa through the TokenRequest API.
func taclToken(ctx context.Context, t *testing.T, k8s client.Client, sa *corev1.ServiceAccount) string {
	t.Helper()
	request := &authenticationv1.TokenRequest{Spec: authenticationv1.TokenRequestSpec{ExpirationSeconds: new(int64(3600))}}
	if err := k8s.SubResource("token").Create(ctx, sa, request); err != nil {
		t.Fatalf("token request for %s/%s: %v", sa.Namespace, sa.Name, err)
	}
	return request.Status.Token
}

// taclLogin logs in as the "app" ServiceAccount of a Kubernetes namespace and returns a client that carries
// the resulting token, scoped to the OpenBao namespace baoNamespace.
func taclLogin(ctx context.Context, t *testing.T, e taclEnv, baoNamespace, k8sNamespace, role string) *baoapi.Client {
	t.Helper()
	c, err := baoapi.NewClient(&baoapi.Config{Address: e.addr, HttpClient: &http.Client{Timeout: 10 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	c.ClearToken()
	c.ClearNamespace()
	if baoNamespace != "" {
		c.SetNamespace(baoNamespace)
	}
	jwt := taclToken(ctx, t, e.k8s, e.sas[k8sNamespace])
	var login *baoapi.Secret
	waitFor(t, "login as "+k8sNamespace+"/"+taclAppSA, func() error {
		login, err = c.Logical().WriteWithContext(ctx, "auth/"+taclAuthMount+"/login", map[string]any{"role": role, "jwt": jwt})
		if err == nil && (login == nil || login.Auth == nil) {
			err = errors.New("login returned no auth block")
		}
		return err
	})
	c.SetToken(login.Auth.ClientToken)
	return c
}

// taclEntity returns the entity behind the token of c, read with the root token.
func taclEntity(ctx context.Context, t *testing.T, e taclEnv, c *baoapi.Client) map[string]any {
	t.Helper()
	self, err := c.Auth().Token().LookupSelfWithContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := self.Data["entity_id"].(string)
	if id == "" {
		t.Fatal("token has no entity")
	}
	root := e.bao
	if ns := c.Headers().Get("X-Vault-Namespace"); ns != "" {
		root = e.bao.WithNamespace(ns)
	}
	entity, err := root.Read(ctx, "identity/entity/id/"+id)
	if err != nil {
		t.Fatal(err)
	}
	return entity
}

// taclAllowed writes a secret at path and reads it back with the token of c.
func taclAllowed(ctx context.Context, t *testing.T, c *baoapi.Client, path string) {
	t.Helper()
	if _, err := c.Logical().WriteWithContext(ctx, path, map[string]any{"data": map[string]any{"owner": path}}); err != nil {
		t.Errorf("write %s: %v", path, err)
		return
	}
	secret, err := c.Logical().ReadWithContext(ctx, path)
	if err != nil || secret == nil {
		t.Errorf("read %s: %v", path, err)
		return
	}
	if data, _ := secret.Data["data"].(map[string]any); data["owner"] != path {
		t.Errorf("read %s returned %v", path, secret.Data["data"])
	}
}

// taclDenied checks that the token of c can neither write nor read path.
func taclDenied(ctx context.Context, t *testing.T, c *baoapi.Client, path string) {
	t.Helper()
	if _, err := c.Logical().WriteWithContext(ctx, path, map[string]any{"data": map[string]any{"owner": "intruder"}}); !taclForbidden(err) {
		t.Errorf("write %s: got %v, want 403", path, err)
	}
	if _, err := c.Logical().ReadWithContext(ctx, path); !taclForbidden(err) {
		t.Errorf("read %s: got %v, want 403", path, err)
	}
}

func taclDeleteDenied(ctx context.Context, t *testing.T, c *baoapi.Client, path string) {
	t.Helper()
	if _, err := c.Logical().DeleteWithContext(ctx, path); !taclForbidden(err) {
		t.Errorf("delete %s: got %v, want 403", path, err)
	}
}

func taclForbidden(err error) bool {
	var resp *baoapi.ResponseError
	return errors.As(err, &resp) && resp.StatusCode == http.StatusForbidden
}

func taclDecode(t *testing.T, file string) []*unstructured.Unstructured {
	t.Helper()
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	decoder := utilyaml.NewYAMLOrJSONDecoder(f, 4096)
	var objects []*unstructured.Unstructured
	for {
		obj := &unstructured.Unstructured{}
		if err := decoder.Decode(&obj.Object); errors.Is(err, io.EOF) {
			return objects
		} else if err != nil {
			t.Fatalf("decode %s: %v", file, err)
		}
		if len(obj.Object) > 0 {
			objects = append(objects, obj)
		}
	}
}

func taclSetup(ctx context.Context, t *testing.T) taclEnv {
	t.Helper()
	root := repoRoot(t)
	baoBin, assets := secretsPrerequisites(t, root)

	addr := startBao(t, baoBin)
	bao, err := openbao.NewPool().Client(ctx, "itest-tacl", openbao.Config{
		Address: addr,
		Timeout: 10 * time.Second,
		Auth: openbao.TokenAuth{
			CredentialKey: "itest-tacl",
			Token:         func(context.Context) (string, error) { return rootToken, nil },
		},
	})
	if err != nil {
		t.Fatalf("openbao client: %v", err)
	}

	crdNames := map[string][]string{
		"sys":      {"authmounts", "secretsmounts", "aclpolicies", "baonamespaces", "plugins"},
		"auth":     {"kubernetesauthconfigs", "kubernetesauthroles"},
		"identity": {"identityentities", "identityentityaliases", "identitygroups", "identitygroupmemberships"},
		"access":   {"accesslevels", "accessgrants"},
		"core":     {"baoconnections"},
	}
	var crds []string
	for group, plurals := range crdNames {
		for _, plural := range plurals {
			crds = append(crds, filepath.Join(root, "config", "crd", "bases", group+".obco.dev_"+plural+".yaml"))
		}
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
	for _, add := range []func(*runtime.Scheme) error{
		clientgoscheme.AddToScheme, sysv1alpha1.AddToScheme, authv1alpha1.AddToScheme,
		identityv1alpha1.AddToScheme, accessv1alpha1.AddToScheme,
	} {
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
	conns := taclConnections{taclRootConn: bao, taclTeamConn: bao.WithNamespace(taclTeamNS)}
	opts := itestEngineOptions()
	for _, setupKind := range []func(ctrl.Manager, engine.Connections, engine.Options) error{
		sys.SetupAuthMount, sys.SetupSecretsMount, sys.SetupACLPolicy, sys.SetupBaoNamespace,
		auth.SetupKubernetesAuthConfig, auth.SetupKubernetesAuthRole,
		identity.SetupIdentityEntity, identity.SetupIdentityEntityAlias, identity.SetupIdentityGroup, identity.SetupIdentityGroupMembership,
		access.SetupAccessLevel, access.SetupAccessGrant,
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
	return taclEnv{env: env{k8s: mgr.GetClient(), bao: bao}, addr: addr, kube: cfg}
}
