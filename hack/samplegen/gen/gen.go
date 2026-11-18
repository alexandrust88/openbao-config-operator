// Package gen builds one sample custom resource per obco.dev CRD with every spec field set.
package gen

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/yaml"
)

const (
	// SampleNamespace is the Kubernetes namespace of every namespaced sample and referenced Secret.
	SampleNamespace = "openbao-config"
	// SampleName is the metadata.name of every sample.
	SampleName     = "example"
	connectionName = "openbao"
	groupSuffix    = ".obco.dev"
	fakeSecret     = "fake-value-not-a-secret"
	headerWidth    = 118
)

// Sample is the generated manifest of one kind: the custom resource followed by the Secrets it references.
type Sample struct {
	Group      string
	Version    string
	Kind       string
	Namespaced bool
	// Summary is the first sentence of the CRD description.
	Summary string
	// Required lists the required spec fields, sorted.
	Required []string
	Docs     []map[string]any
	Warnings []string
}

// Links are the targets the header comment of every sample points at.
type Links struct {
	// Site is the root URL of the documentation site, which serves the API reference under reference/api/.
	Site string
	// Examples is the directory of the curated examples, as written in the header (relative to the repository).
	Examples string
}

// File is the path of the sample relative to the output directory.
func (s Sample) File() string {
	return filepath.Join(strings.TrimSuffix(s.Group, groupSuffix), strings.ToLower(s.Kind)+".yaml")
}

// Generate builds a sample for every *.obco.dev_*.yaml CRD in crdDir, sorted by file name.
func Generate(crdDir string) ([]Sample, error) {
	files, err := filepath.Glob(filepath.Join(crdDir, "*"+groupSuffix+"_*.yaml"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	samples := make([]Sample, 0, len(files))
	for _, f := range files {
		crd, err := readCRD(f)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		s, err := generateSample(crd)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		samples = append(samples, s)
	}
	return samples, nil
}

func readCRD(path string) (*apiextensionsv1.CustomResourceDefinition, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	crd := &apiextensionsv1.CustomResourceDefinition{}
	if err := yaml.Unmarshal(raw, crd); err != nil {
		return nil, err
	}
	return crd, nil
}

func storageVersion(
	crd *apiextensionsv1.CustomResourceDefinition,
) (apiextensionsv1.CustomResourceDefinitionVersion, error) {
	for _, v := range crd.Spec.Versions {
		if v.Storage {
			if v.Schema == nil || v.Schema.OpenAPIV3Schema == nil {
				return v, fmt.Errorf("version %s has no schema", v.Name)
			}
			return v, nil
		}
	}
	return apiextensionsv1.CustomResourceDefinitionVersion{}, fmt.Errorf("no storage version")
}

func generateSample(crd *apiextensionsv1.CustomResourceDefinition) (Sample, error) {
	version, err := storageVersion(crd)
	if err != nil {
		return Sample{}, err
	}
	root := &apiextensions.JSONSchemaProps{}
	v1 := version.Schema.OpenAPIV3Schema
	if err := apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(v1, root, nil); err != nil {
		return Sample{}, err
	}
	s := Sample{
		Group:      crd.Spec.Group,
		Version:    version.Name,
		Kind:       crd.Spec.Names.Kind,
		Namespaced: crd.Spec.Scope == apiextensionsv1.NamespaceScoped,
		Summary:    firstSentence(root.Description),
	}
	metadata := map[string]any{nameKey: SampleName}
	if s.Namespaced {
		metadata["namespace"] = SampleNamespace
	}
	obj := map[string]any{
		"apiVersion": crd.Spec.Group + "/" + version.Name,
		"kind":       s.Kind,
		"metadata":   metadata,
	}
	g := &generator{kind: s.Kind}
	if spec, ok := root.Properties["spec"]; ok {
		obj["spec"] = g.value(&spec, "spec", "", field.NewPath("spec"))
		s.Required = slices.Sorted(slices.Values(spec.Required))
	}
	if err := g.validateRoot(root, obj); err != nil {
		return Sample{}, err
	}
	s.Docs = append([]map[string]any{obj}, g.secretDocs(root, obj)...)
	s.Warnings = g.warnings
	return s, nil
}

// Marshal renders the sample as a multi-document YAML stream with sorted keys.
func Marshal(s Sample) ([]byte, error) {
	var buf bytes.Buffer
	for i, doc := range s.Docs {
		if i > 0 {
			buf.WriteString("---\n")
		}
		b, err := yaml.Marshal(doc)
		if err != nil {
			return nil, err
		}
		buf.Write(b)
	}
	return buf.Bytes(), nil
}

// Header is the comment block written above a sample: what the kind is, what the sample is and is not, and where
// the reference and the curated example are. curated is the curated example path, or "" when there is none.
func Header(s Sample, links Links, curated string) string {
	short := strings.TrimSuffix(s.Group, groupSuffix)
	var b strings.Builder
	fmt.Fprintf(&b, "# %s (%s/%s)\n", s.Kind, s.Group, s.Version)
	writeWrapped(&b, s.Summary)
	b.WriteString("#\n")
	text := "Generated from the CRD by hack/samplegen (make samples); do not edit. Every spec field is set to a " +
		"placeholder that passes the CRD's OpenAPI and CEL validation, so the sample shows the shape of the kind, " +
		"not a working configuration."
	if len(s.Docs) > 1 {
		text += " The Secrets after it hold fake values for the secret references."
	}
	required := "none"
	if len(s.Required) > 0 {
		required = strings.Join(s.Required, ", ")
	}
	writeWrapped(&b, text+" Required spec fields: "+required+".")
	b.WriteString("#\n")
	fmt.Fprintf(&b, "# Reference: %s/reference/api/%s/#%s\n", strings.TrimSuffix(links.Site, "/"), short,
		strings.ToLower(s.Kind))
	if curated != "" {
		fmt.Fprintf(&b, "# Curated example: %s\n", curated)
	}
	return b.String()
}

func writeWrapped(b *strings.Builder, text string) {
	line := "#"
	for word := range strings.FieldsSeq(text) {
		if len(line)+1+len(word) > headerWidth && line != "#" {
			b.WriteString(line + "\n")
			line = "#"
		}
		line += " " + word
	}
	b.WriteString(line + "\n")
}

// firstSentence returns the first sentence of the first paragraph of a CRD description, on one line.
func firstSentence(description string) string {
	para, _, _ := strings.Cut(strings.TrimSpace(description), "\n\n")
	para = strings.Join(strings.Fields(para), " ")
	if i := strings.Index(para, ". "); i >= 0 {
		return para[:i+1]
	}
	return para
}

// Write writes every sample, with its header comment, under outDir and removes YAML files of kinds that no longer
// exist.
func Write(outDir string, links Links, samples []Sample) error {
	keep := map[string]bool{}
	for _, s := range samples {
		b, err := Marshal(s)
		if err != nil {
			return fmt.Errorf("%s: %w", s.Kind, err)
		}
		curated := filepath.ToSlash(filepath.Join(links.Examples, s.File()))
		if _, err := os.Stat(curated); err != nil {
			curated = ""
		}
		b = append([]byte(Header(s, links, curated)), b...)
		path := filepath.Join(outDir, s.File())
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, b, 0o644); err != nil {
			return err
		}
		keep[path] = true
	}
	return filepath.WalkDir(outDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".yaml" || keep[path] {
			return err
		}
		return os.Remove(path)
	})
}
