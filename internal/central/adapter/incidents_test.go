// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// freshIncidentStoreForAdapter opens an in-memory SQLite database with all
// migrations applied and returns an IncidentStore backed by it.
func freshIncidentStoreForAdapter(t *testing.T) *sqlite.IncidentStore {
	t.Helper()
	db, err := sqlite.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return sqlite.NewIncidentStore(db)
}

// registryWithUnit returns a Registry containing a single Unit named by name.
func registryWithUnit(t *testing.T, name string) (*central.Registry, *central.Unit) {
	t.Helper()
	unit, err := central.New(central.Config{Name: name})
	if err != nil {
		t.Fatalf("central.New(%q): %v", name, err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(unit); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	return reg, unit
}

// TestIncidentsStoreReaderNilStoreReturnsNil verifies that a nil store
// does not panic and returns nil.
func TestIncidentsStoreReaderNilStoreReturnsNil(t *testing.T) {
	t.Parallel()
	reg, _ := registryWithUnit(t, "ccu-a")
	r := NewIncidentsStoreReader(nil, reg, nil)
	got := r.Incidents()
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

// TestIncidentsStoreReaderHappyPath verifies the mapping for two incidents
// recorded for a single central: one with InterfaceID + JournalExcerpt, one
// without either (triggering the Summary fallback to Type string).
func TestIncidentsStoreReaderHappyPath(t *testing.T) {
	t.Parallel()
	store := freshIncidentStoreForAdapter(t)
	reg, _ := registryWithUnit(t, "ccu-a")
	ctx := context.Background()

	// Incident 1: interface-scoped, has message and journal excerpt.
	id1, err := store.Record(ctx, sqlite.Incident{
		CentralName:    "ccu-a",
		InterfaceID:    "HmIP-RF",
		Type:           hmenum.IncidentTypeConnectionLost,
		Severity:       hmenum.IncidentSeverityError,
		Message:        "lost connection",
		Details:        "dial tcp: refused",
		JournalExcerpt: "PING sent; PONG not received",
	})
	if err != nil {
		t.Fatalf("Record incident 1: %v", err)
	}

	// Incident 2: central-level (no interface), no message → Summary falls back to Type.
	id2, err := store.Record(ctx, sqlite.Incident{
		CentralName: "ccu-a",
		InterfaceID: "",
		Type:        hmenum.IncidentTypeAuthFailure,
		Severity:    hmenum.IncidentSeverityWarning,
		Message:     "",
		Details:     "",
	})
	if err != nil {
		t.Fatalf("Record incident 2: %v", err)
	}

	r := NewIncidentsStoreReader(store, reg, nil)
	got := r.Incidents()
	if len(got) != 2 {
		t.Fatalf("len=%d want 2", len(got))
	}

	// Build a lookup by ID for order-independent assertions.
	byID := make(map[string]int, len(got))
	for i, inc := range got {
		byID[inc.ID] = i
	}

	// --- incident 1 ---
	idx1, ok := byID[strconv.FormatInt(id1, 10)]
	if !ok {
		t.Fatalf("incident with id=%d not found in result", id1)
	}
	inc1 := got[idx1]
	if inc1.Component != "ccu-a/HmIP-RF" {
		t.Errorf("Component=%q want ccu-a/HmIP-RF", inc1.Component)
	}
	if inc1.Severity != string(hmenum.IncidentSeverityError) {
		t.Errorf("Severity=%q want %q", inc1.Severity, hmenum.IncidentSeverityError)
	}
	if inc1.Summary != "lost connection" {
		t.Errorf("Summary=%q want lost connection", inc1.Summary)
	}
	wantDetail1 := "dial tcp: refused\nPING sent; PONG not received"
	if inc1.Detail != wantDetail1 {
		t.Errorf("Detail=%q want %q", inc1.Detail, wantDetail1)
	}
	if inc1.When.IsZero() {
		t.Error("When is zero")
	}

	// --- incident 2 ---
	idx2, ok := byID[strconv.FormatInt(id2, 10)]
	if !ok {
		t.Fatalf("incident with id=%d not found in result", id2)
	}
	inc2 := got[idx2]
	if inc2.Component != "ccu-a" {
		t.Errorf("Component=%q want ccu-a", inc2.Component)
	}
	if inc2.Severity != string(hmenum.IncidentSeverityWarning) {
		t.Errorf("Severity=%q want %q", inc2.Severity, hmenum.IncidentSeverityWarning)
	}
	// Empty message → Summary falls back to string(Type).
	if inc2.Summary != string(hmenum.IncidentTypeAuthFailure) {
		t.Errorf("Summary=%q want %q (type fallback)", inc2.Summary, hmenum.IncidentTypeAuthFailure)
	}
	if inc2.Detail != "" {
		t.Errorf("Detail=%q want empty", inc2.Detail)
	}
}

// TestIncidentsStoreReaderMultiCentral verifies that incidents from two
// registered centrals are all returned.
func TestIncidentsStoreReaderMultiCentral(t *testing.T) {
	t.Parallel()
	store := freshIncidentStoreForAdapter(t)
	ctx := context.Background()

	reg := central.NewRegistry()
	for _, name := range []string{"ccu-a", "ccu-b"} {
		unit, err := central.New(central.Config{Name: name})
		if err != nil {
			t.Fatalf("central.New(%q): %v", name, err)
		}
		if err := reg.Register(unit); err != nil {
			t.Fatalf("reg.Register(%q): %v", name, err)
		}
		if _, err := store.Record(ctx, sqlite.Incident{
			CentralName: name,
			Type:        hmenum.IncidentTypeRPCFault,
			Severity:    hmenum.IncidentSeverityWarning,
			Message:     name + " fault",
		}); err != nil {
			t.Fatalf("Record for %q: %v", name, err)
		}
	}

	r := NewIncidentsStoreReader(store, reg, nil)
	got := r.Incidents()
	if len(got) != 2 {
		t.Fatalf("len=%d want 2", len(got))
	}
	components := make(map[string]bool)
	for _, inc := range got {
		components[inc.Component] = true
	}
	for _, name := range []string{"ccu-a", "ccu-b"} {
		if !components[name] {
			t.Errorf("component %q missing from result", name)
		}
	}
}

// TestIncidentsStoreReaderCentralWithNoIncidents verifies that a registered
// central with no persisted incidents contributes nothing to the output.
func TestIncidentsStoreReaderCentralWithNoIncidents(t *testing.T) {
	t.Parallel()
	store := freshIncidentStoreForAdapter(t)
	ctx := context.Background()

	reg := central.NewRegistry()

	// ccu-with: has one incident.
	unitWith, err := central.New(central.Config{Name: "ccu-with"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(unitWith); err != nil {
		t.Fatalf("reg.Register ccu-with: %v", err)
	}
	if _, err := store.Record(ctx, sqlite.Incident{
		CentralName: "ccu-with",
		Type:        hmenum.IncidentTypeConfigError,
		Severity:    hmenum.IncidentSeverityInfo,
		Message:     "present",
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	// ccu-empty: registered but no incidents recorded.
	unitEmpty, err := central.New(central.Config{Name: "ccu-empty"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(unitEmpty); err != nil {
		t.Fatalf("reg.Register ccu-empty: %v", err)
	}

	r := NewIncidentsStoreReader(store, reg, nil)
	got := r.Incidents()
	if len(got) != 1 {
		t.Fatalf("len=%d want 1", len(got))
	}
	if got[0].Component != "ccu-with" {
		t.Errorf("Component=%q want ccu-with", got[0].Component)
	}
}

// TestIncidentsStoreReaderIncidentsFilteredScopesToOneCentral verifies
// that a non-empty central argument reads only that CCU's rows even when
// other centrals have incidents recorded.
func TestIncidentsStoreReaderIncidentsFilteredScopesToOneCentral(t *testing.T) {
	t.Parallel()
	store := freshIncidentStoreForAdapter(t)
	ctx := context.Background()

	reg := central.NewRegistry()
	for _, name := range []string{"ccu-a", "ccu-b"} {
		unit, err := central.New(central.Config{Name: name})
		if err != nil {
			t.Fatalf("central.New(%q): %v", name, err)
		}
		if err := reg.Register(unit); err != nil {
			t.Fatalf("reg.Register(%q): %v", name, err)
		}
		if _, err := store.Record(ctx, sqlite.Incident{
			CentralName: name,
			Type:        hmenum.IncidentTypeRPCFault,
			Severity:    hmenum.IncidentSeverityWarning,
			Message:     name + " fault",
		}); err != nil {
			t.Fatalf("Record for %q: %v", name, err)
		}
	}

	r := NewIncidentsStoreReader(store, reg, nil)
	got := r.IncidentsFiltered("ccu-a", time.Time{}, time.Time{}, 0)
	if len(got) != 1 {
		t.Fatalf("len=%d want 1 (scoped to ccu-a)", len(got))
	}
	if got[0].Component != "ccu-a" {
		t.Errorf("Component=%q want ccu-a", got[0].Component)
	}
}

// TestIncidentsStoreReaderIncidentsFilteredEmptyCentralMergesAll verifies
// that an empty central argument merges every registered central's rows,
// newest-first.
func TestIncidentsStoreReaderIncidentsFilteredEmptyCentralMergesAll(t *testing.T) {
	t.Parallel()
	store := freshIncidentStoreForAdapter(t)
	ctx := context.Background()

	reg := central.NewRegistry()
	for _, name := range []string{"ccu-a", "ccu-b"} {
		unit, err := central.New(central.Config{Name: name})
		if err != nil {
			t.Fatalf("central.New(%q): %v", name, err)
		}
		if err := reg.Register(unit); err != nil {
			t.Fatalf("reg.Register(%q): %v", name, err)
		}
		if _, err := store.Record(ctx, sqlite.Incident{
			CentralName: name,
			Type:        hmenum.IncidentTypeRPCFault,
			Severity:    hmenum.IncidentSeverityWarning,
			Message:     name + " fault",
		}); err != nil {
			t.Fatalf("Record for %q: %v", name, err)
		}
	}

	r := NewIncidentsStoreReader(store, reg, nil)
	got := r.IncidentsFiltered("", time.Time{}, time.Time{}, 0)
	if len(got) != 2 {
		t.Fatalf("len=%d want 2 (merged across centrals)", len(got))
	}
}

// TestIncidentsStoreReaderIncidentsFilteredLimitAppliedAfterMerge verifies
// that limit is honored on the merged, re-sorted result — not just
// per-central — when multiple centrals are queried together.
func TestIncidentsStoreReaderIncidentsFilteredLimitAppliedAfterMerge(t *testing.T) {
	t.Parallel()
	store := freshIncidentStoreForAdapter(t)
	ctx := context.Background()

	reg := central.NewRegistry()
	for _, name := range []string{"ccu-a", "ccu-b"} {
		unit, err := central.New(central.Config{Name: name})
		if err != nil {
			t.Fatalf("central.New(%q): %v", name, err)
		}
		if err := reg.Register(unit); err != nil {
			t.Fatalf("reg.Register(%q): %v", name, err)
		}
		for i := range 3 {
			if _, err := store.Record(ctx, sqlite.Incident{
				CentralName: name,
				Type:        hmenum.IncidentTypeRPCFault,
				Severity:    hmenum.IncidentSeverityWarning,
				Message:     name + "-" + strconv.Itoa(i),
			}); err != nil {
				t.Fatalf("Record for %q: %v", name, err)
			}
		}
	}

	r := NewIncidentsStoreReader(store, reg, nil)
	got := r.IncidentsFiltered("", time.Time{}, time.Time{}, 2)
	if len(got) != 2 {
		t.Fatalf("len=%d want 2 (limit applied after merge)", len(got))
	}
}

// TestIncidentsStoreReaderIncidentsFilteredNilReaderReturnsNil mirrors the
// nil-safety contract of Incidents().
func TestIncidentsStoreReaderIncidentsFilteredNilReaderReturnsNil(t *testing.T) {
	t.Parallel()
	reg, _ := registryWithUnit(t, "ccu-a")
	r := NewIncidentsStoreReader(nil, reg, nil)
	got := r.IncidentsFiltered("", time.Time{}, time.Time{}, 0)
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

// TestIncidentsStoreReaderClearIncidentsClearsEveryRegisteredCentral
// verifies that ClearIncidents (backing both DELETE /incidents and the WS
// incidents.clear command) empties every registered central's rows.
func TestIncidentsStoreReaderClearIncidentsClearsEveryRegisteredCentral(t *testing.T) {
	t.Parallel()
	store := freshIncidentStoreForAdapter(t)
	ctx := context.Background()

	reg := central.NewRegistry()
	for _, name := range []string{"ccu-a", "ccu-b"} {
		unit, err := central.New(central.Config{Name: name})
		if err != nil {
			t.Fatalf("central.New(%q): %v", name, err)
		}
		if err := reg.Register(unit); err != nil {
			t.Fatalf("reg.Register(%q): %v", name, err)
		}
		if _, err := store.Record(ctx, sqlite.Incident{
			CentralName: name,
			Type:        hmenum.IncidentTypeRPCFault,
			Severity:    hmenum.IncidentSeverityWarning,
			Message:     name + " fault",
		}); err != nil {
			t.Fatalf("Record for %q: %v", name, err)
		}
	}

	r := NewIncidentsStoreReader(store, reg, nil)
	if err := r.ClearIncidents(ctx); err != nil {
		t.Fatalf("ClearIncidents: %v", err)
	}
	if got := r.Incidents(); len(got) != 0 {
		t.Fatalf("Incidents() after clear = %+v, want empty", got)
	}
}

// TestToAPIIncidentFieldMapping exercises toAPIIncident directly for all
// field-mapping edge cases.
func TestToAPIIncidentFieldMapping(t *testing.T) {
	t.Parallel()

	t.Run("detail with journal excerpt appended", func(t *testing.T) {
		t.Parallel()
		inc := sqlite.Incident{
			ID:             42,
			CentralName:    "ccu-x",
			InterfaceID:    "BidCos-RF",
			Type:           hmenum.IncidentTypeRPCError,
			Severity:       hmenum.IncidentSeverityCritical,
			Message:        "rpc error",
			Details:        "details here",
			JournalExcerpt: "journal line",
		}
		got := toAPIIncident(&inc)
		if got.ID != "42" {
			t.Errorf("ID=%q want 42", got.ID)
		}
		if got.Component != "ccu-x/BidCos-RF" {
			t.Errorf("Component=%q want ccu-x/BidCos-RF", got.Component)
		}
		if got.Summary != "rpc error" {
			t.Errorf("Summary=%q want rpc error", got.Summary)
		}
		if got.Detail != "details here\njournal line" {
			t.Errorf("Detail=%q want details here\\njournal line", got.Detail)
		}
	})

	t.Run("journal excerpt with no details", func(t *testing.T) {
		t.Parallel()
		inc := sqlite.Incident{
			ID:             7,
			CentralName:    "ccu-x",
			JournalExcerpt: "only journal",
		}
		got := toAPIIncident(&inc)
		if got.Detail != "only journal" {
			t.Errorf("Detail=%q want only journal", got.Detail)
		}
	})

	t.Run("no interface keeps central-only component", func(t *testing.T) {
		t.Parallel()
		inc := sqlite.Incident{
			CentralName: "ccu-y",
			InterfaceID: "",
			Type:        hmenum.IncidentTypePingPongMismatch,
			Message:     "",
		}
		got := toAPIIncident(&inc)
		if got.Component != "ccu-y" {
			t.Errorf("Component=%q want ccu-y", got.Component)
		}
		if got.Summary != string(hmenum.IncidentTypePingPongMismatch) {
			t.Errorf("Summary=%q want type fallback", got.Summary)
		}
	})
}
