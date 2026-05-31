// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/store/session"
)

// openSessionStore returns a migrated in-memory db and a SessionRecorderStore.
func openSessionStore(t *testing.T) *SessionRecorderStore {
	t.Helper()
	// Re-use the shared openMu to avoid a goose data-race on the base FS global.
	openMu.Lock()
	db, err := Open(context.Background(), "file::memory:?cache=shared&_"+t.Name())
	openMu.Unlock()
	if err != nil {
		t.Fatalf("openSessionStore: Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewSessionRecorderStore(db)
}

// ---------------------------------------------------------------------------
// PersistAll / Load round-trip
// ---------------------------------------------------------------------------

func TestSessionRecorderStoreRoundTrip(t *testing.T) {
	t.Parallel()
	s := openSessionStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	rows := []session.PersistRow{
		{
			CentralName:  "main",
			Slug:         "diag",
			RPCType:      "xml",
			Method:       "getDeviceDescription",
			FrozenParams: `[]interface {}{"VCU1234567"}`,
			ResponseJSON: `{"TYPE":"HM-CC-RT-DN"}`,
			RecordedAt:   now,
			TTLSeconds:   0,
		},
		{
			CentralName:  "main",
			Slug:         "diag",
			RPCType:      "xml",
			Method:       "listDevices",
			FrozenParams: `<nil>`,
			ResponseJSON: `["dev1","dev2"]`,
			RecordedAt:   now.Add(-1 * time.Second),
			TTLSeconds:   300,
		},
	}

	if err := s.PersistAll(ctx, "main", "diag", rows); err != nil {
		t.Fatalf("PersistAll: %v", err)
	}

	got, err := s.Load(ctx, "main", "diag", 0)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Load returned %d rows, want 2", len(got))
	}
	// Most recent first.
	if got[0].Method != "getDeviceDescription" {
		t.Errorf("got[0].Method=%q want getDeviceDescription", got[0].Method)
	}
	if got[1].Method != "listDevices" {
		t.Errorf("got[1].Method=%q want listDevices", got[1].Method)
	}
}

// ---------------------------------------------------------------------------
// PersistAll replaces on second call
// ---------------------------------------------------------------------------

func TestSessionRecorderStorePersistAllReplaces(t *testing.T) {
	t.Parallel()
	s := openSessionStore(t)
	ctx := context.Background()

	row1 := []session.PersistRow{{
		CentralName: "main", Slug: "s1", RPCType: "xml", Method: "ping",
		FrozenParams: `<nil>`, ResponseJSON: `"pong"`, RecordedAt: time.Now().UTC(),
	}}
	row2 := []session.PersistRow{{
		CentralName: "main", Slug: "s1", RPCType: "json", Method: "listDevices",
		FrozenParams: `<nil>`, ResponseJSON: `[]`, RecordedAt: time.Now().UTC(),
	}}

	if err := s.PersistAll(ctx, "main", "s1", row1); err != nil {
		t.Fatalf("first PersistAll: %v", err)
	}
	if err := s.PersistAll(ctx, "main", "s1", row2); err != nil {
		t.Fatalf("second PersistAll: %v", err)
	}

	got, err := s.Load(ctx, "main", "s1", 0)
	if err != nil {
		t.Fatalf("Load after replace: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("after second PersistAll len=%d want 1 (replace semantics)", len(got))
	}
	if got[0].Method != "listDevices" {
		t.Errorf("got[0].Method=%q want listDevices", got[0].Method)
	}
}

// ---------------------------------------------------------------------------
// MaxLoadEntries cap
// ---------------------------------------------------------------------------

func TestSessionRecorderStoreMaxLoadEntries(t *testing.T) {
	t.Parallel()
	s := openSessionStore(t)
	ctx := context.Background()

	const total = 10
	rows := make([]session.PersistRow, total)
	base := time.Now().UTC()
	for i := range rows {
		rows[i] = session.PersistRow{
			CentralName:  "main",
			Slug:         "cap",
			RPCType:      "xml",
			Method:       "m",
			FrozenParams: string(rune('A' + i)),
			ResponseJSON: `null`,
			RecordedAt:   base.Add(time.Duration(i) * time.Millisecond),
		}
	}

	if err := s.PersistAll(ctx, "main", "cap", rows); err != nil {
		t.Fatalf("PersistAll: %v", err)
	}

	const limit = 3
	got, err := s.Load(ctx, "main", "cap", limit)
	if err != nil {
		t.Fatalf("Load(limit=%d): %v", limit, err)
	}
	if len(got) != limit {
		t.Fatalf("Load returned %d rows, want %d", len(got), limit)
	}
}

// ---------------------------------------------------------------------------
// DeleteAll
// ---------------------------------------------------------------------------

func TestSessionRecorderStoreDeleteAll(t *testing.T) {
	t.Parallel()
	s := openSessionStore(t)
	ctx := context.Background()

	row := []session.PersistRow{{
		CentralName: "main", Slug: "del", RPCType: "xml", Method: "ping",
		FrozenParams: `<nil>`, ResponseJSON: `"pong"`, RecordedAt: time.Now().UTC(),
	}}
	if err := s.PersistAll(ctx, "main", "del", row); err != nil {
		t.Fatalf("PersistAll: %v", err)
	}
	if err := s.DeleteAll(ctx, "main", "del"); err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}

	got, err := s.Load(ctx, "main", "del", 0)
	if err != nil {
		t.Fatalf("Load after DeleteAll: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("after DeleteAll Load returned %d rows, want 0", len(got))
	}
}

// ---------------------------------------------------------------------------
// CountEntries
// ---------------------------------------------------------------------------

func TestSessionRecorderStoreCountEntries(t *testing.T) {
	t.Parallel()
	s := openSessionStore(t)
	ctx := context.Background()

	n, err := s.CountEntries(ctx, "main", "cnt")
	if err != nil {
		t.Fatalf("CountEntries on empty: %v", err)
	}
	if n != 0 {
		t.Fatalf("initial count=%d want 0", n)
	}

	rows := []session.PersistRow{
		{CentralName: "main", Slug: "cnt", RPCType: "xml", Method: "a", FrozenParams: "x", ResponseJSON: `1`, RecordedAt: time.Now().UTC()},
		{CentralName: "main", Slug: "cnt", RPCType: "xml", Method: "b", FrozenParams: "y", ResponseJSON: `2`, RecordedAt: time.Now().UTC()},
	}
	if err := s.PersistAll(ctx, "main", "cnt", rows); err != nil {
		t.Fatalf("PersistAll: %v", err)
	}

	n, err = s.CountEntries(ctx, "main", "cnt")
	if err != nil {
		t.Fatalf("CountEntries after insert: %v", err)
	}
	if n != 2 {
		t.Fatalf("count=%d want 2", n)
	}
}

// ---------------------------------------------------------------------------
// Multi-CCU scoping: rows for central "a" must not bleed into central "b"
// ---------------------------------------------------------------------------

func TestSessionRecorderStoreMultiCCUScope(t *testing.T) {
	t.Parallel()
	s := openSessionStore(t)
	ctx := context.Background()

	rowA := []session.PersistRow{{
		CentralName: "ccu-a", Slug: "s", RPCType: "xml", Method: "ping",
		FrozenParams: "<nil>", ResponseJSON: `"pong-a"`, RecordedAt: time.Now().UTC(),
	}}
	rowB := []session.PersistRow{{
		CentralName: "ccu-b", Slug: "s", RPCType: "xml", Method: "ping",
		FrozenParams: "<nil>", ResponseJSON: `"pong-b"`, RecordedAt: time.Now().UTC(),
	}}

	if err := s.PersistAll(ctx, "ccu-a", "s", rowA); err != nil {
		t.Fatalf("PersistAll ccu-a: %v", err)
	}
	if err := s.PersistAll(ctx, "ccu-b", "s", rowB); err != nil {
		t.Fatalf("PersistAll ccu-b: %v", err)
	}

	gotA, _ := s.Load(ctx, "ccu-a", "s", 0)
	gotB, _ := s.Load(ctx, "ccu-b", "s", 0)

	if len(gotA) != 1 || gotA[0].ResponseJSON != `"pong-a"` {
		t.Errorf("ccu-a got %v", gotA)
	}
	if len(gotB) != 1 || gotB[0].ResponseJSON != `"pong-b"` {
		t.Errorf("ccu-b got %v", gotB)
	}
}

// ---------------------------------------------------------------------------
// DeleteAll and CountEntries combined
// ---------------------------------------------------------------------------

func TestSessionRecorderStoreDeleteAllAndCountEntries(t *testing.T) {
	s := openSessionStore(t)
	ctx := context.Background()

	// Insert two rows.
	rows := []session.PersistRow{
		{CentralName: "ccu1", Slug: "slug1", RPCType: "xml", Method: "getParamset", RecordedAt: time.Now()},
		{CentralName: "ccu1", Slug: "slug1", RPCType: "xml", Method: "getValue", RecordedAt: time.Now()},
	}
	if err := s.PersistAll(ctx, "ccu1", "slug1", rows); err != nil {
		t.Fatalf("PersistAll: %v", err)
	}

	// CountEntries.
	n, err := s.CountEntries(ctx, "ccu1", "slug1")
	if err != nil {
		t.Fatalf("CountEntries: %v", err)
	}
	if n != 2 {
		t.Errorf("CountEntries=%d, want 2", n)
	}

	// DeleteAll.
	if err := s.DeleteAll(ctx, "ccu1", "slug1"); err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}

	n2, _ := s.CountEntries(ctx, "ccu1", "slug1")
	if n2 != 0 {
		t.Errorf("CountEntries after DeleteAll=%d, want 0", n2)
	}
}
