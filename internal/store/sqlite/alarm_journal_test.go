// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func freshAlarmJournalStore(t *testing.T) *AlarmJournalStore {
	t.Helper()
	return NewAlarmJournalStore(openTestDB(t, "alarm_journal.db"))
}

func baseAlarmJournalEntry(zoneID string, tsMS int64, class hmenum.AlarmJournalClass) AlarmJournalEntry {
	return AlarmJournalEntry{
		TsMS:        tsMS,
		ZoneID:      zoneID,
		Class:       class,
		Event:       "test-event",
		Actor:       "operator",
		Source:      "rest",
		IncidentID:  0,
		Hidden:      false,
		DetailsJSON: "{}",
	}
}

// TestAlarmJournalStoreAppendReturnsIncreasingIDs verifies that Append
// returns strictly increasing row ids and that e.ID on input is ignored.
func TestAlarmJournalStoreAppendReturnsIncreasingIDs(t *testing.T) {
	s := freshAlarmJournalStore(t)
	ctx := context.Background()

	e1 := baseAlarmJournalEntry("zone-1", 1000, hmenum.AlarmJournalClassArm)
	e1.ID = 999 // must be ignored
	id1, err := s.Append(ctx, e1)
	if err != nil {
		t.Fatalf("Append 1: %v", err)
	}
	id2, err := s.Append(ctx, baseAlarmJournalEntry("zone-1", 1001, hmenum.AlarmJournalClassArm))
	if err != nil {
		t.Fatalf("Append 2: %v", err)
	}
	if id1 <= 0 {
		t.Errorf("id1=%d want >0", id1)
	}
	if id2 <= id1 {
		t.Errorf("id2=%d must be > id1=%d", id2, id1)
	}
}

// TestAlarmJournalStoreQueryNewestFirst verifies that Query with no filter
// returns every row ordered newest (highest id) first.
func TestAlarmJournalStoreQueryNewestFirst(t *testing.T) {
	s := freshAlarmJournalStore(t)
	ctx := context.Background()

	ids := make([]int64, 0, 5)
	for i := range 5 {
		id, err := s.Append(ctx, baseAlarmJournalEntry("zone-1", int64(1000+i), hmenum.AlarmJournalClassArm))
		if err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
		ids = append(ids, id)
	}

	got, err := s.Query(ctx, AlarmJournalFilter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("len=%d want 5", len(got))
	}
	for i, e := range got {
		want := ids[len(ids)-1-i]
		if e.ID != want {
			t.Errorf("got[%d].ID=%d want %d (newest-first order)", i, e.ID, want)
		}
	}
}

// TestAlarmJournalStoreQueryFilterByZone verifies ZoneID filters to that
// zone's entries only, and "" (zero value) matches every zone.
func TestAlarmJournalStoreQueryFilterByZone(t *testing.T) {
	s := freshAlarmJournalStore(t)
	ctx := context.Background()

	if _, err := s.Append(ctx, baseAlarmJournalEntry("zone-1", 1000, hmenum.AlarmJournalClassArm)); err != nil {
		t.Fatalf("Append zone-1: %v", err)
	}
	if _, err := s.Append(ctx, baseAlarmJournalEntry("zone-2", 1001, hmenum.AlarmJournalClassArm)); err != nil {
		t.Fatalf("Append zone-2: %v", err)
	}

	got, err := s.Query(ctx, AlarmJournalFilter{ZoneID: "zone-1"})
	if err != nil {
		t.Fatalf("Query zone-1: %v", err)
	}
	if len(got) != 1 || got[0].ZoneID != "zone-1" {
		t.Fatalf("got=%+v want single zone-1 entry", got)
	}

	all, err := s.Query(ctx, AlarmJournalFilter{})
	if err != nil {
		t.Fatalf("Query all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("len=%d want 2 (ZoneID zero value matches every zone)", len(all))
	}
}

// TestAlarmJournalStoreQueryFilterByClass verifies Class filters to that
// class's entries only, and "" (zero value) matches every class.
func TestAlarmJournalStoreQueryFilterByClass(t *testing.T) {
	s := freshAlarmJournalStore(t)
	ctx := context.Background()

	if _, err := s.Append(ctx, baseAlarmJournalEntry("zone-1", 1000, hmenum.AlarmJournalClassArm)); err != nil {
		t.Fatalf("Append arm: %v", err)
	}
	if _, err := s.Append(ctx, baseAlarmJournalEntry("zone-1", 1001, hmenum.AlarmJournalClassTrigger)); err != nil {
		t.Fatalf("Append trigger: %v", err)
	}

	got, err := s.Query(ctx, AlarmJournalFilter{Class: hmenum.AlarmJournalClassTrigger})
	if err != nil {
		t.Fatalf("Query class: %v", err)
	}
	if len(got) != 1 || got[0].Class != hmenum.AlarmJournalClassTrigger {
		t.Fatalf("got=%+v want single trigger entry", got)
	}

	all, err := s.Query(ctx, AlarmJournalFilter{})
	if err != nil {
		t.Fatalf("Query all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("len=%d want 2 (Class zero value matches every class)", len(all))
	}
}

// TestAlarmJournalStoreQueryFilterByIncidentID verifies IncidentID filters
// to entries tied to that incident, and 0 (zero value) matches every entry.
func TestAlarmJournalStoreQueryFilterByIncidentID(t *testing.T) {
	s := freshAlarmJournalStore(t)
	ctx := context.Background()

	withIncident := baseAlarmJournalEntry("zone-1", 1000, hmenum.AlarmJournalClassTrigger)
	withIncident.IncidentID = 42
	if _, err := s.Append(ctx, withIncident); err != nil {
		t.Fatalf("Append with incident: %v", err)
	}
	if _, err := s.Append(ctx, baseAlarmJournalEntry("zone-1", 1001, hmenum.AlarmJournalClassArm)); err != nil {
		t.Fatalf("Append without incident: %v", err)
	}

	got, err := s.Query(ctx, AlarmJournalFilter{IncidentID: 42})
	if err != nil {
		t.Fatalf("Query incident: %v", err)
	}
	if len(got) != 1 || got[0].IncidentID != 42 {
		t.Fatalf("got=%+v want single entry with IncidentID=42", got)
	}

	all, err := s.Query(ctx, AlarmJournalFilter{})
	if err != nil {
		t.Fatalf("Query all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("len=%d want 2 (IncidentID zero value matches every entry)", len(all))
	}
}

// TestAlarmJournalStoreQueryFromToRangeInclusive verifies FromMS/ToMS bound
// ts_ms inclusively on both ends.
func TestAlarmJournalStoreQueryFromToRangeInclusive(t *testing.T) {
	s := freshAlarmJournalStore(t)
	ctx := context.Background()

	for _, ts := range []int64{1000, 2000, 3000, 4000, 5000} {
		if _, err := s.Append(ctx, baseAlarmJournalEntry("zone-1", ts, hmenum.AlarmJournalClassArm)); err != nil {
			t.Fatalf("Append ts=%d: %v", ts, err)
		}
	}

	got, err := s.Query(ctx, AlarmJournalFilter{FromMS: 2000, ToMS: 4000})
	if err != nil {
		t.Fatalf("Query range: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d want 3 (2000,3000,4000 inclusive)", len(got))
	}
	for _, e := range got {
		if e.TsMS < 2000 || e.TsMS > 4000 {
			t.Errorf("entry ts_ms=%d outside [2000,4000]", e.TsMS)
		}
	}

	// Boundary-exact single-point range must include exactly that entry.
	exact, err := s.Query(ctx, AlarmJournalFilter{FromMS: 3000, ToMS: 3000})
	if err != nil {
		t.Fatalf("Query exact: %v", err)
	}
	if len(exact) != 1 || exact[0].TsMS != 3000 {
		t.Fatalf("got=%+v want single entry at ts_ms=3000", exact)
	}
}

// TestAlarmJournalStoreQueryLimitHonored verifies Limit caps the result set
// while keeping newest-first order; Limit<=0 returns every matching row.
func TestAlarmJournalStoreQueryLimitHonored(t *testing.T) {
	s := freshAlarmJournalStore(t)
	ctx := context.Background()

	for i := range 5 {
		if _, err := s.Append(ctx, baseAlarmJournalEntry("zone-1", int64(1000+i), hmenum.AlarmJournalClassArm)); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}

	limited, err := s.Query(ctx, AlarmJournalFilter{Limit: 2})
	if err != nil {
		t.Fatalf("Query limited: %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("len=%d want 2", len(limited))
	}
	if limited[0].ID <= limited[1].ID {
		t.Errorf("limited results not newest-first: %+v", limited)
	}

	unlimited, err := s.Query(ctx, AlarmJournalFilter{Limit: 0})
	if err != nil {
		t.Fatalf("Query unlimited: %v", err)
	}
	if len(unlimited) != 5 {
		t.Fatalf("len=%d want 5 (Limit<=0 returns every row)", len(unlimited))
	}
}

// TestAlarmJournalStoreQueryHiddenExcludedByDefault verifies that Query
// excludes hidden (duress) entries unless IncludeHidden is set — the
// duress-privacy filter.
func TestAlarmJournalStoreQueryHiddenExcludedByDefault(t *testing.T) {
	s := freshAlarmJournalStore(t)
	ctx := context.Background()

	visible := baseAlarmJournalEntry("zone-1", 1000, hmenum.AlarmJournalClassArm)
	hidden := baseAlarmJournalEntry("zone-1", 1001, hmenum.AlarmJournalClassSilence)
	hidden.Hidden = true
	hidden.Event = "duress"

	if _, err := s.Append(ctx, visible); err != nil {
		t.Fatalf("Append visible: %v", err)
	}
	if _, err := s.Append(ctx, hidden); err != nil {
		t.Fatalf("Append hidden: %v", err)
	}

	defaultResult, err := s.Query(ctx, AlarmJournalFilter{})
	if err != nil {
		t.Fatalf("Query default: %v", err)
	}
	if len(defaultResult) != 1 {
		t.Fatalf("len=%d want 1 (hidden entry excluded by default)", len(defaultResult))
	}
	if defaultResult[0].Hidden {
		t.Error("default query returned a hidden entry")
	}

	withHidden, err := s.Query(ctx, AlarmJournalFilter{IncludeHidden: true})
	if err != nil {
		t.Fatalf("Query IncludeHidden: %v", err)
	}
	if len(withHidden) != 2 {
		t.Fatalf("len=%d want 2 (IncludeHidden must return both)", len(withHidden))
	}
	var sawHidden bool
	for _, e := range withHidden {
		if e.Hidden {
			sawHidden = true
			if e.Event != "duress" {
				t.Errorf("hidden entry Event=%q want duress", e.Event)
			}
		}
	}
	if !sawHidden {
		t.Error("IncludeHidden query did not return the hidden entry")
	}
}

// TestAlarmJournalStorePurgeBefore verifies PurgeBefore deletes only
// entries strictly before cutoffMS, including hidden ones, and returns the
// deleted count.
func TestAlarmJournalStorePurgeBefore(t *testing.T) {
	s := freshAlarmJournalStore(t)
	ctx := context.Background()

	hiddenOld := baseAlarmJournalEntry("zone-1", 1000, hmenum.AlarmJournalClassSilence)
	hiddenOld.Hidden = true
	if _, err := s.Append(ctx, hiddenOld); err != nil {
		t.Fatalf("Append hiddenOld: %v", err)
	}
	if _, err := s.Append(ctx, baseAlarmJournalEntry("zone-1", 2000, hmenum.AlarmJournalClassArm)); err != nil {
		t.Fatalf("Append at 2000: %v", err)
	}
	if _, err := s.Append(ctx, baseAlarmJournalEntry("zone-1", 3000, hmenum.AlarmJournalClassArm)); err != nil {
		t.Fatalf("Append at 3000: %v", err)
	}

	n, err := s.PurgeBefore(ctx, 3000)
	if err != nil {
		t.Fatalf("PurgeBefore: %v", err)
	}
	if n != 2 {
		t.Errorf("PurgeBefore returned %d want 2 (1000 and 2000, including hidden)", n)
	}

	remaining, err := s.Query(ctx, AlarmJournalFilter{IncludeHidden: true})
	if err != nil {
		t.Fatalf("Query remaining: %v", err)
	}
	if len(remaining) != 1 || remaining[0].TsMS != 3000 {
		t.Fatalf("remaining=%+v want single entry at ts_ms=3000", remaining)
	}
}
