// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"log/slog"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
)

// DefaultUnobservedSweepInterval is the cadence at which
// [StartUnobservedSweepLoop] re-runs the bootstrap-style LoadValue
// retry across every device in the central. 5 minutes mirrors
// scaled up by 60× because openccu-loom has a permanent push channel
// and only needs a slow safety net for stragglers (event DPs that
// have not fired since the last CCU restart, RELEVANT_INIT
// parameters that fetch_all_device_data omitted).
const DefaultUnobservedSweepInterval = 5 * time.Minute

// StartUnobservedSweepLoop runs an [UnobservedSweep] in a dedicated
// goroutine that ticks at the given interval until ctx is cancelled.
// Returns a stop closure the caller MUST defer; the closure waits
// for the in-flight tick to complete before returning so the caller
// can rely on the goroutine being fully drained.
//
// The sweep walks every central in the registry and triggers
// [device.Device.LoadValue] for any DP on the bootstrap whitelist
// (RELEVANT_INIT_PARAMETERS, readable events) that is still
// unobserved. Already-observed DPs are skipped without a CCU
// round-trip. Idempotent — repeated ticks during steady-state
// degenerate into pure map walks with no wire calls.
//
// Pass interval = 0 for [DefaultUnobservedSweepInterval]. A negative
// value disables the loop (returns a no-op stop closure without
// spawning a goroutine).
//
// The goroutine path bypasses [scheduler.Scheduler] because the
// scheduler does not allow adding jobs after Start, and the
// south-bound wiring (which produces the devices the sweep walks)
// runs after [Registry.StartAll]. A standalone ticker keeps the
// lifecycle simple.
func StartUnobservedSweepLoop(
	ctx context.Context,
	sweep *UnobservedSweep,
	interval time.Duration,
	logger *slog.Logger,
) (stop func()) {
	noop := func() {}
	if sweep == nil {
		return noop
	}
	if interval < 0 {
		return noop
	}
	if interval == 0 {
		interval = DefaultUnobservedSweepInterval
	}

	loopCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-loopCtx.Done():
				return
			case <-ticker.C:
				loaded, errored := sweep.SweepUnobserved(loopCtx)
				if logger != nil && (loaded > 0 || errored > 0) {
					logger.Debug("unobserved_sweep.tick",
						slog.Int("loaded", loaded),
						slog.Int("errored", errored))
				}
			}
		}
	}()

	return func() {
		cancel()
		<-done
	}
}

// SweepUnobservedForCentral exposes the per-central walk so callers
// that already hold a [*central.Unit] handle (e.g. a future
// REST endpoint that triggers a manual sweep on one CCU) can target
// it directly without going through the registry. The full
// [UnobservedSweep.SweepUnobserved] still iterates every central,
// which is what [StartUnobservedSweepLoop] uses on every tick.
func (s *UnobservedSweep) SweepUnobservedForCentral(ctx context.Context, unit *central.Unit) (loaded, errored int) {
	if s == nil || unit == nil {
		return 0, 0
	}
	return s.sweepCentral(ctx, unit)
}
