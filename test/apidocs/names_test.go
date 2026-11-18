package apidocs

import (
	"strings"
	"testing"
)

// wellKnownResources are plurals and short names of core Kubernetes and of projects often installed next to the
// operator. kubectl resolves a bare name to one group only, so a clash hides one of the two kinds (ADR 0025).
var wellKnownResources = map[string]string{
	// core Kubernetes
	"namespaces": "kubernetes", "secrets": "kubernetes", "configmaps": "kubernetes", "pods": "kubernetes",
	"services": "kubernetes", "serviceaccounts": "kubernetes", "roles": "kubernetes", "rolebindings": "kubernetes",
	"clusterroles": "kubernetes", "clusterrolebindings": "kubernetes", "deployments": "kubernetes",
	"statefulsets": "kubernetes", "daemonsets": "kubernetes", "jobs": "kubernetes", "cronjobs": "kubernetes",
	"endpoints": "kubernetes", "endpointslices": "kubernetes", "events": "kubernetes", "nodes": "kubernetes",
	"leases": "kubernetes", "ingresses": "kubernetes", "networkpolicies": "kubernetes",
	"persistentvolumes": "kubernetes", "persistentvolumeclaims": "kubernetes", "certificatesigningrequests": "kubernetes",
	"ns": "kubernetes", "cm": "kubernetes", "po": "kubernetes", "svc": "kubernetes", "sa": "kubernetes",
	"deploy": "kubernetes", "sts": "kubernetes", "ds": "kubernetes", "cj": "kubernetes", "ep": "kubernetes",
	"ev": "kubernetes", "no": "kubernetes", "pv": "kubernetes", "pvc": "kubernetes", "ing": "kubernetes",
	"netpol": "kubernetes", "pdb": "kubernetes", "csr": "kubernetes",
	// Kyverno
	"policies": "kyverno", "clusterpolicies": "kyverno", "policyreports": "kyverno", "clusterpolicyreports": "kyverno",
	"policyexceptions": "kyverno", "cleanuppolicies": "kyverno", "clustercleanuppolicies": "kyverno",
	"validatingpolicies": "kyverno", "mutatingpolicies": "kyverno", "generatingpolicies": "kyverno",
	"deletingpolicies": "kyverno", "imagevalidatingpolicies": "kyverno",
	"pol": "kyverno", "cpol": "kyverno", "polr": "kyverno", "cpolr": "kyverno", "polex": "kyverno",
	// Argo CD, Argo Workflows, Argo Rollouts
	"applications": "argo", "applicationsets": "argo", "appprojects": "argo", "workflows": "argo",
	"workflowtemplates": "argo", "clusterworkflowtemplates": "argo", "cronworkflows": "argo", "rollouts": "argo",
	"app": "argo", "apps": "argo", "appset": "argo", "appproj": "argo", "wf": "argo", "cwf": "argo", "wftmpl": "argo",
	"ro": "argo",
	// OpenShift
	"groups": "openshift", "users": "openshift", "projects": "openshift", "routes": "openshift",
	"identities": "openshift",
	// cert-manager
	"certificates": "cert-manager", "issuers": "cert-manager", "clusterissuers": "cert-manager",
	"certificaterequests": "cert-manager", "orders": "cert-manager", "challenges": "cert-manager",
	"cert": "cert-manager", "certs": "cert-manager", "cr": "cert-manager", "crs": "cert-manager",
	// Flux
	"kustomizations": "flux", "helmreleases": "flux", "gitrepositories": "flux", "ocirepositories": "flux",
	"helmrepositories": "flux", "helmcharts": "flux", "buckets": "flux",
	"ks": "flux", "hr": "flux", "gitrepo": "flux", "ocirepo": "flux", "helmrepo": "flux", "hc": "flux",
	// Crossplane provider-vault
	"entities": "provider-vault", "entityaliases": "provider-vault", "groupaliases": "provider-vault",
	"mounts": "provider-vault", "tokens": "provider-vault", "audits": "provider-vault",
	"secretsmounts": "provider-vault", "oidcassignments": "provider-vault", "oidcclients": "provider-vault",
	"oidckeys": "provider-vault", "oidcproviders": "provider-vault", "oidcroles": "provider-vault",
	"oidcscopes": "provider-vault", "mfaloginenforcements": "provider-vault",
	// redhat-cop vault-config-operator
	"passwordpolicies": "vault-config-operator", "randomsecrets": "vault-config-operator",
	"vaultsecrets": "vault-config-operator", "secretenginemounts": "vault-config-operator",
	"authenginemounts": "vault-config-operator", "kubernetesauthengineroles": "vault-config-operator",
}

// acceptedOverlaps are clashes kept on purpose: the names are the OpenBao terms, the other project is rarely
// installed next to this operator, and the group-qualified name (kubectl get <plural>.<group>) stays unambiguous.
var acceptedOverlaps = map[string]bool{
	"passwordpolicies": true, "randomsecrets": true, // redhat-cop vault-config-operator
	"audits": true, "secretsmounts": true, "oidcassignments": true, "oidcclients": true, "oidckeys": true,
	"oidcproviders": true, "oidcroles": true, "oidcscopes": true, "mfaloginenforcements": true, // provider-vault
}

// TestResourceNamesDoNotClash fails when a plural or short name is used by a well-known project, and on the
// openbao* plurals and ob* short names of the other openbao-config-operator projects (Stakater MTO and its copies).
func TestResourceNamesDoNotClash(t *testing.T) {
	used := map[string]bool{}
	for _, crd := range loadCRDs(t) {
		names := append([]string{crd.Spec.Names.Plural}, crd.Spec.Names.ShortNames...)
		for i, name := range names {
			used[name] = true
			if owner, ok := wellKnownResources[name]; ok && !acceptedOverlaps[name] {
				t.Errorf("%s: %q is a resource name of %s", crd.Spec.Names.Kind, name, owner)
			}
			if i == 0 && strings.HasPrefix(name, "openbao") {
				t.Errorf("%s: plural %q clashes with the openbao* plurals of Stakater's operator", crd.Spec.Names.Kind, name)
			}
			if i > 0 && strings.HasPrefix(name, "ob") {
				t.Errorf("%s: short name %q clashes with the ob* short names of Stakater's operator", crd.Spec.Names.Kind, name)
			}
		}
	}
	for name := range acceptedOverlaps {
		if !used[name] {
			t.Errorf("acceptedOverlaps lists %q, which no CRD uses any more", name)
		}
	}
}
