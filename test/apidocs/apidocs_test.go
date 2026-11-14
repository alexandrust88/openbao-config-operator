// Package apidocs checks that every generated CRD documents its fields well enough for kubectl explain.
package apidocs

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

const (
	crdDir               = "../../config/crd/bases"
	apiDir               = "../../api"
	minDescriptionLength = 20
)

var groupsFlag = flag.String("groups", os.Getenv("APIDOCS_GROUPS"),
	"comma-separated API group prefixes to check, for example core,sys,access; empty checks every group")

var (
	camelCase = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*[a-z0-9][A-Z][A-Za-z0-9]*$`)
	word      = regexp.MustCompile(`[A-Za-z][A-Za-z0-9]*`)
	// OpenBao template placeholders such as {{.UserDN}} are user syntax, not leaked Go names.
	placeholder = regexp.MustCompile(`\{\{[^}]*\}\}`)
	goSyntaxes  = []string{"json:", "+kubebuilder", "+optional", "+required", "+listType"}
)

func TestCRDDescriptions(t *testing.T) {
	crds := loadCRDs(t)
	goNames := goIdentifiers(t, crds)
	wanted := map[string]bool{}
	for g := range strings.SplitSeq(*groupsFlag, ",") {
		if g = strings.TrimSpace(g); g != "" {
			wanted[g] = true
		}
	}

	checked := 0
	for _, crd := range crds {
		if len(wanted) > 0 && !wanted[strings.SplitN(crd.Spec.Group, ".", 2)[0]] {
			continue
		}
		checked++
		t.Run(crd.Name, func(t *testing.T) {
			for _, v := range crd.Spec.Versions {
				if v.Schema == nil || v.Schema.OpenAPIV3Schema == nil {
					t.Errorf("%s: version %s has no schema", crd.Name, v.Name)
					continue
				}
				root := v.Schema.OpenAPIV3Schema
				if !strings.Contains(root.Description, "Example:") {
					t.Errorf("%s: kind description has no \"Example:\" section", crd.Spec.Names.Kind)
				}
				var problems []string
				checkDescription(crd.Spec.Names.Kind, root.Description, goNames, &problems)
				for name, prop := range root.Properties {
					if name == "metadata" {
						continue
					}
					walk(crd.Spec.Names.Kind+"."+name, prop, goNames, &problems)
				}
				sort.Strings(problems)
				for _, p := range problems {
					t.Error(p)
				}
			}
		})
	}
	if checked == 0 {
		t.Fatalf("no CRD matched -groups=%q", *groupsFlag)
	}
}

func walk(path string, s apiextensionsv1.JSONSchemaProps, goNames map[string]bool, problems *[]string) {
	checkDescription(path, s.Description, goNames, problems)
	for name, prop := range s.Properties {
		walk(path+"."+name, prop, goNames, problems)
	}
	if s.Items != nil && s.Items.Schema != nil {
		walkNested(path+"[]", *s.Items.Schema, goNames, problems)
	}
	if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
		walkNested(path+"{}", *s.AdditionalProperties.Schema, goNames, problems)
	}
}

// walkNested checks an items or additionalProperties schema: a scalar element carries no description of its
// own (the field describes it), an object element must describe itself.
func walkNested(path string, s apiextensionsv1.JSONSchemaProps, goNames map[string]bool, problems *[]string) {
	if len(s.Properties) > 0 {
		walk(path, s, goNames, problems)
		return
	}
	if s.Description != "" {
		checkDescription(path, s.Description, goNames, problems)
	}
	if s.Items != nil && s.Items.Schema != nil {
		walkNested(path+"[]", *s.Items.Schema, goNames, problems)
	}
	if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
		walkNested(path+"{}", *s.AdditionalProperties.Schema, goNames, problems)
	}
}

func checkDescription(path, desc string, goNames map[string]bool, problems *[]string) {
	if len(strings.TrimSpace(desc)) < minDescriptionLength {
		*problems = append(*problems, fmt.Sprintf("%s: description shorter than %d characters: %q",
			path, minDescriptionLength, desc))
		return
	}
	for _, s := range goSyntaxes {
		if strings.Contains(desc, s) {
			*problems = append(*problems, fmt.Sprintf("%s: description contains Go syntax %q", path, s))
		}
	}
	for _, w := range word.FindAllString(placeholder.ReplaceAllString(desc, ""), -1) {
		if goNames[w] {
			*problems = append(*problems, fmt.Sprintf("%s: description contains Go identifier %q", path, w))
		}
	}
}

func loadCRDs(t *testing.T) []apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(crdDir, "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no CRDs found in %s: %v", crdDir, err)
	}
	crds := make([]apiextensionsv1.CustomResourceDefinition, 0, len(files))
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var crd apiextensionsv1.CustomResourceDefinition
		if err := yaml.Unmarshal(raw, &crd); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		crds = append(crds, crd)
	}
	return crds
}

// properNames are Kubernetes kinds and product names that descriptions legitimately use even where a Go type or
// field of the same spelling exists.
var properNames = []string{
	"ServiceAccount", "ConfigMap", "TokenRequest",
	"OpenBao", "HashiCorp", "PingID", "PostgreSQL", "MySQL", "MariaDB", "InfluxDB", "MongoDB", "OpenLDAP",
	"GitHub", "GitLab", "RabbitMQ", "OpenID", "JavaScript",
}

// goIdentifiers returns the CamelCase names of exported types and struct fields in the API packages that are
// neither kind names nor proper names, so a description that leaks one is caught.
func goIdentifiers(t *testing.T, crds []apiextensionsv1.CustomResourceDefinition) map[string]bool {
	t.Helper()
	kinds := map[string]bool{}
	for _, n := range properNames {
		kinds[n] = true
	}
	for _, crd := range crds {
		kinds[crd.Spec.Names.Kind] = true
	}
	files, err := filepath.Glob(filepath.Join(apiDir, "*", "*", "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no Go files found in %s: %v", apiDir, err)
	}
	names := map[string]bool{}
	add := func(id *ast.Ident) {
		if id.IsExported() && camelCase.MatchString(id.Name) && !kinds[id.Name] {
			names[id.Name] = true
		}
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasPrefix(filepath.Base(f), "zz_generated") || strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.TypeSpec:
				add(n.Name)
			case *ast.StructType:
				for _, field := range n.Fields.List {
					for _, id := range field.Names {
						add(id)
					}
				}
			}
			return true
		})
	}
	return names
}
