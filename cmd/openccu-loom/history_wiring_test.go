// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// seedHistoryDB writes one raw sample far outside any sane retention
// window and one from just now into a history database under dataDir,
// then closes the handle so the daemon can open the file itself.
func seedHistoryDB(t *testing.T, dataDir string) {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.OpenHistory(ctx, sqlite.FileDSN(filepath.Join(dataDir, "history.db")))
	if err != nil {
		t.Fatalf("sqlite.OpenHistory (seed): %v", err)
	}
	store := sqlite.NewMeasurementStore(db)
	now := time.Now()
	if err := store.SaveBatch(ctx, []sqlite.MeasurementSample{
		{
			CentralName: "ccu1", InterfaceID: "HmIP-RF", ChannelAddress: "DEV0001:1",
			Parameter: "ACTUAL_TEMPERATURE", TS: now.Add(-200 * 24 * time.Hour), Value: 19,
		},
		{
			CentralName: "ccu1", InterfaceID: "HmIP-RF", ChannelAddress: "DEV0001:1",
			Parameter: "ACTUAL_TEMPERATURE", TS: now.Add(-time.Minute), Value: 21,
		},
	}); err != nil {
		t.Fatalf("SaveBatch (seed): %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}
}

// historyRowCount reopens the history database and reports how many raw
// samples survive. Called after the daemon released its own handle.
func historyRowCount(t *testing.T, dataDir string) int64 {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.OpenHistory(ctx, sqlite.FileDSN(filepath.Join(dataDir, "history.db")))
	if err != nil {
		t.Fatalf("sqlite.OpenHistory (verify): %v", err)
	}
	defer func() { _ = db.Close() }()
	stats, err := sqlite.NewMeasurementStore(db).Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	return stats.Rows
}

// TestSharedInfrastructureEvictsHistoryWithRecordingDisabled pins the
// retention purge onto the recording-off path. Retention used to hang off
// the recorder, and the recorder off the store, and the store off the
// enabled flag — so an operator who switched history off to reclaim disk
// froze history.db at its final size instead of watching it drain. The
// daemon's own infrastructure wiring is the surface under test; nothing
// here starts a recorder or a purge itself.
func TestSharedInfrastructureEvictsHistoryWithRecordingDisabled(t *testing.T) {
	dataDir := t.TempDir()
	seedHistoryDB(t, dataDir)
	if got := historyRowCount(t, dataDir); got != 2 {
		t.Fatalf("seeded row count = %d, want 2", got)
	}

	disabled := false
	cfg := &config.Config{DataDir: dataDir}
	cfg.Persistence.History.Enabled = &disabled
	cfg.Persistence.History.Retention = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	si, teardown := wireSharedInfrastructure(ctx, cfg, discardTestLogger(), central.NewRegistry(), &reloadDeps{}, nil)

	// The recording feature stays off: no history store, so no /history
	// REST surface and no `history` runtime capability.
	if si.historyStore != nil {
		t.Error("recording is disabled but the shared history store was opened; " +
			"that would advertise the history REST surface and capability")
	}

	// The eviction runs one pass before its first tick, so the stale row
	// is gone shortly after boot. Poll rather than sleep a fixed span.
	deadline := time.Now().Add(10 * time.Second)
	var rows int64 = -1
	for time.Now().Before(deadline) {
		if rows = historyRowCount(t, dataDir); rows == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	teardown()

	if rows != 1 {
		t.Fatalf("raw rows after boot with recording disabled = %d, want 1 "+
			"(the 200-day-old sample must be evicted, the recent one kept); "+
			"history.db never shrinks while the feature is off", rows)
	}
	if got := historyRowCount(t, dataDir); got != 1 {
		t.Errorf("raw rows after teardown = %d, want 1", got)
	}
}

// TestWireHistoryRetentionIsANoOpWithoutADatabase covers the two cases
// that must not touch the disk: recording enabled (the recorder owns the
// loop) and no history database at all — a disabled feature must never
// create the file it is meant to be reclaiming.
func TestWireHistoryRetentionIsANoOpWithoutADatabase(t *testing.T) {
	t.Parallel()

	t.Run("no database on disk", func(t *testing.T) {
		t.Parallel()
		dataDir := t.TempDir()
		disabled := false
		cfg := &config.Config{DataDir: dataDir}
		cfg.Persistence.History.Enabled = &disabled

		wireHistoryRetention(cfg, discardTestLogger())()

		matches, err := filepath.Glob(filepath.Join(dataDir, "history.db*"))
		if err != nil {
			t.Fatalf("glob: %v", err)
		}
		if len(matches) != 0 {
			t.Errorf("retention wiring created %v; a disabled feature must not create its database", matches)
		}
	})

	t.Run("recording enabled", func(t *testing.T) {
		t.Parallel()
		dataDir := t.TempDir()
		seedHistoryDB(t, dataDir)
		enabled := true
		cfg := &config.Config{DataDir: dataDir}
		cfg.Persistence.History.Enabled = &enabled
		cfg.Persistence.History.Retention = time.Hour

		wireHistoryRetention(cfg, discardTestLogger())()

		if got := historyRowCount(t, dataDir); got != 2 {
			t.Errorf("rows = %d, want 2: with recording enabled the recorder owns the purge "+
				"and this path must not open a second handle on the same file", got)
		}
	})
}
