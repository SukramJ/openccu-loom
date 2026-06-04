// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package events

// bus_concurrency_test.go — gap-filling tests for Event-Bus ordering &
// concurrency tests.
//
// Cases in this file cover invariants NOT already exercised by the five
// existing test files (bus_test.go, bus_deep_test.go, bus_race_test.go,
// bus_metrics_test.go, group_batch_test.go).
//
// Gaps covered here:
//
// 1. TestPanicInHandlerIsolation — panic in one handler must not kill others.
// 2. TestMultiCCUBusIsolation — two independent buses never cross-talk.
// 3. TestEventStatsCountSurvivesUnsubscription — per
// 4. TestPriorityNormalIsZeroValue — default (no WithPriority) == PriorityNormal(0).
// 5. TestBatchConcurrentAddFlush — concurrent Add + Flush on the same Batch.
// 6. TestGoroutineLeakAfterFullTeardown — no goroutines leak after Close+unsub.

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// ---------- local test-only event types ----------

type concEvtPanic struct{ hmevent.Base }

func (concEvtPanic) Type() hmevent.EventType { return "conc.evt.panic" }

type concEvtIsolA struct{ hmevent.Base }

func (concEvtIsolA) Type() hmevent.EventType { return "conc.evt.isol_a" }

type concEvtIsolB struct{ hmevent.Base }

func (concEvtIsolB) Type() hmevent.EventType { return "conc.evt.isol_b" }

type concEvtStats struct{ hmevent.Base }

func (concEvtStats) Type() hmevent.EventType { return "conc.evt.stats" }

type concEvtPrio struct{ hmevent.Base }

func (concEvtPrio) Type() hmevent.EventType { return "conc.evt.prio" }

type concEvtBatch struct{ hmevent.Base }

func (concEvtBatch) Type() hmevent.EventType { return "conc.evt.batch" }

type concEvtLeak struct{ hmevent.Base }

func (concEvtLeak) Type() hmevent.EventType { return "conc.evt.leak" }

// ---------- 1. TestPanicInHandlerIsolation ----------

// TestPanicInHandlerIsolation verifies that a handler which panics does NOT
// prevent subsequent handlers (registered for the same event type) from
// receiving the event.
//
// Panic-recovery is mandatory — bad handlers must not break the bus.
// Production code in callHandler() wraps every handler invocation in a
// deferred recover(), logs the panic via slog, and increments TotalErrors on
// the panicking handler's stat. Remaining handlers always fire.
func TestPanicInHandlerIsolation(t *testing.T) {
	t.Parallel()

	b := NewBus()

	var workingCount atomic.Int32

	// panicking handler — registered first so it runs before the working one
	// (PriorityHigh > PriorityNormal).
	Subscribe(b, func(concEvtPanic) {
		panic("deliberate test panic")
	}, WithPriority(PriorityHigh), WithName("panicker"))

	Subscribe(b, func(concEvtPanic) {
		workingCount.Add(1)
	}, WithPriority(PriorityNormal), WithName("worker"))

	// Publish must NOT propagate the panic; the bus recovers it internally.
	Publish(b, concEvtPanic{Base: hmevent.NewBase()})

	// Recovery is mandatory: working handler must have been called.
	if got := workingCount.Load(); got != 1 {
		t.Errorf("workingCount=%d, want 1 (panic recovery mandatory — bad handler must not kill bus)", got)
	}
}

// ---------- 2. TestMultiCCUBusIsolation ----------

// TestMultiCCUBusIsolation creates two independent Bus instances and verifies
// that events published on bus A never reach subscribers on bus B — and vice
// versa. This is the core multi-CCU correctness requirement: each Unit
// has its own Bus; they must be hermetically isolated.
//
// openccu-loom ADR-0002 makes multi-CCU a first- class constraint; cross-talk
// between buses would be a critical bug.
func TestMultiCCUBusIsolation(t *testing.T) {
	t.Parallel()

	busA := NewBus()
	busB := NewBus()

	var countA, countB atomic.Int32

	Subscribe(busA, func(concEvtIsolA) { countA.Add(1) })
	Subscribe(busB, func(concEvtIsolA) { countB.Add(1) })

	// Publish only on busA.
	Publish(busA, concEvtIsolA{Base: hmevent.NewBase()})

	if got := countA.Load(); got != 1 {
		t.Errorf("busA handler: countA=%d, want 1", got)
	}
	if got := countB.Load(); got != 0 {
		t.Errorf("busB cross-talk: countB=%d, want 0 (buses must be isolated)", got)
	}

	// Publish only on busB.
	countA.Store(0)
	Publish(busB, concEvtIsolA{Base: hmevent.NewBase()})
	if got := countA.Load(); got != 0 {
		t.Errorf("busA cross-talk after busB publish: countA=%d, want 0", got)
	}
	if got := countB.Load(); got != 1 {
		t.Errorf("busB handler: countB=%d, want 1", got)
	}
}

// TestMultiCCUBusIsolationDifferentTypes verifies isolation across different
// event types between two buses. Subscribing to type X on busA must not
// receive type X events from busB even when both buses use identical
// event types.
func TestMultiCCUBusIsolationDifferentTypes(t *testing.T) {
	t.Parallel()

	busA := NewBus()
	busB := NewBus()

	var aCntA, aCntB, bCntA, bCntB atomic.Int32

	Subscribe(busA, func(concEvtIsolA) { aCntA.Add(1) })
	Subscribe(busA, func(concEvtIsolB) { bCntA.Add(1) })
	Subscribe(busB, func(concEvtIsolA) { aCntB.Add(1) })
	Subscribe(busB, func(concEvtIsolB) { bCntB.Add(1) })

	Publish(busA, concEvtIsolA{Base: hmevent.NewBase()})
	Publish(busA, concEvtIsolB{Base: hmevent.NewBase()})
	Publish(busB, concEvtIsolA{Base: hmevent.NewBase()})
	Publish(busB, concEvtIsolB{Base: hmevent.NewBase()})

	// Each bus must have received exactly its own events.
	if got := aCntA.Load(); got != 1 {
		t.Errorf("busA.concEvtIsolA=%d, want 1", got)
	}
	if got := bCntA.Load(); got != 1 {
		t.Errorf("busA.concEvtIsolB=%d, want 1", got)
	}
	if got := aCntB.Load(); got != 1 {
		t.Errorf("busB.concEvtIsolA=%d, want 1", got)
	}
	if got := bCntB.Load(); got != 1 {
		t.Errorf("busB.concEvtIsolB=%d, want 1", got)
	}
}

// ---------- 3. TestEventStatsCountSurvivesUnsubscription ----------

// TestEventStatsCountSurvivesUnsubscription verifies that EventStats counters
// are NOT reset when subscribers unsubscribe. The counter records every
// publish, regardless of how many handlers were active at the time.
//
// The Go bus mirrors this contract via Bus.EventStats().
func TestEventStatsCountSurvivesUnsubscription(t *testing.T) {
	t.Parallel()

	b := NewBus()

	// Subscribe, publish 3 times, then unsubscribe.
	unsub := Subscribe(b, func(concEvtStats) {})
	for range 3 {
		Publish(b, concEvtStats{Base: hmevent.NewBase()})
	}
	if before := b.EventStats()[string(concEvtStats{}.Type())]; before != 3 {
		t.Fatalf("before unsub: EventStats=%d, want 3", before)
	}

	unsub()

	// Publish 2 more with no subscribers.
	for range 2 {
		Publish(b, concEvtStats{Base: hmevent.NewBase()})
	}

	// Total must be 5 — counter survives unsubscription and no-subscriber publishes.
	if got := b.EventStats()[string(concEvtStats{}.Type())]; got != 5 {
		t.Errorf("after unsub+2publishes: EventStats=%d, want 5 (counter must survive unsub)", got)
	}
}

// ---------- 4. TestPriorityNormalIsZeroValue ----------

// TestPriorityNormalIsZeroValue verifies that:
//
//	a) PriorityNormal == 0 (the Go zero value) — so a handler registered
//	 without WithPriority gets the same priority as one with
//	 WithPriority(PriorityNormal).
//	b) Dispatching these two handlers (one implicit, one explicit) preserves
//	 the FIFO registration order, proving they are in the same bucket.
//
// This pins the CLAUDE.md invariant: "CommandPriority.Critical = 0 — never
// use `if priority != 0` as 'set'". The same reasoning applies to Normal=0:
// zero is a valid, meaningful priority, not a sentinel for "unset".
func TestPriorityNormalIsZeroValue(t *testing.T) {
	t.Parallel()

	// Compile-time assertion: PriorityNormal must equal 0.
	const _ Priority = 0 // zero is a valid Priority; this line documents intent
	if PriorityNormal != 0 {
		t.Fatalf("PriorityNormal=%d, want 0 (zero-value invariant from CLAUDE.md)", PriorityNormal)
	}

	b := NewBus()
	var order []string
	var mu sync.Mutex

	record := func(label string) {
		mu.Lock()
		order = append(order, label)
		mu.Unlock()
	}

	// Register "implicit" first (no WithPriority → defaults to PriorityNormal).
	Subscribe(b, func(concEvtPrio) { record("implicit") })
	// Register "explicit" second (WithPriority(PriorityNormal) == WithPriority(0)).
	Subscribe(b, func(concEvtPrio) { record("explicit") }, WithPriority(PriorityNormal))

	Publish(b, concEvtPrio{Base: hmevent.NewBase()})

	mu.Lock()
	got := append([]string(nil), order...)
	mu.Unlock()

	// Both must fire in FIFO registration order (same priority bucket).
	want := []string{"implicit", "explicit"}
	for i, g := range got {
		if i >= len(want) || g != want[i] {
			t.Fatalf("FIFO order=%v, want %v (PriorityNormal==0 must share bucket)", got, want)
		}
	}
	if len(got) != 2 {
		t.Fatalf("handler count=%d, want 2", len(got))
	}
}

// ---------- 5. TestBatchConcurrentAddFlush ----------

// TestBatchConcurrentAddFlush stresses a single Batch from multiple
// goroutines that Add events while a separate goroutine calls Flush
// repeatedly.
//
// Invariant: no events are lost (every Add either lands in a Flush or the
// final Flush), no race detected (run under -race), and the final sum equals
// exactly the number of Add calls.
//
// In Go, Batch.mu must guard both Add and Flush correctly.
func TestBatchConcurrentAddFlush(t *testing.T) {
	t.Parallel()

	b := NewBus()
	batch := NewBatch(b)

	var total atomic.Int64
	Subscribe(b, func(concEvtBatch) { total.Add(1) })

	const (
		adders     = 10
		addsEach   = 50
		flushLoops = 5
	)

	var wgAdd sync.WaitGroup
	wgAdd.Add(adders)
	for range adders {
		go func() {
			defer wgAdd.Done()
			for range addsEach {
				Add(batch, concEvtBatch{Base: hmevent.NewBase()})
			}
		}()
	}

	// Flush goroutine: periodically drains without racing.
	var wgFlush sync.WaitGroup
	wgFlush.Go(func() {
		for range flushLoops {
			batch.Flush()
			// Yield so adders can make progress.
			runtime.Gosched()
		}
	})

	wgAdd.Wait()
	wgFlush.Wait()
	// Final drain: pick up any events added after the last periodic flush.
	batch.Flush()

	want := int64(adders * addsEach)
	if got := total.Load(); got != want {
		t.Errorf("total dispatched=%d, want %d (no events must be lost)", got, want)
	}
}

// ---------- 6. TestGoroutineLeakAfterFullTeardown ----------

// TestGoroutineLeakAfterFullTeardown verifies that creating a Bus,
// subscribing several handlers, publishing events, then unsubscribing all
// does not leak goroutines. The Bus has no internal goroutines by design —
// this test pins that invariant.
//
// Any goroutine started by the Bus itself (not by test code) would appear as
// a leak here.
func TestGoroutineLeakAfterFullTeardown(t *testing.T) {
	// Not parallel: we measure absolute goroutine counts.
	// Let the runtime settle before taking the baseline.
	runtime.GC()
	time.Sleep(10 * time.Millisecond)
	before := runtime.NumGoroutine()

	b := NewBus()

	unsubs := make([]func(), 0, 20)
	for range 20 {
		unsubs = append(unsubs, Subscribe(b, func(concEvtLeak) {}))
	}

	for range 50 {
		Publish(b, concEvtLeak{Base: hmevent.NewBase()})
	}

	for _, u := range unsubs {
		u()
	}

	// Let any runtime bookkeeping settle.
	runtime.GC()
	time.Sleep(10 * time.Millisecond)
	after := runtime.NumGoroutine()

	// Allow ±2 for runtime background goroutines that may vary.
	if after > before+2 {
		t.Errorf("goroutine leak: before=%d, after=%d (leaked %d goroutines)", before, after, after-before)
	}
}

// ---------- 7. TestBatchFlushInsideHandlerSeesDeferred ----------

// TestBatchFlushInsideHandlerSeesDeferred verifies that when a Batch.Flush is
// called from inside a handler (re-entrant context), the batch's Publish
// calls land in the deferred queue and are processed after the current
// dispatch completes — exactly the same as a direct re-entrant Publish.
func TestBatchFlushInsideHandlerSeesDeferred(t *testing.T) {
	t.Parallel()

	b := NewBus()
	batch := NewBatch(b)

	// Pre-load the batch.
	Add(batch, concEvtIsolB{Base: hmevent.NewBase()})

	var outerDone atomic.Bool
	var innerFiredAfterOuter atomic.Bool

	Subscribe(b, func(concEvtIsolA) {
		// Flush the batch from inside a handler — must be deferred.
		batch.Flush()
		outerDone.Store(true)
	})

	Subscribe(b, func(concEvtIsolB) {
		innerFiredAfterOuter.Store(outerDone.Load())
	})

	Publish(b, concEvtIsolA{Base: hmevent.NewBase()})

	if !outerDone.Load() {
		t.Fatal("outer handler never ran")
	}
	if !innerFiredAfterOuter.Load() {
		t.Fatal("batch event fired before outer handler returned (deferred dispatch violated)")
	}
}
