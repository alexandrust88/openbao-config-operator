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
	authPluginMountPath = "mountPath"
	authPluginName      = "name"
	authPluginLDAPMount = "ldap"
)

var authPluginKinds = []string{
	"githubauthconfig", "githubauthteam", "githubauthuser",
	"kerberosauthconfig", "kerberosauthgroup", "kerberosauthldapconfig",
	"ldapauthconfig", "ldapauthgroup", "ldapauthuser",
	"oktaauthconfig", "oktaauthgroup", "oktaauthuser",
	"radiusauthconfig", "radiusauthuser",
}

// TestAuthPluginExamples applies the examples of the external-plugin auth kinds with a server-side dry run and
// checks their CEL rules against envtest.
func TestAuthPluginExamples(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	crds := make([]string, 0, len(authPluginKinds))
	for _, kind := range authPluginKinds {
		crds = append(crds, "auth.obco.dev_"+kind+"s.yaml")
	}
	k8s := secretsStartEnv(t, root, crds)
	ctx := context.Background()
	const ns = "obco-examples"
	if err := k8s.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}); err != nil {
		t.Fatal(err)
	}

	for _, kind := range authPluginKinds {
		t.Run(kind, func(t *testing.T) {
			for _, obj := range secretsDecode(t, filepath.Join(root, "examples", "auth", kind+".yaml")) {
				obj.SetNamespace(ns)
				if err := k8s.Create(ctx, obj, client.DryRunAll, client.FieldValidation("Strict")); err != nil {
					t.Errorf("%s %s: %v", obj.GetKind(), obj.GetName(), err)
				}
			}
		})
	}

	t.Run("validation", func(t *testing.T) {
		for _, tt := range []struct {
			name, doc, wantErr string
		}{
			{name: "radius config with delete policy", wantErr: "the config endpoint cannot be deleted", doc: `
apiVersion: auth.obco.dev/v1alpha1
kind: RADIUSAuthConfig
metadata: {name: delete}
spec: {connectionRef: {name: openbao}, host: radius, secretSecretRef: {name: s, key: k}, deletionPolicy: Delete}`},
			{name: "client cert without key", wantErr: "set clientTLSCert and clientTLSKeySecretRef together", doc: `
apiVersion: auth.obco.dev/v1alpha1
kind: LDAPAuthConfig
metadata: {name: cert-only}
spec: {connectionRef: {name: openbao}, url: ldaps://ldap, clientTLSCert: pem}`},
			{name: "bind password without bind dn", wantErr: "bindPassSecretRef requires bindDN", doc: `
apiVersion: auth.obco.dev/v1alpha1
kind: KerberosAuthLDAPConfig
metadata: {name: no-binddn}
spec: {connectionRef: {name: openbao}, url: ldaps://ldap, bindPassSecretRef: {name: s, key: k}}`},
			{name: "tls min above max", wantErr: "tlsMinVersion must not exceed tlsMaxVersion", doc: `
apiVersion: auth.obco.dev/v1alpha1
kind: LDAPAuthConfig
metadata: {name: tls}
spec: {connectionRef: {name: openbao}, url: ldaps://ldap, tlsMinVersion: tls13, tlsMaxVersion: tls12}`},
			{name: "token ttl above max", wantErr: "tokenTTL must not exceed tokenMaxTTL", doc: `
apiVersion: auth.obco.dev/v1alpha1
kind: OktaAuthConfig
metadata: {name: ttl}
spec: {connectionRef: {name: openbao}, orgName: example, tokenTTL: 2h, tokenMaxTTL: 1h}`},
			{name: "upper case policy", wantErr: "spec.policies", doc: `
apiVersion: auth.obco.dev/v1alpha1
kind: LDAPAuthGroup
metadata: {name: upper}
spec: {connectionRef: {name: openbao}, policies: [Admin]}`},
			{name: "comma in group", wantErr: "spec.groups", doc: `
apiVersion: auth.obco.dev/v1alpha1
kind: OktaAuthUser
metadata: {name: comma}
spec: {connectionRef: {name: openbao}, groups: ["a,b"]}`},
			{name: "slash in name", wantErr: "should match '^[^/]+$'", doc: `
apiVersion: auth.obco.dev/v1alpha1
kind: GitHubAuthTeam
metadata: {name: slash}
spec: {connectionRef: {name: openbao}, name: org/team}`},
			{name: "user delete policy", doc: `
apiVersion: auth.obco.dev/v1alpha1
kind: RADIUSAuthUser
metadata: {name: delete}
spec: {connectionRef: {name: openbao}, deletionPolicy: Delete}`},
		} {
			obj := secretsDecodeString(t, tt.doc)
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
		obj := secretsDecodeString(t, `
apiVersion: auth.obco.dev/v1alpha1
kind: LDAPAuthUser
metadata: {name: immutable}
spec: {connectionRef: {name: openbao}, name: jdoe}`)
		obj.SetNamespace(ns)
		if err := k8s.Create(ctx, obj); err != nil {
			t.Fatal(err)
		}
		if mount, _, _ := unstructured.NestedString(obj.Object, "spec", authPluginMountPath); mount != authPluginLDAPMount {
			t.Errorf("default mountPath = %q, want the LDAP default", mount)
		}
		for field, value := range map[string]any{authPluginMountPath: "corp/ldap", authPluginName: "jsmith"} {
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
		if err := unstructured.SetNestedStringSlice(changed.Object, []string{"auditors"}, "spec", "policies"); err != nil {
			t.Fatal(err)
		}
		if err := k8s.Update(ctx, changed); err != nil {
			t.Errorf("mutable policies: %v", err)
		}
	})
}
