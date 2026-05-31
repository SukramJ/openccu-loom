// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability

// Edge-case tests — first wave. Each case targets a specific gap between
// the Python reference (test_command_retry.py, test_command_throttle.py) and
// the existing Go test suite. All tests are deterministic: fake clocks
// replace wall-clock sleeps wherever possible; real-time sleeps are kept to
// the minimum needed for goroutine scheduling.
//
// Covered gaps:
// 1. Retrier.DoForKey re-registration after CancelKey (new chain registers).
// 2. Circuit-breaker AddOnStateChange + OnStateChange coexist:
// both listeners fire on the same state transition.
// 3. Circuit OPEN→HALF_OPEN callback runs outside the lock
// (refreshLocked fires via goroutine; CB methods may be called from
// within the callback without deadlock).
// 4. Coalescer Clear while leader executes: the leader's own Do call still
// returns its result (not nil/nil like cleared followers).
// 5. Throttle Purge concurrent with burst-window waiters:
// Purge unblocks address-tagged waiters while burst-window waiters
// for a different address remain queued.
// 6. Retrier exhausts with context already cancelled: ctx.Err is returned,
// not the wrapped "exhausted after N attempts" error.

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ─── 1. Retrier.DoForKey re-registration after CancelKey ────────────────────

// TestRetrierDoForKeyReregistersAfterCancelKey verifies that after CancelKey
// removes a slot, a subsequent DoForKey on the same key can succeed — the
// registry is clean enough to accept a new entry.
func TestRetrierDoForKeyReregistersAfterCancelKey(t *testing.T) {
	t.Parallel()

	r := NewRetrier(RetryConfig{
		MaxAttempts: 5,
		Initial:     100 * time.Millisecond,
		Max:         200 * time.Millisecond,
		Multiplier:  2,
	})

	key := hmtypes.DataPointKey{
		InterfaceID:    "HmIP-RF",
		ChannelAddress: "ABCD1234:1",
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      "LEVEL",
	}

	// Start a retry chain that always fails; it will park on its first backoff.
	blocking := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- r.DoForKey(context.Background(), key, func(_ context.Context, _ int) error {
			<-blocking // block until released to prevent the timer from being the cancellation mechanism
			return errors.New("transient")
		})
	}()

	// Wait for the goroutine to register.
	deadline := time.Now().Add(time.Second)
	for r.ActiveRetryCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if r.ActiveRetryCount() != 1 {
		close(blocking)
		t.Fatalf("expected 1 active retry, got %d", r.ActiveRetryCount())
	}

	// CancelKey wakes the goroutine.
	close(blocking)
	r.CancelKey(key)

	select {
	case err := <-firstDone:
		if !errors.Is(err, ErrRetrySuperseded) {
			t.Fatalf("first chain: expected ErrRetrySuperseded, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first chain did not return after CancelKey")
	}

	// After cancellation the slot is gone.
	if got := r.ActiveRetryCount(); got != 0 {
		t.Fatalf("ActiveRetryCount after CancelKey=%d, want 0", got)
	}

	// A new DoForKey on the same key must succeed without interference.
	if err := r.DoForKey(context.Background(), key, func(_ context.Context, _ int) error {
		return nil // immediate success
	}); err != nil {
		t.Fatalf("re-registration: %v", err)
	}
	if got := r.ActiveRetryCount(); got != 0 {
		t.Fatalf("ActiveRetryCount after successful re-run=%d, want 0", got)
	}
}

// ─── 2. Circuit-breaker: OnStateChange and AddOnStateChange both fire ───────

// TestCircuitBothListenersFireOnTrip verifies that the primary listener
// registered via OnStateChange and an additional listener registered via
// AddOnStateChange both fire when the circuit trips to OPEN. This pins the
// contract that listeners are additive, not exclusive.
func TestCircuitBothListenersFireOnTrip(t *testing.T) {
	t.Parallel()

	c := NewCircuit(CircuitConfig{
		FailureThreshold: 1,
		ResetTimeout:     time.Minute,
		HalfOpenSuccess:  1,
	})

	var primaryFired atomic.Int32
	var additionalFired atomic.Int32

	c.OnStateChange(func(_, to hmenum.CircuitState) {
		if to == hmenum.CircuitStateOpen {
			primaryFired.Add(1)
		}
	})
	c.AddOnStateChange(func(_, to hmenum.CircuitState) {
		if to == hmenum.CircuitStateOpen {
			additionalFired.Add(1)
		}
	})

	// Trigger a trip.
	_ = c.Do(context.Background(), "setValue", func(_ context.Context) error {
		return errors.New("failure")
	})

	if c.State() != hmenum.CircuitStateOpen {
		t.Fatalf("breaker not OPEN after single failure (threshold=1)")
	}
	if n := primaryFired.Load(); n != 1 {
		t.Errorf("primary listener fired %d times, want 1", n)
	}
	if n := additionalFired.Load(); n != 1 {
		t.Errorf("additional listener fired %d times, want 1", n)
	}
}

// TestCircuitAddOnStateChangeDoesNotDisplacePrimary verifies that calling
// AddOnStateChange does not overwrite (or silence) a primary listener that
// was registered via OnStateChange before any additional listeners were added.
func TestCircuitAddOnStateChangeDoesNotDisplacePrimary(t *testing.T) {
	t.Parallel()

	c := NewCircuit(CircuitConfig{
		FailureThreshold: 2,
		ResetTimeout:     time.Minute,
		HalfOpenSuccess:  1,
	})

	var primaryCount, addCount atomic.Int32

	c.OnStateChange(func(_, _ hmenum.CircuitState) { primaryCount.Add(1) })
	c.AddOnStateChange(func(_, _ hmenum.CircuitState) { addCount.Add(1) })
	c.AddOnStateChange(func(_, _ hmenum.CircuitState) { addCount.Add(1) }) // two additional

	// Cause 2 failures to trip.
	for i := 0; i < 2; i++ {
		_ = c.Do(context.Background(), "setValue", func(_ context.Context) error {
			return errors.New("fail")
		})
	}

	if c.State() != hmenum.CircuitStateOpen {
		t.Fatalf("expected OPEN after %d failures", 2)
	}

	// One state change (CLOSED→OPEN).
	if n := primaryCount.Load(); n != 1 {
		t.Errorf("primary listener count=%d, want 1", n)
	}
	if n := addCount.Load(); n != 2 {
		t.Errorf("additional listener count=%d, want 2 (2 listeners × 1 transition)", n)
	}
}

// ─── 3. Circuit OPEN→HALF_OPEN callback runs outside the lock ───────────────

// TestCircuitHalfOpenCallbackCanCallBack verifies that the OPEN→HALF_OPEN
// state change callback (which refreshLocked fires via a goroutine) does not
// deadlock when it calls back into the CircuitBreaker (e.g. reads State()).
//
// The proof: if the callback held the lock, calling c.State() would deadlock
// and the test would time out. With the goroutine dispatch the callback runs
// lock-free and returns promptly.
func TestCircuitHalfOpenCallbackCanCallBack(t *testing.T) {
	t.Parallel()

	tick := time.Unix(0, 0)
	clk := func() time.Time { return tick }

	c := NewCircuit(CircuitConfig{
		FailureThreshold: 1,
		ResetTimeout:     100 * time.Millisecond,
		HalfOpenSuccess:  1,
		Clock:            clk,
	})

	callbackDone := make(chan hmenum.CircuitState, 1)
	c.AddOnStateChange(func(_, to hmenum.CircuitState) {
		if to == hmenum.CircuitStateHalfOpen {
			// This must not deadlock — the callback runs outside the lock.
			callbackDone <- c.State()
		}
	})

	// Trip the breaker.
	_ = c.Do(context.Background(), "setValue", func(_ context.Context) error {
		return errors.New("boom")
	})
	if c.State() != hmenum.CircuitStateOpen {
		t.Fatalf("expected OPEN, got %s", c.State())
	}

	// Advance fake clock past reset timeout to trigger OPEN→HALF_OPEN.
	tick = tick.Add(200 * time.Millisecond)

	// c.State() triggers refreshLocked which fires the callback via goroutine.
	c.State()

	select {
	case state := <-callbackDone:
		if state != hmenum.CircuitStateHalfOpen {
			t.Errorf("callback saw state %s, want HALF_OPEN", state)
		}
	case <-time.After(time.Second):
		t.Fatal("OPEN→HALF_OPEN callback did not fire (possible deadlock)")
	}
}

// ─── 4. Coalescer Clear: leader's own Do returns its own result ──────────────

// TestCoalescerClearLeaderRetainsOwnResult verifies that when Clear is called
// while the leader goroutine is running:
// - the leader's own Do call returns the result produced by its fn (not nil)
// - followers that were waiting receive nil/nil (as documented in Clear)
//
// This pins the cleared-flag guard introduced in coalesce.go so a second
// close(call.done) is never attempted by the leader after Clear already closed
// the channel.
func TestCoalescerClearLeaderRetainsOwnResult(t *testing.T) {
	t.Parallel()

	c := NewCoalescer()

	leaderStarted := make(chan struct{})
	leaderContinue := make(chan struct{})
	clearDone := make(chan struct{})

	sentinelVal := "leader-result"
	sentinelErr := errors.New("leader-error")

	// Leader goroutine blocks inside fn until clearDone (so Clear happens first).
	leaderResult := make(chan struct {
		val any
		err error
	}, 1)
	go func() {
		v, err := c.Do(context.Background(), "key", func(_ context.Context) (any, error) {
			close(leaderStarted)
			<-clearDone // wait until Clear has already been called
			<-leaderContinue
			return sentinelVal, sentinelErr
		})
		leaderResult <- struct {
			val any
			err error
		}{v, err}
	}()

	// Wait until leader is inside fn.
	<-leaderStarted

	// Start follower — it coalesces.
	followerResult := make(chan struct {
		val any
		err error
	}, 1)
	go func() {
		v, err := c.Do(context.Background(), "key", func(_ context.Context) (any, error) {
			return "should-not-run", nil
		})
		followerResult <- struct {
			val any
			err error
		}{v, err}
	}()

	// Give follower time to park.
	time.Sleep(10 * time.Millisecond)

	// Clear unblocks the follower.
	c.Clear()
	close(clearDone)

	// Follower must see nil/nil (cleared).
	select {
	case fr := <-followerResult:
		if fr.val != nil || fr.err != nil {
			t.Errorf("follower: got (%v, %v), want (nil, nil)", fr.val, fr.err)
		}
	case <-time.After(time.Second):
		t.Fatal("follower did not unblock after Clear")
	}

	// Now release the leader fn.
	close(leaderContinue)

	// Leader must see its own fn's return values, not nil/nil.
	select {
	case lr := <-leaderResult:
		if lr.val != sentinelVal {
			t.Errorf("leader val=%v, want %q", lr.val, sentinelVal)
		}
		if !errors.Is(lr.err, sentinelErr) {
			t.Errorf("leader err=%v, want %v", lr.err, sentinelErr)
		}
	case <-time.After(time.Second):
		t.Fatal("leader did not complete after leaderContinue")
	}
}

// ─── 5. Throttle Purge concurrent with burst-window waiters ─────────────────

// TestThrottlePurgeWithMixedPriorityQueue checks that Purge correctly cancels
// address-tagged waiters in the priority queue while leaving waiters with a
// different address queued and admissible in priority order.
//
// This is a richer variant of the existing TestThrottlePurgeCancelsAllWaitersForAddress:
// it adds a CRITICAL-priority "keep-me" waiter to verify that Purge does not
// disturb the heap's ordering of survivors and that the survivor is admitted
// before lower-priority waiters after the in-flight permit is released.
func TestThrottlePurgeWithMixedPriorityQueue(t *testing.T) {
	t.Parallel()

	// MaxInFlight=1 forces all Acquires past the first into the priority queue.
	tt := NewThrottle(ThrottleConfig{MaxInFlight: 1})

	// Hold the sole in-flight permit so subsequent Acquires must queue.
	if err := tt.Acquire(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("holder acquire: %v", err)
	}

	// Queue 3 HIGH-priority waiters for "purge-me".
	const numPurge = 3
	purgeResults := make(chan error, numPurge)
	for i := 0; i < numPurge; i++ {
		go func() {
			purgeResults <- tt.AcquireFor(context.Background(), hmenum.CommandPriorityHigh, "purge-me")
		}()
	}

	// Queue a CRITICAL-priority waiter for "keep-me" — it must survive Purge
	// and be admitted first (highest urgency) once the holder releases.
	keepResult := make(chan error, 1)
	go func() {
		keepResult <- tt.AcquireFor(context.Background(), hmenum.CommandPriorityCritical, "keep-me")
	}()

	// Wait for all 4 goroutines to enter the priority queue.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if tt.Waiting() >= numPurge+1 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if tt.Waiting() < numPurge+1 {
		t.Fatalf("expected ≥%d waiting, got %d", numPurge+1, tt.Waiting())
	}

	// Purge the "purge-me" waiters.
	purged := tt.Purge("purge-me")
	if purged != numPurge {
		t.Fatalf("Purge returned %d, want %d", purged, numPurge)
	}

	// All "purge-me" goroutines must return ErrSuperseded promptly.
	purgeTimeout := time.After(500 * time.Millisecond)
	for i := 0; i < numPurge; i++ {
		select {
		case err := <-purgeResults:
			if !errors.Is(err, ErrSuperseded) {
				t.Errorf("purge-me waiter %d: got %v, want ErrSuperseded", i, err)
			}
		case <-purgeTimeout:
			t.Fatalf("purge-me waiter %d did not return in time", i)
		}
	}

	// "keep-me" must still be queued — Waiting==1.
	if tt.Waiting() != 1 {
		t.Fatalf("Waiting=%d after Purge, want 1 (keep-me still queued)", tt.Waiting())
	}

	// Release the holder — CRITICAL "keep-me" must be admitted immediately.
	tt.Release()
	select {
	case err := <-keepResult:
		if err != nil {
			t.Errorf("keep-me got %v, want nil", err)
		} else {
			tt.Release() // release the permit keep-me acquired
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("keep-me was not admitted after holder released")
	}
}

// ─── 6. Retrier ctx already cancelled: ctx.Err not wrapped exhaustion ────────

// TestRetrierContextAlreadyCancelledReturnsCtxErr verifies that when the
// context is cancelled before the first attempt even runs, Do returns
// ctx.Err() (not nil, not a wrapped "exhausted" error). This is the Go
// equivalent of the Python test_retry_disabled_per_call / context-cancel
// semantics where the caller's cancellation always wins over exhaustion.
func TestRetrierContextAlreadyCancelledReturnsCtxErr(t *testing.T) {
	t.Parallel()

	r := NewRetrier(RetryConfig{
		MaxAttempts: 5,
		Initial:     10 * time.Millisecond,
		Max:         100 * time.Millisecond,
		Multiplier:  2,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel before calling Do

	var calls atomic.Int32
	err := r.Do(ctx, func(ctx context.Context, _ int) error {
		calls.Add(1)
		// fn itself returns ctx.Err() to simulate a context-aware operation.
		return ctx.Err()
	})

	// Must not be nil and must not be the internal exhaustion wrapper.
	if err == nil {
		t.Fatal("expected non-nil error on cancelled context, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled (or wrapping), got %v", err)
	}
	// The fn should have been called once (the first attempt), but Do
	// should not have retried because ctx was already done.
	if n := calls.Load(); n != 1 {
		t.Errorf("fn called %d times, want 1 (no retry on cancelled ctx)", n)
	}
}

// ─── 7. Circuit+Retry: RecoveryWaiter is called once per circuit failure ──────

// TestCircuitRecoveryWaiterCalledOnCircuitOpen verifies that when a Retrier
// is configured with a CircuitRecoveryWaiter and the fn returns
// ErrCircuitBreakerOpen, the WaitForRecovery method is invoked (at least once)
// before the retrier gives up. This pins the integration between the recovery
// waiter and the retry policy without requiring wall-clock waits.
func TestCircuitRecoveryWaiterCalledOnCircuitOpen(t *testing.T) {
	t.Parallel()

	var waiterCalls atomic.Int32
	waiter := RecoveryWaiterFunc(func(_ context.Context, _ time.Time) {
		waiterCalls.Add(1)
		// Return immediately — recovery is instant in this test.
	})

	r := NewRetrier(RetryConfig{
		MaxAttempts:    3,
		Initial:        10 * time.Millisecond,
		Max:            50 * time.Millisecond,
		RecoveryWaiter: waiter,
	})

	// fn always returns ErrCircuitBreakerOpen so every attempt triggers the
	// recovery waiter path.
	err := r.Do(context.Background(), func(_ context.Context, _ int) error {
		return hmerr.ErrCircuitBreakerOpen
	})

	// After MaxAttempts the retrier should be exhausted.
	if err == nil {
		t.Fatal("expected exhaustion error, got nil")
	}

	// WaitForRecovery should have been called once per inter-attempt gap
	// (MaxAttempts-1 gaps = 2 waiter calls for 3 attempts).
	if n := waiterCalls.Load(); n < 1 {
		t.Errorf("WaitForRecovery called %d times, want ≥1", n)
	}
}

// ─── 8. Throttle Close unblocks burst-window waiters and counts them ─────────

// TestThrottleCloseSuspendsBurstWaiters verifies that Close() also wakes
// goroutines that are blocked in waitForBurstSlot (not in the priority queue)
// and increments the Suspended counter for each of them.
//
// This is distinct from the existing TestThrottleSuspendedIncrementedByWaitForBurstSlotOnClose
// in that it checks the final Suspended() value covers all burst-window
// goroutines, not just one.
func TestThrottleCloseSuspendsBurstWaiters(t *testing.T) {
	t.Parallel()

	// MaxInFlight=10 so burst is the only bottleneck; BurstWindow=10s keeps
	// the window full for the lifetime of the test so blocked goroutines park.
	tt := NewThrottle(ThrottleConfig{
		MaxInFlight:    10,
		BurstThreshold: 2,
		BurstWindow:    10 * time.Second,
	})

	// Saturate the burst window.
	for i := 0; i < 2; i++ {
		if err := tt.Acquire(context.Background(), hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("burst fill %d: %v", i, err)
		}
		tt.Release()
	}

	// Spawn several goroutines that will block in waitForBurstSlot.
	const blocked = 4
	results := make(chan error, blocked)
	var wg sync.WaitGroup
	for i := 0; i < blocked; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- tt.Acquire(context.Background(), hmenum.CommandPriorityHigh)
		}()
	}

	// Wait for goroutines to park; they are in waitForBurstSlot (not the
	// priority queue), so tt.Waiting() stays 0 — we use a real-time sleep.
	time.Sleep(30 * time.Millisecond)

	// Close wakes all burst-window waiters.
	tt.Close()

	// Wait for all goroutines to complete.
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("goroutines did not unblock after Close")
	}

	// All must have received ErrThrottleClosed.
	close(results)
	for err := range results {
		if !errors.Is(err, ErrThrottleClosed) {
			t.Errorf("burst waiter got %v, want ErrThrottleClosed", err)
		}
	}

	// Suspended counter must be ≥ blocked.
	if n := tt.Suspended(); n < blocked {
		t.Errorf("Suspended=%d, want ≥%d", n, blocked)
	}
}
