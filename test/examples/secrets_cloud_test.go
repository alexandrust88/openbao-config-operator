package examples

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var secretsCloudKinds = []string{
	"awssecretsconfig", "awssecretsrole", "azuresecretsconfig", "azuresecretsrole",
	"consulsecretsconfig", "consulsecretsrole",
	"gcpsecretsconfig", "gcproleset", "gcpstaticaccount", "gcpimpersonatedaccount",
}

const (
	secretsCloudNamespace = "openbao-config"
	secretsCloudOrphanErr = "deletionPolicy must be Orphan"
)

// secretsCloudDoc renders a minimal object of kind whose spec holds the connectionRef plus spec.
func secretsCloudDoc(kind, spec string) string {
	return "apiVersion: secrets.obco.dev/v1alpha1\nkind: " + kind + "\nmetadata: {name: x, namespace: " +
		secretsCloudNamespace + "}\nspec: {connectionRef: {name: openbao}, " + spec + "}"
}

// TestSecretsCloudExamples applies the AWS, Azure, Consul and GCP secrets engine examples with a server-side dry run
// and checks the CEL rules of those kinds.
func TestSecretsCloudExamples(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	crds := make([]string, 0, len(secretsCloudKinds))
	for _, kind := range secretsCloudKinds {
		crds = append(crds, "secrets.obco.dev_"+kind+"s.yaml")
	}
	k8s := secretsStartEnv(t, root, crds)
	ctx := context.Background()
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: secretsCloudNamespace}}
	if err := k8s.Create(ctx, namespace); err != nil {
		t.Fatal(err)
	}

	for _, kind := range secretsCloudKinds {
		t.Run(kind, func(t *testing.T) {
			for _, obj := range secretsDecode(t, filepath.Join(root, "examples", "secrets", kind+".yaml")) {
				if err := k8s.Create(ctx, obj, client.DryRunAll, client.FieldValidation("Strict")); err != nil {
					t.Errorf("%s %s: %v", obj.GetKind(), obj.GetName(), err)
				}
			}
		})
	}

	const (
		awsConfig    = "AWSSecretsConfig"
		awsRole      = "AWSSecretsRole"
		consulConfig = "ConsulSecretsConfig"
		gcpRoleset   = "GCPRoleset"
		gcpStatic    = "GCPStaticAccount"
		fieldName    = "name"
		bindingsP    = "bindings: [{resource: projects/p, roles: [roles/viewer]}]"
		tokenRef     = "address: c:8500, tokenSecretRef: {name: s, key: k}"
		staticEmail  = "serviceAccountEmail: sa@p.iam.gserviceaccount.com"
	)
	t.Run("validation", func(t *testing.T) {
		for _, tt := range []struct{ name, kind, spec, wantErr string }{
			{"aws config delete policy", awsConfig, "deletionPolicy: Delete", secretsCloudOrphanErr},
			{"aws config half credentials", awsConfig, "secretKeySecretRef: {name: s, key: k}",
				"set both or neither of accessKeySecretRef"},
			{"aws config lease without max", awsConfig, "lease: 1h", "set both or neither of lease and leaseMax"},
			{"aws role arns on iam user", awsRole, "credentialType: iam_user, roleARNs: [arn]",
				"roleARNs requires credentialType assumed_role"},
			{"aws role user path on assumed role", awsRole, "credentialType: assumed_role, userPath: /x/",
				"require credentialType iam_user"},
			{"aws role sts ttl on iam user", awsRole, "credentialType: iam_user, defaultSTSTTL: 1h",
				"require credentialType assumed_role or federation_token"},
			{"aws role bad credential type", awsRole, "credentialType: root", "spec.credentialType"},
			{"azure config half credentials", "AzureSecretsConfig",
				"subscriptionID: s, tenantID: t, clientIDSecretRef: {name: s, key: k}", "set both or neither of clientIDSecretRef"},
			{"azure config delete allowed", "AzureSecretsConfig", "subscriptionID: s, tenantID: t, deletionPolicy: Delete", ""},
			{"azure role empty", "AzureSecretsRole", "ttl: 1h", "set azureRoles, azureGroups or applicationObjectID"},
			{"azure role assignment without role", "AzureSecretsRole", "azureRoles: [{scope: /subscriptions/x}]",
				"set roleName or roleID"},
			{"consul config delete policy", consulConfig, tokenRef + ", deletionPolicy: Delete", secretsCloudOrphanErr},
			{"consul config cert without key", consulConfig, tokenRef + ", clientCert: pem",
				"set clientCert and clientKeySecretRef together"},
			{"consul config without token", consulConfig, "address: c:8500", "spec.tokenSecretRef"},
			{"consul role empty", "ConsulSecretsRole", "ttl: 1h", "set at least one of consulPolicies"},
			{"gcp config delete policy", "GCPSecretsConfig", "deletionPolicy: Delete", secretsCloudOrphanErr},
			{"gcp roleset access token without scopes", gcpRoleset, "project: p, " + bindingsP, "tokenScopes is required"},
			{"gcp roleset key with scopes", gcpRoleset,
				"project: p, secretType: service_account_key, tokenScopes: [s], " + bindingsP, "tokenScopes is required"},
			{"gcp roleset key", gcpRoleset, "project: p, secretType: service_account_key, " + bindingsP, ""},
			{"gcp roleset duplicate resource", "GCPRoleset",
				"project: p, tokenScopes: [s], bindings: [{resource: projects/p, roles: [a]}, {resource: projects/p, roles: [b]}]",
				"Duplicate value"},
			{"gcp roleset without bindings", gcpRoleset, "project: p, tokenScopes: [s]", "spec.bindings"},
			{"gcp static key without bindings", gcpStatic, staticEmail + ", secretType: service_account_key", ""},
			{"gcp static empty bindings", gcpStatic, staticEmail + ", secretType: service_account_key, bindings: []",
				"spec.bindings"},
			{"gcp impersonated without scopes", "GCPImpersonatedAccount", staticEmail, "spec.tokenScopes"},
		} {
			err := k8s.Create(ctx, secretsDecodeString(t, secretsCloudDoc(tt.kind, tt.spec)), client.DryRunAll)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("%s: %v", tt.name, err)
			case tt.wantErr != "" && (!apierrors.IsInvalid(err) || !strings.Contains(err.Error(), tt.wantErr)):
				t.Errorf("%s: got %v, want invalid with %q", tt.name, err, tt.wantErr)
			}
		}
	})

	const roleset = "project: p, secretType: access_token, tokenScopes: [s], " +
		"bindings: [{resource: projects/p, roles: [a]}]"
	t.Run("immutable", func(t *testing.T) {
		for i, tt := range []struct {
			name, kind, spec, field string
			value                   any
		}{
			{"aws role mount", awsRole, "credentialType: iam_user", "mountPath", "aws2"},
			{"consul role name", "ConsulSecretsRole", "name: app, consulPolicies: [p]", fieldName, "app2"},
			{"gcp roleset secret type", gcpRoleset, roleset, "secretType", "service_account_key"},
			{"gcp roleset project", gcpRoleset, roleset, "project", "q"},
			{"gcp static email", gcpStatic, "serviceAccountEmail: a@p.iam.gserviceaccount.com, tokenScopes: [s]",
				"serviceAccountEmail", "b@p.iam.gserviceaccount.com"},
			{"gcp config mount", "GCPSecretsConfig", "driftDetection: Enabled", "mountPath", "gcp2"},
		} {
			obj := secretsDecodeString(t, secretsCloudDoc(tt.kind, tt.spec))
			obj.SetName(fmt.Sprintf("immutable-%d", i))
			if err := k8s.Create(ctx, obj); err != nil {
				t.Fatalf("%s: create: %v", tt.name, err)
			}
			if err := unstructured.SetNestedField(obj.Object, tt.value, "spec", tt.field); err != nil {
				t.Fatal(err)
			}
			wantErr := tt.field + " is immutable"
			if err := k8s.Update(ctx, obj); !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), wantErr) {
				t.Errorf("%s: got %v, want invalid with %q", tt.name, err, wantErr)
			}
		}
	})
}
