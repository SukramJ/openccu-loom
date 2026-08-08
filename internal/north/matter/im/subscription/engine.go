// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package subscription

import (
	"context"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
)

// run is the engine's tick loop. Iterates the live subscription set
// every Config.TickInterval, emitting:
//
//   - dirty-paths reports when MinIntervalFloor has elapsed,
//   - keep-alive reports when MaxIntervalCeiling has elapsed.
//
// Closed subscriptions are skipped. The reporter callback is invoked
// outside the manager's lock so it can safely block on the wire.
//
// Shared engine ticker vs. matter.js per-subscription timer (by-design):
// matter.js (`packages/node/src/node/server/ServerSubscription.ts:191`) gives
// each ServerSubscription its own `Time.getTimer(sendInterval, callback)`.
// chip (`src/app/ReadHandler.cpp`) uses per-ReadHandler state inside the IM
// engine — one state machine per subscription. openccu-loom uses a single shared
// ticker (default 250 ms) that iterates all subscriptions. The ticker granularity
// means a subscription's report can fire up to 250 ms early compared to a strict
// per-timer model. This is unobservable by any Matter commissioner (Apple Home,
// chip-tool) because the early-fire falls well within the spec-allowed window and
// the MinIntervalFloor gate already protects against over-reporting. The shared
// ticker is the idiomatic Go approach: per-subscription goroutines would create
// O(N_subscriptions) timers and goroutines for a problem that is trivially
// solved by a single shared poller. Documented in
// `notes/parity/by_design.md` §"Systematic Parity Run #02".
func (m *Manager) run(ctx context.Context) {
	defer m.wg.Done()
	ticker := time.NewTicker(m.cfg.TickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopCh:
			return
		case now := <-ticker.C:
			m.tick(ctx, now)
		}
	}
}

// tick walks every subscription once. Public for tests that want to
// drive the engine deterministically without waiting for the timer.
func (m *Manager) tick(ctx context.Context, now time.Time) {
	m.mu.RLock()
	eventReporter := m.eventReporter
	m.mu.RUnlock()

	for _, sub := range m.snapshot() {
		if sub.IsClosed() {
			continue
		}
		// Pending events drain whenever MinInterval has elapsed —
		// Critical priority bypasses the gate inside the subscription.
		// Drains BEFORE attribute dirty so urgent events do not wait
		// behind a value sweep.
		//
		// After an event drain the same-tick dirty drain is explicitly
		// skipped. drainEventsIfElapsed stamps lastReport=now,
		// which makes drainDirtyIfElapsed return nil (0 < MinIntervalFloor)
		// for floor>0, but the skip-flag makes the intent unambiguous and
		// prevents a double-report when floor=0 (degenerate subscription
		// created outside the manager). Apple Home's duplicate-suppression
		// heuristic rejects two consecutive ReportData frames in the same
		// 250 ms window regardless of which path produced them.
		// matter.js ref: ServerSubscription.ts — events and dirty attrs
		// are merged into a single ReportData per send cycle.
		eventDrained := false
		if events := sub.drainEventsIfElapsed(now); len(events) > 0 {
			eventDrained = true
			if eventReporter != nil {
				out := make([]im.EventReport, 0, len(events))
				for _, ev := range events {
					ts := ev.Timestamp
					if ts == 0 {
						ts = uint64(now.UnixMilli()) //nolint:gosec // millis fit uint64; see #20
					}
					out = append(out, im.EventReport{
						Path:      ev.Path,
						Number:    ev.Number,
						Priority:  ev.Priority,
						Timestamp: ts,
						Data:      ev.Data,
					})
				}
				eventReporter(ctx, sub, out)
			}
		}
		// Dirty-path report fires when MinInterval has elapsed, but only
		// when no event report fired in the same tick. The
		// lastReport stamp inside drainEventsIfElapsed already prevents
		// drainDirtyIfElapsed from firing when floor>0; the explicit
		// eventDrained guard closes the degenerate floor=0 case and
		// makes the intent unambiguous at code-review time.
		if dirty := sub.drainDirtyIfElapsed(now); !eventDrained && len(dirty) > 0 {
			if m.reporter != nil {
				m.reporter(ctx, sub, dirty)
			}
			continue
		}
		// Keep-alive: nothing dirty, but the publisher-side heartbeat
		// cadence (≈ matter.js sendInterval; see
		// [Subscription.heartbeatIntervalElapsed]) has elapsed. Apple
		// Home's MTRDevice and chip-tool's ReadClient both drop the
		// subscription after an internal timer that fires *well before*
		// `MaxIntervalCeiling` would — so heartbeats must ride at the
		// faster matter.js-style cadence (`min(maxInterval/2, 30s)`-ish),
		// not at the spec-only `MaxIntervalCeiling`.
		if sub.heartbeatIntervalElapsed(now) {
			sub.touchLastReport(now)
			if m.reporter != nil {
				m.reporter(ctx, sub, nil)
			}
		}
	}
}

// Tick is the test surface for [Manager.tick].
func (m *Manager) Tick(ctx context.Context, now time.Time) {
	m.tick(ctx, now)
}
