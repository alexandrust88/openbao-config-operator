// Package access holds the controllers of the access.openbaoconfig.io kinds.
package access

import (
	"maps"
	"slices"
	"strings"

	accessv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/access/v1alpha1"
)

const managedBy = "openbao-config-operator"

var capabilityOrder = []accessv1alpha1.AccessCapability{"create", "read", "update", "patch", "delete", "list"}

// policyRule is one pair of paths with its level resolved and {org} already expanded.
type policyRule struct {
	mount        string
	path         string
	metadataPath string
	level        accessv1alpha1.AccessLevelSpec
}

// expandRule turns a path pair into one policyRule per organization; orgs may be empty.
func expandRule(p accessv1alpha1.AccessPath, level accessv1alpha1.AccessLevelSpec, orgs []string) []policyRule {
	metadataPath := p.MetadataPath
	if metadataPath == "" {
		metadataPath = p.Path
	}
	if len(orgs) == 0 {
		return []policyRule{{mount: p.Mount, path: p.Path, metadataPath: metadataPath, level: level}}
	}
	rules := make([]policyRule, 0, len(orgs))
	for _, org := range orgs {
		rules = append(rules, policyRule{
			mount:        p.Mount,
			path:         strings.ReplaceAll(p.Path, "{org}", org),
			metadataPath: strings.ReplaceAll(metadataPath, "{org}", org),
			level:        level,
		})
	}
	return rules
}

// renderPolicy renders rules into ACL policy text. The text is deterministic: one block per path, sorted
// by path, capabilities merged and in a fixed order, so an unchanged spec always renders the same bytes.
// {sa.namespace} and {sa.name} become identity templates on the alias metadata of saAccessor.
func renderPolicy(owner string, rules []policyRule, saAccessor string) string {
	grants := map[string]map[accessv1alpha1.AccessCapability]bool{}
	add := func(path string, capabilities []accessv1alpha1.AccessCapability) {
		if len(capabilities) == 0 {
			return
		}
		if grants[path] == nil {
			grants[path] = map[accessv1alpha1.AccessCapability]bool{}
		}
		for _, capability := range capabilities {
			grants[path][capability] = true
		}
	}
	for _, rule := range rules {
		data := templatePath(rule.path, saAccessor)
		add(rule.mount+"/data/"+data, rule.level.Data)
		add(rule.mount+"/metadata/"+templatePath(rule.metadataPath, saAccessor), rule.level.Metadata)
		if rule.level.Versions {
			for _, operation := range []string{"delete", "destroy", "undelete"} {
				add(rule.mount+"/"+operation+"/"+data, []accessv1alpha1.AccessCapability{"update"})
			}
		}
	}

	var b strings.Builder
	b.WriteString("# managed-by: " + managedBy + " owner: " + owner + "\n")
	for _, path := range slices.Sorted(maps.Keys(grants)) {
		var quoted []string
		for _, capability := range capabilityOrder {
			if grants[path][capability] {
				quoted = append(quoted, `"`+string(capability)+`"`)
			}
		}
		b.WriteString(`path "` + path + "\" {\n  capabilities = [" + strings.Join(quoted, ", ") + "]\n}\n")
	}
	return b.String()
}

func templatePath(path, saAccessor string) string {
	alias := "{{identity.entity.aliases." + saAccessor + ".metadata."
	path = strings.ReplaceAll(path, "{sa.namespace}", alias+"service_account_namespace}}")
	return strings.ReplaceAll(path, "{sa.name}", alias+"service_account_name}}")
}
