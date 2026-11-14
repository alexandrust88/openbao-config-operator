//go:build integration

package integration

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/controller/auth"
	"github.com/alexandrust88/openbao-config-operator/internal/controller/sys"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// TestIntegrationBuiltinAuth runs KubernetesAuthConfig, KubernetesAuthRole, JWTAuthConfig, JWTAuthRole and
// JWTCELRole against envtest and bao server -dev.
func TestIntegrationBuiltinAuth(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e := builtinAuthSetup(ctx, t)
	mustWrite(ctx, t, e.bao, "sys/auth/kubernetes", map[string]any{authITType: taclAuthMount})
	mustWrite(ctx, t, e.bao, "sys/auth/jwt", map[string]any{authITType: authITJWT})
	caPEM, pubPEM := builtinAuthPEMs(t)

	t.Run("kubernetesauthconfig", func(t *testing.T) { testKubernetesAuthConfig(ctx, t, e, caPEM, pubPEM) })
	t.Run("kubernetesauthrole", func(t *testing.T) { testKubernetesAuthRole(ctx, t, e) })
	t.Run("jwtauthconfig", func(t *testing.T) { testJWTAuthConfig(ctx, t, e, pubPEM) })
	t.Run("jwtauthrole", func(t *testing.T) { testJWTAuthRole(ctx, t, e) })
	t.Run("jwtcelrole", func(t *testing.T) { testJWTCELRole(ctx, t, e) })
	t.Run("policyrefs", func(t *testing.T) { testBuiltinAuthPolicyRefs(ctx, t, e) })
	t.Run("defaults", func(t *testing.T) { testBuiltinAuthDefaults(ctx, t, e, caPEM, pubPEM) })
}

// testBuiltinAuthPolicyRefs proves that a role waits for a referenced Policy and then merges its name.
func testBuiltinAuthPolicyRefs(ctx context.Context, t *testing.T, e env) {
	const path = "auth/kubernetes/role/itest-refs"
	role := &authv1alpha1.KubernetesAuthRole{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-refs", Namespace: namespace},
		Spec: authv1alpha1.KubernetesAuthRoleSpec{
			ResourceSpec:                  builtinAuthSpec(),
			BoundServiceAccountNames:      []string{taclAppSA},
			BoundServiceAccountNamespaces: []string{taclTeamConn},
			TokenFields: authv1alpha1.TokenFields{
				TokenPolicies: []string{"literal"},
				PolicyRefs:    []commonv1alpha1.LocalObjectReference{{Name: "itest-refs-policy"}},
			},
		},
	}
	mustCreate(ctx, t, e.k8s, role)
	waitFor(t, "role waits for the Policy", func() error {
		if err := e.k8s.Get(ctx, client.ObjectKeyFromObject(role), role); err != nil {
			return err
		}
		synced := meta.FindStatusCondition(role.Status.Conditions, commonv1alpha1.ConditionSynced)
		if synced == nil || synced.Reason != commonv1alpha1.ReasonReferenceNotReady {
			return fmt.Errorf("synced = %+v, want reason ReferenceNotReady", synced)
		}
		return nil
	})
	if err := expectAbsent(ctx, e.bao, path); err != nil {
		t.Errorf("a role with an unready Policy must not be written: %v", err)
	}

	policy := &sysv1alpha1.Policy{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-refs-policy", Namespace: namespace},
		Spec: sysv1alpha1.PolicySpec{
			ResourceSpec: builtinAuthSpec(),
			Name:         "itest-refs-reader",
			Policy:       "path \"secret/*\" {\n  capabilities = [\"read\"]\n}\n",
		},
	}
	mustCreate(ctx, t, e.k8s, policy)
	want := map[string]any{authITTokenPolicies: []any{"literal", "itest-refs-reader"}}
	waitFor(t, "role written with the Policy name", func() error { return builtinAuthExpect(ctx, e.bao, path, want) })
	waitFor(t, "role ready", func() error { return expectReady(ctx, e.k8s, role) })
	expectNoWrites(t, "KubernetesAuthRole")
}

// testBuiltinAuthDefaults creates each kind with only its required fields and proves that nothing is written at
// rest: the server defaults the adapters send match what OpenBao stores.
func testBuiltinAuthDefaults(ctx context.Context, t *testing.T, e env, caPEM, pubPEM string) {
	mustWrite(ctx, t, e.bao, "sys/auth/kubernetes-defaults", map[string]any{authITType: taclAuthMount})
	mustWrite(ctx, t, e.bao, "sys/auth/jwt-defaults", map[string]any{authITType: authITJWT})
	objMeta := metav1.ObjectMeta{Name: "itest-defaults", Namespace: namespace}
	objs := []engine.Object{
		&authv1alpha1.KubernetesAuthConfig{ObjectMeta: objMeta, Spec: authv1alpha1.KubernetesAuthConfigSpec{
			ResourceSpec: builtinAuthSpec(), MountPath: "kubernetes-defaults",
			KubernetesHost: "https://k8s", KubernetesCACert: caPEM,
		}},
		&authv1alpha1.KubernetesAuthRole{ObjectMeta: objMeta, Spec: authv1alpha1.KubernetesAuthRoleSpec{
			ResourceSpec: builtinAuthSpec(), MountPath: "kubernetes-defaults",
			BoundServiceAccountNames: []string{"a"}, BoundServiceAccountNamespaces: []string{"b"},
		}},
		&authv1alpha1.JWTAuthConfig{ObjectMeta: objMeta, Spec: authv1alpha1.JWTAuthConfigSpec{
			ResourceSpec: builtinAuthSpec(), MountPath: authITJWTDefaults, JWTValidationPubkeys: []string{pubPEM},
		}},
		&authv1alpha1.JWTAuthRole{ObjectMeta: objMeta, Spec: authv1alpha1.JWTAuthRoleSpec{
			ResourceSpec: builtinAuthSpec(), MountPath: authITJWTDefaults, UserClaim: authITSub, BoundAudiences: []string{"a"},
		}},
		&authv1alpha1.JWTCELRole{ObjectMeta: objMeta, Spec: authv1alpha1.JWTCELRoleSpec{
			ResourceSpec: builtinAuthSpec(), MountPath: authITJWTDefaults,
			CELProgram: authv1alpha1.CELProgram{Expression: authITTrue},
		}},
	}
	authDefaultsNoWrites(ctx, t, e, objs)
}

// authDefaultsNoWrites creates objs, waits until each is Ready and asserts that none of their kinds writes at rest.
func authDefaultsNoWrites(ctx context.Context, t *testing.T, e env, objs []engine.Object) {
	t.Helper()
	for _, obj := range objs {
		mustCreate(ctx, t, e.k8s, obj)
	}
	for _, obj := range objs {
		waitFor(t, reflect.TypeOf(obj).Elem().Name()+" ready", func() error { return expectReady(ctx, e.k8s, obj) })
	}
	time.Sleep(driftInterval)
	before := map[string]float64{}
	for _, obj := range objs {
		kind := reflect.TypeOf(obj).Elem().Name()
		before[kind] = sysCoreWrites(t, kind)
	}
	time.Sleep(3 * driftInterval)
	for kind, n := range before {
		if after := sysCoreWrites(t, kind); after != n {
			t.Errorf("%s with only required fields: %v writes at rest, want none", kind, after-n)
		}
	}
}

// Literals that several auth integration tests repeat.
const (
	authITApprole                              = "approle"
	authITBoundAudiences                       = "bound_audiences"
	authITBoundServiceAccountNames             = "bound_service_account_names"
	authITBoundServiceAccountNamespaceSelector = "bound_service_account_namespace_selector"
	authITCertDefaults                         = "cert-defaults"
	authITES256                                = "ES256"
	authITExpirationLeeway                     = "expiration_leeway"
	authITExpression                           = "expression"
	authITIntruder                             = "intruder"
	authITItestAlice                           = "itest-alice"
	authITItestApp                             = "itest-app"
	authITItestCi                              = "itest-ci"
	authITItestWeb                             = "itest-web"
	authITJWT                                  = "jwt"
	authITJWTDefaults                          = "jwt-defaults"
	authITKubernetesHost                       = "kubernetes_host"
	authITCIDR                                 = "10.0.0.0/8"
	authITName                                 = "name"
	authITOpenbao                              = "openbao"
	authITPassword                             = "password"
	authITProject                              = "project"
	authITRead                                 = "read"
	authITReader                               = "reader"
	authITSub                                  = "sub"
	authITToken                                = "token"
	authITTokenPolicies                        = "token_policies"
	authITTokenTTL                             = "token_ttl"
	authITTrue                                 = "true"
	authITType                                 = "type"
	authITWorker                               = "worker"
)

func builtinAuthSpec() commonv1alpha1.ResourceSpec {
	return commonv1alpha1.ResourceSpec{ConnectionRef: commonv1alpha1.ConnectionReference{Name: audDev}}
}

// builtinAuthDeleteSpec opts in to deletion in OpenBao; every kind defaults to Orphan.
func builtinAuthDeleteSpec() commonv1alpha1.ResourceSpec {
	spec := builtinAuthSpec()
	spec.DeletionPolicy = commonv1alpha1.DeletionPolicyDelete
	return spec
}

func testKubernetesAuthConfig(ctx context.Context, t *testing.T, e env, caPEM, pubPEM string) {
	const path = "auth/kubernetes/config"
	reviewer := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-reviewer", Namespace: namespace},
		Data:       map[string][]byte{authITToken: []byte("first.reviewer.jwt")},
	}
	mustCreate(ctx, t, e.k8s, reviewer)
	cfg := &authv1alpha1.KubernetesAuthConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-k8s", Namespace: namespace},
		Spec: authv1alpha1.KubernetesAuthConfigSpec{
			ResourceSpec:              builtinAuthSpec(),
			KubernetesHost:            "https://kubernetes.default.svc",
			KubernetesCACert:          caPEM,
			TokenReviewerJWTSecretRef: &commonv1alpha1.SecretKeySelector{Name: reviewer.Name, Key: authITToken},
			PEMKeys:                   []string{pubPEM},
			DisableLocalCAJWT:         new(true),
		},
	}
	mustCreate(ctx, t, e.k8s, cfg)
	want := map[string]any{
		authITKubernetesHost: "https://kubernetes.default.svc", "kubernetes_ca_cert": caPEM,
		"disable_local_ca_jwt": true, "token_reviewer_jwt_set": true,
	}
	waitFor(t, "config written", func() error { return builtinAuthExpect(ctx, e.bao, path, want) })
	waitFor(t, "config ready", func() error { return expectReady(ctx, e.k8s, cfg) })

	mustWrite(ctx, t, e.bao, path, map[string]any{authITKubernetesHost: "https://intruder", "kubernetes_ca_cert": caPEM})
	waitFor(t, "out-of-band replace repaired, reviewer JWT restored", func() error {
		return builtinAuthExpect(ctx, e.bao, path, want)
	})
	expectNoWrites(t, "KubernetesAuthConfig")

	hash := cfg.Status.WriteOnlyHash
	rotated := reviewer.DeepCopy()
	rotated.Data[authITToken] = []byte("second.reviewer.jwt")
	if err := e.k8s.Update(ctx, rotated); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "rotated reviewer JWT rewritten", func() error {
		if err := e.k8s.Get(ctx, client.ObjectKeyFromObject(cfg), cfg); err != nil {
			return err
		}
		if cfg.Status.WriteOnlyHash == hash {
			return fmt.Errorf("writeOnlyHash unchanged")
		}
		return nil
	})

	if err := e.k8s.Delete(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "config resource gone", func() error { return expectGone(ctx, e.k8s, cfg) })
	if err := builtinAuthExpect(ctx, e.bao, path, want); err != nil {
		t.Errorf("Orphan config must stay in OpenBao: %v", err)
	}
}

func testKubernetesAuthRole(ctx context.Context, t *testing.T, e env) {
	const path = "auth/kubernetes/role/itest-app"
	role := &authv1alpha1.KubernetesAuthRole{
		ObjectMeta: metav1.ObjectMeta{Name: authITItestApp, Namespace: namespace},
		Spec: authv1alpha1.KubernetesAuthRoleSpec{
			ResourceSpec:             builtinAuthDeleteSpec(),
			BoundServiceAccountNames: []string{taclAppSA, authITWorker},
			BoundServiceAccountNamespaceSelector: &authv1alpha1.NamespaceLabelSelector{
				MatchLabels: map[string]string{"tenant": "a", "env": audDev},
			},
			AliasNameSource: authv1alpha1.AliasNameSourceServiceAccountName,
			TokenFields: authv1alpha1.TokenFields{
				TokenBaseFields: authv1alpha1.TokenBaseFields{TokenBoundCIDRs: []string{authITCIDR, "10.1.2.3/32"}},
				TokenTTL:        &metav1.Duration{Duration: time.Hour},
				TokenPolicies:   []string{authITRead, "write"},
			},
		},
	}
	mustCreate(ctx, t, e.k8s, role)
	want := map[string]any{
		authITBoundServiceAccountNames: []any{taclAppSA, authITWorker}, "alias_name_source": "serviceaccount_name",
		authITTokenTTL: 3600, authITTokenPolicies: []any{authITRead, "write"},
		"token_bound_cidrs":                        []any{authITCIDR, "10.1.2.3"},
		authITBoundServiceAccountNamespaceSelector: `{"matchLabels":{"env":"dev","tenant":"a"}}`,
	}
	waitFor(t, "role written", func() error { return builtinAuthExpect(ctx, e.bao, path, want) })
	waitFor(t, "role ready", func() error { return expectReady(ctx, e.k8s, role) })

	selector := want[authITBoundServiceAccountNamespaceSelector]
	mustWrite(ctx, t, e.bao, path, map[string]any{
		authITTokenTTL: "5m", authITBoundServiceAccountNamespaceSelector: selector,
	})
	waitFor(t, "out-of-band ttl repaired", func() error { return builtinAuthExpect(ctx, e.bao, path, want) })

	equivalent := map[string]any{
		authITBoundServiceAccountNames:             []any{authITWorker, taclAppSA},
		authITBoundServiceAccountNamespaceSelector: "matchLabels:\n  tenant: a\n  env: dev\n",
		authITTokenPolicies:                        []any{"write", authITRead},
		authITTokenTTL:                             "60m",
	}
	mustWrite(ctx, t, e.bao, path, equivalent)
	stayFor(t, "equivalent role kept", 3*driftInterval, func() error {
		return builtinAuthExpect(ctx, e.bao, path, map[string]any{
			authITBoundServiceAccountNamespaceSelector: equivalent[authITBoundServiceAccountNamespaceSelector],
			authITBoundServiceAccountNames:             equivalent[authITBoundServiceAccountNames],
		})
	})
	expectNoWrites(t, "KubernetesAuthRole")

	if err := e.k8s.Delete(ctx, role); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "role deleted", func() error { return expectAbsent(ctx, e.bao, path) })
	waitFor(t, "role resource gone", func() error { return expectGone(ctx, e.k8s, role) })
}

func testJWTAuthConfig(ctx context.Context, t *testing.T, e env, pubPEM string) {
	const path = "auth/jwt/config"
	cfg := &authv1alpha1.JWTAuthConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-jwt", Namespace: namespace},
		Spec: authv1alpha1.JWTAuthConfigSpec{
			ResourceSpec:         builtinAuthSpec(),
			JWTValidationPubkeys: []string{pubPEM},
			JWTSupportedAlgs:     []string{authITES256},
			BoundIssuer:          "https://issuer.example.com",
			DefaultRole:          authITItestCi,
		},
	}
	mustCreate(ctx, t, e.k8s, cfg)
	want := map[string]any{
		"bound_issuer": "https://issuer.example.com", "default_role": authITItestCi, "jwt_supported_algs": []any{authITES256},
	}
	waitFor(t, "config written", func() error { return builtinAuthExpect(ctx, e.bao, path, want) })
	waitFor(t, "config ready", func() error { return expectReady(ctx, e.k8s, cfg) })

	mustWrite(ctx, t, e.bao, path, map[string]any{"jwt_validation_pubkeys": []any{pubPEM}, "default_role": authITIntruder})
	waitFor(t, "out-of-band replace repaired", func() error { return builtinAuthExpect(ctx, e.bao, path, want) })
	expectNoWrites(t, "JWTAuthConfig")

	if err := e.k8s.Delete(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "config resource gone", func() error { return expectGone(ctx, e.k8s, cfg) })
	if err := builtinAuthExpect(ctx, e.bao, path, want); err != nil {
		t.Errorf("Orphan config must stay in OpenBao: %v", err)
	}
}

func testJWTAuthRole(ctx context.Context, t *testing.T, e env) {
	const path = "auth/jwt/role/itest-ci"
	disabled := intstr.FromInt32(-1)
	twoMinutes := intstr.FromString("2m")
	role := &authv1alpha1.JWTAuthRole{
		ObjectMeta: metav1.ObjectMeta{Name: authITItestCi, Namespace: namespace},
		Spec: authv1alpha1.JWTAuthRoleSpec{
			ResourceSpec:   builtinAuthDeleteSpec(),
			UserClaim:      authITSub,
			BoundAudiences: []string{authITOpenbao, "ci"},
			BoundClaims:    map[string][]string{authITProject: {"a", "b"}},
			ClaimMappings:  map[string]string{authITProject: authITProject},
			MaxAge:         &metav1.Duration{Duration: time.Hour},
			JWTLeeways:     authv1alpha1.JWTLeeways{ExpirationLeeway: &disabled, NotBeforeLeeway: &twoMinutes},
			TokenFields:    authv1alpha1.TokenFields{TokenPolicies: []string{"ci"}},
		},
	}
	mustCreate(ctx, t, e.k8s, role)
	want := map[string]any{
		"role_type": authITJWT, "user_claim": authITSub, authITBoundAudiences: []any{authITOpenbao, "ci"},
		"bound_claims": map[string]any{authITProject: []any{"a", "b"}}, authITExpirationLeeway: -1, "not_before_leeway": 120,
		"max_age": 3600, authITTokenPolicies: []any{"ci"},
	}
	waitFor(t, "role written", func() error { return builtinAuthExpect(ctx, e.bao, path, want) })
	waitFor(t, "role ready", func() error { return expectReady(ctx, e.k8s, role) })

	mustWrite(ctx, t, e.bao, path, map[string]any{
		"role_type": authITJWT, authITExpirationLeeway: 150, "bound_subject": "x",
	})
	waitFor(t, "out-of-band leeway repaired", func() error { return builtinAuthExpect(ctx, e.bao, path, want) })
	mustWrite(ctx, t, e.bao, path, map[string]any{
		"role_type": authITJWT, authITBoundAudiences: []any{"ci", authITOpenbao},
	})
	expectNoWrites(t, "JWTAuthRole")

	if err := e.k8s.Delete(ctx, role); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "role deleted", func() error { return expectAbsent(ctx, e.bao, path) })
	waitFor(t, "role resource gone", func() error { return expectGone(ctx, e.k8s, role) })
}

func testJWTCELRole(ctx context.Context, t *testing.T, e env) {
	const path = "auth/jwt/cel/role/itest-cel"
	role := &authv1alpha1.JWTCELRole{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-cel", Namespace: namespace},
		Spec: authv1alpha1.JWTCELRoleSpec{
			ResourceSpec: builtinAuthDeleteSpec(),
			CELProgram: authv1alpha1.CELProgram{
				Variables:  []authv1alpha1.CELVariable{{Name: authITProject, Expression: "claims.project"}},
				Expression: `project == "a"`,
			},
			Message:        "project not allowed",
			BoundAudiences: []string{authITOpenbao},
		},
	}
	mustCreate(ctx, t, e.k8s, role)
	want := map[string]any{
		"cel_program": map[string]any{
			authITExpression: `project == "a"`,
			"variables":      []any{map[string]any{authITName: authITProject, authITExpression: "claims.project"}},
		},
		"message": "project not allowed", authITBoundAudiences: []any{authITOpenbao},
		authITExpirationLeeway: 150, "not_before_leeway": 150, "clock_skew_leeway": 60,
	}
	waitFor(t, "cel role written", func() error { return builtinAuthExpect(ctx, e.bao, path, want) })
	waitFor(t, "cel role ready", func() error { return expectReady(ctx, e.k8s, role) })

	mustWrite(ctx, t, e.bao, path, map[string]any{"cel_program": map[string]any{authITExpression: authITTrue}})
	waitFor(t, "out-of-band replace repaired", func() error { return builtinAuthExpect(ctx, e.bao, path, want) })
	expectNoWrites(t, "JWTCELRole")

	if err := e.k8s.Delete(ctx, role); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "cel role deleted", func() error { return expectAbsent(ctx, e.bao, path) })
	waitFor(t, "cel role resource gone", func() error { return expectGone(ctx, e.k8s, role) })
}

// builtinAuthExpect compares the keys of want against the read, lists as sets and numbers numerically.
func builtinAuthExpect(ctx context.Context, bao openbao.Client, path string, want map[string]any) error {
	data, err := bao.Read(ctx, path)
	if err != nil {
		return err
	}
	var sets []string
	for key, value := range want {
		if _, ok := value.([]any); ok {
			sets = append(sets, key)
		}
	}
	if differing := engine.Diff(want, data, engine.FieldSpec{Sets: sets}); len(differing) > 0 {
		return fmt.Errorf("%s differs on %v", path, differing)
	}
	return nil
}

// builtinAuthPEMs returns a self-signed CA certificate and a public key, both PEM-encoded.
func builtinAuthPEMs(t *testing.T) (caPEM, pubPEM string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "itest-ca"},
		NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pub}))
}

func builtinAuthSetup(ctx context.Context, t *testing.T) env {
	t.Helper()
	root := repoRoot(t)
	baoBin, assets := secretsPrerequisites(t, root)

	addr := startBao(t, baoBin)
	bao, err := openbao.NewPool().Client(ctx, "itest-auth", openbao.Config{
		Address: addr,
		Timeout: 10 * time.Second,
		Auth: openbao.TokenAuth{
			CredentialKey: "itest-auth",
			Token:         func(context.Context) (string, error) { return rootToken, nil },
		},
	})
	if err != nil {
		t.Fatalf("openbao client: %v", err)
	}

	names := []string{"kubernetesauthconfigs", "kubernetesauthroles", "jwtauthconfigs", "jwtauthroles", "jwtcelroles"}
	crds := make([]string, 0, len(names)+1)
	for _, name := range names {
		crds = append(crds, filepath.Join(root, "config", "crd", "bases", "auth.openbaoconfig.io_"+name+".yaml"))
	}
	crds = append(crds, filepath.Join(root, "config", "crd", "bases", "sys.openbaoconfig.io_policies.yaml"),
		filepath.Join(root, "config", "crd", "bases", "sys.openbaoconfig.io_authmounts.yaml"))
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
	adds := []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, authv1alpha1.AddToScheme, sysv1alpha1.AddToScheme}
	for _, add := range adds {
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
	opts := engine.Options{DriftIntervalCritical: driftInterval, DriftIntervalStandard: driftInterval}
	for _, setupKind := range []func(ctrl.Manager, engine.Connections, engine.Options) error{
		auth.SetupKubernetesAuthConfig, auth.SetupKubernetesAuthRole, auth.SetupJWTAuthConfig,
		auth.SetupJWTAuthRole, auth.SetupJWTCELRole, sys.SetupPolicy,
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
