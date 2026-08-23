// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// wireFunctionsWithoutCaller records exported Wire* constructors that
// deliberately have no composition-root call site, with the reason. An entry
// here is a decision, not a deferral.
var wireFunctionsWithoutCaller = map[string]string{}

// TestEveryWireFunctionHasAProductionCaller closes the hole its sibling
// TestEveryWiringSetterHasAProductionCaller cannot reach.
//
// That guard models a wiring seam as a METHOD — it keys everything on the
// receiver type, so it can only see `Set*` / `Attach*` / `Register*` / `With*`
// on a struct. The adapter package's other wiring shape is a free function:
// `WireValuesCacheEviction`, `WireMasterValuesEviction`,
// `WireChannelFlagsEviction` and friends take the registry plus a store and
// subscribe an observer. Nothing checked that the composition root ever calls
// one.
//
// It cost a real defect. `WireChannelFlagsEviction` shipped complete — with a
// store method, an overlay method, unit tests and a doc comment reading
// "Called on device-remove / unpair" — and no line in `cmd/openccu-loom`
// invoking it. Every test passed. An operator's Hidden/Locked overrides
// outlived the device being unpaired, and a replacement paired into the same
// address (routine: the CCU reuses addresses) silently inherited them.
//
// A behaviour test cannot catch that, because a behaviour test wires the
// collaboration itself and so proves only that the collaboration CAN happen.
// This one asks the only question that distinguishes a live seam from a
// dead one: does anything in the composition root call it.
func TestEveryWireFunctionHasAProductionCaller(t *testing.T) {
	root := repoRootForWiring(t)

	declared := map[string]string{} // name -> file it is declared in
	for _, pkgDir := range []string{
		filepath.Join(root, "internal", "central", "adapter"),
		filepath.Join(root, "internal", "central"),
		filepath.Join(root, "internal", "north", "mqtt"),
	} {
		for name, file := range exportedWireFuncs(t, pkgDir) {
			declared[name] = file
		}
	}
	if len(declared) == 0 {
		t.Fatal("found no exported Wire* functions at all — the scan is measuring nothing")
	}

	// "Called by production" is the real question, not "called by cmd": several
	// of these constructors are legitimately invoked by a sibling wiring
	// function inside the adapter package, which the composition root then
	// calls once. Scoping the scan to cmd/ alone would report those as dead.
	called := map[string]bool{}
	for _, dir := range []string{
		filepath.Join(root, "cmd"),
		filepath.Join(root, "internal"),
	} {
		for name := range calledIdentifiersTree(t, dir) {
			called[name] = true
		}
	}

	var orphans []string
	for name, file := range declared {
		if _, exempt := wireFunctionsWithoutCaller[name]; exempt {
			continue
		}
		if called[name] {
			continue
		}
		rel, err := filepath.Rel(root, file)
		if err != nil {
			rel = file
		}
		orphans = append(orphans, name+"  ("+rel+")")
	}

	if len(orphans) > 0 {
		sort.Strings(orphans)
		t.Errorf("%d exported Wire* constructor(s) that no production code calls:\n  %s\n\n"+
			"A wiring constructor with no composition-root caller is a subsystem that exists,\n"+
			"tests green, and never runs. Either call it from the daemon's wiring, or record\n"+
			"the decision in wireFunctionsWithoutCaller with the reason it stays dormant.",
			len(orphans), strings.Join(orphans, "\n  "))
	}
}

// exportedWireFuncs returns the exported, receiverless Wire* functions
// declared directly in dir (non-test files only), mapped to their file.
func exportedWireFuncs(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", dir, err)
	}
	for _, pkg := range pkgs {
		for path, file := range pkg.Files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv != nil || fn.Name == nil || !fn.Name.IsExported() {
					continue
				}
				n := fn.Name.Name
				if strings.HasPrefix(n, "Wire") && len(n) > 4 && n[4] >= 'A' && n[4] <= 'Z' {
					out[n] = path
				}
			}
		}
	}
	return out
}

// calledIdentifiersTree returns every function-name identifier appearing in a
// call position in a non-test file anywhere under dir. Test files are excluded
// deliberately: a constructor called only from a test is exactly the dead seam
// this guard exists to find, and counting its own unit test as a caller would
// make the check answer yes to everything.
func calledIdentifiersTree(t *testing.T, dir string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil // a file this scan cannot parse is not evidence of a dead seam
		}
		{
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch fn := call.Fun.(type) {
				case *ast.Ident:
					out[fn.Name] = true
				case *ast.SelectorExpr:
					if fn.Sel != nil {
						out[fn.Sel.Name] = true
					}
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return out
}

func repoRootForWiring(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test file")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
}
