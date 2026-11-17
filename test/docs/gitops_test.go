//go:build docs

package docs

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
)

// Validate the actual generated lab objects against CRD CEL before building a kind cluster.
func gitopsFixtures(t *testing.T) []source {
	t.Helper()
	command := exec.Command("python3", "-c",
		"import common,json; print(json.dumps([common.fixtures(1), common.fixtures(2)]))")
	command.Dir = filepath.Join(root, "hack", "lab", "gitops")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("render GitOps configuration fixtures: %v", err)
	}
	var variants [][]map[string]any
	if err := json.Unmarshal(output, &variants); err != nil {
		t.Fatal(err)
	}
	if len(variants) != 2 || len(variants[0]) == 0 || len(variants[1]) == 0 {
		t.Fatal("GitOps renderer must produce both nonempty policy revisions")
	}
	var sources []source
	for version, objects := range variants {
		name := fmt.Sprintf("hack/lab/gitops/common.py:fixtures-v%d", version+1)
		s := source{name: name}
		for i, object := range objects {
			s.manifests = append(s.manifests, manifest{file: name, line: i + 1, obj: object})
		}
		sources = append(sources, s)
	}
	return sources
}
