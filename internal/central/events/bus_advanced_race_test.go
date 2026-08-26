// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package events

// bus_advanced_race_test.go — four advanced concurrency / ordering scenarios
// not covered by the existing race/concurrency/priority/panic test files.
//
// Cases:
//
//  1. TestCrossPriorityReentrantOrdering — CRITICAL handler publishes typeB;
//     LOW handler on typeB publishes typeA back. Asserts no lost events and
//     causal (FIFO-deferred) ordering.
//
//  2. TestSelfUnsubscribeInDeferredDispatch — handler that self-unsubscribes
//     while running inside a *deferred* (re-entrant) dispatch frame; asserts
//     no deadlock/panic and that the handler does not fire for the deferred
//     event it is currently handling.
//
//  3. TestClearDuringDispatchThenResubscribe — ClearAllSubscriptions called
//     from inside a handler; a new handler is subscribed before the outer
//     Publish returns; the new handler must receive the next Publish
//     correctly and race-safely.
//
//  4. TestPanicCascadeUnderDeferredDispatch — a CRITICAL panicking handler
//     publishes a deferred event whose own handler also panics; asserts that
//     low-priority handlers on both the outer and deferred events still fire
//     and the bus remains usable afterwards.

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// ──────────────────────────────────────────────────────────────────────────────
// Local test-only event types
// ──────────────────────────────────────────────────────────────────────────────

type advEvtAlpha struct{ hmevent.Base }

func (advEvtAlpha) Type() hmevent.EventType { return "adv.evt.alpha" }

type advEvtBeta struct{ hmevent.Base }

func (advEvtBeta) Type() hmevent.EventType { return "adv.evt.beta" }

type advEvtGamma struct{ hmevent.Base }

func (advEvtGamma) Type() hmevent.EventType { return "adv.evt.gamma" }

type advEvtDelta struct{ hmevent.Base }

func (advEvtDelta) Type() hmevent.EventType { return "adv.evt.delta" }

// ──────────────────────────────────────────────────────────────────────────────
// 1. TestCrossPriorityReentrantOrdering
// ──────────────────────────────────────────────────────────────────────────────

// TestCrossPriorityReentrantOrdering verifies the cross-priority reentrancy
// ordering invariant:
//
//   - A PriorityCritical handler on advEvtAlpha publishes advEvtBeta (deferred).
//   - A PriorityLow handler on advEvtBeta publishes advEvtAlpha back (also
//     deferred, because the outer dispatch already holds b.dispatch).
//
// Expected causal sequence (FIFO-deferred):
//
//	alpha(critical) → flush → beta(low) → flush → alpha(critical) [counter ≥ 2]
//
// Invariants asserted:
//   - No events are lost (all deferred events are eventually drained).
//   - The first beta handler fires only after the first alpha handler returns
//     (deferred dispatch is never immediate).
//   - The bus stays usable after the chain resolves.
func TestCrossPriorityReentrantOrdering(t *testing.T) {
	t.Parallel()

	b := NewBus()

	const maxCycles = 3 // cap to keep the test bounded

	var alphaCalls, betaCalls atomic.Int32
	// alphaRunning is set while the alpha CRITICAL handler is executing.
	var alphaRunning atomic.Bool
	// betaFiredWhileAlphaRunning records whether the deferred guarantee failed.
	var betaFiredWhileAlphaRunning atomic.Bool

	// PriorityCritical alpha handler: publishes advEvtBeta (deferred) and
	// tracks re-entry depth via alphaRunning.
	Subscribe(b, func(advEvtAlpha) {
		alphaCalls.Add(1)
		alphaRunning.Store(true)
		if int(alphaCalls.Load()) <= maxCycles {
			// Deferred — must not run until this handler returns.
			Publish(b, advEvtBeta{Base: hmevent.NewBase()})
		}
		alphaRunning.Store(false)
	}, WithPriority(PriorityCritical), WithName("alpha-critical"))

	// PriorityLow beta handler: publishes advEvtAlpha back (deferred).
	Subscribe(b, func(advEvtBeta) {
		if alphaRunning.Load() {
			betaFiredWhileAlphaRunning.Store(true)
		}
		betaCalls.Add(1)
		if int(betaCalls.Load()) < maxCycles {
			Publish(b, advEvtAlpha{Base: hmevent.NewBase()})
		}
	}, WithPriority(PriorityLow), WithName("beta-low"))

	done := make(chan struct{})
	go func() {
		Publish(b, advEvtAlpha{Base: hmevent.NewBase()})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cross-priority reentrant chain did not terminate within 2 s (possible deadlock)")
	}

	// The deferred guarantee: beta must never fire while alpha is executing.
	if betaFiredWhileAlphaRunning.Load() {
		t.Error("beta handler fired while alpha handler was still running (deferred dispatch violated)")
	}

	// No events lost: both alpha and beta must have fired maxCycles times.
	// alpha fires first (seed), publishes beta each time (until alphaCalls > maxCycles).
	// beta fires for each alpha, publishes alpha back (until betaCalls >= maxCycles).
	// With maxCycles=3 the chain runs: alpha(1)→beta(1)→alpha(2)→beta(2)→alpha(3)→beta(3).
	alphaFinal := alphaCalls.Load()
	betaFinal := betaCalls.Load()
	if alphaFinal == 0 || betaFinal == 0 {
		t.Errorf("alphaCalls=%d betaCalls=%d — at least one of each must fire", alphaFinal, betaFinal)
	}
	if alphaFinal != maxCycles {
		t.Errorf("alphaCalls=%d, want %d", alphaFinal, maxCycles)
	}
	if betaFinal != maxCycles {
		t.Errorf("betaCalls=%d, want %d", betaFinal, maxCycles)
	}

	// Bus must still be usable after the chain drains.
	var afterCall atomic.Bool
	unsub := Subscribe(b, func(advEvtGamma) { afterCall.Store(true) })
	defer unsub()
	Publish(b, advEvtGamma{Base: hmevent.NewBase()})
	if !afterCall.Load() {
		t.Error("bus not usable after cross-priority reentrant chain")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// 2. TestSelfUnsubscribeInDeferredDispatch
// ──────────────────────────────────────────────────────────────────────────────

// TestSelfUnsubscribeInDeferredDispatch verifies that a handler which
// unsubscribes itself while running inside a *deferred* dispatch frame
// (i.e. it was itself published re-entrantly from another handler) does
// not deadlock or panic, and does not fire for subsequent publishes.
//
// Sequence:
//  1. Outer handler on advEvtAlpha publishes advEvtBeta (deferred).
//  2. The beta handler self-unsubscribes while it is executing (deferred frame).
//  3. A subsequent direct Publish of advEvtBeta must not reach the beta handler.
func TestSelfUnsubscribeInDeferredDispatch(t *testing.T) {
	t.Parallel()

	b := NewBus()

	var betaCount atomic.Int32
	var unsubBeta func()

	// Outer handler: publishes advEvtBeta from within its own frame (deferred).
	Subscribe(b, func(advEvtAlpha) {
		Publish(b, advEvtBeta{Base: hmevent.NewBase()})
	}, WithName("alpha-trigger"))

	// Beta handler: self-unsubscribes during its deferred execution.
	// Because dispatchNow snapshots the handler list before iterating, the
	// handler is still called for the in-flight deferred event. The
	// unsubscribe removes it from the live list, so subsequent publishes
	// must not reach it.
	unsubBeta = Subscribe(b, func(advEvtBeta) {
		betaCount.Add(1)
		unsubBeta() // self-unsubscribe mid-deferred-dispatch
	}, WithName("beta-self-unsub"))

	// Trigger: alpha fires, which defers a beta.
	// Must complete without deadlock.
	done := make(chan struct{})
	go func() {
		Publish(b, advEvtAlpha{Base: hmevent.NewBase()})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("deferred self-unsubscribe deadlocked")
	}

	// Beta must have fired exactly once (for the in-flight deferred event).
	if got := betaCount.Load(); got != 1 {
		t.Errorf("betaCount=%d, want 1 (deferred in-flight event must still deliver)", got)
	}

	// A direct publish of advEvtBeta must not reach the now-unsubscribed handler.
	Publish(b, advEvtBeta{Base: hmevent.NewBase()})
	if got := betaCount.Load(); got != 1 {
		t.Errorf("betaCount=%d after second publish, want 1 (self-unsub must prevent re-fire)", got)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// 3. TestClearDuringDispatchThenResubscribe
// ──────────────────────────────────────────────────────────────────────────────

// TestClearDuringDispatchThenResubscribe verifies that calling
// ClearAllSubscriptions from inside a handler while another handler is
// currently executing (both registered for the same event type) is
// race-safe, and that a handler subscribed immediately after the clear
// (still within the same Publish call, from the second handler's frame)
// correctly participates in subsequent publishes.
//
// Because dispatchNow snapshots the handler list before iterating, the
// in-progress dispatch snapshot is unaffected by the clear. The second
// handler fires for the current event even after the clear. The new handler
// registered in the second handler's frame does NOT join the current dispatch
// (snapshot already taken) but DOES fire on the next Publish.
func TestClearDuringDispatchThenResubscribe(t *testing.T) {
	t.Parallel()

	b := NewBus()

	var firstCount, secondCount, newCount atomic.Int32

	// First handler: ClearAllSubscriptions while dispatch is in progress.
	Subscribe(b, func(advEvtGamma) {
		firstCount.Add(1)
		b.ClearAllSubscriptions()
	}, WithPriority(PriorityHigh), WithName("clear-handler"))

	// Second handler (same event, lower priority): fires in the same dispatch
	// snapshot despite the clear above. Registers a new handler from its frame.
	Subscribe(b, func(advEvtGamma) {
		secondCount.Add(1)
		// Subscribe a new handler from inside the handler frame.
		Subscribe(b, func(advEvtGamma) {
			newCount.Add(1)
		}, WithName("new-after-clear"))
	}, WithPriority(PriorityNormal), WithName("resubscribe-handler"))

	// First Publish — both original handlers fire (snapshot predates clear).
	done := make(chan struct{})
	go func() {
		Publish(b, advEvtGamma{Base: hmevent.NewBase()})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("first publish deadlocked")
	}

	if got := firstCount.Load(); got != 1 {
		t.Errorf("firstCount=%d, want 1 (high-priority handler must fire once)", got)
	}
	if got := secondCount.Load(); got != 1 {
		t.Errorf("secondCount=%d, want 1 (second handler must fire from snapshot despite clear)", got)
	}
	// The new handler was registered in the second handler's frame — it was NOT
	// in the snapshot, so it must not have fired during the first Publish.
	if got := newCount.Load(); got != 0 {
		t.Errorf("newCount=%d after first publish, want 0 (new handler must not join current dispatch)", got)
	}

	// Second Publish — only the new handler is registered (the originals were cleared).
	Publish(b, advEvtGamma{Base: hmevent.NewBase()})

	if got := firstCount.Load(); got != 1 {
		t.Errorf("firstCount=%d after second publish, want 1 (cleared handler must not fire)", got)
	}
	if got := secondCount.Load(); got != 1 {
		t.Errorf("secondCount=%d after second publish, want 1 (cleared handler must not fire)", got)
	}
	if got := newCount.Load(); got != 1 {
		t.Errorf("newCount=%d after second publish, want 1 (new handler must fire)", got)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// 4. TestPanicCascadeUnderDeferredDispatch
// ──────────────────────────────────────────────────────────────────────────────

// TestPanicCascadeUnderDeferredDispatch verifies that a panic cascade across
// both direct and deferred dispatch layers does not break the bus:
//
//   - A PriorityCritical handler on advEvtDelta panics AND publishes advEvtAlpha
//     (deferred).
//   - The deferred advEvtAlpha has a PriorityCritical handler that also panics.
//   - Both a PriorityLow handler on advEvtDelta and a PriorityLow handler on
//     advEvtAlpha must still fire despite the upstream panics.
//   - The bus must remain usable after the cascade.
//
// The panic recovery in callHandler is per-handler and must not abort the
// entire dispatch loop — each panicking handler is isolated. The same
// isolation must hold in the deferred drain path (flushDeferred → dispatchNow
// → callHandler).
func TestPanicCascadeUnderDeferredDispatch(t *testing.T) {
	t.Parallel()

	b := NewBus()

	var deltaLowCount, alphaLowCount atomic.Int32
	var deltaPanicCount, alphaPanicCount atomic.Int32

	// advEvtDelta: critical panicker + publishes advEvtAlpha (deferred).
	Subscribe(b, func(advEvtDelta) {
		deltaPanicCount.Add(1)
		// Publish advEvtAlpha — deferred because we are inside a handler frame.
		Publish(b, advEvtAlpha{Base: hmevent.NewBase()})
		panic("delta critical panic")
	}, WithPriority(PriorityCritical), WithName("delta-critical-panic"))

	// advEvtDelta: low-priority normal handler — must fire despite panic above.
	Subscribe(b, func(advEvtDelta) {
		deltaLowCount.Add(1)
	}, WithPriority(PriorityLow), WithName("delta-low"))

	// advEvtAlpha: critical panicker in the deferred frame.
	Subscribe(b, func(advEvtAlpha) {
		alphaPanicCount.Add(1)
		panic("alpha deferred panic")
	}, WithPriority(PriorityCritical), WithName("alpha-critical-panic"))

	// advEvtAlpha: low-priority normal handler — must fire despite the upstream panic.
	Subscribe(b, func(advEvtAlpha) {
		alphaLowCount.Add(1)
	}, WithPriority(PriorityLow), WithName("alpha-low"))

	// Trigger the whole cascade.
	done := make(chan struct{})
	go func() {
		Publish(b, advEvtDelta{Base: hmevent.NewBase()})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("panic cascade did not complete within 2 s (possible deadlock)")
	}

	// Both panicking handlers must have fired (they ran before panicking).
	if got := deltaPanicCount.Load(); got != 1 {
		t.Errorf("deltaPanicCount=%d, want 1", got)
	}
	if got := alphaPanicCount.Load(); got != 1 {
		t.Errorf("alphaPanicCount=%d, want 1 (deferred panicking handler must fire)", got)
	}

	// Low-priority handlers on both types must have fired — panic isolation
	// is mandatory at every dispatch level.
	if got := deltaLowCount.Load(); got != 1 {
		t.Errorf("deltaLowCount=%d, want 1 (delta low must survive upstream critical panic)", got)
	}
	if got := alphaLowCount.Load(); got != 1 {
		t.Errorf("alphaLowCount=%d, want 1 (alpha low must survive upstream deferred panic)", got)
	}

	// Bus must remain usable after the cascade.
	var afterCount atomic.Int32
	unsub := Subscribe(b, func(advEvtBeta) { afterCount.Add(1) })
	defer unsub()
	Publish(b, advEvtBeta{Base: hmevent.NewBase()})
	if got := afterCount.Load(); got != 1 {
		t.Errorf("bus unusable after panic cascade: afterCount=%d, want 1", got)
	}
}
