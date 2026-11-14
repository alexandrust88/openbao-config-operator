package access

import (
	"testing"

	accessv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/access/v1alpha1"
)

func TestRenderPolicy(t *testing.T) {
	read := accessv1alpha1.AccessLevelSpec{Data: []accessv1alpha1.AccessCapability{"read"},
		Metadata: []accessv1alpha1.AccessCapability{"list", "read"}}
	owner := accessv1alpha1.AccessLevelSpec{Data: []accessv1alpha1.AccessCapability{"update", "create", "read"},
		Versions: true}
	rules := []policyRule{
		{mount: "secret", path: "apps/{sa.namespace}/*", metadataPath: "apps/{sa.namespace}/*", level: read},
		{mount: "secret", path: "apps/{sa.namespace}/*", metadataPath: "apps/{sa.namespace}", level: owner},
	}
	want := `# managed-by: openbao-config-operator owner: AccessGrant/team/app
path "secret/data/apps/{{identity.entity.aliases.auth_kubernetes_1.metadata.service_account_namespace}}/*" {
  capabilities = ["create", "read", "update"]
}
path "secret/delete/apps/{{identity.entity.aliases.auth_kubernetes_1.metadata.service_account_namespace}}/*" {
  capabilities = ["update"]
}
path "secret/destroy/apps/{{identity.entity.aliases.auth_kubernetes_1.metadata.service_account_namespace}}/*" {
  capabilities = ["update"]
}
path "secret/metadata/apps/{{identity.entity.aliases.auth_kubernetes_1.metadata.service_account_namespace}}/*" {
  capabilities = ["read", "list"]
}
path "secret/undelete/apps/{{identity.entity.aliases.auth_kubernetes_1.metadata.service_account_namespace}}/*" {
  capabilities = ["update"]
}
`
	got := renderPolicy("AccessGrant/team/app", rules, "auth_kubernetes_1")
	if got != want {
		t.Errorf("renderPolicy:\n%s\nwant:\n%s", got, want)
	}

	reversed := []policyRule{rules[1], rules[0]}
	if again := renderPolicy("AccessGrant/team/app", reversed, "auth_kubernetes_1"); again != got {
		t.Errorf("rule order changed the text:\n%s", again)
	}
}

func TestRenderPolicySAName(t *testing.T) {
	level := accessv1alpha1.AccessLevelSpec{Data: []accessv1alpha1.AccessCapability{"read"}}
	got := renderPolicy("o", []policyRule{{mount: "kv", path: "{sa.name}", metadataPath: "{sa.name}", level: level}}, "acc")
	want := "# managed-by: openbao-config-operator owner: o\n" +
		"path \"kv/data/{{identity.entity.aliases.acc.metadata.service_account_name}}\" {\n  capabilities = [\"read\"]\n}\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestExpandRule(t *testing.T) {
	level := accessv1alpha1.AccessLevelSpec{Data: []accessv1alpha1.AccessCapability{"read"}}
	p := accessv1alpha1.AccessPath{Mount: "orgs", Path: "{org}/+/*"}
	rules := expandRule(p, level, []string{"acme", "globex"})
	if len(rules) != 2 {
		t.Fatalf("rules = %d, want 2", len(rules))
	}
	if rules[0].path != "acme/+/*" || rules[0].metadataPath != "acme/+/*" || rules[1].path != "globex/+/*" {
		t.Errorf("expanded = %+v", rules)
	}

	p.MetadataPath = "{org}/*"
	single := expandRule(accessv1alpha1.AccessPath{Mount: "kv", Path: "a/*"}, level, nil)
	if len(single) != 1 || single[0].metadataPath != "a/*" {
		t.Errorf("no orgs: %+v", single)
	}
	if got := expandRule(p, level, []string{"acme"}); got[0].metadataPath != "acme/*" {
		t.Errorf("metadataPath = %q", got[0].metadataPath)
	}
}
