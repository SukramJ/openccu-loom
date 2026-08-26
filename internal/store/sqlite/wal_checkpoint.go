// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"time"
)

// DefaultWALCheckpointInterval is the default cadence for the periodic
// PASSIVE WAL checkpoint. Four hours keeps the WAL file bounded on
// embedded/busy targets without adding measurable write pressure.
// Operators can override via the store option passed to
// StartWALCheckpointLoop.
const DefaultWALCheckpointInterval = 4 * time.Hour

// WALCheckpointResult carries the counts returned by SQLite's
// wal_checkpoint pragma: the total number of WAL frames and the number
// of frames successfully transferred to the database file.
type WALCheckpointResult struct {
	// Busy is true when one or more frames could not be checkpointed
	// because a concurrent reader held a read lock on them. This is
	// normal in a PASSIVE checkpoint and not an error.
	Busy bool
	// TotalFrames is the total number of frames in the WAL at checkpoint
	// time.
	TotalFrames int
	// CheckpointedFrames is the number of frames moved to the DB file.
	// When CheckpointedFrames == TotalFrames and Busy == false the WAL
	// is fully checkpointed and SQLite may reset it on the next write.
	CheckpointedFrames int
}

// CheckpointWAL executes a single PASSIVE wal_checkpoint on db. It is
// a no-op for in-memory databases (WAL mode is not applicable there).
// Errors are returned to the caller for logging; a checkpoint failure
// never crashes the process.
func CheckpointWAL(ctx context.Context, db *sql.DB) (WALCheckpointResult, error) {
	if isMemoryDSN(ctx, db) {
		return WALCheckpointResult{}, nil
	}
	// PRAGMA wal_checkpoint(PASSIVE) returns three integer columns:
	//   busy         – 1 if not all frames could be checkpointed, 0 otherwise
	//   log          – total WAL frame count
	//   checkpointed – frames moved to the DB file
	var busy, log, checkpointed int
	row := db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)")
	if err := row.Scan(&busy, &log, &checkpointed); err != nil {
		return WALCheckpointResult{}, err
	}
	return WALCheckpointResult{
		Busy:               busy != 0,
		TotalFrames:        log,
		CheckpointedFrames: checkpointed,
	}, nil
}

// StartWALCheckpointLoop starts a background goroutine that calls
// CheckpointWAL every interval. Pass interval == 0 to use
// DefaultWALCheckpointInterval. The goroutine is cancelled by the
// returned stop function, which also performs one final checkpoint on
// shutdown so the WAL is as small as possible after a graceful stop.
//
// Errors from individual checkpoint calls are logged at debug level (a
// busy checkpoint is expected during normal read activity) and do not
// stop the loop. The loop is a no-op for in-memory databases.
//
// Usage:
//
//	stop := sqlite.StartWALCheckpointLoop(db, 4*time.Hour, logger)
//	defer stop()
func StartWALCheckpointLoop(db *sql.DB, interval time.Duration, logger *slog.Logger) func() {
	if db == nil {
		return func() {}
	}
	if interval <= 0 {
		interval = DefaultWALCheckpointInterval
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				// Final checkpoint on graceful shutdown. Use a fresh
				// context because ctx is already cancelled.
				//nolint:contextcheck // shutdown path must not inherit the cancelled loop ctx
				runCheckpoint(context.Background(), db, logger, "shutdown")
				return
			case <-ticker.C:
				runCheckpoint(ctx, db, logger, "tick")
			}
		}
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}
}

// runCheckpoint executes one WAL checkpoint and logs the outcome.
func runCheckpoint(ctx context.Context, db *sql.DB, logger *slog.Logger, trigger string) {
	res, err := CheckpointWAL(ctx, db)
	if err != nil {
		if logger != nil {
			logger.Warn("sqlite.wal_checkpoint.err",
				slog.String("trigger", trigger),
				slog.String("err", err.Error()))
		}
		return
	}
	if logger != nil {
		logger.Debug("sqlite.wal_checkpoint",
			slog.String("trigger", trigger),
			slog.Bool("busy", res.Busy),
			slog.Int("total_frames", res.TotalFrames),
			slog.Int("checkpointed_frames", res.CheckpointedFrames))
	}
}
