// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"testing"
	"time"
)

// TestValuesCacheStore_DB_ReturnsHandle verifies that DB() returns the
// underlying *sql.DB and that a WAL checkpoint can be run against it.
// This is the minimal smoke test for the values-cache WAL checkpoint path:
// the loop itself (StartWALCheckpointLoop) is exercised by its own tests;
// this test verifies that DB() exposes a handle that CheckpointWAL accepts.
func TestValuesCacheStore_DB_ReturnsHandle(t *testing.T) {
	t.Parallel()
	s := freshValuesCacheStore(t)

	db := s.DB()
	if db == nil {
		t.Fatal("DB() returned nil for a live store")
	}

	res, err := CheckpointWAL(context.Background(), db)
	if err != nil {
		t.Fatalf("CheckpointWAL on values-cache DB: %v", err)
	}
	if res.TotalFrames < 0 || res.CheckpointedFrames < 0 {
		t.Errorf("negative frame counts: total=%d checkpointed=%d",
			res.TotalFrames, res.CheckpointedFrames)
	}
}

// TestValuesCacheStore_DB_NilStore verifies that DB() on a nil store returns
// nil without panicking (important for the nil-guard in StartWALCheckpointLoop).
func TestValuesCacheStore_DB_NilStore(t *testing.T) {
	t.Parallel()
	var s *ValuesCacheStore
	if got := s.DB(); got != nil {
		t.Errorf("DB() on nil store returned non-nil: %v", got)
	}
}

// TestValuesCacheStore_WALCheckpointLoop_TickAndStop starts the WAL checkpoint
// loop against a real values-cache DB, waits for at least one tick, then
// stops it. This is a smoke test: the full loop semantics are covered by
// TestStartWALCheckpointLoop_TickAndStop in wal_checkpoint_test.go.
func TestValuesCacheStore_WALCheckpointLoop_TickAndStop(t *testing.T) {
	t.Parallel()
	s := freshValuesCacheStore(t)

	// Seed one row so the WAL has content that a checkpoint can transfer.
	ctx := context.Background()
	now := nowMS()
	if err := s.SaveValue(ctx, "ccu1", "HmIP-RF", "DEV:1", "STATE", true, now, now); err != nil {
		t.Fatalf("SaveValue: %v", err)
	}

	stop := StartWALCheckpointLoop(s.DB(), 50*time.Millisecond, nil)

	// Allow at least two ticks.
	time.Sleep(150 * time.Millisecond)

	// Stop must return without deadlock and run the final checkpoint.
	stop()
}
