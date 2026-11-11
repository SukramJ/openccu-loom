// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestValidateRunsTheFiniteFloatWalk pins, in the source, that
// validateFieldRanges — the range pass Validate ends with — hands the config
// it was given to validateFiniteFloats.
//
// A behavioural test cannot show it today: the one float leaf the config
// carries has a finite check of its own that runs earlier, so Validate
// rejects a NaN there with or without the walk. The walk exists for the next
// float leaf added without such a check, and that is exactly the case where
// a dropped call would go unnoticed.
func TestValidateRunsTheFiniteFloatWalk(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "validate.go", nil, 0)
	if err != nil {
		t.Fatalf("parse validate.go: %v", err)
	}
	var found, sawRangePass bool
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "validateFieldRanges" || fn.Recv != nil ||
			len(fn.Type.Params.List) != 1 || len(fn.Type.Params.List[0].Names) != 1 {
			continue
		}
		sawRangePass = true
		receiver := fn.Type.Params.List[0].Names[0].Name
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			callee, ok := call.Fun.(*ast.Ident)
			if !ok || callee.Name != "validateFiniteFloats" || len(call.Args) != 1 {
				return true
			}
			if arg, ok := call.Args[0].(*ast.Ident); ok && arg.Name == receiver {
				found = true
			}
			return true
		})
	}
	if !sawRangePass {
		t.Fatal("no validateFieldRanges(c) in validate.go — the range pass moved; point this test at its new home")
	}
	if !found {
		t.Error("validateFieldRanges does not call validateFiniteFloats on its config; a non-finite float in a leaf without its own check would load")
	}
}
