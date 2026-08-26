// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"fmt"
	"go/ast"
	"go/types"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// reloadDepsOwnerFile is the one file allowed to touch the bag's fields
// directly: the file that defines the type and every nil-guarded
// accessor on it.
const reloadDepsOwnerFile = "reload_deps.go"

// TestReloadDepsFieldsAreOnlyTouchedByItsAccessors asserts that no code
// outside reload_deps.go reads or writes a field of the daemon's
// late-bound dependency bag directly.
//
// The bag is legitimately nil at runtime: daemonServe passes nil when
// the daemon runs without a config file (no --config flag and no file at
// any discovered location — the shape every installation that keeps its
// configuration in the database has). Every accessor on the type opens
// with `if d == nil`, which makes the nil case total — but only for code
// that goes through an accessor.
//
// A direct field access has no such guard, and the failure is silent in
// exactly the setups that hit it: the composition root's post-hub-ready
// hook dereferenced the bag to reach an atomic slot, so on every
// config-file-less boot the mDNS TXT re-announce (ADR 0058) panicked the
// moment a central reported southbound-ready. A recover consumed it, the
// daemon carried on, and the serial list the discovery contract promises
// was never published. Eight releases shipped that way.
//
// The guard resolves fields through the type checker rather than by
// name, so a field named like one on another struct cannot mask a real
// access or invent a false one.
func TestReloadDepsFieldsAreOnlyTouchedByItsAccessors(t *testing.T) {
	t.Parallel()
	pkgs := loadProductionPackages(t)

	var offences []string
	var seen int
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		if p.TypesInfo == nil {
			return
		}
		for _, file := range p.Syntax {
			owned := filepath.Base(p.Fset.Position(file.Pos()).Filename) == reloadDepsOwnerFile
			ast.Inspect(file, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				v, ok := p.TypesInfo.Selections[sel]
				if !ok || v.Kind() != types.FieldVal || !isReloadDepsField(v) {
					return true
				}
				seen++
				if owned {
					return true
				}
				pos := p.Fset.Position(sel.Pos())
				offences = append(offences, fmt.Sprintf("%s:%d reaches reloadDeps.%s directly",
					filepath.Base(pos.Filename), pos.Line, sel.Sel.Name))
				return true
			})
		}
	})
	if seen == 0 {
		t.Fatal("no reloadDeps field access resolved anywhere, not even in its own file; " +
			"the walk is broken and this test would pass vacuously")
	}

	sort.Strings(offences)
	if len(offences) > 0 {
		t.Errorf("reloadDeps fields are reachable only through its nil-guarded accessors, because the "+
			"bag is nil on every config-file-less boot. Add an accessor on the type (with the "+
			"`if d == nil` opener the others carry) and call that instead:\n  %s",
			strings.Join(offences, "\n  "))
	}
}

// isReloadDepsField reports whether sel selects a field whose receiver
// is the daemon's reloadDeps struct.
//
// The receiver decides, not the field name: several structs in the
// composition root carry a field called mqttSup, and matching on the
// name alone reported every one of them.
func isReloadDepsField(sel *types.Selection) bool {
	recv := sel.Recv()
	for {
		ptr, ok := recv.(*types.Pointer)
		if !ok {
			break
		}
		recv = ptr.Elem()
	}
	named, ok := recv.(*types.Named)
	return ok && named.Obj() != nil && named.Obj().Name() == "reloadDeps"
}
