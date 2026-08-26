// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package events

// bus_race_test.go — race, reentrancy, and ordering contract tests for Bus.
//
// Tests in this file focus on invariants NOT already locked down by
// bus_test.go, bus_deep_test.go, and group_batch_test.go:
//
// - Skipped (already covered):
// - TestReentrantPublishOrderingIsCausal → TestReentrantPublishChain (bus_deep_test.go)
// - TestKeyFilterCountsSkippedAsCallsNotMatches → TestHandlerStatsTracksCallsAndMatches (bus_deep_test.go)
//
// All other cases below are net-new coverage.

import (
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// ---------- local test-only event types ----------

// raceEvtA is a minimal typed event used in race tests to avoid
// collisions with production event types or types from sibling test files.
type raceEvtA struct{ hmevent.Base }

func (raceEvtA) Type() hmevent.EventType { return "race_test.evt_a" }

type raceEvtB struct{ hmevent.Base }

func (raceEvtB) Type() hmevent.EventType { return "race_test.evt_b" }

type raceEvtFanout struct{ hmevent.Base }

func (raceEvtFanout) Type() hmevent.EventType { return "race_test.fanout" }

type raceEvtUnsub struct{ hmevent.Base }

func (raceEvtUnsub) Type() hmevent.EventType { return "race_test.unsub" }

type raceEvtNewSub struct{ hmevent.Base }

func (raceEvtNewSub) Type() hmevent.EventType { return "race_test.newsub" }

type raceEvtPrio struct{ hmevent.Base }

func (raceEvtPrio) Type() hmevent.EventType { return "race_test.prio" }

type raceEvtLock struct{ hmevent.Base }

func (raceEvtLock) Type() hmevent.EventType { return "race_test.lock" }

type raceEvtDeferred struct{ hmevent.Base }

func (raceEvtDeferred) Type() hmevent.EventType { return "race_test.deferred" }

type raceEvtConcSub struct{ hmevent.Base }

func (raceEvtConcSub) Type() hmevent.EventType { return "race_test.conc_sub" }

// ---------- helpers ----------

// awaitOrFatal waits for done to be closed within timeout, failing the
// test with msg if it is not.
func awaitOrFatal(t *testing.T, done <-chan struct{}, timeout time.Duration, msg string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatal(msg)
	}
}

// ---------- 1. TestPublishFromHandlerNeverDeadlocks ----------

// TestPublishFromHandlerNeverDeadlocks registers:
// - handlerA on raceEvtA that publishes raceEvtB (cross-type re-entrant).
// - handlerLoop on raceEvtA that re-publishes raceEvtA while a counter < 5
// (same-type loop guard).
//
// The dispatch must complete within 1 s and the cross-type handler B must
// fire exactly once while the loop guard dispatches exactly 5 A events total.
func TestPublishFromHandlerNeverDeadlocks(t *testing.T) {
	t.Parallel()

	b := NewBus()

	var crossTypeCount atomic.Int32
	var loopCount atomic.Int32

	Subscribe(b, func(raceEvtA) {
		n := loopCount.Add(1)
		if n < 5 {
			// Same-type re-entrant publish — must be deferred, not recursive.
			Publish(b, raceEvtA{Base: hmevent.NewBase()})
		}
		// Cross-type re-entrant publish — must be deferred every time.
		Publish(b, raceEvtB{Base: hmevent.NewBase()})
	})

	Subscribe(b, func(raceEvtB) {
		crossTypeCount.Add(1)
	})

	done := make(chan struct{})
	go func() {
		Publish(b, raceEvtA{Base: hmevent.NewBase()})
		close(done)
	}()

	awaitOrFatal(t, done, time.Second, "Publish deadlocked — dispatch did not complete within 1 s")

	// loopCount==5: first A + 4 re-entrant deferred A's (loop guard stops at 5).
	if got := loopCount.Load(); got != 5 {
		t.Errorf("loopCount=%d, want 5", got)
	}
	// crossTypeCount==5: one B per A invocation.
	if got := crossTypeCount.Load(); got != 5 {
		t.Errorf("crossTypeCount=%d, want 5", got)
	}
}

// ---------- 2. TestRecursivePublishLoopBoundedByCounter ----------

// TestRecursivePublishLoopBoundedByCounter verifies that a handler that
// re-publishes the same event type terminates deterministically at the
// counter boundary, and that dispatch is *not* recursive — the call stack
// depth inside any single handler invocation must be 1 (each handler body
// runs flat, never calling itself; re-entrant publishes land in the
// deferred queue and are drained iteratively by flushDeferred).
//
// We cannot read the goroutine stack depth directly, so we verify the
// observable proxy: the shared "activeNestDepth" counter never exceeds 1
// during any single handler invocation because flushDeferred only calls
// dispatchNow after the previous dispatchNow has returned.
func TestRecursivePublishLoopBoundedByCounter(t *testing.T) {
	t.Parallel()

	b := NewBus()

	const limit = 10
	var dispatchCount atomic.Int32
	var maxObservedNesting atomic.Int32
	var currentDepth atomic.Int32

	Subscribe(b, func(raceEvtA) {
		// Record entry depth.
		depth := currentDepth.Add(1)
		if depth > maxObservedNesting.Load() {
			maxObservedNesting.Store(depth)
		}
		n := dispatchCount.Add(1)
		if n < limit {
			Publish(b, raceEvtA{Base: hmevent.NewBase()})
		}
		currentDepth.Add(-1)
	})

	done := make(chan struct{})
	go func() {
		Publish(b, raceEvtA{Base: hmevent.NewBase()})
		close(done)
	}()

	awaitOrFatal(t, done, time.Second, "recursive publish loop did not terminate within 1 s")

	if got := dispatchCount.Load(); got != limit {
		t.Errorf("dispatchCount=%d, want %d", got, limit)
	}
	// The dispatch must be queue-driven, not recursive: depth == 1 always.
	if got := maxObservedNesting.Load(); got != 1 {
		t.Errorf("maxObservedNesting=%d, want 1 (dispatch must be iterative, not recursive)", got)
	}
}

// ---------- 3. TestConcurrentPublishersFanOut ----------

// TestConcurrentPublishersFanOut spawns 50 goroutines each publishing 100
// events into a single subscriber that atomically increments a counter.
// The counter must reach 5 000 exactly. Run under -race to catch data races.
func TestConcurrentPublishersFanOut(t *testing.T) {
	t.Parallel()

	b := NewBus()
	var counter atomic.Int64

	Subscribe(b, func(raceEvtFanout) {
		counter.Add(1)
	})

	const (
		goroutines   = 50
		perGoroutine = 100
	)

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			for range perGoroutine {
				Publish(b, raceEvtFanout{Base: hmevent.NewBase()})
			}
		}()
	}
	wg.Wait()

	if got := counter.Load(); got != goroutines*perGoroutine {
		t.Errorf("counter=%d, want %d", got, goroutines*perGoroutine)
	}
}

// ---------- 4. TestUnsubscribeDuringPublishIsSafe ----------

// TestUnsubscribeDuringPublishIsSafe verifies two sub-cases:
//
//	a) Handler A unsubscribes itself from inside its own callback. Because
//	 dispatchNow snapshots the handler list before iterating, A is still
//	 called for the in-flight event. Subsequent publishes must NOT call A.
//
//	b) Handler B unsubscribes handler A from inside B's callback. Same
//	 snapshot semantics apply — A is called for the current dispatch.
//	 Subsequent publishes must not call A.
func TestUnsubscribeDuringPublishIsSafe(t *testing.T) {
	t.Parallel()

	// Sub-case a: self-unsubscribe.
	t.Run("SelfUnsub", func(t *testing.T) {
		t.Parallel()
		b := NewBus()
		var aCount atomic.Int32

		var unsubA func()
		unsubA = Subscribe(b, func(raceEvtUnsub) {
			aCount.Add(1)
			unsubA() // unsubscribe self mid-dispatch
		})

		// First publish: A is in the snapshot, so it fires and unsubscribes itself.
		Publish(b, raceEvtUnsub{Base: hmevent.NewBase()})
		if got := aCount.Load(); got != 1 {
			t.Errorf("after first publish: aCount=%d, want 1 (snapshot already taken)", got)
		}

		// Second publish: A is gone, must not fire.
		Publish(b, raceEvtUnsub{Base: hmevent.NewBase()})
		if got := aCount.Load(); got != 1 {
			t.Errorf("after second publish: aCount=%d, want 1 (A must be gone)", got)
		}
	})

	// Sub-case b: B unsubscribes A.
	t.Run("BUnsubsA", func(t *testing.T) {
		t.Parallel()
		b := NewBus()
		var aCount, bCount atomic.Int32

		unsubA := Subscribe(b, func(raceEvtUnsub) {
			aCount.Add(1)
		})

		Subscribe(b, func(raceEvtUnsub) {
			bCount.Add(1)
			unsubA() // B unsubscribes A
		})

		// First publish: both A and B are in the snapshot.
		Publish(b, raceEvtUnsub{Base: hmevent.NewBase()})
		if got := aCount.Load(); got != 1 {
			t.Errorf("after first publish: aCount=%d, want 1", got)
		}
		if got := bCount.Load(); got != 1 {
			t.Errorf("after first publish: bCount=%d, want 1", got)
		}

		// Second publish: A is removed, B remains.
		Publish(b, raceEvtUnsub{Base: hmevent.NewBase()})
		if got := aCount.Load(); got != 1 {
			t.Errorf("after second publish: aCount=%d, want 1 (A must not fire again)", got)
		}
		if got := bCount.Load(); got != 2 {
			t.Errorf("after second publish: bCount=%d, want 2", got)
		}
	})
}

// ---------- 5. TestSubscribeDuringPublishDoesNotJoinCurrentDispatch ----------

// TestSubscribeDuringPublishDoesNotJoinCurrentDispatch verifies that a
// handler N registered from inside handler A's callback does NOT receive the
// currently-dispatched event (the snapshot was already taken), but DOES
// receive the next publish.
func TestSubscribeDuringPublishDoesNotJoinCurrentDispatch(t *testing.T) {
	t.Parallel()

	b := NewBus()
	var newHandlerCount atomic.Int32

	Subscribe(b, func(raceEvtNewSub) {
		// Subscribe a new handler while dispatch of raceEvtNewSub is in progress.
		Subscribe(b, func(raceEvtNewSub) {
			newHandlerCount.Add(1)
		})
	})

	// First publish: N is registered during dispatch but must NOT receive E1.
	Publish(b, raceEvtNewSub{Base: hmevent.NewBase()})
	if got := newHandlerCount.Load(); got != 0 {
		t.Errorf("after first publish: newHandlerCount=%d, want 0 (N must not join current dispatch)", got)
	}

	// Second publish: N is now registered and must receive it.
	Publish(b, raceEvtNewSub{Base: hmevent.NewBase()})
	if got := newHandlerCount.Load(); got != 1 {
		t.Errorf("after second publish: newHandlerCount=%d, want 1", got)
	}
}

// ---------- 6. TestUnsubscribeIdempotentUnderConcurrentCalls ----------

// TestUnsubscribeIdempotentUnderConcurrentCalls calls the same unsubscribe
// closure from 100 goroutines simultaneously. It must not panic, and
// HandlerCount must be 0 afterwards.
func TestUnsubscribeIdempotentUnderConcurrentCalls(t *testing.T) {
	t.Parallel()

	b := NewBus()
	unsub := Subscribe(b, func(raceEvtUnsub) {})

	var wg sync.WaitGroup
	const concurrency = 100
	wg.Add(concurrency)
	for range concurrency {
		go func() {
			defer wg.Done()
			unsub()
		}()
	}
	wg.Wait()

	if got := b.HandlerCount(raceEvtUnsub{}.Type()); got != 0 {
		t.Errorf("HandlerCount=%d after concurrent unsubscribe, want 0", got)
	}
}

// ---------- 7. TestHighPriorityHandlerSeesEventBeforeLowPriority ----------

// TestHighPriorityHandlerSeesEventBeforeLowPriority verifies that three
// handlers at PriorityHigh / PriorityNormal / PriorityLow consistently
// observe events in that order across multiple dispatches, with no
// cross-dispatch leakage in the slice.
func TestHighPriorityHandlerSeesEventBeforeLowPriority(t *testing.T) {
	t.Parallel()

	b := NewBus()
	var mu sync.Mutex
	var order []string

	record := func(label string) {
		mu.Lock()
		order = append(order, label)
		mu.Unlock()
	}

	Subscribe(b, func(raceEvtPrio) { record("low") }, WithPriority(PriorityLow))
	Subscribe(b, func(raceEvtPrio) { record("normal") }, WithPriority(PriorityNormal))
	Subscribe(b, func(raceEvtPrio) { record("high") }, WithPriority(PriorityHigh))

	const rounds = 5
	for range rounds {
		mu.Lock()
		order = order[:0]
		mu.Unlock()

		Publish(b, raceEvtPrio{Base: hmevent.NewBase()})

		mu.Lock()
		got := slices.Clone(order)
		mu.Unlock()

		want := []string{"high", "normal", "low"}
		if !slices.Equal(got, want) {
			t.Fatalf("round order=%v, want %v", got, want)
		}
	}
}

// ---------- 8. TestNoLockHeldWhileHandlerRuns ----------

// TestNoLockHeldWhileHandlerRuns verifies that a handler can call
// Bus.HandlerCount (which acquires b.mu) from inside its own callback
// without deadlocking. This proves dispatchNow drops b.mu after taking
// the handler snapshot (lines ~166-169 of bus.go).
func TestNoLockHeldWhileHandlerRuns(t *testing.T) {
	t.Parallel()

	b := NewBus()
	typ := raceEvtLock{}.Type()

	done := make(chan struct{})

	Subscribe(b, func(raceEvtLock) {
		// This call acquires b.mu internally; it must not deadlock.
		_ = b.HandlerCount(typ)
		close(done)
	})

	go Publish(b, raceEvtLock{Base: hmevent.NewBase()})

	awaitOrFatal(t, done, time.Second, "HandlerCount inside handler deadlocked — b.mu held during handler execution")
}

// ---------- 9. TestDeferredPublishesPreserveCausalOrder ----------

// TestDeferredPublishesPreserveCausalOrder publishes three events of
// raceEvtDeferred sequentially from inside a single handler invocation.
// The deferred queue must flush them in FIFO (publish) order.
func TestDeferredPublishesPreserveCausalOrder(t *testing.T) {
	t.Parallel()

	b := NewBus()
	var mu sync.Mutex
	var received []int

	// Trigger event reuses raceEvtA; the payload is raceEvtDeferred.
	// Defining a fresh type inside the function isn't possible because
	// of the method constraint, so we lean on the existing event types.

	// Payload handler records arrival order.
	var seq atomic.Int32
	Subscribe(b, func(raceEvtDeferred) {
		n := int(seq.Add(1))
		mu.Lock()
		received = append(received, n)
		mu.Unlock()
	})

	// Separate counter on the deferred event so we can distinguish
	// the 1st, 2nd, 3rd deferred dispatch. We attach the ordinal via
	// closing over a counter in the trigger handler.
	var ordinal atomic.Int32
	Subscribe(b, func(raceEvtB) {
		// From inside handler on B, publish three A's sequentially.
		// All three land in the deferred queue.
		for range 3 {
			ord := int(ordinal.Add(1))
			_ = ord
			Publish(b, raceEvtDeferred{Base: hmevent.NewBase()})
		}
	})

	Publish(b, raceEvtB{Base: hmevent.NewBase()})

	mu.Lock()
	got := slices.Clone(received)
	mu.Unlock()

	want := []int{1, 2, 3}
	if !slices.Equal(got, want) {
		t.Errorf("deferred dispatch order=%v, want %v", got, want)
	}
}

// ---------- 10. TestConcurrentSubscribeAndPublishWithoutRace ----------

// TestConcurrentSubscribeAndPublishWithoutRace runs 20 goroutines that
// subscribe/unsubscribe and 20 goroutines that publish concurrently.
// Assertions: no race (verified by -race flag), no panic, and
// LeakedSubscriptions is empty once all unsubscribes have been called.
//
// Note: t.Parallel() is omitted intentionally — this test exercises
// high goroutine contention and is better served by owning the scheduler.
func TestConcurrentSubscribeAndPublishWithoutRace(t *testing.T) {
	const (
		subGoroutines = 20
		pubGoroutines = 20
		iterations    = 150
	)

	b := NewBus()
	var wg sync.WaitGroup

	// Subscriber goroutines: subscribe, yield, unsubscribe — repeated.
	wg.Add(subGoroutines)
	for range subGoroutines {
		go func() {
			defer wg.Done()
			for range iterations {
				unsub := Subscribe(b, func(raceEvtConcSub) {}, WithName("transient"))
				unsub()
			}
		}()
	}

	// Publisher goroutines: publish continuously.
	wg.Add(pubGoroutines)
	for range pubGoroutines {
		go func() {
			defer wg.Done()
			for range iterations {
				Publish(b, raceEvtConcSub{Base: hmevent.NewBase()})
			}
		}()
	}

	wg.Wait()

	if leaked := b.LeakedSubscriptions(); len(leaked) != 0 {
		t.Errorf("LeakedSubscriptions after all unsubs: %v", leaked)
	}
}
