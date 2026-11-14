package importer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

// WriteStream writes objs to w as one YAML stream.
func WriteStream(w io.Writer, objs []client.Object) error {
	for i, obj := range objs {
		doc, err := encode(obj)
		if err != nil {
			return err
		}
		if i > 0 {
			if _, err := io.WriteString(w, "---\n"); err != nil {
				return err
			}
		}
		if _, err := w.Write(doc); err != nil {
			return err
		}
	}
	return nil
}

// WriteDir writes objs into dir, one YAML stream per kind named <group>-<kind>.yaml, and returns the files written.
func WriteDir(dir string, objs []client.Object) ([]string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	var order []string
	byFile := map[string][]client.Object{}
	for _, obj := range objs {
		gvk := obj.GetObjectKind().GroupVersionKind()
		group, _, _ := strings.Cut(gvk.Group, ".")
		file := filepath.Join(dir, strings.ToLower(group+"-"+gvk.Kind)+".yaml")
		if _, ok := byFile[file]; !ok {
			order = append(order, file)
		}
		byFile[file] = append(byFile[file], obj)
	}
	for _, file := range order {
		var buf bytes.Buffer
		if err := WriteStream(&buf, byFile[file]); err != nil {
			return nil, err
		}
		if err := os.WriteFile(file, buf.Bytes(), 0o600); err != nil {
			return nil, err
		}
		if err := os.Chmod(file, 0o600); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// encode marshals obj without the empty status and the null creationTimestamp a typed object always carries.
func encode(obj client.Object) ([]byte, error) {
	raw, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", obj.GetName(), err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	delete(doc, "status")
	if meta, ok := doc["metadata"].(map[string]any); ok {
		delete(meta, "creationTimestamp")
	}
	return yaml.Marshal(doc)
}
