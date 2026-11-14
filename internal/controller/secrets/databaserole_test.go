package secrets

import (
	"context"
	"maps"
	"reflect"
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

const (
	dbTestRotationStatement = `ALTER USER "{{name}}" WITH PASSWORD '{{password}}';`
	dbTestCreateStatement   = `CREATE ROLE "{{name}}";`
	dbTestDropStatement     = `DROP ROLE "{{name}}";`
)

func newDatabaseRole() *secretsv1alpha1.DatabaseRole {
	return &secretsv1alpha1.DatabaseRole{
		ObjectMeta: metav1.ObjectMeta{Name: "app-ro", Namespace: "team"},
		Spec: secretsv1alpha1.DatabaseRoleSpec{
			MountPath: "database",
			DBName:    "pg",
			DatabaseCredentialFields: secretsv1alpha1.DatabaseCredentialFields{
				CredentialType:   secretsv1alpha1.DatabaseCredentialPassword,
				CredentialConfig: map[string]string{"password_policy": "db-policy"},
			},
			DefaultTTL:           &metav1.Duration{Duration: time.Hour},
			MaxTTL:               &metav1.Duration{Duration: 24 * time.Hour},
			CreationStatements:   []string{dbTestCreateStatement},
			RevocationStatements: []string{dbTestDropStatement},
		},
	}
}

func TestDatabaseRolePathAndDesired(t *testing.T) {
	a := DatabaseRoleAdapter{}
	r := newDatabaseRole()
	if got := a.Path(r); got != "database/roles/app-ro" {
		t.Errorf("path = %q", got)
	}
	desired, err := a.Desired(context.Background(), nil, r)
	if err != nil {
		t.Fatal(err)
	}
	want := engine.Payload{
		"db_name":               "pg",
		"credential_type":       "password",
		"credential_config":     map[string]string{"password_policy": "db-policy"},
		"default_ttl":           int64(3600),
		"max_ttl":               int64(86400),
		"creation_statements":   []string{dbTestCreateStatement},
		"revocation_statements": []string{dbTestDropStatement},
		"rollback_statements":   []string{},
		"renew_statements":      []string{},
	}
	if !reflect.DeepEqual(desired, want) {
		t.Errorf("desired =\n%#v\nwant\n%#v", desired, want)
	}
}

func TestDatabaseRoleGoldenRead(t *testing.T) {
	a := DatabaseRoleAdapter{}
	r := newDatabaseRole()
	desired, _ := a.Desired(context.Background(), nil, r)
	tests := []struct {
		name      string
		mutate    func(map[string]any)
		differing []string
	}{
		{name: "in sync", mutate: func(map[string]any) {}},
		{name: "ttl changed", mutate: func(read map[string]any) { read["default_ttl"] = 60.0 }, differing: []string{"default_ttl"}},
		{name: "statements changed", mutate: func(read map[string]any) { read["revocation_statements"] = []any{} },
			differing: []string{"revocation_statements"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// GET database/roles/r on OpenBao 2.7.2.
			read := map[string]any{
				"creation_statements":   []any{dbTestCreateStatement},
				"credential_config":     map[string]any{"password_policy": "db-policy"},
				"credential_type":       "password",
				"db_name":               "pg",
				"default_ttl":           3600.0,
				"max_ttl":               86400.0,
				"renew_statements":      []any{},
				"revocation_statements": []any{dbTestDropStatement},
				"rollback_statements":   []any{},
			}
			tt.mutate(read)
			c := &credClient{reads: map[string]map[string]any{"database/roles/app-ro": read}}
			observed, found, err := a.Observe(context.Background(), c, r)
			if err != nil || !found {
				t.Fatalf("found=%v err=%v", found, err)
			}
			if got := engine.Diff(desired, observed, a.Fields()); !slices.Equal(got, tt.differing) {
				t.Errorf("differing = %v, want %v", got, tt.differing)
			}
		})
	}
}

func TestDatabaseRoleCredentialConfigTypedRead(t *testing.T) {
	a := DatabaseRoleAdapter{}
	r := newDatabaseRole()
	r.Spec.CredentialType = secretsv1alpha1.DatabaseCredentialRSAPrivateKey
	r.Spec.CredentialConfig = map[string]string{"key_bits": "2048"}
	desired, _ := a.Desired(context.Background(), nil, r)
	c := &credClient{reads: map[string]map[string]any{"database/roles/app-ro": {
		"creation_statements": []any{dbTestCreateStatement}, "credential_type": "rsa_private_key",
		"credential_config": map[string]any{"format": "pkcs8", "key_bits": 2048.0}, "db_name": "pg",
		"default_ttl": 3600.0, "max_ttl": 86400.0, "renew_statements": []any{},
		"revocation_statements": []any{dbTestDropStatement}, "rollback_statements": []any{},
	}}}
	observed, _, err := a.Observe(context.Background(), c, r)
	if err != nil {
		t.Fatal(err)
	}
	if got := engine.Diff(desired, observed, a.Fields()); len(got) != 0 {
		t.Errorf("differing = %v", got)
	}
}

func TestDatabaseStaticRole(t *testing.T) {
	a := DatabaseStaticRoleAdapter{}
	r := &secretsv1alpha1.DatabaseStaticRole{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "team"},
		Spec: secretsv1alpha1.DatabaseStaticRoleSpec{
			MountPath:          "database",
			Name:               "app-static",
			DBName:             "pg",
			Username:           "app",
			RotationPeriod:     metav1.Duration{Duration: 24 * time.Hour},
			RotationStatements: []string{dbTestRotationStatement},
		},
	}
	if got := a.Path(r); got != "database/static-roles/app-static" {
		t.Errorf("path = %q", got)
	}
	desired, err := a.Desired(context.Background(), nil, r)
	if err != nil {
		t.Fatal(err)
	}
	want := engine.Payload{
		"db_name":             "pg",
		"username":            "app",
		"rotation_period":     int64(86400),
		"rotation_statements": []string{dbTestRotationStatement},
		"credential_type":     "password",
	}
	if !reflect.DeepEqual(desired, want) {
		t.Errorf("desired =\n%#v\nwant\n%#v", desired, want)
	}
	// Shape of the static role read handler (path_roles.go pathStaticRoleRead); the password is never returned.
	read := map[string]any{
		"credential_type":     "password",
		"db_name":             "pg",
		"last_vault_rotation": "2026-11-14T10:00:00.000000Z",
		"rotation_period":     86400.0,
		"rotation_statements": []any{dbTestRotationStatement},
		"username":            "app",
	}
	c := &credClient{reads: map[string]map[string]any{"database/static-roles/app-static": read}}
	observed, _, err := a.Observe(context.Background(), c, r)
	if err != nil {
		t.Fatal(err)
	}
	if got := engine.Diff(desired, observed, a.Fields()); len(got) != 0 {
		t.Errorf("differing = %v", got)
	}
	read["rotation_period"] = 3600.0
	if got := engine.Diff(desired, observed, a.Fields()); !slices.Equal(got, []string{"rotation_period"}) {
		t.Errorf("differing = %v, want rotation_period", got)
	}
}

func TestDatabaseRoleCAPrivateKeyIsWriteOnly(t *testing.T) {
	a := DatabaseRoleAdapter{}
	r := newDatabaseRole()
	r.Spec.CredentialType = secretsv1alpha1.DatabaseCredentialClientCertificate
	r.Spec.CredentialConfig = map[string]string{"ca_cert": "fake-ca-cert", "common_name_template": "{{.RoleName}}", "key_type": "ec"}
	r.Spec.CAPrivateKeySecretRef = &commonv1alpha1.SecretKeySelector{Name: "db-ca", Key: "key"}
	refs := credResolver{"db-ca": {"key": "fake-ca-key"}}
	desired, err := a.Desired(context.Background(), refs, r)
	if err != nil {
		t.Fatal(err)
	}
	if desired[databaseCAPrivateKey] != "fake-ca-key" || engine.WriteOnlyHash(testHashKey, desired, a.Fields()) == "" {
		t.Fatalf("ca key not write-only in desired: %v", slices.Sorted(maps.Keys(desired)))
	}
	if got := a.SecretRefs(r); !slices.Equal(got, []string{"db-ca"}) {
		t.Errorf("secret refs = %v", got)
	}

	c := newKVFakeClient(nil)
	if _, err := a.Write(context.Background(), c, r, desired, true); err != nil {
		t.Fatal(err)
	}
	body := c.writes["database/roles/app-ro"]
	if _, top := body[databaseCAPrivateKey]; top {
		t.Error("ca key sent top-level")
	}
	if cfg := body["credential_config"].(map[string]any); cfg[databaseCAPrivateKey] != "fake-ca-key" || cfg["key_type"] != "ec" {
		t.Errorf("credential_config keys = %v", slices.Sorted(maps.Keys(cfg)))
	}

	// Read shape of a client_certificate role: credential_config holds the stored generator settings with defaults.
	read := map[string]any{
		"db_name": "pg", "credential_type": "client_certificate", "default_ttl": 3600.0, "max_ttl": 86400.0,
		"creation_statements": []any{dbTestCreateStatement}, "revocation_statements": []any{dbTestDropStatement},
		"rollback_statements": []any{}, "renew_statements": []any{},
		"credential_config": map[string]any{
			"ca_cert": "fake-ca-cert", "ca_private_key": "other-ca-key", "common_name_template": "{{.RoleName}}",
			"key_type": "ec", "key_bits": 256.0, "signature_bits": 256.0,
		},
	}
	observed, found, err := a.Observe(context.Background(), newKVFakeClient(map[string]map[string]any{"database/roles/app-ro": read}), r)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if _, kept := observed["credential_config"].(map[string]any)[databaseCAPrivateKey]; kept {
		t.Error("observe kept the ca key")
	}
	if d := engine.Diff(desired, observed, a.Fields()); len(d) != 0 {
		t.Errorf("steady state differs on %v", d)
	}
}
