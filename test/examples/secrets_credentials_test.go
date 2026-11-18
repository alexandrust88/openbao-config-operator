package examples

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestSecretsCredentialExamples applies the database, RabbitMQ and Kubernetes secrets engine samples with a
// server-side dry run and checks their CEL rules against envtest.
func TestSecretsCredentialExamples(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	k8s := secretsStartEnv(t, root, []string{
		"secrets.obco.dev_databaseconnections.yaml",
		"secrets.obco.dev_databaseroles.yaml",
		"secrets.obco.dev_databasestaticroles.yaml",
		"secrets.obco.dev_rabbitmqconfigs.yaml",
		"secrets.obco.dev_rabbitmqroles.yaml",
		"secrets.obco.dev_kubernetessecretsconfigs.yaml",
		"secrets.obco.dev_kubernetessecretsroles.yaml",
	})
	ctx := context.Background()
	if err := k8s.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "openbao-config"}}); err != nil {
		t.Fatal(err)
	}

	for _, file := range []string{"databaseconnection.yaml", "databaserole.yaml", "databasestaticrole.yaml", "rabbitmqconfig.yaml",
		"rabbitmqrole.yaml", "kubernetessecretsconfig.yaml", "kubernetessecretsrole.yaml"} {
		t.Run(file, func(t *testing.T) {
			for _, obj := range secretsDecode(t, filepath.Join(root, "examples", "secrets", file)) {
				if err := k8s.Create(ctx, obj, client.DryRunAll, client.FieldValidation("Strict")); err != nil {
					t.Errorf("%s %s: %v", obj.GetKind(), obj.GetName(), err)
				}
			}
		})
	}

	const head = "apiVersion: secrets.obco.dev/v1alpha1\nmetadata: {name: x, namespace: openbao-config}\n"
	const pg = "connectionRef: {name: openbao}, mountPath: db, pluginName: postgresql-database-plugin"
	t.Run("validation", func(t *testing.T) {
		for _, tt := range []struct {
			name, doc, wantErr string
		}{
			{name: "plugin section mismatch", wantErr: "mysql requires a mysql pluginName",
				doc: "kind: DatabaseConnection\nspec: {" + pg + ", mysql: {connectionURL: x}}"},
			{name: "redis plugin with valkey section",
				doc: "kind: DatabaseConnection\nspec: {connectionRef: {name: openbao}, mountPath: db, pluginName: redis-database-plugin, valkey: {connectionURL: redis://r:6379}}"},
			{name: "secret in pluginConfig", wantErr: "pluginConfig must not set top-level fields or secret material",
				doc: "kind: DatabaseConnection\nspec: {" + pg + ", pluginConfig: {password: x}}"},
			{name: "rotate root without credentials", wantErr: "rotateRootOnCreate requires passwordSecretRef or privateKeySecretRef",
				doc: "kind: DatabaseConnection\nspec: {" + pg + ", rotateRootOnCreate: true}"},
			{name: "valkey url and host", wantErr: "set either connectionURL or host, port and username",
				doc: "kind: DatabaseConnection\nspec: {connectionRef: {name: openbao}, mountPath: db, pluginName: valkey-database-plugin, valkey: {connectionURL: x, host: h}}"},
			{name: "cassandra two pem refs", wantErr: "set at most one of pemBundleSecretRef and pemJSONSecretRef",
				doc: "kind: DatabaseConnection\nspec: {connectionRef: {name: openbao}, mountPath: db, pluginName: cassandra-database-plugin, " +
					"cassandra: {hosts: [c], pemBundleSecretRef: {name: a, key: b}, pemJSONSecretRef: {name: a, key: c}}}"},
			{name: "builtin plugin version", wantErr: "spec.pluginVersion",
				doc: "kind: DatabaseConnection\nspec: {" + pg + ", pluginVersion: v2.7.2+builtin}"},
			{name: "plugin version without v", wantErr: "spec.pluginVersion",
				doc: "kind: DatabaseConnection\nspec: {" + pg + ", pluginVersion: 1.2.3}"},
			{name: "role ca key in credentialConfig", wantErr: "credentialConfig must not set ca_private_key",
				doc: "kind: DatabaseRole\nspec: {connectionRef: {name: openbao}, mountPath: db, dbName: pg, credentialType: client_certificate, credentialConfig: {ca_private_key: x}}"},
			{name: "static role ca key without client certificate", wantErr: "caPrivateKeySecretRef requires credentialType client_certificate",
				doc: "kind: DatabaseStaticRole\nspec: {connectionRef: {name: openbao}, mountPath: db, dbName: pg, username: app, rotationPeriod: 1h, caPrivateKeySecretRef: {name: a, key: b}}"},
			{name: "role ca key with client certificate",
				doc: "kind: DatabaseRole\nspec: {connectionRef: {name: openbao}, mountPath: db, dbName: pg, credentialType: client_certificate, credentialConfig: {ca_cert: x}, caPrivateKeySecretRef: {name: a, key: b}}"},
			{name: "static role short rotation", wantErr: "rotationPeriod must be at least 5s",
				doc: "kind: DatabaseStaticRole\nspec: {connectionRef: {name: openbao}, mountPath: db, dbName: pg, username: app, rotationPeriod: 2s}"},
			{name: "rabbitmq config delete", wantErr: "deletionPolicy must be Orphan",
				doc: "kind: RabbitMQConfig\nspec: {connectionRef: {name: openbao}, mountPath: r, connectionURI: http://r, username: a, " +
					"passwordSecretRef: {name: a, key: b}, deletionPolicy: Delete}"},
			{name: "rabbitmq role without tags or vhosts", wantErr: "set tags or vhosts",
				doc: "kind: RabbitMQRole\nspec: {connectionRef: {name: openbao}, mountPath: r}"},
			{name: "kubernetes role two modes", wantErr: "set exactly one of serviceAccountName, kubernetesRoleName and generatedRoleRules",
				doc: "kind: KubernetesSecretsRole\nspec: {connectionRef: {name: openbao}, mountPath: k, allowedKubernetesNamespaces: [a], serviceAccountName: s, kubernetesRoleName: r}"},
			{name: "kubernetes role without namespaces", wantErr: "set allowedKubernetesNamespaces or allowedKubernetesNamespaceSelector",
				doc: "kind: KubernetesSecretsRole\nspec: {connectionRef: {name: openbao}, mountPath: k, serviceAccountName: s}"},
			{name: "kubernetes role ttl above max", wantErr: "tokenDefaultTTL must not exceed tokenMaxTTL",
				doc: "kind: KubernetesSecretsRole\nspec: {connectionRef: {name: openbao}, mountPath: k, allowedKubernetesNamespaces: [a], serviceAccountName: s, tokenDefaultTTL: 2h, tokenMaxTTL: 1h}"},
			{name: "kubernetes role uppercase name", wantErr: "spec.name",
				doc: "kind: KubernetesSecretsRole\nspec: {connectionRef: {name: openbao}, mountPath: k, name: App, allowedKubernetesNamespaces: [a], serviceAccountName: s}"},
		} {
			t.Run(tt.name, func(t *testing.T) {
				err := k8s.Create(ctx, secretsDecodeString(t, head+tt.doc), client.DryRunAll)
				if tt.wantErr == "" {
					if err != nil {
						t.Errorf("rejected: %v", err)
					}
					return
				}
				if !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("got %v, want %q", err, tt.wantErr)
				}
			})
		}
	})

	t.Run("immutable", func(t *testing.T) {
		for _, tt := range []struct {
			doc     string
			changes map[string]any
		}{
			{doc: "kind: DatabaseConnection\nspec: {" + pg + ", name: a, pluginConfig: {connection_url: x}}",
				changes: map[string]any{"mountPath": "other", "name": "b", "pluginName": "custom-database-plugin"}},
			{doc: "kind: DatabaseStaticRole\nspec: {connectionRef: {name: openbao}, mountPath: db, dbName: pg, username: app, rotationPeriod: 1h}",
				changes: map[string]any{"mountPath": "other", "username": "other"}},
			{doc: "kind: KubernetesSecretsConfig\nspec: {connectionRef: {name: openbao}, mountPath: k}",
				changes: map[string]any{"mountPath": "other"}},
		} {
			obj := secretsDecodeString(t, head+tt.doc)
			kind := obj.GetKind()
			if err := k8s.Create(ctx, obj); err != nil {
				t.Fatalf("%s: %v", kind, err)
			}
			for field, value := range tt.changes {
				changed := &unstructured.Unstructured{}
				changed.SetGroupVersionKind(obj.GroupVersionKind())
				if err := k8s.Get(ctx, client.ObjectKeyFromObject(obj), changed); err != nil {
					t.Fatal(err)
				}
				if err := unstructured.SetNestedField(changed.Object, value, "spec", field); err != nil {
					t.Fatal(err)
				}
				if err := k8s.Update(ctx, changed); !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), field+" is immutable") {
					t.Errorf("%s spec.%s: got %v, want immutable", kind, field, err)
				}
			}
		}
	})
}
