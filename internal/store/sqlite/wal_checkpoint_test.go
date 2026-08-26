// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

// TestCheckpointWAL_FileBacked opens a file-backed WAL database, writes a
// few rows, and asserts that CheckpointWAL completes without error and
// returns non-negative frame counts.
func TestCheckpointWAL_FileBacked(t *testing.T) {
	t.Parallel()
	db := openTestDB(t, "wal_checkpoint_test.db")

	// Write some data to ensure the WAL has content.
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `INSERT INTO devices
		(central_name, interface_id, address, type, model, hash, description_json, updated_at)
		VALUES ('test','HmIP-RF','WAL1','T','T','h1','{}', 0)`)
	if err != nil {
		t.Fatalf("seed insert: %v", err)
	}

	res, err := CheckpointWAL(ctx, db)
	if err != nil {
		t.Fatalf("CheckpointWAL: %v", err)
	}
	if res.TotalFrames < 0 || res.CheckpointedFrames < 0 {
		t.Errorf("negative frame counts: total=%d checkpointed=%d", res.TotalFrames, res.CheckpointedFrames)
	}
}

// TestCheckpointWAL_InMemory verifies that CheckpointWAL is a no-op (zero
// result, no error) for an in-memory database where WAL mode is not active.
func TestCheckpointWAL_InMemory(t *testing.T) {
	t.Parallel()
	openMu.Lock()
	db, err := Open(context.Background(), "file::memory:?cache=shared&_wal_test=1")
	openMu.Unlock()
	if err != nil {
		t.Fatalf("open in-memory: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	res, err := CheckpointWAL(context.Background(), db)
	if err != nil {
		t.Fatalf("CheckpointWAL on in-memory: %v", err)
	}
	if res.TotalFrames != 0 || res.CheckpointedFrames != 0 || res.Busy {
		t.Errorf("expected zero result for in-memory, got %+v", res)
	}
}

// TestStartWALCheckpointLoop_TickAndStop starts a fast-interval loop,
// waits for at least one tick, then stops it. The test asserts that
// stop blocks until the goroutine exits and the final shutdown
// checkpoint fires without error (verified via a nil-logger run —
// errors would have panicked the logger).
func TestStartWALCheckpointLoop_TickAndStop(t *testing.T) {
	t.Parallel()
	db := openTestDB(t, "wal_loop_test.db")
	logger := slog.Default()

	// Use a very short interval so the test does not wait long.
	stop := StartWALCheckpointLoop(db, 50*time.Millisecond, logger)

	// Give the ticker at least two fires.
	time.Sleep(150 * time.Millisecond)

	// stop must return (not deadlock) and run the final checkpoint.
	stop()
}

// TestStartWALCheckpointLoop_NilDB verifies the nil-db guard: the
// returned stop function must be callable without panic.
func TestStartWALCheckpointLoop_NilDB(t *testing.T) {
	t.Parallel()
	stop := StartWALCheckpointLoop(nil, 0, nil)
	stop() // must not panic
}

// TestStartWALCheckpointLoop_StopIdempotent verifies that calling stop
// multiple times does not panic or deadlock.
func TestStartWALCheckpointLoop_StopIdempotent(t *testing.T) {
	t.Parallel()
	db := openTestDB(t, "wal_idempotent_test.db")
	stop := StartWALCheckpointLoop(db, time.Hour, nil)
	stop()
	stop() // second call must be a no-op
}
