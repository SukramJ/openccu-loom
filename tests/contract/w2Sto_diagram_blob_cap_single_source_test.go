// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"testing"
)

// TestW2StoDiagramBlobCapHasOneValue pins the two literals that cap a diagram
// config blob to one value.
//
// The cap is written twice, in two packages that do not import each other, and
// the second one says so only in prose:
// internal/north/rest/handlers/diagram_configs.go declares `maxDiagramBytes`
// under the comment "caps a diagram config blob mirroring the store limit",
// while internal/store/sqlite/diagram_configs.go declares the limit it claims
// to mirror as `maxDiagramBlobSize`. Nothing in the compiler or the suite
// connects the two literals, so "mirroring" is a hypothesis until measured.
//
// Both directions of a drift are operator-visible, and neither is loud:
//
//   - handler cap below the store cap — a diagram the store would accept never
//     reaches it. The body is truncated by the io.LimitReader at
//     handlers/diagram_configs.go:148 and answered as a 413, so the operator is
//     told the payload is too large by a limit no error message names.
//   - handler cap above the store cap — the request arrives, the store rejects
//     it, and the operator gets the store's raw English sentence
//     ("sqlite: diagram invalid: config too large") as the problem detail
//     instead of the handler's own size answer.
//
// The two caps are read out of the source rather than imported because the
// handler constant is unexported and the handler package is deliberately
// store-agnostic (its sentinels are translated by the cmd-side adapter). A
// source pin is what keeps the prose claim honest without coupling the
// packages.
//
// What this guard does NOT assert, because the two caps do not measure the
// same bytes: the handler's cap is over the whole request body (name,
// visibility and the config blob together), the store's is over the config
// blob alone. With equal literals the handler's is therefore the binding one
// on the REST path, and the store's cap is only reachable by a non-REST
// caller. Equality is what the handler's comment claims, so equality is what
// is pinned; the difference in scope is recorded here so a future reader does
// not mistake this test for a claim that the store cap is reachable.
func TestW2StoDiagramBlobCapHasOneValue(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)

	const (
		storeConst   = "maxDiagramBlobSize"
		handlerConst = "maxDiagramBytes"
	)
	storeFile := filepath.Join(root, "internal", "store", "sqlite", "diagram_configs.go")
	handlerFile := filepath.Join(root, "internal", "north", "rest", "handlers", "diagram_configs.go")

	storeCap := w2StoIntConst(t, storeFile, storeConst)
	handlerCap := w2StoIntConst(t, handlerFile, handlerConst)

	if storeCap != handlerCap {
		t.Errorf("diagram blob cap has two values: %s = %d in %s, %s = %d in %s. "+
			"The handler constant is documented as mirroring the store limit; a drift "+
			"either rejects a diagram the store would accept (handler smaller, 413 from "+
			"an unnamed limit) or lets the store answer for the size (handler larger, "+
			"the store's raw English sentence in the operator's toast).",
			storeConst, storeCap, storeFile, handlerConst, handlerCap, handlerFile)
	}
}

// w2StoIntConst returns the value of the named untyped integer constant
// declared at package level in file. It evaluates the literal expressions the
// two caps are actually written as — a plain integer, or a product/sum/shift
// of integers such as `64 * 1024` — and fails rather than guessing on anything
// else, so a cap rewritten as a reference to some other identifier is reported
// instead of silently reading as zero.
func w2StoIntConst(t *testing.T, file, name string) int64 {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, ident := range vs.Names {
				if ident.Name != name || i >= len(vs.Values) {
					continue
				}
				v, ok := w2StoEvalInt(vs.Values[i])
				if !ok {
					t.Fatalf("%s: const %s is not an integer literal expression; "+
						"this guard cannot compare it — either keep it a literal or "+
						"replace this guard with one that can read its new form",
						file, name)
				}
				return v
			}
		}
	}
	t.Fatalf("%s: const %s not found — it was renamed or removed, and the cap it "+
		"pinned is no longer measured", file, name)
	return 0
}

// w2StoEvalInt evaluates the integer literal expressions the caps are written
// as. Anything else returns false.
func w2StoEvalInt(expr ast.Expr) (int64, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.INT {
			return 0, false
		}
		v, err := strconv.ParseInt(e.Value, 0, 64)
		if err != nil {
			return 0, false
		}
		return v, true
	case *ast.ParenExpr:
		return w2StoEvalInt(e.X)
	case *ast.BinaryExpr:
		x, okX := w2StoEvalInt(e.X)
		y, okY := w2StoEvalInt(e.Y)
		if !okX || !okY {
			return 0, false
		}
		switch e.Op {
		case token.MUL:
			return x * y, true
		case token.ADD:
			return x + y, true
		case token.SHL:
			return x << y, true
		default:
			return 0, false
		}
	default:
		return 0, false
	}
}
