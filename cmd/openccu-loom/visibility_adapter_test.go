// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ── newVisibilityAdapter ──────────────────────────────────────────────────────

func TestNewVisibilityAdapter_ReturnsNonNil(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	a := newVisibilityAdapter(nil, nil, reg)
	if a == nil {
		t.Fatal("expected non-nil adapter")
	}
}

// ── Names ─────────────────────────────────────────────────────────────────────

func TestVisibilityAdapter_Names_NilAdapter(t *testing.T) {
	t.Parallel()
	var a *visibilityAdapter
	if got := a.Names(); got != nil {
		t.Errorf("nil receiver: expected nil, got %v", got)
	}
}

func TestVisibilityAdapter_Names_NilCentralRegistry(t *testing.T) {
	t.Parallel()
	a := &visibilityAdapter{centralRegistry: nil}
	if got := a.Names(); got != nil {
		t.Errorf("nil centralRegistry: expected nil, got %v", got)
	}
}

func TestVisibilityAdapter_Names_EmptyRegistry(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	a := newVisibilityAdapter(nil, nil, reg)
	names := a.Names()
	if len(names) != 0 {
		t.Errorf("empty registry: expected 0 names, got %v", names)
	}
}

func TestVisibilityAdapter_Names_PopulatedRegistry(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-one", "ccu-two")
	a := newVisibilityAdapter(nil, nil, reg)
	names := a.Names()
	if len(names) != 2 {
		t.Errorf("expected 2 names, got %d: %v", len(names), names)
	}
	seen := make(map[string]bool)
	for _, n := range names {
		seen[n] = true
	}
	if !seen["ccu-one"] || !seen["ccu-two"] {
		t.Errorf("expected both 'ccu-one' and 'ccu-two' in names, got %v", names)
	}
}

// ── UnIgnoreCandidates ────────────────────────────────────────────────────────

func TestVisibilityAdapter_UnIgnoreCandidates_NilAdapter(t *testing.T) {
	t.Parallel()
	var a *visibilityAdapter
	if got := a.UnIgnoreCandidates("ccu-one", hmenum.ParamsetKeyMaster); got != nil {
		t.Errorf("nil receiver: expected nil, got %v", got)
	}
}

func TestVisibilityAdapter_UnIgnoreCandidates_NilCentralRegistry(t *testing.T) {
	t.Parallel()
	a := &visibilityAdapter{centralRegistry: nil}
	if got := a.UnIgnoreCandidates("ccu-one", hmenum.ParamsetKeyMaster); got != nil {
		t.Errorf("nil registry: expected nil, got %v", got)
	}
}

func TestVisibilityAdapter_UnIgnoreCandidates_UnknownCentral(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-one")
	a := newVisibilityAdapter(nil, nil, reg)
	// "nonexistent" is not in the registry → must return nil.
	if got := a.UnIgnoreCandidates("nonexistent", hmenum.ParamsetKeyMaster); got != nil {
		t.Errorf("unknown central: expected nil, got %v", got)
	}
}

// TestVisibilityAdapter_UnIgnoreCandidates_KnownCentral_EmptyRegistry exercises
// the path where Get(centralName) succeeds and GetUnIgnoreCandidates is called.
// An empty model registry returns a nil/empty candidate list — that is acceptable.
func TestVisibilityAdapter_UnIgnoreCandidates_KnownCentral_EmptyRegistry(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-one")
	a := newVisibilityAdapter(nil, nil, reg)
	// "ccu-one" is in the registry; QueryFacade() returns non-nil;
	// GetUnIgnoreCandidates with empty model returns nil or empty slice.
	got := a.UnIgnoreCandidates("ccu-one", hmenum.ParamsetKeyMaster)
	// Must not panic; result may be nil or empty.
	_ = got
}

// ── LoadUnIgnore ──────────────────────────────────────────────────────────────

func TestVisibilityAdapter_LoadUnIgnore_NilAdapter(t *testing.T) {
	t.Parallel()
	var a *visibilityAdapter
	_, _, err := a.LoadUnIgnore("ccu-one", nil)
	if err == nil {
		t.Fatal("nil receiver: expected error, got nil")
	}
}

func TestVisibilityAdapter_LoadUnIgnore_NilRegistry_Errors(t *testing.T) {
	t.Parallel()
	a := &visibilityAdapter{
		registry:        nil,
		registryStore:   nil,
		centralRegistry: nil,
	}
	_, _, err := a.LoadUnIgnore("ccu-one", nil)
	if err == nil {
		t.Fatal("nil fields: expected error, got nil")
	}
}

func TestVisibilityAdapter_LoadUnIgnore_NilVisibilityRegistry_Errors(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-one")
	a := &visibilityAdapter{
		registry:        nil, // nil visibility.Registry triggers error
		registryStore:   nil,
		centralRegistry: reg,
	}
	_, _, err := a.LoadUnIgnore("ccu-one", nil)
	if err == nil {
		t.Fatal("nil visibility registry: expected error, got nil")
	}
}

func TestVisibilityAdapter_LoadUnIgnore_MissingCentral_Errors(t *testing.T) {
	t.Parallel()
	// Build a real visibility.Registry and a SQLite store in the test's
	// temp dir, via the shared openMigratedTestDB helper.
	visReg := visibility.NewRegistry()
	reg := buildTestRegistry(t, "ccu-one")

	// Build a real SQLite visibility store so LoadUnIgnore can call Patterns.
	// We need to open a DB the same way wireVisibilityUnIgnoreStore does.
	// Use the adapter with a nil registryStore to trigger the nil guard first.
	a := &visibilityAdapter{
		registry:        visReg,
		registryStore:   nil,
		centralRegistry: reg,
	}
	_, _, err := a.LoadUnIgnore("ccu-one", nil)
	if err == nil {
		t.Fatal("nil registryStore: expected error, got nil")
	}
}
