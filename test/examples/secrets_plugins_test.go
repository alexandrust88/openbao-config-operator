package examples

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var secretsPluginKinds = []string{
	"githubsecretsconfig", "githubsecretsrole",
	"ldapsecretsconfig", "ldapsecretsdynamicrole", "ldapsecretsstaticrole",
	"mongodbatlassecretsconfig", "mongodbatlassecretsrole",
	"nomadsecretsconfig", "nomadsecretsrole",
	"quaysecretsconfig", "quaysecretsrole", "quaysecretsstaticrole",
	"terraformcloudsecretsconfig", "terraformcloudsecretsrole",
}

// TestSecretsPluginExamples applies the examples of the external-plugin secrets kinds with a server-side dry run
// and checks their CEL rules against envtest.
func TestSecretsPluginExamples(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	crds := make([]string, 0, len(secretsPluginKinds))
	for _, kind := range secretsPluginKinds {
		crds = append(crds, "secrets.obco.dev_"+kind+"s.yaml")
	}
	k8s := secretsStartEnv(t, root, crds)
	ctx := context.Background()
	const ns = "obco-examples"
	if err := k8s.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}); err != nil {
		t.Fatal(err)
	}

	for _, kind := range secretsPluginKinds {
		t.Run(kind, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(root, "examples", "secrets", kind+".yaml"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "PRIVATE KEY") {
				t.Error("example carries a PEM private key block, which secret scanners flag")
			}
			for _, obj := range secretsDecode(t, filepath.Join(root, "examples", "secrets", kind+".yaml")) {
				obj.SetNamespace(ns)
				if err := k8s.Create(ctx, obj, client.DryRunAll, client.FieldValidation("Strict")); err != nil {
					t.Errorf("%s %s: %v", obj.GetKind(), obj.GetName(), err)
				}
			}
		})
	}

	const head = "apiVersion: secrets.obco.dev/v1alpha1\nmetadata: {name: x}\n"
	t.Run("validation", func(t *testing.T) {
		for _, tt := range []struct {
			name, doc, wantErr string
		}{
			{name: "config delete policy", wantErr: "deletionPolicy must be Orphan",
				doc: "kind: QuaySecretsConfig\nspec: {" +
					"connectionRef: {name: openbao}, url: https://quay, " +
					"tokenSecretRef: {name: s, key: k}, deletionPolicy: Delete}"},
			{name: "role delete policy", doc: "kind: QuaySecretsStaticRole\nspec: {" +
				"connectionRef: {name: openbao}, namespaceName: org, " +
				"deletionPolicy: Delete}"},
			{name: "ldap client cert without key", wantErr: "set clientTLSCert and clientTLSKeySecretRef together",
				doc: "kind: LDAPSecretsConfig\nspec: {" +
					"connectionRef: {name: openbao}, bindDN: cn=x, " +
					"bindPassSecretRef: {name: s, key: k}, clientTLSCert: pem}"},
			{name: "nomad client cert without key", wantErr: "set clientCert and clientKeySecretRef together",
				doc: "kind: NomadSecretsConfig\nspec: {" +
					"connectionRef: {name: openbao}, address: https://nomad, " +
					"tokenSecretRef: {name: s, key: k}, clientCert: pem}"},
			{name: "nomad config without token", wantErr: "spec.tokenSecretRef",
				doc: "kind: NomadSecretsConfig\nspec: {connectionRef: {name: openbao}, address: https://nomad}"},
			{name: "ldap uppercase url", wantErr: "url must be lowercase",
				doc: "kind: LDAPSecretsConfig\nspec: {" +
					"connectionRef: {name: openbao}, bindDN: cn=x, " +
					"bindPassSecretRef: {name: s, key: k}, url: ldaps://LDAP.example.org}"},
			{name: "ldap tls max below min", wantErr: "tlsMaxVersion must not be lower than tlsMinVersion",
				doc: "kind: LDAPSecretsConfig\nspec: {" +
					"connectionRef: {name: openbao}, bindDN: cn=x, " +
					"bindPassSecretRef: {name: s, key: k}, tlsMinVersion: tls13}"},
			{name: "nomad client role without policies", wantErr: "client roles need policies",
				doc: "kind: NomadSecretsRole\nspec: {connectionRef: {name: openbao}}"},
			{name: "nomad management role with policies", wantErr: "management roles take none",
				doc: "kind: NomadSecretsRole\nspec: {connectionRef: {name: openbao}, type: management, policies: [p]}"},
			{name: "nomad management role", doc: "kind: NomadSecretsRole\nspec: {" +
				"connectionRef: {name: openbao}, type: management}"},
			{name: "github role without installation", wantErr: "set installationID or organizationName",
				doc: "kind: GitHubSecretsRole\nspec: {connectionRef: {name: openbao}}"},
			{name: "atlas role without scope", wantErr: "set organizationID or projectID",
				doc: "kind: MongoDBAtlasSecretsRole\nspec: {connectionRef: {name: openbao}, roles: [ORG_MEMBER]}"},
			{name: "atlas project roles without both ids", wantErr: "set projectRoles exactly when both organizationID and projectID are set",
				doc: "kind: MongoDBAtlasSecretsRole\nspec: {" +
					"connectionRef: {name: openbao}, projectID: p, roles: [GROUP_OWNER], " +
					"projectRoles: [GROUP_READ_ONLY]}"},
			{name: "atlas both ids without project roles", wantErr: "set projectRoles exactly when both",
				doc: "kind: MongoDBAtlasSecretsRole\nspec: {" +
					"connectionRef: {name: openbao}, organizationID: o, projectID: p, roles: [ORG_MEMBER]}"},
			{name: "terraform organization and user", wantErr: "userID cannot be combined with organization or teamID",
				doc: "kind: TerraformCloudSecretsRole\nspec: {" +
					"connectionRef: {name: openbao}, organization: o, userID: u}"},
			{name: "terraform without owner", wantErr: "set organization, teamID or userID",
				doc: "kind: TerraformCloudSecretsRole\nspec: {connectionRef: {name: openbao}}"},
			{name: "terraform legacy team with ttl", wantErr: "team_legacy roles take no description, ttl or maxTTL",
				doc: "kind: TerraformCloudSecretsRole\nspec: {" +
					"connectionRef: {name: openbao}, teamID: t, credentialType: team_legacy, ttl: 1h}"},
			{name: "uppercase role name", wantErr: "spec.name",
				doc: "kind: MongoDBAtlasSecretsRole\nspec: {" +
					"connectionRef: {name: openbao}, name: Reader, projectID: p, roles: [GROUP_READ_ONLY]}"},
			{name: "terraform team and user", wantErr: "userID cannot be combined with organization or teamID",
				doc: "kind: TerraformCloudSecretsRole\nspec: {" +
					"connectionRef: {name: openbao}, organization: o, teamID: t, userID: u}"},
			{name: "quay bad permission", wantErr: "spec.repositories",
				doc: "kind: QuaySecretsRole\nspec: {" +
					"connectionRef: {name: openbao}, namespaceName: org, " +
					"repositories: {r: owner}}"},
			{name: "ldap bad duration", wantErr: "spec.rotationPeriod",
				doc: "kind: LDAPSecretsStaticRole\nspec: {" +
					"connectionRef: {name: openbao}, username: u, rotationPeriod: daily}"},
			{name: "slash in role name", wantErr: "spec.name",
				doc: "kind: TerraformCloudSecretsRole\nspec: {" +
					"connectionRef: {name: openbao}, name: a/b, organization: o}"},
		} {
			obj := secretsDecodeString(t, head+tt.doc)
			obj.SetNamespace(ns)
			err := k8s.Create(ctx, obj, client.DryRunAll)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("%s: %v", tt.name, err)
			case tt.wantErr != "" && (!apierrors.IsInvalid(err) || !strings.Contains(err.Error(), tt.wantErr)):
				t.Errorf("%s: got %v, want invalid with %q", tt.name, err, tt.wantErr)
			}
		}
	})

	t.Run("defaults and immutable", func(t *testing.T) {
		obj := secretsDecodeString(t, head+"kind: LDAPSecretsStaticRole\nspec: {"+
			"connectionRef: {name: openbao}, name: svc, username: svc, "+
			"rotationPeriod: 24h}")
		obj.SetNamespace(ns)
		obj.SetName("immutable")
		if err := k8s.Create(ctx, obj); err != nil {
			t.Fatal(err)
		}
		if mount, _, _ := unstructured.NestedString(obj.Object, "spec", "mountPath"); mount != "ldap" {
			t.Errorf("default mountPath = %q, want ldap", mount)
		}
		for field, value := range map[string]any{"mountPath": "other", "name": "other", "username": "other", "dn": "cn=other"} {
			changed := obj.DeepCopy()
			if err := unstructured.SetNestedField(changed.Object, value, "spec", field); err != nil {
				t.Fatal(err)
			}
			err := k8s.Update(ctx, changed)
			if !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), field+" is immutable") {
				t.Errorf("spec.%s: got %v, want immutable", field, err)
			}
		}
		changed := obj.DeepCopy()
		if err := unstructured.SetNestedField(changed.Object, "48h", "spec", "rotationPeriod"); err != nil {
			t.Fatal(err)
		}
		if err := k8s.Update(ctx, changed); err != nil {
			t.Errorf("mutable rotationPeriod: %v", err)
		}

		role := secretsDecodeString(t, head+"kind: TerraformCloudSecretsRole\nspec: {"+
			"connectionRef: {name: openbao}, organization: o, "+
			"credentialType: organization}")
		role.SetNamespace(ns)
		role.SetName("credential-type")
		if err := k8s.Create(ctx, role); err != nil {
			t.Fatal(err)
		}
		if err := unstructured.SetNestedField(role.Object, "team", "spec", "credentialType"); err != nil {
			t.Fatal(err)
		}
		err := k8s.Update(ctx, role)
		if !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), "credentialType is immutable") {
			t.Errorf("spec.credentialType: got %v, want immutable", err)
		}
	})
}
