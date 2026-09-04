// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package reliability

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// --- Circuit breaker ---

func TestCircuitOpensAfterThreshold(t *testing.T) {
	now := time.Unix(0, 0)
	c := NewCircuit(CircuitConfig{FailureThreshold: 2, ResetTimeout: time.Second, Clock: func() time.Time { return now }})

	err := errors.New("boom")
	for range 2 {
		_ = c.Do(context.Background(), "setValue", func(context.Context) error { return err })
	}
	if c.State() != hmenum.CircuitStateOpen {
		t.Fatalf("state=%s, want open", c.State())
	}
	gotErr := c.Do(context.Background(), "setValue", func(context.Context) error { return nil })
	if !errors.Is(gotErr, hmerr.ErrCircuitBreakerOpen) {
		t.Fatalf("got %v, want ErrCircuitBreakerOpen", gotErr)
	}
}

func TestCircuitHalfOpenAndClose(t *testing.T) {
	now := time.Unix(0, 0)
	clk := func() time.Time { return now }
	c := NewCircuit(CircuitConfig{FailureThreshold: 1, ResetTimeout: time.Second, HalfOpenSuccess: 1, Clock: clk})

	_ = c.Do(context.Background(), "setValue", func(context.Context) error { return errors.New("x") })
	if c.State() != hmenum.CircuitStateOpen {
		t.Fatalf("state=%s, want open", c.State())
	}
	now = now.Add(2 * time.Second)
	// State() refreshes OPEN → HALF_OPEN.
	if c.State() != hmenum.CircuitStateHalfOpen {
		t.Fatalf("state=%s, want half_open", c.State())
	}
	_ = c.Do(context.Background(), "setValue", func(context.Context) error { return nil })
	if c.State() != hmenum.CircuitStateClosed {
		t.Fatalf("state=%s, want closed", c.State())
	}
}

func TestCircuitHalfOpenFailureReopens(t *testing.T) {
	now := time.Unix(0, 0)
	clk := func() time.Time { return now }
	c := NewCircuit(CircuitConfig{FailureThreshold: 1, ResetTimeout: time.Second, HalfOpenSuccess: 1, Clock: clk})

	_ = c.Do(context.Background(), "setValue", func(context.Context) error { return errors.New("x") })
	now = now.Add(2 * time.Second)
	_ = c.State() // forces HALF_OPEN

	_ = c.Do(context.Background(), "setValue", func(context.Context) error { return errors.New("x") })
	if c.State() != hmenum.CircuitStateOpen {
		t.Fatalf("state=%s, want open after half-open failure", c.State())
	}
}

// --- Retrier ---

func TestRetrierEventuallySucceeds(t *testing.T) {
	r := NewRetrier(RetryConfig{MaxAttempts: 3, Initial: time.Millisecond, Max: time.Millisecond})
	var attempts int
	err := r.Do(context.Background(), func(context.Context, int) error {
		attempts++
		if attempts < 2 {
			return errors.New("transient")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts=%d", attempts)
	}
}

func TestRetrierGivesUp(t *testing.T) {
	r := NewRetrier(RetryConfig{MaxAttempts: 2, Initial: time.Millisecond})
	err := r.Do(context.Background(), func(context.Context, int) error { return errors.New("x") })
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRetrierShortCircuitsOnNonRetryable(t *testing.T) {
	r := NewRetrier(RetryConfig{MaxAttempts: 5, Initial: time.Millisecond})
	var attempts int
	err := r.Do(context.Background(), func(context.Context, int) error {
		attempts++
		return hmerr.ErrAuthFailure
	})
	if !errors.Is(err, hmerr.ErrAuthFailure) {
		t.Fatalf("got %v, want ErrAuthFailure", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts=%d, want 1", attempts)
	}
}

func TestRetrierRespectsContext(t *testing.T) {
	r := NewRetrier(RetryConfig{MaxAttempts: 5, Initial: 50 * time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := r.Do(ctx, func(context.Context, int) error { return errors.New("x") })
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- Throttle ---

func TestThrottleSerialisesConcurrent(t *testing.T) {
	tr := NewThrottle(ThrottleConfig{MaxInFlight: 1})
	var peak atomic.Int32
	var inflight atomic.Int32
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if err := tr.Acquire(context.Background(), hmenum.CommandPriorityHigh); err != nil {
				t.Error(err)
				return
			}
			defer tr.Release()
			n := inflight.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(2 * time.Millisecond)
			inflight.Add(-1)
		})
	}
	wg.Wait()
	if peak.Load() > 1 {
		t.Fatalf("peak=%d, want 1", peak.Load())
	}
}

func TestThrottlePriorityOrder(t *testing.T) {
	tr := NewThrottle(ThrottleConfig{MaxInFlight: 1})
	// Block the single slot.
	if err := tr.Acquire(context.Background(), hmenum.CommandPriorityLow); err != nil {
		t.Fatal(err)
	}

	order := make(chan string, 3)
	var wg sync.WaitGroup
	wg.Add(3)

	queue := func(name string, p hmenum.CommandPriority) {
		go func() {
			defer wg.Done()
			_ = tr.Acquire(context.Background(), p)
			order <- name
			tr.Release()
		}()
	}

	// Queue each waiter and wait until it has actually parked in the
	// priority queue (Waiting() reflects it) before queuing the next, so
	// the heap ordering is deterministic regardless of goroutine
	// scheduling. A fixed sleep between queue() calls was racy: under load
	// a goroutine might not have parked yet, scrambling the dequeue order.
	queue("low", hmenum.CommandPriorityLow)
	waitForWaiters(t, tr, 1)
	queue("high", hmenum.CommandPriorityHigh)
	waitForWaiters(t, tr, 2)
	queue("critical", hmenum.CommandPriorityCritical)
	waitForWaiters(t, tr, 3)

	// Release the initial permit; critical should admit first.
	tr.Release()
	wg.Wait()
	close(order)
	seen := make([]string, 0, 3)
	for s := range order {
		seen = append(seen, s)
	}
	if seen[0] != "critical" || seen[1] != "high" || seen[2] != "low" {
		t.Fatalf("order=%v", seen)
	}
}

func TestThrottleContextCancelReleasesWaiter(t *testing.T) {
	tr := NewThrottle(ThrottleConfig{MaxInFlight: 1})
	_ = tr.Acquire(context.Background(), hmenum.CommandPriorityHigh)
	defer tr.Release()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := tr.Acquire(ctx, hmenum.CommandPriorityHigh); err == nil {
		t.Fatal("expected cancellation error")
	}
	if tr.Waiting() != 0 {
		t.Fatalf("waiting=%d, should be cleared after cancel", tr.Waiting())
	}
}

// --- Coalescer ---

func TestCoalescerSharesResult(t *testing.T) {
	c := NewCoalescer()
	var calls atomic.Int32
	fn := func(context.Context) (any, error) {
		calls.Add(1)
		time.Sleep(10 * time.Millisecond)
		return "ok", nil
	}
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			v, err := c.Do(context.Background(), "k", fn)
			if err != nil || v.(string) != "ok" {
				t.Errorf("got %v %v", v, err)
			}
		})
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("calls=%d", calls.Load())
	}
}

// --- PingPong ---

func TestPingPongMatch(t *testing.T) {
	tr := NewPingPongTracker(PingPongConfig{})
	tr.RecordPing("a")
	matched, _ := tr.RecordPong("a")
	if !matched {
		t.Fatal("matching pong should succeed")
	}
	if tr.PendingCount() != 0 {
		t.Fatal("pending should be empty")
	}
}

func TestPingPongUnmatchedPong(t *testing.T) {
	tr := NewPingPongTracker(PingPongConfig{})
	if matched, _ := tr.RecordPong("orphan"); matched {
		t.Fatal("should report no match")
	}
	if tr.UnknownCount() != 1 {
		t.Fatal("orphan should be in unknown table")
	}
}

func TestPingPongSweepExpires(t *testing.T) {
	fake := clock.NewFake(time.Unix(0, 0))
	tr := NewPingPongTracker(PingPongConfig{
		PendingTTL: 100 * time.Millisecond,
		UnknownTTL: 100 * time.Millisecond,
		Clock:      fake,
	})
	tr.RecordPing("a")
	tr.RecordPong("orphan")
	fake.Advance(200 * time.Millisecond)
	out := tr.Sweep()
	if len(out) != 2 {
		t.Fatalf("sweep returned %d", len(out))
	}
}

func TestPingPongRTTComputed(t *testing.T) {
	fake := clock.NewFake(time.Unix(0, 0))
	tr := NewPingPongTracker(PingPongConfig{Clock: fake})
	tr.RecordPing("a")
	fake.Advance(123 * time.Millisecond)
	matched, rtt := tr.RecordPong("a")
	if !matched {
		t.Fatal("matched expected")
	}
	if rtt != 123*time.Millisecond {
		t.Fatalf("rtt=%v want 123ms", rtt)
	}
}

func TestPingPongStatsSeverity(t *testing.T) {
	fake := clock.NewFake(time.Unix(0, 0))
	tr := NewPingPongTracker(PingPongConfig{
		PendingTTL:        time.Hour, // disable expiry
		UnknownTTL:        time.Hour,
		MismatchThreshold: 2,
		Clock:             fake,
	})
	if got := tr.Stats().Severity; got != "ok" {
		t.Fatalf("empty severity=%q want ok", got)
	}
	// Exactly at the threshold is not over it: the severity boundary is
	// the same strict one every emit decision uses.
	tr.RecordPing("a")
	tr.RecordPing("b")
	if got := tr.Stats().Severity; got != "ok" {
		t.Fatalf("pending=2 (== threshold) severity=%q want ok", got)
	}
	// Three unmatched PINGs → pending=3, unknown=0 → degraded.
	tr.RecordPing("c")
	if got := tr.Stats().Severity; got != "degraded" {
		t.Fatalf("pending=3 severity=%q want degraded", got)
	}
	// Three orphan PONGs → unknown=3 → both tables over → critical.
	tr.RecordPong("orphan1")
	tr.RecordPong("orphan2")
	tr.RecordPong("orphan3")
	if got := tr.Stats().Severity; got != "critical" {
		t.Fatalf("both severity=%q want critical", got)
	}
}

func TestPingPongJournalRingBuffer(t *testing.T) {
	fake := clock.NewFake(time.Unix(0, 0))
	tr := NewPingPongTracker(PingPongConfig{
		JournalSize: 3,
		Clock:       fake,
	})
	tr.RecordPing("a")
	tr.RecordPing("b")
	tr.RecordPing("c")
	tr.RecordPing("d") // overruns the ring → "a" sent entry drops out
	j := tr.Journal()
	if len(j) != 3 {
		t.Fatalf("len=%d want 3", len(j))
	}
	wantIDs := []string{"b", "c", "d"}
	for i, e := range j {
		if e.ID != wantIDs[i] || e.Kind != JournalEventSent {
			t.Fatalf("entry[%d]=%+v want id=%q sent", i, e, wantIDs[i])
		}
	}
}

func TestPingPongStatsCounters(t *testing.T) {
	tr := NewPingPongTracker(PingPongConfig{})
	tr.RecordPing("a")
	tr.RecordPing("b")
	tr.RecordPong("a")     // matched
	tr.RecordPong("ghost") // unknown
	s := tr.Stats()
	if s.TotalSent != 2 || s.TotalReceived != 2 || s.MatchedTotal != 1 || s.Pending != 1 || s.Unknown != 1 {
		t.Fatalf("stats=%+v", s)
	}
}

// --- Stats.SuccessRate ---

func TestStats_SuccessRate_ZeroTotal(t *testing.T) {
	s := Stats{TotalSent: 0, MatchedTotal: 0}
	if got := s.SuccessRate(); got != 0 {
		t.Errorf("SuccessRate with TotalSent=0: got %f, want 0", got)
	}
}

func TestStats_SuccessRate_NonZero(t *testing.T) {
	s := Stats{TotalSent: 4, MatchedTotal: 3}
	got := s.SuccessRate()
	want := 0.75
	if got != want {
		t.Errorf("SuccessRate: got %f, want %f", got, want)
	}
}

func TestStats_SuccessRate_AllMatched(t *testing.T) {
	s := Stats{TotalSent: 10, MatchedTotal: 10}
	got := s.SuccessRate()
	if got != 1.0 {
		t.Errorf("SuccessRate(all matched): got %f, want 1.0", got)
	}
}
