package importer

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

func TestRedactURL(t *testing.T) {
	for value, want := range map[string]string{
		"https://synthetic-user:synthetic-pass@siem.test/ingest":        "https://synthetic-user:REDACTED@siem.test/ingest",
		"https://synthetic-token@siem.test/ingest":                      "https://REDACTED@siem.test/ingest",
		"https://siem.test/ingest?Token=synthetic&format=json":          "https://siem.test/ingest?Token=REDACTED&format=json",
		"https://s.test/x?X-Amz-Signature=synthetic&sig=s#frag":         "https://s.test/x?X-Amz-Signature=REDACTED&sig=REDACTED#frag",
		"https://s.test/x?access_token=a&api_key=b&client_secret=c":     "https://s.test/x?access_token=REDACTED&api_key=REDACTED&client_secret=REDACTED",
		"postgresql://admin:pass@db:5432/app?sslpassword=synthetic":     "postgresql://admin:REDACTED@db:5432/app?sslpassword=REDACTED",
		"admin:synthetic-pass@tcp(db:3306)/app":                         "admin:REDACTED@tcp(db:3306)/app",
		"postgresql://{{username}}:{{password}}@db:5432/app":            "",
		"postgresql://admin:xxxxx@db:5432/app":                          "",
		"mongodb://{{username}}:{{password}}@db/admin?authSource=admin": "",
		"https://siem.test/ingest?password={{password}}&key=":           "",
		"https://keycloak.test/realms/platform":                         "",
		"env://AUDIT_URI":                                               "",
		"alice@example.test":                                            "",
		`path "secret/*" { capabilities = ["read"] }`:                   "",
	} {
		got, ok := redactURL(value)
		if want == "" {
			want = value
		}
		if got != want || ok != (want != value) {
			t.Errorf("redactURL(%q) = %q, %v; want %q", value, got, ok, want)
		}
	}
}

// TestImportMovesURLCredentials: no credential in a URL reaches the emitted YAML. An audit device URL moves into a
// Secret reference like its headers; a URL without a Secret alternative is redacted with a note.
func TestImportMovesURLCredentials(t *testing.T) {
	bao := newFakeBao(t)
	uri := "https://synthetic-user:synthetic-pass@siem.test/ingest?token=synthetic-token"
	bao.reads["sys/audit"] = engine.Payload{"siem/": map[string]any{litType: "http", litDescription: "", "local": false,
		"path": "siem/", "options": map[string]any{"uri": uri, "headers": `{"Authorization":["synthetic-header"]}`,
			"format": "json"}}}
	bao.reads["sys/mounts"]["database/"] = map[string]any{litType: "database", "accessor": "database_1"}
	bao.lists["database/config"] = []string{"pg"}
	bao.reads["database/config/pg"] = engine.Payload{"plugin_name": "postgresql-database-plugin",
		"allowed_roles": []any{}, "connection_details": map[string]any{
			"connection_url": "postgresql://{{username}}:{{password}}@db:5432/app?sslpassword=synthetic-ssl",
			"username":       "admin"}}

	result, err := Import(t.Context(), bao, Options{Namespace: litObco})
	if err != nil {
		t.Fatal(err)
	}
	var stream bytes.Buffer
	if err := WriteStream(&stream, result.Objects); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stream.String(), "synthetic") {
		t.Errorf("emitted YAML holds a credential:\n%s", stream.String())
	}

	var audit *sysv1alpha1.Audit
	var db *secretsv1alpha1.DatabaseConnection
	for _, obj := range result.Objects {
		switch o := obj.(type) {
		case *sysv1alpha1.Audit:
			audit = o
		case *secretsv1alpha1.DatabaseConnection:
			db = o
		}
	}
	if audit == nil || db == nil {
		t.Fatalf("objects = %v, notes = %q", result.Objects, result.Notes)
	}
	wantRefs := map[string]commonv1alpha1.SecretKeySelector{"uri": {Name: "siem", Key: "uri"},
		"headers": {Name: "siem", Key: "headers"}}
	if !reflect.DeepEqual(audit.Spec.OptionsSecretRefs, wantRefs) ||
		!reflect.DeepEqual(audit.Spec.Options, map[string]sysv1alpha1.AuditOptionValue{"format": "json"}) {
		t.Errorf("audit options = %v, secret refs = %v", audit.Spec.Options, audit.Spec.OptionsSecretRefs)
	}
	if got := db.Spec.PostgreSQL.ConnectionURL; got != "postgresql://{{username}}:{{password}}@db:5432/app?sslpassword=REDACTED" {
		t.Errorf("connectionURL = %q", got)
	}
	note := "DatabaseConnection/database-pg: spec.postgresql.connectionURL held credentials in a URL, emitted as " +
		"REDACTED: set the real value before managementPolicy Manage"
	if !strings.Contains(strings.Join(result.Notes, "\n"), note) {
		t.Errorf("notes = %q", result.Notes)
	}
	placeholders := make([]string, 0, len(result.Placeholders))
	for _, p := range result.Placeholders {
		placeholders = append(placeholders, p.Object+" "+p.Field)
	}
	for _, want := range []string{"Audit/siem spec.optionsSecretRefs.headers", "Audit/siem spec.optionsSecretRefs.uri"} {
		if !strings.Contains(strings.Join(placeholders, "\n"), want) {
			t.Errorf("placeholders = %q, want %s", placeholders, want)
		}
	}
}
