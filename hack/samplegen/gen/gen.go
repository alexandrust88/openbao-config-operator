// Package gen builds one sample custom resource per openbaoconfig.io CRD with every spec field set.
package gen

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
	groupSuffix    = ".openbaoconfig.io"
	fakeSecret     = "fake-value-not-a-secret"
)

// Sample is the generated manifest of one kind: the custom resource followed by the Secrets it references.
type Sample struct {
	Group      string
	Kind       string
	Namespaced bool
	Docs       []map[string]any
	Warnings   []string
}

// File is the path of the sample relative to the output directory.
func (s Sample) File() string {
	return filepath.Join(strings.TrimSuffix(s.Group, groupSuffix), strings.ToLower(s.Kind)+".yaml")
}

// Generate builds a sample for every *.openbaoconfig.io_*.yaml CRD in crdDir, sorted by file name.
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
		Kind:       crd.Spec.Names.Kind,
		Namespaced: crd.Spec.Scope == apiextensionsv1.NamespaceScoped,
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

// Write writes every sample under outDir and removes YAML files of kinds that no longer exist.
func Write(outDir string, samples []Sample) error {
	keep := map[string]bool{}
	for _, s := range samples {
		b, err := Marshal(s)
		if err != nil {
			return fmt.Errorf("%s: %w", s.Kind, err)
		}
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
