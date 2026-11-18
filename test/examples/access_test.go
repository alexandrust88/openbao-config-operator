package examples

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestAccessExamples installs the access CRDs, applies the access examples with a server-side dry run and
// checks the CEL rules of the access kinds.
func TestAccessExamples(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	k8s := secretsStartEnv(t, root, []string{
		"access.obco.dev_accesslevels.yaml",
		"access.obco.dev_accessgrants.yaml",
		"access.obco.dev_accesslayouts.yaml",
	})
	ctx := context.Background()

	for _, file := range []string{"accesslevel.yaml", "accessgrant.yaml", "accesslayout.yaml"} {
		t.Run(file, func(t *testing.T) {
			for _, obj := range secretsDecode(t, filepath.Join(root, "examples", "access", file)) {
				obj.SetNamespace("default")
				if err := k8s.Create(ctx, obj, client.DryRunAll, client.FieldValidation("Strict")); err != nil {
					t.Errorf("%s %s: %v", obj.GetKind(), obj.GetName(), err)
				}
			}
		})
	}

	t.Run("validation", func(t *testing.T) {
		const head = "apiVersion: access.obco.dev/v1alpha1\nmetadata: {name: x, namespace: default}\n"
		const rule = "rules: [{mount: secret, path: 'apps/{sa.namespace}/*', level: read}]"
		for _, tt := range []struct {
			name, doc, wantErr string
		}{
			{name: "empty level", wantErr: "set at least one of data, metadata and versions", doc: `
kind: AccessLevel
spec: {}`},
			{name: "grant without mount", wantErr: "set exactly one of mountPath, mountAccessor and authMountRef", doc: `
kind: AccessGrant
spec: {connectionRef: {name: openbao}, subjects: [{name: app, namespace: team}], ` + rule + `}`},
			{name: "grant with two mounts", wantErr: "set exactly one of mountPath, mountAccessor and authMountRef", doc: `
kind: AccessGrant
spec: {connectionRef: {name: openbao}, mountPath: kubernetes, mountAccessor: a, subjects: [{name: app, namespace: team}], ` + rule + `}`},
			{name: "subject with both namespace forms", wantErr: "set exactly one of namespace and namespaceSelector", doc: `
kind: AccessGrant
spec: {connectionRef: {name: openbao}, mountPath: kubernetes, subjects: [{name: app, namespace: team, namespaceSelector: {matchLabels: {a: b}}}], ` + rule + `}`},
			{name: "broad subject without sa.namespace", wantErr: "requires {sa.namespace}", doc: `
kind: AccessGrant
spec: {connectionRef: {name: openbao}, mountPath: kubernetes, subjects: [{name: app, namespace: 'team-*'}], rules: [{mount: secret, path: 'apps/*', level: read}]}`},
			{name: "selector subject with unscoped metadataPath", wantErr: "requires {sa.namespace}", doc: `
kind: AccessGrant
spec: {connectionRef: {name: openbao}, mountPath: kubernetes, subjects: [{name: app, namespaceSelector: {matchLabels: {a: b}}}], rules: [{mount: secret, path: 'apps/{sa.namespace}/*', metadataPath: 'apps/*', level: read}]}`},
			{name: "org without organizations", wantErr: "lists organizations exactly when", doc: `
kind: AccessGrant
spec: {connectionRef: {name: openbao}, mountPath: kubernetes, subjects: [{name: app, namespace: team}], rules: [{mount: secret, path: '{org}/*', level: read}]}`},
			{name: "organizations without org", wantErr: "lists organizations exactly when", doc: `
kind: AccessGrant
spec: {connectionRef: {name: openbao}, mountPath: kubernetes, subjects: [{name: app, namespace: team}], rules: [{mount: secret, path: 'apps/*', level: read, organizations: [acme]}]}`},
			{name: "dot dot segment", wantErr: "spec.rules[0].path", doc: `
kind: AccessGrant
spec: {connectionRef: {name: openbao}, mountPath: kubernetes, subjects: [{name: app, namespace: team}], rules: [{mount: secret, path: 'apps/../*', level: read}]}`},
			{name: "token inside a segment", wantErr: "spec.rules[0].path", doc: `
kind: AccessGrant
spec: {connectionRef: {name: openbao}, mountPath: kubernetes, subjects: [{name: app, namespace: team}], rules: [{mount: secret, path: 'apps-{sa.name}/*', level: read}]}`},
			{name: "layout with sa token", wantErr: "only valid in AccessGrant rules", doc: `
kind: AccessLayout
spec: {connectionRef: {name: openbao}, authMounts: [oidc], paths: [{mount: secret, path: '{sa.name}/*'}], roles: [{name: dev}]}`},
			{name: "layout template without role", wantErr: "groupAliasTemplate must contain {role}", doc: `
kind: AccessLayout
spec: {connectionRef: {name: openbao}, authMounts: [oidc], groupAliasTemplate: devs, paths: [{mount: secret, path: 'apps/*'}], roles: [{name: dev}]}`},
			{name: "layout template org without organizations", wantErr: "groupAliasTemplate contains {org} exactly when", doc: `
kind: AccessLayout
spec: {connectionRef: {name: openbao}, authMounts: [oidc], groupAliasTemplate: '{org}-{role}', paths: [{mount: secret, path: 'apps/*'}], roles: [{name: dev}]}`},
			{name: "layout organizations without template org", wantErr: "groupAliasTemplate contains {org} exactly when", doc: `
kind: AccessLayout
spec: {connectionRef: {name: openbao}, authMounts: [oidc], organizations: [acme], groupAliasTemplate: '{role}', paths: [{mount: secret, path: 'apps/*'}], roles: [{name: dev}]}`},
			{name: "layout org path without organizations", wantErr: "paths that use {org} require organizations", doc: `
kind: AccessLayout
spec: {connectionRef: {name: openbao}, authMounts: [oidc], paths: [{mount: secret, path: '{org}/*'}], roles: [{name: dev}]}`},
		} {
			t.Run(tt.name, func(t *testing.T) {
				err := k8s.Create(ctx, secretsDecodeString(t, head+tt.doc), client.DryRunAll)
				if !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("got %v, want invalid with %q", err, tt.wantErr)
				}
			})
		}
	})
}
