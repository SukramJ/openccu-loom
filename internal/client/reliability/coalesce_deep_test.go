// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability

// P1-1 / P1-2 deep tests for Coalescer: concurrency, hook semantics,
// error propagation, and ctx-cancellation — all deterministic, race-clean.

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// waitAllEntered blocks until all n callers have entered c.Do (Stats().Total
// == n), guaranteeing the n-1 non-leaders coalesce on the in-flight leader.
// Deterministic replacement for a fixed follower-settling sleep; fails fast
// on timeout instead of hanging.
func waitAllEntered(t *testing.T, c *Coalescer, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for c.Stats().Total < uint64(n) {
		if time.Now().After(deadline) {
			t.Fatalf("timeout: only %d/%d callers entered Do", c.Stats().Total, n)
		}
		time.Sleep(time.Millisecond)
	}
}

// ---------------------------------------------------------------------------
// 1. Burst fan-out — inner fn runs exactly once
// ---------------------------------------------------------------------------

// TestCoalescerLeaderRunsOnceForBurst fans out N goroutines on the same key
// and asserts the inner function is called exactly once while every goroutine
// receives the same value.
func TestCoalescerLeaderRunsOnceForBurst(t *testing.T) {
	t.Parallel()

	const N = 50
	c := NewCoalescer()

	// leaderReady gates followers: the leader signals when it has started so
	// all goroutines are truly coalesced before the leader returns.
	leaderReady := make(chan struct{})
	leaderDone := make(chan struct{})

	var callCount atomic.Int64
	fn := func(_ context.Context) (any, error) {
		callCount.Add(1)
		close(leaderReady) // signal: I'm the leader and I've started
		<-leaderDone       // block until released
		return int64(42), nil
	}

	var wg sync.WaitGroup
	results := make([]int64, N)
	errs := make([]error, N)

	for i := range N {
		wg.Go(func() {
			v, err := c.Do(context.Background(), "burst-key", fn)
			errs[i] = err
			if err == nil {
				results[i] = v.(int64) //nolint:forcetypeassert // known concrete type in this test
			}
		})
	}

	// Wait until at least one goroutine has entered the fn (the leader).
	<-leaderReady
	// Wait until all N goroutines have entered Do so every non-leader is
	// guaranteed to coalesce on the in-flight leader.
	waitAllEntered(t, c, N)
	// Release the leader.
	close(leaderDone)

	wg.Wait()

	if got := callCount.Load(); got != 1 {
		t.Fatalf("inner fn called %d times, want exactly 1", got)
	}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: unexpected error: %v", i, err)
		}
		if results[i] != 42 {
			t.Fatalf("goroutine %d: got %d, want 42", i, results[i])
		}
	}
}

// ---------------------------------------------------------------------------
// 2. Distinct keys run independently — no cross-coalescing
// ---------------------------------------------------------------------------

// TestCoalescerDistinctKeysRunIndependently confirms that two goroutines
// using distinct keys each run their own inner fn — there is no piggy-back.
func TestCoalescerDistinctKeysRunIndependently(t *testing.T) {
	t.Parallel()

	c := NewCoalescer()

	var countA, countB atomic.Int64
	fnA := func(_ context.Context) (any, error) { countA.Add(1); return "a", nil }
	fnB := func(_ context.Context) (any, error) { countB.Add(1); return "b", nil }

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); c.Do(context.Background(), "key-a", fnA) }() //nolint:errcheck // error is asserted via channel or atomic in the goroutine above
	go func() { defer wg.Done(); c.Do(context.Background(), "key-b", fnB) }() //nolint:errcheck // error is asserted via channel or atomic in the goroutine above
	wg.Wait()

	if countA.Load() != 1 {
		t.Fatalf("key-a fn call count=%d, want 1", countA.Load())
	}
	if countB.Load() != 1 {
		t.Fatalf("key-b fn call count=%d, want 1", countB.Load())
	}

	s := c.Stats()
	if s.Coalesced != 0 {
		t.Fatalf("coalesced=%d, want 0 (distinct keys never coalesce)", s.Coalesced)
	}
}

// ---------------------------------------------------------------------------
// 3. Error propagation to all waiters
// ---------------------------------------------------------------------------

// TestCoalescerErrorPropagatesToWaiters verifies that when the leader fn
// returns an error every coalesced follower receives that same error.
func TestCoalescerErrorPropagatesToWaiters(t *testing.T) {
	t.Parallel()

	const N = 10
	c := NewCoalescer()

	boom := errors.New("boom")

	leaderReady := make(chan struct{})
	leaderDone := make(chan struct{})

	fn := func(_ context.Context) (any, error) {
		close(leaderReady)
		<-leaderDone
		return nil, boom
	}

	errs := make([]error, N)
	var wg sync.WaitGroup
	for i := range N {
		wg.Go(func() {
			_, err := c.Do(context.Background(), "err-key", fn)
			errs[i] = err
		})
	}

	<-leaderReady
	waitAllEntered(t, c, N)
	close(leaderDone)
	wg.Wait()

	for i, err := range errs {
		if !errors.Is(err, boom) {
			t.Fatalf("goroutine %d: got %v, want boom", i, err)
		}
	}
}

// ---------------------------------------------------------------------------
// 4. Stats accounting — leader + followers
// ---------------------------------------------------------------------------

// TestCoalescerStatsAccountsLeaderAndFollowers checks that after N concurrent
// calls on one key the counters read: Total==N, Executed==1, Coalesced==N-1,
// Failed==0, InFlight==0.
func TestCoalescerStatsAccountsLeaderAndFollowers(t *testing.T) {
	t.Parallel()

	const N = 20
	c := NewCoalescer()

	leaderReady := make(chan struct{})
	leaderDone := make(chan struct{})

	fn := func(_ context.Context) (any, error) {
		close(leaderReady)
		<-leaderDone
		return "x", nil
	}

	var wg sync.WaitGroup
	for range N {
		wg.Go(func() {
			c.Do(context.Background(), "stats-key", fn) //nolint:errcheck // error is asserted via channel or atomic in the goroutine above
		})
	}

	<-leaderReady
	waitAllEntered(t, c, N)
	close(leaderDone)
	wg.Wait()

	s := c.Stats()
	if s.Total != N {
		t.Errorf("Total=%d, want %d", s.Total, N)
	}
	if s.Executed != 1 {
		t.Errorf("Executed=%d, want 1", s.Executed)
	}
	if s.Coalesced != N-1 {
		t.Errorf("Coalesced=%d, want %d", s.Coalesced, N-1)
	}
	if s.Failed != 0 {
		t.Errorf("Failed=%d, want 0", s.Failed)
	}
	if s.InFlight != 0 {
		t.Errorf("InFlight=%d, want 0 after drain", s.InFlight)
	}
}

// ---------------------------------------------------------------------------
// 5. Stats.Failed incremented on fn error
// ---------------------------------------------------------------------------

// TestCoalescerStatsCountsFailures confirms that a single failing call
// increments Stats().Failed to 1.
func TestCoalescerStatsCountsFailures(t *testing.T) {
	t.Parallel()

	c := NewCoalescer()
	fn := func(_ context.Context) (any, error) { return nil, errors.New("fail") }
	c.Do(context.Background(), "fail-key", fn) //nolint:errcheck // error is asserted via channel or atomic in the goroutine above

	if s := c.Stats(); s.Failed != 1 {
		t.Fatalf("Failed=%d, want 1", s.Failed)
	}
}

// ---------------------------------------------------------------------------
// 6. Hook fires per follower, not for leader; waiter counts grow
// ---------------------------------------------------------------------------

// TestCoalescerHookFiresPerFollowerNotLeader installs a hook and spawns 5
// goroutines on the same key. The hook must be called exactly 4 times (once
// per follower) and the set of recorded waiter counts must be {1,2,3,4}.
func TestCoalescerHookFiresPerFollowerNotLeader(t *testing.T) {
	t.Parallel()

	const goroutines = 5
	c := NewCoalescer()

	leaderReady := make(chan struct{})
	leaderDone := make(chan struct{})

	fn := func(_ context.Context) (any, error) {
		close(leaderReady)
		<-leaderDone
		return "ok", nil
	}

	var mu sync.Mutex
	var recordedWaiters []int
	c.SetHook(func(_ string, waiters int) {
		mu.Lock()
		recordedWaiters = append(recordedWaiters, waiters)
		mu.Unlock()
	})

	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			c.Do(context.Background(), "hook-key", fn) //nolint:errcheck // error is asserted via channel or atomic in the goroutine above
		})
	}

	<-leaderReady
	// Wait until all goroutines have entered Do so every non-leader is registered.
	waitAllEntered(t, c, goroutines)
	close(leaderDone)
	wg.Wait()

	mu.Lock()
	got := slices.Clone(recordedWaiters)
	mu.Unlock()

	if len(got) != goroutines-1 {
		t.Fatalf("hook called %d times, want %d", len(got), goroutines-1)
	}

	slices.Sort(got)
	want := []int{1, 2, 3, 4}
	if !slices.Equal(got, want) {
		t.Fatalf("waiter counts (sorted)=%v, want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// 7. Hook may call back into Coalescer without deadlock
// ---------------------------------------------------------------------------

// TestCoalescerHookSafeToCallBackIntoCoalescer verifies that a hook that
// calls c.InFlight() and c.Stats() does not deadlock (hooks run with no lock
// held).
func TestCoalescerHookSafeToCallBackIntoCoalescer(t *testing.T) {
	t.Parallel()

	c := NewCoalescer()

	leaderReady := make(chan struct{})
	leaderDone := make(chan struct{})

	fn := func(_ context.Context) (any, error) {
		close(leaderReady)
		<-leaderDone
		return nil, nil
	}

	hookDone := make(chan struct{}, 1)
	c.SetHook(func(_ string, _ int) {
		_ = c.InFlight()
		_ = c.Stats()
		hookDone <- struct{}{}
	})

	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			c.Do(context.Background(), "reentrant-key", fn) //nolint:errcheck // error is asserted via channel or atomic in the goroutine above
		})
	}

	<-leaderReady
	waitAllEntered(t, c, 2)
	close(leaderDone)
	wg.Wait()

	select {
	case <-hookDone:
		// hook fired without deadlock
	case <-time.After(500 * time.Millisecond):
		t.Fatal("hook did not fire — possible deadlock")
	}
}

// ---------------------------------------------------------------------------
// 8. SetHook replaces an existing hook atomically
// ---------------------------------------------------------------------------

// TestCoalescerSetHookReplacesExisting installs hook A, then replaces it with
// hook B before any followers arrive; followers that call Do after the swap
// must fire hook B only.
//
// Sync protocol:
// 1. Leader goroutine starts and blocks inside fn (signals leaderIn).
// 2. Main goroutine installs hook A, then immediately replaces with hook B.
// 3. Main goroutine signals followersGo.
// 4. Follower goroutines call Do only after followersGo is closed — they
// capture hook B atomically inside the Coalescer lock.
// 5. Leader is released; all goroutines drain.
func TestCoalescerSetHookReplacesExisting(t *testing.T) {
	t.Parallel()

	c := NewCoalescer()

	leaderIn := make(chan struct{})    // leader has entered fn
	followersGo := make(chan struct{}) // hooks swapped; followers may now call Do
	leaderDone := make(chan struct{})  // release the leader

	fn := func(_ context.Context) (any, error) {
		close(leaderIn)
		<-leaderDone
		return "v", nil
	}

	var firedA, firedB atomic.Int64

	// Launch the leader goroutine.
	var wg sync.WaitGroup
	wg.Go(func() {
		c.Do(context.Background(), "replace-key", fn) //nolint:errcheck // error is asserted via channel or atomic in the goroutine above
	})

	// Wait for the leader to be inside fn.
	<-leaderIn

	// Install hook A, then immediately replace with hook B — all before
	// any follower arrives.
	c.SetHook(func(_ string, _ int) { firedA.Add(1) })
	c.SetHook(func(_ string, _ int) { firedB.Add(1) })

	// Now allow followers to call Do. They will all see hook B.
	close(followersGo)

	const followers = 3
	for range followers {
		wg.Go(func() {
			<-followersGo                                 // ensure hook swap is visible before calling Do
			c.Do(context.Background(), "replace-key", fn) //nolint:errcheck // error is asserted via channel or atomic in the goroutine above
		})
	}

	// Wait until all goroutines (1 leader + followers) have entered Do.
	waitAllEntered(t, c, 1+followers)
	close(leaderDone)
	wg.Wait()

	if firedA.Load() != 0 {
		t.Fatalf("hook A fired %d times, want 0 (should have been replaced)", firedA.Load())
	}
	if got := firedB.Load(); got == 0 {
		t.Fatal("hook B was never called")
	}
}

// ---------------------------------------------------------------------------
// 9. SetHook(nil) detaches — no panic, hook not called
// ---------------------------------------------------------------------------

// TestCoalescerSetHookNilDetaches installs a hook, then clears it with
// SetHook(nil); subsequent followers must not panic and the hook must not fire.
func TestCoalescerSetHookNilDetaches(t *testing.T) {
	t.Parallel()

	c := NewCoalescer()

	var fired atomic.Int64
	c.SetHook(func(_ string, _ int) { fired.Add(1) })
	// Detach the hook.
	c.SetHook(nil)

	leaderReady := make(chan struct{})
	leaderDone := make(chan struct{})

	fn := func(_ context.Context) (any, error) {
		close(leaderReady)
		<-leaderDone
		return nil, nil
	}

	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			c.Do(context.Background(), "nil-hook-key", fn) //nolint:errcheck // error is asserted via channel or atomic in the goroutine above
		})
	}

	<-leaderReady
	waitAllEntered(t, c, 3)
	close(leaderDone)
	wg.Wait()

	if fired.Load() != 0 {
		t.Fatalf("detached hook fired %d times, want 0", fired.Load())
	}
}

// ---------------------------------------------------------------------------
// 10. Cancelled follower context returns ctx.Err(); leader still completes
// ---------------------------------------------------------------------------

// TestCoalescerCtxCancelReturnsErrToWaiterOnly verifies that a follower whose
// context is cancelled receives ctx.Err() while the leader still runs to
// completion, reflected in Stats (Executed==1).
func TestCoalescerCtxCancelReturnsErrToWaiterOnly(t *testing.T) {
	t.Parallel()

	c := NewCoalescer()

	leaderReady := make(chan struct{})
	leaderDone := make(chan struct{})

	fn := func(_ context.Context) (any, error) {
		close(leaderReady)
		<-leaderDone
		return "done", nil
	}

	// Leader goroutine — uses a background ctx so cancellation doesn't affect it.
	leaderErrCh := make(chan error, 1)
	go func() {
		_, err := c.Do(context.Background(), "cancel-key", fn)
		leaderErrCh <- err
	}()

	// Wait for leader to be inside fn.
	<-leaderReady

	// Follower with a cancellable context.
	followerCtx, cancel := context.WithCancel(context.Background())
	followerErrCh := make(chan error, 1)
	go func() {
		_, err := c.Do(followerCtx, "cancel-key", fn)
		followerErrCh <- err
	}()

	// Wait until both callers (leader + follower) have entered Do.
	waitAllEntered(t, c, 2)
	cancel() // cancel the follower's context

	// Follower should return quickly with ctx.Err().
	select {
	case err := <-followerErrCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("follower: got %v, want context.Canceled", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("follower did not return after ctx cancel")
	}

	// Release the leader now.
	close(leaderDone)

	if err := <-leaderErrCh; err != nil {
		t.Fatalf("leader: unexpected error: %v", err)
	}

	s := c.Stats()
	if s.Executed != 1 {
		t.Errorf("Executed=%d, want 1", s.Executed)
	}
}

// ---------------------------------------------------------------------------
// 11. Completed key allows a fresh run
// ---------------------------------------------------------------------------

// TestCoalescerCompletedKeyAllowsFreshRun confirms that once a coalesced
// group completes, the next call on the same key starts a brand-new leader
// (Executed grows by 1, Coalesced stays at 0 across both rounds combined).
func TestCoalescerCompletedKeyAllowsFreshRun(t *testing.T) {
	t.Parallel()

	c := NewCoalescer()
	fn := func(_ context.Context) (any, error) { return "v", nil }

	// First call (round 1).
	if _, err := c.Do(context.Background(), "fresh-key", fn); err != nil {
		t.Fatalf("round 1: %v", err)
	}
	s1 := c.Stats()
	if s1.Executed != 1 {
		t.Fatalf("after round 1: Executed=%d, want 1", s1.Executed)
	}

	// Second call (round 2) — must be a new leader, not coalesced with round 1.
	if _, err := c.Do(context.Background(), "fresh-key", fn); err != nil {
		t.Fatalf("round 2: %v", err)
	}
	s2 := c.Stats()
	if s2.Executed != 2 {
		t.Fatalf("after round 2: Executed=%d, want 2", s2.Executed)
	}
	if s2.Coalesced != 0 {
		t.Fatalf("Coalesced=%d, want 0 (sequential calls must not coalesce)", s2.Coalesced)
	}
}

// ---------------------------------------------------------------------------
// 12. InFlight counter during leader run
// ---------------------------------------------------------------------------

// TestCoalescerInFlightDuringLeaderRun asserts InFlight()==1 while the leader
// is executing and InFlight()==0 after the call completes.
func TestCoalescerInFlightDuringLeaderRun(t *testing.T) {
	t.Parallel()

	c := NewCoalescer()

	leaderReady := make(chan struct{})
	inflight := make(chan int, 1)
	leaderDone := make(chan struct{})

	fn := func(_ context.Context) (any, error) {
		close(leaderReady)
		<-leaderDone
		return nil, nil
	}

	var wg sync.WaitGroup
	wg.Go(func() {
		c.Do(context.Background(), "inflight-key", fn) //nolint:errcheck // error is asserted via channel or atomic in the goroutine above
	})

	<-leaderReady

	// Probe InFlight from a separate goroutine while the leader is still running.
	go func() {
		inflight <- c.InFlight()
	}()

	select {
	case n := <-inflight:
		if n != 1 {
			t.Errorf("InFlight during leader run=%d, want 1", n)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out reading InFlight")
	}

	close(leaderDone)
	wg.Wait()

	if n := c.InFlight(); n != 0 {
		t.Errorf("InFlight after drain=%d, want 0", n)
	}
}

// ---------------------------------------------------------------------------
// C14 — Coalescer.Clear() tests
// ---------------------------------------------------------------------------

// TestCoalescerClearReleasesWaiters verifies that a follower waiting in Do
// is unblocked when Clear is called, carrying [ErrCoalescerCleared]: its
// call was abandoned, which must not read as a successful empty result.
func TestCoalescerClearReleasesWaiters(t *testing.T) {
	t.Parallel()

	c := NewCoalescer()
	leaderStarted := make(chan struct{})
	leaderContinue := make(chan struct{})

	// Start a leader that blocks until leaderContinue is closed.
	go func() {
		_, _ = c.Do(context.Background(), "key", func(_ context.Context) (any, error) {
			close(leaderStarted)
			<-leaderContinue
			return "leader-result", nil
		})
	}()

	// Wait until the leader is inside Do.
	<-leaderStarted

	// Start a follower — it should coalesce with the leader.
	followerDone := make(chan struct{})
	var followerVal any
	var followerErr error
	go func() {
		defer close(followerDone)
		followerVal, followerErr = c.Do(context.Background(), "key", func(_ context.Context) (any, error) {
			// Must not run — this is a follower.
			return "should-not-run", nil
		})
	}()

	// Wait until both callers (leader + follower) have entered Do.
	waitAllEntered(t, c, 2)

	// Clear: all waiters must be unblocked.
	c.Clear()

	select {
	case <-followerDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("follower did not unblock after Clear")
	}

	if followerVal != nil || !errors.Is(followerErr, ErrCoalescerCleared) {
		t.Errorf("follower got (%v, %v), want (nil, %v)", followerVal, followerErr, ErrCoalescerCleared)
	}

	// InFlight must be 0 after Clear.
	if n := c.InFlight(); n != 0 {
		t.Errorf("InFlight after Clear=%d, want 0", n)
	}

	// Unblock the leader so goroutines can exit cleanly.
	close(leaderContinue)
}

// TestCoalescerClearOnEmptyIsNoop verifies Clear on an empty coalescer
// is a no-op and leaves the coalescer functional.
func TestCoalescerClearOnEmptyIsNoop(t *testing.T) {
	t.Parallel()
	c := NewCoalescer()
	c.Clear() // must not panic

	val, err := c.Do(context.Background(), "k", func(_ context.Context) (any, error) {
		return 99, nil
	})
	if err != nil || val != 99 {
		t.Errorf("Do after Clear: got (%v, %v), want (99, nil)", val, err)
	}
}

// TestCoalescerLeaderCancellationDoesNotFailFollowers pins that the shared
// call belongs to the group, not to the caller that happened to start it.
//
// Concretely: a REST client aborting its request must not fail an MQTT
// command that coalesced onto the same write. Both asked for the identical
// (priority, address, parameter, value) tuple, the wire call is already in
// flight, and the follower's own context is alive — reporting
// "context canceled" to it would call a write failed that is on its way to
// the CCU.
func TestCoalescerLeaderCancellationDoesNotFailFollowers(t *testing.T) {
	t.Parallel()

	c := NewCoalescer()
	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	defer cancelLeader()

	started := make(chan struct{})
	release := make(chan struct{})
	leaderErr := make(chan error, 1)
	go func() {
		_, err := c.Do(leaderCtx, "setValue|0|ABC:1|STATE|bool|true", func(ctx context.Context) (any, error) {
			close(started)
			<-release
			// The shared call must not have been cancelled with the
			// leader: its own error is what the followers receive.
			return "written", ctx.Err()
		})
		leaderErr <- err
	}()
	<-started

	type outcome struct {
		val any
		err error
	}
	followerOut := make(chan outcome, 1)
	go func() {
		v, err := c.Do(context.Background(), "setValue|0|ABC:1|STATE|bool|true", func(_ context.Context) (any, error) {
			return "should-not-run", nil
		})
		followerOut <- outcome{v, err}
	}()
	waitAllEntered(t, c, 2)

	cancelLeader()

	select {
	case err := <-leaderErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("leader err=%v, want context.Canceled — a caller still returns on its own context", err)
		}
	case <-time.After(time.Second):
		t.Fatal("leader did not return after its own context was cancelled")
	}

	close(release)

	select {
	case got := <-followerOut:
		if got.err != nil {
			t.Fatalf("follower err=%v, want nil — the leader's caller disconnecting must not fail it", got.err)
		}
		if got.val != "written" {
			t.Fatalf("follower val=%v, want %q", got.val, "written")
		}
	case <-time.After(time.Second):
		t.Fatal("follower did not receive the shared call's result")
	}
}

// TestCoalescerCancelsSharedCallWhenEveryCallerLeaves pins the other half of
// the same rule: once nobody is waiting for the result any more, the shared
// call is cancelled instead of running on and occupying the wire.
func TestCoalescerCancelsSharedCallWhenEveryCallerLeaves(t *testing.T) {
	t.Parallel()

	c := NewCoalescer()
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	callCtxDone := make(chan struct{})
	go func() {
		_, _ = c.Do(ctx, "key", func(callCtx context.Context) (any, error) {
			close(started)
			<-callCtx.Done()
			close(callCtxDone)
			return nil, callCtx.Err()
		})
	}()
	<-started

	cancel()

	select {
	case <-callCtxDone:
	case <-time.After(time.Second):
		t.Fatal("the shared call kept running although its last participant had left")
	}
}
