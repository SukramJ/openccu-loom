// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wiring_pins

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// centralAdoptSource is the composition seam that drives one central's
// lifecycle up and down at runtime.
const centralAdoptSource = "cmd/openccu-loom/central_adopt.go"

// TestPin_BootCentralRemovalMirrorsEveryAdoptHook pins the shape that keeps
// the two removal paths from drifting apart.
//
// A per-central domain hook is an attach/detach pair, and for a long time only
// the adopt path kept the unwires. A central the boot path registered was torn
// down through a synthesised handle carrying none of them, so the Security &
// Safety detach — the half that drops the CCU from the hazard aggregate and
// clears its fault ledger — never ran for a boot-configured CCU removed at
// runtime: its smoke/water class kept reporting active on REST, MQTT and the
// SPA, and its open faults came back after every restart.
//
// The remedy is structural rather than a second checklist: one ordered hook
// list that both paths read. This pin fails when a hook is reached outside
// that list, which is precisely how the boot path would lose it again.
func TestPin_BootCentralRemovalMirrorsEveryAdoptHook(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	path := filepath.Join("..", "..", "..", filepath.FromSlash(centralAdoptSource))
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", centralAdoptSource, err)
	}

	funcs := map[string]*ast.FuncDecl{}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
			funcs[fn.Name.Name] = fn
		}
	}

	const shared = "perCentralHooks"
	if _, ok := funcs[shared]; !ok {
		t.Fatalf("%s no longer declares %s — the one list both removal paths read", centralAdoptSource, shared)
	}
	for _, caller := range []string{"attachCentralHooks", "bootHandle"} {
		fn, ok := funcs[caller]
		if !ok {
			t.Fatalf("%s no longer declares %s", centralAdoptSource, caller)
		}
		if !callsIdent(fn.Body, shared) {
			t.Errorf("%s does not read %s: the adopted and the boot-registered central no longer get the same per-central teardown", caller, shared)
		}
	}

	// Every hook read outside the shared list is a hook the boot path cannot
	// see. Assignments in the set* installers are the one legitimate touch.
	for name, fn := range funcs {
		if name == shared || strings.HasPrefix(name, "set") {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			recv, ok := sel.X.(*ast.Ident)
			if !ok || recv.Name != "o" {
				return true
			}
			if strings.HasSuffix(sel.Sel.Name, "Hook") || strings.HasSuffix(sel.Sel.Name, "Trigger") {
				t.Errorf("%s reads o.%s directly; route it through %s so removing a boot-registered central runs its detach too",
					name, sel.Sel.Name, shared)
			}
			return true
		})
	}
}

// callsIdent reports whether body mentions the given identifier.
func callsIdent(body *ast.BlockStmt, ident string) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		if id, ok := n.(*ast.Ident); ok && id.Name == ident {
			found = true
		}
		return !found
	})
	return found
}
