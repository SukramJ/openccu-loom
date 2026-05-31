// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package events

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// ──────────────────────────────────────────────────────────────────────────────
// Local test-only event types
// ──────────────────────────────────────────────────────────────────────────────

type w2EvtPanic struct{ hmevent.Base }

func (w2EvtPanic) Type() hmevent.EventType { return "w2.evt.panic" }

type w2EvtClear struct{ hmevent.Base }

func (w2EvtClear) Type() hmevent.EventType { return "w2.evt.clear" }

type w2EvtClearB struct{ hmevent.Base }

func (w2EvtClearB) Type() hmevent.EventType { return "w2.evt.clear_b" }

type w2EvtDuration struct{ hmevent.Base }

func (w2EvtDuration) Type() hmevent.EventType { return "w2.evt.duration" }

type w2EvtError struct{ hmevent.Base }

func (w2EvtError) Type() hmevent.EventType { return "w2.evt.error" }

// w3Evt is a local test event whose EventKey() returns the key field,
// used for key-filter and stats tests.
type w3Evt struct {
	hmevent.Base
	key string
}

func (e w3Evt) Type() hmevent.EventType { return "w3.test" }
func (e w3Evt) EventKey() string        { return e.key }

// ──────────────────────────────────────────────────────────────────────────────
// Priority: Critical ordering
// ──────────────────────────────────────────────────────────────────────────────

// TestPriorityCritical verifies PriorityCritical is higher than PriorityHigh.
func TestPriorityCritical(t *testing.T) {
	if PriorityCritical <= PriorityHigh {
		t.Fatalf("PriorityCritical=%d must be > PriorityHigh=%d", PriorityCritical, PriorityHigh)
	}
}

// TestPriorityCriticalOrdering verifies that Critical handlers run before
// High handlers.
func TestPriorityCriticalOrdering(t *testing.T) {
	b := NewBus()
	var order []string
	Subscribe(b, func(e hmevent.CentralStateChangedEvent) { order = append(order, "high") }, WithPriority(PriorityHigh))
	Subscribe(b, func(e hmevent.CentralStateChangedEvent) { order = append(order, "critical") }, WithPriority(PriorityCritical))
	Subscribe(b, func(e hmevent.CentralStateChangedEvent) { order = append(order, "normal") })

	Publish(b, hmevent.CentralStateChangedEvent{CentralName: "main"})

	if len(order) != 3 || order[0] != "critical" || order[1] != "high" || order[2] != "normal" {
		t.Fatalf("unexpected order: %v", order)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// External subscriptions
// ──────────────────────────────────────────────────────────────────────────────

// TestClearExternalSubscriptions verifies only external subs are cleared;
// internal ones survive.
func TestClearExternalSubscriptions(t *testing.T) {
	b := NewBus()

	internalCalls := 0
	externalCalls := 0

	Subscribe(b, func(_ hmevent.CentralStateChangedEvent) { internalCalls++ })
	Subscribe(b, func(_ hmevent.CentralStateChangedEvent) { externalCalls++ }, WithExternal())
	Subscribe(b, func(_ hmevent.ConnectionLostEvent) { externalCalls++ }, WithExternal())

	// Before clear — all fire.
	Publish(b, hmevent.CentralStateChangedEvent{})
	if internalCalls != 1 || externalCalls != 1 {
		t.Fatalf("pre-clear: internal=%d external=%d", internalCalls, externalCalls)
	}

	// Clear only CentralStateChanged external subs.
	removed := b.ClearExternalSubscriptions(hmevent.EventTypeCentralStateChanged)
	if removed != 1 {
		t.Fatalf("ClearExternalSubscriptions returned %d, want 1", removed)
	}

	Publish(b, hmevent.CentralStateChangedEvent{})
	if internalCalls != 2 {
		t.Fatalf("internal sub should still fire: got %d calls", internalCalls)
	}
	if externalCalls != 1 {
		t.Fatalf("cleared external sub fired again: %d", externalCalls)
	}

	// ConnectionLost external sub is still registered.
	Publish(b, hmevent.ConnectionLostEvent{})
	if externalCalls != 2 {
		t.Fatalf("other external sub should still fire: %d", externalCalls)
	}
}

// TestClearExternalSubscriptionsAll verifies that passing no types clears
// all external handlers across all event types.
func TestClearExternalSubscriptionsAll(t *testing.T) {
	b := NewBus()

	ext1, ext2 := 0, 0
	internal := 0
	Subscribe(b, func(_ hmevent.CentralStateChangedEvent) { internal++ })
	Subscribe(b, func(_ hmevent.CentralStateChangedEvent) { ext1++ }, WithExternal())
	Subscribe(b, func(_ hmevent.ConnectionLostEvent) { ext2++ }, WithExternal())

	removed := b.ClearExternalSubscriptions()
	if removed != 2 {
		t.Fatalf("expected 2 removed, got %d", removed)
	}
	Publish(b, hmevent.CentralStateChangedEvent{})
	Publish(b, hmevent.ConnectionLostEvent{})
	if ext1 != 0 || ext2 != 0 {
		t.Fatalf("external subs fired after ClearAll: ext1=%d ext2=%d", ext1, ext2)
	}
	if internal != 1 {
		t.Fatalf("internal sub should still fire: got %d", internal)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// ClearSubscriptionsByKey
// ──────────────────────────────────────────────────────────────────────────────

func TestBusClearSubscriptionsByKey(t *testing.T) {
	b := NewBus()

	var calledA, calledB, calledNoKey int
	Subscribe(b, func(e w3Evt) { calledA++ }, WithKey("a"))
	Subscribe(b, func(e w3Evt) { calledB++ }, WithKey("b"))
	Subscribe(b, func(e w3Evt) { calledNoKey++ }) // no key filter

	Publish(b, w3Evt{Base: hmevent.NewBase(), key: "a"})
	if calledA != 1 || calledB != 0 || calledNoKey != 1 {
		t.Fatalf("before clear: calledA=%d calledB=%d calledNoKey=%d", calledA, calledB, calledNoKey)
	}

	b.ClearSubscriptionsByKey("a")

	calledA, calledB, calledNoKey = 0, 0, 0
	Publish(b, w3Evt{Base: hmevent.NewBase(), key: "a"})
	if calledA != 0 {
		t.Fatalf("after ClearSubscriptionsByKey(a): calledA=%d, want 0", calledA)
	}
	if calledNoKey != 1 {
		t.Fatalf("after ClearSubscriptionsByKey(a): calledNoKey=%d, want 1 (no-key sub survives)", calledNoKey)
	}
	if calledB != 0 {
		// key="b" sub: event key is "a", so calledB stays 0.
		t.Fatalf("after ClearSubscriptionsByKey(a): calledB=%d, want 0", calledB)
	}
}

func TestBusClearSubscriptionsByKeyIdempotent(t *testing.T) {
	b := NewBus()
	b.ClearSubscriptionsByKey("nonexistent") // must not panic
}

// ──────────────────────────────────────────────────────────────────────────────
// ResetEventStats
// ──────────────────────────────────────────────────────────────────────────────

func TestBusResetEventStats(t *testing.T) {
	b := NewBus()
	Subscribe(b, func(e w3Evt) {})
	Publish(b, w3Evt{Base: hmevent.NewBase()})
	Publish(b, w3Evt{Base: hmevent.NewBase()})

	stats := b.EventStats()
	if stats["w3.test"] != 2 {
		t.Fatalf("EventStats before reset: %v, want w3.test=2", stats)
	}

	b.ResetEventStats()

	after := b.EventStats()
	if after["w3.test"] != 0 {
		t.Fatalf("EventStats after ResetEventStats: %v, want w3.test=0", after)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Panic recovery: additional test cases
// ──────────────────────────────────────────────────────────────────────────────

// TestPanicRecoveryDoesNotPropagateToPublisher verifies that the panic is
// fully swallowed inside the bus — the caller of Publish never sees it.
// This is the minimal invariant: Publish must not panic out.
func TestPanicRecoveryDoesNotPropagateToPublisher(t *testing.T) {
	t.Parallel()

	b := NewBus()
	Subscribe(b, func(w2EvtPanic) {
		panic("bus must not let this escape")
	}, WithName("panic-only"))

	// If the panic escapes, the defer below catches it and fails the test.
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("panic escaped Publish: %v", r)
		}
	}()

	Publish(b, w2EvtPanic{Base: hmevent.NewBase()})
}

// TestPanicRecoveryLogsViaSlog verifies that a recovered panic produces a
// log record with "event handler panicked" and the handler name. We
// redirect slog's default handler to a buffer for the duration of the test.
//
// Intentionally NOT t.Parallel(): this test mutates slog.Default() globally
// and any concurrent panic-handler-test that triggers the slog.Error path
// would race on the shared Buffer.
func TestPanicRecoveryLogsViaSlog(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError})
	logger := slog.New(handler)

	// Temporarily override the default slog handler.
	old := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(old)

	b := NewBus()
	Subscribe(b, func(w2EvtPanic) {
		panic("slog-capture-test")
	}, WithName("logged-panicker"))

	Publish(b, w2EvtPanic{Base: hmevent.NewBase()})

	logOutput := buf.String()
	if !strings.Contains(logOutput, "event handler panicked") {
		t.Errorf("slog output missing 'event handler panicked': %q", logOutput)
	}
	if !strings.Contains(logOutput, "logged-panicker") {
		t.Errorf("slog output missing handler name 'logged-panicker': %q", logOutput)
	}
}

// TestPanicRecoveryAllSubsequentHandlersFire verifies N-handler isolation:
// with 3 handlers where the middle one panics, all of #1, #3 must fire.
func TestPanicRecoveryAllSubsequentHandlersFire(t *testing.T) {
	t.Parallel()

	b := NewBus()
	var fired [3]atomic.Int32

	// All three at same priority to ensure registration order.
	Subscribe(b, func(w2EvtPanic) { fired[0].Add(1) }, WithPriority(PriorityNormal), WithName("h0"))
	Subscribe(b, func(w2EvtPanic) { panic("middle panics") }, WithPriority(PriorityNormal), WithName("h1"))
	Subscribe(b, func(w2EvtPanic) { fired[2].Add(1) }, WithPriority(PriorityNormal), WithName("h2"))

	Publish(b, w2EvtPanic{Base: hmevent.NewBase()})

	if got := fired[0].Load(); got != 1 {
		t.Errorf("h0.fired=%d, want 1 (must fire before panicking handler)", got)
	}
	if got := fired[2].Load(); got != 1 {
		t.Errorf("h2.fired=%d, want 1 (must fire after panicking handler is recovered)", got)
	}
}

// TestPanicRecoveryRepeatedPanicsDoNotBreakBus verifies that a handler
// that panics on every invocation does not degrade the bus across multiple
// publishes — the working handler fires each time.
func TestPanicRecoveryRepeatedPanicsDoNotBreakBus(t *testing.T) {
	t.Parallel()

	b := NewBus()
	var workingCount atomic.Int32
	var panicCount atomic.Int32

	Subscribe(b, func(w2EvtPanic) {
		panicCount.Add(1)
		panic(fmt.Sprintf("panic #%d", panicCount.Load()))
	}, WithPriority(PriorityHigh), WithName("always-panics"))

	Subscribe(b, func(w2EvtPanic) {
		workingCount.Add(1)
	}, WithPriority(PriorityNormal), WithName("always-works"))

	const rounds = 5
	for range rounds {
		Publish(b, w2EvtPanic{Base: hmevent.NewBase()})
	}

	if got := workingCount.Load(); got != rounds {
		t.Errorf("workingCount=%d, want %d (bus must stay healthy across repeated panics)", got, rounds)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// ClearSubscriptions / ClearAllSubscriptions
// ──────────────────────────────────────────────────────────────────────────────

// TestClearSubscriptionsRemovesOnlyTargetType verifies that
// ClearSubscriptions(type) removes handlers for the specified type while
// leaving handlers for other types intact.
func TestClearSubscriptionsRemovesOnlyTargetType(t *testing.T) {
	t.Parallel()

	b := NewBus()

	var countA, countB atomic.Int32
	Subscribe(b, func(w2EvtClear) { countA.Add(1) })
	Subscribe(b, func(w2EvtClearB) { countB.Add(1) })

	// Verify both fire before the clear.
	Publish(b, w2EvtClear{Base: hmevent.NewBase()})
	Publish(b, w2EvtClearB{Base: hmevent.NewBase()})
	if got := countA.Load(); got != 1 {
		t.Fatalf("pre-clear: countA=%d, want 1", got)
	}
	if got := countB.Load(); got != 1 {
		t.Fatalf("pre-clear: countB=%d, want 1", got)
	}

	// Clear only type A.
	b.ClearSubscriptions(w2EvtClear{}.Type())

	if got := b.HandlerCount(w2EvtClear{}.Type()); got != 0 {
		t.Errorf("after ClearSubscriptions(A): HandlerCount(A)=%d, want 0", got)
	}
	if got := b.HandlerCount(w2EvtClearB{}.Type()); got != 1 {
		t.Errorf("after ClearSubscriptions(A): HandlerCount(B)=%d, want 1 (B must be unaffected)", got)
	}

	// Publishing A must not fire the cleared handler; B handler still works.
	Publish(b, w2EvtClear{Base: hmevent.NewBase()})
	Publish(b, w2EvtClearB{Base: hmevent.NewBase()})
	if got := countA.Load(); got != 1 {
		t.Errorf("after ClearSubscriptions(A): countA=%d, want 1 (no new fires)", got)
	}
	if got := countB.Load(); got != 2 {
		t.Errorf("after ClearSubscriptions(A): countB=%d, want 2 (B must still fire)", got)
	}
}

// TestClearSubscriptionsIsIdempotent verifies that calling
// ClearSubscriptions on a type with no registered handlers is safe.
func TestClearSubscriptionsIsIdempotent(t *testing.T) {
	t.Parallel()

	b := NewBus()
	// No subscriptions for w2EvtClear — clear should not panic.
	b.ClearSubscriptions(w2EvtClear{}.Type())
	b.ClearSubscriptions(w2EvtClear{}.Type()) // second call, still no-op

	// Registering after a clear works normally.
	var count atomic.Int32
	Subscribe(b, func(w2EvtClear) { count.Add(1) })
	Publish(b, w2EvtClear{Base: hmevent.NewBase()})
	if got := count.Load(); got != 1 {
		t.Errorf("count=%d, want 1 (subscribe after clear must work)", got)
	}
}

// TestClearAllSubscriptionsRemovesEverything verifies that
// ClearAllSubscriptions removes all handlers across every event type.
// EventStats counters must survive the clear.
func TestClearAllSubscriptionsRemovesEverything(t *testing.T) {
	t.Parallel()

	b := NewBus()

	var countA, countB atomic.Int32
	Subscribe(b, func(w2EvtClear) { countA.Add(1) })
	Subscribe(b, func(w2EvtClearB) { countB.Add(1) })

	// Publish once to accumulate EventStats.
	Publish(b, w2EvtClear{Base: hmevent.NewBase()})
	Publish(b, w2EvtClearB{Base: hmevent.NewBase()})

	// Clear all.
	b.ClearAllSubscriptions()

	if got := b.TotalSubscriptionCount(); got != 0 {
		t.Errorf("TotalSubscriptionCount=%d after ClearAll, want 0", got)
	}
	if got := b.HandlerCount(w2EvtClear{}.Type()); got != 0 {
		t.Errorf("HandlerCount(A)=%d after ClearAll, want 0", got)
	}
	if got := b.HandlerCount(w2EvtClearB{}.Type()); got != 0 {
		t.Errorf("HandlerCount(B)=%d after ClearAll, want 0", got)
	}

	// Publish again — no handlers should fire.
	Publish(b, w2EvtClear{Base: hmevent.NewBase()})
	Publish(b, w2EvtClearB{Base: hmevent.NewBase()})
	if got := countA.Load(); got != 1 {
		t.Errorf("countA=%d after ClearAll+publish, want 1 (no new fires)", got)
	}
	if got := countB.Load(); got != 1 {
		t.Errorf("countB=%d after ClearAll+publish, want 1 (no new fires)", got)
	}

	// EventStats counters MUST survive the clear (publishes still counted).
	stats := b.EventStats()
	if got := stats[string(w2EvtClear{}.Type())]; got != 2 {
		t.Errorf("EventStats[A]=%d after ClearAll, want 2 (stats must survive clear)", got)
	}
	if got := stats[string(w2EvtClearB{}.Type())]; got != 2 {
		t.Errorf("EventStats[B]=%d after ClearAll, want 2 (stats must survive clear)", got)
	}
}

// TestClearSubscriptionsDoesNotResetEventStats confirms the per-type
// clear variant also does not affect EventStats.
func TestClearSubscriptionsDoesNotResetEventStats(t *testing.T) {
	t.Parallel()

	b := NewBus()
	Subscribe(b, func(w2EvtClear) {})

	for range 4 {
		Publish(b, w2EvtClear{Base: hmevent.NewBase()})
	}

	b.ClearSubscriptions(w2EvtClear{}.Type())

	if got := b.EventStats()[string(w2EvtClear{}.Type())]; got != 4 {
		t.Errorf("EventStats after ClearSubscriptions=%d, want 4 (stats must not be cleared)", got)
	}
}

// TestClearAllThenResubscribeWorks verifies that subscriptions registered
// after ClearAllSubscriptions work correctly.
func TestClearAllThenResubscribeWorks(t *testing.T) {
	t.Parallel()

	b := NewBus()

	// First round.
	var round1 atomic.Int32
	Subscribe(b, func(w2EvtClear) { round1.Add(1) })
	Publish(b, w2EvtClear{Base: hmevent.NewBase()})
	if got := round1.Load(); got != 1 {
		t.Fatalf("round1=%d, want 1", got)
	}

	b.ClearAllSubscriptions()

	// Second round — fresh subscriptions must work.
	var round2 atomic.Int32
	Subscribe(b, func(w2EvtClear) { round2.Add(1) })
	Publish(b, w2EvtClear{Base: hmevent.NewBase()})
	if got := round2.Load(); got != 1 {
		t.Errorf("round2=%d, want 1 (re-subscribe after ClearAll must work)", got)
	}
	// Old handler must not fire.
	if got := round1.Load(); got != 1 {
		t.Errorf("round1=%d, want 1 (old handler must not fire after ClearAll)", got)
	}
}

// TestClearSubscriptionsRaceSafe is a concurrent stress test that
// interleaves ClearSubscriptions / ClearAllSubscriptions with Subscribe
// and Publish. The goal is clean execution under -race; no exact counts
// are asserted.
func TestClearSubscriptionsRaceSafe(t *testing.T) {
	b := NewBus()

	const goroutines = 20
	const iterations = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := range goroutines {
		i := i
		go func() {
			defer wg.Done()
			for range iterations {
				switch i % 4 {
				case 0:
					u := Subscribe(b, func(w2EvtClear) {})
					u()
				case 1:
					Publish(b, w2EvtClear{Base: hmevent.NewBase()})
				case 2:
					b.ClearSubscriptions(w2EvtClear{}.Type())
				case 3:
					b.ClearAllSubscriptions()
				}
			}
		}()
	}

	wg.Wait()
	// Reaching here without a race or panic is the goal.
}

// ──────────────────────────────────────────────────────────────────────────────
// HandlerStat TotalDurationMs / TotalErrors
// ──────────────────────────────────────────────────────────────────────────────

// TestHandlerStatDurationIsMonotonic verifies that TotalDurationMs grows with
// each handler invocation — it is non-decreasing. We also check it is
// strictly positive after at least one dispatch.
func TestHandlerStatDurationIsMonotonic(t *testing.T) {
	t.Parallel()

	b := NewBus()

	// Handler with a deliberate short sleep so duration is measurable.
	Subscribe(b, func(w2EvtDuration) {
		time.Sleep(time.Millisecond)
	}, WithName("slow-handler"))

	var prevDuration float64
	const rounds = 4
	for i := range rounds {
		Publish(b, w2EvtDuration{Base: hmevent.NewBase()})

		stats := b.HandlerStats()
		var stat *HandlerStat
		for j := range stats {
			if stats[j].Name == "slow-handler" {
				stat = &stats[j]
				break
			}
		}
		if stat == nil {
			t.Fatalf("round %d: 'slow-handler' missing from HandlerStats", i)
		}
		if stat.TotalDurationMs <= prevDuration {
			t.Errorf(
				"round %d: TotalDurationMs=%f not > prev=%f (must be monotonically increasing)",
				i, stat.TotalDurationMs, prevDuration,
			)
		}
		prevDuration = stat.TotalDurationMs
	}

	if prevDuration <= 0 {
		t.Errorf("TotalDurationMs=%f, must be > 0 after %d rounds", prevDuration, rounds)
	}
}

// TestHandlerStatTotalErrorsCountsPanics verifies that each recovered panic
// increments TotalErrors on the panicking handler's stat.
func TestHandlerStatTotalErrorsCountsPanics(t *testing.T) {
	t.Parallel()

	b := NewBus()

	Subscribe(b, func(w2EvtError) {
		panic("error for stats")
	}, WithName("error-handler"))

	const panics = 3
	for range panics {
		Publish(b, w2EvtError{Base: hmevent.NewBase()})
	}

	stats := b.HandlerStats()
	var stat *HandlerStat
	for i := range stats {
		if stats[i].Name == "error-handler" {
			stat = &stats[i]
			break
		}
	}

	if stat == nil {
		t.Fatal("'error-handler' missing from HandlerStats")
	}
	if got := stat.TotalErrors; got != panics {
		t.Errorf("TotalErrors=%d, want %d (each recovered panic must count)", got, panics)
	}
}

// TestHandlerStatTotalErrorsNotIncrementedForSuccess verifies that
// TotalErrors stays zero for a handler that never panics.
func TestHandlerStatTotalErrorsNotIncrementedForSuccess(t *testing.T) {
	t.Parallel()

	b := NewBus()
	Subscribe(b, func(w2EvtError) {}, WithName("clean-handler"))

	for range 5 {
		Publish(b, w2EvtError{Base: hmevent.NewBase()})
	}

	stats := b.HandlerStats()
	var stat *HandlerStat
	for i := range stats {
		if stats[i].Name == "clean-handler" {
			stat = &stats[i]
			break
		}
	}

	if stat == nil {
		t.Fatal("'clean-handler' missing from HandlerStats")
	}
	if got := stat.TotalErrors; got != 0 {
		t.Errorf("TotalErrors=%d, want 0 for non-panicking handler", got)
	}
}

// TestHandlerStatDurationStableUnderConcurrentPublish verifies that
// TotalDurationMs and TotalErrors remain consistent (no races) when
// events are published from multiple goroutines simultaneously.
// Run under -race.
func TestHandlerStatDurationStableUnderConcurrentPublish(t *testing.T) {
	t.Parallel()

	b := NewBus()
	Subscribe(b, func(w2EvtDuration) {}, WithName("concurrent-target"))

	const (
		goroutines = 20
		iterations = 50
	)

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			for range iterations {
				Publish(b, w2EvtDuration{Base: hmevent.NewBase()})
			}
		}()
	}
	// Simultaneously read HandlerStats to exercise concurrent read/write.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range goroutines * iterations {
			_ = b.HandlerStats()
		}
	}()
	wg.Wait()

	stats := b.HandlerStats()
	var stat *HandlerStat
	for i := range stats {
		if stats[i].Name == "concurrent-target" {
			stat = &stats[i]
			break
		}
	}

	if stat == nil {
		t.Fatal("'concurrent-target' missing from HandlerStats")
	}
	// Every dispatch should have been counted (Matches == total dispatched).
	want := uint64(goroutines * iterations)
	if got := stat.Matches; got != want {
		t.Errorf("Matches=%d, want %d", got, want)
	}
	// Duration must be non-negative.
	if stat.TotalDurationMs < 0 {
		t.Errorf("TotalDurationMs=%f, must be >= 0", stat.TotalDurationMs)
	}
}

// TestHandlerStatsBothFieldsPopulated verifies that a mixed scenario
// (some handlers panic, some don't) produces correct TotalErrors counts
// and positive TotalDurationMs for both.
func TestHandlerStatsBothFieldsPopulated(t *testing.T) {
	t.Parallel()

	b := NewBus()
	Subscribe(b, func(w2EvtError) {
		panic("intentional")
	}, WithName("panicker"))
	Subscribe(b, func(w2EvtError) {}, WithName("clean"))

	const publishes = 4
	for range publishes {
		Publish(b, w2EvtError{Base: hmevent.NewBase()})
	}

	stats := b.HandlerStats()
	found := make(map[string]HandlerStat, 2)
	for _, s := range stats {
		found[s.Name] = s
	}

	panicker, ok := found["panicker"]
	if !ok {
		t.Fatal("'panicker' missing from HandlerStats")
	}
	if panicker.TotalErrors != publishes {
		t.Errorf("panicker.TotalErrors=%d, want %d", panicker.TotalErrors, publishes)
	}
	if panicker.TotalDurationMs < 0 {
		t.Errorf("panicker.TotalDurationMs=%f, must be >= 0", panicker.TotalDurationMs)
	}

	clean, ok := found["clean"]
	if !ok {
		t.Fatal("'clean' missing from HandlerStats")
	}
	if clean.TotalErrors != 0 {
		t.Errorf("clean.TotalErrors=%d, want 0", clean.TotalErrors)
	}
	if clean.TotalDurationMs < 0 {
		t.Errorf("clean.TotalDurationMs=%f, must be >= 0", clean.TotalDurationMs)
	}
}
