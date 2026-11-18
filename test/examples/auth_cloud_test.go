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

const (
	authCloudNamespace = "openbao-config"
	authCloudOrphanErr = "deletionPolicy must be Orphan"
	authCloudMountPath = "mountPath"
	authCloudName      = "name"
	authCloudType      = "type"
	authCloudAWSRole   = "AWSAuthRole"
	authCloudGCPRole   = "GCPAuthRole"
)

var authCloudKinds = []string{
	"awsauthconfig", "awsauthidentityconfig", "awsauthcertificate", "awsauthstsrole", "awsauthtidyconfig", "awsauthrole",
	"azureauthconfig", "azureauthrole", "gcpauthconfig", "gcpauthrole", "ociauthconfig", "ociauthrole",
	"cfauthconfig", "cfauthrole",
}

// TestAuthCloudExamples applies the cloud auth method examples with a server-side dry run and checks their CEL rules.
func TestAuthCloudExamples(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	crds := make([]string, 0, len(authCloudKinds))
	for _, kind := range authCloudKinds {
		crds = append(crds, "auth.obco.dev_"+kind+"s.yaml")
	}
	k8s := secretsStartEnv(t, root, crds)
	ctx := context.Background()
	if err := k8s.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: authCloudNamespace}}); err != nil {
		t.Fatal(err)
	}

	for _, kind := range authCloudKinds {
		t.Run(kind, func(t *testing.T) {
			for _, obj := range secretsDecode(t, filepath.Join(root, "examples", "auth", kind+".yaml")) {
				if err := k8s.Create(ctx, obj, client.DryRunAll); err != nil {
					t.Errorf("%s %s: %v", obj.GetKind(), obj.GetName(), err)
				}
			}
		})
	}

	t.Run("validation", func(t *testing.T) {
		for _, tt := range []struct{ name, kind, spec, wantErr string }{
			{
				name: "config delete policy", kind: "OCIAuthConfig", wantErr: authCloudOrphanErr,
				spec: "homeTenancyID: t, deletionPolicy: Delete",
			},
			{
				name: "aws sts region without endpoint", kind: "AWSAuthConfig", wantErr: "stsRegion requires stsEndpoint",
				spec: "stsRegion: eu-central-1",
			},
			{
				name: "aws metadata default", kind: "AWSAuthIdentityConfig", wantErr: "instead of default",
				spec: "ec2Metadata: [default]",
			},
			{
				name: "gcp metadata default", kind: "GCPAuthConfig", wantErr: "instead of default",
				spec: "iamMetadata: [default]",
			},
			{
				name: "aws half credentials", kind: "AWSAuthConfig", wantErr: "set both or neither of accessKeySecretRef",
				spec: "accessKeySecretRef: {name: s, key: k}",
			},
			{
				name: "aws ec2 field on iam role", kind: authCloudAWSRole, wantErr: "apply only to authType ec2",
				spec: "roleTag: VaultRole",
			},
			{
				name: "aws iam field on ec2 role", kind: authCloudAWSRole, wantErr: "apply only to authType iam",
				spec: "authType: ec2, boundIAMPrincipalARNs: [arn]",
			},
			{
				name: "aws ec2 bound on plain iam role", kind: authCloudAWSRole, wantErr: "EC2 bound fields",
				spec: "boundRegions: [eu-central-1]",
			},
			{
				name: "aws inferred without region", kind: authCloudAWSRole, wantErr: "inferredEntityType and inferredAWSRegion",
				spec: "inferredEntityType: ec2_instance",
			},
			{
				name: "aws sts account", kind: "AWSAuthSTSRole", wantErr: "spec.accountID",
				spec: `accountID: "12", stsRole: "arn:aws:iam::1:role/x"`,
			},
			{
				name: "aws tidy empty", kind: "AWSAuthTidyConfig", wantErr: "set identityAccessList, roleTagDenyList or both",
				spec: "",
			},
			{
				name: "aws certificate not pem", kind: "AWSAuthCertificate", wantErr: "spec.awsPublicCert",
				spec: "awsPublicCert: MIIBbase64",
			},
			{
				name: "azure secret without client id", kind: "AzureAuthConfig", wantErr: "clientSecretSecretRef requires clientID",
				spec: "tenantID: t, resource: r, clientSecretSecretRef: {name: s, key: k}",
			},
			{
				name: "gcp iam without service accounts", kind: authCloudGCPRole,
				wantErr: "an iam role requires boundServiceAccounts",
				spec:    "type: iam",
			},
			{
				name: "gcp gce field on iam role", kind: authCloudGCPRole, wantErr: "apply only to type gce",
				spec: `type: iam, boundServiceAccounts: ["*"], boundZones: [z]`,
			},
			{
				name: "gcp iam field on gce role", kind: authCloudGCPRole, wantErr: "apply only to type iam",
				spec: "type: gce, maxJWTExp: 15m",
			},
			{
				name: "cf password without user", kind: "CFAuthConfig", wantErr: "cfUsername and cfPasswordSecretRef",
				spec: "identityCACertificates: [c], cfAPIAddr: a, cfPasswordSecretRef: {name: s, key: k}",
			},
			{
				name: "cf mtls key without certificate", kind: "CFAuthConfig",
				wantErr: "cfAPIMutualTLSCertificate and cfAPIMutualTLSKeySecretRef",
				spec:    "identityCACertificates: [c], cfAPIAddr: a, cfAPIMutualTLSKeySecretRef: {name: s, key: k}",
			},
		} {
			err := k8s.Create(ctx, secretsDecodeString(t, authCloudDoc("name: x", tt.kind, tt.spec)), client.DryRunAll)
			if !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("%s: got %v, want invalid with %q", tt.name, err, tt.wantErr)
			}
		}
	})

	t.Run("immutable", func(t *testing.T) {
		for _, tt := range []struct {
			kind, spec, field string
			value             any
		}{
			{kind: authCloudAWSRole, spec: "boundIAMPrincipalARNs: [arn]", field: authCloudMountPath, value: "aws2"},
			{kind: authCloudAWSRole, spec: "name: r, boundIAMPrincipalARNs: [arn]", field: authCloudName, value: "r2"},
			{kind: authCloudAWSRole, spec: "", field: "authType", value: "ec2"},
			{
				kind: "AWSAuthSTSRole", spec: `accountID: "123456789012", stsRole: "arn:aws:iam::1:role/x"`,
				field: "accountID", value: "210987654321",
			},
			{kind: authCloudGCPRole, spec: "type: gce", field: authCloudType, value: "iam"},
		} {
			obj := secretsDecodeString(t, authCloudDoc("generateName: imm-", tt.kind, tt.spec))
			if err := k8s.Create(ctx, obj); err != nil {
				t.Fatalf("%s: %v", obj.GetKind(), err)
			}
			changed := obj.DeepCopy()
			if err := unstructured.SetNestedField(changed.Object, tt.value, "spec", tt.field); err != nil {
				t.Fatal(err)
			}
			err := k8s.Update(ctx, changed)
			if !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), tt.field+" is immutable") {
				t.Errorf("%s spec.%s: got %v, want immutable", obj.GetKind(), tt.field, err)
			}
		}

		role := secretsDecodeString(t, authCloudDoc("name: resolve", authCloudAWSRole, "boundIAMPrincipalARNs: [arn]"))
		if err := k8s.Create(ctx, role); err != nil {
			t.Fatal(err)
		}
		changed := role.DeepCopy()
		if err := unstructured.SetNestedField(changed.Object, false, "spec", "resolveAWSUniqueIDs"); err != nil {
			t.Fatal(err)
		}
		err := k8s.Update(ctx, changed)
		if !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), "cannot change from true to false") {
			t.Errorf("resolveAWSUniqueIDs true -> false: got %v", err)
		}
	})
}

// authCloudDoc renders a cloud auth kind in openbao-config with the given metadata entry and extra spec fields.
func authCloudDoc(meta, kind, spec string) string {
	if spec != "" {
		spec = ", " + spec
	}
	return "apiVersion: auth.obco.dev/v1alpha1\nmetadata: {" + meta + ", namespace: " + authCloudNamespace +
		"}\nkind: " + kind + "\nspec: {connectionRef: {name: openbao}" + spec + "}\n"
}
