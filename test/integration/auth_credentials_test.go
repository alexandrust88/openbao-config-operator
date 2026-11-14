//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/controller/auth"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// authCredTestCA is a public, self-signed test CA certificate (CN=obco-test-ca); its key was discarded.
const authCredTestCA = `-----BEGIN CERTIFICATE-----
MIIBYTCCAQigAwIBAgIUDMw3azJKyUnZ9ymx9nkMtOKG9E4wCgYIKoZIzj0EAwIw
FzEVMBMGA1UEAwwMb2Jjby10ZXN0LWNhMB4XDTI2MTExMzIyMjQzMVoXDTM2MTEx
MDIyMjQzMVowFzEVMBMGA1UEAwwMb2Jjby10ZXN0LWNhMFkwEwYHKoZIzj0CAQYI
KoZIzj0DAQcDQgAEFoZh98uaZo4/3v5cSywC0uH8ZiSzf95fG9lCWe6uidPOb9Dy
C265JytUU1VV9tZ247yA5L72vzWiOTDa/9K3z6MyMDAwHQYDVR0OBBYEFAiOukMR
doR39VvDsVQ++aFecP+GMA8GA1UdEwEB/wQFMAMBAf8wCgYIKoZIzj0EAwIDRwAw
RAIgBLc18g1ypCtJR5OaZI02SIu41XxJdIhT7jGPEsF57AkCIC92H/jJrXlX07vC
ymDvgfvtQ0UWHcdGzHZe+eCO1ZqD
-----END CERTIFICATE-----
`

// TestIntegrationAuthCredentials runs AppRole, AppRoleSecretID, TokenRole, CertAuthConfig, CertAuthRole and
// UserpassUser against envtest and bao server -dev.
func TestIntegrationAuthCredentials(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e := authCredSetup(ctx, t)
	for path, typ := range map[string]string{authITApprole: authITApprole, "cert": "cert", "userpass": "userpass"} {
		mustWrite(ctx, t, e.bao, "sys/auth/"+path, map[string]any{authITType: typ})
	}

	t.Run(authITApprole, func(t *testing.T) { testAuthCredAppRole(ctx, t, e) })
	t.Run("tokenrole", func(t *testing.T) { testAuthCredTokenRole(ctx, t, e) })
	t.Run("cert", func(t *testing.T) { testAuthCredCert(ctx, t, e) })
	t.Run("userpass", func(t *testing.T) { testAuthCredUserpass(ctx, t, e) })
	t.Run("defaults", func(t *testing.T) { testAuthCredDefaults(ctx, t, e) })
}

// testAuthCredDefaults creates each kind with only its required fields and proves that nothing is written at rest.
func testAuthCredDefaults(ctx context.Context, t *testing.T, e env) {
	for _, path := range []string{"approle-defaults", authITCertDefaults, "userpass-defaults"} {
		mustWrite(ctx, t, e.bao, "sys/auth/"+path, map[string]any{authITType: strings.TrimSuffix(path, "-defaults")})
	}
	mustCreate(ctx, t, e.k8s, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-defaults-login", Namespace: namespace},
		Data:       map[string][]byte{authITPassword: []byte("fake-password")},
	})
	objMeta := metav1.ObjectMeta{Name: "itest-defaults", Namespace: namespace}
	authDefaultsNoWrites(ctx, t, e, []engine.Object{
		&authv1alpha1.AppRole{ObjectMeta: objMeta, Spec: authv1alpha1.AppRoleSpec{
			ResourceSpec: authCredSpec(), MountPath: "approle-defaults",
		}},
		&authv1alpha1.TokenRole{ObjectMeta: objMeta, Spec: authv1alpha1.TokenRoleSpec{ResourceSpec: authCredSpec()}},
		&authv1alpha1.CertAuthConfig{ObjectMeta: objMeta, Spec: authv1alpha1.CertAuthConfigSpec{
			ResourceSpec: authCredAdoptSpec(), MountPath: authITCertDefaults,
		}},
		&authv1alpha1.CertAuthRole{ObjectMeta: objMeta, Spec: authv1alpha1.CertAuthRoleSpec{
			ResourceSpec: authCredSpec(), MountPath: authITCertDefaults, Certificate: authCredTestCA,
		}},
		&authv1alpha1.UserpassUser{ObjectMeta: objMeta, Spec: authv1alpha1.UserpassUserSpec{
			ResourceSpec: authCredSpec(), MountPath: "userpass-defaults",
			PasswordSecretRef: &commonv1alpha1.SecretKeySelector{Name: "itest-defaults-login", Key: authITPassword},
		}},
	})
}

func authCredSpec() commonv1alpha1.ResourceSpec {
	return commonv1alpha1.ResourceSpec{ConnectionRef: commonv1alpha1.ConnectionReference{Name: audDev}}
}

// authCredAdoptSpec is for the cert auth config, which always exists in OpenBao.
func authCredAdoptSpec() commonv1alpha1.ResourceSpec {
	spec := authCredSpec()
	spec.CreationPolicy = commonv1alpha1.CreationPolicyCreateOrAdopt
	return spec
}

func testAuthCredAppRole(ctx context.Context, t *testing.T, e env) {
	const rolePath = "auth/approle/role/itest-ci"
	role := &authv1alpha1.AppRole{
		ObjectMeta: metav1.ObjectMeta{Name: authITItestCi, Namespace: namespace},
		Spec: authv1alpha1.AppRoleSpec{
			ResourceSpec: builtinAuthDeleteSpec(),
			SecretIDTTL:  &metav1.Duration{Duration: time.Hour},
			TokenFields: authv1alpha1.TokenFields{
				TokenBaseFields: authv1alpha1.TokenBaseFields{TokenBoundCIDRs: []string{"127.0.0.1/32", authITCIDR}},
				TokenTTL:        &metav1.Duration{Duration: time.Hour},
				TokenPolicies:   []string{"b", "a"},
			},
		},
	}
	mustCreate(ctx, t, e.k8s, role)
	waitFor(t, "approle created", func() error { return authCredExpect(ctx, e.bao, rolePath, authITTokenTTL, "3600") })
	waitFor(t, "approle ready with role id", func() error {
		if err := expectReady(ctx, e.k8s, role); err != nil {
			return err
		}
		if role.Status.ID == "" {
			return errors.New("status.id is empty")
		}
		return nil
	})

	mustWrite(ctx, t, e.bao, rolePath, map[string]any{authITTokenTTL: "60s"})
	waitFor(t, "approle drift repaired", func() error {
		return authCredExpect(ctx, e.bao, rolePath, authITTokenTTL, "3600")
	})
	mustWrite(ctx, t, e.bao, rolePath, map[string]any{authITTokenPolicies: []string{"a", "b"}})
	expectNoWrites(t, "AppRole")

	t.Run("secretid by roleRef", func(t *testing.T) { testAuthCredSecretIDByRef(ctx, t, e, role) })
	t.Run("secretid rotation", func(t *testing.T) { testAuthCredSecretIDRotation(ctx, t, e) })

	if err := e.k8s.Delete(ctx, role); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "approle deleted", func() error { return expectAbsent(ctx, e.bao, rolePath) })
	waitFor(t, "approle resource gone", func() error { return expectGone(ctx, e.k8s, role) })
}

func testAuthCredSecretIDByRef(ctx context.Context, t *testing.T, e env, role *authv1alpha1.AppRole) {
	sid := &authv1alpha1.AppRoleSecretID{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-ci-login", Namespace: namespace},
		Spec: authv1alpha1.AppRoleSecretIDSpec{
			ResourceSpec:   builtinAuthDeleteSpec(),
			RoleRef:        &commonv1alpha1.LocalObjectReference{Name: role.Name},
			Metadata:       map[string]string{taclTeamConn: "platform"},
			TTL:            &metav1.Duration{Duration: time.Hour},
			WriteSecretRef: commonv1alpha1.LocalObjectReference{Name: "itest-ci-approle"},
		},
	}
	mustCreate(ctx, t, e.k8s, sid)

	var first map[string][]byte
	waitFor(t, "secret id delivered", func() error {
		var err error
		first, err = authCredOutputs(ctx, e, sid)
		return err
	})
	if string(first["role_id"]) != role.Status.ID {
		t.Errorf("role_id in the Secret differs from the AppRole status.id")
	}
	if err := authCredLogin(ctx, e.bao, first); err != nil {
		t.Fatalf("login with the delivered secret id: %v", err)
	}
	waitFor(t, "secret id ready", func() error { return expectReady(ctx, e.k8s, sid) })
	if sid.Status.RolePath != authCredCIRolePath || sid.Status.ExpirationTime == nil {
		t.Errorf("status = %+v", sid.Status)
	}

	sibling := &authv1alpha1.AppRoleSecretID{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-ci-sibling", Namespace: namespace},
		Spec: authv1alpha1.AppRoleSecretIDSpec{
			ResourceSpec:   builtinAuthDeleteSpec(),
			RoleName:       authITItestCi,
			WriteSecretRef: commonv1alpha1.LocalObjectReference{Name: "itest-ci-sibling"},
		},
	}
	mustCreate(ctx, t, e.k8s, sibling)
	waitFor(t, "second secret id of the role ready", func() error { return expectReady(ctx, e.k8s, sibling) })
	siblingOutputs, err := authCredOutputs(ctx, e, sibling)
	if err != nil {
		t.Fatal(err)
	}
	if err := authCredLogin(ctx, e.bao, siblingOutputs); err != nil {
		t.Fatalf("login with the second secret id: %v", err)
	}
	if err := e.k8s.Delete(ctx, sibling); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "second secret id destroyed", func() error {
		return authCredSecretIDGone(ctx, e.bao, string(siblingOutputs["secret_id_accessor"]))
	})
	if err := authCredLogin(ctx, e.bao, first); err != nil {
		t.Fatalf("first secret id after the second was destroyed: %v", err)
	}

	stayFor(t, "secret id kept at rest", 3*driftInterval, func() error {
		now, err := authCredOutputs(ctx, e, sid)
		if err != nil {
			return err
		}
		if string(now["secret_id_accessor"]) != string(first["secret_id_accessor"]) {
			return errors.New("secret id was replaced without a reason")
		}
		return nil
	})

	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "itest-ci-approle", Namespace: namespace}}
	if err := e.k8s.Delete(ctx, secret); err != nil {
		t.Fatal(err)
	}
	var second map[string][]byte
	waitFor(t, "lost Secret regenerated", func() error {
		var err error
		if second, err = authCredOutputs(ctx, e, sid); err != nil {
			return err
		}
		if string(second["secret_id_accessor"]) == string(first["secret_id_accessor"]) {
			return errors.New("still the old secret id")
		}
		return nil
	})
	waitFor(t, "replaced secret id destroyed", func() error {
		return authCredSecretIDGone(ctx, e.bao, string(first["secret_id_accessor"]))
	})
	if err := authCredLogin(ctx, e.bao, second); err != nil {
		t.Fatalf("login with the regenerated secret id: %v", err)
	}

	if err := e.k8s.Delete(ctx, sid); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "secret id destroyed on delete", func() error {
		return authCredSecretIDGone(ctx, e.bao, string(second["secret_id_accessor"]))
	})
	waitFor(t, "secret id resource gone", func() error { return expectGone(ctx, e.k8s, sid) })
}

func testAuthCredSecretIDRotation(ctx context.Context, t *testing.T, e env) {
	sid := &authv1alpha1.AppRoleSecretID{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-ci-rotating", Namespace: namespace},
		Spec: authv1alpha1.AppRoleSecretIDSpec{
			ResourceSpec:   authCredSpec(),
			RoleName:       authITItestCi,
			TTL:            &metav1.Duration{Duration: 30 * time.Second},
			WriteSecretRef: commonv1alpha1.LocalObjectReference{Name: "itest-ci-rotating"},
			Rotation:       &authv1alpha1.SecretIDRotation{RenewBefore: "20s"},
		},
	}
	mustCreate(ctx, t, e.k8s, sid)
	var first map[string][]byte
	waitFor(t, "secret id delivered", func() error {
		var err error
		first, err = authCredOutputs(ctx, e, sid)
		return err
	})
	var rotated map[string][]byte
	waitFor(t, "secret id rotated before expiry", func() error {
		var err error
		if rotated, err = authCredOutputs(ctx, e, sid); err != nil {
			return err
		}
		if string(rotated["secret_id_accessor"]) == string(first["secret_id_accessor"]) {
			return errors.New("not rotated yet")
		}
		return nil
	})
	waitFor(t, "old secret id destroyed after rotation", func() error {
		return authCredSecretIDGone(ctx, e.bao, string(first["secret_id_accessor"]))
	})
	if err := authCredLogin(ctx, e.bao, rotated); err != nil {
		t.Fatalf("login with the rotated secret id: %v", err)
	}
	if err := e.k8s.Delete(ctx, sid); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "rotating secret id resource gone", func() error { return expectGone(ctx, e.k8s, sid) })
}

func testAuthCredTokenRole(ctx context.Context, t *testing.T, e env) {
	const path = "auth/token/roles/itest-issuer"
	role := &authv1alpha1.TokenRole{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-issuer", Namespace: namespace},
		Spec: authv1alpha1.TokenRoleSpec{
			ResourceSpec:    builtinAuthDeleteSpec(),
			AllowedPolicies: []string{"b", "a"},
			PathSuffix:      "v-1",
			TokenBaseFields: authv1alpha1.TokenBaseFields{
				TokenPeriod:  &metav1.Duration{Duration: time.Hour},
				TokenNumUses: new(int32(0)),
				TokenType:    authv1alpha1.TokenTypeDefault,
			},
		},
	}
	mustCreate(ctx, t, e.k8s, role)
	waitFor(t, "token role created", func() error { return authCredExpect(ctx, e.bao, path, "token_period", "3600") })
	waitFor(t, "token role ready", func() error { return expectReady(ctx, e.k8s, role) })

	mustWrite(ctx, t, e.bao, path, map[string]any{"allowed_policies": []string{"a"}})
	waitFor(t, "token role drift repaired", func() error {
		return authCredExpect(ctx, e.bao, path, "allowed_policies", "[a b]")
	})
	mustWrite(ctx, t, e.bao, path, map[string]any{"allowed_policies": []string{"b", "a"}})
	expectNoWrites(t, "TokenRole")

	if err := e.k8s.Delete(ctx, role); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "token role deleted", func() error { return expectAbsent(ctx, e.bao, path) })
	waitFor(t, "token role resource gone", func() error { return expectGone(ctx, e.k8s, role) })
}

func testAuthCredCert(ctx context.Context, t *testing.T, e env) {
	const configPath, rolePath = "auth/cert/config", "auth/cert/certs/itest-web"
	config := &authv1alpha1.CertAuthConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-cert", Namespace: namespace},
		Spec: authv1alpha1.CertAuthConfigSpec{
			ResourceSpec:                authCredAdoptSpec(),
			EnableIdentityAliasMetadata: new(true),
			OCSPCacheSize:               new(int32(50)),
		},
	}
	mustCreate(ctx, t, e.k8s, config)
	waitFor(t, "cert config written", func() error {
		return authCredExpect(ctx, e.bao, configPath, "ocsp_cache_size", "50")
	})
	waitFor(t, "cert config ready", func() error { return expectReady(ctx, e.k8s, config) })
	mustWrite(ctx, t, e.bao, configPath, map[string]any{"ocsp_cache_size": 10})
	waitFor(t, "cert config drift repaired", func() error {
		return authCredExpect(ctx, e.bao, configPath, "ocsp_cache_size", "50")
	})

	role := &authv1alpha1.CertAuthRole{
		ObjectMeta: metav1.ObjectMeta{Name: authITItestWeb, Namespace: namespace},
		Spec: authv1alpha1.CertAuthRoleSpec{
			ResourceSpec:       builtinAuthDeleteSpec(),
			Certificate:        authCredTestCA,
			AllowedCommonNames: []string{"b.example.com", "a.example.com"},
			TokenFields: authv1alpha1.TokenFields{
				TokenTTL:      &metav1.Duration{Duration: time.Hour},
				TokenPolicies: []string{"web"},
			},
		},
	}
	mustCreate(ctx, t, e.k8s, role)
	waitFor(t, "cert role created", func() error { return authCredExpect(ctx, e.bao, rolePath, authITTokenTTL, "3600") })
	waitFor(t, "cert role ready", func() error { return expectReady(ctx, e.k8s, role) })
	mustWrite(ctx, t, e.bao, rolePath, map[string]any{authITTokenTTL: 60})
	waitFor(t, "cert role drift repaired", func() error {
		return authCredExpect(ctx, e.bao, rolePath, authITTokenTTL, "3600")
	})
	mustWrite(ctx, t, e.bao, rolePath, map[string]any{"allowed_common_names": []string{"a.example.com", "b.example.com"}})
	expectNoWrites(t, "CertAuthRole")
	expectNoWrites(t, "CertAuthConfig")

	if err := e.k8s.Delete(ctx, role); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "cert role deleted", func() error { return expectAbsent(ctx, e.bao, rolePath) })
	if err := e.k8s.Delete(ctx, config); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "cert config resource gone", func() error { return expectGone(ctx, e.k8s, config) })
	if err := authCredExpect(ctx, e.bao, configPath, "ocsp_cache_size", "50"); err != nil {
		t.Errorf("orphaned cert config changed: %v", err)
	}
}

func testAuthCredUserpass(ctx context.Context, t *testing.T, e env) {
	const path = "auth/userpass/users/itest-alice"
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-alice-login", Namespace: namespace},
		Data:       map[string][]byte{authITPassword: []byte("first-fake-password")},
	}
	mustCreate(ctx, t, e.k8s, secret)
	user := &authv1alpha1.UserpassUser{
		ObjectMeta: metav1.ObjectMeta{Name: authITItestAlice, Namespace: namespace},
		Spec: authv1alpha1.UserpassUserSpec{
			ResourceSpec:      builtinAuthDeleteSpec(),
			PasswordSecretRef: &commonv1alpha1.SecretKeySelector{Name: secret.Name, Key: authITPassword},
			TokenFields:       authv1alpha1.TokenFields{TokenPolicies: []string{authITReader}},
		},
	}
	mustCreate(ctx, t, e.k8s, user)
	waitFor(t, "userpass user created", func() error {
		return authCredExpect(ctx, e.bao, path, authITTokenPolicies, "[reader]")
	})
	waitFor(t, "userpass user ready", func() error { return expectReady(ctx, e.k8s, user) })
	login := func(password string) func() error {
		return func() error {
			_, err := e.bao.Write(ctx, "auth/userpass/login/itest-alice", map[string]any{authITPassword: password})
			return err
		}
	}
	waitFor(t, "login with the first password", login("first-fake-password"))
	expectNoWrites(t, "UserpassUser")

	if err := e.k8s.Get(ctx, client.ObjectKeyFromObject(user), user); err != nil {
		t.Fatal(err)
	}
	hash := user.Status.WriteOnlyHash
	secret.Data[authITPassword] = []byte("second-fake-password")
	if err := e.k8s.Update(ctx, secret); err != nil {
		t.Fatal(err)
	}
	// Polling the login before the rewrite would trip OpenBao's lockout after 5 failures.
	waitFor(t, "rotated password written", func() error {
		if err := e.k8s.Get(ctx, client.ObjectKeyFromObject(user), user); err != nil {
			return err
		}
		if user.Status.WriteOnlyHash == hash {
			return errors.New("writeOnlyHash unchanged")
		}
		return nil
	})
	if err := login("second-fake-password")(); err != nil {
		t.Fatalf("login with the rotated password: %v", err)
	}

	if err := e.k8s.Delete(ctx, user); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "userpass user deleted", func() error { return expectAbsent(ctx, e.bao, path) })
	waitFor(t, "userpass user resource gone", func() error { return expectGone(ctx, e.k8s, user) })
}

func authCredExpect(ctx context.Context, bao openbao.Client, path, key, want string) error {
	data, err := bao.Read(ctx, path)
	if err != nil {
		return err
	}
	if got := fmt.Sprint(data[key]); got != want {
		return fmt.Errorf("%s %s = %s, want %s", path, key, got, want)
	}
	return nil
}

// authCredOutputs returns the output Secret of sid once it holds the accessor recorded in status.id.
func authCredOutputs(ctx context.Context, e env, sid *authv1alpha1.AppRoleSecretID) (map[string][]byte, error) {
	var secret corev1.Secret
	key := client.ObjectKey{Namespace: namespace, Name: sid.Spec.WriteSecretRef.Name}
	if err := e.k8s.Get(ctx, key, &secret); err != nil {
		return nil, err
	}
	if err := e.k8s.Get(ctx, client.ObjectKeyFromObject(sid), sid); err != nil {
		return nil, err
	}
	if len(secret.Data["secret_id"]) == 0 || string(secret.Data["secret_id_accessor"]) != sid.Status.ID {
		return nil, errors.New("secret does not hold the current secret id yet")
	}
	return secret.Data, nil
}

func authCredLogin(ctx context.Context, bao openbao.Client, outputs map[string][]byte) error {
	_, err := bao.Write(ctx, "auth/approle/login", map[string]any{
		"role_id": string(outputs["role_id"]), "secret_id": string(outputs["secret_id"]),
	})
	return err
}

const authCredCIRolePath = "auth/approle/role/itest-ci"

func authCredSecretIDGone(ctx context.Context, bao openbao.Client, accessor string) error {
	_, err := bao.Write(ctx, authCredCIRolePath+"/secret-id-accessor/lookup", map[string]any{"secret_id_accessor": accessor})
	if errors.Is(err, openbao.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("secret id %s still exists", accessor)
}

func authCredSetup(ctx context.Context, t *testing.T) env {
	t.Helper()
	root := repoRoot(t)
	baoBin, assets := secretsPrerequisites(t, root)

	addr := startBao(t, baoBin)
	bao, err := openbao.NewPool().Client(ctx, "itest-auth-credentials", openbao.Config{
		Address: addr,
		Timeout: 10 * time.Second,
		Auth: openbao.TokenAuth{
			CredentialKey: "itest-auth-credentials",
			Token:         func(context.Context) (string, error) { return rootToken, nil },
		},
	})
	if err != nil {
		t.Fatalf("openbao client: %v", err)
	}

	names := []string{"approles", "approlesecretids", "tokenroles", "certauthconfigs", "certauthroles", "userpassusers"}
	crds := make([]string, 0, len(names))
	for _, name := range names {
		crds = append(crds, filepath.Join(root, "config", "crd", "bases", "auth.openbaoconfig.io_"+name+".yaml"))
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
	for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, authv1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		Controller:             ctrlconfig.Controller{SkipNameValidation: new(true)},
	})
	if err != nil {
		t.Fatal(err)
	}
	conns := staticConnections{client: bao}
	opts := engine.Options{DriftIntervalCritical: driftInterval, DriftIntervalStandard: driftInterval}
	for _, setupKind := range []func(ctrl.Manager, engine.Connections, engine.Options) error{
		auth.SetupAppRole, auth.SetupAppRoleSecretID, auth.SetupTokenRole, auth.SetupCertAuthConfig,
		auth.SetupCertAuthRole, auth.SetupUserpassUser,
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
