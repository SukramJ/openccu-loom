// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/SukramJ/openccu-loom/internal/health"
)

// StoreComponentName is the name reported to the health tracker for
// the SQLite persistence layer.
const StoreComponentName = "sqlite"

// DefaultProbeInterval is the cadence at which the probe pings the
// database. 30 s sits comfortably under the tracker's default
// [health.DefaultStaleAfter] (90 s) — two missed probes already
// downgrade the store to UNKNOWN.
const DefaultProbeInterval = 30 * time.Second

// Latency thresholds applied to the `SELECT 1` round-trip. The
// healthy budget mirrors the §13.2 spec ("response p99 under
// 100 ms"); 500 ms is the established degraded boundary used by the
// daemon's REST-side budget warnings.
//
// Declared as vars (not consts) so a test can shrink them temporarily and
// force the elevated/slow branches deterministically, without depending on
// a genuinely slow database round-trip.
var (
	healthyLatencyBudget  = 100 * time.Millisecond
	degradedLatencyBudget = 500 * time.Millisecond
)

// HealthRecorder is the slim contract the probe needs from the
// [*health.Tracker]. Defined here so tests can drop in a fake
// without pulling the full tracker.
type HealthRecorder interface {
	Record(name string, sample health.Sample)
	// RecordQuality reports a soft, non-fatal observation: the resulting
	// status is capped at DEGRADED and can never escalate to UNHEALTHY. The
	// probe uses it for elevated latency, which is not a database failure.
	RecordQuality(name, note string)
}

// StartHealthProbe spawns a goroutine that pings db on a fixed cadence
// and reports the round-trip outcome to tracker as a [StoreComponentName]
// sample. The returned stop function cancels the goroutine and waits
// for it to exit.
//
// The probe is silent when tracker or db is nil — callers in tests
// that wire only one of them get a no-op stopper.
func StartHealthProbe(ctx context.Context, db *sql.DB, tracker HealthRecorder, interval time.Duration) func() {
	if db == nil || tracker == nil {
		return func() {}
	}
	if interval <= 0 {
		interval = DefaultProbeInterval
	}
	pctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		// One immediate probe so the SPA's Diagnostics view does not
		// have to wait `interval` for the first sample after boot.
		probeOnce(pctx, db, tracker)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-pctx.Done():
				return
			case <-ticker.C:
				probeOnce(pctx, db, tracker)
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// probeOnce runs a single `SELECT 1` round-trip and converts the
// outcome to a [health.Sample]. The query itself is cheap; the
// per-conn budget is bounded — above [busyTimeout] — so a stuck
// DB cannot block the probe goroutine indefinitely while a lock wait
// SQLite itself would still resolve is not misreported as a failure.
//
// The deadline is derived from [busyTimeout] rather than restated: SQLite
// holds a writer-blocked query open for up to that long before returning
// SQLITE_BUSY, so a deadline below it would trip the probe — and with it
// /health's 503 — on a database that is waiting exactly as configured.
func probeOnce(ctx context.Context, db *sql.DB, tracker HealthRecorder) {
	queryCtx, cancel := context.WithTimeout(ctx, busyTimeout+degradedLatencyBudget)
	defer cancel()
	started := time.Now()
	var one int
	err := db.QueryRowContext(queryCtx, "SELECT 1").Scan(&one)
	elapsed := time.Since(started)

	switch {
	case err != nil:
		// A genuine query error (not merely elevated latency) is what
		// UNHEALTHY exists to report: sqlite is a critical component, so
		// this trips ServiceAvailability/HTTP 503 immediately via the
		// second Record below, which escalates DEGRADED to UNHEALTHY.
		tracker.Record(StoreComponentName, health.Sample{
			Healthy: false,
			Note:    fmt.Sprintf("probe failed: %v (elapsed=%s)", err, elapsed),
		})
		// Hit again so the flap-damp escalates DEGRADED to UNHEALTHY
		// immediately — a failed probe is unambiguous.
		tracker.Record(StoreComponentName, health.Sample{
			Healthy: false,
			Note:    "probe failed (escalated)",
		})
	case elapsed > degradedLatencyBudget:
		// Elevated latency alone is not a database failure — capped at
		// DEGRADED via RecordQuality, mirroring how ping/pong correlation
		// noise is capped, so two slow-but-successful round-trips in a row
		// cannot escalate a fully working database to UNHEALTHY/503.
		tracker.RecordQuality(StoreComponentName,
			fmt.Sprintf("slow probe: %s > %s", elapsed, degradedLatencyBudget))
	case elapsed > healthyLatencyBudget:
		// Degraded — the note carries the actual latency so the SPA can
		// show "120 ms (budget 100 ms)" instead of just a status flip.
		tracker.RecordQuality(StoreComponentName,
			fmt.Sprintf("elevated probe: %s > %s", elapsed, healthyLatencyBudget))
	default:
		tracker.Record(StoreComponentName, health.Sample{
			Healthy: true,
			Note:    fmt.Sprintf("probe ok (%s)", elapsed),
		})
	}
}
