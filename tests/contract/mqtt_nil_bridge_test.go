// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryBridgeReachThroughIsNilGuarded asserts that no production call
// site dereferences the result of a Wiring.Bridge() call without checking
// it for nil first.
//
// A nil bridge is a designed state, not an anomaly: disabling MQTT at
// runtime keeps the Wiring alive and points its bridge nowhere, so an
// in-flight publish becomes a no-op. Every method on Wiring itself
// therefore guards. The adapters that reach *through* Wiring for the
// bridge — to get at the discovery builder or the HubInfo stamp — are the
// ones that forgot, and the failure is asymmetric: the hub-discovery
// re-Start runs under panic isolation and merely logged
// `mqtt.hub_discovery.restart_on_ready.panic` while silently skipping
// every hub payload, but the per-central HubInfo stamp has no recover at
// all and takes the daemon down with it.
//
// The check is syntactic because the defect is syntactic: the call
// returns a pointer that may be nil, and the very next thing the code
// does is select on it.
func TestEveryBridgeReachThroughIsNilGuarded(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	// Files whose Bridge() results are nil-safe by construction: the
	// supervisor reads the bridge back out of a stack it has just built,
	// so there is no window in which it could be nil.
	exempt := map[string]bool{
		filepath.Join("cmd", "openccu-loom", "mqtt_supervisor.go"): true,
	}

	for _, dir := range []string{"internal", "cmd", "pkg"} {
		base := filepath.Join(root, dir)
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
				return err
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			if strings.HasSuffix(rel, "_test.go") || exempt[rel] {
				return nil
			}
			checkBridgeReachThroughs(t, path, rel)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", base, err)
		}
	}
}

// checkBridgeReachThroughs reports every `x.Bridge()` whose value is used
// as a receiver on the spot, which is the unguarded shape.
func checkBridgeReachThroughs(t *testing.T, path, rel string) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		// The defect shape is a selector on a call: `w.Bridge().Foo(...)`.
		// Assigning first (`b := w.Bridge()`) and then dereferencing is
		// equally broken, but only when unguarded — and the guard is what
		// the compiler cannot see, so those are covered by the runtime
		// tests next to each adapter rather than here.
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		inner, ok := sel.X.(*ast.CallExpr)
		if !ok {
			return true
		}
		fn, ok := inner.Fun.(*ast.SelectorExpr)
		if !ok || fn.Sel.Name != "Bridge" || len(inner.Args) != 0 {
			return true
		}
		t.Errorf(
			"%s:%d: %s() result is dereferenced without a nil check — "+
				"a nil bridge is the runtime-disabled-MQTT state, not an anomaly",
			rel, fset.Position(sel.Pos()).Line, fn.Sel.Name,
		)
		return true
	})
}
