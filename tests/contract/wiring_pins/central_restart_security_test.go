// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

// securityWiringSource is where the composition root adapts the Security &
// Safety domain onto the orchestrator's per-central hook.
const securityWiringSource = "cmd/openccu-loom/security_wiring.go"

// TestPin_CentralRestartReachesSecurityRestartDetach pins the chain that keeps
// a restarted central's open security faults standing.
//
// A restart used to run the same per-central teardown as a removal, so the
// security domain closed every open fault of the central, and the re-adopted
// central raised the still-active ones again: new ids, a reset `since`, the
// acknowledgement lost and a duplicate "raised" report to every sink. The
// chain has four links, and dropping any one of them silently brings the
// removal back: restartCentral tears down in restart mode, the teardown hands
// that mode to every per-central unwire, the hook maps restart mode onto its
// restart half, and the security hook's restart half is the domain's
// ledger-keeping detach.
func TestPin_CentralRestartReachesSecurityRestartDetach(t *testing.T) {
	t.Parallel()

	adopt := parsePinFuncs(t, centralAdoptSource)

	restart, ok := adopt["restartCentral"]
	if !ok {
		t.Fatalf("%s no longer declares restartCentral", centralAdoptSource)
	}
	if !callsWithIdentArg(restart.Body, "teardownCentralLocked", "teardownRestart") {
		t.Error("restartCentral does not tear the central down with teardownRestart: a restart runs the " +
			"removal teardown, and the security domain closes and re-raises every open fault of the central")
	}

	teardown, ok := adopt["teardownCentralLocked"]
	if !ok {
		t.Fatalf("%s no longer declares teardownCentralLocked", centralAdoptSource)
	}
	mode := secondParamName(teardown)
	if mode == "" {
		t.Fatal("teardownCentralLocked no longer takes the teardown mode as its second parameter")
	}
	if !callsWithIdentArg(teardown.Body, "unwire", mode) {
		t.Errorf("teardownCentralLocked does not hand %s to the per-central unwires: every domain is torn "+
			"down as for a removal whatever the caller asked for", mode)
	}

	teardownFor, ok := adopt["teardownFor"]
	if !ok {
		t.Fatalf("%s no longer declares teardownFor, the one mapping of a teardown mode onto a hook", centralAdoptSource)
	}
	if !callsIdent(teardownFor.Body, "teardownRestart") || !callsIdent(teardownFor.Body, "detachForRestart") {
		t.Error("teardownFor no longer routes teardownRestart to the hook's detachForRestart")
	}

	security := parsePinFuncs(t, securityWiringSource)
	hook, ok := security["securityCentralHook"]
	if !ok {
		t.Fatalf("%s no longer declares securityCentralHook", securityWiringSource)
	}
	if !setsFieldToSelector(hook.Body, "detachForRestart", "DetachCentralForRestart") {
		t.Error("securityCentralHook does not set detachForRestart to the domain's DetachCentralForRestart: " +
			"a restart closes every open security fault of the central")
	}
}

// parsePinFuncs parses a repository file and returns its function
// declarations by name.
func parsePinFuncs(t *testing.T, rel string) map[string]*ast.FuncDecl {
	t.Helper()
	fset := token.NewFileSet()
	path := filepath.Join("..", "..", "..", filepath.FromSlash(rel))
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	funcs := map[string]*ast.FuncDecl{}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
			funcs[fn.Name.Name] = fn
		}
	}
	return funcs
}

// callsWithIdentArg reports whether body calls fn (a plain or selector call)
// with the identifier arg among its arguments.
func callsWithIdentArg(body *ast.BlockStmt, fn, arg string) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || found {
			return !found
		}
		var name string
		switch f := call.Fun.(type) {
		case *ast.Ident:
			name = f.Name
		case *ast.SelectorExpr:
			name = f.Sel.Name
		}
		if name != fn {
			return true
		}
		for _, a := range call.Args {
			if id, ok := a.(*ast.Ident); ok && id.Name == arg {
				found = true
			}
		}
		return !found
	})
	return found
}

// secondParamName returns the name of fn's second parameter, or "".
func secondParamName(fn *ast.FuncDecl) string {
	var names []string
	for _, field := range fn.Type.Params.List {
		for _, n := range field.Names {
			names = append(names, n.Name)
		}
	}
	if len(names) < 2 {
		return ""
	}
	return names[1]
}

// setsFieldToSelector reports whether body holds a composite-literal field
// key set to a selector ending in sel.
func setsFieldToSelector(body *ast.BlockStmt, key, sel string) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok || found {
			return !found
		}
		k, ok := kv.Key.(*ast.Ident)
		if !ok || k.Name != key {
			return true
		}
		if v, ok := kv.Value.(*ast.SelectorExpr); ok && v.Sel.Name == sel {
			found = true
		}
		return !found
	})
	return found
}
