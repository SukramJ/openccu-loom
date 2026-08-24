// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wiring_pins

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestEverySeamAttachWrapsItsHandover pins that a wiring seam's Attach
// closure actually performs the handover it declares.
//
// ADR 0065's mechanism is that the declaration and the work are the same
// statement, so removing the work removes the declaration and
// /api/v1/diagnostics/wiring stops reporting the seam. An empty closure
// breaks exactly that: the seam declares itself, the handover sits beside
// it, and deleting the handover leaves the running daemon still reporting
// the seam as wired. The endpoint then answers confidently about a
// collaborator nobody installed — worse than not having the endpoint,
// because an operator reading it has no reason to doubt it.
//
// mqtt.hub_ready_restart was attached with `func() {}` for exactly this
// reason: the subscription it names was set up in the following statement.
//
// Both declaring shapes are walked. Manifest.Attach takes the handover as
// its closure; Registry.OnRegisterDeclared takes the per-central observer
// as its closure, and an empty observer is the same defect one phase
// later — the seam is declared for every central and does nothing for any
// of them.
//
// Blind spot, stated rather than left to be discovered: twelve of the
// thirty-three seams hand over a named method (`s.StartCentral`) instead
// of a closure, and those are not inspected. A named function cannot be
// empty the way `func() {}` is — naming it is already the handover — so
// the shape this guard rules out is unreachable there. A stubbed-out
// method body is a different defect and needs a different check.
func TestEverySeamAttachWrapsItsHandover(t *testing.T) {
	t.Parallel()

	root := repoRootFromPin(t)
	var empty []string
	var seen, inspected int

	for _, dir := range []string{"cmd/openccu-loom", "internal"} {
		walkGoFiles(t, filepath.Join(root, dir), func(path string, file *ast.File, fset *token.FileSet) {
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) != 2 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || (sel.Sel.Name != "Attach" && sel.Sel.Name != "OnRegisterDeclared") {
					return true
				}
				name := seamNameOf(call.Args[0])
				if name == "" {
					return true
				}
				seen++
				lit, ok := call.Args[1].(*ast.FuncLit)
				if !ok || lit.Body == nil {
					return true
				}
				inspected++
				if len(lit.Body.List) == 0 {
					rel, _ := filepath.Rel(root, path)
					empty = append(empty, name+" ("+rel+":"+
						strconv.Itoa(fset.Position(call.Pos()).Line)+")")
				}
				return true
			})
		})
	}

	if seen < 30 {
		t.Fatalf("found only %d seam declarations — the walk is wrong, and a guard that "+
			"sees too few seams passes by measuring nothing", seen)
	}
	t.Logf("%d seam declarations; %d hand over a closure this guard inspects, "+
		"%d hand over a named method it does not", seen, inspected, seen-inspected)
	if len(empty) > 0 {
		t.Errorf("%d seam(s) declare themselves and wrap no work:\n  %s\n\n"+
			"Move the handover inside the Attach closure. While it sits beside the "+
			"declaration, deleting it leaves the daemon reporting the seam as wired "+
			"on /api/v1/diagnostics/wiring — which is the state the manifest exists "+
			"to make impossible.",
			len(empty), strings.Join(empty, "\n  "))
	}
}

// seamNameOf returns the Name field of a wiring.Seam composite literal, or
// "" when the argument is not one.
func seamNameOf(arg ast.Expr) string {
	cl, ok := arg.(*ast.CompositeLit)
	if !ok {
		return ""
	}
	sel, ok := cl.Type.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Seam" {
		return ""
	}
	for _, el := range cl.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "Name" {
			continue
		}
		if lit, ok := kv.Value.(*ast.BasicLit); ok {
			return strings.Trim(lit.Value, `"`)
		}
	}
	return ""
}

func walkGoFiles(t *testing.T, root string, fn func(string, *ast.File, *token.FileSet)) {
	t.Helper()
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			// A file that does not parse cannot hide a seam declaration
			// from this walk — the compiler rejects it long before a
			// guard would. Skipping is safe; failing here would make an
			// unrelated syntax error surface as a wiring finding.
			return nil //nolint:nilerr // a syntactically broken file carries no reachable seam
		}
		fn(path, f, fset)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}

// repoRootFromPin resolves the repository root from this file's location
// (tests/contract/wiring_pins/ → three levels up).
func repoRootFromPin(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	abs, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
	if err != nil {
		t.Fatalf("repo root resolution: %v", err)
	}
	return abs
}
