package apidocs

import (
	"encoding/json"
	"slices"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

// TestManagedKindsDeclareV2Defaults checks design rules 5, 6 and 10 on every generated CRD of a managed kind (one
// with spec.connectionRef): create, retain and manage by default, an immutable connection, and Ready/Synced
// conditions with observedGeneration.
func TestManagedKindsDeclareV2Defaults(t *testing.T) {
	managed := 0
	for _, crd := range loadCRDs(t) {
		for _, v := range crd.Spec.Versions {
			root := v.Schema.OpenAPIV3Schema
			spec, status := root.Properties["spec"], root.Properties["status"]
			if _, ok := spec.Properties["connectionRef"]; !ok {
				continue
			}
			managed++
			name := crd.Spec.Names.Kind + "/" + v.Name
			for field, want := range map[string]string{
				"creationPolicy": "Create", "deletionPolicy": "Orphan", "managementPolicy": "Manage",
			} {
				if got := schemaDefault(t, spec.Properties[field]); got != want {
					t.Errorf("%s: spec.%s default = %q, want %q", name, field, got, want)
				}
			}
			if !slices.ContainsFunc(spec.XValidations, func(r apiextensionsv1.ValidationRule) bool {
				return r.Message == "connectionRef is immutable"
			}) {
				t.Errorf("%s: spec has no connectionRef immutability rule", name)
			}
			conditions := status.Properties["conditions"]
			if conditions.XListType == nil || *conditions.XListType != "map" || !slices.Equal(conditions.XListMapKeys, []string{"type"}) {
				t.Errorf("%s: status.conditions is not a list map keyed by type", name)
			}
			if _, ok := status.Properties["observedGeneration"]; !ok {
				t.Errorf("%s: status has no observedGeneration", name)
			}
			var columns []string
			for _, c := range v.AdditionalPrinterColumns {
				columns = append(columns, c.Name)
			}
			if !slices.Contains(columns, "Ready") || !slices.Contains(columns, "Synced") {
				t.Errorf("%s: printer columns %v lack Ready or Synced", name, columns)
			}
		}
	}
	if managed == 0 {
		t.Fatal("no managed CRD found")
	}
}

func schemaDefault(t *testing.T, s apiextensionsv1.JSONSchemaProps) string {
	t.Helper()
	if s.Default == nil {
		return ""
	}
	var v string
	if err := json.Unmarshal(s.Default.Raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}
