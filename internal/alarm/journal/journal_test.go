// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package journal_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/alarm/journal"
	"github.com/SukramJ/openccu-loom/internal/clock"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// fakeStore is a minimal in-memory journal.Store for facade tests: it
// records every appended row and lets a test force an Append failure.
type fakeStore struct {
	entries     []sqlitestore.AlarmJournalEntry
	appendErr   error
	purgeCalled bool
	purgeCutoff int64
}

func (s *fakeStore) Append(_ context.Context, e sqlitestore.AlarmJournalEntry) (int64, error) {
	if s.appendErr != nil {
		return 0, s.appendErr
	}
	e.ID = int64(len(s.entries) + 1)
	s.entries = append(s.entries, e)
	return e.ID, nil
}

func (s *fakeStore) PurgeBefore(_ context.Context, cutoffMS int64) (int64, error) {
	s.purgeCalled = true
	s.purgeCutoff = cutoffMS
	return 0, nil
}

func TestAppend_StampsPersistsAndPublishes(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC))
	store := &fakeStore{}
	var published []hmevent.Event
	j := journal.New(store, clk, func(e hmevent.Event) { published = append(published, e) }, nil)

	id, err := j.Append(context.Background(), engine.JournalEntry{
		AreaID: "eg", Class: hmenum.AlarmJournalClassArm, Event: "armed", Actor: "tester",
		Details: map[string]any{"mode": "full"},
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if len(store.entries) != 1 {
		t.Fatalf("stored entries = %d, want 1", len(store.entries))
	}
	got := store.entries[0]
	if got.TsMS != clk.Now().UnixMilli() {
		t.Fatalf("TsMS = %d, want %d", got.TsMS, clk.Now().UnixMilli())
	}
	var details map[string]string
	if err := json.Unmarshal([]byte(got.DetailsJSON), &details); err != nil {
		t.Fatalf("details json: %v", err)
	}
	if details["mode"] != "full" {
		t.Fatalf("details = %v, want mode=full", details)
	}

	if len(published) != 1 {
		t.Fatalf("published = %d events, want 1", len(published))
	}
	ev, ok := published[0].(hmevent.AlarmJournalAppendedEvent)
	if !ok {
		t.Fatalf("published event type = %T, want AlarmJournalAppendedEvent", published[0])
	}
	if ev.EntryID != id || ev.AreaID != "eg" || ev.Event != "armed" {
		t.Fatalf("published event = %+v, want EntryID=%d AreaID=eg Event=armed", ev, id)
	}
}

func TestAppend_HiddenEntryPersistsButIsNotPublished(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC))
	store := &fakeStore{}
	var published []hmevent.Event
	j := journal.New(store, clk, func(e hmevent.Event) { published = append(published, e) }, nil)

	if _, err := j.Append(context.Background(), engine.JournalEntry{
		AreaID: "eg", Class: hmenum.AlarmJournalClassTrigger, Event: "duress", Hidden: true,
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if len(store.entries) != 1 {
		t.Fatalf("stored entries = %d, want 1", len(store.entries))
	}
	if !store.entries[0].Hidden {
		t.Fatal("stored entry not marked hidden")
	}
	if len(published) != 0 {
		t.Fatalf("published = %d events, want 0 for a hidden entry", len(published))
	}
}

func TestAppend_StoreErrorPropagatesWithoutPublishing(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC))
	store := &fakeStore{appendErr: errors.New("disk full")}
	var published []hmevent.Event
	j := journal.New(store, clk, func(e hmevent.Event) { published = append(published, e) }, nil)

	if _, err := j.Append(context.Background(), engine.JournalEntry{AreaID: "eg", Event: "armed"}); err == nil {
		t.Fatal("expected an error from a failing store")
	}
	if len(published) != 0 {
		t.Fatalf("published = %d events, want 0 on a store error", len(published))
	}
}

func TestPurge_ComputesCutoffFromClockAndMaxAge(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC))
	store := &fakeStore{}
	j := journal.New(store, clk, nil, nil)

	if _, err := j.Purge(context.Background(), 24*time.Hour); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if !store.purgeCalled {
		t.Fatal("expected PurgeBefore to be called")
	}
	if want := clk.Now().Add(-24 * time.Hour).UnixMilli(); store.purgeCutoff != want {
		t.Fatalf("cutoff = %d, want %d", store.purgeCutoff, want)
	}
}
