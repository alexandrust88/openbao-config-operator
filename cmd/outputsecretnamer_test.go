package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryOutputWriterNamesItsSecret: the engine refuses an output Secret the resource does not own before it
// writes to OpenBao only for adapters that implement engine.OutputSecretNamer. An OutputWriter without it would
// generate a one-time value that can then never be delivered, so every adapter with Outputs needs OutputSecretName.
func TestEveryOutputWriterNamesItsSecret(t *testing.T) {
	methods := map[string]map[string]bool{}
	fset := token.NewFileSet()
	err := filepath.WalkDir("../internal/controller", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) == 0 {
				continue
			}
			recv := fn.Recv.List[0].Type
			if star, ok := recv.(*ast.StarExpr); ok {
				recv = star.X
			}
			ident, ok := recv.(*ast.Ident)
			if !ok {
				continue
			}
			typ := filepath.Base(filepath.Dir(path)) + "." + ident.Name
			if methods[typ] == nil {
				methods[typ] = map[string]bool{}
			}
			methods[typ][fn.Name.Name] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	writers := 0
	for typ, m := range methods {
		if !m["Outputs"] {
			continue
		}
		writers++
		if !m["OutputSecretName"] {
			t.Errorf("%s implements engine.OutputWriter but not engine.OutputSecretNamer", typ)
		}
	}
	if writers == 0 {
		t.Fatal("found no OutputWriter adapters under internal/controller")
	}
}
