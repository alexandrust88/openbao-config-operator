package examples

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestConnectionCredentialCEL checks the CRD rules that keep login paths clean and credentials out of URLs.
func TestConnectionCredentialCEL(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	k8s := secretsStartEnv(t, root, []string{
		"core.obco.dev_baoconnections.yaml",
		"core.obco.dev_clusterbaoconnections.yaml",
		"core.obco.dev_baoservers.yaml",
		"secrets.obco.dev_databaseconnections.yaml",
		"secrets.obco.dev_nomadsecretsconfigs.yaml",
		"secrets.obco.dev_ldapsecretsconfigs.yaml",
	})
	ctx := context.Background()
	const (
		core    = "apiVersion: core.obco.dev/v1alpha1\n"
		secrets = "apiVersion: secrets.obco.dev/v1alpha1\n"
		meta    = "metadata: {name: x, namespace: default}\n"
		dbBase  = secrets + "kind: DatabaseConnection\n" + meta + "spec: {connectionRef: {name: openbao}, mountPath: database, name: pg, "
		db      = dbBase + "pluginName: postgresql-database-plugin, "
		valkey  = dbBase + "pluginName: valkey-database-plugin, "
		nomad   = secrets + "kind: NomadSecretsConfig\n" + meta +
			"spec: {connectionRef: {name: openbao}, mountPath: nomad, tokenSecretRef: {name: nomad-token, key: token}, "
		ldap = secrets + "kind: LDAPSecretsConfig\n" + meta +
			"spec: {connectionRef: {name: openbao}, mountPath: ldap, bindDN: cn=admin, bindPassSecretRef: {name: l, key: p}, "
		errMountPath = "spec.auth."
		errLiteral   = "must not contain a literal password"
		errUserinfo  = "must not contain credentials"
	)
	tests := []struct {
		name    string
		doc     string
		wantErr string
	}{
		{"connection mount traversal", core + "kind: BaoConnection\n" + meta +
			"spec: {serverRef: {name: s}, auth: {kubernetes: {mountPath: ../sys, role: r, serviceAccountRef: {name: sa}, audiences: [openbao]}}}", errMountPath},
		{"connection mount nested", core + "kind: BaoConnection\n" + meta +
			"spec: {serverRef: {name: s}, auth: {jwt: {mountPath: team/jwt-k8s, role: r, serviceAccountRef: {name: sa}, audiences: [openbao]}}}", ""},
		{"cluster connection empty segment", core + "kind: ClusterBaoConnection\nmetadata: {name: x}\n" +
			"spec: {serverRef: {name: s}, auth: {appRole: {mountPath: a//b, roleIDSecretRef: {name: r, namespace: p, key: id}, secretIDSecretRef: {name: r, namespace: p, key: sid}}}}", errMountPath},
		{"server address userinfo", core + "kind: BaoServer\nmetadata: {name: x}\n" +
			"spec: {address: 'https://user:pass@bao.example:8200'}", "spec.address"},
		{"server address", core + "kind: BaoServer\nmetadata: {name: plain}\n" +
			"spec: {address: 'https://bao.example:8200'}", ""},
		{"database literal password", db + "postgresql: {connectionURL: 'postgresql://root:topsecret@pg:5432/app'}}", errLiteral},
		{"database literal password in dsn", db + "postgresql: {connectionURL: 'root:topsecret@tcp(pg:3306)/'}}", errLiteral},
		{"database templated", db + "postgresql: {connectionURL: 'postgresql://{{username}}:{{password}}@pg:5432/app'}}", ""},
		{"database username only", db + "postgresql: {connectionURL: 'postgresql://{{username}}@pg:5432/app'}}", ""},
		{"valkey literal password", valkey + "valkey: {connectionURL: 'valkeys://default:topsecret@vk:6379'}}", errLiteral},
		{"nomad userinfo", nomad + "address: 'https://user:pass@nomad.example:4646'}", errUserinfo},
		{"nomad token as user", nomad + "address: 'https://token@nomad.example:4646'}", errUserinfo},
		{"nomad", nomad + "address: 'https://nomad.example:4646'}", ""},
		{"ldap second url userinfo", ldap + "url: 'ldaps://a.example,ldaps://cn=x:pw@b.example'}", errUserinfo},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := k8s.Create(ctx, secretsDecodeString(t, tt.doc), client.DryRunAll)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("want accepted, got %v", err)
				}
				return
			}
			if !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("got %v, want invalid with %q", err, tt.wantErr)
			}
		})
	}
}
