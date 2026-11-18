//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/controller/secrets"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
	"github.com/alexandrust88/openbao-config-operator/test/testenv"
)

const (
	secCredDB     = "itest-db"
	secCredRabbit = "itest-rabbit"
	secCredK8s    = "itest-k8s"
)

// TestIntegrationSecretsCredentials runs the database, RabbitMQ and Kubernetes secrets engine controllers against
// bao server -dev. Database connections use verify_connection=false; the postgres subtest additionally runs
// against a real PostgreSQL container when docker and the image are available locally.
func TestIntegrationSecretsCredentials(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e := secCredSetup(ctx, t)

	for mount, engineType := range map[string]string{secCredDB: "database", secCredRabbit: "rabbitmq", secCredK8s: "kubernetes"} {
		mustWrite(ctx, t, e.bao, "sys/mounts/"+mount, map[string]any{"type": engineType})
	}

	t.Run("databaseconnection", func(t *testing.T) { testDatabaseConnection(ctx, t, e) })
	t.Run("databaserole", func(t *testing.T) { testDatabaseRole(ctx, t, e) })
	t.Run("postgres", func(t *testing.T) { testDatabasePostgres(ctx, t, e) })
	t.Run("rabbitmqconfig", func(t *testing.T) { testRabbitMQConfig(ctx, t, e) })
	t.Run("rabbitmqrole", func(t *testing.T) { testRabbitMQRole(ctx, t, e) })
	t.Run("kubernetessecretsconfig", func(t *testing.T) { testKubernetesSecretsConfig(ctx, t, e) })
	t.Run("kubernetessecretsrole", func(t *testing.T) { testKubernetesSecretsRole(ctx, t, e) })
}

func testDatabaseConnection(ctx context.Context, t *testing.T, e env) {
	const path = secCredDB + "/config/itest-pg"
	secret := secCredSecret(ctx, t, e, "itest-db-root", "password", "first-root-password")
	conn := &secretsv1alpha1.DatabaseConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-pg", Namespace: namespace},
		Spec: secretsv1alpha1.DatabaseConnectionSpec{
			ResourceSpec:      secCredDeleteSpec(),
			MountPath:         secCredDB,
			PluginName:        "postgresql-database-plugin",
			VerifyConnection:  new(false),
			AllowedRoles:      []string{"itest-ro", "itest-rw"},
			PasswordSecretRef: &commonv1alpha1.SecretKeySelector{Name: secret.Name, Key: "password"},
			PostgreSQL: &secretsv1alpha1.DatabasePostgreSQLConfig{DatabaseSQLConfig: secretsv1alpha1.DatabaseSQLConfig{
				ConnectionURL:         "postgresql://{{username}}:{{password}}@db.invalid:5432/app",
				Username:              "vault-root",
				MaxOpenConnections:    new(int32(4)),
				MaxConnectionLifetime: &metav1.Duration{Duration: time.Minute},
			}},
		},
	}
	mustCreate(ctx, t, e.k8s, conn)
	waitFor(t, "connection written", func() error {
		return secCredExpect(ctx, e.bao, path, func(d map[string]any) error {
			details, _ := d["connection_details"].(map[string]any)
			if _, ok := details["password"]; ok {
				return errors.New("password returned by the read")
			}
			return secCredEqual(d["allowed_roles"], []any{"itest-ro", "itest-rw"})
		})
	})
	waitFor(t, "connection ready", func() error { return expectReady(ctx, e.k8s, conn) })
	hash := conn.Status.WriteOnlyHash

	mustWrite(ctx, t, e.bao, path, map[string]any{"allowed_roles": []string{"intruder"}, "verify_connection": false})
	waitFor(t, "allowed_roles repaired", func() error {
		return secCredExpect(ctx, e.bao, path, func(d map[string]any) error {
			return secCredEqual(d["allowed_roles"], []any{"itest-ro", "itest-rw"})
		})
	})
	mustWrite(ctx, t, e.bao, path, map[string]any{"allowed_roles": []string{"itest-rw", "itest-ro"}, "verify_connection": false})
	expectNoWrites(t, "DatabaseConnection")

	secCredRotate(ctx, t, e, secret, "password", "second-root-password")
	waitFor(t, "password change written", func() error {
		if err := e.k8s.Get(ctx, client.ObjectKeyFromObject(conn), conn); err != nil {
			return err
		}
		if conn.Status.WriteOnlyHash == hash {
			return errors.New("write-only hash unchanged")
		}
		return nil
	})

	if err := e.k8s.Delete(ctx, conn); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "connection deleted", func() error { return expectAbsent(ctx, e.bao, path) })
	waitFor(t, "connection resource gone", func() error { return expectGone(ctx, e.k8s, conn) })
}

func testDatabaseRole(ctx context.Context, t *testing.T, e env) {
	const path = secCredDB + "/roles/itest-ro"
	role := &secretsv1alpha1.DatabaseRole{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-ro", Namespace: namespace},
		Spec: secretsv1alpha1.DatabaseRoleSpec{
			ResourceSpec:         secCredDeleteSpec(),
			MountPath:            secCredDB,
			DBName:               "itest-pg",
			DefaultTTL:           &metav1.Duration{Duration: time.Hour},
			MaxTTL:               &metav1.Duration{Duration: 24 * time.Hour},
			CreationStatements:   []string{`CREATE ROLE "{{name}}" WITH LOGIN PASSWORD '{{password}}' VALID UNTIL '{{expiration}}';`},
			RevocationStatements: []string{`DROP ROLE IF EXISTS "{{name}}";`},
			DatabaseCredentialFields: secretsv1alpha1.DatabaseCredentialFields{
				CredentialType:   secretsv1alpha1.DatabaseCredentialPassword,
				CredentialConfig: map[string]string{"password_policy": "default"},
			},
		},
	}
	mustCreate(ctx, t, e.k8s, role)
	waitFor(t, "role written", func() error {
		return secCredExpect(ctx, e.bao, path, func(d map[string]any) error { return secCredEqual(d["default_ttl"], 3600) })
	})
	waitFor(t, "role ready", func() error { return expectReady(ctx, e.k8s, role) })

	mustWrite(ctx, t, e.bao, path, map[string]any{"db_name": "itest-pg", "default_ttl": 60})
	waitFor(t, "default_ttl repaired", func() error {
		return secCredExpect(ctx, e.bao, path, func(d map[string]any) error { return secCredEqual(d["default_ttl"], 3600) })
	})
	expectNoWrites(t, "DatabaseRole")

	if err := e.k8s.Delete(ctx, role); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "role deleted", func() error { return expectAbsent(ctx, e.bao, path) })
}

// testDatabasePostgres proves rotateRootOnCreate and static roles against a real PostgreSQL.
func testDatabasePostgres(ctx context.Context, t *testing.T, e env) {
	addr := secCredStartPostgres(t, "itest-postgres-password")
	const connPath = secCredDB + "/config/itest-live"
	secret := secCredSecret(ctx, t, e, "itest-live-root", "password", "itest-postgres-password")
	conn := &secretsv1alpha1.DatabaseConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-live", Namespace: namespace},
		Spec: secretsv1alpha1.DatabaseConnectionSpec{
			ResourceSpec:       secCredDeleteSpec(),
			MountPath:          secCredDB,
			PluginName:         "postgresql-database-plugin",
			AllowedRoles:       []string{"itest-static"},
			PasswordSecretRef:  &commonv1alpha1.SecretKeySelector{Name: secret.Name, Key: "password"},
			RotateRootOnCreate: true,
			PostgreSQL: &secretsv1alpha1.DatabasePostgreSQLConfig{DatabaseSQLConfig: secretsv1alpha1.DatabaseSQLConfig{
				ConnectionURL: "postgresql://{{username}}:{{password}}@" + addr + "/postgres?sslmode=disable",
				Username:      "postgres",
			}},
		},
	}
	mustCreate(ctx, t, e.k8s, conn)
	waitFor(t, "live connection ready", func() error { return expectReady(ctx, e.k8s, conn) })

	_, err := e.bao.Write(ctx, secCredDB+"/config/itest-probe", map[string]any{
		"plugin_name":    "postgresql-database-plugin",
		"connection_url": "postgresql://{{username}}:{{password}}@" + addr + "/postgres?sslmode=disable",
		"username":       "postgres",
		"password":       "itest-postgres-password",
	})
	if err == nil {
		t.Error("the original root password still works after rotateRootOnCreate")
	}

	static := &secretsv1alpha1.DatabaseStaticRole{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-static", Namespace: namespace},
		Spec: secretsv1alpha1.DatabaseStaticRoleSpec{
			ResourceSpec:   secCredDeleteSpec(),
			MountPath:      secCredDB,
			DBName:         "itest-live",
			Username:       "itest_app",
			RotationPeriod: metav1.Duration{Duration: time.Hour},
		},
	}
	mustCreate(ctx, t, e.k8s, static)
	staticPath := secCredDB + "/static-roles/itest-static"
	waitFor(t, "static role ready", func() error { return expectReady(ctx, e.k8s, static) })
	if err := expectPresent(ctx, e.bao, secCredDB+"/static-creds/itest-static"); err != nil {
		t.Errorf("static credentials: %v", err)
	}

	mustWrite(ctx, t, e.bao, staticPath, map[string]any{"username": "itest_app", "rotation_period": 600})
	waitFor(t, "rotation_period repaired", func() error {
		return secCredExpect(ctx, e.bao, staticPath, func(d map[string]any) error { return secCredEqual(d["rotation_period"], 3600) })
	})
	expectNoWrites(t, "DatabaseStaticRole")

	// An update after rotateRootOnCreate must not send the stale password, or the next rotation fails.
	changed := &secretsv1alpha1.DatabaseConnection{}
	if err := e.k8s.Get(ctx, client.ObjectKeyFromObject(conn), changed); err != nil {
		t.Fatal(err)
	}
	changed.Spec.AllowedRoles = []string{"itest-static", "itest-other"}
	if err := e.k8s.Update(ctx, changed); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "live connection updated", func() error {
		return secCredExpect(ctx, e.bao, connPath, func(d map[string]any) error {
			return secCredEqual(d["allowed_roles"], []any{"itest-static", "itest-other"})
		})
	})
	if _, err := e.bao.Write(ctx, secCredDB+"/rotate-role/itest-static", map[string]any{}); err != nil {
		t.Errorf("rotation after a connection update: %v", err)
	}

	if err := e.k8s.Delete(ctx, static); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "static role deleted", func() error { return expectAbsent(ctx, e.bao, staticPath) })
	if err := e.k8s.Delete(ctx, conn); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "live connection deleted", func() error { return expectAbsent(ctx, e.bao, connPath) })
}

func testRabbitMQConfig(ctx context.Context, t *testing.T, e env) {
	const leasePath = secCredRabbit + "/config/lease"
	secret := secCredSecret(ctx, t, e, "itest-rabbit-admin", "password", "first-admin-password")
	cfg := &secretsv1alpha1.RabbitMQConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-rabbit", Namespace: namespace},
		Spec: secretsv1alpha1.RabbitMQConfigSpec{
			ResourceSpec:      secCredSpec(),
			MountPath:         secCredRabbit,
			ConnectionURI:     "http://rabbitmq.invalid:15672",
			Username:          "admin",
			PasswordSecretRef: commonv1alpha1.SecretKeySelector{Name: secret.Name, Key: "password"},
			VerifyConnection:  new(false),
			Lease: &secretsv1alpha1.RabbitMQLease{
				TTL:    &metav1.Duration{Duration: time.Hour},
				MaxTTL: &metav1.Duration{Duration: 2 * time.Hour},
			},
		},
	}
	mustCreate(ctx, t, e.k8s, cfg)
	waitFor(t, "lease written", func() error {
		return secCredExpect(ctx, e.bao, leasePath, func(d map[string]any) error { return secCredEqual(d["ttl"], 3600) })
	})
	waitFor(t, "rabbitmq config ready", func() error { return expectReady(ctx, e.k8s, cfg) })

	mustWrite(ctx, t, e.bao, leasePath, map[string]any{"ttl": 60, "max_ttl": 7200})
	waitFor(t, "lease repaired", func() error {
		return secCredExpect(ctx, e.bao, leasePath, func(d map[string]any) error { return secCredEqual(d["ttl"], 3600) })
	})
	expectNoWrites(t, "RabbitMQConfig")

	before := sysCoreWrites(t, "RabbitMQConfig")
	secCredRotate(ctx, t, e, secret, "password", "second-admin-password")
	waitFor(t, "password change written", func() error {
		if sysCoreWrites(t, "RabbitMQConfig") == before {
			return errors.New("no write after the password changed")
		}
		return nil
	})

	if err := e.k8s.Delete(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "rabbitmq config resource gone", func() error { return expectGone(ctx, e.k8s, cfg) })
	if err := expectPresent(ctx, e.bao, leasePath); err != nil {
		t.Errorf("orphaned lease: %v", err)
	}
}

func testRabbitMQRole(ctx context.Context, t *testing.T, e env) {
	const path = secCredRabbit + "/roles/itest-app"
	role := &secretsv1alpha1.RabbitMQRole{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-app", Namespace: namespace},
		Spec: secretsv1alpha1.RabbitMQRoleSpec{
			ResourceSpec: secCredDeleteSpec(),
			MountPath:    secCredRabbit,
			Tags:         []string{"management"},
			VHosts:       map[string]secretsv1alpha1.RabbitMQVHostPermission{"/": {Configure: "^app\\.", Write: ".*", Read: ".*"}},
			VHostTopics:  map[string]map[string]secretsv1alpha1.RabbitMQTopicPermission{"/": {"amq.topic": {Write: "^app\\.", Read: ".*"}}},
		},
	}
	mustCreate(ctx, t, e.k8s, role)
	wantVHosts := map[string]any{"/": map[string]any{"configure": "^app\\.", "write": ".*", "read": ".*"}}
	waitFor(t, "rabbitmq role written", func() error {
		return secCredExpect(ctx, e.bao, path, func(d map[string]any) error { return secCredEqual(d["vhosts"], wantVHosts) })
	})
	waitFor(t, "rabbitmq role ready", func() error { return expectReady(ctx, e.k8s, role) })

	mustWrite(ctx, t, e.bao, path, map[string]any{"tags": "management", "vhosts": `{"/":{"configure":".*","write":".*","read":".*"}}`})
	waitFor(t, "vhosts repaired", func() error {
		return secCredExpect(ctx, e.bao, path, func(d map[string]any) error { return secCredEqual(d["vhosts"], wantVHosts) })
	})
	expectNoWrites(t, "RabbitMQRole")

	if err := e.k8s.Delete(ctx, role); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "rabbitmq role deleted", func() error { return expectAbsent(ctx, e.bao, path) })
}

func testKubernetesSecretsConfig(ctx context.Context, t *testing.T, e env) {
	const path = secCredK8s + "/config"
	secret := secCredSecret(ctx, t, e, "itest-k8s-sa", "token", "first.fake.jwt")
	cfg := &secretsv1alpha1.KubernetesSecretsConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-k8s", Namespace: namespace},
		Spec: secretsv1alpha1.KubernetesSecretsConfigSpec{
			ResourceSpec:               secCredSpec(),
			MountPath:                  secCredK8s,
			KubernetesHost:             "https://kubernetes.invalid:6443",
			ServiceAccountJWTSecretRef: &commonv1alpha1.SecretKeySelector{Name: secret.Name, Key: "token"},
			DisableLocalCAJWT:          new(true),
		},
	}
	mustCreate(ctx, t, e.k8s, cfg)
	waitFor(t, "kubernetes config written", func() error {
		return secCredExpect(ctx, e.bao, path, func(d map[string]any) error {
			if _, ok := d["service_account_jwt"]; ok {
				return errors.New("service_account_jwt returned by the read")
			}
			return secCredEqual(d["kubernetes_host"], "https://kubernetes.invalid:6443")
		})
	})
	waitFor(t, "kubernetes config ready", func() error { return expectReady(ctx, e.k8s, cfg) })

	mustWrite(ctx, t, e.bao, path, map[string]any{"kubernetes_host": "https://intruder.invalid"})
	waitFor(t, "kubernetes_host repaired", func() error {
		return secCredExpect(ctx, e.bao, path, func(d map[string]any) error {
			return secCredEqual(d["kubernetes_host"], "https://kubernetes.invalid:6443")
		})
	})
	expectNoWrites(t, "KubernetesSecretsConfig")

	before := sysCoreWrites(t, "KubernetesSecretsConfig")
	secCredRotate(ctx, t, e, secret, "token", "second.fake.jwt")
	waitFor(t, "token change written", func() error {
		if sysCoreWrites(t, "KubernetesSecretsConfig") == before {
			return errors.New("no write after the token changed")
		}
		return nil
	})

	if err := e.k8s.Delete(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "kubernetes config resource gone", func() error { return expectGone(ctx, e.k8s, cfg) })
	if err := expectPresent(ctx, e.bao, path); err != nil {
		t.Errorf("orphaned config: %v", err)
	}
}

func testKubernetesSecretsRole(ctx context.Context, t *testing.T, e env) {
	const path = secCredK8s + "/roles/itest-sa"
	role := &secretsv1alpha1.KubernetesSecretsRole{
		ObjectMeta: metav1.ObjectMeta{Name: "itest-sa", Namespace: namespace},
		Spec: secretsv1alpha1.KubernetesSecretsRoleSpec{
			ResourceSpec:                secCredDeleteSpec(),
			MountPath:                   secCredK8s,
			AllowedKubernetesNamespaces: []string{"team-b", "team-a"},
			ServiceAccountName:          "app",
			TokenDefaultTTL:             &metav1.Duration{Duration: time.Hour},
			ExtraLabels:                 map[string]string{"owner": "itest"},
		},
	}
	mustCreate(ctx, t, e.k8s, role)
	waitFor(t, "kubernetes role written", func() error {
		return secCredExpect(ctx, e.bao, path, func(d map[string]any) error { return secCredEqual(d["token_default_ttl"], 3600) })
	})
	waitFor(t, "kubernetes role ready", func() error { return expectReady(ctx, e.k8s, role) })

	mustWrite(ctx, t, e.bao, path, map[string]any{"allowed_kubernetes_namespaces": []string{"team-a"}})
	waitFor(t, "namespaces repaired", func() error {
		return secCredExpect(ctx, e.bao, path, func(d map[string]any) error {
			return secCredEqual(d["allowed_kubernetes_namespaces"], []any{"team-a", "team-b"})
		})
	})
	expectNoWrites(t, "KubernetesSecretsRole")

	if err := e.k8s.Delete(ctx, role); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "kubernetes role deleted", func() error { return expectAbsent(ctx, e.bao, path) })
}

func secCredSpec() commonv1alpha1.ResourceSpec {
	return commonv1alpha1.ResourceSpec{ConnectionRef: commonv1alpha1.ConnectionReference{Name: "dev"}}
}

func secCredDeleteSpec() commonv1alpha1.ResourceSpec {
	spec := secCredSpec()
	spec.DeletionPolicy = commonv1alpha1.DeletionPolicyDelete
	return spec
}

func secCredSecret(ctx context.Context, t *testing.T, e env, name, key, value string) *corev1.Secret {
	t.Helper()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Data:       map[string][]byte{key: []byte(value)},
	}
	mustCreate(ctx, t, e.k8s, secret)
	return secret
}

func secCredRotate(ctx context.Context, t *testing.T, e env, secret *corev1.Secret, key, value string) {
	t.Helper()
	if err := e.k8s.Get(ctx, client.ObjectKeyFromObject(secret), secret); err != nil {
		t.Fatal(err)
	}
	secret.Data[key] = []byte(value)
	if err := e.k8s.Update(ctx, secret); err != nil {
		t.Fatal(err)
	}
}

func secCredExpect(ctx context.Context, bao openbao.Client, path string, check func(map[string]any) error) error {
	data, err := bao.Read(ctx, path)
	if err != nil {
		return err
	}
	return check(data)
}

// secCredEqual compares a value read from OpenBao with want after a JSON round trip of both.
func secCredEqual(got, want any) error {
	if engine.Diff(engine.Payload{"v": want}, engine.Payload{"v": got}, engine.FieldSpec{}) != nil {
		return fmt.Errorf("got %v, want %v", got, want)
	}
	return nil
}

// secCredStartPostgres starts a PostgreSQL container with an extra login user itest_app and returns host:port.
// It never pulls: when docker or the image is not available locally it skips, or fails when CI is set (CI pulls
// $POSTGRES_IMAGE first).
func secCredStartPostgres(t *testing.T, password string) string {
	t.Helper()
	docker := os.Getenv("DOCKER_BIN")
	if docker == "" {
		docker, _ = exec.LookPath("docker")
	}
	image := os.Getenv("POSTGRES_IMAGE")
	if image == "" {
		image = "postgres:16-alpine"
	}
	if docker == "" || exec.Command(docker, "image", "inspect", image).Run() != nil {
		testenv.SkipOrFail(t, "docker or the "+image+" image is not available locally")
	}
	out, err := exec.Command(docker, "run", "-d", "--rm", "-p", "127.0.0.1::5432", "-e", "POSTGRES_PASSWORD="+password, image).Output()
	if err != nil {
		t.Fatalf("docker run: %v", err)
	}
	id := strings.TrimSpace(string(out))
	t.Cleanup(func() { _ = exec.Command(docker, "rm", "-f", id).Run() })

	waitFor(t, "postgres ready", func() error {
		return exec.Command(docker, "exec", id, "pg_isready", "-h", "127.0.0.1", "-U", "postgres").Run()
	})
	create := exec.Command(docker, "exec", id, "psql", "-h", "127.0.0.1", "-U", "postgres", "-c",
		"CREATE ROLE itest_app LOGIN PASSWORD 'initial'")
	if out, err := create.CombinedOutput(); err != nil {
		t.Fatalf("create itest_app: %v: %s", err, out)
	}
	out, err = exec.Command(docker, "port", id, "5432/tcp").Output()
	if err != nil {
		t.Fatalf("docker port: %v", err)
	}
	lines := strings.Fields(string(out))
	if len(lines) == 0 {
		t.Fatal("docker port: no mapping")
	}
	return lines[0]
}

func secCredSetup(ctx context.Context, t *testing.T) env {
	t.Helper()
	root := repoRoot(t)
	baoBin, assets := secretsPrerequisites(t, root)

	addr := startBao(t, baoBin)
	bao, err := openbao.NewPool().Client(ctx, "itest-cred", openbao.Config{
		Address: addr,
		Timeout: 10 * time.Second,
		Auth: openbao.TokenAuth{
			CredentialKey: "itest-cred",
			Token:         func(context.Context) (string, error) { return rootToken, nil },
		},
	})
	if err != nil {
		t.Fatalf("openbao client: %v", err)
	}

	kinds := []string{"databaseconnections", "databaseroles", "databasestaticroles", "rabbitmqconfigs", "rabbitmqroles",
		"kubernetessecretsconfigs", "kubernetessecretsroles"}
	crds := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		crds = append(crds, filepath.Join(root, "config", "crd", "bases", "secrets.obco.dev_"+kind+".yaml"))
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
	for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, secretsv1alpha1.AddToScheme} {
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
	setups := []func(ctrl.Manager, engine.Connections, engine.Options) error{
		secrets.SetupDatabaseConnection, secrets.SetupDatabaseRole, secrets.SetupDatabaseStaticRole,
		secrets.SetupRabbitMQConfig, secrets.SetupRabbitMQRole,
		secrets.SetupKubernetesSecretsConfig, secrets.SetupKubernetesSecretsRole,
	}
	for _, setupKind := range setups {
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
