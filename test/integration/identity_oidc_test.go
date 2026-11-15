//go:build integration

package integration

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	identityv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/identity/v1alpha1"
	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/controller/identity"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

func TestIntegrationIdentityOIDC(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e := identityOIDCSetup(ctx, t)

	t.Run("oidcconfig", func(t *testing.T) { testOIDCConfig(ctx, t, e) })
	t.Run("oidc provider chain", func(t *testing.T) { testOIDCProviderChain(ctx, t, e) })
	t.Run("mfa", func(t *testing.T) { testMFA(ctx, t, e) })
}

func oidcSpec(deletion commonv1alpha1.DeletionPolicy) commonv1alpha1.ResourceSpec {
	return commonv1alpha1.ResourceSpec{ConnectionRef: commonv1alpha1.ConnectionReference{Name: "dev"}, DeletionPolicy: deletion}
}

func testOIDCConfig(ctx context.Context, t *testing.T, e env) {
	const path = "identity/oidc/config"
	cfg := &identityv1alpha1.OIDCConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-oidc-config", Namespace: namespace},
		Spec: identityv1alpha1.OIDCConfigSpec{ResourceSpec: oidcSpec(commonv1alpha1.DeletionPolicyDelete),
			Issuer: "https://bao.example.com:8200"},
	}
	mustCreate(ctx, t, e.k8s, cfg)
	want := map[string]any{"issuer": "https://bao.example.com:8200"}
	waitFor(t, "issuer set", func() error { return oidcExpect(ctx, e.bao, path, want) })
	waitFor(t, "config ready", func() error { return expectReady(ctx, e.k8s, cfg) })

	mustWrite(ctx, t, e.bao, path, map[string]any{"issuer": "https://other.example.com"})
	waitFor(t, "issuer repaired", func() error { return oidcExpect(ctx, e.bao, path, want) })
	oidcExpectNoWrites(t, "OIDCConfig")

	if err := e.k8s.Delete(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "issuer reset", func() error { return oidcExpect(ctx, e.bao, path, map[string]any{"issuer": ""}) })
	waitFor(t, "config resource gone", func() error { return expectGone(ctx, e.k8s, cfg) })
}

func testOIDCProviderChain(ctx context.Context, t *testing.T, e env) {
	const (
		keyPath        = "identity/oidc/key/itest-key"
		rolePath       = "identity/oidc/role/itest-role"
		scopePath      = "identity/oidc/scope/itest-scope"
		assignmentPath = "identity/oidc/assignment/itest-assignment"
		clientPath     = "identity/oidc/client/itest-client"
		providerPath   = "identity/oidc/provider/itest-provider"
	)
	spec := oidcSpec(commonv1alpha1.DeletionPolicyDelete)
	hours := func(h int) *metav1.Duration { return &metav1.Duration{Duration: time.Duration(h) * time.Hour} }

	key := &identityv1alpha1.OIDCKey{ObjectMeta: metav1.ObjectMeta{Name: "itest-key", Namespace: namespace},
		Spec: identityv1alpha1.OIDCKeySpec{ResourceSpec: spec, RotationPeriod: hours(12), VerificationTTL: hours(6),
			Algorithm: "ES256", AllowedClientIDs: []string{"*"}}}
	mustCreate(ctx, t, e.k8s, key)
	waitFor(t, "key ready", func() error { return expectReady(ctx, e.k8s, key) })
	if err := oidcExpect(ctx, e.bao, keyPath, map[string]any{"algorithm": "ES256", "rotation_period": "43200"}); err != nil {
		t.Fatal(err)
	}
	mustWrite(ctx, t, e.bao, keyPath, map[string]any{"rotation_period": "1h"})
	waitFor(t, "key repaired", func() error { return oidcExpect(ctx, e.bao, keyPath, map[string]any{"rotation_period": "43200"}) })

	role := &identityv1alpha1.OIDCRole{ObjectMeta: metav1.ObjectMeta{Name: "itest-role", Namespace: namespace},
		Spec: identityv1alpha1.OIDCRoleSpec{ResourceSpec: spec, Key: "itest-key", TTL: hours(1),
			Template: `{"team": {{identity.entity.metadata.team}}}`}}
	mustCreate(ctx, t, e.k8s, role)
	waitFor(t, "role ready with client id", func() error { return oidcExpectID(ctx, e.k8s, role) })
	mustWrite(ctx, t, e.bao, rolePath, map[string]any{"ttl": "30m"})
	waitFor(t, "role repaired", func() error { return oidcExpect(ctx, e.bao, rolePath, map[string]any{"ttl": "3600"}) })

	scope := &identityv1alpha1.OIDCScope{ObjectMeta: metav1.ObjectMeta{Name: "itest-scope", Namespace: namespace},
		Spec: identityv1alpha1.OIDCScopeSpec{ResourceSpec: spec, Description: "groups",
			Template: `{"groups": {{identity.entity.groups.names}}}`}}
	mustCreate(ctx, t, e.k8s, scope)
	waitFor(t, "scope ready", func() error { return expectReady(ctx, e.k8s, scope) })
	mustWrite(ctx, t, e.bao, scopePath, map[string]any{"description": "tampered"})
	waitFor(t, "scope repaired", func() error { return oidcExpect(ctx, e.bao, scopePath, map[string]any{"description": "groups"}) })

	entity, err := e.bao.Write(ctx, "identity/entity", map[string]any{"name": "itest-alice"})
	if err != nil {
		t.Fatal(err)
	}
	group, err := e.bao.Write(ctx, "identity/group", map[string]any{"name": "itest-devs"})
	if err != nil {
		t.Fatal(err)
	}
	entityID, groupID := fmt.Sprint(entity["id"]), fmt.Sprint(group["id"])
	assignment := &identityv1alpha1.OIDCAssignment{ObjectMeta: metav1.ObjectMeta{Name: "itest-assignment", Namespace: namespace},
		Spec: identityv1alpha1.OIDCAssignmentSpec{ResourceSpec: spec,
			Entities: []identityv1alpha1.EntityMatch{{EntityName: "itest-alice"}},
			Groups:   []identityv1alpha1.GroupMatch{{GroupID: groupID}}}}
	mustCreate(ctx, t, e.k8s, assignment)
	waitFor(t, "assignment ready", func() error { return expectReady(ctx, e.k8s, assignment) })
	wantMembers := map[string][]string{"entity_ids": {entityID}, "group_ids": {groupID}}
	if err := oidcExpectLists(ctx, e.bao, assignmentPath, wantMembers); err != nil {
		t.Fatal(err)
	}
	mustWrite(ctx, t, e.bao, assignmentPath, map[string]any{"entity_ids": []string{}, "group_ids": []string{}})
	waitFor(t, "assignment repaired", func() error { return oidcExpectLists(ctx, e.bao, assignmentPath, wantMembers) })

	oidcClient := &identityv1alpha1.OIDCClient{ObjectMeta: metav1.ObjectMeta{Name: "itest-client", Namespace: namespace},
		Spec: identityv1alpha1.OIDCClientSpec{ResourceSpec: spec, Key: "itest-key", ClientType: "confidential",
			RedirectURIs: []string{"https://app.example.com/a", "https://app.example.com/b"},
			Assignments:  []string{"itest-assignment"}, IDTokenTTL: hours(1), AccessTokenTTL: hours(2),
			WriteSecretRef: &commonv1alpha1.LocalObjectReference{Name: "itest-client-credentials"}}}
	mustCreate(ctx, t, e.k8s, oidcClient)
	waitFor(t, "client ready with client id", func() error { return oidcExpectID(ctx, e.k8s, oidcClient) })
	waitFor(t, "client credentials written", func() error { return oidcExpectCredentials(ctx, e, clientPath, "itest-client-credentials") })

	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "itest-client-credentials", Namespace: namespace}}
	if err := e.k8s.Delete(ctx, secret); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "deleted credentials written again", func() error {
		return oidcExpectCredentials(ctx, e, clientPath, "itest-client-credentials")
	})

	mustWrite(ctx, t, e.bao, clientPath, map[string]any{"id_token_ttl": "10m"})
	waitFor(t, "client repaired", func() error { return oidcExpect(ctx, e.bao, clientPath, map[string]any{"id_token_ttl": "3600"}) })
	mustWrite(ctx, t, e.bao, clientPath, map[string]any{"redirect_uris": []string{"https://app.example.com/b", "https://app.example.com/a"}})
	oidcExpectNoWrites(t, "OIDCClient")

	provider := &identityv1alpha1.OIDCProvider{ObjectMeta: metav1.ObjectMeta{Name: "itest-provider", Namespace: namespace},
		Spec: identityv1alpha1.OIDCProviderSpec{ResourceSpec: spec, Issuer: "https://bao.example.com:8200",
			AllowedClients:  []identityv1alpha1.OIDCClientMatch{{ClientRef: &commonv1alpha1.LocalObjectReference{Name: "itest-client"}}},
			ScopesSupported: []string{"itest-scope"}}}
	mustCreate(ctx, t, e.k8s, provider)
	waitFor(t, "provider ready", func() error { return expectReady(ctx, e.k8s, provider) })
	wantProvider := map[string][]string{"allowed_client_ids": {oidcClient.Status.ID}, "scopes_supported": {"itest-scope"}}
	if err := oidcExpectLists(ctx, e.bao, providerPath, wantProvider); err != nil {
		t.Fatal(err)
	}
	mustWrite(ctx, t, e.bao, providerPath, map[string]any{"scopes_supported": []string{}})
	waitFor(t, "provider repaired", func() error { return oidcExpectLists(ctx, e.bao, providerPath, wantProvider) })
	oidcExpectNoWrites(t, "OIDCProvider")

	for _, step := range []struct {
		obj  client.Object
		path string
	}{
		{provider, providerPath}, {oidcClient, clientPath}, {assignment, assignmentPath}, {scope, scopePath},
		{role, rolePath}, {key, keyPath},
	} {
		if err := e.k8s.Delete(ctx, step.obj); err != nil {
			t.Fatal(err)
		}
		waitFor(t, step.path+" deleted", func() error { return expectAbsent(ctx, e.bao, step.path) })
		waitFor(t, step.obj.GetName()+" resource gone", func() error { return expectGone(ctx, e.k8s, step.obj) })
	}
}

func testMFA(ctx context.Context, t *testing.T, e env) {
	mustWrite(ctx, t, e.bao, "sys/auth/itest-userpass", map[string]any{"type": "userpass"})
	mustCreate(ctx, t, e.k8s, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-duo", Namespace: namespace},
		Data:       map[string][]byte{"secret-key": []byte("fake-secret"), "integration-key": []byte("fake-ikey")},
	})

	allowDelete := map[string]string{commonv1alpha1.AnnotationAllowDestructiveDelete: "true"}
	totp := &identityv1alpha1.MFAMethod{ObjectMeta: metav1.ObjectMeta{Name: "itest-totp", Namespace: namespace, Annotations: allowDelete},
		Spec: identityv1alpha1.MFAMethodSpec{ResourceSpec: oidcSpec(commonv1alpha1.DeletionPolicyDelete),
			TOTP: &identityv1alpha1.MFATOTP{Issuer: "Acme", Period: &metav1.Duration{Duration: 30 * time.Second}}}}
	mustCreate(ctx, t, e.k8s, totp)
	waitFor(t, "totp ready with id", func() error { return oidcExpectID(ctx, e.k8s, totp) })
	totpPath := "identity/mfa/method/totp/" + totp.Status.ID
	mustWrite(ctx, t, e.bao, totpPath, map[string]any{"method_name": "itest-totp", "issuer": "Intruder"})
	waitFor(t, "totp repaired", func() error { return oidcExpect(ctx, e.bao, totpPath, map[string]any{"issuer": "Acme"}) })
	oidcExpectNoWrites(t, "MFAMethod")

	duo := &identityv1alpha1.MFAMethod{ObjectMeta: metav1.ObjectMeta{Name: "itest-duo", Namespace: namespace},
		Spec: identityv1alpha1.MFAMethodSpec{ResourceSpec: oidcSpec(""), Duo: &identityv1alpha1.MFADuo{
			SecretKeySecretRef:      commonv1alpha1.SecretKeySelector{Name: "itest-duo", Key: "secret-key"},
			IntegrationKeySecretRef: commonv1alpha1.SecretKeySelector{Name: "itest-duo", Key: "integration-key"},
			APIHostname:             "api-fake.duosecurity.com", PushInfo: "from=itest",
		}}}
	mustCreate(ctx, t, e.k8s, duo)
	waitFor(t, "duo ready with id", func() error { return oidcExpectID(ctx, e.k8s, duo) })
	duoPath := "identity/mfa/method/duo/" + duo.Status.ID
	if err := oidcExpect(ctx, e.bao, duoPath, map[string]any{"api_hostname": "api-fake.duosecurity.com", "pushinfo": "from=itest"}); err != nil {
		t.Fatal(err)
	}
	before := oidcWrites(t, "MFAMethod")
	rotated := &corev1.Secret{}
	if err := e.k8s.Get(ctx, client.ObjectKey{Namespace: namespace, Name: "itest-duo"}, rotated); err != nil {
		t.Fatal(err)
	}
	rotated.Data["secret-key"] = []byte("fake-secret-rotated")
	if err := e.k8s.Update(ctx, rotated); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "rotated duo secret written", func() error {
		if now := oidcWrites(t, "MFAMethod"); now <= before {
			return fmt.Errorf("no write after rotation (%v)", now)
		}
		return nil
	})

	mustCreate(ctx, t, e.k8s, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-pingid", Namespace: namespace},
		Data: map[string][]byte{"settings": []byte("use_base64_key=fake\nuse_signature=true\ntoken=fake\n" +
			"idp_url=https://idp.example\norg_alias=org\nadmin_url=https://admin.example\nauthenticator_url=https://auth.example\n")},
	})
	pingID := &identityv1alpha1.MFAMethod{ObjectMeta: metav1.ObjectMeta{Name: "itest-pingid", Namespace: namespace, Annotations: allowDelete},
		Spec: identityv1alpha1.MFAMethodSpec{ResourceSpec: oidcSpec(commonv1alpha1.DeletionPolicyDelete), PingID: &identityv1alpha1.MFAPingID{
			SettingsFileSecretRef: commonv1alpha1.SecretKeySelector{Name: "itest-pingid", Key: "settings"},
			UsernameFormat:        "{{identity.entity.name}}",
		}}}
	mustCreate(ctx, t, e.k8s, pingID)
	waitFor(t, "pingid ready with id", func() error { return oidcExpectID(ctx, e.k8s, pingID) })
	oidcExpectNoWrites(t, "MFAMethod")
	if err := e.k8s.Delete(ctx, pingID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "pingid deleted", func() error { return expectAbsent(ctx, e.bao, "identity/mfa/method/pingid/"+pingID.Status.ID) })

	enforcement := &identityv1alpha1.MFALoginEnforcement{ObjectMeta: metav1.ObjectMeta{Name: "itest-enforcement", Namespace: namespace},
		Spec: identityv1alpha1.MFALoginEnforcementSpec{ResourceSpec: oidcSpec(commonv1alpha1.DeletionPolicyDelete),
			MFAMethods: []identityv1alpha1.MFAMethodMatch{
				{MethodRef: &commonv1alpha1.LocalObjectReference{Name: "itest-totp"}}, {MethodName: "itest-duo"},
			},
			AuthMounts:      []identityv1alpha1.AuthMountMatch{{MountPath: "itest-userpass"}},
			AuthMethodTypes: []string{"userpass"},
		}}
	mustCreate(ctx, t, e.k8s, enforcement)
	waitFor(t, "enforcement ready with id", func() error { return oidcExpectID(ctx, e.k8s, enforcement) })
	enforcementPath := "identity/mfa/login-enforcement/itest-enforcement"
	wantEnforcement := map[string][]string{"mfa_method_ids": {totp.Status.ID, duo.Status.ID}, "auth_method_types": {"userpass"}}
	if err := oidcExpectLists(ctx, e.bao, enforcementPath, wantEnforcement); err != nil {
		t.Fatal(err)
	}
	mustWrite(ctx, t, e.bao, enforcementPath, map[string]any{"mfa_method_ids": []string{totp.Status.ID}, "auth_method_types": []string{}})
	waitFor(t, "enforcement repaired", func() error { return oidcExpectLists(ctx, e.bao, enforcementPath, wantEnforcement) })
	oidcExpectNoWrites(t, "MFALoginEnforcement")

	if err := e.k8s.Delete(ctx, enforcement); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "enforcement deleted", func() error { return expectAbsent(ctx, e.bao, enforcementPath) })
	if err := e.k8s.Delete(ctx, totp); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "totp deleted", func() error { return expectAbsent(ctx, e.bao, totpPath) })
	if err := e.k8s.Delete(ctx, duo); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "duo resource gone", func() error { return expectGone(ctx, e.k8s, duo) })
	if err := oidcExpect(ctx, e.bao, duoPath, map[string]any{"name": "itest-duo"}); err != nil {
		t.Errorf("orphaned duo method: %v", err)
	}
}

// oidcExpect compares the given keys of a read, formatted with fmt.Sprint.
func oidcExpect(ctx context.Context, bao openbao.Client, path string, want map[string]any) error {
	data, err := bao.Read(ctx, path)
	if err != nil {
		return err
	}
	for key, value := range want {
		if got := fmt.Sprint(data[key]); got != fmt.Sprint(value) {
			return fmt.Errorf("%s %s = %s, want %v", path, key, got, value)
		}
	}
	return nil
}

// oidcExpectLists compares list keys of a read without regard to order.
func oidcExpectLists(ctx context.Context, bao openbao.Client, path string, want map[string][]string) error {
	data, err := bao.Read(ctx, path)
	if err != nil {
		return err
	}
	for key, values := range want {
		raw, _ := data[key].([]any)
		got := make([]string, 0, len(raw))
		for _, v := range raw {
			got = append(got, fmt.Sprint(v))
		}
		expected := slices.Clone(values)
		slices.Sort(got)
		slices.Sort(expected)
		if !slices.Equal(got, expected) {
			return fmt.Errorf("%s %s = %v, want %v", path, key, got, expected)
		}
	}
	return nil
}

func oidcExpectID(ctx context.Context, k8s client.Client, obj engine.Object) error {
	if err := expectReady(ctx, k8s, obj); err != nil {
		return err
	}
	if obj.GetResourceStatus().ID == "" {
		return fmt.Errorf("%s has no status.id", obj.GetName())
	}
	return nil
}

// oidcExpectCredentials checks that the output Secret holds the client's current credentials, without printing
// them.
func oidcExpectCredentials(ctx context.Context, e env, path, secretName string) error {
	data, err := e.bao.Read(ctx, path)
	if err != nil {
		return err
	}
	secret := &corev1.Secret{}
	if err := e.k8s.Get(ctx, client.ObjectKey{Namespace: namespace, Name: secretName}, secret); err != nil {
		return err
	}
	if string(secret.Data[identity.OIDCClientIDKey]) != data["client_id"] ||
		string(secret.Data[identity.OIDCClientSecretKey]) != data["client_secret"] || len(secret.Data[identity.OIDCClientSecretKey]) == 0 {
		return fmt.Errorf("secret %s does not hold the client credentials", secretName)
	}
	if len(secret.OwnerReferences) != 1 || secret.OwnerReferences[0].Kind != "OIDCClient" {
		return fmt.Errorf("secret %s is not owned by the OIDCClient", secretName)
	}
	return nil
}

// oidcExpectNoWrites checks that openbao_writes_total for kind stays flat over three drift intervals.
func oidcExpectNoWrites(t *testing.T, kind string) {
	t.Helper()
	before := oidcWrites(t, kind)
	if before == 0 {
		t.Fatalf("no %s writes counted; the metric lookup is broken", kind)
	}
	stayFor(t, kind+" writes nothing at rest", 3*driftInterval, func() error {
		if now := oidcWrites(t, kind); now != before {
			return fmt.Errorf("writes went from %v to %v", before, now)
		}
		return nil
	})
}

func oidcWrites(t *testing.T, kind string) float64 {
	t.Helper()
	families, err := metrics.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "openbao_writes_total" {
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

// identityOIDCSetup starts bao -dev, envtest with the OIDC and MFA CRDs, and a manager running their controllers.
func identityOIDCSetup(ctx context.Context, t *testing.T) env {
	t.Helper()
	root := repoRoot(t)
	baoBin, assets := secretsPrerequisites(t, root)

	addr := startBao(t, baoBin)
	bao, err := openbao.NewPool().Client(ctx, "itest-identity-oidc", openbao.Config{
		Address: addr,
		Timeout: 10 * time.Second,
		Auth: openbao.TokenAuth{
			CredentialKey: "itest-identity-oidc",
			Token:         func(context.Context) (string, error) { return rootToken, nil },
		},
	})
	if err != nil {
		t.Fatalf("openbao client: %v", err)
	}

	crds := make([]string, 0, 12)
	for _, name := range []string{"oidcconfigs", "oidckeys", "oidcroles", "oidcscopes", "oidcassignments", "oidcclients",
		"oidcproviders", "mfamethods", "mfaloginenforcements", "entities", "groups"} {
		crds = append(crds, filepath.Join(root, "config", "crd", "bases", "identity.openbaoconfig.io_"+name+".yaml"))
	}
	crds = append(crds, filepath.Join(root, "config", "crd", "bases", "sys.openbaoconfig.io_authmounts.yaml"))
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
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		Controller:             config.Controller{SkipNameValidation: new(true)},
	})
	if err != nil {
		t.Fatal(err)
	}
	conns := staticConnections{client: bao}
	opts := itestEngineOptions()
	for _, setupKind := range []func(ctrl.Manager, engine.Connections, engine.Options) error{
		identity.SetupOIDCConfig, identity.SetupOIDCKey, identity.SetupOIDCRole, identity.SetupOIDCScope,
		identity.SetupOIDCAssignment, identity.SetupOIDCClient, identity.SetupOIDCProvider, identity.SetupMFAMethod,
		identity.SetupMFALoginEnforcement,
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
