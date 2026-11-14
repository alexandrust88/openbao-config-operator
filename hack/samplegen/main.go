// Command samplegen writes examples-generated/<group>/<kind>.yaml, a sample with every spec field set, for every
// openbaoconfig.io CRD in config/crd/bases.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/alexandrust88/openbao-config-operator/hack/samplegen/gen"
)

func main() {
	crdDir := flag.String("crds", "config/crd/bases", "directory with the generated CRDs")
	outDir := flag.String("out", "examples-generated", "directory the samples are written to")
	flag.Parse()

	samples, err := gen.Generate(*crdDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "samplegen:", err)
		os.Exit(1)
	}
	if err := gen.Write(*outDir, samples); err != nil {
		fmt.Fprintln(os.Stderr, "samplegen:", err)
		os.Exit(1)
	}
	warned := 0
	for _, s := range samples {
		for _, w := range s.Warnings {
			fmt.Fprintf(os.Stderr, "samplegen: warning: %s/%s: %s\n", s.Group, s.Kind, w)
		}
		if len(s.Warnings) > 0 {
			warned++
		}
	}
	fmt.Printf("samplegen: wrote %d samples to %s (%d with warnings)\n", len(samples), *outDir, warned)
}
