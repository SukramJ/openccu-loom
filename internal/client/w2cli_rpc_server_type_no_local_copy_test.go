// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package client

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestW2CliRPCServerTypeKeepsNoLocalCopy pins the SHAPE of
// [RPCServerTypeForInterface], because its value cannot pin it.
//
// The interface→callback-server-type datum lives in pkg/hmenum in two halves
// (the InterfaceRPCServerType map plus Interface.IsBINRPC). This package used
// to restate it as a local switch over the five interface tags. A value test
// cannot detect that copy: the switch and the derivation answer identically
// for every interface hmenum declares today and for an unknown one, so
// [TestHmCliRPCServerTypeForInterfaceDerivesFromHmenum] stays green with
// either implementation. What it detects is drift AFTER the copy has already
// diverged — one release too late, and only if the copy is the one that
// changed.
//
// So the property that has to be enforced is structural: this package
// consults hmenum and does not enumerate the mapping itself. The check reads
// the function's AST rather than its output — restoring the switch turns it
// red immediately, whatever the switch happens to return.
func TestW2CliRPCServerTypeKeepsNoLocalCopy(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	var derivation *ast.FuncDecl
	checkedFiles := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		checkedFiles++

		// No file in this package may declare its own
		// map[hmenum.Interface]hmenum.RPCServerType — that is the other
		// shape a second copy takes.
		ast.Inspect(f, func(n ast.Node) bool {
			mt, ok := n.(*ast.MapType)
			if !ok {
				return true
			}
			if w2CliSelectorName(mt.Key) == "hmenum.Interface" && w2CliSelectorName(mt.Value) == "hmenum.RPCServerType" {
				t.Errorf("%s:%d declares a map[hmenum.Interface]hmenum.RPCServerType: the mapping belongs to pkg/hmenum (InterfaceRPCServerType + Interface.IsBINRPC), and a second home for it drifts without any test noticing",
					name, fset.Position(mt.Pos()).Line)
			}
			return true
		})

		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Recv == nil && fn.Name.Name == "RPCServerTypeForInterface" {
				derivation = fn
			}
		}
	}
	if checkedFiles == 0 {
		t.Fatal("no non-test .go file in the package dir — the guard would pass vacuously")
	}
	if derivation == nil {
		t.Fatal("RPCServerTypeForInterface not found in package client — the guard would pass vacuously")
	}

	// The body must consult both hmenum halves…
	var usesMap, usesIsBINRPC bool
	ast.Inspect(derivation.Body, func(n ast.Node) bool {
		if w2CliSelectorName(n) == "hmenum.InterfaceRPCServerType" {
			usesMap = true
		}
		if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "IsBINRPC" {
			usesIsBINRPC = true
		}
		return true
	})
	if !usesMap {
		t.Errorf("RPCServerTypeForInterface no longer reads hmenum.InterfaceRPCServerType (%s): the answer must be derived from pkg/hmenum, not restated here",
			fset.Position(derivation.Pos()))
	}
	if !usesIsBINRPC {
		t.Errorf("RPCServerTypeForInterface no longer calls Interface.IsBINRPC (%s): without it CUxD answers RPCServerTypeNone, because the hmenum map models the XML-RPC listener only",
			fset.Position(derivation.Pos()))
	}

	// …and must not enumerate the interfaces itself.
	ast.Inspect(derivation.Body, func(n ast.Node) bool {
		sw, ok := n.(*ast.SwitchStmt)
		if !ok {
			return true
		}
		t.Errorf("%s: RPCServerTypeForInterface switches over interface tags again — that is the second copy of the pkg/hmenum mapping this package was freed of; it answers identically today, which is exactly why no value test reports it",
			fset.Position(sw.Pos()))
		return true
	})
}

// w2CliSelectorName renders a qualified identifier ("pkg.Name") or returns
// the empty string for any other node.
func w2CliSelectorName(n ast.Node) string {
	sel, ok := n.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}
	return pkg.Name + "." + sel.Sel.Name
}
