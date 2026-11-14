package examples

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	authBuiltinSpecName  = "spec.name"
	authBuiltinLeewayErr = "must be -1, a number of seconds"
)

// TestBuiltinAuthExamples applies the Kubernetes and JWT auth examples with a server-side dry run and checks the
// CEL rules of those kinds against envtest.
func TestBuiltinAuthExamples(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	k8s := secretsStartEnv(t, root, []string{
		"auth.openbaoconfig.io_kubernetesauthconfigs.yaml",
		"auth.openbaoconfig.io_kubernetesauthroles.yaml",
		"auth.openbaoconfig.io_jwtauthconfigs.yaml",
		"auth.openbaoconfig.io_jwtauthroles.yaml",
		"auth.openbaoconfig.io_jwtcelroles.yaml",
	})
	ctx := context.Background()

	files := []string{
		"kubernetesauthconfig.yaml", "kubernetesauthrole.yaml", "jwtauthconfig.yaml", "jwtauthrole.yaml", "jwtcelrole.yaml",
	}
	for _, file := range files {
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
		const head = "apiVersion: auth.openbaoconfig.io/v1alpha1\nmetadata: {name: x, namespace: default}\n"
		for _, tt := range []struct {
			name, doc, wantErr string
		}{
			{name: "kubernetes config delete policy", wantErr: authCloudOrphanErr, doc: `
kind: KubernetesAuthConfig
spec: {connectionRef: {name: openbao}, kubernetesHost: https://k8s, deletionPolicy: Delete}`},
			{name: "kubernetes config local ca disabled without ca", wantErr: "kubernetesCACert is required", doc: `
kind: KubernetesAuthConfig
spec: {connectionRef: {name: openbao}, kubernetesHost: https://k8s, disableLocalCAJWT: true}`},
			{name: "kubernetes role without namespaces", wantErr: "set boundServiceAccountNamespaces", doc: `
kind: KubernetesAuthRole
spec: {connectionRef: {name: openbao}, boundServiceAccountNames: [app]}`},
			{name: "kubernetes role wildcard mixed", wantErr: "'*' cannot be combined", doc: `
kind: KubernetesAuthRole
spec: {connectionRef: {name: openbao}, boundServiceAccountNames: ['*', app], boundServiceAccountNamespaces: [a]}`},
			{name: "kubernetes role selector with expressions", wantErr: "boundServiceAccountNamespaceSelector.matchLabels: Required value", doc: `
kind: KubernetesAuthRole
spec: {connectionRef: {name: openbao}, boundServiceAccountNames: [app],
  boundServiceAccountNamespaceSelector: {matchExpressions: [{key: env, operator: In, values: [dev]}]}}`},
			{name: "kubernetes role selector without labels", wantErr: "matchLabels in body should have at least 1 properties", doc: `
kind: KubernetesAuthRole
spec: {connectionRef: {name: openbao}, boundServiceAccountNames: [app],
  boundServiceAccountNamespaceSelector: {matchLabels: {}}}`},
			{name: "kubernetes role selector with labels", doc: `
kind: KubernetesAuthRole
spec: {connectionRef: {name: openbao}, boundServiceAccountNames: [app],
  boundServiceAccountNamespaceSelector: {matchLabels: {team: a}}}`},
			{name: "kubernetes role ttl over max", wantErr: "tokenTTL must not exceed tokenMaxTTL", doc: `
kind: KubernetesAuthRole
spec: {connectionRef: {name: openbao}, boundServiceAccountNames: [app], boundServiceAccountNamespaces: [a],
  tokenTTL: 2h, tokenMaxTTL: 1h}`},
			{name: "kubernetes role bad cidr", wantErr: "CIDR blocks or IP addresses", doc: `
kind: KubernetesAuthRole
spec: {connectionRef: {name: openbao}, boundServiceAccountNames: [app], boundServiceAccountNamespaces: [a],
  tokenBoundCIDRs: [not-a-cidr]}`},
			{name: "kubernetes role upper case name", wantErr: authBuiltinSpecName, doc: `
kind: KubernetesAuthRole
spec: {connectionRef: {name: openbao}, name: App, boundServiceAccountNames: [app],
  boundServiceAccountNamespaces: [a]}`},
			{name: "jwt config two key sources", wantErr: "set exactly one of oidcDiscoveryURL", doc: `
kind: JWTAuthConfig
spec: {connectionRef: {name: openbao}, jwksURL: https://a/keys, jwtValidationPubkeys: [k]}`},
			{name: "jwt config no key source", wantErr: "set exactly one of oidcDiscoveryURL", doc: `
kind: JWTAuthConfig
spec: {connectionRef: {name: openbao}, boundIssuer: x}`},
			{name: "jwt config client id without secret", wantErr: "must be set together", doc: `
kind: JWTAuthConfig
spec: {connectionRef: {name: openbao}, oidcDiscoveryURL: https://idp, oidcClientID: c}`},
			{name: "jwt config client id without discovery", wantErr: "oidcClientID requires oidcDiscoveryURL", doc: `
kind: JWTAuthConfig
spec: {connectionRef: {name: openbao}, jwksURL: https://idp/keys, oidcClientID: c,
  oidcClientSecretSecretRef: {name: s, key: k}}`},
			{name: "jwt config delete policy", wantErr: authCloudOrphanErr, doc: `
kind: JWTAuthConfig
spec: {connectionRef: {name: openbao}, jwksURL: https://idp/keys, deletionPolicy: Delete}`},
			{name: "jwt config well-known", wantErr: "must not contain the .well-known", doc: `
kind: JWTAuthConfig
spec: {connectionRef: {name: openbao}, oidcDiscoveryURL: https://idp/.well-known/openid-configuration}`},
			{name: "jwt config id_token with query", wantErr: "requires oidcResponseMode form_post", doc: `
kind: JWTAuthConfig
spec: {connectionRef: {name: openbao}, oidcDiscoveryURL: https://idp, oidcResponseTypes: [id_token]}`},
			{name: "jwt role without bound constraint", wantErr: "needs at least one of boundAudiences", doc: `
kind: JWTAuthRole
spec: {connectionRef: {name: openbao}, userClaim: sub}`},
			{name: "oidc role without redirect", wantErr: "allowedRedirectURIs is required", doc: `
kind: JWTAuthRole
spec: {connectionRef: {name: openbao}, userClaim: sub, roleType: oidc}`},
			{name: "jwt role bad leeway", wantErr: authBuiltinLeewayErr, doc: `
kind: JWTAuthRole
spec: {connectionRef: {name: openbao}, userClaim: sub, boundSubject: s, expirationLeeway: -5}`},
			{name: "jwt role bad leeway string", wantErr: authBuiltinLeewayErr, doc: `
kind: JWTAuthRole
spec: {connectionRef: {name: openbao}, userClaim: sub, boundSubject: s, notBeforeLeeway: soon}`},
			{name: "jwt role sub-second leeway", wantErr: authBuiltinLeewayErr, doc: `
kind: JWTAuthRole
spec: {connectionRef: {name: openbao}, userClaim: sub, boundSubject: s, clockSkewLeeway: 500ms}`},
			{name: "jwt role disabled leeway", doc: `
kind: JWTAuthRole
spec: {connectionRef: {name: openbao}, userClaim: sub, boundSubject: s, clockSkewLeeway: -1}`},
			{name: "cel role without expression", wantErr: "spec.celProgram.expression", doc: `
kind: JWTCELRole
spec: {connectionRef: {name: openbao}, celProgram: {expression: ""}}`},
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

	t.Run("selector expressions", func(t *testing.T) {
		const doc = "apiVersion: auth.openbaoconfig.io/v1alpha1\nkind: KubernetesAuthRole\n" +
			"metadata: {name: x, namespace: default}\nspec: {connectionRef: {name: openbao}, boundServiceAccountNames: [app],\n" +
			"  boundServiceAccountNamespaceSelector: {matchLabels: {team: a}, matchExpressions: [{key: env, operator: In, values: [dev]}]}}"
		for _, validation := range []string{"Ignore", "Strict"} {
			err := k8s.Create(ctx, secretsDecodeString(t, doc), client.DryRunAll, client.FieldValidation(validation))
			if !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), "matchExpressions is not supported by OpenBao; use matchLabels") {
				t.Errorf("fieldValidation=%s: got %v, want invalid with the matchExpressions message", validation, err)
			}
		}
	})

	t.Run("immutable", func(t *testing.T) {
		for kind, spec := range map[string]string{
			"KubernetesAuthConfig": "{connectionRef: {name: openbao}, kubernetesHost: https://k8s}",
			"KubernetesAuthRole": "{connectionRef: {name: openbao}, name: r, boundServiceAccountNames: [app], " +
				"boundServiceAccountNamespaces: [a]}",
			"JWTAuthConfig": "{connectionRef: {name: openbao}, jwksURL: https://idp/keys}",
			"JWTAuthRole":   "{connectionRef: {name: openbao}, name: r, userClaim: sub, boundSubject: s}",
			"JWTCELRole":    "{connectionRef: {name: openbao}, name: r, celProgram: {expression: 'true'}}",
		} {
			obj := secretsDecodeString(t, "apiVersion: auth.openbaoconfig.io/v1alpha1\nkind: "+kind+
				"\nmetadata: {name: immutable, namespace: default}\nspec: "+spec)
			if err := k8s.Create(ctx, obj); err != nil {
				t.Fatalf("%s: %v", kind, err)
			}
			fields := []string{authCloudMountPath}
			if strings.HasSuffix(kind, "Role") {
				fields = append(fields, "name")
			}
			for _, field := range fields {
				changed := obj.DeepCopy()
				if err := unstructured.SetNestedField(changed.Object, "other", "spec", field); err != nil {
					t.Fatal(err)
				}
				err := k8s.Update(ctx, changed)
				if !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), field+" is immutable") {
					t.Errorf("%s spec.%s: got %v, want immutable", kind, field, err)
				}
			}
		}
	})
}
