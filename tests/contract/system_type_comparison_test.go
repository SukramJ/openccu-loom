// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// systemTypeComparisonAllowed are the production files that may compare a
// central's system type: the one selection function, and the configuration
// rules whose meaning depends on the type.
var systemTypeComparisonAllowed = map[string]bool{
	"internal/central/adapter/south_profile.go": true,
	"internal/config/config.go":                 true,
}

// TestSystemTypeIsComparedOnlyInSouthProfileFor holds the architecture of the
// south profiles: a central's system type is looked at in exactly one place,
// [adapter.southProfileFor], and everything downstream works through the
// profile's ports. A comparison against an hmenum.SystemType value anywhere
// else is how "if lite" branches creep through shared code — each one a
// place where a CCU and an openccu-lite central can silently diverge.
//
// Comparisons are `==`/`!=` against a SystemType constant and `case` labels
// naming one. Returning a profile's own constant is not a comparison.
func TestSystemTypeIsComparedOnlyInSouthProfileFor(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	var hits []string
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			if systemTypeComparisonAllowed[rel] {
				return nil
			}
			hits = append(hits, systemTypeComparisons(t, path, rel)...)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	for _, h := range hits {
		t.Errorf("%s compares a system type outside southProfileFor — express the difference as a south-profile port instead", h)
	}
}

// systemTypeComparisons lists the comparisons against an hmenum.SystemType
// constant in one file, as "path:line".
func systemTypeComparisons(t *testing.T, path, rel string) []string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.Contains(string(src), "SystemType") {
		return nil
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var out []string
	note := func(n ast.Node) {
		out = append(out, rel+":"+itoa(fset.Position(n.Pos()).Line))
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BinaryExpr:
			if (x.Op == token.EQL || x.Op == token.NEQ) && (isSystemTypeConst(x.X) || isSystemTypeConst(x.Y)) {
				note(x)
			}
		case *ast.CaseClause:
			for _, e := range x.List {
				if isSystemTypeConst(e) {
					note(e)
				}
			}
		}
		return true
	})
	return out
}

// isSystemTypeConst reports whether e names one of hmenum's SystemType
// constants (hmenum.SystemTypeCCU, …).
func isSystemTypeConst(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "hmenum" && strings.HasPrefix(sel.Sel.Name, "SystemType")
}
