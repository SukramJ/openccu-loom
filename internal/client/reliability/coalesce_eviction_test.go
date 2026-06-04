// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability

// Item C — Coalescer eviction stress-tests.
//
// These tests drive the Coalescer hard to confirm that the internal calls map
// is always empty once every goroutine has returned (memory-leak guard) and
// that no goroutines are leaked (checked via runtime.NumGoroutine before/after
// the stress run).
//
// Scenarios:
// C1. 1000 goroutines, 100 unique keys, ~10 callers per key.
// C2. All goroutines use the same single key (maximum leader/follower ratio).
// C3. Clear() called concurrently while the stress runs; no goroutine hangs.

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// goroutineLeakThreshold is the maximum number of extra goroutines we tolerate
// after the stress test completes. A non-zero budget accounts for Go's
// internal goroutines (finaliser, GC) that may appear between measurements.
const goroutineLeakThreshold = 5

// eventuallyGoroutineDelta polls runtime.NumGoroutine for up to total
// duration, returning once the delta against baseline drops at or below
// threshold. Required for the stress tests because the Go runtime is
// allowed to keep finaliser / GC / scheduler workers alive briefly
// after wg.Wait returns — a single-shot measurement is racy when other
// tests run in parallel (`go test ./...` triggers many of those). The
// retry loop turns a single moment-in-time observation into a stable
// "settles within budget" assertion.
func eventuallyGoroutineDelta(baseline, threshold int, total time.Duration) int {
	deadline := time.Now().Add(total)
	delta := runtime.NumGoroutine() - baseline
	for time.Now().Before(deadline) && delta > threshold {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
		delta = runtime.NumGoroutine() - baseline
	}
	return delta
}

// ─── C1. 1000 goroutines, 100 unique keys ────────────────────────────────────

// TestCoalescerEvictionStress1000GoroutinesMultiKey fans out 1000 goroutines
// across 100 distinct keys (~10 callers per key). After all goroutines return:
// - the internal calls map must be empty (no entry leaked), and
// - the goroutine count must have returned to within leakThreshold of the
// pre-test baseline (no goroutine leaked).
func TestCoalescerEvictionStress1000GoroutinesMultiKey(t *testing.T) {
	t.Parallel()

	const (
		totalGoroutines = 1000
		uniqueKeys      = 100
	)

	// Force a GC + goroutine schedule stabilisation before sampling.
	runtime.GC()
	time.Sleep(5 * time.Millisecond)
	baselineGoroutines := runtime.NumGoroutine()

	c := NewCoalescer()

	var wg sync.WaitGroup
	wg.Add(totalGoroutines)

	var callCount atomic.Int64

	for i := range totalGoroutines {
		key := fmt.Sprintf("key-%d", i%uniqueKeys)
		go func(k string) {
			defer wg.Done()
			_, _ = c.Do(context.Background(), k, func(_ context.Context) (any, error) {
				callCount.Add(1)
				// A tiny sleep makes it more likely that concurrent callers
				// actually coalesce rather than serialising.
				time.Sleep(time.Millisecond)
				return k + "-result", nil
			})
		}(key)
	}

	wg.Wait()

	// ── post-condition: internal map empty ──────────────────────────────────
	if inFlight := c.InFlight(); inFlight != 0 {
		t.Errorf("InFlight after drain=%d, want 0 (calls map leaked entries)", inFlight)
	}

	// ── post-condition: no goroutine leak ────────────────────────────────────
	if delta := eventuallyGoroutineDelta(baselineGoroutines, goroutineLeakThreshold, 2*time.Second); delta > goroutineLeakThreshold {
		t.Errorf("goroutine delta=%d (baseline=%d) after settle window, want ≤%d — possible leak",
			delta, baselineGoroutines, goroutineLeakThreshold)
	}

	// ── sanity: coalesce must have happened (not all 1000 fns ran) ───────────
	stats := c.Stats()
	if stats.Executed >= totalGoroutines {
		// Not a hard failure — if the scheduler chose not to coalesce it is
		// still correct, but it is useful to know.
		t.Logf("note: no coalescing observed (executed=%d); scheduler may have serialised all calls", stats.Executed)
	}
	if stats.Total != totalGoroutines {
		t.Errorf("Stats.Total=%d, want %d", stats.Total, totalGoroutines)
	}
}

// ─── C2. All goroutines on the same key ──────────────────────────────────────

// TestCoalescerEvictionStressSingleKey sends 200 goroutines all sharing one
// key to maximise the leader/follower ratio. After completion the calls map
// must be empty and goroutines must not leak.
func TestCoalescerEvictionStressSingleKey(t *testing.T) {
	t.Parallel()

	const totalGoroutines = 200

	runtime.GC()
	time.Sleep(5 * time.Millisecond)
	baselineGoroutines := runtime.NumGoroutine()

	c := NewCoalescer()

	// leaderStarted gates followers: the leader signals when it enters fn so
	// followers have time to queue before the leader returns.
	leaderStarted := make(chan struct{})
	leaderOnce := sync.Once{}
	leaderDone := make(chan struct{})

	var executed atomic.Int64
	fn := func(_ context.Context) (any, error) {
		executed.Add(1)
		leaderOnce.Do(func() { close(leaderStarted) })
		<-leaderDone
		return "shared-result", nil
	}

	var wg sync.WaitGroup
	wg.Add(totalGoroutines)
	for range totalGoroutines {
		go func() {
			defer wg.Done()
			_, _ = c.Do(context.Background(), "single-key", fn)
		}()
	}

	// Wait until at least one goroutine has started fn (the leader).
	select {
	case <-leaderStarted:
	case <-time.After(time.Second):
		t.Fatal("leader did not start within 1s")
	}
	// Wait deterministically until all followers have registered (Total
	// reaches totalGoroutines). A fixed sleep is too racy under -race
	// where goroutine scheduling can deprioritise the follower
	// registrations past any reasonable wait window.
	deadline := time.Now().Add(5 * time.Second)
	for c.Stats().Total < uint64(totalGoroutines) {
		if time.Now().After(deadline) {
			t.Fatalf("only %d/%d followers registered within 5s", c.Stats().Total, totalGoroutines)
		}
		time.Sleep(time.Millisecond)
	}

	// Release the leader.
	close(leaderDone)
	wg.Wait()

	// ── post-condition: calls map empty ─────────────────────────────────────
	if inFlight := c.InFlight(); inFlight != 0 {
		t.Errorf("InFlight after drain=%d, want 0", inFlight)
	}

	// ── post-condition: leader/follower pattern confirmed ────────────────────
	// fn should have been called exactly once because all goroutines used the
	// same key; all subsequent callers are followers.
	if n := executed.Load(); n != 1 {
		t.Errorf("fn executed %d times, want 1 (all should coalesce)", n)
	}

	stats := c.Stats()
	if stats.Executed != 1 {
		t.Errorf("Stats.Executed=%d, want 1", stats.Executed)
	}
	if stats.Coalesced != totalGoroutines-1 {
		t.Errorf("Stats.Coalesced=%d, want %d", stats.Coalesced, totalGoroutines-1)
	}

	// ── post-condition: goroutine leak check ─────────────────────────────────
	if delta := eventuallyGoroutineDelta(baselineGoroutines, goroutineLeakThreshold, 2*time.Second); delta > goroutineLeakThreshold {
		t.Errorf("goroutine delta=%d (baseline=%d) after settle window, want ≤%d — possible leak",
			delta, baselineGoroutines, goroutineLeakThreshold)
	}
}

// ─── C3. Clear() concurrent with stress ──────────────────────────────────────

// TestCoalescerEvictionStressWithConcurrentClear runs a moderate stress
// (300 goroutines, 30 keys) while a separate goroutine repeatedly calls
// Clear(). After the stress finishes all goroutines must have returned (no
// goroutine hangs) and the calls map must be empty.
func TestCoalescerEvictionStressWithConcurrentClear(t *testing.T) {
	t.Parallel()

	const (
		totalGoroutines = 300
		uniqueKeys      = 30
	)

	runtime.GC()
	time.Sleep(5 * time.Millisecond)
	baselineGoroutines := runtime.NumGoroutine()

	c := NewCoalescer()

	stopClearer := make(chan struct{})
	clearerDone := make(chan struct{})

	// Clearer goroutine: repeatedly calls Clear() until the stress is done.
	go func() {
		defer close(clearerDone)
		for {
			select {
			case <-stopClearer:
				return
			default:
				c.Clear()
				// Yield so stress goroutines get scheduled.
				runtime.Gosched()
			}
		}
	}()

	var wg sync.WaitGroup
	wg.Add(totalGoroutines)
	for i := range totalGoroutines {
		key := fmt.Sprintf("stress-key-%d", i%uniqueKeys)
		go func(k string) {
			defer wg.Done()
			// Do must never hang — Clear() must unblock any follower.
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = c.Do(ctx, k, func(innerCtx context.Context) (any, error) {
				// Simulate some work.
				select {
				case <-innerCtx.Done():
					return nil, innerCtx.Err()
				case <-time.After(time.Millisecond):
					return "ok", nil
				}
			})
		}(key)
	}

	// Wait for all goroutines with a generous timeout to catch hangs.
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("stress goroutines did not finish within 30s — possible hang after Clear")
	}

	// Stop the clearer.
	close(stopClearer)
	<-clearerDone

	// ── post-condition: calls map empty ─────────────────────────────────────
	if inFlight := c.InFlight(); inFlight != 0 {
		t.Errorf("InFlight after drain=%d, want 0 (map leaked)", inFlight)
	}

	// ── post-condition: goroutine leak check ─────────────────────────────────
	if delta := eventuallyGoroutineDelta(baselineGoroutines, goroutineLeakThreshold, 2*time.Second); delta > goroutineLeakThreshold {
		t.Errorf("goroutine delta=%d (baseline=%d) after settle window, want ≤%d — possible leak",
			delta, baselineGoroutines, goroutineLeakThreshold)
	}
}
