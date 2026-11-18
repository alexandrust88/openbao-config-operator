package examples

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestAuthCredentialExamples installs the AppRole, token role, cert and userpass CRDs into envtest (a CRD the
// apiserver rejects, for example for CEL cost, fails here), applies their examples with a server-side dry run and
// checks their CEL rules.
func TestAuthCredentialExamples(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	k8s := secretsStartEnv(t, root, []string{
		"auth.obco.dev_approles.yaml",
		"auth.obco.dev_approlesecretids.yaml",
		"auth.obco.dev_tokenroles.yaml",
		"auth.obco.dev_certauthconfigs.yaml",
		"auth.obco.dev_certauthroles.yaml",
		"auth.obco.dev_userpassusers.yaml",
	})
	ctx := context.Background()

	for _, file := range []string{"approle.yaml", "approlesecretid.yaml", "tokenrole.yaml", "certauthconfig.yaml",
		"certauthrole.yaml", "userpassuser.yaml"} {
		t.Run(file, func(t *testing.T) {
			for _, obj := range secretsDecode(t, filepath.Join(root, "examples", "auth", file)) {
				if obj.GetNamespace() == "" {
					obj.SetNamespace("default")
				}
				if err := k8s.Create(ctx, obj, client.DryRunAll, client.FieldValidation("Strict")); err != nil {
					t.Errorf("%s %s: %v", obj.GetKind(), obj.GetName(), err)
				}
			}
		})
	}

	t.Run("validation", func(t *testing.T) {
		const head = "apiVersion: auth.obco.dev/v1alpha1\nmetadata: {name: x, namespace: default}\n"
		for _, tt := range []struct {
			name, doc, wantErr string
		}{
			{name: "secret id without role", wantErr: "set exactly one of roleName and roleRef", doc: `
kind: AppRoleSecretID
spec: {connectionRef: {name: openbao}, writeSecretRef: {name: s}}`},
			{name: "secret id with both role fields", wantErr: "set exactly one of roleName and roleRef", doc: `
kind: AppRoleSecretID
spec: {connectionRef: {name: openbao}, roleName: r, roleRef: {name: r}, writeSecretRef: {name: s}}`},
			{name: "secret id mount with roleRef", wantErr: "mountPath is only used with roleName", doc: `
kind: AppRoleSecretID
spec: {connectionRef: {name: openbao}, roleRef: {name: r}, mountPath: approle, writeSecretRef: {name: s}}`},
			{name: "secret id adopt", wantErr: "cannot be adopted", doc: `
kind: AppRoleSecretID
spec: {connectionRef: {name: openbao}, roleName: r, creationPolicy: Adopt, writeSecretRef: {name: s}}`},
			{name: "secret id bad renewBefore", wantErr: "spec.rotation.renewBefore", doc: `
kind: AppRoleSecretID
spec: {connectionRef: {name: openbao}, roleName: r, writeSecretRef: {name: s}, rotation: {renewBefore: 100%}}`},
			{name: "approle upper case name", wantErr: authBuiltinSpecName, doc: `
kind: AppRole
spec: {connectionRef: {name: openbao}, name: CI}`},
			{name: "approle bad cidr", wantErr: "CIDR blocks or IP addresses", doc: `
kind: AppRole
spec: {connectionRef: {name: openbao}, secretIDBoundCIDRs: [nope]}`},
			{name: "token role batch renewable", wantErr: "tokenType batch requires", doc: `
kind: TokenRole
spec: {connectionRef: {name: openbao}, tokenType: batch, orphan: true}`},
			{name: "token role batch", doc: `
kind: TokenRole
spec: {connectionRef: {name: openbao}, tokenType: batch, orphan: true, renewable: false}`},
			{name: "token role upper case policy", wantErr: "spec.allowedPolicies", doc: `
kind: TokenRole
spec: {connectionRef: {name: openbao}, allowedPolicies: [Admin]}`},
			{name: "cert config delete", wantErr: authCloudOrphanErr, doc: `
kind: CertAuthConfig
spec: {connectionRef: {name: openbao}, deletionPolicy: Delete}`},
			{name: "cert config create", wantErr: "set creationPolicy to Adopt or CreateOrAdopt", doc: `
kind: CertAuthConfig
spec: {connectionRef: {name: openbao}}`},
			{name: "approle without constraint", wantErr: "bindSecretID false requires", doc: `
kind: AppRole
spec: {connectionRef: {name: openbao}, bindSecretID: false}`},
			{name: "approle cidr constraint", doc: `
kind: AppRole
spec: {connectionRef: {name: openbao}, bindSecretID: false, tokenBoundCIDRs: [10.0.0.0/8]}`},
			{name: "approle too many policy refs", wantErr: "spec.policyRefs", doc: `
kind: AppRole
spec: {connectionRef: {name: openbao}, policyRefs: [` + authCredPolicyRefs(33) + `]}`},
			{name: "cert role not pem", wantErr: "certificate must be PEM encoded", doc: `
kind: CertAuthRole
spec: {connectionRef: {name: openbao}, certificate: nope}`},
			{name: "userpass both passwords", wantErr: "set exactly one of passwordSecretRef", doc: `
kind: UserpassUser
spec: {connectionRef: {name: openbao}, passwordSecretRef: {name: s, key: p},
  passwordHashSecretRef: {name: s, key: h}}`},
			{name: "userpass no password", wantErr: "set exactly one of passwordSecretRef", doc: `
kind: UserpassUser
spec: {connectionRef: {name: openbao}}`},
		} {
			obj := secretsDecodeString(t, head+tt.doc)
			err := k8s.Create(ctx, obj, client.DryRunAll)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("%s: %v", tt.name, err)
			case tt.wantErr != "" && (!apierrors.IsInvalid(err) || !strings.Contains(err.Error(), tt.wantErr)):
				t.Errorf("%s: got %v, want invalid with %q", tt.name, err, tt.wantErr)
			}
		}
	})

	t.Run("immutable", func(t *testing.T) {
		for _, tt := range []struct {
			kind, spec string
			fields     map[string]any
		}{
			{"AppRole", "{connectionRef: {name: openbao}, name: r}",
				map[string]any{authCloudMountPath: authCredOther, authCloudName: authCredOther, "localSecretIDs": true}},
			{"AppRoleSecretID", "{connectionRef: {name: openbao}, roleName: r, ttl: 1h, writeSecretRef: {name: s}}",
				map[string]any{"roleName": authCredOther, "ttl": "2h", "numUses": int64(2), "cidrList": []any{"10.0.0.0/8"},
					"metadata": map[string]any{"a": "b"}, "writeSecretRef": map[string]any{authCloudName: authCredOther}}},
			{"TokenRole", "{connectionRef: {name: openbao}, name: r}", map[string]any{authCloudName: authCredOther}},
			{"CertAuthConfig", "{connectionRef: {name: openbao}, creationPolicy: Adopt}",
				map[string]any{authCloudMountPath: authCredOther}},
			{"CertAuthRole", "{connectionRef: {name: openbao}, name: r, certificate: '-----BEGIN CERTIFICATE-----'}",
				map[string]any{authCloudMountPath: authCredOther, authCloudName: authCredOther}},
			{"UserpassUser", "{connectionRef: {name: openbao}, name: r, passwordSecretRef: {name: s, key: p}}",
				map[string]any{authCloudMountPath: authCredOther, authCloudName: authCredOther}},
		} {
			obj := secretsDecodeString(t, "apiVersion: auth.obco.dev/v1alpha1\nkind: "+tt.kind+
				"\nmetadata: {name: immutable-"+strings.ToLower(tt.kind)+", namespace: default}\nspec: "+tt.spec)
			if err := k8s.Create(ctx, obj); err != nil {
				t.Fatalf("%s: %v", tt.kind, err)
			}
			for field, value := range tt.fields {
				changed := obj.DeepCopy()
				if err := unstructured.SetNestedField(changed.Object, value, "spec", field); err != nil {
					t.Fatal(err)
				}
				err := k8s.Update(ctx, changed)
				if !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), field+" is immutable") {
					t.Errorf("%s spec.%s: got %v, want immutable", tt.kind, field, err)
				}
			}
			if tt.kind == "AppRoleSecretID" {
				changed := obj.DeepCopy()
				_ = unstructured.SetNestedField(changed.Object, "1h", "spec", "rotation", "renewBefore")
				if err := k8s.Update(ctx, changed); err != nil {
					t.Errorf("rotation must stay mutable: %v", err)
				}
			}
		}
	})
}

// authCredOther is the changed value of an immutable field.
const authCredOther = "other"

func authCredPolicyRefs(n int) string {
	refs := make([]string, n)
	for i := range refs {
		refs[i] = fmt.Sprintf("{name: p%d}", i)
	}
	return strings.Join(refs, ", ")
}
