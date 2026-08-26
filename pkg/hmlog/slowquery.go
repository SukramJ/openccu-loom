// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmlog

import (
	"context"
	"log/slog"
	"time"
)

// DefaultSlowQueryThreshold matches the budget from SPECIFICATION
// §13.2 (response p99 under 100 ms). Callers that want a stricter or
// laxer threshold pass an explicit value.
const DefaultSlowQueryThreshold = 100 * time.Millisecond

// WatchSlow starts a timer for a hot path (typically a SQLite query
// or RPC roundtrip) and returns a function that, when called, logs a
// single Warn-level record if the elapsed time is at least
// `threshold`. The record carries the operation name, the elapsed
// milliseconds, and the W3C trace IDs that the [ContextHandler]
// already injects.
//
// Unlike [StartOp], WatchSlow emits no start record and stays silent
// on fast paths. That keeps the daemon's normal-case noise floor low
// even when wired into hundreds of query call-sites. A zero or
// negative threshold defaults to [DefaultSlowQueryThreshold].
//
// Usage:
//
//	defer hmlog.WatchSlow(ctx, slog.Default(), "paramsets.bulk_upsert", 0)()
//
// The deferred call captures the elapsed time at function exit
// without requiring the caller to manage the timer manually.
func WatchSlow(ctx context.Context, logger *slog.Logger, op string, threshold time.Duration) func() {
	if logger == nil {
		logger = slog.Default()
	}
	if threshold <= 0 {
		threshold = DefaultSlowQueryThreshold
	}
	started := time.Now()
	return func() {
		elapsed := time.Since(started)
		if elapsed < threshold {
			return
		}
		logger.LogAttrs(
			ctx, slog.LevelWarn, "query.slow",
			slog.String("op", op),
			slog.Float64("elapsed_ms", float64(elapsed.Milliseconds())),
			slog.Float64("threshold_ms", float64(threshold.Milliseconds())),
		)
	}
}
