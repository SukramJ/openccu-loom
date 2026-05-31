// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// recorder_disk_test.go — tests for Recorder.Persist, Recorder.Load,
// and Recorder.SetAutoPersist (, P2).
package session

import (
	"context"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// fakeStore is a test double that satisfies PersistStore in-process.
// ---------------------------------------------------------------------------

type fakeStore struct {
	mu   sync.Mutex
	rows map[string][]LoadRow // keyed by "central/slug"
}

func newFakeStore() *fakeStore {
	return &fakeStore{rows: make(map[string][]LoadRow)}
}

func (f *fakeStore) PersistAll(_ context.Context, centralName, slug string, rows []PersistRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := centralName + "/" + slug
	out := make([]LoadRow, 0, len(rows))
	for i := range rows {
		out = append(out, LoadRow{
			RPCType:      rows[i].RPCType,
			Method:       rows[i].Method,
			FrozenParams: rows[i].FrozenParams,
			ResponseJSON: rows[i].ResponseJSON,
			RecordedAt:   rows[i].RecordedAt,
			TTLSeconds:   rows[i].TTLSeconds,
		})
	}
	f.rows[k] = out
	return nil
}

func (f *fakeStore) Load(_ context.Context, centralName, slug string, maxEntries int) ([]LoadRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := centralName + "/" + slug
	rows := f.rows[k]
	if maxEntries > 0 && len(rows) > maxEntries {
		rows = rows[:maxEntries]
	}
	out := make([]LoadRow, len(rows))
	copy(out, rows)
	return out, nil
}

// ---------------------------------------------------------------------------
// Persist round-trip
// ---------------------------------------------------------------------------

func TestRecorderPersistRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newFakeStore()

	r := New(Config{Active: true})
	r.RecordResponse(RPCTypeXML, "getDeviceDescription", []any{"VCU1234567"}, map[string]any{"TYPE": "HM-CC-RT-DN"})
	r.RecordResponse(RPCTypeJSON, "listDevices", nil, []any{"dev1", "dev2"})

	if err := r.Persist(ctx, store, "main", "diag"); err != nil {
		t.Fatalf("Persist: %v", err)
	}

	// New recorder, load from store — must reproduce the same entries.
	r2 := New(Config{Active: true})
	if err := r2.Load(ctx, store, "main", "diag", 0); err != nil {
		t.Fatalf("Load: %v", err)
	}

	resp, ok := r2.Get(RPCTypeXML, "getDeviceDescription", []any{"VCU1234567"})
	if !ok {
		t.Fatal("after Load, Get must find persisted entry")
	}
	m, ok := resp.(map[string]any)
	if !ok {
		t.Fatalf("response type=%T want map[string]any", resp)
	}
	if m["TYPE"] != "HM-CC-RT-DN" {
		t.Errorf("TYPE=%v want HM-CC-RT-DN", m["TYPE"])
	}

	_, ok2 := r2.Get(RPCTypeJSON, "listDevices", nil)
	if !ok2 {
		t.Error("after Load, JSON listDevices must be present")
	}
}

// ---------------------------------------------------------------------------
// Load: live data wins over persisted data
// ---------------------------------------------------------------------------

func TestRecorderLoadLiveDataWins(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newFakeStore()

	// Seed the store with an old response.
	seed := New(Config{Active: true})
	seed.RecordResponse(RPCTypeXML, "ping", nil, "old-pong")
	if err := seed.Persist(ctx, store, "main", "live"); err != nil {
		t.Fatalf("seed Persist: %v", err)
	}

	// New recorder has a fresh (live) response for the same key.
	r := New(Config{Active: true})
	r.RecordResponse(RPCTypeXML, "ping", nil, "live-pong")

	if err := r.Load(ctx, store, "main", "live", 0); err != nil {
		t.Fatalf("Load: %v", err)
	}

	resp, ok := r.Get(RPCTypeXML, "ping", nil)
	if !ok {
		t.Fatal("Get must find the ping entry")
	}
	if resp != "live-pong" {
		t.Errorf("resp=%v want live-pong (live data must win over persisted)", resp)
	}
}

// ---------------------------------------------------------------------------
// MaxLoadEntries cap
// ---------------------------------------------------------------------------

func TestRecorderLoadMaxLoadEntries(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newFakeStore()

	// Persist 5 distinct entries.
	r := New(Config{Active: true})
	for i := range 5 {
		r.RecordResponse(RPCTypeXML, "m", []any{i}, i)
	}
	if err := r.Persist(ctx, store, "main", "cap"); err != nil {
		t.Fatalf("Persist: %v", err)
	}

	// Load with cap=3.
	r2 := New(Config{Active: true})
	if err := r2.Load(ctx, store, "main", "cap", 3); err != nil {
		t.Fatalf("Load with cap: %v", err)
	}

	meta := r2.Metadata()
	total := meta["total_entries"].(int)
	if total > 3 {
		t.Errorf("after Load(max=3) total_entries=%d want <=3", total)
	}
}

// ---------------------------------------------------------------------------
// Auto-persist: periodic flush happens, closer stops it
// ---------------------------------------------------------------------------

func TestRecorderSetAutoPersistAndClose(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newFakeStore()

	r := New(Config{Active: true})
	r.RecordResponse(RPCTypeXML, "ping", nil, "pong")

	stop := r.SetAutoPersist(ctx, store, "main", "auto", 20*time.Millisecond)

	// Wait long enough for at least one auto-persist tick.
	time.Sleep(80 * time.Millisecond)
	stop()

	// Verify that the store received rows (at least one flush happened).
	rows, err := store.Load(ctx, "main", "auto", 0)
	if err != nil {
		t.Fatalf("Load after auto-persist: %v", err)
	}
	if len(rows) == 0 {
		t.Error("auto-persist must have flushed at least one row")
	}
}

// ---------------------------------------------------------------------------
// SetAutoPersist with interval <= 0 returns a no-op stop
// ---------------------------------------------------------------------------

func TestRecorderSetAutoPersistNoopInterval(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newFakeStore()

	r := New(Config{Active: true})
	r.RecordResponse(RPCTypeXML, "ping", nil, "pong")

	stop := r.SetAutoPersist(ctx, store, "main", "noop", 0)
	stop() // must not panic

	// Nothing should have been flushed.
	rows, _ := store.Load(ctx, "main", "noop", 0)
	if len(rows) != 0 {
		t.Errorf("no-op auto-persist must not flush, got %d rows", len(rows))
	}
}

// ---------------------------------------------------------------------------
// SetAutoPersist respects ctx cancellation
// ---------------------------------------------------------------------------

func TestRecorderSetAutoPersistContextCancel(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	store := newFakeStore()

	r := New(Config{Active: true})
	r.RecordResponse(RPCTypeXML, "ping", nil, "pong")

	stop := r.SetAutoPersist(ctx, store, "main", "cancel", 10*time.Millisecond)
	defer stop()

	time.Sleep(50 * time.Millisecond)
	cancel() // trigger ctx.Done path

	// Give the goroutine a moment to exit before we check for leaks
	// (test framework exits cleanly regardless).
	time.Sleep(20 * time.Millisecond)
}

// ---------------------------------------------------------------------------
// Persist on empty recorder produces zero rows
// ---------------------------------------------------------------------------

func TestRecorderPersistEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newFakeStore()

	r := New(Config{Active: true})
	if err := r.Persist(ctx, store, "main", "empty"); err != nil {
		t.Fatalf("Persist on empty: %v", err)
	}

	rows, _ := store.Load(ctx, "main", "empty", 0)
	if len(rows) != 0 {
		t.Errorf("Persist on empty recorder must yield 0 rows, got %d", len(rows))
	}
}
