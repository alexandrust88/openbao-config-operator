package secrets

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// credResolver resolves Secret keys from a map of Secret name -> key -> value.
type credResolver map[string]map[string]string

func (r credResolver) SecretValue(_ context.Context, _ string, ref commonv1alpha1.SecretKeySelector) ([]byte, error) {
	if value, ok := r[ref.Name][ref.Key]; ok {
		return []byte(value), nil
	}
	return nil, fmt.Errorf("secret %s key %s: %w", ref.Name, ref.Key, engine.ErrReferenceNotReady)
}

func (credResolver) Object(context.Context, client.ObjectKey, client.Object) error {
	return errors.New("unexpected object lookup")
}

// credClient is an in-memory OpenBao that records writes and deletes and fails writes to failPaths.
type credClient struct {
	openbao.Client
	reads     map[string]map[string]any
	writes    []string
	deletes   []string
	failPaths map[string]bool
}

func (c *credClient) Read(_ context.Context, path string) (map[string]any, error) {
	if data, ok := c.reads[path]; ok {
		return data, nil
	}
	return nil, openbao.ErrNotFound
}

func (c *credClient) Write(_ context.Context, path string, _ map[string]any) (map[string]any, error) {
	c.writes = append(c.writes, path)
	if c.failPaths[path] {
		return nil, &openbao.ResponseError{StatusCode: 400, Errors: []string{"failed"}}
	}
	return nil, nil
}

func (c *credClient) Delete(_ context.Context, path string) error {
	c.deletes = append(c.deletes, path)
	return nil
}

func newDatabaseConnection() *secretsv1alpha1.DatabaseConnection {
	return &secretsv1alpha1.DatabaseConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "app-db", Namespace: "team"},
		Spec: secretsv1alpha1.DatabaseConnectionSpec{
			MountPath:              "database",
			PluginName:             "postgresql-database-plugin",
			VerifyConnection:       new(false),
			AllowedRoles:           []string{"app-rw", "app-ro"},
			RootRotationStatements: []string{dbTestRotationStatement},
			PasswordPolicy:         "db-policy",
			PasswordSecretRef:      &commonv1alpha1.SecretKeySelector{Name: "db-root", Key: "password"},
			PostgreSQL: &secretsv1alpha1.DatabasePostgreSQLConfig{
				DatabaseSQLConfig: secretsv1alpha1.DatabaseSQLConfig{
					ConnectionURL:         "postgresql://{{username}}:{{password}}@db.example:5432/app?sslmode=disable",
					Username:              "vault-root",
					MaxOpenConnections:    new(int32(8)),
					MaxIdleConnections:    new(int32(4)),
					MaxConnectionLifetime: &metav1.Duration{Duration: 5 * time.Minute},
					UsernameTemplate:      "{{.RoleName}}-{{random 8}}",
				},
				DisableEscaping:        new(true),
				PasswordAuthentication: "scram-sha-256",
			},
		},
	}
}

func dbResolver() credResolver {
	return credResolver{"db-root": {"password": "fake-root-password"}}
}

// goldenPostgresRead is the .data of GET database/config/pg on OpenBao 2.7.2 after a JSON write of the spec above.
func goldenPostgresRead() map[string]any {
	return map[string]any{
		"allowed_roles": []any{"app-ro", "app-rw"},
		"connection_details": map[string]any{
			"connection_url":          "postgresql://{{username}}:{{password}}@db.example:5432/app?sslmode=disable",
			"disable_escaping":        true,
			"max_connection_lifetime": 300.0,
			"max_idle_connections":    4.0,
			"max_open_connections":    8.0,
			"password_authentication": "scram-sha-256",
			"username":                "vault-root",
			"username_template":       "{{.RoleName}}-{{random 8}}",
		},
		"password_policy":                    "db-policy",
		"plugin_name":                        "postgresql-database-plugin",
		"plugin_version":                     "",
		"root_credentials_rotate_statements": []any{dbTestRotationStatement},
	}
}

func TestDatabaseConnectionPath(t *testing.T) {
	c := newDatabaseConnection()
	a := DatabaseConnectionAdapter{}
	if got := a.Path(c); got != "database/config/app-db" {
		t.Errorf("default name: got %q", got)
	}
	c.Spec.Name = "pg"
	c.Spec.MountPath = "team/db"
	if got := a.Path(c); got != "team/db/config/pg" {
		t.Errorf("spec name: got %q", got)
	}
}

func TestDatabaseConnectionDesired(t *testing.T) {
	desired, err := DatabaseConnectionAdapter{}.Desired(context.Background(), dbResolver(), newDatabaseConnection())
	if err != nil {
		t.Fatal(err)
	}
	want := engine.Payload{
		"plugin_name":              "postgresql-database-plugin",
		"verify_connection":        false,
		"allowed_roles":            []string{"app-rw", "app-ro"},
		"root_rotation_statements": []string{dbTestRotationStatement},
		"password_policy":          "db-policy",
		"password":                 "fake-root-password",
		"connection_url":           "postgresql://{{username}}:{{password}}@db.example:5432/app?sslmode=disable",
		"username":                 "vault-root",
		"max_open_connections":     int64(8),
		"max_idle_connections":     int64(4),
		"max_connection_lifetime":  int64(300),
		"username_template":        "{{.RoleName}}-{{random 8}}",
		"disable_escaping":         true,
		"password_authentication":  "scram-sha-256",
	}
	if !reflect.DeepEqual(desired, want) {
		t.Errorf("desired =\n%#v\nwant\n%#v", desired, want)
	}
}

// dbConnectionDefaults adds the top-level settings Desired fills with their server default.
func dbConnectionDefaults(p engine.Payload) engine.Payload {
	out := engine.Payload{"allowed_roles": []string{}, "root_rotation_statements": []string{}, "password_policy": ""}
	maps.Copy(out, p)
	return out
}

func TestDatabaseConnectionDesiredDefaults(t *testing.T) {
	c := &secretsv1alpha1.DatabaseConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "team"},
		Spec: secretsv1alpha1.DatabaseConnectionSpec{
			MountPath:    "database",
			PluginName:   "custom-database-plugin",
			PluginConfig: map[string]string{"connection_url": "custom://db", "region": "eu"},
		},
	}
	desired, err := DatabaseConnectionAdapter{}.Desired(context.Background(), credResolver{}, c)
	if err != nil {
		t.Fatal(err)
	}
	want := dbConnectionDefaults(engine.Payload{"plugin_name": "custom-database-plugin", "connection_url": "custom://db", "region": "eu"})
	if !reflect.DeepEqual(desired, want) {
		t.Errorf("desired = %#v, want %#v", desired, want)
	}
}

func TestDatabaseConnectionDesiredPluginSections(t *testing.T) {
	refs := credResolver{"tls": {"bundle": "fake-pem", "key": "fake-key"}}
	tests := []struct {
		name string
		spec secretsv1alpha1.DatabaseConnectionSpec
		want engine.Payload
	}{
		{
			name: "mysql",
			spec: secretsv1alpha1.DatabaseConnectionSpec{PluginName: "mysql-database-plugin", MySQL: &secretsv1alpha1.DatabaseMySQLConfig{
				DatabaseSQLConfig:          secretsv1alpha1.DatabaseSQLConfig{ConnectionURL: "{{username}}:{{password}}@tcp(db:3306)/"},
				TLSCA:                      "ca-pem",
				TLSCertificateKeySecretRef: &commonv1alpha1.SecretKeySelector{Name: "tls", Key: "key"},
				TLSServerName:              "db",
				TLSSkipVerify:              new(false),
			}},
			want: engine.Payload{"plugin_name": "mysql-database-plugin", "connection_url": "{{username}}:{{password}}@tcp(db:3306)/",
				"tls_ca": "ca-pem", "tls_certificate_key": "fake-key", "tls_server_name": "db", "tls_skip_verify": false},
		},
		{
			name: "cassandra",
			spec: secretsv1alpha1.DatabaseConnectionSpec{PluginName: "cassandra-database-plugin", Cassandra: &secretsv1alpha1.DatabaseCassandraConfig{
				DatabaseTLSPemConfig: secretsv1alpha1.DatabaseTLSPemConfig{TLS: new(true), PemBundleSecretRef: &commonv1alpha1.SecretKeySelector{Name: "tls", Key: "bundle"},
					ConnectTimeout: &metav1.Duration{Duration: 5 * time.Second}},
				Hosts: []string{"c1", "c2"}, Port: new(int32(9042)), Username: "cassandra",
			}},
			want: engine.Payload{"plugin_name": "cassandra-database-plugin", "tls": true, "pem_bundle": "fake-pem", "connect_timeout": int64(5),
				"hosts": "c1,c2", "port": int64(9042), "username": "cassandra"},
		},
		{
			name: "influxdb",
			spec: secretsv1alpha1.DatabaseConnectionSpec{PluginName: "influxdb-database-plugin", InfluxDB: &secretsv1alpha1.DatabaseInfluxDBConfig{
				DatabaseTLSPemConfig: secretsv1alpha1.DatabaseTLSPemConfig{TLSMinVersion: "tls12"}, Host: "influx", Username: "admin",
			}},
			want: engine.Payload{"plugin_name": "influxdb-database-plugin", "tls_min_version": "tls12", "host": "influx", "username": "admin"},
		},
		{
			name: "valkey",
			spec: secretsv1alpha1.DatabaseConnectionSpec{PluginName: "valkey-database-plugin", Valkey: &secretsv1alpha1.DatabaseValkeyConfig{
				Host: "valkey", Port: new(int32(6379)), Username: "default", TLS: new(true), CACert: "ca-pem",
			}},
			want: engine.Payload{"plugin_name": "valkey-database-plugin", "host": "valkey", "port": int64(6379), "username": "default", "tls": true, "ca_cert": "ca-pem"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &secretsv1alpha1.DatabaseConnection{ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "team"}, Spec: tt.spec}
			desired, err := DatabaseConnectionAdapter{}.Desired(context.Background(), refs, c)
			if err != nil {
				t.Fatal(err)
			}
			if want := dbConnectionDefaults(tt.want); !reflect.DeepEqual(desired, want) {
				t.Errorf("desired =\n%#v\nwant\n%#v", desired, want)
			}
		})
	}
}

func TestDatabaseConnectionDesiredMissingSecret(t *testing.T) {
	_, err := DatabaseConnectionAdapter{}.Desired(context.Background(), credResolver{}, newDatabaseConnection())
	if !errors.Is(err, engine.ErrReferenceNotReady) {
		t.Errorf("err = %v, want ErrReferenceNotReady", err)
	}
}

func TestDatabaseConnectionGoldenRead(t *testing.T) {
	a := DatabaseConnectionAdapter{}
	conn := newDatabaseConnection()
	desired, err := a.Desired(context.Background(), dbResolver(), conn)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		mutate    func(read map[string]any)
		differing []string
	}{
		{name: "in sync", mutate: func(map[string]any) {}},
		{name: "allowed roles changed", mutate: func(read map[string]any) { read["allowed_roles"] = []any{"app-ro"} },
			differing: []string{"allowed_roles"}},
		{name: "detail changed", mutate: func(read map[string]any) {
			read["connection_details"].(map[string]any)["max_open_connections"] = 2.0
		}, differing: []string{"max_open_connections"}},
		{name: "rotation statements changed", mutate: func(read map[string]any) {
			read["root_credentials_rotate_statements"] = []any{}
		}, differing: []string{"root_rotation_statements"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			read := goldenPostgresRead()
			tt.mutate(read)
			c := &credClient{reads: map[string]map[string]any{"database/config/app-db": read}}
			observed, found, err := a.Observe(context.Background(), c, conn)
			if err != nil || !found {
				t.Fatalf("found=%v err=%v", found, err)
			}
			if got := engine.Diff(desired, observed, a.Fields()); !slices.Equal(got, tt.differing) {
				t.Errorf("differing = %v, want %v", got, tt.differing)
			}
		})
	}
}

func TestDatabaseConnectionObserveRedactedURL(t *testing.T) {
	a := DatabaseConnectionAdapter{}
	conn := &secretsv1alpha1.DatabaseConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "lit", Namespace: "team"},
		Spec: secretsv1alpha1.DatabaseConnectionSpec{MountPath: "database", PluginName: "postgresql-database-plugin",
			PostgreSQL: &secretsv1alpha1.DatabasePostgreSQLConfig{
				DatabaseSQLConfig: secretsv1alpha1.DatabaseSQLConfig{ConnectionURL: "postgresql://root:topsecret@db.example:5432/app"},
			}},
	}
	desired, err := a.Desired(context.Background(), credResolver{}, conn)
	if err != nil {
		t.Fatal(err)
	}
	for stored, differing := range map[string][]string{
		"postgresql://root:xxxxx@db.example:5432/app":    nil,
		"postgresql://root:xxxxx@other.example:5432/app": {"connection_url"},
	} {
		c := &credClient{reads: map[string]map[string]any{"database/config/lit": {
			"allowed_roles":                      []any{},
			"connection_details":                 map[string]any{"connection_url": stored},
			"password_policy":                    "",
			"plugin_name":                        "postgresql-database-plugin",
			"plugin_version":                     "",
			"root_credentials_rotate_statements": []any{},
		}}}
		observed, _, err := a.Observe(context.Background(), c, conn)
		if err != nil {
			t.Fatal(err)
		}
		if got := engine.Diff(desired, observed, a.Fields()); !slices.Equal(got, differing) {
			t.Errorf("stored %s: differing = %v, want %v", stored, got, differing)
		}
	}
}

func TestDatabaseConnectionGoldenReadCassandra(t *testing.T) {
	a := DatabaseConnectionAdapter{}
	conn := &secretsv1alpha1.DatabaseConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "cs", Namespace: "team"},
		Spec: secretsv1alpha1.DatabaseConnectionSpec{MountPath: "database", PluginName: "cassandra-database-plugin",
			PasswordSecretRef: &commonv1alpha1.SecretKeySelector{Name: "db-root", Key: "password"},
			Cassandra: &secretsv1alpha1.DatabaseCassandraConfig{
				DatabaseTLSPemConfig: secretsv1alpha1.DatabaseTLSPemConfig{TLS: new(false), ConnectTimeout: &metav1.Duration{Duration: 5 * time.Second}},
				Hosts:                []string{"c1.example", "c2.example"}, Port: new(int32(9042)), ProtocolVersion: new(int32(4)),
				Username: "cassandra", SkipVerification: new(true), SocketKeepAlive: &metav1.Duration{Duration: 30 * time.Second},
				Consistency: "LOCAL_QUORUM", LocalDatacenter: "dc1",
			}},
	}
	desired, err := a.Desired(context.Background(), dbResolver(), conn)
	if err != nil {
		t.Fatal(err)
	}
	c := &credClient{reads: map[string]map[string]any{"database/config/cs": {
		"allowed_roles": []any{},
		"connection_details": map[string]any{"connect_timeout": 5.0, "consistency": "LOCAL_QUORUM", "hosts": "c1.example,c2.example",
			"local_datacenter": "dc1", "port": 9042.0, "protocol_version": 4.0, "skip_verification": true, "socket_keep_alive": 30.0,
			"tls": false, "username": "cassandra"},
		"password_policy": "", "plugin_name": "cassandra-database-plugin", "plugin_version": "",
		"root_credentials_rotate_statements": []any{},
	}}}
	observed, _, err := a.Observe(context.Background(), c, conn)
	if err != nil {
		t.Fatal(err)
	}
	if got := engine.Diff(desired, observed, a.Fields()); len(got) != 0 {
		t.Errorf("differing = %v", got)
	}
}

func TestDatabaseConnectionObserveAbsent(t *testing.T) {
	_, found, err := DatabaseConnectionAdapter{}.Observe(context.Background(), &credClient{}, newDatabaseConnection())
	if err != nil || found {
		t.Errorf("found=%v err=%v, want absent", found, err)
	}
}

func TestDatabaseConnectionWriteOnlyHash(t *testing.T) {
	a := DatabaseConnectionAdapter{}
	key := []byte("test-key")
	first, _ := a.Desired(context.Background(), dbResolver(), newDatabaseConnection())
	second, _ := a.Desired(context.Background(), credResolver{"db-root": {"password": "rotated"}}, newDatabaseConnection())
	if engine.WriteOnlyHash(key, first, a.Fields()) == engine.WriteOnlyHash(key, second, a.Fields()) {
		t.Error("a changed password must change the write-only hash")
	}
	if got := engine.Diff(first, second, a.Fields()); len(got) != 0 {
		t.Errorf("write-only keys compared: %v", got)
	}
}

func TestDatabaseConnectionRotateRootFields(t *testing.T) {
	a := DatabaseConnectionAdapter{}
	conn := newDatabaseConnection()
	extra, _ := a.FieldsFor(context.Background(), dbResolver(), conn)
	if len(extra.CreateOnly) != 0 {
		t.Errorf("without rotateRootOnCreate CreateOnly = %v", extra.CreateOnly)
	}
	conn.Spec.RotateRootOnCreate = true
	extra, _ = a.FieldsFor(context.Background(), dbResolver(), conn)
	desired, _ := a.Desired(context.Background(), dbResolver(), conn)
	update := engine.StripCreateOnly(desired, extra)
	if _, ok := update["password"]; ok {
		t.Error("an update after rotateRootOnCreate must not send the stale password")
	}
	if _, ok := update["connection_url"]; !ok {
		t.Error("an update must still send the other settings")
	}
}

func TestDatabaseConnectionWrite(t *testing.T) {
	a := DatabaseConnectionAdapter{}
	tests := []struct {
		name    string
		rotate  bool
		create  bool
		fail    bool
		writes  []string
		deletes []string
		wantErr bool
	}{
		{name: "update", rotate: true, writes: []string{"database/config/app-db"}},
		{name: "create without rotation", create: true, writes: []string{"database/config/app-db"}},
		{name: "create with rotation", rotate: true, create: true,
			writes: []string{"database/config/app-db", "database/rotate-root/app-db"}},
		{name: "failed rotation removes the connection", rotate: true, create: true, fail: true,
			writes: []string{"database/config/app-db", "database/rotate-root/app-db"}, deletes: []string{"database/config/app-db"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := newDatabaseConnection()
			conn.Spec.RotateRootOnCreate = tt.rotate
			c := &credClient{failPaths: map[string]bool{"database/rotate-root/app-db": tt.fail}}
			_, err := a.Write(context.Background(), c, conn, engine.Payload{"plugin_name": "x"}, tt.create)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !slices.Equal(c.writes, tt.writes) || !slices.Equal(c.deletes, tt.deletes) {
				t.Errorf("writes %v deletes %v, want %v %v", c.writes, c.deletes, tt.writes, tt.deletes)
			}
		})
	}
}

func TestDatabaseConnectionSecretRefs(t *testing.T) {
	conn := newDatabaseConnection()
	conn.Spec.PrivateKeySecretRef = &commonv1alpha1.SecretKeySelector{Name: "db-key", Key: "pem"}
	if got := (DatabaseConnectionAdapter{}).SecretRefs(conn); !slices.Equal(got, []string{"db-key", "db-root"}) {
		t.Errorf("SecretRefs = %v", got)
	}
}
