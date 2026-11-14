// Command apicoverage checks api-coverage/classification.yaml against the pinned OpenBao OpenAPI snapshots and
// writes the coverage report to docs/api-coverage.md.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/alexandrust88/openbao-config-operator/hack/apicoverage/coverage"
)

func main() {
	dataDir := flag.String("data", "api-coverage", "directory with the OpenAPI snapshots and classification.yaml")
	crdDir := flag.String("crds", "config/crd/bases", "directory with the generated CRDs")
	out := flag.String("out", "docs/api-coverage.md", "report file")
	flag.Parse()

	if err := run(*dataDir, *crdDir, *out); err != nil {
		fmt.Fprintln(os.Stderr, "apicoverage:", err)
		os.Exit(1)
	}
}

func run(dataDir, crdDir, out string) error {
	d, err := coverage.Load(dataDir, crdDir)
	if err != nil {
		return err
	}
	if problems := d.Problems(); len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, "apicoverage:", p)
		}
		return fmt.Errorf("%d classification problems", len(problems))
	}
	if err := os.WriteFile(out, []byte(d.Report()), 0o644); err != nil {
		return err
	}
	for _, s := range d.Snapshots {
		c := d.Count(s)
		fmt.Printf("apicoverage: %s: %d writable, %d typed, %d endpoint-only, %d unreviewed, %d action\n",
			s.Version, c.Writable, c.Typed, c.EndpointOnly, c.Unreviewed, c.Action)
	}
	return nil
}
