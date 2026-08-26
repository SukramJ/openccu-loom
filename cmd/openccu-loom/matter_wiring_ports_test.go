// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryMatterRESTPortIsAssignedAndForwarded closes the gap that this
// struct sits in: `matterWiring` is a plain struct, so a port that is
// declared and never assigned compiles, passes every handler test, and
// answers its route with 503 forever.
//
// Every REST handler on the Matter surface is nil-gated — that is what
// keeps a disabled bridge from panicking. The same gate makes a wiring
// mistake indistinguishable from "Matter is off": the operator sees a
// button that always fails, and no test anywhere goes red.
//
// The wiring guard in tests/contract/ resolves Set*/Attach*/Register*
// methods and cannot see a struct-field assignment, so this check reads
// the composition root directly and asserts both halves:
//
//   - every handlers.Matter* field is assigned somewhere in production
//     code (i.e. wireMatterRuntime populates it), and
//   - every one of them is forwarded into rest.Deps (i.e. the router
//     ever sees it).
//
// Either half missing produces the same silent-503 feature.
func TestEveryMatterRESTPortIsAssignedAndForwarded(t *testing.T) {
	t.Parallel()

	files := parseProductionFiles(t)
	ports := matterWiringPortFields(files)
	if len(ports) == 0 {
		t.Fatal("no handlers.Matter* fields found on matterWiring — the guard has lost its subject")
	}

	assigned := map[string]bool{}
	forwarded := map[string]bool{}
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.AssignStmt:
				for _, lhs := range node.Lhs {
					if sel, ok := lhs.(*ast.SelectorExpr); ok {
						assigned[sel.Sel.Name] = true
					}
				}
			case *ast.KeyValueExpr:
				// `MatterFabricPurger: d.matter.fabricPurger` — the
				// value side names the wiring field.
				if sel, ok := node.Value.(*ast.SelectorExpr); ok {
					if inner, ok := sel.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "matter" {
						forwarded[sel.Sel.Name] = true
					}
				}
			}
			return true
		})
	}

	for _, field := range ports {
		if !assigned[field] {
			t.Errorf("matterWiring.%s is never assigned in production code — its routes answer "+
				"503 on every request, which is indistinguishable from a disabled bridge", field)
		}
		if !forwarded[field] {
			t.Errorf("matterWiring.%s never reaches rest.Deps — the router never sees the port, "+
				"so the handler stays nil-gated however well it is wired", field)
		}
	}
}

// parseProductionFiles parses every non-test Go file of this package.
func parseProductionFiles(t *testing.T) []*ast.File {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	fset := token.NewFileSet()
	var out []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		out = append(out, f)
	}
	return out
}

// matterWiringPortFields returns the names of the matterWiring fields
// whose type is one of the handlers package's Matter ports. Fields of
// other types (the event publisher, the cluster reference, the central
// hook) are wired through paths this guard does not model.
func matterWiringPortFields(files []*ast.File) []string {
	var out []string
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok || ts.Name.Name != "matterWiring" {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return false
			}
			for _, f := range st.Fields.List {
				sel, ok := f.Type.(*ast.SelectorExpr)
				if !ok {
					continue
				}
				ident, ok := sel.X.(*ast.Ident)
				if !ok || ident.Name != "handlers" || !strings.HasPrefix(sel.Sel.Name, "Matter") {
					continue
				}
				for _, name := range f.Names {
					out = append(out, name.Name)
				}
			}
			return false
		})
	}
	return out
}
