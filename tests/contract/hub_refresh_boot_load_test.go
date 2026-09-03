// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

// hub_refresh_boot_load_test.go — every hub refresh category is accounted for
// at boot.
//
// HubCoordinator.InitHub is the hub's boot-load sequence. A category declared
// in hubRefreshSet but never run there is not obviously broken: it simply
// stays empty until the first scheduler tick, which on a 60s interval is a
// silently stale surface rather than a failure anyone sees. Two categories are
// deliberately loaded elsewhere; this guard forces a third one to declare
// where its boot load lives instead of inheriting the silence.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"testing"
)

// hubRefreshSlotsLoadedElsewhere maps a hubRefreshSet field that InitHub does
// NOT run to where its boot behaviour was measured. Adding a row is a claim
// about production code — verify it before you write it.
var hubRefreshSlotsLoadedElsewhere = map[string]string{
	// runInitialSystemUpdateLoad in internal/central/adapter/hub_wiring.go
	// performs the one-shot boot fetch on a goroutine detached from WireHub.
	"systemUpdate": "boot-loaded by runInitialSystemUpdateLoad (internal/central/adapter/hub_wiring.go)",
	// No boot load exists. The only production driver is the periodic
	// "hub.bidcos_interfaces_refresh" job registered in internal/central/jobs.go,
	// which carries no RunOnStart, so the category stays empty until the
	// first tick. Recorded as measured, not as intended.
	"bidcosInterfaces": "no boot load; first populated by the hub.bidcos_interfaces_refresh scheduler job (internal/central/jobs.go)",
}

// TestEveryHubRefreshCategoryHasABootLoad pins InitHub's slot coverage
// against the hubRefreshSet declaration.
func TestEveryHubRefreshCategoryHasABootLoad(t *testing.T) {
	root := filepath.Join("..", "..", "internal", "central", "coordinators")

	declared := hubRefreshSetFields(t, filepath.Join(root, "hub_refresh.go"))
	if len(declared) == 0 {
		t.Fatal("hubRefreshSet declares no fields — the extractor stopped matching the source")
	}
	loaded := initHubRunSlots(t, filepath.Join(root, "hub.go"))
	if len(loaded) == 0 {
		t.Fatal("InitHub runs no refresh slot — the extractor stopped matching the source")
	}

	var unaccounted []string
	for _, field := range declared {
		if loaded[field] {
			continue
		}
		if _, allowed := hubRefreshSlotsLoadedElsewhere[field]; allowed {
			continue
		}
		unaccounted = append(unaccounted, field)
	}
	sort.Strings(unaccounted)
	if len(unaccounted) > 0 {
		t.Fatalf("hubRefreshSet fields with no boot load and no recorded reason: %v\n"+
			"Either run the slot in InitHub or add a row to hubRefreshSlotsLoadedElsewhere naming where its boot load lives.",
			unaccounted)
	}

	// The allowlist is not self-cleaning: a row for a slot InitHub now runs
	// would keep asserting a boot load somewhere else that is no longer the
	// only one.
	for field := range hubRefreshSlotsLoadedElsewhere {
		if loaded[field] {
			t.Errorf("hubRefreshSlotsLoadedElsewhere[%q] is stale: InitHub runs this slot itself now", field)
		}
	}
}

// hubRefreshSetFields returns the field names of the hubRefreshSet struct.
func hubRefreshSetFields(t *testing.T, path string) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var fields []string
	ast.Inspect(file, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name == nil || ts.Name.Name != "hubRefreshSet" {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok || st.Fields == nil {
			return false
		}
		for _, f := range st.Fields.List {
			for _, name := range f.Names {
				fields = append(fields, name.Name)
			}
		}
		return false
	})
	sort.Strings(fields)
	return fields
}

// initHubRunSlots returns the set of `h.refresh.<field>.run(` slots invoked
// inside the InitHub method body.
func initHubRunSlots(t *testing.T, path string) map[string]bool {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	slots := map[string]bool{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name == nil || fn.Name.Name != "InitHub" || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			// Shape: <x>.refresh.<field>.run(...)
			runSel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || runSel.Sel == nil || runSel.Sel.Name != "run" {
				return true
			}
			fieldSel, ok := runSel.X.(*ast.SelectorExpr)
			if !ok || fieldSel.Sel == nil {
				return true
			}
			refreshSel, ok := fieldSel.X.(*ast.SelectorExpr)
			if !ok || refreshSel.Sel == nil || refreshSel.Sel.Name != "refresh" {
				return true
			}
			slots[fieldSel.Sel.Name] = true
			return true
		})
	}
	return slots
}
